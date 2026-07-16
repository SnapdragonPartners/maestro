// Package engine implements the benchmark runner's execution engine
// (design_engine.md): attempt lifecycle with repeat isolation, budget
// enforcement, engine-executed validators and checks, verdict composition,
// cleanup verification before the append-only record, and suite
// orchestration with a persisted manifest.
package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/results"
	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

// Defaults for engine timeouts.
const (
	// DefaultCleanupTimeout bounds the cleanup phase, which always runs on
	// a fresh context — never the (possibly expired) attempt context.
	DefaultCleanupTimeout = 2 * time.Minute
	// DefaultDescribeTimeout bounds the pre-run Describe call.
	DefaultDescribeTimeout = 1 * time.Minute
	// detailLimit truncates captured command output in check results.
	detailLimit = 2000
)

// Engine executes golden story attempts against registered adapters.
type Engine struct {
	// Adapters maps harness.adapter names to implementations.
	Adapters map[string]target.Adapter
	// Store receives run records and suite manifests.
	Store *results.Store
	// Logf receives human progress lines; nil is silent.
	Logf func(format string, args ...any)
	// Workdir is the root under which run-scoped workspaces are created.
	Workdir string
	// CleanupTimeout overrides DefaultCleanupTimeout when positive.
	CleanupTimeout time.Duration
}

func (e *Engine) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

func (e *Engine) cleanupTimeout() time.Duration {
	if e.CleanupTimeout > 0 {
		return e.CleanupTimeout
	}
	return DefaultCleanupTimeout
}

// adapterFor resolves the adapter a bundle selects.
func (e *Engine) adapterFor(name string) (target.Adapter, error) {
	adapter, ok := e.Adapters[name]
	if !ok {
		known := make([]string, 0, len(e.Adapters))
		for k := range e.Adapters {
			known = append(known, k)
		}
		return nil, fmt.Errorf("unknown adapter %q (registered: %s)", name, strings.Join(known, ", "))
	}
	return adapter, nil
}

// newRunID builds a unique, lowercase, filename-safe run ID.
func newRunID(storyID, configName string, repeat int) (string, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("run id entropy: %w", err)
	}
	return fmt.Sprintf("%s--%s--r%d--%s", storyID, configName, repeat, hex.EncodeToString(suffix)), nil
}

// truncateDetail bounds captured output for record storage.
func truncateDetail(s string) string {
	if len(s) <= detailLimit {
		return s
	}
	return s[:detailLimit] + " …[truncated]"
}
