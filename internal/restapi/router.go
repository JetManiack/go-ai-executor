package restapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/JetManiack/go-ai-executor/internal/humanauth"
	"github.com/JetManiack/go-ai-executor/internal/sandbox"
)

// Options is what the REST API needs: the database, the sandbox manager whose
// terminals it streams and whose processes it kills, and the provider that
// authenticates the humans doing it.
type Options struct {
	DB           *gorm.DB
	Manager      *sandbox.Manager
	AuthProvider humanauth.Provider
}

// NewRouter builds the API handler, mounted with no path prefix (the caller
// mounts it under /api — see cmd/executor/main.go).
//
// Every route requires a human session. Reading — the sandbox list and the
// terminal stream — is open to any authenticated human; everything that changes
// state, meaning the emergency block and agent credentials, additionally
// requires role admin.
func NewRouter(opts Options) http.Handler {
	r := chi.NewRouter()
	r.Use(humanauth.RequireHumanAuth(opts.DB, opts.AuthProvider))

	r.Get("/me", meHandler())

	r.Route("/sandboxes", func(r chi.Router) {
		r.Get("/", listSandboxesHandler(opts))
		r.Get("/{id}", getSandboxHandler(opts))
		r.Get("/{id}/stream", streamSandboxHandler(opts))

		r.Group(func(r chi.Router) {
			r.Use(humanauth.RequireAdmin)
			r.Post("/{id}/block", blockSandboxHandler(opts))
			r.Delete("/{id}/block", releaseSandboxHandler(opts))
		})
	})

	r.Route("/agents", func(r chi.Router) {
		r.Use(humanauth.RequireAdmin)
		r.Get("/", listAgentsHandler(opts.DB))
		r.Post("/", createAgentHandler(opts.DB))
		r.Delete("/{id}", deleteAgentHandler(opts.DB))
		r.Get("/{id}/tokens", listAgentTokensHandler(opts.DB))
		r.Post("/{id}/tokens", issueTokenHandler(opts.DB))
		r.Delete("/{id}/tokens/{tokenID}", revokeTokenHandler(opts.DB))
	})

	return r
}
