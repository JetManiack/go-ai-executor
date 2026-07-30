package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"

	"go-ai-executor/internal/sandbox"
	"go-ai-executor/internal/storage"
)

type ExecCommandInput struct {
	Command    string `json:"command" jsonschema:"the shell command string to execute inside the sandbox"`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"optional execution timeout in seconds (default: 30)"`
	WorkDir    string `json:"work_dir,omitempty" jsonschema:"optional working directory relative to sandbox root"`
}

type ExecCommandOutput struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	Truncated  bool   `json:"truncated"`
}

func execCommandHandler(mgr *sandbox.Manager, db *gorm.DB) mcp.ToolHandlerFor[ExecCommandInput, ExecCommandOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ExecCommandInput) (*mcp.CallToolResult, ExecCommandOutput, error) {
		if in.Command == "" {
			return nil, ExecCommandOutput{}, fmt.Errorf("command cannot be empty")
		}

		agentID, _ := AgentIDFromContext(ctx)
		sb, err := mgr.GetSandbox(agentID)
		if err != nil {
			return nil, ExecCommandOutput{}, fmt.Errorf("failed to get agent sandbox: %w", err)
		}

		var timeout time.Duration
		if in.TimeoutSec > 0 {
			timeout = time.Duration(in.TimeoutSec) * time.Second
		}

		res, err := sb.ExecCommand(ctx, in.Command, timeout, in.WorkDir)
		output := ExecCommandOutput{
			Stdout:     res.Stdout,
			Stderr:     res.Stderr,
			ExitCode:   res.ExitCode,
			DurationMs: res.DurationMs,
			Truncated:  res.Truncated,
		}

		now := time.Now().UTC()
		logID := uuid.New().String()

		// Publish to live stream subscribers
		mgr.GetBroadcaster().Publish(sandbox.ExecEvent{
			ID:         logID,
			AgentID:    agentID,
			Command:    in.Command,
			WorkDir:    in.WorkDir,
			Stdout:     res.Stdout,
			Stderr:     res.Stderr,
			ExitCode:   res.ExitCode,
			DurationMs: res.DurationMs,
			Truncated:  res.Truncated,
			Timestamp:  now,
		})

		// Record in database if DB is configured
		if db != nil && agentID != "default" {
			_ = storage.RecordExecLog(db, &storage.ExecLog{
				ID:         logID,
				AgentID:    agentID,
				Command:    in.Command,
				WorkDir:    in.WorkDir,
				Stdout:     res.Stdout,
				Stderr:     res.Stderr,
				ExitCode:   res.ExitCode,
				DurationMs: res.DurationMs,
				Truncated:  res.Truncated,
				CreatedAt:  now,
			})
		}

		if err != nil {
			return nil, output, fmt.Errorf("execution error: %w", err)
		}

		return nil, output, nil
	}
}
