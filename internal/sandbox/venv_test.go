package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newVenvSandbox builds a sandbox with a Python environment configured.
func newVenvSandbox(t *testing.T) *Sandbox {
	t.Helper()

	cfg := DefaultConfig(t.TempDir())
	cfg.VenvDir = DefaultVenvDir
	cfg.DefaultTimeout = 3 * time.Minute
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sb
}

func requirePython(t *testing.T) {
	t.Helper()

	for _, name := range []string{"python3", "python"} {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	t.Skip("no Python interpreter on PATH")
}

// TestActivationIsTheEnvironmentRatherThanAScript is the point of the design:
// there is no shell to source `activate` in, so the sandbox sets what activate
// would have set. A command an agent runs is therefore always inside the
// environment, rather than inside it only when the agent remembered.
func TestActivationIsTheEnvironmentRatherThanAScript(t *testing.T) {
	root := t.TempDir()
	venvBin := filepath.Join(root, ".venv", "bin")

	env := buildEnv(Config{
		RootDir:        root,
		EnvPassthrough: []string{"PATH"},
	}, root, venvBin)

	var path, virtualEnv string
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			path = value
		}
		if value, ok := strings.CutPrefix(entry, "VIRTUAL_ENV="); ok {
			virtualEnv = value
		}
	}

	// Prepended, not appended: an interpreter in the environment has to win over
	// the one in the image, or the environment is decorative.
	if !strings.HasPrefix(path, venvBin+string(os.PathListSeparator)) {
		t.Errorf("PATH = %q, want it to start with %q", path, venvBin)
	}
	if want := filepath.Join(root, ".venv"); virtualEnv != want {
		t.Errorf("VIRTUAL_ENV = %q, want %q", virtualEnv, want)
	}
}

func TestTheWorkersOwnVirtualEnvIsNotInherited(t *testing.T) {
	// A worker started from inside someone's virtual environment would otherwise
	// aim every agent's Python at a directory the agent cannot see.
	t.Setenv("VIRTUAL_ENV", "/home/operator/some-project/.venv")

	if err := ValidateEnvPassthrough([]string{"VIRTUAL_ENV"}); err == nil {
		t.Error("VIRTUAL_ENV was accepted as a passthrough variable")
	}

	root := t.TempDir()
	env := buildEnv(Config{RootDir: root, EnvPassthrough: []string{"VIRTUAL_ENV"}}, root, "")
	for _, entry := range env {
		if strings.HasPrefix(entry, "VIRTUAL_ENV=") {
			t.Errorf("VIRTUAL_ENV survived into the command environment as %q", entry)
		}
	}
}

func TestTheEnvironmentIsCreatedOnFirstUse(t *testing.T) {
	requirePython(t)
	sb := newVenvSandbox(t)

	// Nothing is provisioned until an agent actually runs something: creating it
	// when the sandbox is registered would hold the manager's lock for the length
	// of a venv build, blocking every other agent's first call.
	if _, err := os.Stat(filepath.Join(sb.config.RootDir, DefaultVenvDir)); !os.IsNotExist(err) {
		t.Fatalf("the environment exists before any command ran: %v", err)
	}

	res, err := sb.ExecCommand(context.Background(), "python", []string{"-c", "import sys; print(sys.prefix)"},
		3*time.Minute, "")
	if err != nil {
		t.Fatalf("ExecCommand: %v (stderr %q)", err, res.Stderr)
	}

	// `python` resolved to the environment's interpreter, which is what the PATH
	// order is for, and sys.prefix proves it is the environment rather than the
	// system one.
	if want := filepath.Join(sb.config.RootDir, DefaultVenvDir); !strings.Contains(res.Stdout, want) {
		t.Errorf("sys.prefix = %q, want the sandbox's environment %q", strings.TrimSpace(res.Stdout), want)
	}
}

func TestTheEnvironmentIsCreatedOnlyOnce(t *testing.T) {
	requirePython(t)
	sb := newVenvSandbox(t)

	first, err := sb.ExecCommand(context.Background(), "python", []string{"-c", "print(1)"}, 3*time.Minute, "")
	if err != nil {
		t.Fatalf("first: %v (stderr %q)", err, first.Stderr)
	}
	marker := filepath.Join(sb.config.RootDir, DefaultVenvDir, "created-by-the-test")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write the marker: %v", err)
	}

	if _, err := sb.ExecCommand(context.Background(), "python", []string{"-c", "print(2)"}, 3*time.Minute, ""); err != nil {
		t.Fatalf("second: %v", err)
	}

	// A rebuild would wipe whatever the agent installed, which is the whole
	// content of a sandbox that has been working for an hour.
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the environment was rebuilt on the second command: %v", err)
	}
}

func TestAMissingInterpreterDoesNotBreakOtherCommands(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.VenvDir = DefaultVenvDir
	cfg.PythonProgram = "python-that-is-not-installed"
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// An agent running `echo` should not be refused because the image has no
	// Python: the environment is a convenience, not a precondition.
	res, err := sb.ExecCommand(context.Background(), "echo", []string{"still works"}, 30*time.Second, "")
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if !strings.Contains(res.Stdout, "still works") {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

func TestWithoutAVenvDirNothingIsCreatedOrAnnounced(t *testing.T) {
	sb, err := New(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := sb.venvBinDir(); got != "" {
		t.Errorf("venvBinDir = %q, want empty when disabled", got)
	}

	res, err := sb.ExecCommand(context.Background(), "env", nil, 30*time.Second, "")
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if strings.Contains(res.Stdout, "VIRTUAL_ENV=") {
		t.Error("VIRTUAL_ENV is announced although no environment was configured")
	}
	entries, err := os.ReadDir(sb.config.RootDir)
	if err != nil {
		t.Fatalf("read the sandbox: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the sandbox contains %+v, want nothing created", entries)
	}
}
