package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runRoot runs the CLI with args and returns its error, bounded by a timeout so a
// case that unexpectedly starts serving fails instead of hanging the suite.
func runRoot(t *testing.T, args ...string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return newRootCommand().Run(ctx, append([]string{"executor"}, args...))
}

// TestDefaultAuthRequiresOIDC is a security regression test. This flag used to
// default to true, so an untouched binary served the whole UI — every sandbox's
// live terminal, and the button that kills their processes — to anyone who could
// reach the port, as an administrator.
func TestDefaultAuthRequiresOIDC(t *testing.T) {
	err := runRoot(t,
		"--listen-addr=127.0.0.1:0",
		"--db-dsn="+filepath.Join(t.TempDir(), "test.db"),
	)
	if err == nil {
		t.Fatal("server started with no auth configured and no --auth-stub")
	}
	for _, want := range []string{"--oidc-issuer", "--auth-stub"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestIncompleteOIDCConfigIsRefused(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "issuer only",
			args: []string{"--oidc-issuer=https://keycloak.example.com/realms/executor"},
		},
		{
			name: "missing session key",
			args: []string{
				"--oidc-issuer=https://keycloak.example.com/realms/executor",
				"--oidc-client-id=executor",
				"--oidc-client-secret=shhh",
				"--public-url=https://executor.example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{
				"--listen-addr=127.0.0.1:0",
				"--db-dsn=" + filepath.Join(t.TempDir(), "test.db"),
			}, tt.args...)

			err := runRoot(t, args...)
			if err == nil {
				t.Fatal("server started on an incomplete OIDC config")
			}
			// Silently falling back to the stub would be the dangerous outcome
			// here, so the message has to name what is missing.
			if !strings.Contains(err.Error(), "OIDC auth requires") {
				t.Errorf("error = %q, want it to name the missing OIDC settings", err)
			}
		})
	}
}

func TestSessionKeyMustBe32Bytes(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "not base64", key: "not-base64!!", want: "valid base64"},
		{name: "wrong length", key: "c2hvcnQ=", want: "exactly 32 bytes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runRoot(t,
				"--listen-addr=127.0.0.1:0",
				"--db-dsn="+filepath.Join(t.TempDir(), "test.db"),
				"--oidc-issuer=https://keycloak.example.com/realms/executor",
				"--oidc-client-id=executor",
				"--oidc-client-secret=shhh",
				"--public-url=https://executor.example.com",
				"--session-encryption-key="+tt.key,
			)
			if err == nil {
				t.Fatal("server started with an unusable session encryption key")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}
