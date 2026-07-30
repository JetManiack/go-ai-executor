package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JetManiack/go-ai-executor/internal/sandbox"
)

type WriteFileInput struct {
	Path    string `json:"path" jsonschema:"file path relative to sandbox root"`
	Content string `json:"content" jsonschema:"text content to write to the file"`
}

type WriteFileOutput struct {
	Path  string `json:"path" jsonschema:"the path that was written, as given"`
	Bytes int    `json:"bytes" jsonschema:"number of bytes written"`
}

func writeFileHandler(mgr *sandbox.Manager) mcp.ToolHandlerFor[WriteFileInput, WriteFileOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in WriteFileInput) (*mcp.CallToolResult, WriteFileOutput, error) {
		if in.Path == "" {
			return nil, WriteFileOutput{}, errors.New("path cannot be empty")
		}
		sb, err := sandboxForActor(ctx, mgr)
		if err != nil {
			return nil, WriteFileOutput{}, err
		}
		data := []byte(in.Content)
		if err := sb.WriteFile(in.Path, data, 0o644); err != nil {
			return nil, WriteFileOutput{}, fmt.Errorf("write file: %w", err)
		}
		return nil, WriteFileOutput{Path: in.Path, Bytes: len(data)}, nil
	}
}
