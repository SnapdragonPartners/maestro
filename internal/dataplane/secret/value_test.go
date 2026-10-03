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

// TestRedactReplacesEveryOccurrenceAndNothingElse is the boundary's
// redaction pass (item 5 design, D8) at the unit: every occurrence goes,
// text without the secret is untouched, and an empty secret is a no-op
// rather than a text-destroying insertion between every character.
//
// THE MUTANT: replace only the first occurrence (strings.Replace with n=1)
// and the "twice" case reads the token back; drop the empty guard and the
// empty case comes back mangled.
func TestRedactReplacesEveryOccurrenceAndNothingElse(t *testing.T) {
	const token = "ghp_secret_token_value"
	const reference = "secret:0193b4f0-0000-7000-8000-000000000000@3"
	value := NewValue([]byte(token))

	for name, tc := range map[string]struct{ in, want string }{
		"once":       {"Authorization: token " + token, "Authorization: token " + reference},
		"twice":      {token + " and again " + token, reference + " and again " + reference},
		"absent":     {"nothing to see", "nothing to see"},
		"substring":  {"prefix" + token + "suffix", "prefix" + reference + "suffix"},
		"empty text": {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := value.Redact(tc.in, reference); got != tc.want {
				t.Fatalf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(value.Redact(tc.in, reference), token) {
				t.Fatal("the token survived redaction")
			}
		})
	}

	empty := NewValue(nil)
	if got := empty.Redact("untouched text", reference); got != "untouched text" {
		t.Fatalf("an empty secret changed the text to %q", got)
	}
}

// TestRedactNeverReintroducesTheSecret (PR #384 review, two rounds): the
// result never contains the plaintext -- not through a replacement that
// contains it, not through the fallback marker containing it, and not
// through a replacement boundary recreating it.
//
// THE MUTANTS: drop the fallback and "secret:" is back in the output; drop
// the repeat and "xa"-for-"ab" leaves "xab"; drop the whole-text fallback
// and a crafted oscillation survives the passes.
func TestRedactNeverReintroducesTheSecret(t *testing.T) {
	const reference = "secret:0193b4f0-0000-7000-8000-000000000000@3"
	cases := map[string]struct{ plaintext, text, replacement string }{
		"plaintext inside the reference":            {"secret:", "token=secret: sent", reference},
		"plaintext is the reference":                {reference, "x" + reference + "y", reference},
		"plaintext inside [redacted] too":           {"e", "token=e", reference},
		"single-byte # plaintext":                   {"#", "a#b", "#"},
		"boundary recreates the plaintext":          {"ab", "abb", "xa"},
		"boundary recreates it past the pass bound": {"ab", "ab" + strings.Repeat("b", 3*redactPasses), "xa"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := NewValue([]byte(tc.plaintext)).Redact(tc.text, tc.replacement)
			if strings.Contains(got, tc.plaintext) {
				t.Fatalf("plaintext %q survived redaction: %q", tc.plaintext, got)
			}
		})
	}
	// The repeat and the whole-text fallback are each asserted by their
	// RESULT, because absence alone cannot tell them apart: one pass plus
	// the fallback is also plaintext-free. Two passes on "abb" give "xxa";
	// a run the bound cannot converge gives the marker alone.
	if got := NewValue([]byte("ab")).Redact("abb", "xa"); got != "xxa" {
		t.Fatalf("Redact(abb, xa) = %q, want the repeated substitution xxa", got)
	}
	if got := NewValue([]byte("ab")).Redact("ab"+strings.Repeat("b", 3*redactPasses), "xa"); got != "" {
		t.Fatalf("a run past the pass bound = %q, want the empty string", got)
	}
	// And the replacement for a plaintext inside both the reference and
	// "[redacted]" is the empty string, applied to every occurrence.
	if got := NewValue([]byte("e")).Redact("eve", reference); got != "v" {
		t.Fatalf("Redact(eve) = %q, want v", got)
	}
	// The ordinary case still uses the caller's replacement, once.
	if got := NewValue([]byte("ghp_x")).Redact("ghp_x", reference); got != reference {
		t.Fatalf("Redact = %q, want %q", got, reference)
	}
}

// TestRedactAllComposesAcrossSecrets (PR #384 review): redacting one secret
// after another is not safe when a later replacement contains an earlier
// plaintext -- secret A "secret:" and secret B "TOKEN" with B's reference
// as its replacement puts A back. RedactAll chooses every replacement
// against every plaintext and verifies none remains after all of them.
//
// THE MUTANT: choose each replacement against its own plaintext only --
// A's bytes come back through B's reference.
func TestRedactAllComposesAcrossSecrets(t *testing.T) {
	const refA = "secret:0193b4f0-0000-7000-8000-00000000000a@1"
	const refB = "secret:0193b4f0-0000-7000-8000-00000000000b@2"
	a := NewValue([]byte("secret:"))
	b := NewValue([]byte("TOKEN"))
	text := "auth secret: TOKEN sent"

	// The sequential composition the finding describes is unsafe, which is
	// the reason RedactAll exists: shown, not assumed.
	sequential := b.Redact(a.Redact(text, refA), refB)
	if !strings.Contains(sequential, "secret:") {
		t.Fatalf("sequential Redact happened to be safe here: %q; the premise of RedactAll changed", sequential)
	}

	got := RedactAll(text, []Redaction{{Value: a, Replacement: refA}, {Value: b, Replacement: refB}})
	for _, plaintext := range []string{"secret:", "TOKEN"} {
		if strings.Contains(got, plaintext) {
			t.Fatalf("plaintext %q survived RedactAll: %q", plaintext, got)
		}
	}
	// NEITHER reference is safe: both contain A's plaintext "secret:", B's
	// included -- which is exactly why choosing B's replacement against B
	// alone is wrong. The marker stands in for both.
	if got != "auth [redacted] [redacted] sent" {
		t.Fatalf("RedactAll = %q", got)
	}
	// With a plaintext no reference contains, the reference is used.
	c := NewValue([]byte("ghp_c"))
	if mixed := RedactAll("secret: ghp_c", []Redaction{{Value: a, Replacement: refA}, {Value: c, Replacement: refB}}); mixed != "[redacted] [redacted]" {
		// refB contains "secret:" too, so c's replacement is also unsafe here.
		t.Fatalf("RedactAll = %q", mixed)
	}
	if alone := RedactAll("x ghp_c y", []Redaction{{Value: c, Replacement: refB}}); alone != "x "+refB+" y" {
		t.Fatalf("a safe reference was not used: %q", alone)
	}
	// Order of the redactions does not matter.
	if reversed := RedactAll(text, []Redaction{{Value: b, Replacement: refB}, {Value: a, Replacement: refA}}); reversed != got {
		t.Fatalf("RedactAll is order-dependent: %q vs %q", reversed, got)
	}
	// An empty secret in the batch is skipped, not a between-every-byte insertion.
	if withEmpty := RedactAll(text, []Redaction{{Value: NewValue(nil), Replacement: "x"}, {Value: b, Replacement: refB}}); withEmpty != "auth secret: "+refB+" sent" {
		t.Fatalf("RedactAll with an empty secret = %q", withEmpty)
	}
}

// TestRedactAllFallbackIsSafeAcrossABatch (PR #384 review): a batch holding
// "e" and "~" excludes both "[redacted]" and the earlier "~" fallback, and a
// third secret reaching the pass limit was then returned as "~" -- the
// second secret's plaintext, verbatim. The empty string is the only
// candidate no non-empty plaintext can be inside.
//
// THE MUTANT: restore "~" as the last fallback -- the result IS the "~"
// secret.
func TestRedactAllFallbackIsSafeAcrossABatch(t *testing.T) {
	batch := []Redaction{
		{Value: NewValue([]byte("ab")), Replacement: "xa"},
		{Value: NewValue([]byte("e")), Replacement: "secret:e"},
		{Value: NewValue([]byte("~")), Replacement: "~"},
	}
	text := "ab" + strings.Repeat("b", 3*redactPasses) + " e ~"
	got := RedactAll(text, batch)
	for _, plaintext := range []string{"ab", "e", "~"} {
		if strings.Contains(got, plaintext) {
			t.Fatalf("plaintext %q survived RedactAll past the pass limit: %q", plaintext, got)
		}
	}
	if got != "" {
		t.Fatalf("RedactAll past the pass limit = %q, want the empty string", got)
	}
	// Short of the limit, the same batch redacts each occurrence with the
	// empty string where no marker is safe.
	if short := RedactAll("x ab e ~ y", batch); short != "x xa   y" {
		t.Fatalf("RedactAll = %q", short)
	}
}

// TestRedactAllIsOrderIndependentForOverlappingSecrets (PR #384 review):
// with "ab" and "abc" in one batch, the substitution order decided whether
// "abc" met its own replacement or was cut by "ab"'s, and the batch comes
// from a map. Longest first, ties by bytes.
//
// THE MUTANT: drop the sort -- the two orders below differ.
func TestRedactAllIsOrderIndependentForOverlappingSecrets(t *testing.T) {
	short := Redaction{Value: NewValue([]byte("ab")), Replacement: "[S]"}
	long := Redaction{Value: NewValue([]byte("abc")), Replacement: "[L]"}
	text := "x abc y ab z"
	forward := RedactAll(text, []Redaction{short, long})
	reverse := RedactAll(text, []Redaction{long, short})
	if forward != reverse {
		t.Fatalf("order-dependent: %q vs %q", forward, reverse)
	}
	if forward != "x [L] y [S] z" {
		t.Fatalf("RedactAll = %q; the longer secret must meet its own replacement", forward)
	}
}

// TestRedactAllCoalescesEqualPlaintexts (PR #384 review): two secrets with
// the same bytes and different references were substituted by whichever
// came first, so the record could name the wrong revision depending on map
// order. Equal plaintexts coalesce; disagreeing replacements take the
// marker, agreeing ones keep theirs.
//
// THE MUTANT: keep the first replacement on a collision -- the two orders
// below name different references.
func TestRedactAllCoalescesEqualPlaintexts(t *testing.T) {
	const refA = "secret:0193b4f0-0000-7000-8000-00000000000a@1"
	const refB = "secret:0193b4f0-0000-7000-8000-00000000000b@2"
	same := []byte("ghp_shared")
	a := Redaction{Value: NewValue(same), Replacement: refA}
	b := Redaction{Value: NewValue(same), Replacement: refB}
	forward := RedactAll("t=ghp_shared", []Redaction{a, b})
	reverse := RedactAll("t=ghp_shared", []Redaction{b, a})
	if forward != reverse {
		t.Fatalf("order-dependent: %q vs %q", forward, reverse)
	}
	if forward != "t="+redacted {
		t.Fatalf("RedactAll = %q; disagreeing references for one plaintext must take the marker, not one of them", forward)
	}
	// The same secret resolved into two slots agrees on its reference and keeps it.
	if agreed := RedactAll("t=ghp_shared", []Redaction{a, a}); agreed != "t="+refA {
		t.Fatalf("RedactAll with agreeing replacements = %q", agreed)
	}
}
