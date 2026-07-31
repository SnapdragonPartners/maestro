package secret

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"
)

var testRoot = []byte("a root-of-trust key, thirty-two+ bytes long")

// testBinding is a complete, valid binding a case can vary one field of.
func testBinding() Binding {
	owner := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	return Binding{
		OwnerUserID:    &owner,
		Name:           "forge-token",
		ScopeType:      "repository",
		OrganizationID: uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		SecretID:       uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		ScopeID:        uuid.MustParse("44444444-4444-4444-8444-444444444444"),
		Version:        1,
	}
}

func TestSealAndOpenRoundTrip(t *testing.T) {
	plaintext := []byte("ghp_the_token_nobody_should_ever_see")

	envelope, err := Seal(testRoot, testBinding(), plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if envelope.Scheme != SchemeAESGCMv1 {
		t.Fatalf("scheme is %q, want %q", envelope.Scheme, SchemeAESGCMv1)
	}
	if len(envelope.Nonce) != nonceBytes {
		t.Fatalf("nonce is %d bytes, want %d", len(envelope.Nonce), nonceBytes)
	}
	// The stored bytes must not be the plaintext, nor contain it.
	if bytes.Contains(envelope.Ciphertext, plaintext) {
		t.Fatal("the ciphertext contains the plaintext verbatim")
	}
	// At least GCM's tag, which is what the schema's own CHECK enforces.
	if len(envelope.Ciphertext) < 16 {
		t.Fatalf("ciphertext is %d bytes, shorter than GCM's tag", len(envelope.Ciphertext))
	}

	opened, err := Open(testRoot, testBinding(), envelope)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened.Reveal(), plaintext) {
		t.Fatalf("round trip returned %q", opened.Reveal())
	}
}

// TestSealIsNotDeterministic covers the nonce doing its job: two seals of
// identical plaintext under an identical binding must differ, or an observer
// of the table learns which rows hold the same credential.
func TestSealIsNotDeterministic(t *testing.T) {
	plaintext := []byte("the same secret twice")

	first, err := Seal(testRoot, testBinding(), plaintext)
	if err != nil {
		t.Fatalf("first seal: %v", err)
	}
	second, err := Seal(testRoot, testBinding(), plaintext)
	if err != nil {
		t.Fatalf("second seal: %v", err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("two seals drew the same nonce")
	}
	if bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("two seals of one plaintext produced identical ciphertext")
	}
}

// TestEveryBoundFieldIsAuthenticated is the AAD's whole purpose, and the
// case an earlier design missed.
//
// Moving a ciphertext to another row fails on the derived key before
// authentication is ever reached, so a test that did that proved nothing.
// What matters is editing the METADATA on the row the ciphertext already
// belongs to: the key covers only the id and version, so without the AAD an
// owner swap leaves the secret perfectly readable by somebody else.
func TestEveryBoundFieldIsAuthenticated(t *testing.T) {
	plaintext := []byte("bound to exactly one identity")
	envelope, err := Seal(testRoot, testBinding(), plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	otherUser := uuid.MustParse("55555555-5555-4555-8555-555555555555")
	for name, mutate := range map[string]func(*Binding){
		"a different owner":      func(b *Binding) { b.OwnerUserID = &otherUser },
		"no owner at all":        func(b *Binding) { b.OwnerUserID = nil },
		"a different name":       func(b *Binding) { b.Name = "other-token" },
		"a different scope type": func(b *Binding) { b.ScopeType = "product" },
		"a different scope id": func(b *Binding) {
			b.ScopeID = uuid.MustParse("66666666-6666-4666-8666-666666666666")
		},
		"a different organization": func(b *Binding) {
			b.OrganizationID = uuid.MustParse("77777777-7777-4777-8777-777777777777")
		},
	} {
		t.Run(name, func(t *testing.T) {
			tampered := testBinding()
			mutate(&tampered)
			if _, err := Open(testRoot, tampered, envelope); !errors.Is(err, ErrDecrypt) {
				t.Fatalf("opening under %s returned %v, want ErrDecrypt: the binding is not "+
					"authenticated, so editing this column retargets a working credential", name, err)
			}
		})
	}
}

// TestVersionIsPartOfTheKey covers replacement's fence. A row read at one
// version cannot be opened as another, because the key context carries it —
// which is also what makes nonce reuse across rewrites impossible.
func TestVersionIsPartOfTheKey(t *testing.T) {
	envelope, err := Seal(testRoot, testBinding(), []byte("version one"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	next := testBinding()
	next.Version = 2
	if _, err := Open(testRoot, next, envelope); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("opening version 1's ciphertext as version 2 returned %v, want ErrDecrypt", err)
	}
}

// TestTamperedCiphertextFails is GCM's authentication, asserted rather than
// assumed: silent corruption of a credential is worse than a missing one.
func TestTamperedCiphertextFails(t *testing.T) {
	envelope, err := Seal(testRoot, testBinding(), []byte("do not corrupt me"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	envelope.Ciphertext[0] ^= 0xff

	if _, err := Open(testRoot, testBinding(), envelope); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("a flipped byte opened anyway, or failed as %v", err)
	}
}

// TestADifferentRootKeyFails is the property the whole vault rests on: the
// plane holds ciphertext, and without the external key it holds nothing
// else.
func TestADifferentRootKeyFails(t *testing.T) {
	envelope, err := Seal(testRoot, testBinding(), []byte("locked to one root"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := Open([]byte("an entirely different root key value"), testBinding(), envelope); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("a foreign root key opened the secret, or failed as %v", err)
	}
}

// TestUnknownSchemeIsDistinctFromCorruption keeps "this build is too old"
// from reading as "this data is damaged". One is resolved by upgrading, the
// other by restoring.
func TestUnknownSchemeIsDistinctFromCorruption(t *testing.T) {
	envelope, err := Seal(testRoot, testBinding(), []byte("sealed today"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	envelope.Scheme = "aes-256-gcm/hkdf-sha256/v2"

	_, err = Open(testRoot, testBinding(), envelope)
	if !errors.Is(err, ErrUnknownScheme) {
		t.Fatalf("an unrecognised scheme returned %v, want ErrUnknownScheme", err)
	}
	if errors.Is(err, ErrDecrypt) {
		t.Fatal("an unreadable scheme reported itself as corruption")
	}
}

// TestSealRefusesAnUnversionedBinding stops a zero version reaching the key
// context, where it would silently derive a key no row could ever name.
func TestSealRefusesAnUnversionedBinding(t *testing.T) {
	unversioned := testBinding()
	unversioned.Version = 0
	if _, err := Seal(testRoot, unversioned, []byte("x")); err == nil {
		t.Fatal("a binding with no version was sealed")
	}
}

// TestDeriveKeyReturnsRawBytes is why DeriveKey exists at all: Derive's
// output is base64 TEXT, and handing that to AES-256 would use 43 bytes
// where the cipher wants 32 — wrong length, wrong bytes, and everything
// still compiles.
func TestDeriveKeyReturnsRawBytes(t *testing.T) {
	raw, err := DeriveKey(testRoot, ContextPostgresPassword)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if len(raw) != derivedBytes {
		t.Fatalf("DeriveKey returned %d bytes, want %d", len(raw), derivedBytes)
	}

	encoded, err := Derive(testRoot, ContextPostgresPassword)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(encoded) == len(raw) {
		t.Fatal("the encoded and raw forms are the same length, so one of them is not what it claims")
	}
	// The two must be the same derivation, or "one context, one secret"
	// stops being true across the encoded and raw forms.
	if encoded != base64.RawURLEncoding.EncodeToString(raw) {
		t.Fatal("Derive and DeriveKey disagree about the same context")
	}
}

// TestKeyContextSeparatesSecretsAndVersions covers the derivation domain
// itself: two secrets, or two versions of one secret, must never share a
// context, because that is the whole mechanism behind per-version keys.
func TestKeyContextSeparatesSecretsAndVersions(t *testing.T) {
	first := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	second := uuid.MustParse("88888888-8888-4888-8888-888888888888")

	seen := map[string]string{}
	for label, context := range map[string]string{
		"first@v1":  KeyContext(first, 1),
		"first@v2":  KeyContext(first, 2),
		"second@v1": KeyContext(second, 1),
	} {
		if previous, clash := seen[context]; clash {
			t.Fatalf("%s and %s share the derivation context %q", label, previous, context)
		}
		seen[context] = label
	}
}
