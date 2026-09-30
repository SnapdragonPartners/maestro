package secret

import (
	"bytes"
	"fmt"
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
// (Phase 3 item 5 design, D6 and D8).
//
// This is the execution boundary's mandatory redaction pass: a family that
// echoes a token in its result, or a client error that quotes the request
// it sent, would otherwise put a revealed credential in the record. The
// boundary holds the Value through settlement and runs this over the
// projected result and the error text with the secret's substituted
// reference as the replacement, so the record names the revision that was
// used and never the bytes.
//
// An EMPTY secret redacts nothing: strings.ReplaceAll with an empty pattern
// inserts the replacement between every character, which would turn the
// text into noise while redacting no secret. The vault refuses to store an
// empty plaintext, so the case is defensive, and it is stated because the
// silent alternative is worse than the loud one.
//
// The match is on the exact bytes. A token that appears transformed --
// base64-encoded, URL-escaped, split across lines -- is not found, and a
// caller that formats a secret into any encoding is making the decision
// Reveal's name exists to make visible.
//
// A replacement that itself contains the plaintext -- a credential that
// happens to be a substring of "secret:<id>@<version>" -- would put the
// secret back into the redacted output (PR #384 review), so it is replaced
// by the fixed "[redacted]" instead, which by construction contains no
// caller-chosen bytes.
func (v Value) Redact(text, replacement string) string {
	if len(v.plaintext) == 0 {
		return text
	}
	plaintext := string(v.plaintext)
	if strings.Contains(replacement, plaintext) {
		replacement = redacted
	}
	return strings.ReplaceAll(text, plaintext, replacement)
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
