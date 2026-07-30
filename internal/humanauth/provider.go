// Package humanauth authenticates the humans who watch sandbox terminals and
// stop runaway sandboxes, as opposed to the agents that run in them (see
// internal/mcpserver).
package humanauth

import "net/http"

// Roles this app recognizes. Anything that is not RoleAdmin is read-only, so a
// new group added to the identity provider later can never become an accidental
// privilege escalation.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

// Identity is the provider-agnostic result of authenticating a request.
type Identity struct {
	Subject     string
	DisplayName string
	Role        string
}

// Provider authenticates an incoming HTTP request and returns the identity
// making it.
type Provider interface {
	Authenticate(r *http.Request) (*Identity, error)
}

// StubProvider always authenticates the same fixed test identity, with NO real
// credential check whatsoever — no session, no cookie, no external call.
//
// ⚠️ It exists only so the app can be run locally without a Keycloak instance,
// and is reachable only via --auth-stub. Anything past a trusted dev machine must
// use the OIDC provider: this UI streams the live terminal of every sandbox and
// can kill the processes running in them.
type StubProvider struct{}

func (StubProvider) Authenticate(r *http.Request) (*Identity, error) {
	return &Identity{
		Subject:     "stub-user",
		DisplayName: "Local Tester",
		Role:        RoleAdmin,
	}, nil
}
