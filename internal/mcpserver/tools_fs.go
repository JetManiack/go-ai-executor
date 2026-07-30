package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"go-ai-executor/internal/sandbox"
)

func getAgentSandbox(ctx context.Context, mgr *sandbox.Manager) (*sandbox.Sandbox, error) {
	agentID, ok := AgentIDFromContext(ctx)
	if !ok {
		agentID = "default"
	}
	return mgr.GetSandbox(agentID)
}

// ReadFile
type ReadFileInput struct {
	Path string `json:"path" jsonschema:"file path relative to sandbox root"`
}

type ReadFileOutput struct {
	Content string `json:"content"`
	Path    string `json:"path"`
}

func readFilePathHandler(mgr *sandbox.Manager) mcp.ToolHandlerFor[ReadFileInput, ReadFileOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ReadFileInput) (*mcp.CallToolResult, ReadFileOutput, error) {
		if in.Path == "" {
			return nil, ReadFileOutput{}, fmt.Errorf("path cannot be empty")
		}
		sb, err := getAgentSandbox(ctx, mgr)
		if err != nil {
			return nil, ReadFileOutput{}, err
		}
		data, err := sb.ReadFile(in.Path)
		if err != nil {
			return nil, ReadFileOutput{}, fmt.Errorf("failed to read file: %w", err)
		}
		return nil, ReadFileOutput{
			Content: string(data),
			Path:    in.Path,
		}, nil
	}
}

// WriteFile
type WriteFileInput struct {
	Path    string `json:"path" jsonschema:"file path relative to sandbox root"`
	Content string `json:"content" jsonschema:"text content to write to the file"`
}

type WriteFileOutput struct {
	Success bool   `json:"success"`
	Path    string `json:"path"`
	Bytes   int    `json:"bytes"`
}

func writeFilePathHandler(mgr *sandbox.Manager) mcp.ToolHandlerFor[WriteFileInput, WriteFileOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in WriteFileInput) (*mcp.CallToolResult, WriteFileOutput, error) {
		if in.Path == "" {
			return nil, WriteFileOutput{}, fmt.Errorf("path cannot be empty")
		}
		sb, err := getAgentSandbox(ctx, mgr)
		if err != nil {
			return nil, WriteFileOutput{}, err
		}
		data := []byte(in.Content)
		if err := sb.WriteFile(in.Path, data, 0644); err != nil {
			return nil, WriteFileOutput{}, fmt.Errorf("failed to write file: %w", err)
		}
		return nil, WriteFileOutput{
			Success: true,
			Path:    in.Path,
			Bytes:   len(data),
		}, nil
	}
}

// ListDir
type ListDirInput struct {
	Path string `json:"path,omitempty" jsonschema:"directory path relative to sandbox root (defaults to root '.' if empty)"`
}

type ListDirOutput struct {
	Files []sandbox.FileInfo `json:"files"`
	Path  string             `json:"path"`
}

func listDirHandler(mgr *sandbox.Manager) mcp.ToolHandlerFor[ListDirInput, ListDirOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ListDirInput) (*mcp.CallToolResult, ListDirOutput, error) {
		path := in.Path
		if path == "" {
			path = "."
		}
		sb, err := getAgentSandbox(ctx, mgr)
		if err != nil {
			return nil, ListDirOutput{}, err
		}
		files, err := sb.ListDir(path)
		if err != nil {
			return nil, ListDirOutput{}, fmt.Errorf("failed to list directory: %w", err)
		}
		return nil, ListDirOutput{
			Files: files,
			Path:  path,
		}, nil
	}
}

// DeleteFile
type DeleteFileInput struct {
	Path string `json:"path" jsonschema:"file or directory path relative to sandbox root"`
}

type DeleteFileOutput struct {
	Success bool   `json:"success"`
	Path    string `json:"path"`
}

func deleteFileHandler(mgr *sandbox.Manager) mcp.ToolHandlerFor[DeleteFileInput, DeleteFileOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in DeleteFileInput) (*mcp.CallToolResult, DeleteFileOutput, error) {
		if in.Path == "" {
			return nil, DeleteFileOutput{}, fmt.Errorf("path cannot be empty")
		}
		sb, err := getAgentSandbox(ctx, mgr)
		if err != nil {
			return nil, DeleteFileOutput{}, err
		}
		if err := sb.DeleteFile(in.Path); err != nil {
			return nil, DeleteFileOutput{}, fmt.Errorf("failed to delete file: %w", err)
		}
		return nil, DeleteFileOutput{
			Success: true,
			Path:    in.Path,
		}, nil
	}
}

// GetStatus
type GetStatusInput struct{}

type GetStatusOutput struct {
	Status sandbox.SandboxStatus `json:"status"`
}

func getStatusHandler(mgr *sandbox.Manager) mcp.ToolHandlerFor[GetStatusInput, GetStatusOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in GetStatusInput) (*mcp.CallToolResult, GetStatusOutput, error) {
		sb, err := getAgentSandbox(ctx, mgr)
		if err != nil {
			return nil, GetStatusOutput{}, err
		}
		return nil, GetStatusOutput{
			Status: sb.GetStatus(),
		}, nil
	}
}
