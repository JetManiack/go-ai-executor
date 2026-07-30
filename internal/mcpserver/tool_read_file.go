package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JetManiack/go-ai-executor/internal/sandbox"
)

type ReadFileInput struct {
	Path string `json:"path" jsonschema:"file path relative to sandbox root"`
}

type ReadFileOutput struct {
	Path    string `json:"path" jsonschema:"the path that was read, as given"`
	Content string `json:"content" jsonschema:"the file's contents"`
}

func readFileHandler(mgr *sandbox.Manager) mcp.ToolHandlerFor[ReadFileInput, ReadFileOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ReadFileInput) (*mcp.CallToolResult, ReadFileOutput, error) {
		if in.Path == "" {
			return nil, ReadFileOutput{}, errors.New("path cannot be empty")
		}
		sb, err := sandboxForActor(ctx, mgr)
		if err != nil {
			return nil, ReadFileOutput{}, err
		}
		data, err := sb.ReadFile(in.Path)
		if err != nil {
			return nil, ReadFileOutput{}, fmt.Errorf("read file: %w", err)
		}
		return nil, ReadFileOutput{Path: in.Path, Content: string(data)}, nil
	}
}
