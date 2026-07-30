package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type DeleteFileInput struct {
	Path string `json:"path" jsonschema:"file or directory path relative to sandbox root; directories are removed recursively"`
}

type DeleteFileOutput struct {
	Path string `json:"path" jsonschema:"the path that was deleted, as given"`
}

func deleteFileHandler(deps Deps) mcp.ToolHandlerFor[DeleteFileInput, DeleteFileOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in DeleteFileInput) (*mcp.CallToolResult, DeleteFileOutput, error) {
		if in.Path == "" {
			return nil, DeleteFileOutput{}, errors.New("path cannot be empty")
		}
		sb, err := sandboxForActor(ctx, deps)
		if err != nil {
			return nil, DeleteFileOutput{}, err
		}
		if err := sb.DeleteFile(in.Path); err != nil {
			return nil, DeleteFileOutput{}, fmt.Errorf("delete: %w", err)
		}
		return nil, DeleteFileOutput{Path: in.Path}, nil
	}
}
