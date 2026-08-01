package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/JetManiack/go-ai-executor/internal/storage"
)

// agentForCall resolves the authenticated agent and refuses if an administrator
// has blocked its sandbox.
//
// It fails closed on a missing actor: without one there is no sandbox to name, and
// guessing would mean dispatching somebody else's work.
//
// The block is read from the database on every call, with nothing cached in the
// process. A cache would need cross-replica invalidation: a block applied through
// one replica would stay invisible to another, so the emergency stop would hold
// or not depending on which replica the agent's next request happened to reach.
// One indexed SELECT is nothing next to the fork/exec it guards.
func agentForCall(ctx context.Context, deps Deps) (string, error) {
	actor, ok := ActorFromContext(ctx)
	if !ok {
		return "", ErrNoActor
	}

	if deps.DB != nil {
		block, err := storage.ActiveSandboxBlock(deps.DB, actor.ID)
		if err != nil {
			// Fail closed: an unreadable block table must not be the way an
			// agent gets past a block.
			return "", fmt.Errorf("cannot verify sandbox block state: %w", err)
		}
		if block != nil {
			return "", blockedError(block)
		}
	}

	// Checked here rather than in the worker, which has no database — and would
	// need one, plus its credentials, to check.
	return actor.ID, nil
}

// ErrSandboxBlocked marks a refusal as an administrator's decision rather than a
// failure. Wrapped rather than returned bare, so the agent still gets the whole
// sentence while the journal can tell the two apart — "blocked" and "error" are
// different entries in a record of what happened.
var ErrSandboxBlocked = errors.New("sandbox is administratively blocked")

// blockedError phrases a block as something the agent can reason about: a
// decision a named human made at a stated time for a stated reason, not a
// transient failure worth retrying.
func blockedError(block *storage.SandboxBlock) error {
	return fmt.Errorf(
		"%w by %s at %s: %s — this is a human decision, not a transient error; do not retry, contact an operator to resume",
		ErrSandboxBlocked,
		block.BlockedByName,
		block.BlockedAt.Format("2006-01-02 15:04:05 MST"),
		block.Reason,
	)
}
