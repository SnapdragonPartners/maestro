package secret

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/internal/dataplane/paths"
)

// TestLoadOnlyRefusesAMissingKey is the state a restored data root arrives
// in, and the whole reason the two accesses are distinguished.
//
// Before this split every lifecycle operation called EnsureKey, so a plane
// restored without its key silently got a NEW one — and then failed three
// minutes later on a readiness timeout, because the freshly derived Postgres
// password does not open a cluster initdb wrote under the original.
func TestLoadOnlyRefusesAMissingKey(t *testing.T) {
	root := t.TempDir()

	_, err := KeyFile(root, LoadOnly).RootKey()
	if !errors.Is(err, paths.ErrNoKey) {
		t.Fatalf("load-only access returned %v, want ErrNoKey", err)
	}

	// And it did not create one on the way out, which is the half that
	// would turn a diagnosable refusal back into a silent regeneration.
	if _, statErr := os.Stat(filepath.Join(root, paths.KeyFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("a key file exists after a load-only refusal (%v)", statErr)
	}
}

// TestMayCreateGeneratesThenLoadsTheSameKey covers first-run setup and the
// run after it: creation is silent, and every later load returns what was
// created rather than a fresh key.
func TestMayCreateGeneratesThenLoadsTheSameKey(t *testing.T) {
	root := t.TempDir()

	created, err := KeyFile(root, MayCreate).RootKey()
	if err != nil {
		t.Fatalf("first-run creation: %v", err)
	}
	if len(created) == 0 {
		t.Fatal("creation returned no key material")
	}

	loaded, err := KeyFile(root, LoadOnly).RootKey()
	if err != nil {
		t.Fatalf("load after creation: %v", err)
	}
	if !bytes.Equal(created, loaded) {
		t.Fatal("the loaded key differs from the created one; every credential derived before the " +
			"reload would stop working")
	}

	// MayCreate on an existing key must also load rather than replace it —
	// otherwise a second `up` on a fresh plane would rotate the key out from
	// under the vault it just created.
	again, err := KeyFile(root, MayCreate).RootKey()
	if err != nil {
		t.Fatalf("second creation attempt: %v", err)
	}
	if !bytes.Equal(created, again) {
		t.Fatal("MayCreate replaced an existing key")
	}
}

// TestUnimplementedBackendsRefuseByName is D3's refusal, and the reason it is
// a refusal rather than a stub.
//
// A stub returning anything — an empty key, or a quiet fall-through to the
// key file — would encrypt real secrets under a key the operator did not
// choose and believes they did not use. That is silent when it happens and
// unrecoverable afterwards, because the operator's model of which key
// protects the vault is wrong.
func TestUnimplementedBackendsRefuseByName(t *testing.T) {
	for _, backend := range []Backend{BackendKeychain, BackendPassphrase} {
		provider, err := ProviderFor(backend, t.TempDir(), MayCreate)
		if err != nil {
			t.Fatalf("ProviderFor(%s): %v", backend, err)
		}
		if provider.Backend() != backend {
			t.Fatalf("provider reports backend %q, want %q", provider.Backend(), backend)
		}

		key, err := provider.RootKey()
		if !errors.Is(err, ErrBackendNotImplemented) {
			t.Fatalf("%s returned %v, want ErrBackendNotImplemented", backend, err)
		}
		if key != nil {
			t.Fatalf("%s returned %d bytes of key material alongside its refusal", backend, len(key))
		}
		// The failure names which backend, or an operator who selected one
		// cannot tell it from the other.
		if !bytes.Contains([]byte(err.Error()), []byte(backend)) {
			t.Fatalf("the refusal does not name the backend: %v", err)
		}
	}
}

// TestUnimplementedBackendsCreateNothing is the specific fall-through this
// design refuses: selecting keychain must not quietly provision a key file.
func TestUnimplementedBackendsCreateNothing(t *testing.T) {
	root := t.TempDir()

	provider, err := ProviderFor(BackendKeychain, root, MayCreate)
	if err != nil {
		t.Fatalf("ProviderFor: %v", err)
	}
	if _, keyErr := provider.RootKey(); keyErr == nil {
		t.Fatal("the keychain backend returned a key")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read config root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("selecting an unimplemented backend left %d files in the config root; a silent "+
			"fall-through to the key file is exactly what refusing exists to prevent", len(entries))
	}
}

// TestUnknownBackendIsAConstructionError separates "named but unbuilt" from
// "not a backend at all". The first is a refusal a caller may report; the
// second is a configuration mistake.
func TestUnknownBackendIsAConstructionError(t *testing.T) {
	if _, err := ProviderFor(Backend("vault-server"), t.TempDir(), LoadOnly); err == nil {
		t.Fatal("an unknown backend was accepted")
	}
}
