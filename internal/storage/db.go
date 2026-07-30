// Package storage holds this service's persistence layer: the GORM models and
// the repository functions over them, behind a single Open(dsn) that selects
// SQLite or Postgres from the DSN.
package storage

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Open opens a database at dsn — a postgres:// or postgresql:// scheme selects
// the Postgres backend; anything else (a file path or ":memory:") selects
// SQLite — applies backend-specific setup, and migrates the schema.
func Open(dsn string) (*gorm.DB, error) {
	var db *gorm.DB
	var err error

	if IsPostgresDSN(dsn) {
		db, err = openPostgres(dsn)
	} else {
		db, err = openSQLite(dsn)
	}
	if err != nil {
		return nil, err
	}

	migrate := func() error {
		if err := db.AutoMigrate(
			&Actor{},
			&AgentCredential{},
			&UserIdentity{},
			&Session{},
			&SandboxBlock{},
		); err != nil {
			return fmt.Errorf("automigrate: %w", err)
		}
		return nil
	}

	// On Postgres, a second replica migrating the same schema concurrently
	// (e.g. mid-RollingUpdate) can deadlock against this one — serialize with
	// an advisory lock. SQLite has no concurrent-replica scenario to guard
	// against.
	if db.Dialector.Name() == "postgres" {
		if err := withMigrationLock(db, migrate); err != nil {
			return nil, err
		}
	} else if err := migrate(); err != nil {
		return nil, err
	}

	return db, nil
}

// IsPostgresDSN reports whether dsn selects the Postgres backend.
func IsPostgresDSN(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}
