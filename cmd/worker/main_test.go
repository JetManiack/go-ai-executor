package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// tokenFromArgs parses flags without starting anything, by replacing the action.
func tokenFromArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var (
		got    string
		gotErr error
	)
	cmd := newRootCommand()
	cmd.Action = func(_ context.Context, c *cli.Command) error {
		got, gotErr = workerToken(c)
		return nil
	}
	if err := cmd.Run(context.Background(), append([]string{"worker"}, args...)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return got, gotErr
}

func TestTheTokenFileIsPreferredOverTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	// A trailing newline is what a here-doc or an editor leaves, and a token
	// off by one invisible byte fails as "invalid worker token" — an error that
	// sends an operator looking anywhere but here.
	if err := os.WriteFile(path, []byte("  from-the-file\n"), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := tokenFromArgs(t, "--worker-token", "from-the-flag", "--worker-token-file", path)
	if err != nil {
		t.Fatalf("workerToken: %v", err)
	}
	if got != "from-the-file" {
		t.Errorf("token = %q, want the file's contents trimmed", got)
	}
}

func TestWithoutAFileTheFlagIsUsed(t *testing.T) {
	got, err := tokenFromArgs(t, "--worker-token", "from-the-flag")
	if err != nil {
		t.Fatalf("workerToken: %v", err)
	}
	if got != "from-the-flag" {
		t.Errorf("token = %q", got)
	}
}

func TestAnUnreadableOrEmptyTokenFileIsRefusedAtStartup(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("\n  \n"), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Both cases fail at startup rather than at the first dial, where they would
	// read as an authentication problem with the server.
	for name, path := range map[string]string{
		"missing": filepath.Join(dir, "not-there"),
		"empty":   empty,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tokenFromArgs(t, "--worker-token-file", path)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("err = %q, want it to name the file", err)
			}
		})
	}
}

func TestTheUIDRangeIsParsedInclusively(t *testing.T) {
	got, err := parseUIDRange("20000-20999")
	if err != nil {
		t.Fatalf("parseUIDRange: %v", err)
	}
	if got.First != 20000 || got.Count != 1000 {
		t.Errorf("range = %+v, want 20000 and 1000 ids", got)
	}
	if !got.Contains(20000) || !got.Contains(20999) || got.Contains(21000) {
		t.Error("the range's ends are wrong; it is written inclusive")
	}
}

func TestNoUIDRangeMeansEveryAgentSharesOneUser(t *testing.T) {
	// The default on a developer's machine, where setuid is not available: the
	// mechanism has to be off rather than broken.
	got, err := parseUIDRange("")
	if err != nil {
		t.Fatalf("parseUIDRange: %v", err)
	}
	if got.Enabled() {
		t.Errorf("range = %+v, want it disabled", got)
	}
}

func TestAnUnusableUIDRangeIsRefusedAtStartup(t *testing.T) {
	for name, spec := range map[string]string{
		"not a range":           "20000",
		"backwards":             "20999-20000",
		"not numbers":           "twenty-thousand",
		"reaches past a userns": "60000-70000",
		"inside the system ids": "500-1500",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseUIDRange(spec); err == nil {
				t.Errorf("parseUIDRange(%q) was accepted", spec)
			}
		})
	}
}
