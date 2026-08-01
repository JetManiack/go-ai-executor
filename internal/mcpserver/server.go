// Package mcpserver exposes this service's sandbox tools over MCP: shell
// execution and filesystem access, each scoped to the calling agent's own
// jailed directory.
package mcpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"

	"github.com/JetManiack/go-ai-executor/internal/workerproto"
)

// Executor is where commands actually run: a pool of worker processes reached over
// the worker link. The interface exists so this package depends on the operations
// rather than on the transport, and so a test can drive a fake without standing up
// a worker.
//
// Every method takes the agent ID because execution is no longer in this process:
// the hub uses it to route to the worker holding that agent's sandbox.
type Executor interface {
	Exec(ctx context.Context, agentID string, in workerproto.ExecRequest) (workerproto.ExecResponse, error)
	ReadFile(ctx context.Context, agentID, path string) (string, error)
	WriteFile(ctx context.Context, agentID, path, content string) (int, error)
	ListDir(ctx context.Context, agentID, path string) ([]workerproto.FileInfo, error)
	DeleteFile(ctx context.Context, agentID, path string) (workerproto.DeleteFileResponse, error)
	Status(ctx context.Context, agentID string) (workerproto.StatusResponse, error)
	Kill(ctx context.Context, agentID, byActor, reason string) (int, error)
}

// ServerName is the MCP implementation name advertised to clients.
const ServerName = "go-ai-executor"

// fallbackVersion is reported when Deps.Version is empty (an unstamped
// `go build`, as opposed to a release built through the Makefile).
const fallbackVersion = "dev"

// Deps is everything the MCP surface needs: the executor that runs the work, and
// the database the agents' identities and block state live in.
type Deps struct {
	DB       *gorm.DB
	Executor Executor

	// Version is advertised to MCP clients as the server version.
	Version string
}

// RegisterTools adds every MCP tool this server exposes to server.
func RegisterTools(server *mcp.Server, deps Deps) {
	// Each handler is wrapped for the journal here rather than journalling inside
	// itself, so a tool added later is recorded by construction. The function
	// beside each name is what that tool's row says it was aimed at.
	mcp.AddTool(server, &mcp.Tool{
		Name:        "exec_command",
		Description: "Execute a shell command inside the sandbox directory with timeout and environment isolation",
	}, audited(deps, "exec_command", func(in ExecCommandInput) string {
		// Quoted as a vector rather than joined into a line. exec_command takes an
		// argument vector precisely so no shell reinterprets it, and flattening it
		// for the journal put that back: ["sh" "-c" "a & b"] read back as a shell
		// line means something else entirely.
		return fmt.Sprintf("%q", append([]string{in.Command}, in.Args...))
	}, execCommandHandler(deps)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_file",
		Description: "Read file contents relative to the sandbox directory",
	}, audited(deps, "read_file", func(in ReadFileInput) string { return in.Path }, readFileHandler(deps)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "write_file",
		Description: "Write text content to a file relative to the sandbox directory",
	}, audited(deps, "write_file", func(in WriteFileInput) string { return in.Path }, writeFileHandler(deps)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_dir",
		Description: "List files and subdirectories inside the sandbox directory",
	}, audited(deps, "list_dir", func(in ListDirInput) string { return in.Path }, listDirHandler(deps)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_file",
		Description: "Delete a file or directory inside the sandbox directory",
	}, audited(deps, "delete_file", func(in DeleteFileInput) string { return in.Path }, deleteFileHandler(deps)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_sandbox_status",
		Description: "Get configuration status and root path of the sandbox environment",
	}, audited(deps, "get_sandbox_status", nil, sandboxStatusHandler(deps)))
}

// NewServer builds the MCP server with every tool registered.
func NewServer(deps Deps) *mcp.Server {
	version := deps.Version
	if version == "" {
		version = fallbackVersion
	}
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: version}, nil)
	RegisterTools(server, deps)
	return server
}

// NewHTTPHandler builds the full /mcp handler: Streamable HTTP transport,
// every registered tool, wrapped in bearer-token authentication.
func NewHTTPHandler(deps Deps) http.Handler {
	server := NewServer(deps)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	return RequireAgentToken(deps.DB, mcpHandler)
}
