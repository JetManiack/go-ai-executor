package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrActorNotFound       = errors.New("actor not found")
	ErrCredentialNotFound  = errors.New("agent credential not found")
	ErrTokenRevoked        = errors.New("agent token revoked")
	ErrEmptyDisplayName    = errors.New("display name is required")
	ErrDisplayNameConflict = errors.New("an actor with that display name already exists")
)

// hashToken is how a bearer token is stored: only its SHA-256 digest ever
// reaches the database, so reading the credentials table yields nothing usable
// as a token.
func hashToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

// HashTokenForTesting exposes hashToken for tests that construct credential
// rows directly instead of going through IssueAgentToken.
func HashTokenForTesting(rawToken string) string {
	return hashToken(rawToken)
}

func CreateAgent(db *gorm.DB, displayName string) (*Actor, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return nil, ErrEmptyDisplayName
	}

	// Probed before inserting, not only after failing: registering a name twice
	// is ordinary user error answered with a 400, and letting it reach the
	// unique index makes GORM log "UNIQUE constraint failed" at WARN — which
	// reads to an operator like a database problem, and teaches them to skim
	// past the level real problems arrive at.
	var existing Actor
	if err := db.Where("display_name = ?", displayName).First(&existing).Error; err == nil {
		return nil, ErrDisplayNameConflict
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check display name: %w", err)
	}

	actor := &Actor{
		ID:          uuid.NewString(),
		DisplayName: displayName,
		Kind:        ActorKindAgent,
		CreatedAt:   time.Now().UTC(),
	}
	if err := db.Create(actor).Error; err != nil {
		// The index is still the authority: two concurrent registrations of the
		// same name both pass the probe above, and one of them lands here. That
		// case is rare enough that its log line is worth having.
		if db.Where("display_name = ?", displayName).First(&existing).Error == nil {
			return nil, ErrDisplayNameConflict
		}
		return nil, fmt.Errorf("create agent: %w", err)
	}
	return actor, nil
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

// IssueAgentToken mints a new bearer token for actorID and returns it in the
// clear exactly once — only its hash is stored, so a lost token can be replaced
// but never recovered.
func IssueAgentToken(db *gorm.DB, actorID string) (string, *AgentCredential, error) {
	if _, err := GetActorByID(db, actorID); err != nil {
		return "", nil, err
	}

	rawToken := "age_" + uuid.NewString()
	cred := &AgentCredential{
		ID:        uuid.NewString(),
		ActorID:   actorID,
		TokenHash: hashToken(rawToken),
		CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(cred).Error; err != nil {
		return "", nil, fmt.Errorf("issue agent token: %w", err)
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
	result := db.Model(&AgentCredential{}).
		Where("id = ? AND revoked_at IS NULL", credID).
		Update("revoked_at", now)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrCredentialNotFound
	}
	return nil
}

// RevokeAllAgentCredentials revokes every credential belonging to actorID. This
// is how an agent is decommissioned: the Actor row stays, so blocks and
// sandboxes recorded against it keep a named owner rather than pointing at a
// vanished actor.
func RevokeAllAgentCredentials(db *gorm.DB, actorID string) error {
	now := time.Now().UTC()
	return db.Model(&AgentCredential{}).
		Where("actor_id = ? AND revoked_at IS NULL", actorID).
		Update("revoked_at", now).Error
}

// ActorsWithActiveToken reports which actors still hold at least one
// unrevoked credential, so the UI can distinguish a usable agent from a
// decommissioned one without a query per row.
func ActorsWithActiveToken(db *gorm.DB) (map[string]bool, error) {
	var actorIDs []string
	if err := db.Model(&AgentCredential{}).
		Where("revoked_at IS NULL").
		Distinct().
		Pluck("actor_id", &actorIDs).Error; err != nil {
		return nil, err
	}
	active := make(map[string]bool, len(actorIDs))
	for _, id := range actorIDs {
		active[id] = true
	}
	return active, nil
}

// AuthenticateAgentToken resolves a raw bearer token to the Actor that owns it,
// rejecting revoked credentials.
func AuthenticateAgentToken(db *gorm.DB, rawToken string) (*Actor, error) {
	var cred AgentCredential
	if err := db.Where("token_hash = ?", hashToken(rawToken)).First(&cred).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCredentialNotFound
		}
		return nil, err
	}

	if cred.RevokedAt != nil {
		return nil, ErrTokenRevoked
	}

	// Best-effort: a failed last-used bookkeeping write must not turn a valid
	// token into a rejected one.
	now := time.Now().UTC()
	_ = db.Model(&cred).Update("last_used_at", now).Error

	return GetActorByID(db, cred.ActorID)
}
