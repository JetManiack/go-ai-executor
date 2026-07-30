package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// openSQLite opens a SQLite database at dsn and applies the pragmas required
// for safe concurrent-writer behavior.
func openSQLite(dsn string) (*gorm.DB, error) {
	if dsn != ":memory:" {
		if dir := filepath.Dir(dsn); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, fmt.Errorf("create database directory: %w", err)
			}
		}
	}

	db, err := gorm.Open(sqlite.Open(dsn), gormConfig())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// SQLite's default is fail-fast on write contention (SQLITE_BUSY), not
	// wait-and-retry. Several agents blocking, unblocking and registering
	// credentials at once contend on writes routinely — both pragmas are
	// required, not just one.
	if err := db.Exec("PRAGMA busy_timeout = 5000").Error; err != nil {
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}
	if err := db.Exec("PRAGMA journal_mode = WAL").Error; err != nil {
		return nil, fmt.Errorf("set journal_mode: %w", err)
	}

	return db, nil
}
