package secret

import (
	"bytes"
	"errors"
	"testing"
)

// validRootKey is material of exactly the required length. Tests that are not
// about length must use it, or they assert the length check by accident.
func validRootKey() []byte { return bytes.Repeat([]byte{0xA5}, RootKeyLen) }

// TestResolvedKeyRejectsMaterialOfTheWrongLength covers the invariant that
// previously held only by accident.
//
// Every key reaching this constructor used to come from paths.LoadKey, which
// refuses anything that is not exactly RootKeyLen — so the length was satisfied
// by where the material came from rather than by any check here. Operator-
// provided material has no such history: a one-byte key derives usable-looking
// subkeys through HKDF and unlocks the same vault at a fraction of the intended
// entropy, and DeriveKey only rejects empty input.
//
// Long material is refused too. Truncating or hashing it to fit would accept two
// different inputs as the same key, and an operator who supplied 64 bytes
// believing all of them mattered would be wrong with nothing reporting it.
func TestResolvedKeyRejectsMaterialOfTheWrongLength(t *testing.T) {
	for name, key := range map[string][]byte{
		"one byte":      {0x01},
		"one short":     bytes.Repeat([]byte{0x02}, RootKeyLen-1),
		"one long":      bytes.Repeat([]byte{0x03}, RootKeyLen+1),
		"double length": bytes.Repeat([]byte{0x04}, RootKeyLen*2),
		"hex of the right length but wrong byte count": []byte("a5a5a5a5"),
	} {
		t.Run(name, func(t *testing.T) {
			provider, err := ResolvedKey(key, BackendOperatorProvided)
			if err == nil {
				t.Fatalf("%d bytes of material was accepted as a root key", len(key))
			}
			if provider != nil {
				t.Fatal("a refusal returned a usable provider")
			}
			if !errors.Is(err, ErrRootKeyLength) {
				t.Fatalf("a wrong-length key must be distinguishable from a missing one: %v", err)
			}
		})
	}
}

// TestResolvedKeyAcceptsMaterialOfExactlyTheRightLength is the control for the
// table above.
func TestResolvedKeyAcceptsMaterialOfExactlyTheRightLength(t *testing.T) {
	if _, err := ResolvedKey(validRootKey(), BackendOperatorProvided); err != nil {
		t.Fatalf("material of exactly %d bytes was refused: %v", RootKeyLen, err)
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
		"unnamed source":    {key: validRootKey(), source: ""},
		"misspelled source": {key: validRootKey(), source: "keyfile"},
		"invented source":   {key: validRootKey(), source: "gcp-kms"},
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
			provider, err := ResolvedKey(validRootKey(), source)
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
	provider, err := ResolvedKey(validRootKey(), BackendOperatorProvided)
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
	if !bytes.Equal(key, validRootKey()) {
		t.Fatalf("resolved key returned different material than it was given")
	}
}
