package secret

import (
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

// The root key must not be recoverable from, or visible in, a credential.
func TestDeriveDoesNotLeakRootKey(t *testing.T) {
	key := rootKey()
	got, err := Derive(key, ContextPostgresPassword)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	for _, encoded := range []string{
		string(key),
		hexOf(key),
	} {
		if strings.Contains(got, encoded) {
			t.Error("derived credential contains the root key verbatim")
		}
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0xf])
	}
	return string(out)
}

func TestDeriveRejectsEmptyInputs(t *testing.T) {
	if _, err := Derive(nil, ContextPostgresPassword); !errors.Is(err, ErrNoRootKey) {
		t.Errorf("Derive(nil) = %v; want ErrNoRootKey", err)
	}
	if _, err := Derive(rootKey(), ""); err == nil {
		t.Error("Derive with an empty context was accepted; domain separation is not optional")
	}
}
