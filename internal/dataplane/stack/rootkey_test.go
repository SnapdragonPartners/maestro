package stack

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/paths"
)

// planeAt builds a Config over a scratch directory, so these cases never
// touch the developer's real roots.
func planeAt(t *testing.T) *Config {
	t.Helper()
	root := t.TempDir()

	cfg, err := NewConfig(paths.Roots{
		Config: filepath.Join(root, "config"),
		Data:   filepath.Join(root, "data"),
		State:  filepath.Join(root, "state"),
		Cache:  filepath.Join(root, "cache"),
	})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if err := cfg.Roots.Ensure(); err != nil {
		t.Fatalf("prepare roots: %v", err)
	}
	return cfg
}

// populate puts a file in one service's data directory, which is what
// initdb and the object store do and therefore what "this plane has already
// been provisioned" looks like from outside.
func populate(t *testing.T, cfg *Config, service paths.Service) {
	t.Helper()
	dir, err := cfg.Roots.ServiceDataDir(service)
	if err != nil {
		t.Fatalf("service data dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create %s dir: %v", service, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("18\n"), 0o600); err != nil {
		t.Fatalf("populate %s: %v", service, err)
	}
}

// TestFreshPlaneMintsAKey is first-run setup: nothing has been provisioned,
// so creating the key silently is right and is item 2's no-ceremony default.
func TestFreshPlaneMintsAKey(t *testing.T) {
	cfg := planeAt(t)

	key, err := rootKeyFor(cfg, lifecycleUp)
	if err != nil {
		t.Fatalf("a fresh plane refused to create a key: %v", err)
	}
	if len(key) == 0 {
		t.Fatal("no key material")
	}
}

// TestExistingPlaneRefusesToMintAKey is the case that matters, and the one
// the previous implementation got wrong on every lifecycle operation.
//
// A data root holding a cluster carries a Postgres password derived from
// SOME key. Minting a new one produces a password that does not open it, and
// the failure surfaces as a three-minute readiness timeout naming nothing.
func TestExistingPlaneRefusesToMintAKey(t *testing.T) {
	cfg := planeAt(t)
	populate(t, cfg, paths.ServicePostgres)

	_, err := rootKeyFor(cfg, lifecycleUp)
	if !errors.Is(err, paths.ErrNoKey) {
		t.Fatalf("an existing plane with no key returned %v, want ErrNoKey", err)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.Roots.Config, paths.KeyFileName)); !os.IsNotExist(statErr) {
		t.Fatal("a key was created for an existing plane")
	}
}

// TestObjectDataAloneMarksThePlaneExisting is why emptiness is judged across
// EVERY service directory rather than Postgres alone.
//
// The object store's credentials derive from the same root key, so a plane
// holding objects and no cluster is still a plane some earlier key
// provisioned — and minting a new key would leave every stored object
// unreachable.
func TestObjectDataAloneMarksThePlaneExisting(t *testing.T) {
	cfg := planeAt(t)
	populate(t, cfg, paths.ServiceMinIO)

	if _, err := rootKeyFor(cfg, lifecycleUp); !errors.Is(err, paths.ErrNoKey) {
		t.Fatalf("a plane holding objects but no cluster returned %v, want ErrNoKey: the "+
			"object-store credentials derive from the same key", err)
	}
}

// TestExistingPlaneLoadsItsOwnKey is the ordinary path after setup: the key
// is present, the plane opens, and the key is the one already there.
func TestExistingPlaneLoadsItsOwnKey(t *testing.T) {
	cfg := planeAt(t)

	created, err := rootKeyFor(cfg, lifecycleUp) // fresh: mints
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	populate(t, cfg, paths.ServicePostgres) // now provisioned

	loaded, err := rootKeyFor(cfg, lifecycleUp)
	if err != nil {
		t.Fatalf("an existing plane with its key refused to open: %v", err)
	}
	if string(loaded) != string(created) {
		t.Fatal("the plane loaded a different key than the one it was provisioned with")
	}
}

// TestOnlyUpMayMintAKey is the accepted D4 contract, and emptiness alone
// does not express it.
//
// An empty data root means "no plane has been provisioned"; it does not mean
// "this operation is the one that provisions it". Neither `migrate` nor
// `force-version` creates a plane, so a key either of them generated would
// belong to nothing — until the eventual `up` adopted it silently, which is
// the state that makes the key's provenance unanswerable.
func TestOnlyUpMayMintAKey(t *testing.T) {
	for _, operation := range []lifecycle{lifecycleMigrate, lifecycleForceVersion} {
		cfg := planeAt(t) // empty data root: `up` would be allowed to create

		_, err := rootKeyFor(cfg, operation)
		if !errors.Is(err, ErrPlaneLocked) {
			t.Fatalf("%s against an empty root returned %v, want ErrPlaneLocked", operation, err)
		}
		if _, statErr := os.Stat(filepath.Join(cfg.Roots.Config, paths.KeyFileName)); !os.IsNotExist(statErr) {
			t.Fatalf("%s created a key file", operation)
		}

		// And the same operation succeeds once a key exists, so the refusal
		// is about CREATION rather than about the operation being locked out.
		if _, err := rootKeyFor(cfg, lifecycleUp); err != nil {
			t.Fatalf("up could not create the key: %v", err)
		}
		if _, err := rootKeyFor(cfg, operation); err != nil {
			t.Fatalf("%s could not load an existing key: %v", operation, err)
		}
	}
}

// TestRefusalIsTypedForItem8 pins the observable restore state the backup
// operation builds its two-part sequence on: refuse, supply the key, open.
// A bare "file not found" would not distinguish that from a first run.
func TestRefusalIsTypedForItem8(t *testing.T) {
	cfg := planeAt(t)
	populate(t, cfg, paths.ServicePostgres)

	_, err := rootKeyFor(cfg, lifecycleUp)
	if !errors.Is(err, ErrPlaneLocked) {
		t.Fatalf("returned %v, want ErrPlaneLocked", err)
	}
	// It still carries the underlying cause, so a caller debugging the file
	// itself is not left guessing which path was checked.
	if !errors.Is(err, paths.ErrNoKey) {
		t.Fatalf("ErrPlaneLocked does not wrap ErrNoKey: %v", err)
	}
}

// TestRefusalNamesWhatToDo keeps the diagnosis useful. The state is
// recoverable — restore the key, or run new-key recovery — and an error that
// says only "not present" leaves an operator guessing at exactly the moment
// they are restoring from backup.
func TestRefusalNamesWhatToDo(t *testing.T) {
	cfg := planeAt(t)
	populate(t, cfg, paths.ServicePostgres)

	_, err := rootKeyFor(cfg, lifecycleUp)
	if err == nil {
		t.Fatal("no refusal")
	}
	for _, phrase := range []string{"Restore the key file", "recovery", "locked"} {
		if !strings.Contains(err.Error(), phrase) {
			t.Errorf("the refusal does not mention %q: %v", phrase, err)
		}
	}
}
