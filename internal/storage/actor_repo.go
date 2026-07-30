package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrActorNotFound      = errors.New("actor not found")
	ErrCredentialNotFound = errors.New("agent credential not found")
	ErrTokenRevoked       = errors.New("agent token revoked")
)

func HashToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

func CreateAgent(db *gorm.DB, displayName string) (*Actor, error) {
	actor := &Actor{
		ID:          uuid.New().String(),
		DisplayName: displayName,
		Kind:        ActorKindAgent,
		CreatedAt:   time.Now().UTC(),
	}
	if err := db.Create(actor).Error; err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}
	return actor, nil
}

// StdioActorName is the display name of the Actor every stdio-transport tool
// call is attributed to. The stdio transport has no credentials to
// authenticate, so it gets one durable identity rather than an exemption:
// without it, stdio would be the one path that bypasses per-agent sandbox
// isolation and block enforcement.
const StdioActorName = "stdio-local"

// GetOrCreateStdioActor returns the Actor backing the stdio transport,
// creating it on first use.
func GetOrCreateStdioActor(db *gorm.DB) (*Actor, error) {
	var actor Actor
	err := db.Where("kind = ? AND display_name = ?", ActorKindAgent, StdioActorName).First(&actor).Error
	if err == nil {
		return &actor, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return CreateAgent(db, StdioActorName)
}

func GetActorByID(db *gorm.DB, id string) (*Actor, error) {
	var actor Actor
	if err := db.First(&actor, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrActorNotFound
		}
		return nil, err
	}
	return &actor, nil
}

func ListAgents(db *gorm.DB) ([]Actor, error) {
	var agents []Actor
	if err := db.Where("kind = ?", ActorKindAgent).Order("created_at desc").Find(&agents).Error; err != nil {
		return nil, err
	}
	return agents, nil
}

func IssueAgentToken(db *gorm.DB, actorID string) (string, *AgentCredential, error) {
	rawToken := "age_" + uuid.New().String()
	tokenHash := HashToken(rawToken)

	cred := &AgentCredential{
		ID:        uuid.New().String(),
		ActorID:   actorID,
		TokenHash: tokenHash,
		CreatedAt: time.Now().UTC(),
	}

	if err := db.Create(cred).Error; err != nil {
		return "", nil, fmt.Errorf("failed to issue agent token: %w", err)
	}

	return rawToken, cred, nil
}

func ListAgentCredentials(db *gorm.DB, actorID string) ([]AgentCredential, error) {
	var creds []AgentCredential
	if err := db.Where("actor_id = ?", actorID).Order("created_at desc").Find(&creds).Error; err != nil {
		return nil, err
	}
	return creds, nil
}

func RevokeAgentToken(db *gorm.DB, credID string) error {
	now := time.Now().UTC()
	result := db.Model(&AgentCredential{}).Where("id = ? AND revoked_at IS NULL", credID).Update("revoked_at", now)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrCredentialNotFound
	}
	return nil
}

func AuthenticateAgentToken(db *gorm.DB, rawToken string) (*Actor, error) {
	tokenHash := HashToken(rawToken)
	var cred AgentCredential
	if err := db.Where("token_hash = ?", tokenHash).First(&cred).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCredentialNotFound
		}
		return nil, err
	}

	if cred.RevokedAt != nil {
		return nil, ErrTokenRevoked
	}

	// Update LastUsedAt
	now := time.Now().UTC()
	db.Model(&cred).Update("last_used_at", now)

	return GetActorByID(db, cred.ActorID)
}

func RecordExecLog(db *gorm.DB, log *ExecLog) error {
	if log.ID == "" {
		log.ID = uuid.New().String()
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	return db.Create(log).Error
}

func ListExecLogs(db *gorm.DB, agentID string, limit int) ([]ExecLog, error) {
	if limit <= 0 {
		limit = 50
	}
	var logs []ExecLog
	err := db.Where("agent_id = ?", agentID).Order("created_at desc").Limit(limit).Find(&logs).Error
	return logs, err
}
