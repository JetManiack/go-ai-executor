package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newEnvSandbox builds a sandbox with the default passthrough list.
func newEnvSandbox(t *testing.T) *Sandbox {
	t.Helper()
	cfg := DefaultConfig(t.TempDir())
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sb
}

// commandEnv runs `env` in the sandbox and returns the variables the command
// actually saw.
func commandEnv(t *testing.T, sb *Sandbox) map[string]string {
	t.Helper()
	res, err := sb.ExecCommand(context.Background(), "env", 10*time.Second, "")
	if err != nil {
		t.Fatalf("ExecCommand(env): %v", err)
	}

	seen := map[string]string{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if name, value, found := strings.Cut(line, "="); found {
			seen[name] = value
		}
	}
	return seen
}

// TestPATHIsInheritedFromTheServer is the regression test for a hardcoded PATH.
// It used to be /usr/bin:/bin:/usr/local/bin:/usr/sbin:/sbin, so anything
// installed outside those five directories — Homebrew, Go, a version manager's
// shims — was invisible to every agent, while plainly present on the host.
func TestPATHIsInheritedFromTheServer(t *testing.T) {
	toolDir := t.TempDir()
	script := filepath.Join(toolDir, "only-on-this-host")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho found-the-tool\n"), 0o700); err != nil {
		t.Fatalf("write the fake tool: %v", err)
	}

	t.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	sb := newEnvSandbox(t)

	res, err := sb.ExecCommand(context.Background(), "only-on-this-host", 10*time.Second, "")
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if !strings.Contains(res.Stdout, "found-the-tool") {
		t.Errorf("a tool on the server's PATH was not resolvable: stdout=%q stderr=%q exit=%d",
			res.Stdout, res.Stderr, res.ExitCode)
	}
}

func TestLocaleVariablesArePassedThrough(t *testing.T) {
	// LC_CTYPE was missing entirely, which makes a range of tools fall back to
	// an ASCII reading of their input and mangle non-ASCII bytes.
	t.Setenv("LC_CTYPE", "en_US.UTF-8")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("TZ", "Europe/Nicosia")

	seen := commandEnv(t, newEnvSandbox(t))

	for name, want := range map[string]string{
		"LC_CTYPE": "en_US.UTF-8",
		"LANG":     "en_US.UTF-8",
		"TZ":       "Europe/Nicosia",
	} {
		if seen[name] != want {
			t.Errorf("%s = %q, want %q", name, seen[name], want)
		}
	}
}

// TestServiceCredentialsNeverReachCommands is why the environment is an allowlist
// rather than os.Environ(): this service's own configuration lives in exactly the
// place a command would otherwise inherit.
func TestServiceCredentialsNeverReachCommands(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://user:hunter2@db/executor")
	t.Setenv("OIDC_CLIENT_SECRET", "the-client-secret")
	t.Setenv("SESSION_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "unrelated-but-also-private")

	sb := newEnvSandbox(t)
	seen := commandEnv(t, sb)

	for _, name := range []string{"DB_DSN", "OIDC_CLIENT_SECRET", "SESSION_ENCRYPTION_KEY", "AWS_SECRET_ACCESS_KEY"} {
		if value, present := seen[name]; present {
			t.Errorf("%s reached the command with value %q", name, value)
		}
	}

	// Also check the raw output, in case a value leaked under another name.
	res, err := sb.ExecCommand(context.Background(), "env", 10*time.Second, "")
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	for _, secret := range []string{"hunter2", "the-client-secret", "unrelated-but-also-private"} {
		if strings.Contains(res.Stdout, secret) {
			t.Errorf("secret value %q appears in the command's environment", secret)
		}
	}
}

func TestPassthroughOfServiceCredentialsIsRefused(t *testing.T) {
	for _, name := range []string{"DB_DSN", "OIDC_CLIENT_SECRET", "SESSION_ENCRYPTION_KEY"} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateEnvPassthrough([]string{"PATH", name}); err == nil {
				t.Errorf("%s was accepted as a passthrough variable", name)
			}
		})
	}
}

func TestPassthroughOfManagedVariablesIsRefused(t *testing.T) {
	// HOME and PWD are aimed inside the jail; inheriting the server's values
	// would point a command's home at the service account's real one.
	for _, name := range []string{"HOME", "PWD"} {
		if err := ValidateEnvPassthrough([]string{name}); err == nil {
			t.Errorf("%s was accepted as a passthrough variable", name)
		}
	}
}

func TestPassthroughRejectsAssignments(t *testing.T) {
	if err := ValidateEnvPassthrough([]string{"FOO=bar"}); err == nil {
		t.Error("an assignment was accepted where a variable name was expected")
	}
}

// TestDeniedNamesAreDroppedEvenIfConfigured covers the second line of defence:
// validation rejects them at startup, and buildEnv drops them anyway, so no
// construction path can put them in a command's environment.
func TestDeniedNamesAreDroppedEvenIfConfigured(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://user:hunter2@db/executor")
	t.Setenv("HOME", "/home/service-account")

	root := t.TempDir()
	env := buildEnv(Config{
		RootDir:        root,
		EnvPassthrough: []string{"DB_DSN", "HOME"},
	}, root)

	for _, entry := range env {
		if strings.HasPrefix(entry, "DB_DSN=") {
			t.Error("DB_DSN survived into the command environment")
		}
	}
	// HOME is present, but as the sandbox's own value.
	var home string
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "HOME="); ok {
			home = value
		}
	}
	if home != root {
		t.Errorf("HOME = %q, want the sandbox root %q", home, root)
	}
}

func TestExtraEnvReachesCommands(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.ExtraEnv = []string{"AGENT_API_TOKEN=for-the-agents-own-task"}
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if seen := commandEnv(t, sb)["AGENT_API_TOKEN"]; seen != "for-the-agents-own-task" {
		t.Errorf("AGENT_API_TOKEN = %q, want the configured value", seen)
	}
}

func TestExtraEnvCannotOverrideTheJailedHome(t *testing.T) {
	root := t.TempDir()
	env := buildEnv(Config{
		RootDir:  root,
		ExtraEnv: []string{"HOME=/etc", "PWD=/etc"},
	}, root)

	// buildEnv appends the sandbox's values last, and the last assignment wins,
	// so a misconfigured ExtraEnv cannot aim a command out of the jail.
	var home, pwd string
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "HOME="); ok {
			home = value
		}
		if value, ok := strings.CutPrefix(entry, "PWD="); ok {
			pwd = value
		}
	}
	if home != root || pwd != root {
		t.Errorf("HOME=%q PWD=%q, want both to be the sandbox root %q", home, pwd, root)
	}
}

func TestFallbackPATHWhenTheServerHasNone(t *testing.T) {
	t.Setenv("PATH", "")
	if err := os.Unsetenv("PATH"); err != nil {
		t.Fatalf("unset PATH: %v", err)
	}

	root := t.TempDir()
	env := buildEnv(Config{RootDir: root, EnvPassthrough: []string{"PATH"}}, root)

	var path string
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			path = value
		}
	}
	// No PATH at all makes every tool unresolvable, which reads as a broken
	// sandbox rather than a misconfigured server.
	if path != FallbackPATH {
		t.Errorf("PATH = %q, want the fallback %q", path, FallbackPATH)
	}
}

func TestUnsetPassthroughVariablesAreOmitted(t *testing.T) {
	if err := os.Unsetenv("LC_ALL"); err != nil {
		t.Fatalf("unset LC_ALL: %v", err)
	}

	root := t.TempDir()
	env := buildEnv(Config{RootDir: root, EnvPassthrough: []string{"LC_ALL"}}, root)

	for _, entry := range env {
		// An empty LC_ALL is not the same as an absent one: some tools treat the
		// empty string as a request for the C locale.
		if strings.HasPrefix(entry, "LC_ALL=") {
			t.Errorf("unset LC_ALL was passed as %q", entry)
		}
	}
}

func TestSuspiciousEnvNames(t *testing.T) {
	got := SuspiciousEnvNames([]string{"PATH", "GITHUB_TOKEN", "MY_SECRET", "db_password", "LANG"})
	want := map[string]bool{"GITHUB_TOKEN": true, "MY_SECRET": true, "db_password": true}

	if len(got) != len(want) {
		t.Fatalf("suspicious = %v, want %d entries", got, len(want))
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("%q reported as suspicious", name)
		}
	}
}

func TestStatusReportsEnvNamesWithoutValues(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.ExtraEnv = []string{"AGENT_API_TOKEN=for-the-agents-own-task"}
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	status := sb.GetStatus()
	for _, name := range status.EnvNames {
		// The status tool tells an agent which variables it has, not what is in
		// them — an agent asking for its own configuration should not be a way to
		// read back a token.
		if strings.Contains(name, "=") || strings.Contains(name, "for-the-agents-own-task") {
			t.Errorf("status leaked a value: %q", name)
		}
	}

	var sawToken bool
	for _, name := range status.EnvNames {
		if name == "AGENT_API_TOKEN" {
			sawToken = true
		}
	}
	if !sawToken {
		t.Error("status does not list AGENT_API_TOKEN, so an agent cannot tell it was given one")
	}
}
