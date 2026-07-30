package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"github.com/JetManiack/go-ai-executor/internal/storage"
)

type contextKey string

const actorContextKey contextKey = "actor"

// ErrNoActor is returned by tool handlers reached without an authenticated
// actor in the request context.
var ErrNoActor = errors.New("no authenticated actor in context")

func withActor(ctx context.Context, actor *storage.Actor) context.Context {
	return context.WithValue(ctx, actorContextKey, actor)
}

// ActorFromContext returns the Actor authenticated for the current request,
// if any. Tool handlers need the whole Actor, not just its ID: the ID selects
// the sandbox directory, and the display name goes into the terminal stream
// and into the message an agent sees when its sandbox is blocked.
func ActorFromContext(ctx context.Context) (*storage.Actor, bool) {
	actor, ok := ctx.Value(actorContextKey).(*storage.Actor)
	return actor, ok
}

// WithActorForTesting injects actor into ctx the same way RequireAgentToken
// does, for tests that exercise tool handlers without an HTTP round trip.
func WithActorForTesting(ctx context.Context, actor *storage.Actor) context.Context {
	return withActor(ctx, actor)
}

// RequireAgentToken authenticates every request by its Authorization: Bearer
// header and injects the resulting Actor into the request context for
// downstream MCP tool handlers to read via ActorFromContext.
func RequireAgentToken(db *gorm.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="executor"`)
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}

		actor, err := storage.AuthenticateAgentToken(db, token)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="executor", error="invalid_token"`)
			http.Error(w, "invalid or revoked token", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r.WithContext(withActor(r.Context(), actor)))
	})
}
