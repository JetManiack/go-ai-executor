package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// lookProgram resolves the program a command should execute.
//
// A name containing a path separator is left alone: exec evaluates a relative
// Path against Cmd.Dir, which is the sandbox working directory, so "./build.sh"
// means the agent's own script and keeps meaning that.
//
// A bare name is resolved against the PATH in env rather than the server's own.
// exec.Command would consult os.Getenv("PATH"), which is the wrong PATH as soon
// as an operator supplies one through --env: the command would be told one PATH
// and have its program found on another.
func lookProgram(program string, env []string) (string, error) {
	if strings.TrimSpace(program) == "" {
		return "", ErrEmptyProgram
	}
	if strings.ContainsRune(program, filepath.Separator) {
		return program, nil
	}

	pathEnv := envValue(env, "PATH")
	if pathEnv == "" {
		return "", fmt.Errorf("cannot resolve %q: no PATH in the command environment", program)
	}

	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, program)
		if err := executable(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%q not found in PATH (%s)", program, pathEnv)
}

// executable reports whether name is a regular file with an execute bit set.
func executable(name string) error {
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("is a directory")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fs.ErrPermission
	}
	return nil
}

// envValue returns the value of name in a KEY=VALUE list, last assignment
// winning — the same rule exec applies.
func envValue(env []string, name string) string {
	value := ""
	prefix := name + "="
	for _, entry := range env {
		if suffix, ok := strings.CutPrefix(entry, prefix); ok {
			value = suffix
		}
	}
	return value
}

// commandLine renders a program and its arguments for display in the terminal
// stream.
//
// Arguments containing whitespace or quotes are quoted so the boundary between
// them stays visible: with no shell involved, `rm -rf my file` is two arguments
// and `rm -rf "my file"` is one, and a watcher has to be able to tell which just
// ran.
func commandLine(program string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteArg(program))
	for _, arg := range args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\"'\\") {
		return arg
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(arg) + `"`
}
