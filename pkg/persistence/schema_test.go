package persistence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationV22_AddsDurableAsksAndIncidentsColumns(t *testing.T) {
	// Create a real on-disk database (InitializeDatabase runs all migrations)
	tempDir, err := os.MkdirTemp("", "schema_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	db, err := InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("InitializeDatabase failed: %v", err)
	}
	defer db.Close()

	// Verify pm_state.current_ask_json exists
	if !tableHasColumn(db, "pm_state", "current_ask_json") {
		t.Error("pm_state should have current_ask_json column after migration v22")
	}

	// Verify pm_state.open_incidents_json exists
	if !tableHasColumn(db, "pm_state", "open_incidents_json") {
		t.Error("pm_state should have open_incidents_json column after migration v22")
	}

	// Verify architect_state.open_incidents_json exists
	if !tableHasColumn(db, "architect_state", "open_incidents_json") {
		t.Error("architect_state should have open_incidents_json column after migration v22")
	}
}

func TestMigrationV22_Idempotent(t *testing.T) {
	// Verify that running InitializeDatabase twice does not fail
	// (migrations should be idempotent via tableHasColumn guards)
	tempDir, err := os.MkdirTemp("", "schema_idempotent_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")

	// First initialization
	db1, err := InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("First InitializeDatabase failed: %v", err)
	}
	db1.Close()

	// Second initialization on same database file
	db2, err := InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Second InitializeDatabase failed (migration not idempotent): %v", err)
	}
	defer db2.Close()

	// Columns should still be present
	if !tableHasColumn(db2, "pm_state", "current_ask_json") {
		t.Error("pm_state.current_ask_json missing after second initialization")
	}
	if !tableHasColumn(db2, "pm_state", "open_incidents_json") {
		t.Error("pm_state.open_incidents_json missing after second initialization")
	}
	if !tableHasColumn(db2, "architect_state", "open_incidents_json") {
		t.Error("architect_state.open_incidents_json missing after second initialization")
	}
}
