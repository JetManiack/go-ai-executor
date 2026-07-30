// Package localexec runs shell commands for the local stdio helper: in the
// directory the operator started it from, through their own shell, with no
// sandbox and no confinement.
//
// This is deliberately the opposite of internal/sandbox, and the difference is a
// difference in who is being served. The server is multi-tenant: several agents
// share one process, so each gets a jailed directory, an allowlisted environment
// and an argument vector with no shell to reinterpret it. Here there is one user,
// running this on their own machine, and the agent acts with exactly their
// authority — the same authority it would have if they pasted the command into
// their own terminal. Confinement would buy nothing it could not trivially step
// around, so it is not pretended at; convenience wins instead, which is why the
// command is one string handed to $SHELL.
//
// What it does keep from the sandboxed path is the mechanics that are about
// correctness rather than containment: the whole process tree is torn down on a
// timeout, output is bounded, and a character is never cut in half.
package localexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/JetManiack/go-ai-executor/internal/procexec"
)

// Defaults chosen for a developer's machine rather than a shared server: local
// work is builds and test suites, which take minutes and print a lot.
const (
	DefaultTimeout        = 2 * time.Minute
	DefaultMaxOutputBytes = 1 << 20 // 1 MiB
	FallbackShell         = "/bin/sh"
)

// ErrCommandTimeout reports a command killed for exceeding its timeout.
var ErrCommandTimeout = errors.New("command execution timed out")

// Config is the whole configuration of the local runner.
type Config struct {
	// Shell runs the command line; empty means $SHELL, then FallbackShell.
	Shell string

	// Dir is the working directory; empty means the process's own, which is the
	// directory the operator started the helper in.
	Dir string

	DefaultTimeout time.Duration
	MaxOutputBytes int
}

// Runner executes command lines. It holds no state beyond its configuration, so
// it is safe for concurrent use.
type Runner struct {
	cfg Config
}

// Result is one command's outcome.
type Result struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	Truncated  bool   `json:"truncated"`
}

// New resolves cfg's defaults and returns a Runner.
func New(cfg Config) (*Runner, error) {
	if cfg.Shell == "" {
		cfg.Shell = os.Getenv("SHELL")
	}
	if cfg.Shell == "" {
		cfg.Shell = FallbackShell
	}

	if cfg.Dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("determine the working directory: %w", err)
		}
		cfg.Dir = wd
	}
	absDir, err := filepath.Abs(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", cfg.Dir, err)
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("working directory %q: %w", absDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("working directory %q is not a directory", absDir)
	}
	cfg.Dir = absDir

	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = DefaultTimeout
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = DefaultMaxOutputBytes
	}

	return &Runner{cfg: cfg}, nil
}

// Shell returns the shell command lines are run with.
func (r *Runner) Shell() string { return r.cfg.Shell }

// Dir returns the working directory command lines run in.
func (r *Runner) Dir() string { return r.cfg.Dir }

// DefaultTimeout returns the timeout applied when a caller sets none.
func (r *Runner) DefaultTimeout() time.Duration { return r.cfg.DefaultTimeout }

// MaxOutputBytes returns the per-stream output cap.
func (r *Runner) MaxOutputBytes() int { return r.cfg.MaxOutputBytes }

// Run executes command through the shell and returns its output.
//
// workDir, when set, is resolved relative to the configured directory; an
// absolute path is taken as given. It is not checked for containment, because
// there is nothing here to contain it to — see the package comment.
func (r *Runner) Run(ctx context.Context, command string, timeout time.Duration, workDir string) (Result, error) {
	if command == "" {
		return Result{}, errors.New("command cannot be empty")
	}
	if timeout <= 0 {
		timeout = r.cfg.DefaultTimeout
	}

	dir := r.cfg.Dir
	if workDir != "" {
		if filepath.IsAbs(workDir) {
			dir = workDir
		} else {
			dir = filepath.Join(r.cfg.Dir, workDir)
		}
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// The command line goes to the shell as one string, which is the interface
	// this helper exists to provide: locally the agent already has the operator's
	// authority, so pipes and redirection are convenience with nothing to protect.
	// #nosec G204 -- see above and the package comment
	cmd := exec.CommandContext(execCtx, r.cfg.Shell, "-c", command) //nolint:gosec // deliberate: see the comment above
	cmd.Dir = dir

	// The operator's full environment, unlike the server's allowlist: this process
	// holds no credentials of its own, and a command an operator would have run in
	// their own terminal should see what their terminal sees.
	cmd.Env = os.Environ()

	procexec.Configure(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return procexec.KillGroup(cmd.Process.Pid)
	}
	cmd.WaitDelay = procexec.WaitDelay

	stdout := procexec.NewCappedBuffer(r.cfg.MaxOutputBytes)
	stderr := procexec.NewCappedBuffer(r.cfg.MaxOutputBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	started := time.Now()
	waitErr := cmd.Run()
	durationMs := time.Since(started).Milliseconds()

	result := Result{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: durationMs,
		Truncated:  stdout.Truncated() || stderr.Truncated(),
	}

	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		result.ExitCode = -1
		result.Stderr += "\n[command timed out]"
		return result, ErrCommandTimeout
	}

	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			// A non-zero exit is the command reporting failure, not this call
			// failing: the caller gets the code and the output, without an error.
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, fmt.Errorf("run command: %w", waitErr)
	}

	return result, nil
}
