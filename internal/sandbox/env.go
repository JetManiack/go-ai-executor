package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DefaultEnvPassthrough names the environment variables a command inherits from
// the server process unless the operator configures otherwise.
//
// PATH is the reason this list exists. It used to be hardcoded to
// /usr/bin:/bin:/usr/local/bin:/usr/sbin:/sbin, which meant an agent could not
// see anything installed anywhere else — Homebrew's /opt/homebrew/bin, Go's
// /usr/local/go/bin, a language version manager's shims — so tools that are
// plainly present on the host were simply missing.
//
// The locale variables matter for correctness rather than convenience: without
// LC_CTYPE (and LANG/LC_ALL) a good number of tools fall back to an ASCII
// interpretation of their input and mangle any non-ASCII byte they are handed.
//
// TERM is deliberately absent. Commands run on pipes, not a terminal, so
// claiming a terminal only invites tools to emit cursor control and colour that
// this service then strips again.
var DefaultEnvPassthrough = []string{"PATH", "LANG", "LC_ALL", "LC_CTYPE", "TZ"}

// FallbackPATH is used when PATH is passed through but unset in the server's own
// environment, which happens under some service managers.
const FallbackPATH = "/usr/local/bin:/usr/bin:/bin:/usr/local/sbin:/usr/sbin:/sbin"

// deniedEnvNames can never be passed through: they are this service's own
// credentials, and no agent task needs them. An agent that needs a token for its
// own work gets it through ExtraEnv, deliberately, under a name of the
// operator's choosing.
//
// This is a hard refusal at startup rather than a warning, because the failure it
// prevents is silent: the variables would simply be present in every command's
// environment, and the first `env` an agent runs hands out the database DSN and
// the session-encryption key.
var deniedEnvNames = []string{"DB_DSN", "OIDC_CLIENT_SECRET", "SESSION_ENCRYPTION_KEY"}

// managedEnvNames are set by the sandbox itself and cannot be inherited: HOME and
// PWD are pointed inside the jail, and letting the server's values through would
// aim a command's "home" at the service account's real home directory.
// VIRTUAL_ENV joins them because the sandbox points it at the environment it
// created: inheriting the worker's would aim an agent's Python at a directory it
// cannot see.
var managedEnvNames = []string{"HOME", "PWD", "VIRTUAL_ENV"}

// ValidateEnvPassthrough reports whether names is a usable passthrough list.
func ValidateEnvPassthrough(names []string) error {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if slices.Contains(deniedEnvNames, name) {
			return fmt.Errorf("environment variable %q holds this service's own credentials and cannot be passed to sandboxed commands", name)
		}
		if slices.Contains(managedEnvNames, name) {
			return fmt.Errorf("environment variable %q is set by the sandbox itself and cannot be passed through", name)
		}
		if strings.Contains(name, "=") {
			return fmt.Errorf("environment passthrough takes variable names, not assignments: %q", name)
		}
	}
	return nil
}

// SuspiciousEnvNames returns the entries of names that look like they carry a
// secret, for the caller to warn about. Unlike deniedEnvNames these are not
// refused: an agent whose actual job is to call an API does need its token, and
// only the operator can say which variable that is.
func SuspiciousEnvNames(names []string) []string {
	markers := []string{"SECRET", "TOKEN", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE_KEY"}

	var suspicious []string
	for _, name := range names {
		upper := strings.ToUpper(name)
		for _, marker := range markers {
			if strings.Contains(upper, marker) {
				suspicious = append(suspicious, name)
				break
			}
		}
	}
	return suspicious
}

// buildEnv assembles the environment one command runs with: the inherited
// variables first, then the operator's literal additions, then the two the
// sandbox controls — so HOME and PWD always win, whatever came before them.
// venvBin, when set, is prepended to PATH and announced as VIRTUAL_ENV — which is
// all `activate` does. There is no shell here to source it in, so the environment
// is where activation lives, and it applies to every command rather than to the
// ones an agent remembered to activate first.
func buildEnv(cfg Config, workDir, venvBin string) []string {
	env := make([]string, 0, len(cfg.EnvPassthrough)+len(cfg.ExtraEnv)+4)

	for _, name := range cfg.EnvPassthrough {
		name = strings.TrimSpace(name)
		if name == "" || slices.Contains(deniedEnvNames, name) || slices.Contains(managedEnvNames, name) {
			continue
		}
		value, ok := os.LookupEnv(name)
		if name == "PATH" {
			// A command with no PATH at all cannot resolve a single tool by name,
			// which looks like every tool being missing rather than like a
			// misconfigured server.
			if !ok {
				value = FallbackPATH
			}
			if venvBin != "" {
				value = venvBin + string(os.PathListSeparator) + value
			}
			env = append(env, "PATH="+value)
			continue
		}
		if !ok {
			continue
		}
		env = append(env, name+"="+value)
	}

	env = append(env, cfg.ExtraEnv...)
	env = append(env, "HOME="+cfg.RootDir, "PWD="+workDir)
	if venvBin != "" {
		env = append(env, "VIRTUAL_ENV="+filepath.Dir(venvBin))
	}
	return env
}

// envNames reduces KEY=VALUE entries to their names.
func envNames(env []string) []string {
	names := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, found := strings.Cut(entry, "=")
		if found {
			names = append(names, name)
		}
	}
	return names
}
