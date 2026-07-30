package storage

import (
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetOrCreateHumanActor finds the human Actor already linked to subject,
// updating its role if the caller's current role differs (so a role change from
// the auth provider — e.g. an OIDC group change — takes effect on the next
// request). If no Actor is linked to subject yet, it creates both a new Actor
// (Kind: human) and its UserIdentity row.
//
// Concurrency: UserIdentity.KeycloakSubject (not Actor.DisplayName) is the sole
// arbiter of "who won the race to create this subject's Actor" — the
// UserIdentity insert is attempted first, with OnConflict{DoNothing}, and only
// the goroutine whose insert actually took effect (RowsAffected > 0) goes on to
// create the Actor row. This means two concurrent first-calls for the SAME
// subject never both attempt an Actor insert, so they never collide on
// Actor.DisplayName's uniqueIndex (which they otherwise would, since both
// racers pass the same displayName for the same person). A genuine
// cross-subject DisplayName collision — two different people who happen to
// share a display name — still surfaces as a hard uniqueness error from the
// winning branch's tx.Create(&Actor{...}).
func GetOrCreateHumanActor(db *gorm.DB, subject, displayName, role string) (*Actor, error) {
	var identity UserIdentity
	err := db.Where("keycloak_subject = ?", subject).First(&identity).Error
	if err == nil {
		if identity.Role != role {
			if err := db.Model(&identity).Update("role", role).Error; err != nil {
				return nil, err
			}
		}
		var actor Actor
		if err := db.First(&actor, "id = ?", identity.ActorID).Error; err != nil {
			return nil, err
		}
		return &actor, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	newActorID := uuid.NewString()
	var won bool

	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "keycloak_subject"}},
			DoNothing: true,
		}).Create(&UserIdentity{
			ActorID:         newActorID,
			KeycloakSubject: subject,
			Role:            role,
		})
		if result.Error != nil {
			return result.Error
		}
		won = result.RowsAffected > 0
		if !won {
			return nil
		}
		return tx.Create(&Actor{
			ID:          newActorID,
			DisplayName: displayName,
			Kind:        ActorKindHuman,
		}).Error
	})
	if err != nil {
		return nil, err
	}

	if won {
		var actor Actor
		if err := db.First(&actor, "id = ?", newActorID).Error; err != nil {
			return nil, err
		}
		return &actor, nil
	}

	// Lost the race: find the winner's Actor via the identity they created.
	var winner UserIdentity
	if err := db.Where("keycloak_subject = ?", subject).First(&winner).Error; err != nil {
		return nil, err
	}
	var winningActor Actor
	if err := db.First(&winningActor, "id = ?", winner.ActorID).Error; err != nil {
		return nil, err
	}
	return &winningActor, nil
}
