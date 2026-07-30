// Package mcpserver exposes this service's sandbox tools over MCP: shell
// execution and filesystem access, each scoped to the calling agent's own
// jailed directory.
package mcpserver

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"

	"github.com/JetManiack/go-ai-executor/internal/sandbox"
)

// ServerName is the MCP implementation name advertised to clients.
const ServerName = "go-ai-executor"

// fallbackVersion is reported when Deps.Version is empty (an unstamped
// `go build`, as opposed to a release built through the Makefile).
const fallbackVersion = "dev"

// Deps is everything the MCP surface needs: the sandbox manager that owns one
// jailed directory per agent, and the database the agents' identities and
// block state live in.
type Deps struct {
	DB      *gorm.DB
	Manager *sandbox.Manager

	// Version is advertised to MCP clients as the server version.
	Version string
}

// RegisterTools adds every MCP tool this server exposes to server.
func RegisterTools(server *mcp.Server, deps Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "exec_command",
		Description: "Execute a shell command inside the sandbox directory with timeout and environment isolation",
	}, execCommandHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_file",
		Description: "Read file contents relative to the sandbox directory",
	}, readFileHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "write_file",
		Description: "Write text content to a file relative to the sandbox directory",
	}, writeFileHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_dir",
		Description: "List files and subdirectories inside the sandbox directory",
	}, listDirHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_file",
		Description: "Delete a file or directory inside the sandbox directory",
	}, deleteFileHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_sandbox_status",
		Description: "Get configuration status and root path of the sandbox environment",
	}, sandboxStatusHandler(deps))
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
