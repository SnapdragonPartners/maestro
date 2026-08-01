package secret

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestValueRedactsEveryRenderingPath is the guard whose partial version
// passes: a Value that implements only String satisfies %v and %s while
// leaking through %#v, %q and json.Marshal, and a test that checks one verb
// cannot tell the difference.
//
// So every path is asserted, including the ones a struct field reaches
// rather than the value itself — because the way this defect actually
// happens is somebody formatting the surrounding struct, not the secret.
func TestValueRedactsEveryRenderingPath(t *testing.T) {
	const plaintext = "ghp_this_must_never_appear"
	value := NewValue([]byte(plaintext))

	holder := struct {
		Token Value  `json:"token"`
		Note  string `json:"note"`
	}{Token: value, Note: "surrounding field"}

	for name, rendered := range map[string]string{
		"%v on the value":     fmt.Sprintf("%v", value),
		"%s on the value":     fmt.Sprintf("%s", value),
		"%q on the value":     fmt.Sprintf("%q", value),
		"%x on the value":     fmt.Sprintf("%x", value),
		"%#v on the value":    fmt.Sprintf("%#v", value),
		"%+v on the value":    fmt.Sprintf("%+v", value),
		"%v on the struct":    fmt.Sprintf("%v", holder),
		"%+v on the struct":   fmt.Sprintf("%+v", holder),
		"%#v on the struct":   fmt.Sprintf("%#v", holder),
		"json on the value":   mustMarshal(t, value),
		"json on the struct":  mustMarshal(t, holder),
		"Error-style %v":      fmt.Sprintf("%v", fmt.Errorf("wrapping %v", value)),
		"String() directly":   value.String(),
		"GoString() directly": value.GoString(),
	} {
		if strings.Contains(rendered, plaintext) {
			t.Errorf("%s leaked the plaintext: %s", name, rendered)
		}
		if !strings.Contains(rendered, redacted) {
			t.Errorf("%s rendered %q, which does not mark the value as redacted", name, rendered)
		}
	}
}

func mustMarshal(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// TestRevealReturnsThePlaintext is the other half. A credential nobody can
// read is not a credential, and the escape hatch has to work — what makes it
// safe is that it is named, not that it is absent.
func TestRevealReturnsThePlaintext(t *testing.T) {
	const plaintext = "the caller asked for this deliberately"
	value := NewValue([]byte(plaintext))

	if got := string(value.Reveal()); got != plaintext {
		t.Fatalf("Reveal returned %q, want %q", got, plaintext)
	}
	if value.Len() != len(plaintext) {
		t.Fatalf("Len is %d, want %d", value.Len(), len(plaintext))
	}
}

// TestNewValueCopiesItsInput stops a caller's buffer reuse from mutating a
// secret already handed out — the kind of aliasing that turns one
// credential into another with no error anywhere.
func TestNewValueCopiesItsInput(t *testing.T) {
	buffer := []byte("original secret")
	value := NewValue(buffer)

	copy(buffer, "overwritten!!!!")

	if got := string(value.Reveal()); got != "original secret" {
		t.Fatalf("the stored secret changed with the caller's buffer: %q", got)
	}
}

// TestRevealCopiesOnTheWayOut is the other end of the same aliasing, and the
// more dangerous one.
//
// Copying a Value copies only the slice header, so every copy shares one
// backing array. If Reveal returned that array, a caller zeroing or reusing
// what it revealed would corrupt the secret held by copies it has never
// seen — including ones already passed to somebody else. Nothing errors; the
// credential is simply wrong from then on.
func TestRevealCopiesOnTheWayOut(t *testing.T) {
	const plaintext = "the credential itself"
	value := NewValue([]byte(plaintext))

	// The obvious case: mutate what one Reveal returned.
	revealed := value.Reveal()
	for i := range revealed {
		revealed[i] = 'x'
	}
	if got := string(value.Reveal()); got != plaintext {
		t.Fatalf("a later Reveal returned %q after the first was mutated", got)
	}

	// And through a COPY of the Value, which is how it would really happen:
	// the secret is passed somewhere, that code clears its own buffer, and
	// the original is silently destroyed.
	duplicate := value
	scratch := duplicate.Reveal()
	for i := range scratch {
		scratch[i] = 0
	}
	if got := string(value.Reveal()); got != plaintext {
		t.Fatalf("clearing a copy's revealed bytes changed the original to %q", got)
	}
	if got := string(duplicate.Reveal()); got != plaintext {
		t.Fatalf("the copy's own secret was destroyed by clearing what it revealed: %q", got)
	}
}
