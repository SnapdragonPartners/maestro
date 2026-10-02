package secret

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// redacted is what a secret renders as everywhere except Reveal.
const redacted = "[redacted]"

// Value is a secret's plaintext, in a shape that does not print.
//
// The v1 defect this design exists to avoid was a forge token in a
// plaintext file. Its v2 equivalent is the same token in a log line, and it
// arrives the way that always happens: somebody formats a struct with %+v,
// or serialises an error body, and the credential rides along in something
// nobody was thinking about at the time.
//
// So plaintext leaves the vault as a Value rather than a string. Every
// rendering path -- fmt's verbs and encoding/json alike -- produces
// "[redacted]", and the bytes are reachable only through Reveal, whose name
// is greppable in review.
//
// This does not make leaking impossible. Reveal exists, and it must: a
// credential nobody can read is not a credential. What it does is make
// leaking DELIBERATE and visible, which is the difference between a mistake
// and a decision.
//
// A value type, not a pointer: fmt consults Formatter on the value it is
// given, so a pointer-receiver implementation would leave a plain
// `secret.Value` field printing its contents -- exactly the case that
// matters.
type Value struct {
	plaintext []byte
}

// NewValue wraps plaintext. The slice is copied, so a caller that reuses
// its buffer cannot mutate a secret already handed out.
func NewValue(plaintext []byte) Value {
	held := make([]byte, len(plaintext))
	copy(held, plaintext)
	return Value{plaintext: held}
}

// Reveal returns the plaintext. Every call site is a decision to expose a
// credential, which is why the name is what it is.
//
// It returns a COPY, for the same reason NewValue takes one. A Value is
// copied by assignment and by being passed around, and copying it copies
// only the slice header — so handing out the backing array would let any
// caller that mutates or zeroes what it revealed corrupt the secret held by
// every other copy, including ones it has never seen. The aliasing is
// invisible until something writes, and then the failure is a credential
// that is quietly wrong rather than one that is obviously missing.
func (v Value) Reveal() []byte { return bytes.Clone(v.plaintext) }

// Len reports the plaintext's length without exposing it, so a caller can
// check for emptiness without reaching for Reveal.
func (v Value) Len() int { return len(v.plaintext) }

// Redact returns text with every occurrence of this secret's plaintext
// replaced by replacement, without exposing the plaintext to the caller
// (Phase 3 item 5 design, D6 and D8). It is RedactAll for one secret; the
// boundary's pass over an attempt's secrets uses RedactAll, because the
// guarantee below holds per call and does not compose across calls.
func (v Value) Redact(text, replacement string) string {
	return RedactAll(text, []Redaction{{Value: v, Replacement: replacement}})
}

// Redaction pairs a secret with what should stand in for it.
type Redaction struct {
	Replacement string
	Value       Value
}

// RedactAll returns text with every occurrence of every secret's plaintext
// replaced, and GUARANTEES the result contains none of them. This is the
// execution boundary's mandatory redaction pass: a family that echoes a
// token in its result, or a client error that quotes the request it sent,
// would otherwise put a revealed credential in the record. The boundary
// holds the Values through settlement and runs this over the projected
// result and the error text with each secret's substituted reference as
// its replacement, so the record names the revision that was used and
// never the bytes.
//
// The guarantee is delivered rather than approximated (PR #384 review,
// three rounds), against every way a substitution can put a secret back:
//
//   - a replacement that contains a plaintext -- a credential that is a
//     substring of "secret:<id>@<version>", or of "[redacted]", or of
//     ANOTHER secret's reference, which is why the pass is over all of them
//     at once and not one Redact after another -- is swapped for a marker
//     that contains none of the plaintexts: "[redacted]", or failing that
//     the EMPTY string, the one replacement no non-empty plaintext can be
//     inside (a batch holding "e" and "~" excludes every fixed marker, PR
//     #384 review);
//   - a replacement's boundary with the surrounding text can recreate a
//     multi-byte plaintext ("xa" for "ab" in "abb" leaves "xab"), so the
//     substitution repeats while any plaintext survives, converging by one
//     occurrence per boundary per pass;
//   - past a bounded number of passes the WHOLE text becomes the empty
//     string: less information, no credential.
//
// An EMPTY secret is skipped: strings.ReplaceAll with an empty pattern
// inserts the replacement between every character, which would turn the
// text into noise while redacting no secret. The vault does not refuse an
// empty plaintext (CreateSecret and ReplaceSecret take a Value and check
// nothing of its length), so this is a real input, not a defensive one.
//
// The match is on the exact bytes. A token that appears transformed --
// base64-encoded, URL-escaped, split across lines -- is not found, and a
// caller that formats a secret into any encoding is making the decision
// Reveal's name exists to make visible.
func RedactAll(text string, redactions []Redaction) string {
	plaintexts := make([]string, 0, len(redactions))
	replacements := make([]string, 0, len(redactions))
	for i := range redactions {
		if redactions[i].Value.Len() == 0 {
			continue
		}
		plaintexts = append(plaintexts, string(redactions[i].Value.plaintext))
		replacements = append(replacements, redactions[i].Replacement)
	}
	if len(plaintexts) == 0 {
		return text
	}
	// Longest plaintext first, ties by bytes: with overlapping secrets ("ab"
	// and "abc") the order decides whether "abc" meets its own replacement
	// or is cut by "ab"'s, and the caller's order is a map's (PR #384
	// review). The result is a function of the batch, not of its order.
	sort.Sort(byLengthThenBytes{plaintexts, replacements})
	for i := range replacements {
		replacements[i] = safeReplacement(plaintexts, replacements[i])
	}
	out := text
	for pass := 0; pass < redactPasses; pass++ {
		if !containsAny(out, plaintexts) {
			return out
		}
		for i := range plaintexts {
			out = strings.ReplaceAll(out, plaintexts[i], replacements[i])
		}
	}
	if containsAny(out, plaintexts) {
		return ""
	}
	return out
}

// redactPasses bounds the repeated substitution. Each pass removes every
// current occurrence and can only recreate one across a boundary, so the
// count falls quickly; the bound exists so the guarantee never depends on
// that argument being right.
const redactPasses = 8

// byLengthThenBytes sorts plaintexts longest first, ties by bytes, carrying
// their replacements along.
type byLengthThenBytes struct {
	plaintexts, replacements []string
}

func (b byLengthThenBytes) Len() int { return len(b.plaintexts) }
func (b byLengthThenBytes) Less(i, j int) bool {
	if len(b.plaintexts[i]) != len(b.plaintexts[j]) {
		return len(b.plaintexts[i]) > len(b.plaintexts[j])
	}
	return b.plaintexts[i] < b.plaintexts[j]
}
func (b byLengthThenBytes) Swap(i, j int) {
	b.plaintexts[i], b.plaintexts[j] = b.plaintexts[j], b.plaintexts[i]
	b.replacements[i], b.replacements[j] = b.replacements[j], b.replacements[i]
}

// containsAny reports whether any plaintext is in text.
func containsAny(text string, plaintexts []string) bool {
	for _, p := range plaintexts {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

// safeReplacement returns replacement if it contains no plaintext, else
// "[redacted]" if that contains none, else the empty string -- the one
// candidate that is safe by construction against every non-empty
// plaintext, and the only one that is: any fixed non-empty marker is
// excluded by a batch that holds each of its bytes as a secret.
func safeReplacement(plaintexts []string, replacement string) string {
	for _, candidate := range []string{replacement, redacted} {
		if !containsAny(candidate, plaintexts) {
			return candidate
		}
	}
	return ""
}

// String and GoString cover fmt's two interface-driven paths. Format below
// covers the rest; all three are present because a reader checking whether
// this type is safe should not have to know which one fmt reaches first.
func (v Value) String() string { return redacted }

// GoString covers %#v, which ignores Stringer.
func (v Value) GoString() string { return redacted }

// Format covers every fmt verb, including the ones String does not reach.
//
// String alone is not enough: %#v uses GoStringer, and a verb like %x or %q
// applied to a struct field would otherwise fall through to the underlying
// bytes. Implementing Formatter takes precedence over both interfaces and
// leaves no verb unaccounted for.
func (v Value) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte(redacted))
	_ = verb
}

// MarshalJSON keeps a secret out of anything serialised -- an artifact
// payload, an error body, a structured log line.
//
// There is deliberately no UnmarshalJSON. A Value that could be decoded
// from JSON would be a credential arriving through a path with no
// decryption and no ownership check, which is precisely what the vault is
// for.
func (v Value) MarshalJSON() ([]byte, error) {
	// Built rather than marshalled: the value is a fixed literal, so there is
	// no encoder to fail and no error to wrap.
	return []byte(`"` + redacted + `"`), nil
}
