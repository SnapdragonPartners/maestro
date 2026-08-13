package contract

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// The framing. ADR 0032 §1 makes this explicitly NON-normative: newline-
// delimited JSON is what Phase 3 implements, and a substitution here does not
// renegotiate the contract.

// ErrProtocol marks a malformed or unreadable message. §8 draws the line this
// error sits on: a protocol violation is FATAL to the execution, while a policy
// denial is data the agent reads and acts on. Collapsing them would either kill
// an execution for an ordinary refusal or let a runtime keep going after it
// stopped speaking the protocol.
type ErrProtocol struct{ Detail string }

func (e *ErrProtocol) Error() string { return "protocol violation: " + e.Detail }

// Writer emits envelopes. Safe for concurrent use: an agent's heartbeat
// goroutine and its work goroutine both write, and interleaved partial lines
// would be indistinguishable from a malformed message.
type Writer struct {
	mu  sync.Mutex
	w   io.Writer
	seq uint64
}

// NewWriter wraps an io.Writer.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Send marshals body into an envelope and writes one line.
func (w *Writer) Send(invocation, msgType string, body any) error {
	var raw json.RawMessage
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal %s body: %w", msgType, err)
		}
		raw = b
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	line, err := json.Marshal(Envelope{V: Version, Type: msgType, Inv: invocation, Seq: w.seq, Body: raw})
	if err != nil {
		return fmt.Errorf("marshal %s envelope: %w", msgType, err)
	}
	if _, err := w.w.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write %s: %w", msgType, err)
	}
	return nil
}

// Reader parses envelopes.
type Reader struct{ sc *bufio.Scanner }

// NewReader wraps an io.Reader. The buffer is generous because an invocation
// carrying a seeding set is not a small message, and a scanner that silently
// truncates would surface as a protocol violation with a misleading cause.
func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	return &Reader{sc: sc}
}

// Next returns the next envelope, or io.EOF.
func (r *Reader) Next() (Envelope, error) {
	if !r.sc.Scan() {
		if err := r.sc.Err(); err != nil {
			return Envelope{}, fmt.Errorf("read: %w", err)
		}
		return Envelope{}, io.EOF
	}
	line := r.sc.Bytes()
	if len(line) == 0 {
		return Envelope{}, &ErrProtocol{Detail: "empty line"}
	}
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return Envelope{}, &ErrProtocol{Detail: "envelope is not JSON: " + err.Error()}
	}
	if env.Type == "" {
		return Envelope{}, &ErrProtocol{Detail: "envelope has no type"}
	}
	return env, nil
}

// Decode unmarshals an envelope body.
func Decode[T any](env Envelope) (T, error) {
	var out T
	if len(env.Body) == 0 {
		return out, &ErrProtocol{Detail: env.Type + " has no body"}
	}
	if err := json.Unmarshal(env.Body, &out); err != nil {
		return out, &ErrProtocol{Detail: env.Type + " body: " + err.Error()}
	}
	return out, nil
}
