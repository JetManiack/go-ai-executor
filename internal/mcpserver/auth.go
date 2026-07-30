package mcpserver

import (
	"context"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"go-ai-executor/internal/storage"
)

type agentContextKey string

const agentIDContextKey agentContextKey = "agent_id"

func ContextWithAgentID(ctx context.Context, agentID string) context.Context {
	return context.WithValue(ctx, agentIDContextKey, agentID)
}

func AgentIDFromContext(ctx context.Context) (string, bool) {
	agentID, ok := ctx.Value(agentIDContextKey).(string)
	return agentID, ok
}

// RequireAgentToken validates the HTTP Bearer token against the AgentCredential database.
func RequireAgentToken(db *gorm.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			http.Error(w, `{"error":"invalid authorization header format, expected Bearer <token>"}`, http.StatusUnauthorized)
			return
		}

		rawToken := parts[1]
		actor, err := storage.AuthenticateAgentToken(db, rawToken)
		if err != nil {
			http.Error(w, `{"error":"invalid or revoked bearer token"}`, http.StatusUnauthorized)
			return
		}

		ctx := ContextWithAgentID(r.Context(), actor.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
