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

	"github.com/JetManiack/go-ai-executor/internal/sandbox"
	"github.com/JetManiack/go-ai-executor/internal/storage"
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

// newTestManager builds a sandbox manager rooted in a per-test temp directory.
func newTestManager(t *testing.T) *sandbox.Manager {
	t.Helper()
	mgr, err := sandbox.NewManager(sandbox.Config{
		RootDir:        t.TempDir(),
		DefaultTimeout: 5 * time.Second,
		MaxOutputBytes: 64 << 10,
		Shell:          "/bin/sh",
		AllowedEnvs:    sandbox.DefaultConfig("").AllowedEnvs,
	})
	if err != nil {
		t.Fatalf("sandbox.NewManager: %v", err)
	}
	return mgr
}

func testDeps(t *testing.T, db *gorm.DB) Deps {
	t.Helper()
	return Deps{DB: db, Manager: newTestManager(t), Version: "test"}
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
