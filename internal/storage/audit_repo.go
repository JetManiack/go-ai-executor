package storage

import (
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// maxTargetBytes bounds what one row records about what an action was aimed at.
//
// An argument vector has no natural size, and a journal a single call can bloat
// is one an operator eventually turns off — which costs more than a truncated
// command line ever would.
const maxTargetBytes = 2 << 10

// AuditRecord is what a caller supplies; the repository fills in the rest.
type AuditRecord struct {
	CallID     string
	Actor      *Actor
	Action     string
	Target     string
	WorkerID   string
	ExecID     string
	Outcome    string
	Error      string
	DurationMs int64
	Bytes      int
}

// AppendAuditStarted writes the row that says an action was attempted, and returns the
// call id tying it to its finished row.
//
// A caller with no id of its own passes an empty CallID and gets one back.
func AppendAuditStarted(db *gorm.DB, rec AuditRecord) (string, error) {
	if rec.CallID == "" {
		rec.CallID = uuid.NewString()
	}
	return rec.CallID, appendAudit(db, rec, AuditStarted)
}

// AppendAuditFinished writes the row that says how it went.
func AppendAuditFinished(db *gorm.DB, rec AuditRecord) error {
	return appendAudit(db, rec, AuditFinished)
}

func appendAudit(db *gorm.DB, rec AuditRecord, phase AuditPhase) error {
	event := &AuditEvent{
		ID:         uuid.NewString(),
		CallID:     rec.CallID,
		Phase:      phase,
		At:         time.Now().UTC(),
		Action:     rec.Action,
		Target:     truncateTarget(rec.Target),
		WorkerID:   rec.WorkerID,
		ExecID:     rec.ExecID,
		Outcome:    rec.Outcome,
		Error:      truncateTarget(rec.Error),
		DurationMs: rec.DurationMs,
		Bytes:      rec.Bytes,
	}
	if rec.Actor != nil {
		event.ActorID = rec.Actor.ID
		event.ActorName = rec.Actor.DisplayName
		event.ActorKind = rec.Actor.Kind
	}
	return db.Create(event).Error
}

// truncateTarget cuts on a rune boundary, because Postgres rejects a string
// containing a partial character outright — a journal that fails to write is
// worse than one that records a shortened command.
func truncateTarget(s string) string {
	if len(s) <= maxTargetBytes {
		return s
	}
	cut := maxTargetBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// AuditFilter narrows a journal query. Every field is optional.
type AuditFilter struct {
	ActorID string
	Action  string
	Since   time.Time
	Limit   int
}

// ListAudit returns journal rows newest first.
func ListAudit(db *gorm.DB, filter AuditFilter) ([]AuditEvent, error) {
	// A default limit rather than none: this table is the one that grows without
	// bound between prunes, and an unbounded query against it is a way to make
	// the server the thing that falls over.
	if filter.Limit <= 0 || filter.Limit > 1000 {
		filter.Limit = 200
	}

	query := db.Order("at DESC").Limit(filter.Limit)
	if filter.ActorID != "" {
		query = query.Where("actor_id = ?", filter.ActorID)
	}
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if !filter.Since.IsZero() {
		query = query.Where("at >= ?", filter.Since)
	}

	var events []AuditEvent
	err := query.Find(&events).Error
	return events, err
}

// PruneAudit deletes rows older than retention and reports how many went.
//
// Deleting by age rather than by count keeps the guarantee an operator can state
// — "we keep a week" — instead of one that depends on how busy the week was.
func PruneAudit(db *gorm.DB, retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-retention)
	result := db.Where("at < ?", cutoff).Delete(&AuditEvent{})
	return result.RowsAffected, result.Error
}
