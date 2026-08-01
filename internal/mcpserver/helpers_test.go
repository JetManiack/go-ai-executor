package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"

	"github.com/JetManiack/go-ai-executor/internal/storage"
	"github.com/JetManiack/go-ai-executor/internal/workertest"
)

// openTestDB gives each test its own migrated SQLite database. The Postgres
// backend is covered by internal/storage's suite; what matters here is the MCP
// behavior on top of it, so this deliberately uses the zero-setup backend.
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	return db
}

func mustAgent(t *testing.T, db *gorm.DB, name string) *storage.Actor {
	t.Helper()
	agent, err := storage.CreateAgent(db, name)
	if err != nil {
		t.Fatalf("CreateAgent(%q): %v", name, err)
	}
	return agent
}

// mustAgentWithToken registers an agent and issues it a usable bearer token.
func mustAgentWithToken(t *testing.T, db *gorm.DB, name string) (*storage.Actor, string) {
	t.Helper()
	agent := mustAgent(t, db, name)
	token, _, err := storage.IssueAgentToken(db, agent.ID)
	if err != nil {
		t.Fatalf("IssueAgentToken: %v", err)
	}
	return agent, token
}

// testDeps wires the MCP surface to a real worker over a real link.
//
// A fake executor would be cheaper and would pass while the wire contract was
// broken: execution crosses a process boundary now, so these tests are only worth
// as much as the link they run over.
func testDeps(t *testing.T, db *gorm.DB) Deps {
	t.Helper()
	return Deps{DB: db, Executor: workertest.StartOne(t).Hub, Version: "test"}
}

// bearerTransport attaches a fixed bearer token to every request, the way a
// configured MCP client would.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	if b.token != "" {
		clone.Header.Set("Authorization", "Bearer "+b.token)
	}
	base := b.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// newTestServer boots the real /mcp handler over HTTP, so auth, transport and
// tool dispatch are exercised on the same path a real agent takes.
func newTestServer(t *testing.T, deps Deps) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(NewHTTPHandler(deps))
	t.Cleanup(server.Close)
	return server
}

// callTool invokes name on session and fails the test on a transport-level
// error. A tool that returns an error to the agent is not a transport error:
// it comes back with CallToolResult.IsError set, which callers assert on.
func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q): %v", name, err)
	}
	return result
}

// contentText flattens a tool result's content blocks into one string, for
// tests that only care whether some substring is present.
func contentText(content []mcp.Content) string {
	var sb strings.Builder
	for _, block := range content {
		if text, ok := block.(*mcp.TextContent); ok {
			sb.WriteString(text.Text)
		}
	}
	return sb.String()
}

// outputField reads one field of a tool's structured output.
//
// Preferred over substring-matching the text content when the value can contain
// characters encoding/json escapes: `&&` arrives as \u0026\u0026 and `>` as
// \u003e in the JSON text, so a raw substring check on it fails for output that
// is in fact correct.
func outputField(t *testing.T, result *mcp.CallToolResult, key string) string {
	t.Helper()
	fields, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content is %T, want a JSON object", result.StructuredContent)
	}
	value, present := fields[key]
	if !present {
		t.Fatalf("structured output has no %q field: %v", key, fields)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("field %q is %T, want a string", key, value)
	}
	return text
}

// connectSession returns a connected MCP client session against server,
// authenticating with token.
func connectSession(t *testing.T, server *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           &http.Client{Transport: bearerTransport{token: token}},
		DisableStandaloneSSE: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
