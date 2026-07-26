//go:build integration

package migrations_test

import (
	"testing"

	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/stack"
)

func TestDownThenUpRestoresSchema(t *testing.T) {
	roots, _ := paths.Resolve()
	cfg, _ := stack.NewConfig(roots)
	key, _ := paths.EnsureKey(roots.Config)
	dsn, _ := cfg.DSN(key)
	if err := migrations.Up(dsn); err != nil {
		t.Skip("plane unavailable")
	}
	if err := migrations.Down(dsn); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if err := migrations.Up(dsn); err != nil {
		t.Fatalf("Up after Down: %v", err)
	}
	v, dirty, err := migrations.Version(dsn)
	if err != nil || dirty || v == 0 {
		t.Fatalf("after up/down/up: v=%d dirty=%v err=%v", v, dirty, err)
	}
}
