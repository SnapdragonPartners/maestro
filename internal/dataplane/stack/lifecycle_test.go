package stack

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/dataplane/paths"
)

// testConfig builds a Config rooted in a temporary MAESTRO_HOME.
func testConfig(t *testing.T) *Config {
	t.Helper()
	t.Setenv(paths.HomeEnv, t.TempDir())

	roots, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cfg, err := NewConfig(roots)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	return cfg
}

// Every lifecycle operation must take the lifecycle lock, and must take it
// before doing anything else — a `reset` that deletes service data while
// another process is midway through initdb is the failure this prevents.
//
// The assertion is on BLOCKING, not on outcome: each operation is expected
// to fail afterwards (the compose file is bogus and Docker may be absent),
// which is fine and deliberately irrelevant. What matters is that it does
// not proceed while the lock is held elsewhere. This is the ADR 0027 rule
// that a lock needs a test which fails without it — removing the
// lockLifecycle call from any of them makes its case return immediately,
// and the case fails.
//
// THE ENUMERATION IS DISCOVERED, NOT LISTED. An earlier version listed the
// operations by hand and its own comment named that as the weak point:
// Migrate and ForceVersion were added later, were absent, and removing
// either lock left this suite green. The comment was right and the fix was
// not applied — Backup, Restore and Verify were then added to the package
// and were missing here too, so three of the four verbs item 8 introduced
// had no lock coverage at all while a test named "every lifecycle
// operation" passed.
//
// So the verbs come from the same AST discovery the marker call-site test
// uses, and a verb without a case here fails rather than being skipped.
func TestLifecycleOperationsTakeTheLock(t *testing.T) {
	// Each case builds its invocation BEFORE the lock is taken, because some
	// verbs validate their arguments before reaching for it: Restore reads
	// and inventories its archive first, and would fail on a bogus path
	// without ever blocking.
	operations := map[string]func(*testing.T, *Config) func(context.Context) error{
		"Up": func(_ *testing.T, cfg *Config) func(context.Context) error {
			return func(ctx context.Context) error { return Up(ctx, cfg, bogusComposeFile) }
		},
		"Down": func(_ *testing.T, cfg *Config) func(context.Context) error {
			return func(ctx context.Context) error { return Down(ctx, cfg, bogusComposeFile) }
		},
		"Reset": func(_ *testing.T, cfg *Config) func(context.Context) error {
			return func(ctx context.Context) error { return Reset(ctx, cfg, bogusComposeFile) }
		},
		"Migrate": func(_ *testing.T, cfg *Config) func(context.Context) error {
			return func(ctx context.Context) error { return Migrate(ctx, cfg) }
		},
		"ForceVersion": func(_ *testing.T, cfg *Config) func(context.Context) error {
			return func(_ context.Context) error { return ForceVersion(cfg, 1) }
		},
		"Backup": func(t *testing.T, cfg *Config) func(context.Context) error {
			t.Helper()
			// A destination that does not exist, which is what Backup
			// requires, and outside the data root, which it also requires.
			destination := filepath.Join(t.TempDir(), "archive")
			return func(ctx context.Context) error { return Backup(ctx, cfg, bogusComposeFile, destination) }
		},
		"Verify": func(_ *testing.T, cfg *Config) func(context.Context) error {
			return func(ctx context.Context) error {
				_, err := Verify(ctx, cfg)
				return err
			}
		},
		"Restore": func(t *testing.T, cfg *Config) func(context.Context) error {
			t.Helper()
			// A REAL archive, built before the lock is held. Restore
			// validates the source before locking, so an invalid one would
			// make this case pass for the wrong reason.
			populatePlane(t, cfg, "for the lock test")
			archive := archiveFrom(t, cfg)
			return func(ctx context.Context) error {
				return Restore(ctx, cfg, bogusComposeFile, archive, true)
			}
		},
	}

	for _, verb := range exportedLifecycleVerbs(t) {
		if _, covered := operations[verb]; !covered {
			t.Errorf("%s is an exported lifecycle verb with no case here: it may be acting on a data "+
				"root while another process holds the lifecycle lock", verb)
		}
	}
	for verb := range operations {
		if !slices.Contains(exportedLifecycleVerbs(t), verb) {
			t.Errorf("this test covers %s, which is no longer an exported lifecycle verb", verb)
		}
	}

	for name, build := range operations {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(t)
			if err := cfg.Roots.Ensure(); err != nil {
				t.Fatalf("Ensure: %v", err)
			}
			invoke := build(t, cfg)

			// Hold the lock the way a concurrent process would.
			release, err := paths.AcquireLock(filepath.Join(cfg.Roots.Data, LifecycleLockFile))
			if err != nil {
				t.Fatalf("AcquireLock: %v", err)
			}

			done := make(chan error, 1)
			go func() {
				done <- invoke(context.Background())
			}()

			select {
			case <-done:
				_ = release()
				t.Fatal("operation completed while the lifecycle lock was held: it is not taking the lock")
			case <-time.After(250 * time.Millisecond):
			}

			if relErr := release(); relErr != nil {
				t.Fatalf("release: %v", relErr)
			}

			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("operation did not proceed after the lifecycle lock was released")
			}
		})
	}
}

// bogusComposeFile is a path that cannot resolve, so an operation fails fast
// once it does get the lock. No test here inspects the resulting error.
const bogusComposeFile = "testdata/does-not-exist.yaml"

// exportedLifecycleVerbs discovers the package's lifecycle entry points,
// using the same structural definition the marker call-site test uses:
// exported, and taking a *Config.
func exportedLifecycleVerbs(t *testing.T) []string {
	t.Helper()
	fileSet := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	var verbs []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fileSet, filepath.Join(".", name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Recv != nil || !isLifecycleEntryPoint(fn) {
				continue
			}
			verbs = append(verbs, fn.Name.Name)
		}
	}
	slices.Sort(verbs)
	return verbs
}
