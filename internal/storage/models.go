package storage

import "time"

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

type UserIdentity struct {
	ActorID         string `gorm:"type:char(36);primaryKey" json:"actor_id"`
	KeycloakSubject string `gorm:"not null;uniqueIndex" json:"keycloak_subject"`
	Role            string `gorm:"type:varchar(10);not null" json:"role"`
}

type Session struct {
	ID                    string    `gorm:"type:char(36);primaryKey"`
	ActorID               string    `gorm:"type:char(36);not null;index"`
	RefreshTokenEncrypted string    `gorm:"type:text;not null"`
	ExpiresAt             time.Time `gorm:"not null;index"`
	CreatedAt             time.Time `gorm:"not null"`
}

type ExecLog struct {
	ID         string    `gorm:"type:char(36);primaryKey" json:"id"`
	AgentID    string    `gorm:"type:char(36);not null;index" json:"agent_id"`
	Command    string    `gorm:"type:text;not null" json:"command"`
	WorkDir    string    `gorm:"type:varchar(255)" json:"work_dir"`
	Stdout     string    `gorm:"type:text" json:"stdout"`
	Stderr     string    `gorm:"type:text" json:"stderr"`
	ExitCode   int       `gorm:"not null" json:"exit_code"`
	DurationMs int64     `gorm:"not null" json:"duration_ms"`
	Truncated  bool      `gorm:"not null" json:"truncated"`
	CreatedAt  time.Time `gorm:"not null;index" json:"created_at"`
}
