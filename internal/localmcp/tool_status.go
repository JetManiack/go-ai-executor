package localmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JetManiack/go-ai-executor/internal/localexec"
)

type StatusInput struct{}

type StatusOutput struct {
	// An agent that does not know where it is will guess, and its guess will be
	// wrong in exactly the cases that matter — relative paths and repository
	// layout.
	Dir            string `json:"dir" jsonschema:"the working directory command lines run in"`
	Shell          string `json:"shell" jsonschema:"the shell command lines are passed to"`
	DefaultTimeout string `json:"default_timeout" jsonschema:"the timeout applied when a call does not set one"`
	MaxOutputBytes int    `json:"max_output_bytes" jsonschema:"per-stream cap on returned output"`

	// Stated in the tool output, not only in the README: an agent that believes it
	// is sandboxed will take risks it would not otherwise take.
	Sandboxed bool `json:"sandboxed" jsonschema:"always false: this helper runs with the operator's own privileges and confines nothing"`
}

func statusHandler(runner *localexec.Runner) mcp.ToolHandlerFor[StatusInput, StatusOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in StatusInput) (*mcp.CallToolResult, StatusOutput, error) {
		return nil, StatusOutput{
			Dir:            runner.Dir(),
			Shell:          runner.Shell(),
			DefaultTimeout: runner.DefaultTimeout().String(),
			MaxOutputBytes: runner.MaxOutputBytes(),
			Sandboxed:      false,
		}, nil
	}
}
