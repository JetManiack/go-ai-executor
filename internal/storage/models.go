package storage

import "time"

// ActorKind distinguishes an AI agent from a human user. Both share the Actor
// table so every other table needs only a single foreign key, regardless of
// which kind of actor it points to — a SandboxBlock references the agent it
// stops and the human who stopped it through the same column type.
type ActorKind string

const (
	ActorKindAgent ActorKind = "agent"
	ActorKindHuman ActorKind = "human"
)

type Actor struct {
	ID          string    `gorm:"type:char(36);primaryKey" json:"id"`
	DisplayName string    `gorm:"not null;uniqueIndex" json:"display_name"`
	Kind        ActorKind `gorm:"type:varchar(10);not null;index" json:"kind"`
	CreatedAt   time.Time `json:"created_at"`
}

type AgentCredential struct {
	ID         string     `gorm:"type:char(36);primaryKey" json:"id"`
	ActorID    string     `gorm:"type:char(36);not null;index" json:"actor_id"`
	TokenHash  string     `gorm:"type:char(64);not null;uniqueIndex" json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// UserIdentity links a human Actor to the OIDC subject that authenticated it,
// and carries the role that subject's group membership maps to.
type UserIdentity struct {
	ActorID         string `gorm:"type:char(36);primaryKey"`
	KeycloakSubject string `gorm:"not null;uniqueIndex"`
	Role            string `gorm:"type:varchar(10);not null"`
}

// Session is a server-side OIDC login session, keyed by an opaque ID stored in
// the browser's session cookie. ExpiresAt is NOT the session's final expiry —
// it's a short checkpoint after which humanauth.OIDCProvider re-validates the
// session against Keycloak using RefreshToken (re-reading claims, so a
// role/group change propagates); the session's true maximum lifetime is bounded
// by how long Keycloak's own refresh token stays valid, which isn't tracked
// separately here.
type Session struct {
	ID           string `gorm:"type:char(64);primaryKey"` // a SHA-256 hex digest (humanauth.hashSessionID), not a UUID — 64 chars, not 36
	Subject      string `gorm:"not null;index"`
	DisplayName  string `gorm:"not null"`
	Role         string `gorm:"type:varchar(10);not null"`
	RefreshToken string `gorm:"not null"`
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

// SandboxBlock is an administrator's emergency stop on one agent's sandbox: its
// running processes are killed and every subsequent tool call is refused until a
// human releases it.
//
// This is operational state, not a journal — one row per incident, answering
// exactly one question ("is this agent blocked right now"). It lives in the
// database rather than in memory because an in-memory flag would be cleared by
// any restart: a rollout, an eviction or an OOM-kill would silently release a
// block nobody decided to release, and the UI would show the sandbox running
// again with no trace of why.
//
// ReleasedAt nil means the block is active.
type SandboxBlock struct {
	ID      string `gorm:"type:char(36);primaryKey" json:"id"`
	ActorID string `gorm:"type:char(36);not null;index" json:"actor_id"`

	// ActiveKey holds ActorID while the block is active and NULL once it's
	// released. The unique index on it is what enforces "at most one active
	// block per agent" in the database, on both backends: SQLite and Postgres
	// both permit repeated NULLs in a unique index, so released rows accumulate
	// freely while a second active block is rejected outright.
	//
	// A partial unique index (`WHERE released_at IS NULL`) would express the
	// same rule more directly but is spelled differently on each backend; a
	// read-then-insert check in the repository would be a race, and one that
	// only shows up as "releasing the sandbox does nothing" long after the two
	// rows were written.
	ActiveKey *string `gorm:"type:char(36);uniqueIndex" json:"-"`

	BlockedByActorID string     `gorm:"type:char(36);not null" json:"blocked_by_actor_id"`
	BlockedByName    string     `gorm:"not null" json:"blocked_by_name"`
	Reason           string     `gorm:"type:text;not null" json:"reason"`
	BlockedAt        time.Time  `gorm:"not null" json:"blocked_at"`
	ReleasedAt       *time.Time `json:"released_at,omitempty"`
	ReleasedByName   string     `json:"released_by_name,omitempty"`

	// KilledProcesses records how many process groups the block tore down, so
	// the UI can distinguish "stopped a runaway" from "blocked an idle
	// sandbox".
	KilledProcesses int `json:"killed_processes"`
}
