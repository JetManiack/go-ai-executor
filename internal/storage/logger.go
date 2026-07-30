package storage

import (
	"log"
	"os"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// gormConfig is the shared GORM configuration for both backends.
//
// IgnoreRecordNotFoundError is the point of it: "not found" is an ordinary
// control-flow outcome here — GetOrCreateHumanActor probes for a UserIdentity
// before creating one, AuthenticateAgentToken probes for a credential, and
// ActiveSandboxBlock probes for a block that usually isn't there — so GORM's
// default logger would print a red ERROR line on every new user's first
// request, every rejected token, and every single tool call. Left alone, the
// log trains its readers to ignore exactly the level real failures arrive at.
func gormConfig() *gorm.Config {
	return &gorm.Config{
		Logger: logger.New(log.New(os.Stderr, "", log.LstdFlags), logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
		}),
	}
}
