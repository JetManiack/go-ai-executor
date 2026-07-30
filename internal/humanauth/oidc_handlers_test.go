package humanauth

import (
	"context"
	"testing"
)

// OAuth state is CSRF protection, so it has to be unguessable and never repeat.
func TestRandomOAuthStateIsUniqueAndLong(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		state := randomOAuthState()
		if len(state) < 32 {
			t.Fatalf("state %q is only %d chars, too short to resist guessing", state, len(state))
		}
		if seen[state] {
			t.Fatalf("state %q was generated twice", state)
		}
		seen[state] = true
	}
}

// Discovery has to fail loudly with an unreachable issuer: the server must
// refuse to start rather than come up with authentication that can't work.
func TestNewOIDCHandlersFailsOnUnreachableIssuer(t *testing.T) {
	db := openTestDB(t)

	_, _, err := NewOIDCHandlers(context.Background(), db, OIDCConfig{
		Issuer:        "http://127.0.0.1:1/realms/nope",
		ClientID:      "executor",
		ClientSecret:  "secret",
		PublicURL:     "https://executor.example.com",
		AdminGroup:    "admins",
		EncryptionKey: testKey(t),
	})
	if err == nil {
		t.Fatal("NewOIDCHandlers succeeded against an unreachable issuer")
	}
}

func TestVerifyAndExtractMapsAdminGroup(t *testing.T) {
	tests := []struct {
		name     string
		groups   []string
		wantRole string
	}{
		{name: "bare group name", groups: []string{"admins"}, wantRole: "admin"},
		{name: "keycloak path form", groups: []string{"/admins"}, wantRole: "admin"},
		{name: "among others", groups: []string{"/devs", "/admins"}, wantRole: "admin"},
		{name: "not a member", groups: []string{"/devs"}, wantRole: "viewer"},
		{name: "no groups claim", groups: nil, wantRole: "viewer"},
		{name: "similar but different", groups: []string{"/administrators"}, wantRole: "viewer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// roleForGroups is the mapping verifyAndExtract applies; testing it
			// directly avoids needing a live issuer to build a token verifier.
			got := roleForGroups(tt.groups, "admins")
			if got != tt.wantRole {
				t.Errorf("role = %q, want %q", got, tt.wantRole)
			}
		})
	}
}
