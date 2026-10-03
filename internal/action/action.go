// Package action is the vocabulary a tool loop and an action executor share
// (Phase 3 item 5 design, D1 and D15).
//
// A LEAF, deliberately: it imports the standard library and uuid and nothing
// of this module. The toolloop imports it to CALL an executor and the
// execution boundary imports it to BE one, so neither has to import the
// other -- the boundary must not reach pkg/tools (whose closure carries
// pkg/config), and the toolloop must not reach the boundary's persistence
// closure. Go imports packages, not declarations, so the shared types have to
// live somewhere neither side owns.
//
// What is here is the shape the loop already consumes -- a call with its
// arguments, a result with content, an error flag and an optional process
// effect -- and one addition, the attempt id, which is what makes an action
// an at-most-once row rather than a provider string (D5).
package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Call is one action the model asked for, as handed to an executor.
//
//nolint:govet // fieldalignment: grouped by what identifies the call and what it carries
type Call struct {
	// AttemptID identifies this logical call for at-most-once execution: it
	// IS the tool-call row's id (D5). A UUIDv7, minted ONCE per logical call
	// by whoever constructs the Call -- the loop for the in-process path,
	// the wire adapter for item 8's, where transport retries are the reason
	// the id exists -- and never by the executor, which would otherwise mint
	// a fresh identity for every re-presentation and turn a retry into a
	// second attempt.
	AttemptID uuid.UUID

	// CallerRef is the provider's own tool-call string (values like
	// "call_1"), kept for correlation with the LLM turn and never used as a
	// key: it is not an identity the plane can rely on.
	CallerRef string

	// Name is what the model called, resolved by the executor against its
	// own Definitions. The loop no longer looks a name up itself: an unknown
	// name is the executor's error result, as it is the provider's today.
	Name string

	// Arguments is what the model supplied, decoded. The executor validates
	// it against the definition it published; a secret slot is never here
	// (D6: the caller omits it, and a supplied value is refused).
	Arguments map[string]any
}

// ErrInvalidCall is the sentinel a malformed Call is refused with.
var ErrInvalidCall = errors.New("invalid action call")

// Validate refuses a Call an executor could not act on.
//
// The attempt id is checked for its VERSION and not only for presence:
// item 2 made every plane identity a UUIDv7 so ordering is derivable from
// the id, and the seam refuses any other version at registration. Refusing
// it here means the refusal names the loop that minted it rather than the
// row that would not insert.
func (c Call) Validate() error {
	switch {
	case c.AttemptID == uuid.Nil:
		return fmt.Errorf("%w: no attempt id; the caller mints one UUIDv7 per logical call", ErrInvalidCall)
	case c.AttemptID.Version() != 7:
		return fmt.Errorf("%w: attempt id %s is a version %d uuid, not a UUIDv7", ErrInvalidCall, c.AttemptID, c.AttemptID.Version())
	case strings.TrimSpace(c.Name) == "":
		return fmt.Errorf("%w: no action name", ErrInvalidCall)
	}
	return nil
}

// Result is what an executor hands back for the model to read: the loop's
// existing consumption of tools.ExecResult, unchanged in shape.
//
//nolint:govet // fieldalignment: ordered as the loop reads it
type Result struct {
	// Content is what the model sees. Empty means the executor has nothing
	// to say beyond "it ran", and the loop supplies its acknowledgement.
	Content string

	// IsError marks the content as a failure the model should react to,
	// whether a Go error or a semantic one the executor classified.
	IsError bool

	// Effect, when present, asks the loop to exit so the state machine can
	// process an asynchronous effect; nil means continue looping.
	Effect *ProcessEffect
}

// ProcessEffect is a signal a tool raises to pause the loop, with optional
// data for the state that handles it. The signal vocabulary is the caller's
// (the v1 drivers' today); this leaf constrains only the shape.
type ProcessEffect struct {
	Data   any
	Signal string
}

// Definition is one action an executor offers the model: name, description
// and the JSON Schema of its arguments -- the shape the loop already
// converts into a provider tool definition -- with exactly one marked
// terminal (D15).
//
//nolint:govet // fieldalignment: ordered as a definition is read
type Definition struct {
	// Name is what the model calls, and what a Call carries back.
	Name string

	// Description is what the model reads to decide.
	Description string

	// Parameters is the JSON Schema object describing Arguments.
	Parameters json.RawMessage

	// Terminal marks the one definition whose call ends the loop's turn. It
	// crosses the executor like any other call (D15): the loop must not run
	// it itself, which would make the loop a second effect site.
	Terminal bool
}

// Executor performs actions and owns the catalog they are chosen from.
//
// Two implementations (D15): the toolloop's legacy executor over v1's tool
// provider, and the execution boundary's, which mediates every call through
// the gates. The loop hands EVERY call to Execute, the terminal one
// included, and does no lookup of its own -- a loop that executed anything
// itself would be a second effect site the mandatoriness guard (D2) would
// have to admit.
//
// Implementations must be safe for concurrent use: one executor serves
// every call of a turn, and a turn may carry several.
type Executor interface {
	// Definitions is the catalog offered to the model on every turn.
	Definitions() []Definition

	// Execute performs one call. It returns a Result rather than an error:
	// the loop reads every outcome the same way, and an executor that
	// could not act says so in the Result it returns.
	Execute(ctx context.Context, call Call) Result
}
