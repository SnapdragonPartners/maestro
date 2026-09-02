package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/readiness"
	"orchestrator/internal/dataplane/stack"
	"orchestrator/internal/orchestrator"
)

// scratchPlane builds a stack.Config over a throwaway root, so these cases
// never touch the developer's real plane. The local guards run before any
// DSN is built, so no service is needed to reach every marker and key state.
func scratchPlane(t *testing.T) *stack.Config {
	t.Helper()
	root := t.TempDir()
	cfg, err := stack.NewConfig(paths.Roots{
		Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data"),
		State: filepath.Join(root, "state"), Cache: filepath.Join(root, "cache"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Roots.Ensure(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func plantFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestStartRendersEveryLocalNotReadyState is design D5's local rows driven
// THROUGH orchestrator.Start over the real composition root: the cause the
// Orchestrator reports and the remedy it renders, one case per state.
// This is the exit criterion "the locked-plane path is exercised by the
// Orchestrator rather than only by the plane's tests".
func TestStartRendersEveryLocalNotReadyState(t *testing.T) {
	cases := []struct {
		name   string
		put    func(t *testing.T, cfg *stack.Config)
		cause  readiness.Cause
		remedy string
	}{
		{"no plane", func(*testing.T, *stack.Config) {}, readiness.NoPlane, "dataplane-up"},
		{"root key missing", func(t *testing.T, cfg *stack.Config) {
			dir, err := cfg.Roots.ServiceDataDir(paths.ServicePostgres)
			if err != nil {
				t.Fatal(err)
			}
			plantFile(t, filepath.Join(dir, "PG_VERSION"))
		}, readiness.RootKeyMissing, "recover-key"},
		{"restore incomplete", func(t *testing.T, cfg *stack.Config) {
			plantFile(t, filepath.Join(cfg.Roots.Data, stack.RestoreIncompleteMarker))
		}, readiness.RestoreIncomplete, "dataplane-restore"},
		{"restore unverified", func(t *testing.T, cfg *stack.Config) {
			plantFile(t, filepath.Join(cfg.Roots.Data, stack.RestoreUnverifiedMarker))
		}, readiness.RestoreUnverified, "dataplane-up"},
		{"recovery interrupted", func(t *testing.T, cfg *stack.Config) {
			plantFile(t, filepath.Join(cfg.Roots.Data, stack.RecoveryMarkerFile))
		}, readiness.RecoveryInterrupted, "recover-key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := scratchPlane(t)
			tc.put(t, cfg)
			open, err := orchestratorOpener(cfg)
			if err != nil {
				t.Fatal(err)
			}
			_, err = orchestrator.Start(context.Background(), open, orchestrator.Config{OrganizationSlug: "acme", OperatorHandle: "dan"})
			var refused *orchestrator.StartupRefused
			if !errors.As(err, &refused) {
				t.Fatalf("want a StartupRefused, got %v", err)
			}
			if refused.Cause != tc.cause {
				t.Fatalf("cause %s, want %s: %v", refused.Cause, tc.cause, err)
			}
			if !strings.Contains(refused.Remedy, tc.remedy) {
				t.Fatalf("remedy %q lacks %q", refused.Remedy, tc.remedy)
			}
			// The rendering an operator reads carries all three parts.
			for _, part := range []string{string(tc.cause), "observed:", "remedy:"} {
				if !strings.Contains(err.Error(), part) {
					t.Fatalf("rendering lacks %q: %s", part, err)
				}
			}
		})
	}
}

// TestStartLeavesAnInterruptedRecoveryUntouched: normal startup neither
// bypasses nor advances the recovery protocol. THE MUTANT: have ordinary
// use clear the marker; the file must survive the refusal.
func TestStartLeavesAnInterruptedRecoveryUntouched(t *testing.T) {
	cfg := scratchPlane(t)
	marker := filepath.Join(cfg.Roots.Data, stack.RecoveryMarkerFile)
	plantFile(t, marker)
	staged := filepath.Join(cfg.Roots.Config, paths.KeyFileName+".staged")
	plantFile(t, staged)
	open, err := orchestratorOpener(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.Start(context.Background(), open, orchestrator.Config{OrganizationSlug: "acme", OperatorHandle: "dan"}); err == nil {
		t.Fatal("started against an interrupted recovery")
	}
	for _, path := range []string{marker, staged} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("startup disturbed the recovery protocol: %s is gone", filepath.Base(path))
		}
	}
}
