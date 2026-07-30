package mcpserver

import (
	"context"
	"fmt"

	"github.com/JetManiack/go-ai-executor/internal/sandbox"
	"github.com/JetManiack/go-ai-executor/internal/storage"
)

// sandboxForActor resolves the sandbox belonging to the authenticated agent,
// refusing if an administrator has blocked it.
//
// It fails closed on a missing actor. An earlier version defaulted to the shared
// root sandbox — unreachable through /mcp, which is wrapped in
// RequireAgentToken, but it would silently hand the root directory (every
// agent's sandbox at once) to any future entry point that forgot to
// authenticate.
//
// The block is read from the database on every call, with nothing cached in the
// process. A cache would need cross-replica invalidation: a block applied through
// one replica would stay invisible to another, so the emergency stop would hold
// or not depending on which replica the agent's next request happened to reach.
// One indexed SELECT is nothing next to the fork/exec it guards.
func sandboxForActor(ctx context.Context, deps Deps) (*sandbox.Sandbox, error) {
	actor, ok := ActorFromContext(ctx)
	if !ok {
		return nil, ErrNoActor
	}

	if deps.DB != nil {
		block, err := storage.ActiveSandboxBlock(deps.DB, actor.ID)
		if err != nil {
			// Fail closed: an unreadable block table must not be the way an
			// agent gets past a block.
			return nil, fmt.Errorf("cannot verify sandbox block state: %w", err)
		}
		if block != nil {
			return nil, blockedError(block)
		}
	}

	return deps.Manager.GetSandbox(actor.ID)
}

// blockedError phrases a block as something the agent can reason about: a
// decision a named human made at a stated time for a stated reason, not a
// transient failure worth retrying.
func blockedError(block *storage.SandboxBlock) error {
	return fmt.Errorf(
		"sandbox administratively blocked by %s at %s: %s — this is a human decision, not a transient error; do not retry, contact an operator to resume",
		block.BlockedByName,
		block.BlockedAt.Format("2006-01-02 15:04:05 MST"),
		block.Reason,
	)
}
