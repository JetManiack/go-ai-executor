package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLookProgramResolvesAgainstTheCommandPATH(t *testing.T) {
	toolDir := t.TempDir()
	tool := filepath.Join(toolDir, "custom-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write the fake tool: %v", err)
	}

	// The server's own PATH deliberately does not contain toolDir: resolution has
	// to use the PATH the command will run with, or an operator-supplied PATH is
	// advertised to the command and then ignored when finding the program.
	resolved, err := lookProgram("custom-tool", []string{"PATH=" + toolDir})
	if err != nil {
		t.Fatalf("lookProgram: %v", err)
	}
	if resolved != tool {
		t.Errorf("resolved = %q, want %q", resolved, tool)
	}
}

func TestLookProgramLeavesPathsAlone(t *testing.T) {
	// A name with a separator is the agent's own script, resolved by exec against
	// the sandbox working directory — not something to look up.
	for _, program := range []string{"./build.sh", "sub/dir/tool", "/usr/bin/env"} {
		resolved, err := lookProgram(program, []string{"PATH=/nowhere"})
		if err != nil {
			t.Errorf("lookProgram(%q): %v", program, err)
		}
		if resolved != program {
			t.Errorf("lookProgram(%q) = %q, want it unchanged", program, resolved)
		}
	}
}

func TestLookProgramRejectsMissingAndEmpty(t *testing.T) {
	if _, err := lookProgram("", []string{"PATH=/usr/bin"}); !errors.Is(err, ErrEmptyProgram) {
		t.Errorf("error for an empty program = %v, want ErrEmptyProgram", err)
	}
	if _, err := lookProgram("   ", []string{"PATH=/usr/bin"}); !errors.Is(err, ErrEmptyProgram) {
		t.Errorf("error for a blank program = %v, want ErrEmptyProgram", err)
	}
	if _, err := lookProgram("definitely-not-installed", []string{"PATH=" + t.TempDir()}); err == nil {
		t.Error("a missing program resolved successfully")
	}
	if _, err := lookProgram("anything", nil); err == nil {
		t.Error("a program resolved with no PATH in the environment")
	}
}

func TestLookProgramSkipsNonExecutables(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tool"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write the non-executable: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "other"), 0o750); err != nil {
		t.Fatalf("make the directory: %v", err)
	}

	// A readable-but-not-executable file and a directory with the right name must
	// not be mistaken for the program.
	if _, err := lookProgram("tool", []string{"PATH=" + dir}); err == nil {
		t.Error("a non-executable file was resolved as a program")
	}
	if _, err := lookProgram("other", []string{"PATH=" + dir}); err == nil {
		t.Error("a directory was resolved as a program")
	}
}

func TestCommandLineQuotesArgumentBoundaries(t *testing.T) {
	// With no shell, `rm -rf my file` is two arguments and `rm -rf "my file"` is
	// one. A watcher reading the terminal has to be able to tell which ran.
	tests := []struct {
		program string
		args    []string
		want    string
	}{
		{program: "echo", args: []string{"hello"}, want: "echo hello"},
		{program: "rm", args: []string{"-rf", "my file"}, want: `rm -rf "my file"`},
		{program: "echo", args: []string{""}, want: `echo ""`},
		{program: "echo", args: []string{`a"b`}, want: `echo "a\"b"`},
		{program: "sh", args: []string{"-c", "echo a; echo b"}, want: `sh -c "echo a; echo b"`},
		{program: "pwd", args: nil, want: "pwd"},
	}

	for _, tt := range tests {
		if got := commandLine(tt.program, tt.args); got != tt.want {
			t.Errorf("commandLine(%q, %q) = %q, want %q", tt.program, tt.args, got, tt.want)
		}
	}
}

func TestEnvValueTakesTheLastAssignment(t *testing.T) {
	// exec applies the last assignment, so resolution has to read it the same way
	// or the program is found on a PATH the command does not have.
	if got := envValue([]string{"PATH=/first", "PATH=/second"}, "PATH"); got != "/second" {
		t.Errorf("envValue = %q, want %q", got, "/second")
	}
	if got := envValue([]string{"OTHER=x"}, "PATH"); got != "" {
		t.Errorf("envValue for a missing name = %q, want empty", got)
	}
}
