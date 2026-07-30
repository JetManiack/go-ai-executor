package humanauth

import (
	"net/http"

	"github.com/JetManiack/go-ai-executor/internal/storage"
)

type HumanIdentity struct {
	Actor    *storage.Actor        `json:"actor"`
	Identity *storage.UserIdentity `json:"identity"`
}

type Provider interface {
	// AuthenticateRequest extracts identity from request (either via cookie or stub)
	AuthenticateRequest(r *http.Request) (*HumanIdentity, error)
}
