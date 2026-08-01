package mcpserver

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JetManiack/go-ai-executor/internal/storage"
)

// workerNamer is implemented by the hub. Consulted through an optional interface
// rather than added to Executor, because knowing which worker served a call is
// the journal's concern and not something a tool handler needs.
type workerNamer interface {
	WorkerFor(agentID string) (string, bool)
}

// audited wraps a tool handler so every call leaves two rows in the journal: one
// before it is attempted and one when it returns.
//
// Wrapped at registration rather than written inside each handler, so a tool
// added later is journalled by construction instead of by remembering. The cost
// is one insert on each side of a call that is already crossing a process
// boundary.
func audited[In, Out any](
	deps Deps,
	action string,
	target func(In) string,
	h mcp.ToolHandlerFor[In, Out],
) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		// No database, or no identity to attribute the action to: the call still
		// runs. A journal that can refuse work is a journal that eventually gets
		// switched off.
		actor, ok := ActorFromContext(ctx)
		if deps.DB == nil || !ok {
			return h(ctx, req, in)
		}

		var describes string
		if target != nil {
			describes = target(in)
		}
		var workerID string
		if namer, ok := deps.Executor.(workerNamer); ok {
			workerID, _ = namer.WorkerFor(actor.ID)
		}

		record := storage.AuditRecord{
			Actor: actor, Action: action, Target: describes, WorkerID: workerID,
		}
		callID, err := storage.AppendAuditStarted(deps.DB, record)
		if err != nil {
			slog.Error("could not journal the start of a tool call", "action", action, "error", err)
		}
		record.CallID = callID

		began := time.Now()
		result, out, callErr := h(ctx, req, in)

		record.DurationMs = time.Since(began).Milliseconds()
		record.Outcome = storage.AuditOutcomeOK
		// A handler that decided something the error does not carry says so here.
		// A command cut short by its timeout returns no error — the tool call
		// succeeded in reporting it — so without this the journal records the one
		// word that hides a kill the service performed.
		if reported, ok := any(out).(interface{ auditOutcome() string }); ok {
			if outcome := reported.auditOutcome(); outcome != "" {
				record.Outcome = outcome
			}
		}
		if coded, ok := any(out).(interface{ auditExitCode() int }); ok {
			code := coded.auditExitCode()
			record.ExitCode = &code
		}
		switch {
		case callErr != nil && errors.Is(callErr, ErrSandboxBlocked):
			// An administrator's decision is not a failure, and a journal that
			// files the two together cannot answer "was this agent stopped".
			record.Outcome = storage.AuditOutcomeBlocked
			record.Error = callErr.Error()
		case callErr != nil:
			record.Outcome = storage.AuditOutcomeError
			record.Error = callErr.Error()
		}
		// The worker may have been resolved only by the call itself, for an agent
		// that had never been pinned before.
		if record.WorkerID == "" {
			if namer, ok := deps.Executor.(workerNamer); ok {
				record.WorkerID, _ = namer.WorkerFor(actor.ID)
			}
		}
		if sized, ok := any(out).(interface{ auditBytes() int }); ok {
			record.Bytes = sized.auditBytes()
		}

		if err := storage.AppendAuditFinished(deps.DB, record); err != nil {
			slog.Error("could not journal the end of a tool call", "action", action, "error", err)
		}
		return result, out, callErr
	}
}

// The outputs that carry a size say so, so the wrapper can record it without
// knowing what any particular tool returns.
//
// ExecID is deliberately not journalled: the tool does not return one, and the
// row would have to invent a link to the stream that does not exist. Correlating
// a journal entry with its output is by actor and time until the tool surfaces
// the id it already has internally.

func (o ExecCommandOutput) auditBytes() int      { return len(o.Stdout) + len(o.Stderr) }
func (o ExecCommandOutput) auditOutcome() string { return o.outcome }
func (o ExecCommandOutput) auditExitCode() int   { return o.ExitCode }
func (o ReadFileOutput) auditBytes() int         { return len(o.Content) }
func (o WriteFileOutput) auditBytes() int        { return o.Bytes }
