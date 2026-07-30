package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// escapeFixture builds a sandbox alongside a file outside it, plus a symlink
// inside the sandbox pointing at that file.
//
// An agent can create exactly this with one shell command — `ln -s /etc/passwd
// link` — so a symlink inside the sandbox is attacker-controlled input, not a
// hypothetical.
func escapeFixture(t *testing.T) (sb *Sandbox, outsidePath string) {
	t.Helper()

	root := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath = filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsidePath, []byte("SECRET"), 0o600); err != nil {
		t.Fatalf("seed the file outside the sandbox: %v", err)
	}

	sb, err := New(Config{
		RootDir:        root,
		DefaultTimeout: 5 * time.Second,
		MaxOutputBytes: 4096,
		Shell:          "/bin/sh",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := os.Symlink(outsidePath, filepath.Join(root, "link")); err != nil {
		t.Fatalf("create the escaping symlink: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(root, "dirlink")); err != nil {
		t.Fatalf("create the escaping directory symlink: %v", err)
	}
	return sb, outsidePath
}

// TestReadThroughSymlinkIsRefused is a regression test for a confirmed escape:
// path containment used to be a lexical check on the requested path, which a
// symlink inside the sandbox passes trivially while os.ReadFile follows it
// straight out. The file operations now go through os.Root, which refuses in the
// kernel.
func TestReadThroughSymlinkIsRefused(t *testing.T) {
	sb, _ := escapeFixture(t)

	data, err := sb.ReadFile("link")
	if err == nil {
		t.Fatalf("read through an escaping symlink succeeded, returning %q", data)
	}
	if strings.Contains(string(data), "SECRET") {
		t.Error("contents from outside the sandbox were returned")
	}
}

func TestWriteThroughSymlinkIsRefused(t *testing.T) {
	sb, outsidePath := escapeFixture(t)

	if err := sb.WriteFile("link", []byte("OVERWRITTEN"), 0o644); err == nil {
		t.Error("write through an escaping symlink succeeded")
	}

	// The check that matters is the file on disk, not the error: a refused write
	// that still landed would be the worst of both.
	after, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("reread the outside file: %v", err)
	}
	if string(after) != "SECRET" {
		t.Errorf("the file outside the sandbox was modified: %q", after)
	}
}

func TestDeleteThroughSymlinkDoesNotReachOutside(t *testing.T) {
	sb, outsidePath := escapeFixture(t)

	// Removing the link itself is legitimate — it lives in the sandbox. What must
	// not happen is the target being removed with it.
	_, _ = sb.DeleteFile("link")

	if _, err := os.Stat(outsidePath); err != nil {
		t.Errorf("the file outside the sandbox was deleted: %v", err)
	}
}

func TestListThroughDirectorySymlinkIsRefused(t *testing.T) {
	sb, _ := escapeFixture(t)

	entries, err := sb.ListDir("dirlink")
	if err == nil {
		t.Errorf("listing an escaping directory symlink succeeded, returning %d entries", len(entries))
	}
}

func TestWriteCannotCreateParentsOutsideTheSandbox(t *testing.T) {
	sb, _ := escapeFixture(t)

	// WriteFile creates missing parents, so the traversal has to be refused
	// before the mkdir, not only at the final write.
	if err := sb.WriteFile("dirlink/nested/new.txt", []byte("x"), 0o644); err == nil {
		t.Error("write created directories through an escaping symlink")
	}
}

func TestWorkDirCannotEscapeThroughASymlink(t *testing.T) {
	sb, _ := escapeFixture(t)

	// A command's working directory is handed to exec, which cannot go through
	// os.Root, so it resolves the symlink chain itself.
	if _, err := sb.ExecCommand(context.Background(), "pwd", time.Second, "dirlink"); err == nil {
		t.Error("a command ran with its working directory outside the sandbox")
	}
}

func TestWorkDirRejectsTraversal(t *testing.T) {
	sb, _ := escapeFixture(t)

	for _, workDir := range []string{"..", "../..", "/etc"} {
		if _, err := sb.ExecCommand(context.Background(), "pwd", time.Second, workDir); err == nil {
			t.Errorf("a command ran with work_dir %q", workDir)
		}
	}
}

// TestExecCommandIsNotConfinedByTheSandbox states the boundary plainly rather
// than leaving a reader to assume the file-path containment extends to shell
// commands. exec_command runs as the server's user and can read whatever that
// user can; the sandbox root is its working directory, not a security boundary.
// The README says so, and this test is what keeps that documentation honest.
func TestExecCommandIsNotConfinedByTheSandbox(t *testing.T) {
	sb, outsidePath := escapeFixture(t)

	res, err := sb.ExecCommand(context.Background(), "cat "+outsidePath, 5*time.Second, "")
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if !strings.Contains(res.Stdout, "SECRET") {
		t.Skipf("could not read %s as this user; the documented boundary is unchanged", outsidePath)
	}
	// Deliberately not an error: containment for shell commands needs a
	// container, and pretending otherwise in a test would misdescribe the
	// deployment requirement.
	t.Log("confirmed: exec_command reaches outside the sandbox, as documented — run this service in a container")
}
