package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JetManiack/go-ai-executor/internal/sandbox"
)

type SandboxStatusInput struct{}

type SandboxStatusOutput struct {
	Status sandbox.SandboxStatus `json:"status" jsonschema:"the calling agent's sandbox configuration and limits"`
}

func sandboxStatusHandler(mgr *sandbox.Manager) mcp.ToolHandlerFor[SandboxStatusInput, SandboxStatusOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in SandboxStatusInput) (*mcp.CallToolResult, SandboxStatusOutput, error) {
		sb, err := sandboxForActor(ctx, mgr)
		if err != nil {
			return nil, SandboxStatusOutput{}, err
		}
		return nil, SandboxStatusOutput{Status: sb.GetStatus()}, nil
	}
}
