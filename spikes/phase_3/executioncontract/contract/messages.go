// Package contract is the wire schema of ADR 0032, the agent execution
// contract. It is the normative half of the spike: message kinds, their fields,
// and the meaning of each. The framing (package codec) and the process model
// (package host) are explicitly NOT normative under ADR 0032 §1.
package contract

import (
	"encoding/json"
	"slices"
)

// Version is the contract version this build speaks. ADR 0032 §11: the version
// is negotiated at the handshake and a mismatch fails at dispatch, before any
// resource is acquired.
const Version = "0032.1"

// Message types, host -> agent.
const (
	TypeHello        = "hello"         // version offer, opens the handshake
	TypeInvoke       = "invoke"        // the resolved invocation (§2)
	TypeActionResult = "action_result" // the boundary's answer to an action_request
	TypeCancel       = "cancel"        // cooperative cancellation with a deadline (§6)
	TypeAttachAck    = "attach_ack"    // state of the correlations an agent re-announced
)

// Message types, agent -> host.
const (
	TypeHelloAck      = "hello_ack"      // selected version + declared runtime capabilities
	TypeStarted       = "started"        // runtime identity; first message after the handshake
	TypeHeartbeat     = "heartbeat"      // liveness (§6: NOT the activity channel)
	TypeActivity      = "activity"       // human-facing progress, never load-bearing
	TypeActionRequest = "action_request" // a mediated action
	TypeQuestion      = "question"       // a nonterminal state, not an outcome
	TypeUsage         = "usage"          // token axes; §4 decides observed vs reported
	TypeProvenance    = "provenance"     // per-call bindings + closure status (§9)
	TypeWarning       = "warning"        // recorded, not fatal
	TypeTerminal      = "terminal"       // exactly one per invocation, and it is a claim
	TypeAttach        = "attach"         // re-announce outstanding correlations (§6)
)

// Envelope is every message on the wire. ADR 0032 §10: newline-delimited JSON.
type Envelope struct {
	V    string          `json:"v"`
	Type string          `json:"type"`
	Inv  string          `json:"inv,omitempty"`
	Seq  uint64          `json:"seq"`
	Body json.RawMessage `json:"body,omitempty"`
}

// ---------- handshake ----------

// Hello is the Orchestrator's version offer.
type Hello struct {
	// Supported lists every contract version the Orchestrator will speak. The
	// adapter selects one; it never proposes a version of its own, because a
	// negotiation in which both sides may propose has no deterministic winner.
	Supported []string `json:"supported"`
}

// HelloAck is the adapter's selection plus what the runtime can do.
//
// Runtime capabilities are negotiated SEPARATELY from the contract version
// (§11): a runtime that cannot resume is not speaking a different contract.
type HelloAck struct {
	Selected string `json:"selected"`
	// Resumable declares that the runtime can resume its own session from an
	// opaque token. §7 makes this the difference between an execution that may
	// be released while blocked and one that must be held resident.
	Resumable bool `json:"resumable"`
	// Adapter and Executable are provenance (§9), not identity for routing.
	Adapter    string `json:"adapter"`
	Executable string `json:"executable"`
}

// ---------- the invocation (§2) ----------

// PrincipalRef is ADR 0021's accountable identity.
type PrincipalRef struct {
	InstanceID string `json:"instance_id"`
	Kind       string `json:"kind"` // agent | human | system
}

// WorkRef carries the work scope and the effective version. ADR 0019's
// version-bound dispatch: ADR 0030's admission gate compares every action
// against EffectiveVersion.
type WorkRef struct {
	StoryID          string `json:"story_id,omitempty"`
	EpicID           string `json:"epic_id,omitempty"`
	EffectiveVersion uint64 `json:"effective_version"`
}

// ArtifactRef is a reference, never inlined content (§2).
type ArtifactRef struct {
	ArtifactID string `json:"artifact_id"`
	Digest     string `json:"digest"`
}

// ModelRoute is where the request goes. ADR 0032 §3: explicit, never inferred
// from the model name. This is what replaces v1's ProviderPatterns as the
// source of truth.
type ModelRoute struct {
	Provider          string `json:"provider"`
	Endpoint          string `json:"endpoint"`
	ProviderModelName string `json:"provider_model_name"`
}

// ServedModelID is a provider's offering: the thing that has a retirement date.
// Deliberately does NOT include the endpoint -- two deployments of one offering
// compare equal in the MPH signature (§3, with the self-hosted limit stated
// there).
type ServedModelID struct {
	Provider          string `json:"provider"`
	ProviderModelName string `json:"provider_model_name"`
}

// ModelBinding is what the invocation carries. The UNDERLYING model reference
// is deliberately absent: it is a fact about a model rather than about this
// invocation, and the plane resolves it from model metadata (§3).
type ModelBinding struct {
	Route            ModelRoute    `json:"route"`
	Served           ServedModelID `json:"served"`
	RequestedByAlias bool          `json:"requested_by_alias"`
}

// PackRef is ADR 0031 §2's four facts. Name is a label: carried for humans and
// for validation, never dereferenced.
type PackRef struct {
	Name                 string `json:"name"`
	Scheme               string `json:"scheme"`
	Digest               string `json:"digest"`
	ContentRef           string `json:"content_ref"`
	InstallationID       string `json:"installation_id"`
	InstallationRevision uint64 `json:"installation_revision"`
}

// ActionID is the Orchestrator-owned action identity of ADR 0030 §3 -- a stable
// kind and verb, NOT the runtime's tool name. An adapted runtime brings its own
// vocabulary for the same action, so a capability set keyed on caller names
// would be runtime-specific by construction.
type ActionID struct {
	Kind string `json:"kind"`
	Verb string `json:"verb"`
}

func (a ActionID) String() string { return a.Kind + "." + a.Verb }

// ResourceRef is ADR 0029 §5's reference plus the instance generation, which is
// the fencing identity. This is the field that replaces v1's RunOptions.WorkDir
// -- there is deliberately no path here.
type ResourceRef struct {
	Kind               string `json:"kind"` // incubator | habitat
	ReferenceID        string `json:"reference_id"`
	InstanceGeneration uint64 `json:"instance_generation"`
}

// Budgets are enforced by the execution contract, not by ADR 0030's boundary:
// an LLM call is not a mediated action (ADR 0030 §10).
type Budgets struct {
	MaxTokens      int64 `json:"max_tokens,omitempty"`
	MaxWallClockMS int64 `json:"max_wall_clock_ms,omitempty"`
	MaxIterations  int   `json:"max_iterations,omitempty"`
}

// Invocation is resolved, immutable, and complete (§2). Everything requiring a
// decision is decided before it is sent; the runtime looks nothing up.
type Invocation struct {
	ID           string        `json:"id"`
	Principal    PrincipalRef  `json:"principal"`
	Role         string        `json:"role"`
	Work         WorkRef       `json:"work"`
	Seeding      []ArtifactRef `json:"seeding,omitempty"`
	Model        ModelBinding  `json:"model"`
	Pack         PackRef       `json:"pack"`
	Capabilities []ActionID    `json:"capabilities"`
	Resources    []ResourceRef `json:"resources"`
	Budgets      Budgets       `json:"budgets"`

	// OperatorResponder is ADR 0030 §4: headless is a DECLARED configuration
	// known at dispatch, never an observation that nobody answered.
	OperatorResponder bool `json:"operator_responder"`

	// ExpectedSources is ADR 0031 §3 obligation 1 -- which of the four
	// provenance sources this runtime will report, and by what mechanism. A
	// claim about capability, made before there is anything to account for.
	ExpectedSources []string `json:"expected_sources"`

	// ResumeToken is opaque to the Orchestrator (§6). Present only when a
	// resumable runtime is being restarted into an existing execution.
	ResumeToken string `json:"resume_token,omitempty"`
}

// HasCapability reports whether the invocation granted this action.
//
// The invocation's copy is NOT the authority -- ADR 0030's admission gate
// re-checks immediately before the effect (§8). It exists so a runtime can
// present a coherent tool surface and refuse locally rather than discovering
// every denial through a round trip.
func (inv *Invocation) HasCapability(id ActionID) bool {
	return slices.Contains(inv.Capabilities, id)
}

// Resource returns the reference for a resource kind.
func (inv *Invocation) Resource(kind string) (ResourceRef, bool) {
	for _, r := range inv.Resources {
		if r.Kind == kind {
			return r, true
		}
	}
	return ResourceRef{}, false
}

// ---------- events (§4) ----------

// Started carries the runtime's own identity.
type Started struct {
	Adapter         string `json:"adapter"`
	ExecutableVer   string `json:"executable_version"`
	ContractVersion string `json:"contract_version"`
}

// Heartbeat is the liveness channel. §6 keeps liveness OFF the activity
// channel: a long compile is silent and healthy, and v1's SignalInactivity
// conflates not-talking with not-working.
type Heartbeat struct {
	Phase string `json:"phase"`
}

// Activity is human-facing progress. Never load-bearing.
type Activity struct {
	Message string `json:"message"`
}

// ActionRequest is a mediated action. Correlation is chosen by the RUNTIME
// (§6); the boundary's reply carries the attempt identity that owns ADR 0030
// §3's at-most-once semantics.
type ActionRequest struct {
	Correlation string          `json:"correlation"`
	Action      ActionID        `json:"action"`
	Arguments   json.RawMessage `json:"arguments"`
}

// ActionOutcome is what the boundary determined.
type ActionOutcome string

const (
	OutcomeSucceeded ActionOutcome = "succeeded"
	OutcomeFailed    ActionOutcome = "failed"
	// OutcomeDenied is DATA, not a protocol violation (§8). The agent reads it
	// and continues; the execution does not fail.
	OutcomeDenied ActionOutcome = "denied"
	// OutcomeUnknown is settled by reconciliation for an attempt holding an
	// intent and no outcome. It is an OUTCOME, not a state (§6) -- and it is
	// the value Phase 2's tool_calls_finished_check cannot express.
	OutcomeUnknown ActionOutcome = "unknown"
)

// ActionResult is the boundary's answer.
type ActionResult struct {
	Correlation string          `json:"correlation"`
	AttemptID   string          `json:"attempt_id"`
	Outcome     ActionOutcome   `json:"outcome"`
	Reason      string          `json:"reason,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

// Question is a nonterminal state, not an outcome -- the distinction v1's
// QUESTION signal loses by putting it in the same enum as ERROR and TIMEOUT.
type Question struct {
	Text    string `json:"text"`
	Context string `json:"context,omitempty"`
}

// Usage carries token axes. §4: whether this is authoritative depends on who
// could observe it, and the record says which.
type Usage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	// CostUSD is a pointer because nil means UNAVAILABLE and 0 means free.
	// Phase 1's lesson, encoded in llm_calls.cost_usd's own comment.
	CostUSD *float64 `json:"cost_usd,omitempty"`
}

// SourceBinding is ADR 0031 §3 obligation 2: the exact contributing reference
// and digest. A status without bindings is not provenance.
type SourceBinding struct {
	Source string `json:"source"` // P | H | seeding | turn
	Ref    string `json:"ref"`
	Digest string `json:"digest,omitempty"`
}

// ClosureStatus is obligation 3, drawn FROM the bindings.
type ClosureStatus string

const (
	ClosureClosed   ClosureStatus = "closed"
	ClosureUnclosed ClosureStatus = "unclosed"
)

// Provenance is emitted per model call.
type Provenance struct {
	CallRef  string          `json:"call_ref"`
	Bindings []SourceBinding `json:"bindings"`
	Closure  ClosureStatus   `json:"closure"`
	// Unaccounted names what the adapter knows it could not bind. An unclosed
	// status that cannot say what escaped it is barely better than no status.
	Unaccounted []string `json:"unaccounted,omitempty"`
}

// Warning is recorded and not fatal.
type Warning struct {
	Message string `json:"message"`
}

// ---------- cancellation and re-attach (§6) ----------

// Cancel is cooperative first. Fencing is what actually stops a process, and it
// is the host's business, not a message.
type Cancel struct {
	Reason     string `json:"reason"`
	DeadlineMS int64  `json:"deadline_ms"`
}

// Attach re-announces outstanding correlations after a transport interruption.
type Attach struct {
	Correlations []string `json:"correlations"`
}

// AttachState is deliberately COARSE. §6: re-attach never surfaces approval
// semantics to the agent -- the runtime learns a call is still outstanding, it
// does not learn that a human is being asked. Telling the model it is waiting
// on a person gives it something to reason about, which is the deny-and-retry
// shape ADR 0030's redraft deleted, re-entering through the transport.
type AttachState string

const (
	AttachOutstanding AttachState = "outstanding"
	AttachSettled     AttachState = "settled"
	AttachUnknown     AttachState = "unknown"
)

// AttachAck reports each correlation's state, and replays the result of any
// that settled while the transport was down.
type AttachAck struct {
	States  map[string]AttachState `json:"states"`
	Settled []ActionResult         `json:"settled,omitempty"`
}
