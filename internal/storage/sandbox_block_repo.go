package storage

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	// ErrAlreadyBlocked is returned when a sandbox already has an active block.
	ErrAlreadyBlocked = errors.New("sandbox is already blocked")

	// ErrNotBlocked is returned when releasing a sandbox that isn't blocked.
	ErrNotBlocked = errors.New("sandbox is not blocked")

	// ErrEmptyReason is returned when a block is requested with no reason.
	ErrEmptyReason = errors.New("a block reason is required")
)

// BlockSandbox records an emergency block on actorID's sandbox.
//
// Uniqueness of the active block is enforced by the database, via the unique
// index on SandboxBlock.ActiveKey, not by a read-then-write check here: two
// administrators hitting the button at the same moment must produce one block
// and one ErrAlreadyBlocked. A check-then-insert would let both pass the check
// and insert two rows that each look active, at which point releasing appears
// not to work — releasing one leaves the other.
//
// The failure is mapped by re-reading rather than by matching a driver error
// string, so the same code path is correct on both backends.
func BlockSandbox(db *gorm.DB, actorID string, blockedBy *Actor, reason string) (*SandboxBlock, error) {
	if reason == "" {
		return nil, ErrEmptyReason
	}

	activeKey := actorID
	block := &SandboxBlock{
		ID:               uuid.NewString(),
		ActorID:          actorID,
		ActiveKey:        &activeKey,
		BlockedByActorID: blockedBy.ID,
		BlockedByName:    blockedBy.DisplayName,
		Reason:           reason,
		BlockedAt:        time.Now().UTC(),
	}

	if err := db.Create(block).Error; err != nil {
		existing, lookupErr := ActiveSandboxBlock(db, actorID)
		if lookupErr == nil && existing != nil {
			return nil, ErrAlreadyBlocked
		}
		return nil, fmt.Errorf("create sandbox block: %w", err)
	}
	return block, nil
}

// ReleaseSandbox lifts the active block on actorID's sandbox. Clearing
// ActiveKey is what frees the unique index for the next block.
func ReleaseSandbox(db *gorm.DB, actorID string, releasedBy *Actor) error {
	now := time.Now().UTC()
	result := db.Model(&SandboxBlock{}).
		Where("actor_id = ? AND active_key IS NOT NULL", actorID).
		Updates(map[string]any{
			"active_key":       nil,
			"released_at":      now,
			"released_by_name": releasedBy.DisplayName,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotBlocked
	}
	return nil
}

// ActiveSandboxBlock returns the active block on actorID's sandbox, or nil when
// it isn't blocked. A missing row is the common case, not an error — every tool
// call goes through here.
func ActiveSandboxBlock(db *gorm.DB, actorID string) (*SandboxBlock, error) {
	var block SandboxBlock
	err := db.Where("active_key = ?", actorID).First(&block).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up sandbox block: %w", err)
	}
	return &block, nil
}

// ActiveSandboxBlocks returns the active block for every blocked agent, keyed by
// actor ID, so the sandbox list renders without one query per row.
func ActiveSandboxBlocks(db *gorm.DB) (map[string]*SandboxBlock, error) {
	var blocks []SandboxBlock
	if err := db.Where("active_key IS NOT NULL").Find(&blocks).Error; err != nil {
		return nil, err
	}
	byActor := make(map[string]*SandboxBlock, len(blocks))
	for i := range blocks {
		byActor[blocks[i].ActorID] = &blocks[i]
	}
	return byActor, nil
}

// RecordKilledProcesses attaches the number of process groups a block tore down
// to the block row. Called after the kill, because the count isn't known until
// the sandbox has been swept.
func RecordKilledProcesses(db *gorm.DB, blockID string, killed int) error {
	return db.Model(&SandboxBlock{}).Where("id = ?", blockID).Update("killed_processes", killed).Error
}
