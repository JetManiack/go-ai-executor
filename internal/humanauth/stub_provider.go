package humanauth

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"go-ai-executor/internal/storage"
)

type StubProvider struct {
	db       *gorm.DB
	identity *HumanIdentity
}

func NewStubProvider(db *gorm.DB) (*StubProvider, error) {
	// Ensure stub human actor exists in database
	var actor storage.Actor
	err := db.Where("kind = ? AND display_name = ?", storage.ActorKindHuman, "Admin User").First(&actor).Error
	if err == gorm.ErrRecordNotFound {
		actor = storage.Actor{
			ID:          uuid.New().String(),
			DisplayName: "Admin User",
			Kind:        storage.ActorKindHuman,
			CreatedAt:   time.Now().UTC(),
		}
		if err := db.Create(&actor).Error; err != nil {
			return nil, err
		}

		userIdentity := storage.UserIdentity{
			ActorID:         actor.ID,
			KeycloakSubject: "stub-admin-subject",
			Role:            "admin",
		}
		if err := db.Create(&userIdentity).Error; err != nil {
			return nil, err
		}
	}

	var userIdentity storage.UserIdentity
	if err := db.First(&userIdentity, "actor_id = ?", actor.ID).Error; err != nil {
		return nil, err
	}

	return &StubProvider{
		db: db,
		identity: &HumanIdentity{
			Actor:    &actor,
			Identity: &userIdentity,
		},
	}, nil
}

func (p *StubProvider) AuthenticateRequest(r *http.Request) (*HumanIdentity, error) {
	return p.identity, nil
}
