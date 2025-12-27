// Package persistence provides SQLite-based storage with singleton database access.
package persistence

import (
	"database/sql"
	"fmt"
	"sync"

	"orchestrator/pkg/logx"
)

// DB is the singleton database manager.
// All database access should go through this instance.
//
//nolint:gochecknoglobals // Intentional singleton pattern for database access
var (
	globalDB     *sql.DB
	globalDBOnce sync.Once
	globalDBMu   sync.RWMutex
	dbLogger     *logx.Logger
	sessionID    string // Current session ID for all operations
)

// Initialize sets up the singleton database connection.
// This must be called once at startup before any database operations.
// Subsequent calls are no-ops.
func Initialize(dbPath, sessID string) error {
	var initErr error

	globalDBOnce.Do(func() {
		dbLogger = logx.NewLogger("persistence")
		sessionID = sessID

		// Open database connection with WAL mode and busy timeout
		db, err := sql.Open("sqlite", fmt.Sprintf(
			"file:%s?_foreign_keys=ON&_journal_mode=WAL&_busy_timeout=5000",
			dbPath,
		))
		if err != nil {
			initErr = fmt.Errorf("failed to open database: %w", err)
			return
		}

		// Test connection
		if err := db.Ping(); err != nil {
			_ = db.Close()
			initErr = fmt.Errorf("failed to ping database: %w", err)
			return
		}

		// Initialize schema
		if err := initializeSchemaWithMigrations(db); err != nil {
			_ = db.Close()
			initErr = fmt.Errorf("failed to initialize schema: %w", err)
			return
		}

		// Configure connection pool for SQLite (single writer)
		db.SetMaxOpenConns(1) // SQLite only supports one writer
		db.SetMaxIdleConns(1)

		globalDB = db
		dbLogger.Info("📦 Database initialized: %s (session: %s)", dbPath, sessID)
	})

	return initErr
}

// GetDB returns the singleton database connection.
// Panics if Initialize has not been called.
func GetDB() *sql.DB {
	globalDBMu.RLock()
	defer globalDBMu.RUnlock()

	if globalDB == nil {
		panic("persistence.Initialize must be called before GetDB")
	}
	return globalDB
}

// GetSessionID returns the current session ID.
func GetSessionID() string {
	globalDBMu.RLock()
	defer globalDBMu.RUnlock()
	return sessionID
}

// SetSessionID updates the session ID (for session restarts).
func SetSessionID(sessID string) {
	globalDBMu.Lock()
	defer globalDBMu.Unlock()
	sessionID = sessID
}

// Close closes the database connection.
// Should be called during shutdown.
func Close() error {
	globalDBMu.Lock()
	defer globalDBMu.Unlock()

	if globalDB != nil {
		err := globalDB.Close()
		globalDB = nil
		if err != nil {
			return fmt.Errorf("failed to close database: %w", err)
		}
	}
	return nil
}

// Ops returns a DatabaseOperations instance using the singleton connection.
// This is the primary way to perform database operations.
func Ops() *DatabaseOperations {
	return NewDatabaseOperations(GetDB(), GetSessionID())
}

// IsInitialized returns true if the database has been initialized.
func IsInitialized() bool {
	globalDBMu.RLock()
	defer globalDBMu.RUnlock()
	return globalDB != nil
}

// Reset closes the database and resets the singleton for testing.
// This should only be used in tests to allow re-initialization.
func Reset() error {
	globalDBMu.Lock()
	defer globalDBMu.Unlock()

	if globalDB != nil {
		if err := globalDB.Close(); err != nil {
			return fmt.Errorf("failed to close database during reset: %w", err)
		}
		globalDB = nil
	}

	// Reset the sync.Once by creating a new one
	globalDBOnce = sync.Once{}
	sessionID = ""
	dbLogger = nil

	return nil
}
