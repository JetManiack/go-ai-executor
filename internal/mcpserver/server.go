package mcpserver

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"

	"go-ai-executor/internal/sandbox"
)

// RegisterTools registers all MCP tools exposed by the sandbox executor.
func RegisterTools(server *mcp.Server, mgr *sandbox.Manager, db *gorm.DB) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "exec_command",
		Description: "Execute a shell command inside the sandbox directory with timeout and environment isolation",
	}, execCommandHandler(mgr, db))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_file",
		Description: "Read file contents relative to the sandbox directory",
	}, readFilePathHandler(mgr))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "write_file",
		Description: "Write text content to a file relative to the sandbox directory",
	}, writeFilePathHandler(mgr))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_dir",
		Description: "List files and subdirectories inside the sandbox directory",
	}, listDirHandler(mgr))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_file",
		Description: "Delete a file or directory inside the sandbox directory",
	}, deleteFileHandler(mgr))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_sandbox_status",
		Description: "Get configuration status and root path of the sandbox environment",
	}, getStatusHandler(mgr))
}

// NewServer creates a new configured MCP server instance.
func NewServer(mgr *sandbox.Manager, db *gorm.DB) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "go-ai-executor",
		Version: "0.1.0",
	}, nil)

	RegisterTools(server, mgr, db)
	return server
}

// NewHTTPHandler returns an HTTP handler for Streamable HTTP transport with DB token authentication.
func NewHTTPHandler(mgr *sandbox.Manager, db *gorm.DB) http.Handler {
	server := NewServer(mgr, db)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	return RequireAgentToken(db, mcpHandler)
}

// ServeStdio runs the MCP server listening on stdin/stdout.
func ServeStdio(ctx context.Context, mgr *sandbox.Manager, db *gorm.DB) error {
	server := NewServer(mgr, db)
	return server.Run(ctx, &mcp.StdioTransport{})
}
