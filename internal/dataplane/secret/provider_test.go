package secret

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// TestUnimplementedBackendsRefuseAtConstruction is D3's refusal, and the
// reason it happens HERE rather than at first use.
//
// A stub returning anything — an empty key, or a quiet fall-through to the
// key file — would encrypt real secrets under a key the operator did not
// choose and believes they did not use. That is silent when it happens and
// unrecoverable afterwards, because the operator's model of which key
// protects the vault is wrong.
//
// A provider that refuses LATER is a weaker version of the same problem: it
// can be constructed, held and passed around, and a caller may have decided
// the plane is usable long before it asks for key material. So no provider
// comes back at all.
func TestUnimplementedBackendsRefuseAtConstruction(t *testing.T) {
	for _, backend := range []Backend{BackendKeychain, BackendPassphrase} {
		root := t.TempDir()

		provider, err := ProviderFor(backend, root, MayCreate)
		if !errors.Is(err, ErrBackendNotImplemented) {
			t.Fatalf("ProviderFor(%s) returned %v, want ErrBackendNotImplemented", backend, err)
		}
		if provider != nil {
			t.Fatalf("%s returned a usable provider alongside its refusal", backend)
		}
		// The failure names which backend, or an operator who selected one
		// cannot tell it from the other.
		if !strings.Contains(err.Error(), string(backend)) {
			t.Fatalf("the refusal does not name the backend: %v", err)
		}
		// And nothing was provisioned on the way out: a silent fall-through
		// to the key file is what refusing exists to prevent.
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			t.Fatalf("read config root: %v", readErr)
		}
		if len(entries) != 0 {
			t.Fatalf("selecting %s left %d files in the config root", backend, len(entries))
		}
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

// TestResolvedKeyRejectsAnIncompleteValue covers every way the constructor can
// be handed something that would fail later instead of here.
//
// The point of returning an error at all is to fail where the mistake is made.
// An earlier revision checked only the source, which left empty material and
// typo'd backends still deferring to first use — the very thing the error
// return was added to prevent.
func TestResolvedKeyRejectsAnIncompleteValue(t *testing.T) {
	for name, tc := range map[string]struct {
		key    []byte
		source Backend
		is     error
	}{
		"no material":       {key: nil, source: BackendKeyFile, is: ErrNoRootKey},
		"empty material":    {key: []byte{}, source: BackendOperatorProvided, is: ErrNoRootKey},
		"unnamed source":    {key: []byte("material"), source: ""},
		"misspelled source": {key: []byte("material"), source: "keyfile"},
		"invented source":   {key: []byte("material"), source: "gcp-kms"},
	} {
		t.Run(name, func(t *testing.T) {
			provider, err := ResolvedKey(tc.key, tc.source)
			if err == nil {
				t.Fatal("accepted, so the failure is deferred to the first vault operation, a long " +
					"way from the caller that caused it")
			}
			if provider != nil {
				t.Fatal("a refusal returned a usable provider, which invites the fall-through the " +
					"refusal exists to prevent")
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("refusal does not wrap %v: %v", tc.is, err)
			}
		})
	}
}

// TestResolvedKeyAcceptsEveryNamedBackend is the control for the table above.
// Without it, the rejections would pass just as well against a constructor
// that refused everything.
func TestResolvedKeyAcceptsEveryNamedBackend(t *testing.T) {
	for _, source := range knownBackends {
		t.Run(string(source), func(t *testing.T) {
			provider, err := ResolvedKey([]byte("material"), source)
			if err != nil {
				t.Fatalf("named backend %q was refused: %v", source, err)
			}
			if got := provider.Backend(); got != source {
				t.Fatalf("provider reports %q, want %q", got, source)
			}
		})
	}
}

// TestResolvedKeyReportsTheSourceItWasGiven pins the behaviour the hardcoded
// constant used to break, using a backend OTHER than the key file on purpose:
// BackendKeyFile would pass against the old implementation too and could not
// tell the fix from the defect.
func TestResolvedKeyReportsTheSourceItWasGiven(t *testing.T) {
	provider, err := ResolvedKey([]byte("material"), BackendOperatorProvided)
	if err != nil {
		t.Fatalf("a named source was refused: %v", err)
	}
	if got := provider.Backend(); got != BackendOperatorProvided {
		t.Fatalf("resolved key reports backend %q, want %q — a provider that renames its own "+
			"source misdirects the operator who needs to know which key protects the vault",
			got, BackendOperatorProvided)
	}
	key, err := provider.RootKey()
	if err != nil {
		t.Fatalf("root key: %v", err)
	}
	if string(key) != "material" {
		t.Fatalf("resolved key returned %q, want %q", key, "material")
	}
}
