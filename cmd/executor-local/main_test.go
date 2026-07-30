package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// syncBuffer collects a subprocess's stderr safely.
//
// A plain bytes.Buffer is not enough: os/exec copies into cmd.Stderr from its own
// goroutine for as long as the process runs, so reading Len() while the session is
// still open is a data race — one the race detector does catch.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// buildHelper compiles the binary under test once and returns its path.
//
// The properties worth checking here — that stdout carries MCP frames and nothing
// else, and that a startup failure goes to stderr — only exist for the real
// process. Exercising newRootCommand in-process would not see them.
func buildHelper(t *testing.T) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "executor-local")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build the helper: %v", err)
	}
	return binary
}

// TestSpeaksMCPOverStdio drives the real binary the way a desktop client does.
//
// It also covers the "silent" requirement without asserting on it directly: the
// MCP protocol owns stdout, so a startup banner or a stray log line would arrive
// as a malformed frame and the handshake below would fail.
func TestSpeaksMCPOverStdio(t *testing.T) {
	binary := buildHelper(t)
	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var stderr syncBuffer
	cmd := exec.Command(binary, "--dir", dir)
	cmd.Stderr = &stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to the helper: %v (stderr: %s)", err, stderr.String())
	}
	t.Cleanup(func() { _ = session.Close() })

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v (stderr: %s)", err, stderr.String())
	}
	var sawRunShell bool
	for _, tool := range tools.Tools {
		if tool.Name == "run_shell" {
			sawRunShell = true
		}
	}
	if !sawRunShell {
		t.Error("the helper does not advertise run_shell")
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "run_shell",
		Arguments: map[string]any{"command": "echo from-the-helper && pwd"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v (stderr: %s)", err, stderr.String())
	}
	if result.IsError {
		t.Fatalf("run_shell failed: %v", result.Content)
	}

	fields, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content is %T, want a JSON object", result.StructuredContent)
	}
	stdout, _ := fields["stdout"].(string)
	if !strings.Contains(stdout, "from-the-helper") {
		t.Errorf("stdout = %q, want the echoed text", stdout)
	}

	// The command ran in --dir, which is the whole point of a local helper.
	wantDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if !strings.Contains(stdout, wantDir) {
		t.Errorf("stdout = %q, want it to show the working directory %q", stdout, wantDir)
	}

	// Nothing was written to stderr either: the helper is meant to be silent, not
	// merely to keep its noise off stdout. Checked after closing the session, so
	// the subprocess and its output copier are finished.
	if err := session.Close(); err != nil {
		t.Fatalf("close the session: %v", err)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("the helper wrote to stderr during a normal session: %q", got)
	}
}

// TestStartupFailureGoesToStderrNotStdout matters because a message on stdout
// would reach the client as a malformed frame, and the operator would see a
// session that died with no explanation.
func TestStartupFailureGoesToStderrNotStdout(t *testing.T) {
	binary := buildHelper(t)

	cmd := exec.Command(binary, "--dir", filepath.Join(t.TempDir(), "does-not-exist"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		t.Fatal("the helper started with a nonexistent working directory")
	}
	if stdout.Len() != 0 {
		t.Errorf("startup failure wrote %q to stdout, which the MCP protocol owns", stdout.String())
	}
	if !strings.Contains(stderr.String(), "does-not-exist") {
		t.Errorf("stderr = %q, want it to name the unusable directory", stderr.String())
	}
}

// TestUsageGoesToStderr covers the same hazard for --help, which a client may
// invoke by misconfiguration.
func TestUsageGoesToStderr(t *testing.T) {
	binary := buildHelper(t)

	cmd := exec.Command(binary, "--help")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()

	if stdout.Len() != 0 {
		t.Errorf("--help wrote %q to stdout, which the MCP protocol owns", stdout.String())
	}
	if !strings.Contains(stderr.String(), "executor-local") {
		t.Errorf("stderr = %q, want the usage text", stderr.String())
	}
}
