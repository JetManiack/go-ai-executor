package storage

import (
	"context"
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// MigrationAdvisoryLockKey identifies this application's Postgres
// session-level advisory lock, held for the duration of AutoMigrate so two
// replicas migrating the same schema concurrently — e.g. during a
// RollingUpdate — serialize instead of racing each other. Exported so tests can
// acquire the same lock directly to simulate a concurrent migrator. The value
// is otherwise arbitrary; it only needs to not collide with a lock key used
// elsewhere against the same database.
const MigrationAdvisoryLockKey = 87430003

// openPostgres opens a Postgres database at dsn (a postgres:// or postgresql://
// connection string).
func openPostgres(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), gormConfig())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return db, nil
}

// withMigrationLock runs fn while holding MigrationAdvisoryLockKey on a single
// dedicated connection checked out from db's pool. The lock is session-scoped
// in Postgres — tied to that one connection for as long as it stays open — so
// this serializes fn (AutoMigrate) against any other process holding the same
// lock, regardless of which connection(s) fn itself uses internally.
func withMigrationLock(db *gorm.DB, fn func() error) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get pooled db handle: %w", err)
	}

	ctx := context.Background()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("checkout migration-lock connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", MigrationAdvisoryLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", MigrationAdvisoryLockKey); err != nil {
			// Losing the unlock isn't fatal: the lock is session-scoped and is
			// released when this connection closes on the deferred conn.Close()
			// above.
			_ = err
		}
	}()

	return fn()
}
