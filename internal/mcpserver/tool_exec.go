package mcpserver

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JetManiack/go-ai-executor/internal/sandbox"
)

type ExecCommandInput struct {
	Command    string `json:"command" jsonschema:"the shell command string to execute inside the sandbox"`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"optional execution timeout in seconds (default: 30)"`
	WorkDir    string `json:"work_dir,omitempty" jsonschema:"optional working directory relative to sandbox root"`
}

type ExecCommandOutput struct {
	Stdout     string `json:"stdout" jsonschema:"everything the command wrote to stdout, up to the server's output cap"`
	Stderr     string `json:"stderr" jsonschema:"everything the command wrote to stderr, up to the server's output cap"`
	ExitCode   int    `json:"exit_code" jsonschema:"the command's exit status; -1 when it was killed by a timeout"`
	DurationMs int64  `json:"duration_ms" jsonschema:"wall-clock execution time in milliseconds"`
	Truncated  bool   `json:"truncated" jsonschema:"true when output exceeded the server's cap and was cut"`
}

func execCommandHandler(deps Deps) mcp.ToolHandlerFor[ExecCommandInput, ExecCommandOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ExecCommandInput) (*mcp.CallToolResult, ExecCommandOutput, error) {
		if in.Command == "" {
			return nil, ExecCommandOutput{}, errors.New("command cannot be empty")
		}

		actor, ok := ActorFromContext(ctx)
		if !ok {
			return nil, ExecCommandOutput{}, ErrNoActor
		}
		sb, err := deps.Manager.GetSandbox(actor.ID)
		if err != nil {
			return nil, ExecCommandOutput{}, err
		}

		var timeout time.Duration
		if in.TimeoutSec > 0 {
			timeout = time.Duration(in.TimeoutSec) * time.Second
		}

		res, execErr := sb.ExecCommand(ctx, in.Command, timeout, in.WorkDir)
		output := ExecCommandOutput{
			Stdout:     res.Stdout,
			Stderr:     res.Stderr,
			ExitCode:   res.ExitCode,
			DurationMs: res.DurationMs,
			Truncated:  res.Truncated,
		}

		now := time.Now().UTC()
		execID := uuid.NewString()

		deps.Manager.Broadcaster().Publish(sandbox.ExecEvent{
			ID:         execID,
			AgentID:    actor.ID,
			Command:    in.Command,
			WorkDir:    in.WorkDir,
			Stdout:     res.Stdout,
			Stderr:     res.Stderr,
			ExitCode:   res.ExitCode,
			DurationMs: res.DurationMs,
			Truncated:  res.Truncated,
			Timestamp:  now,
		})

		// A command that exits non-zero is a successful tool call reporting a
		// failed command, so only a genuine execution failure (timeout, missing
		// shell) becomes a tool error. The output is returned either way.
		if execErr != nil {
			return nil, output, execErr
		}
		return nil, output, nil
	}
}
