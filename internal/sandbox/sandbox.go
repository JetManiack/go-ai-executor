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
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPathOutsideSandbox = errors.New("path is outside sandbox directory")
	ErrCommandTimeout     = errors.New("command execution timed out")
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
	RootDir           string
	DefaultTimeout    time.Duration
	MaxOutputBytes    int
	Shell             string
	AllowedEnvs       []string
	StreamBufferBytes int
}

// Sandbox is one agent's jailed directory and the commands running in it.
type Sandbox struct {
	id     string
	bus    *Broadcaster
	config Config

	mu sync.RWMutex
	// running maps an execution ID to the process group leading its process
	// tree, so an administrator can kill everything this sandbox is running
	// without waiting for it to finish.
	running map[string]int
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
	RootDir        string   `json:"root_dir"`
	DefaultTimeout string   `json:"default_timeout"`
	MaxOutputBytes int      `json:"max_output_bytes"`
	Shell          string   `json:"shell"`
	AllowedEnvs    []string `json:"allowed_envs"`
	RunningCommand int      `json:"running_commands"`
}

func DefaultConfig(rootDir string) Config {
	if rootDir == "" {
		rootDir = "./scratch"
	}
	return Config{
		RootDir:           rootDir,
		DefaultTimeout:    30 * time.Second,
		MaxOutputBytes:    512 << 10,
		Shell:             "/bin/sh",
		StreamBufferBytes: DefaultStreamBufferBytes,
		AllowedEnvs: []string{
			"PATH=/usr/bin:/bin:/usr/local/bin:/usr/sbin:/sbin",
			"LANG=C.UTF-8",
			"LC_ALL=C.UTF-8",
		},
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
	if cfg.Shell == "" {
		cfg.Shell = "/bin/sh"
	}
	cfg.RootDir = absRoot

	return &Sandbox{id: id, bus: bus, config: cfg, running: make(map[string]int)}, nil
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

// ExecCommand runs command in the sandbox, publishing its output to watchers as
// it is produced and returning the (possibly truncated) whole of it once the
// command exits.
func (s *Sandbox) ExecCommand(ctx context.Context, command string, timeout time.Duration, workDir string) (ExecResult, error) {
	s.mu.RLock()
	cfg := s.config
	s.mu.RUnlock()

	if timeout <= 0 {
		timeout = cfg.DefaultTimeout
	}

	targetWorkDir := cfg.RootDir
	if workDir != "" {
		resolved, err := s.ResolvePath(workDir)
		if err != nil {
			return ExecResult{}, fmt.Errorf("invalid working directory: %w", err)
		}
		targetWorkDir = resolved
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	execID := uuid.NewString()
	cmd := exec.CommandContext(execCtx, cfg.Shell, "-c", command) //nolint:gosec // running an arbitrary command is this service's purpose; containment is the sandbox root, the scrubbed environment and the process group, not command inspection
	cmd.Dir = targetWorkDir
	cmd.Env = append(append([]string{}, cfg.AllowedEnvs...),
		"HOME="+cfg.RootDir,
		"PWD="+targetWorkDir,
	)
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
	if err != nil {
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

	s.trackRunning(execID, cmd.Process.Pid)
	defer s.untrackRunning(execID)

	s.publish(Event{
		Kind:    EventStarted,
		ExecID:  execID,
		Command: command,
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

	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
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

func (s *Sandbox) trackRunning(execID string, pid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running[execID] = pid
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

// KillAll SIGKILLs the process group of every command running in this sandbox
// and returns how many it signalled.
//
// The kill is reported to watchers before it lands, so the terminal shows why its
// output stopped. Entries are not removed here: each ExecCommand call untracks
// its own execution when Wait returns, which is the only place that knows the
// process is really gone.
func (s *Sandbox) KillAll(byActor, reason string) (int, error) {
	s.mu.RLock()
	pids := make(map[string]int, len(s.running))
	for execID, pid := range s.running {
		pids[execID] = pid
	}
	s.mu.RUnlock()

	var killed int
	var errs []error
	for execID, pid := range pids {
		s.publish(Event{
			Kind:    EventKilled,
			ExecID:  execID,
			ByActor: byActor,
			Reason:  reason,
		})
		if err := killProcessGroup(pid); err != nil {
			// ESRCH means it exited between the snapshot and the signal, which
			// is success as far as the caller is concerned.
			if errors.Is(err, os.ErrProcessDone) || errors.Is(err, errProcessNotFound) {
				continue
			}
			errs = append(errs, fmt.Errorf("kill process group %d: %w", pid, err))
			continue
		}
		killed++
	}
	return killed, errors.Join(errs...)
}

func (s *Sandbox) ReadFile(relPath string) ([]byte, error) {
	absPath, err := s.ResolvePath(relPath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(absPath)
}

func (s *Sandbox) WriteFile(relPath string, data []byte, perm os.FileMode) error {
	absPath, err := s.ResolvePath(relPath)
	if err != nil {
		return err
	}
	if perm == 0 {
		perm = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o750); err != nil {
		return fmt.Errorf("create directory structure: %w", err)
	}
	return os.WriteFile(absPath, data, perm)
}

func (s *Sandbox) ListDir(relPath string) ([]FileInfo, error) {
	absPath, err := s.ResolvePath(relPath)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(absPath)
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
		entryRelPath, err := filepath.Rel(s.config.RootDir, filepath.Join(absPath, entry.Name()))
		if err != nil {
			entryRelPath = entry.Name()
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

func (s *Sandbox) DeleteFile(relPath string) error {
	absPath, err := s.ResolvePath(relPath)
	if err != nil {
		return err
	}
	if absPath == s.config.RootDir {
		return errors.New("cannot delete the sandbox root directory")
	}
	return os.RemoveAll(absPath)
}

func (s *Sandbox) GetStatus() SandboxStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return SandboxStatus{
		RootDir:        s.config.RootDir,
		DefaultTimeout: s.config.DefaultTimeout.String(),
		MaxOutputBytes: s.config.MaxOutputBytes,
		Shell:          s.config.Shell,
		AllowedEnvs:    s.config.AllowedEnvs,
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
