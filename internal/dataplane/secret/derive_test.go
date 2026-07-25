package secret

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func rootKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func TestDeriveIsDeterministic(t *testing.T) {
	first, err := Derive(rootKey(), ContextPostgresPassword)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	second, err := Derive(rootKey(), ContextPostgresPassword)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	// Determinism is the whole mechanism: the password is never stored, so
	// every startup must reproduce exactly the one Postgres was created
	// with, or the cluster becomes unreachable.
	if first != second {
		t.Errorf("derivation is not deterministic: %q vs %q", first, second)
	}
	if first == "" {
		t.Error("derived credential is empty")
	}
}

// Domain separation is the reason for the info string. Without it, every
// consumer of the root key would get the same credential, so leaking one
// would leak all of them.
func TestDeriveSeparatesContexts(t *testing.T) {
	seen := map[string]string{}
	for _, ctx := range []string{ContextPostgresPassword, ContextObjectAccessKey, ContextObjectSecretKey} {
		got, err := Derive(rootKey(), ctx)
		if err != nil {
			t.Fatalf("Derive(%s): %v", ctx, err)
		}
		if prior, clash := seen[got]; clash {
			t.Fatalf("contexts %q and %q derive the same credential", prior, ctx)
		}
		seen[got] = ctx
	}
}

func TestDeriveVariesWithRootKey(t *testing.T) {
	a, err := Derive(rootKey(), ContextPostgresPassword)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	other := rootKey()
	other[0] ^= 0xff
	b, err := Derive(other, ContextPostgresPassword)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if a == b {
		t.Error("derivation ignores the root key")
	}
}

// The credential travels through connection strings, environment
// variables and CLI arguments, so it must need no escaping.
func TestDeriveIsShellAndURLSafe(t *testing.T) {
	got, err := Derive(rootKey(), ContextPostgresPassword)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	const safe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	for _, r := range got {
		if !strings.ContainsRune(safe, r) {
			t.Errorf("derived credential contains %q, which needs escaping: %s", r, got)
		}
	}
}

// The credential must not be a re-encoding of the root key.
//
// What this can and cannot check is worth being precise about. HKDF's
// one-wayness is not unit-testable, and an earlier version of this test
// pretended otherwise: it searched the output for the raw key bytes and
// for their hex encoding, and *both* comparisons were vacuous. The output
// is restricted to the base64url alphabet, so it can never contain raw
// bytes like NUL; and a 64-character hex needle cannot occur inside a
// 43-character haystack. It passed unconditionally, including against a
// Derive that leaked the key outright.
//
// The realistic regression is someone "simplifying" Derive into a direct
// encoding of the root key — dropping the KDF while keeping the shape. So
// that is what is asserted: the output must differ from the key's own
// encodings, and no meaningful run of the key may survive into it.
func TestDeriveIsNotAnEncodingOfTheRootKey(t *testing.T) {
	key := rootKey()
	got, err := Derive(key, ContextPostgresPassword)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	for name, encoded := range map[string]string{
		"base64url":        base64.RawURLEncoding.EncodeToString(key),
		"base64url padded": base64.URLEncoding.EncodeToString(key),
		"base64 std":       base64.RawStdEncoding.EncodeToString(key),
		"hex":              hex.EncodeToString(key),
		"hex, uppercase":   strings.ToUpper(hex.EncodeToString(key)),
	} {
		if got == encoded {
			t.Errorf("derived credential is just the root key in %s: the KDF has been bypassed", name)
		}
	}

	// A shared run long enough to matter would indicate the output is
	// partly the key. 8 hex characters is 4 key bytes — far beyond
	// coincidence for a 43-character output, and short enough to catch a
	// truncated-prefix bug.
	keyHex := hex.EncodeToString(key)
	const runLen = 8
	for i := 0; i+runLen <= len(keyHex); i++ {
		if strings.Contains(strings.ToLower(got), keyHex[i:i+runLen]) {
			t.Errorf("derived credential contains %q, a run of the root key's encoding", keyHex[i:i+runLen])
		}
	}
}

func TestDeriveRejectsEmptyInputs(t *testing.T) {
	if _, err := Derive(nil, ContextPostgresPassword); !errors.Is(err, ErrNoRootKey) {
		t.Errorf("Derive(nil) = %v; want ErrNoRootKey", err)
	}
	if _, err := Derive(rootKey(), ""); err == nil {
		t.Error("Derive with an empty context was accepted; domain separation is not optional")
	}
}
