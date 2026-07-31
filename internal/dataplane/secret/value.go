package secret

import "fmt"

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
func (v Value) Reveal() []byte { return v.plaintext }

// Len reports the plaintext's length without exposing it, so a caller can
// check for emptiness without reaching for Reveal.
func (v Value) Len() int { return len(v.plaintext) }

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
