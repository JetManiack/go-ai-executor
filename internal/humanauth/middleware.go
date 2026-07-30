package humanauth

import (
	"context"
	"net/http"
)

type contextKey string

const humanIdentityKey contextKey = "human_identity"

func ContextWithHumanIdentity(ctx context.Context, identity *HumanIdentity) context.Context {
	return context.WithValue(ctx, humanIdentityKey, identity)
}

func HumanIdentityFromContext(ctx context.Context) (*HumanIdentity, bool) {
	identity, ok := ctx.Value(humanIdentityKey).(*HumanIdentity)
	return identity, ok
}

func RequireHumanSession(provider Provider, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := provider.AuthenticateRequest(r)
		if err != nil || identity == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		ctx := ContextWithHumanIdentity(r.Context(), identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
