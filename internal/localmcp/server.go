// Package localmcp is the MCP surface of the local stdio helper: two tools over
// stdin/stdout, no HTTP, no authentication, no web UI.
//
// Authentication is absent because there is nothing to authenticate. The client
// is whoever started the process, the transport is that process's own stdin and
// stdout, and a token would only be a token the operator hands to themselves.
package localmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JetManiack/go-ai-executor/internal/localexec"
)

// ServerName is the MCP implementation name advertised to clients. It is distinct
// from the server binary's so a client's logs say which one it is talking to.
const ServerName = "go-ai-executor-local"

const fallbackVersion = "dev"

// NewServer builds the MCP server with both tools registered.
func NewServer(runner *localexec.Runner, version string) *mcp.Server {
	if version == "" {
		version = fallbackVersion
	}
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: version}, nil)
	RegisterTools(server, runner)
	return server
}

// RegisterTools adds every tool this helper exposes to server.
func RegisterTools(server *mcp.Server, runner *localexec.Runner) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "run_shell",
		// Named for what it is rather than mirroring the server's exec_command:
		// that tool takes a program and an argument vector, this one takes a
		// command line for a shell. Sharing a name across two different schemas
		// would mislead any agent that talks to both.
		Description: "Run a shell command line in the local working directory. Pipes, redirection, globs and && work: the line is passed to the shell as written.",
	}, runShellHandler(runner))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_status",
		Description: "Report the working directory, shell, default timeout and output cap this helper is using",
	}, statusHandler(runner))
}

// Serve runs the MCP server over stdin/stdout until ctx is cancelled or the
// client disconnects.
func Serve(ctx context.Context, runner *localexec.Runner, version string) error {
	return NewServer(runner, version).Run(ctx, &mcp.StdioTransport{})
}
