// Package sandbox gives each agent a jailed directory it can read, write and
// run shell commands in, streams that command output to watching humans as it
// happens, and lets an administrator tear the whole process tree down.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPathOutsideSandbox = errors.New("path is outside sandbox directory")
	ErrCommandTimeout     = errors.New("command execution timed out")

	// ErrCommandStopped reports a command cut short by cancellation — an
	// administrator stopping the sandbox, or the calling agent disconnecting —
	// as opposed to running out of time.
	ErrCommandStopped = errors.New("command execution was stopped")

	// ErrEmptyProgram reports a call with no program to run.
	ErrEmptyProgram = errors.New("a program to execute is required")
)

// chunkSize is how much output is read per syscall before being published as one
// event. Small enough that a watcher sees progress on a chatty command promptly,
// large enough not to publish an event per line of a fast loop.
const chunkSize = 16 << 10

// waitDelay bounds how long Wait blocks after cancellation before giving up on
// the output pipes. Killing the process group normally closes them, but a
// grandchild that escaped into its own session can hold one open forever, and
// without this the calling tool never returns.
const waitDelay = 2 * time.Second

type Config struct {
	RootDir        string
	DefaultTimeout time.Duration
	MaxOutputBytes int

	// EnvPassthrough names variables inherited from the server's environment;
	// ExtraEnv holds literal KEY=VALUE entries. Everything else is dropped — the
	// server's environment is where this service's own database DSN and session
	// key live, so a command must not simply inherit it.
	EnvPassthrough []string
	ExtraEnv       []string

	StreamBufferBytes int
}

// Sandbox is one agent's jailed directory and the commands running in it.
type Sandbox struct {
	id     string
	bus    *Broadcaster
	config Config

	// root confines every file operation to the sandbox directory. A string
	// check on the path is not enough: an agent can create a symlink inside its
	// own sandbox (nothing stops `ln -s /etc/passwd link`), and that path passes
	// any purely lexical containment test while os.ReadFile follows it straight
	// out. os.Root refuses to traverse a link that leaves the root, in the
	// kernel rather than in our arithmetic.
	//
	// Never closed: a sandbox lives as long as the process, and closing the
	// root would break every subsequent call for that agent.
	root *os.Root

	mu sync.RWMutex
	// running maps an execution ID to the function that cancels its context.
	//
	// Cancelling is how a command is stopped, rather than signalling a recorded
	// PID: exec.Cmd's own cancellation path then runs, which is where the
	// process-group teardown lives, and WaitDelay bounds how long Wait may block
	// on output pipes a survivor is holding. One place decides how a command
	// dies, and it is the same place for a timeout and for an operator.
	running map[string]context.CancelFunc
}

type ExecResult struct {
	ExecID     string `json:"exec_id"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	Truncated  bool   `json:"truncated"`
}

type FileInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

type SandboxStatus struct {
	RootDir        string `json:"root_dir"`
	DefaultTimeout string `json:"default_timeout"`
	MaxOutputBytes int    `json:"max_output_bytes"`

	// EnvNames lists the variable names a command is given, without their
	// values: an agent needs to know whether PATH or a locale is set, and has no
	// business being handed the contents through a status call.
	EnvNames []string `json:"env_names"`

	RunningCommand int `json:"running_commands"`
}

func DefaultConfig(rootDir string) Config {
	if rootDir == "" {
		rootDir = "./scratch"
	}
	return Config{
		RootDir:           rootDir,
		DefaultTimeout:    30 * time.Second,
		MaxOutputBytes:    512 << 10,
		StreamBufferBytes: DefaultStreamBufferBytes,
		EnvPassthrough:    slices.Clone(DefaultEnvPassthrough),
	}
}

// New creates a standalone sandbox rooted at cfg.RootDir. The Manager is the
// usual constructor; this is for tests and for a single-tenant sandbox with no
// event bus.
func New(cfg Config) (*Sandbox, error) {
	return newSandbox("", cfg, nil)
}

func newSandbox(id string, cfg Config, bus *Broadcaster) (*Sandbox, error) {
	if cfg.RootDir == "" {
		return nil, errors.New("root directory cannot be empty")
	}

	absRoot, err := filepath.Abs(cfg.RootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o750); err != nil {
		return nil, fmt.Errorf("create sandbox root directory: %w", err)
	}
	// Resolve symlinks on the root itself so ResolvePath compares like with
	// like: without this, a root reached through a symlink makes every
	// containment check compare a resolved path against an unresolved prefix.
	if evalRoot, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = evalRoot
	}

	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 30 * time.Second
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 512 << 10
	}
	if err := ValidateEnvPassthrough(cfg.EnvPassthrough); err != nil {
		return nil, err
	}
	if cfg.EnvPassthrough == nil {
		cfg.EnvPassthrough = slices.Clone(DefaultEnvPassthrough)
	}
	cfg.RootDir = absRoot

	root, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, fmt.Errorf("open sandbox root: %w", err)
	}

	return &Sandbox{id: id, bus: bus, config: cfg, root: root, running: make(map[string]context.CancelFunc)}, nil
}

// relativeName turns a caller-supplied path into a name relative to the sandbox
// root, which is what os.Root's methods take.
//
// An absolute path is accepted only if it already points inside the root, for
// callers echoing back a path this package handed them. os.Root would reject an
// escaping name on its own; the explicit check here is what makes the error say
// so in this package's own terms.
func (s *Sandbox) relativeName(relPath string) (string, error) {
	cleaned := filepath.Clean(relPath)
	if cleaned == "" || cleaned == "." {
		return ".", nil
	}

	if filepath.IsAbs(cleaned) {
		rel, err := filepath.Rel(s.config.RootDir, cleaned)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrPathOutsideSandbox, err)
		}
		cleaned = rel
	}

	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideSandbox, relPath)
	}
	return cleaned, nil
}

// ID returns the actor ID this sandbox belongs to, empty for a standalone one.
func (s *Sandbox) ID() string { return s.id }

func (s *Sandbox) ResolvePath(relPath string) (string, error) {
	s.mu.RLock()
	root := s.config.RootDir
	s.mu.RUnlock()

	cleaned := filepath.Clean(relPath)
	if cleaned == "." || cleaned == "" {
		return root, nil
	}

	target := cleaned
	if !filepath.IsAbs(cleaned) {
		target = filepath.Join(root, cleaned)
	}

	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrPathOutsideSandbox, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideSandbox, relPath)
	}
	return target, nil
}

// resolveWorkDir returns the absolute directory a command should run in.
//
// exec needs a path, not a name in a root, so this cannot go through os.Root the
// way the file operations do — it resolves the symlink chain itself and then
// checks containment against the (already symlink-resolved) sandbox root.
//
// Note what this does and does not buy: exec_command runs an arbitrary shell
// command as the server's user, so it is not confined by the sandbox at all and
// can read anything that user can. Keeping the working directory inside the
// sandbox is about the tools behaving as documented, not about containment —
// which the README states plainly.
func (s *Sandbox) resolveWorkDir(workDir string) (string, error) {
	name, err := s.relativeName(workDir)
	if err != nil {
		return "", err
	}

	absPath := filepath.Join(s.config.RootDir, name)
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// A working directory that does not exist yet is a plain user error; say
		// so rather than reporting it as an escape.
		return "", fmt.Errorf("%w: %v", ErrPathOutsideSandbox, err)
	}

	rel, err := filepath.Rel(s.config.RootDir, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideSandbox, workDir)
	}
	return resolved, nil
}

// ExecCommand runs program with args in the sandbox, publishing its output to
// watchers as it is produced and returning the (possibly truncated) whole of it
// once it exits.
//
// The program is executed directly. There is no shell between the caller and the
// process, so `&&`, pipes, redirections and globs are not interpreted and an
// argument containing them is passed through as the literal text it is. That
// makes the tool's contract unambiguous — one program, one argument vector — and
// removes a layer that silently reinterpreted every string handed to it.
//
// It is not a security boundary, and this is worth stating plainly: if a shell is
// installed, an agent can still run `/bin/sh` with `-c` as its arguments. What
// bounds a command is the deployment (see the README), not the absence of a
// shell here.
func (s *Sandbox) ExecCommand(ctx context.Context, program string, args []string, timeout time.Duration, workDir string) (ExecResult, error) {
	s.mu.RLock()
	cfg := s.config
	s.mu.RUnlock()

	if timeout <= 0 {
		timeout = cfg.DefaultTimeout
	}

	targetWorkDir := cfg.RootDir
	if workDir != "" {
		resolved, err := s.resolveWorkDir(workDir)
		if err != nil {
			return ExecResult{}, fmt.Errorf("invalid working directory: %w", err)
		}
		targetWorkDir = resolved
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	env := buildEnv(cfg, targetWorkDir)

	// Resolved against the PATH the command will actually run with, not the
	// server's: exec.Command would consult os.Getenv("PATH"), so a PATH supplied
	// through --env would be advertised to the command and then ignored when
	// finding the program.
	resolved, err := lookProgram(program, env)
	if err != nil {
		return ExecResult{}, err
	}

	execID := uuid.NewString()
	// Executing a caller-supplied program is precisely this service's purpose, so
	// there is nothing here to sanitize. Note what did change with the move away
	// from `$SHELL -c`: the arguments are no longer a string somebody else will
	// reinterpret, so injection through quoting or metacharacters is not
	// expressible — an argument is the literal bytes it is. What bounds the
	// command is the deployment (see the README), not inspection of its argv.
	// #nosec G204 -- see above
	cmd := exec.CommandContext(execCtx, resolved, args...) //nolint:gosec // deliberate: see the comment above
	cmd.Dir = targetWorkDir
	cmd.Env = env
	setProcessGroup(cmd)
	// Cancel the whole process group, not just the shell: this is what makes a
	// timeout collect backgrounded children instead of orphaning them.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return killProcessGroup(cmd.Process.Pid)
	}
	cmd.WaitDelay = waitDelay

	stdout, err := cmd.StdoutPipe()
	if err != nil { //nolint:dupl // the stderr pipe below reads the same by necessity
		return ExecResult{ExecID: execID}, fmt.Errorf("open stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ExecResult{ExecID: execID}, fmt.Errorf("open stderr pipe: %w", err)
	}

	started := time.Now()
	if err := cmd.Start(); err != nil {
		return ExecResult{ExecID: execID}, fmt.Errorf("start command: %w", err)
	}

	s.trackRunning(execID, cancel)
	defer s.untrackRunning(execID)

	s.publish(Event{
		Kind:    EventStarted,
		ExecID:  execID,
		Command: commandLine(program, args),
		WorkDir: workDir,
	})

	stdoutBuf := &limitedBuffer{limit: cfg.MaxOutputBytes}
	stderrBuf := &limitedBuffer{limit: cfg.MaxOutputBytes}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.pump(stdout, EventStdout, execID, stdoutBuf) }()
	go func() { defer wg.Done(); s.pump(stderr, EventStderr, execID, stderrBuf) }()
	// Both pipes must reach EOF before Wait, which closes them.
	wg.Wait()

	waitErr := cmd.Wait()
	durationMs := time.Since(started).Milliseconds()
	truncated := stdoutBuf.truncated || stderrBuf.truncated

	result := ExecResult{
		ExecID:     execID,
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
		DurationMs: durationMs,
		Truncated:  truncated,
	}

	switch {
	case errors.Is(execCtx.Err(), context.DeadlineExceeded):
		result.ExitCode = -1
		result.Stderr += "\n[command timed out]"
		s.publish(Event{
			Kind:       EventFinished,
			ExecID:     execID,
			ExitCode:   -1,
			DurationMs: durationMs,
			Truncated:  truncated,
			Reason:     "timed out after " + timeout.String(),
		})
		return result, ErrCommandTimeout

	case errors.Is(execCtx.Err(), context.Canceled):
		// Either an administrator stopped the sandbox or the caller went away.
		// Both are distinct from a timeout, and reporting them as one would tell
		// an operator the command ran out of time when they had just stopped it.
		result.ExitCode = -1
		result.Stderr += "\n[command stopped]"
		s.publish(Event{
			Kind:       EventFinished,
			ExecID:     execID,
			ExitCode:   -1,
			DurationMs: durationMs,
			Truncated:  truncated,
			Reason:     "stopped before completion",
		})
		return result, ErrCommandStopped
	}

	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
	}

	s.publish(Event{
		Kind:       EventFinished,
		ExecID:     execID,
		ExitCode:   result.ExitCode,
		DurationMs: durationMs,
		Truncated:  truncated,
	})
	return result, nil
}

// pump copies from r, appending to sink and publishing each chunk as an event of
// kind, until r reaches EOF.
//
// Publishing continues past the point where sink stops accepting: sink bounds
// what the agent gets back in one tool result, while the live terminal is meant
// to keep showing what is happening. Memory stays bounded by the ring buffer,
// which evicts, rather than by refusing to read.
func (s *Sandbox) pump(r io.Reader, kind EventKind, execID string, sink *limitedBuffer) {
	buf := make([]byte, chunkSize)
	var carry []byte

	for {
		n, err := r.Read(buf)
		if n > 0 {
			_, _ = sink.Write(buf[:n])

			// Built fresh rather than appended onto carry, so the tail below
			// can't alias an array the next read overwrites.
			chunk := make([]byte, 0, len(carry)+n)
			chunk = append(chunk, carry...)
			chunk = append(chunk, buf[:n]...)

			emit, tail := splitCompleteRunes(chunk)
			carry = tail
			if len(emit) > 0 {
				s.publish(Event{Kind: kind, ExecID: execID, Data: string(emit)})
			}
		}
		if err != nil {
			// Whatever is left is either a genuinely malformed tail or a rune
			// the process never finished writing; emit it rather than lose it,
			// and let the JSON encoder substitute U+FFFD.
			if len(carry) > 0 {
				s.publish(Event{Kind: kind, ExecID: execID, Data: string(carry)})
			}
			return
		}
	}
}

// publish sends e to this sandbox's watchers, filling in the fields every event
// shares. A sandbox with no bus (a standalone one) simply has no watchers.
func (s *Sandbox) publish(e Event) {
	if s.bus == nil {
		return
	}
	e.SandboxID = s.id
	e.At = time.Now().UTC()
	s.bus.Publish(e)
}

func (s *Sandbox) trackRunning(execID string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running[execID] = cancel
}

func (s *Sandbox) untrackRunning(execID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, execID)
}

// RunningCommands reports how many commands are executing in this sandbox.
func (s *Sandbox) RunningCommands() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.running)
}

// KillAll stops every command running in this sandbox and returns how many it
// stopped.
//
// It cancels each command's context rather than signalling a PID directly. The
// cancellation runs exec.Cmd's own Cancel hook, which tears down the whole
// process group — so a backgrounded child cannot outlive the stop — and
// WaitDelay bounds how long Wait blocks if something is still holding an output
// pipe. Signalling requires no privilege: a process may signal another with the
// same UID, and these are its own children.
//
// The kill is reported to watchers before it lands, so the terminal shows why its
// output stopped. Entries are not removed here: each ExecCommand call untracks
// its own execution when Wait returns, which is the only place that knows the
// process is really gone.
func (s *Sandbox) KillAll(byActor, reason string) (int, error) {
	s.mu.RLock()
	stopping := make(map[string]context.CancelFunc, len(s.running))
	for execID, cancel := range s.running {
		stopping[execID] = cancel
	}
	s.mu.RUnlock()

	for execID, cancel := range stopping {
		s.publish(Event{
			Kind:    EventKilled,
			ExecID:  execID,
			ByActor: byActor,
			Reason:  reason,
		})
		cancel()
	}
	return len(stopping), nil
}

func (s *Sandbox) ReadFile(relPath string) ([]byte, error) {
	name, err := s.relativeName(relPath)
	if err != nil {
		return nil, err
	}
	return s.root.ReadFile(name)
}

func (s *Sandbox) WriteFile(relPath string, data []byte, perm os.FileMode) error {
	name, err := s.relativeName(relPath)
	if err != nil {
		return err
	}
	if perm == 0 {
		perm = 0o644
	}
	if dir := filepath.Dir(name); dir != "." {
		if err := s.root.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create directory structure: %w", err)
		}
	}
	return s.root.WriteFile(name, data, perm)
}

func (s *Sandbox) ListDir(relPath string) ([]FileInfo, error) {
	name, err := s.relativeName(relPath)
	if err != nil {
		return nil, err
	}

	dir, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}

	results := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			// The entry was removed between ReadDir and Info; it is simply no
			// longer part of the listing.
			continue
		}
		entryRelPath := entry.Name()
		if name != "." {
			entryRelPath = filepath.Join(name, entry.Name())
		}
		results = append(results, FileInfo{
			Name:    entry.Name(),
			Path:    entryRelPath,
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	return results, nil
}

// DeleteResult describes what a delete actually removed.
//
// RemoveAll succeeds on a path that was never there, so without this an agent
// cannot tell "deleted" from "there was nothing to delete" — and cannot tell that
// it just removed a whole subtree rather than one file.
type DeleteResult struct {
	Existed      bool `json:"existed"`
	WasDirectory bool `json:"was_directory"`
}

func (s *Sandbox) DeleteFile(relPath string) (DeleteResult, error) {
	name, err := s.relativeName(relPath)
	if err != nil {
		return DeleteResult{}, err
	}
	if name == "." {
		return DeleteResult{}, errors.New("cannot delete the sandbox root directory")
	}

	// Lstat, not Stat: a symlink is deleted as a link, so what matters is what
	// the entry itself is, not what it points at.
	var result DeleteResult
	if info, statErr := s.root.Lstat(name); statErr == nil {
		result.Existed = true
		result.WasDirectory = info.IsDir()
	}

	if err := s.root.RemoveAll(name); err != nil {
		return DeleteResult{}, err
	}
	return result, nil
}

func (s *Sandbox) GetStatus() SandboxStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return SandboxStatus{
		RootDir:        s.config.RootDir,
		DefaultTimeout: s.config.DefaultTimeout.String(),
		MaxOutputBytes: s.config.MaxOutputBytes,
		EnvNames:       envNames(buildEnv(s.config, s.config.RootDir)),
		RunningCommand: len(s.running),
	}
}

// limitedBuffer accumulates up to limit bytes and reports whether anything was
// dropped. Writes past the limit are reported as accepted so the producer keeps
// running: the goal is to bound what is returned, not to break the command's
// stdout with a short write.
type limitedBuffer struct {
	buf       []byte
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - len(b.buf)
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf = append(b.buf, p[:remaining]...)
		b.truncated = true
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// String returns the accumulated output, dropping a character the byte limit cut
// in half.
func (b *limitedBuffer) String() string {
	if b.truncated {
		return string(trimIncompleteRune(b.buf))
	}
	return string(b.buf)
}
