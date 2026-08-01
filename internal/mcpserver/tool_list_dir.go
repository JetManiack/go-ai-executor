package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JetManiack/go-ai-executor/internal/workerproto"
)

type ListDirInput struct {
	Path string `json:"path,omitempty" jsonschema:"directory path relative to sandbox root; defaults to the sandbox root"`
}

type ListDirOutput struct {
	Path  string                 `json:"path" jsonschema:"the directory that was listed"`
	Files []workerproto.FileInfo `json:"files" jsonschema:"entries in the directory, non-recursive"`
}

func listDirHandler(deps Deps) mcp.ToolHandlerFor[ListDirInput, ListDirOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ListDirInput) (*mcp.CallToolResult, ListDirOutput, error) {
		path := in.Path
		if path == "" {
			path = "."
		}
		agentID, err := agentForCall(ctx, deps)
		if err != nil {
			return nil, ListDirOutput{}, err
		}
		files, err := deps.Executor.ListDir(ctx, agentID, path)
		if err != nil {
			return nil, ListDirOutput{}, fmt.Errorf("list directory: %w", err)
		}
		// An empty directory must serialize as [] rather than null: a client
		// distinguishing "no entries" from "field absent" would otherwise see the
		// latter.
		if files == nil {
			files = []workerproto.FileInfo{}
		}
		return nil, ListDirOutput{Path: path, Files: files}, nil
	}
}
