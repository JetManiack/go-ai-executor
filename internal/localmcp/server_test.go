package localmcp_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JetManiack/go-ai-executor/internal/localexec"
	"github.com/JetManiack/go-ai-executor/internal/localmcp"
)

// connectSession wires a client to the server over in-memory transports, so tool
// dispatch and schema generation are exercised the way a real client drives them.
func connectSession(t *testing.T, dir string) (*mcp.ClientSession, *localexec.Runner) {
	t.Helper()

	runner, err := localexec.New(localexec.Config{Dir: dir})
	if err != nil {
		t.Fatalf("localexec.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := localmcp.NewServer(runner, "test")
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	return session, runner
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q): %v", name, err)
	}
	return result
}

func field(t *testing.T, result *mcp.CallToolResult, key string) any {
	t.Helper()
	fields, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content is %T, want a JSON object", result.StructuredContent)
	}
	value, present := fields[key]
	if !present {
		t.Fatalf("structured output has no %q field: %v", key, fields)
	}
	return value
}

func TestAdvertisesItsTools(t *testing.T) {
	session, _ := connectSession(t, t.TempDir())

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
		if tool.InputSchema == nil {
			t.Errorf("tool %q advertises no input schema", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q advertises no description", tool.Name)
		}
	}

	for _, want := range []string{"run_shell", "get_status"} {
		if !slices.Contains(names, want) {
			t.Errorf("tools = %v, want it to contain %q", names, want)
		}
	}

	// The name has to differ from the server's exec_command: that tool takes a
	// program and an argument vector, this one a command line, and an agent that
	// talks to both must not see one name with two schemas.
	if slices.Contains(names, "exec_command") {
		t.Error("the local helper advertises exec_command, which is the server's differently-shaped tool")
	}
}

func TestRunShellReturnsOutput(t *testing.T) {
	session, _ := connectSession(t, t.TempDir())

	result := callTool(t, session, "run_shell", map[string]any{"command": "echo hello && echo world"})
	if result.IsError {
		t.Fatalf("run_shell failed: %v", result.Content)
	}
	if got := field(t, result, "stdout"); !strings.Contains(got.(string), "hello") ||
		!strings.Contains(got.(string), "world") {
		t.Errorf("stdout = %q, want both echoes — the shell should have run the whole line", got)
	}
}

func TestRunShellReportsANonZeroExit(t *testing.T) {
	session, _ := connectSession(t, t.TempDir())

	result := callTool(t, session, "run_shell", map[string]any{"command": "exit 4"})
	// A command that fails is an answer, not a failed tool call.
	if result.IsError {
		t.Fatalf("run_shell reported a tool error for a non-zero exit: %v", result.Content)
	}
	if got := field(t, result, "exit_code"); got != float64(4) {
		t.Errorf("exit_code = %v, want 4", got)
	}
}

func TestRunShellRejectsAnEmptyCommand(t *testing.T) {
	session, _ := connectSession(t, t.TempDir())

	result := callTool(t, session, "run_shell", map[string]any{"command": ""})
	if !result.IsError {
		t.Error("an empty command was accepted")
	}
}

func TestRunShellHonoursItsTimeout(t *testing.T) {
	session, _ := connectSession(t, t.TempDir())

	result := callTool(t, session, "run_shell", map[string]any{"command": "sleep 30", "timeout_sec": 1})
	if !result.IsError {
		t.Error("a command that outran its timeout was reported as success")
	}
}

func TestStatusDescribesTheEnvironmentAndSaysItIsNotSandboxed(t *testing.T) {
	dir := t.TempDir()
	session, runner := connectSession(t, dir)

	result := callTool(t, session, "get_status", map[string]any{})
	if result.IsError {
		t.Fatalf("get_status failed: %v", result.Content)
	}

	if got := field(t, result, "dir"); got != runner.Dir() {
		t.Errorf("dir = %v, want %q", got, runner.Dir())
	}
	if got := field(t, result, "shell"); got != runner.Shell() {
		t.Errorf("shell = %v, want %q", got, runner.Shell())
	}
	// Reported in the tool output, not only in the README: an agent that believes
	// it is sandboxed will take risks it otherwise would not.
	if got := field(t, result, "sandboxed"); got != false {
		t.Errorf("sandboxed = %v, want false", got)
	}
}

// TestTimeoutKeepsPartialOutput covers a defect the SDK's typed-handler path
// makes easy to ship: returning the error to it drops the structured output, so a
// timed-out build reported "timed out" and nothing else. Where it got stuck is
// usually the only thing worth having.
func TestTimeoutKeepsPartialOutput(t *testing.T) {
	session, _ := connectSession(t, t.TempDir())

	result := callTool(t, session, "run_shell", map[string]any{
		"command":     "echo before-the-hang; sleep 30",
		"timeout_sec": 1,
	})
	if !result.IsError {
		t.Fatal("a command that outran its timeout was reported as success")
	}
	if got := field(t, result, "stdout"); !strings.Contains(got.(string), "before-the-hang") {
		t.Errorf("stdout = %q, want the output produced before the timeout", got)
	}

	// Clients that render only the text content should see it too.
	var text strings.Builder
	for _, block := range result.Content {
		if content, ok := block.(*mcp.TextContent); ok {
			text.WriteString(content.Text)
		}
	}
	if !strings.Contains(text.String(), "before-the-hang") {
		t.Errorf("content = %q, want the partial output alongside the reason", text.String())
	}
	if !strings.Contains(text.String(), "timed out") {
		t.Errorf("content = %q, want it to say why the command was cut", text.String())
	}
}
