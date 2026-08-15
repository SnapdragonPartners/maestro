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
const Version = "0032.2"

// Message types, host -> agent.
const (
	TypeHello        = "hello"         // version offer, opens the handshake
	TypeInvoke       = "invoke"        // config + bindings for one incarnation (§2)
	TypeActionResult = "action_result" // the boundary's answer to an action_request
	TypeCancel       = "cancel"        // cooperative cancellation with a deadline (§6)
	TypeAttachAck    = "attach_ack"    // state of this execution's outstanding actions
	TypeAck          = "ack"           // durable-receipt watermark for agent events (§4)
)

// Ack advances the receiver's durable watermark for an epoch. Everything at or
// below Through has been committed by the Orchestrator; the sender may release
// it. Anything above it the sender MUST be prepared to replay.
//
// Identity alone makes a duplicate harmless; it does not make delivery
// at-least-once. Without an acknowledgement there is nothing telling the sender
// what to retain, and a crash between emission and commit loses the event
// silently.
type Ack struct {
	Epoch   uint64 `json:"epoch"`
	Stream  string `json:"stream"`
	Through uint64 `json:"through"`
}

// Message types, agent -> host.
const (
	TypeHelloAck      = "hello_ack"      // selected version + declared runtime capabilities
	TypeStarted       = "started"        // runtime identity; first message after the handshake
	TypeHeartbeat     = "heartbeat"      // liveness (§6: NOT the activity channel)
	TypeActivity      = "activity"       // human-facing progress, never load-bearing
	TypeActionRequest = "action_request" // a mediated action
	TypeUsage         = "usage"          // token axes for one model call (§4, §9)
	TypeProvenance    = "provenance"     // per-call bindings + closure status (§9)
	TypeWarning       = "warning"        // recorded, not fatal
	TypeTerminal      = "terminal"       // at most one per invocation, and it is a claim
	TypeAttach        = "attach"         // ask what is outstanding after a restart (§6)
)

// Envelope is every message on the wire. ADR 0032 §10: newline-delimited JSON.
//
// Identity is (Inv, Epoch, Seq), and all three are needed. A sequence number
// alone restarts at 1 with every process, so at-least-once delivery could not
// be made idempotent: two different messages from two incarnations would share
// an identity. The EPOCH is assigned by the Orchestrator and is what orders
// them.
type Envelope struct {
	V     string `json:"v"`
	Type  string `json:"type"`
	Inv   string `json:"inv,omitempty"`
	Epoch uint64 `json:"epoch"`
	Seq   uint64 `json:"seq"`
	// Stream separates the RELIABLE sequence space from the best-effort one.
	//
	// One shared space cannot work: the watermark is contiguous, so a dropped
	// best-effort `activity` would permanently block acknowledgement of every
	// retained `usage` behind it -- a diagnostic message holding accounting
	// hostage. Each stream is its own monotonic space with its own watermark.
	Stream string          `json:"stream"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// The two sequence spaces (§4).
const (
	// StreamReliable carries the events with a retention and replay obligation.
	StreamReliable = "reliable"
	// StreamBestEffort carries diagnostics whose loss is tolerable and declared.
	StreamBestEffort = "best_effort"
)

// StreamFor reports which space a message type belongs to.
func StreamFor(msgType string) string {
	if msgType == TypeUsage || msgType == TypeProvenance {
		return StreamReliable
	}
	return StreamBestEffort
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
	// opaque token. §7 makes this the difference between an execution that can
	// be released and resumed cheaply and one whose restart replays from the
	// last durable workflow artifact.
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

// ModelBinding is the REQUESTED model. The UNDERLYING model reference is
// deliberately absent: it is a fact about a model rather than about this
// invocation, and the plane resolves it from model metadata (§3). What the
// provider actually served is reported per call, on Usage.
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

// ActionAsk routes a question to another principal. It is a MEDIATED ACTION and
// not an event: it changes execution state and invokes Orchestrator message
// routing, so under ADR 0022 it must pass through an action record. A raw
// `question` event would be a side door around ADR 0030's boundary -- which is
// the exact hole that ADR forbids.
var ActionAsk = ActionID{Kind: "message", Verb: "ask"}

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

// ExpectedSource is ADR 0031 §3 obligation 1. A source NAME alone is not a
// capability claim: "this runtime will report its turn material" says nothing
// about how, so nothing can be checked against it. The MECHANISM is what makes
// the declaration falsifiable.
type ExpectedSource struct {
	Source string `json:"source"` // P | H | seeding | turn
	// Mechanism is how this runtime will account for that source -- for example
	// "invocation" (fixed at dispatch), "mediated-actions" (observable in the
	// Audit record), or "adapter-events" (reported by the adapter).
	Mechanism string `json:"mechanism"`
}

// ExecutionConfig is the IMMUTABLE half of an invocation: everything resolved
// once at dispatch and reused verbatim for the life of the execution, including
// across a restart. It is persisted, so a restarted Orchestrator can reissue it
// without re-resolving -- which is what stops a configuration edit between the
// crash and the restart from moving a factory lever mid-Story.
//
// Nothing here may change while the execution lives.
type ExecutionConfig struct {
	ID           string           `json:"id"`
	Principal    PrincipalRef     `json:"principal"`
	Role         string           `json:"role"`
	Work         WorkRef          `json:"work"`
	Seeding      []ArtifactRef    `json:"seeding,omitempty"`
	Model        ModelBinding     `json:"model"`
	Pack         PackRef          `json:"pack"`
	Capabilities []ActionID       `json:"capabilities"`
	Budgets      Budgets          `json:"budgets"`
	Sources      []ExpectedSource `json:"expected_sources"`

	// OperatorResponder is ADR 0030 §4: headless is a DECLARED configuration
	// known at dispatch, never an observation that nobody answered. It is
	// immutable because it describes the run, not the moment.
	OperatorResponder bool `json:"operator_responder"`
}

// Bindings is the MUTABLE half: what is true of THIS incarnation and may differ
// after a restart or after gate 3 replaces a resource.
//
// A first version of this contract put resource generations and the resume
// token inside an "immutable" invocation, which was self-contradicting on both
// counts: ADR 0030's gate 3 may acquire or replace a resource mid-execution, so
// the generation it carries is a snapshot; and a resume token exists only on
// the second and later incarnations.
type Bindings struct {
	// Epoch identifies this incarnation and orders events across restarts. The
	// ORCHESTRATOR assigns it.
	Epoch uint64 `json:"epoch"`
	// Resources carries the current grants and their instance generations
	// (ADR 0029 §5). Refreshed whenever gate 3 acquires or replaces one.
	Resources []ResourceRef `json:"resources"`
	// ResumeToken is opaque to the Orchestrator (§6). Present only when a
	// resumable runtime is being restarted into an existing execution.
	ResumeToken string `json:"resume_token,omitempty"`
	// Inbound carries artifact references that arrived FOR this execution since
	// the previous incarnation -- an answer to a question, most obviously.
	//
	// It is on the bindings and not the configuration precisely because the
	// configuration is immutable: an execution cannot acquire a new seeding
	// artifact by restarting, so the arrivals need a mutable home of their own.
	Inbound []ArtifactRef `json:"inbound,omitempty"`
}

// Invocation is what crosses the wire: the immutable configuration plus the
// bindings for this incarnation.
type Invocation struct {
	Config   ExecutionConfig `json:"config"`
	Bindings Bindings        `json:"bindings"`
}

// ID is the execution's identity, which lives on the immutable half.
func (inv *Invocation) ID() string { return inv.Config.ID }

// HasCapability reports whether the configuration granted this action.
//
// The invocation's copy is NOT the authority -- ADR 0030's admission gate
// re-checks immediately before the effect (§8). It exists so a runtime can
// present a coherent tool surface and refuse locally rather than discovering
// every denial through a round trip.
func (inv *Invocation) HasCapability(id ActionID) bool {
	return slices.Contains(inv.Config.Capabilities, id)
}

// Resource returns the current binding for a resource kind.
func (inv *Invocation) Resource(kind string) (ResourceRef, bool) {
	for _, r := range inv.Bindings.Resources {
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

// ActionRequest is a mediated action.
//
// Correlation is the caller-supplied idempotency key -- what
// [ADR 0030](0030-tool-execution-policy-hook.md) §3 calls "attempt identity" on
// the request. It is renamed here only because the boundary's own record needs
// a primary key of its own, which the reply carries as AttemptID; the semantics
// ADR 0030 attaches to it are unchanged.
//
// The key alone does NOT identify the logical action. The boundary binds it to
// the action identity and a digest of the substituted arguments, and refuses a
// reuse that does not match both -- otherwise one correlation could replay the
// result of a different action, or of the same action with different arguments.
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
	// OutcomeBlocked is TERMINAL for the action: a gate required an operator
	// and the configuration declares no responder, so nothing will ever answer
	// it. It is distinct from `denied` because the requirement is preserved and
	// the Story is irreconcilable within this run rather than refused.
	//
	// Leaving such an attempt in `operator_waiting` would be a lie: a headless
	// wait is not a healthy wait, because it has no responder.
	OutcomeBlocked ActionOutcome = "blocked"
	// OutcomeOutstanding answers a DUPLICATE request for an action already in
	// flight. It re-enters no gate and commits nothing; the original
	// submission's result is still coming.
	OutcomeOutstanding ActionOutcome = "outstanding"
	// OutcomeStale is ADR 0030 §5's word for an action that must be RE-REQUESTED
	// rather than continued. Reconciliation settles a declared wait this way
	// after an Orchestrator restart: the requirement and any operator decision
	// are preserved, but the half-run action is not resumed.
	//
	// It is deliberately distinct from `unknown`: an attempt awaiting an
	// operator is not an attempt whose outcome nobody knows.
	OutcomeStale ActionOutcome = "stale"
	// OutcomeUnknown is settled by reconciliation for an attempt holding an
	// intent and no outcome. It is an OUTCOME, not a state (§6) -- and it is
	// the value Phase 2's tool_calls_finished_check cannot express.
	OutcomeUnknown ActionOutcome = "unknown"
)

// Terminal reports whether this outcome settles the attempt.
func (o ActionOutcome) Terminal() bool {
	switch o {
	case OutcomeSucceeded, OutcomeFailed, OutcomeDenied, OutcomeBlocked, OutcomeStale, OutcomeUnknown:
		return true
	default:
		return false
	}
}

// ActionResult is the boundary's answer.
type ActionResult struct {
	Correlation string          `json:"correlation"`
	AttemptID   string          `json:"attempt_id"`
	Outcome     ActionOutcome   `json:"outcome"`
	Reason      string          `json:"reason,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	// Requirement is preserved on a blocked outcome so the terminal result can
	// reference what was actually being asked (§5).
	Requirement *RequirementRef `json:"requirement,omitempty"`
}

// Usage carries one model call's token axes and what was actually served.
//
// CallRef is required: without a stable call identity, usage cannot be joined
// to the provenance for the same call, and neither can be attributed to a
// model. §9 makes them two reports about one thing.
type Usage struct {
	CallRef         string `json:"call_ref"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	// CostUSD is a pointer because nil means UNAVAILABLE and 0 means free.
	// Phase 1's lesson, encoded in llm_calls.cost_usd's own comment.
	CostUSD *float64 `json:"cost_usd,omitempty"`

	// Served is the EFFECTIVE served identity for this call -- what the
	// provider says it ran, which is not necessarily what was requested (§3).
	// An alias resolves at call time to something the requester did not choose,
	// and recording the alias would make two runs months apart compare equal
	// when they ran different models.
	Served ServedModelID `json:"served"`
	// ServedConfirmed distinguishes an identity the provider REPORTED from one
	// carried over from the request because the provider said nothing more
	// specific. Recording the second as though it were the first is the
	// unavailable-versus-zero error in another costume.
	ServedConfirmed bool `json:"served_confirmed"`
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

// Provenance is emitted PER MODEL CALL, and only per model call. There is no
// such thing as provenance for an execution that made none: with no model
// input there is nothing to account for, and emitting `closed` anyway would
// assert an accounting that never happened.
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

// Attach asks what is outstanding for this execution. The AGENT does not
// enumerate: a first version had it re-announce correlations it derived from a
// step ordinal, which is unsound for a nondeterministic runtime -- step 3 after
// a restart need not be the same logical action as step 3 before it.
//
// The Orchestrator is the authority on what is outstanding, because it holds
// the durable attempt records. The agent asks.
type Attach struct{}

// AttachState is deliberately COARSE. §6: re-attach never surfaces approval
// semantics to the agent -- the runtime learns a call is still outstanding, it
// does not learn that a human is being asked. Telling the model it is waiting
// on a person gives it something to reason about, which is the deny-and-retry
// shape ADR 0030's redraft deleted, re-entering through the transport.
type AttachState string

const (
	AttachOutstanding AttachState = "outstanding"
	AttachSettled     AttachState = "settled"
)

// OutstandingAction is one entry in the Orchestrator's answer.
type OutstandingAction struct {
	Correlation string      `json:"correlation"`
	AttemptID   string      `json:"attempt_id"`
	Action      ActionID    `json:"action"`
	State       AttachState `json:"state"`
}

// AttachAck reports every known action for the execution, and replays the
// result of any that settled while the previous incarnation was gone.
type AttachAck struct {
	Actions []OutstandingAction `json:"actions"`
	Settled []ActionResult      `json:"settled,omitempty"`
}
