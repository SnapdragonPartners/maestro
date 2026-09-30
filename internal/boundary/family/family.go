// Package family is the vocabulary an action family is declared in (Phase 3
// item 5 design, D2 and D3).
//
// A LEAF beneath the boundary: every family under internal/boundary/families
// imports it, the boundary imports it to assemble and validate the closed
// set, and it imports nothing of either. That is what lets the mandatoriness
// guard (D2) count importers of the families themselves as exactly the
// boundary plus each family's own tests -- a family that needed the boundary
// to declare itself would be an importer the guard had to admit.
//
// A family is a VALUE, not a plugin: a struct literal in its own package,
// declaring everything the gates need to admit, substitute, record, execute
// and reconcile it. Registry construction validates every declaration; a
// family that omits one cannot be loaded, let alone recorded.
package family

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/secret"
)

// Classification is a schema field's disposition on the record (D3, D6):
// what of the caller's input the plane may hold.
type Classification string

// The five classifications. Every field carries exactly one; a schema with an
// unclassified field is refused at registry construction.
const (
	// Persist: the value is written to the record as supplied.
	Persist Classification = "persist"
	// DigestOnly: the value enters the arguments digest and is not written.
	DigestOnly Classification = "digest_only"
	// SecretSlot: the caller supplies NOTHING; the boundary fills the slot
	// from the vault and records its substituted reference (D6).
	SecretSlot Classification = "secret_slot"
	// Large: written by reference when over the projection limit.
	Large Classification = "large"
	// KeyedCommitment: a sensitive low-entropy non-secret value, recorded
	// as a keyed commitment. DECLARED AND NOT IMPLEMENTED in item 5 (D6):
	// no family here has such a field, and the classification exists so a
	// family that needs one cannot omit it silently. A family declaring it
	// is refused at registry construction until an implementation exists.
	KeyedCommitment Classification = "keyed_commitment"
)

// Classifications is the closed set, in declaration order.
//
//nolint:gochecknoglobals // Immutable enumeration.
var Classifications = []Classification{Persist, DigestOnly, SecretSlot, Large, KeyedCommitment}

// EffectSite is where the effect happens (ADR 0030 section 6; D3), which
// decides what "policed per action" means for the family and, at gate 3,
// whether a resource step is needed (D8).
type EffectSite string

// The three sites.
const (
	// OrchestratorSide: the Orchestrator performs the effect itself, with a
	// credential the execution resource never holds. Needs no resource step.
	OrchestratorSide EffectSite = "orchestrator_side"
	// InResource: the effect happens inside the execution resource, reached
	// through a resolver seam item 7 supplies.
	InResource EffectSite = "in_resource"
	// External: the effect is performed by something outside both.
	External EffectSite = "external"
)

// EffectSites is the closed set.
//
//nolint:gochecknoglobals // Immutable enumeration.
var EffectSites = []EffectSite{OrchestratorSide, InResource, External}

// FieldType is the JSON type a schema field accepts.
type FieldType string

// The types a field may declare; a schema is a flat object of these.
const (
	String  FieldType = "string"
	Integer FieldType = "integer"
	Number  FieldType = "number"
	Boolean FieldType = "boolean"
)

// Field is one argument or result field: its type, whether the caller must
// supply it, and its classification. A secret slot additionally names the
// secret and the scope it resolves at.
//
//nolint:govet // fieldalignment: ordered as a declaration reads
type Field struct {
	// Name is the field's key in the arguments object.
	Name string
	// Description is rendered into the definition the model reads.
	Description string
	// Type is the JSON type accepted; a secret slot's is String and is never
	// checked against input, because a secret slot admits no input.
	Type FieldType
	// Required means a caller that omits the field is refused. Meaningless
	// for a secret slot, which the boundary always fills.
	Required bool
	// Classification is the field's disposition on the record.
	Classification Classification
	// Secret is present iff Classification is SecretSlot.
	Secret *Slot
}

// SecretScope is where a secret slot's name resolves (D6): the vault's
// ownership ladder is walked from that scope.
type SecretScope string

// The scopes a slot may declare, matching the vault's.
const (
	ScopeRepository   SecretScope = "repository"
	ScopeProduct      SecretScope = "product"
	ScopeOrganization SecretScope = "organization"
)

// Slot declares which secret fills a secret-slot field and at what scope.
type Slot struct {
	// Name is the secret's name in the vault, e.g. "forge.token".
	Name string
	// Scope is where the ladder starts.
	Scope SecretScope
}

// Schema is a flat object schema: the family's arguments or its result.
//
// Flat by decision, not by omission. Every classification is per FIELD, and
// a nested object would need the classification pushed down to its leaves
// before the projection could say what of it the plane holds. No item 5
// family needs nesting; the first that does adds it beside the rule.
type Schema struct {
	Fields []Field
}

// Field returns the named field, if declared.
func (s Schema) Field(name string) (Field, bool) {
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			return s.Fields[i], true
		}
	}
	return Field{}, false
}

// SecretSlots returns the fields classified as secret slots, in order.
func (s Schema) SecretSlots() []Field {
	var slots []Field
	for i := range s.Fields {
		if s.Fields[i].Classification == SecretSlot {
			slots = append(slots, s.Fields[i])
		}
	}
	return slots
}

// CommitState is what an Effect reports about its declared commit point
// when it fails (D3, D8): one of three things, and never silence.
type CommitState string

// The three reports.
const (
	// CommitNotReached: the effect stopped before its commit point. The
	// attempt settles failed with drainage stopped_before_commit.
	CommitNotReached CommitState = "not_reached"
	// CommitPassed: the commit point was passed and the effect landed. On
	// failure, the attempt settles failed with drainage committed.
	CommitPassed CommitState = "passed"
	// CommitUnknown: the effect cannot tell whether it passed its commit
	// point. The boundary calls Reconcile before settling.
	CommitUnknown CommitState = "unknown"
)

// Failure is how an Effect reports an error WITH its commit state.
//
// A family's effect returns a *Failure so the boundary can settle the
// attempt with the disposition the family attests. An effect that returns
// any other error has not said whether its commit point was passed, and the
// boundary reads that as CommitUnknown -- the conservative reading, since
// an effect that did not say is one whose silence cannot be trusted to
// mean "stopped".
type Failure struct {
	Err    error
	Commit CommitState
}

func (f *Failure) Error() string {
	return fmt.Sprintf("effect failed (commit point %s): %v", f.Commit, f.Err)
}

// Unwrap keeps the cause reachable.
func (f *Failure) Unwrap() error { return f.Err }

// Fail builds a Failure. Convenience for families, so the commit state is
// named at the site that knows it.
func Fail(commit CommitState, err error) *Failure {
	return &Failure{Commit: commit, Err: err}
}

// Execution is what a family may know about the execution an attempt runs
// under: identity and lineage, the accountable user, and the repository's
// forge bindings. Copied into the leaf rather than imported from the seam,
// so a family stays importable by nothing but the boundary and its tests.
//
//nolint:govet // fieldalignment: grouped by lineage
type Execution struct {
	ExecutionID    uuid.UUID
	OrganizationID uuid.UUID
	StoryID        uuid.UUID
	EpicID         uuid.UUID
	RepositoryID   uuid.UUID
	// ActingUserID is the operator the execution acts for (D6).
	ActingUserID uuid.UUID
	// Forge is the repository's forge bindings, in provider order; empty
	// when the repository is unbound.
	Forge []ForgeBinding
}

// ForgeBinding is one of a repository's forge bindings (D12, D13).
type ForgeBinding struct {
	Provider string
	BaseURL  string
	Owner    string
	Repo     string
}

// Target is what a family resolves from an execution: the key its attempts
// correlate on (with the family, D5's inheritance lookup) and the shared
// resource its effect mutates, named family-independently so two families
// touching one resource serialize as one (D12; ADR 0027).
type Target struct {
	// Key is the family's declared target, e.g. "<repository>/<head>/<base>".
	Key string
	// MutationKey names the shared resource, e.g. "forge:<repository>/<head>/<base>";
	// empty for a family that mutates nothing shared, which registers with
	// no key and is never serialized.
	MutationKey string
}

// Attempt is what an Effect and a Reconcile receive: the attempt id -- the
// only source of the id a family writes as its evidence, supplied by the
// boundary and never by a side channel (D3) -- the execution, the resolved
// target, and the arguments the caller supplied, already validated against
// the schema. Secret slots are NOT in Arguments: they arrive separately as
// revealed values, for the effect's lifetime only.
//
//nolint:govet // fieldalignment: ordered as the family reads it
type Attempt struct {
	ID        uuid.UUID
	Execution Execution
	Target    Target
	Arguments map[string]any
}

// Secrets is the revealed values for an attempt's secret slots, keyed by
// slot field name. Held by the boundary through settlement; a family sees
// it only for the duration of one Effect or Reconcile call.
type Secrets map[string]secret.Value

// Result is what a successful Effect returns: the family's result, which
// the boundary projects through ResultSchema and redacts before anything
// is recorded (D8). Nothing here is persisted as returned.
type Result struct {
	Values map[string]any
}

// Evidence is what Reconcile reports: whether THIS attempt's effect is
// found to have committed, and the result it recovered if so.
type Evidence struct {
	Values    map[string]any
	Committed bool
}

// Family is one action family, declared as a value (D3).
//
//nolint:govet // fieldalignment: ordered as the design's table reads
type Family struct {
	// Kind and Verb form the Orchestrator-owned identity "<kind>/<verb>",
	// never the caller's tool name. The identity IS the record's tool_name.
	Kind string
	Verb string

	// Description is what the model reads in the definition.
	Description string

	// Schema is the argument schema; ResultSchema the result's.
	Schema       Schema
	ResultSchema Schema

	// EffectSite is where the effect happens.
	EffectSite EffectSite

	// Checkability is one sentence: what prevents the execution resource
	// from performing this directly. A family with no answer is mediated in
	// documentation only (ADR 0030 section 7) and is refused.
	Checkability string

	// CommitPoint names the instant after which the effect is no longer the
	// Orchestrator's to withhold (ADR 0030 section 5).
	CommitPoint string

	// ConditionalCommit reports whether the effect site accepts a
	// generation predicate (item 9's second drain disposition).
	ConditionalCommit bool

	// Resolve derives the target from the execution, at admission and again
	// before the effect. A pure function of its input: the boundary records
	// what it returned at admission, and the effect must see the same.
	// Refusing -- an unbound repository, say -- is an admission denial.
	Resolve func(execution Execution) (Target, error)

	// Effect performs the action. On failure it returns a *Failure naming
	// the commit state; any other error reads as CommitUnknown.
	Effect func(ctx context.Context, attempt Attempt, secrets Secrets) (Result, error)

	// Reconcile is the attempt-specific probe: did THIS attempt's effect
	// commit? Required for every family regardless of effect site, because
	// every interrupted attempt is reconciled (D5).
	Reconcile func(ctx context.Context, attempt Attempt, secrets Secrets) (Evidence, error)
}

// Identity is the family's "<kind>/<verb>".
func (f *Family) Identity() string { return f.Kind + "/" + f.Verb }

// identitySegment is what Kind and Verb may each be: lower-case, starting
// with a letter, underscores between. One shape for both, so an identity
// splits on its single slash without ambiguity.
var identitySegment = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidIdentitySegment reports whether s is a well-formed Kind or Verb.
func ValidIdentitySegment(s string) bool { return identitySegment.MatchString(s) }

// ErrInvalidIdentity is what a malformed family identity is refused with.
var ErrInvalidIdentity = errors.New("invalid family identity")

// ParseIdentity splits "<kind>/<verb>" and refuses any other shape. It is
// the one parser, used by the registry's lookup and by the requirement
// vocabulary's qualifier, so the two cannot disagree about what a family
// identity looks like.
func ParseIdentity(identity string) (kind, verb string, err error) {
	kind, verb, found := strings.Cut(identity, "/")
	switch {
	case !found:
		return "", "", fmt.Errorf("%w: %q has no slash; a family identity is <kind>/<verb>", ErrInvalidIdentity, identity)
	case !ValidIdentitySegment(kind):
		return "", "", fmt.Errorf("%w: kind %q is not lower-case [a-z][a-z0-9_]*", ErrInvalidIdentity, kind)
	case !ValidIdentitySegment(verb):
		return "", "", fmt.Errorf("%w: verb %q is not lower-case [a-z][a-z0-9_]*", ErrInvalidIdentity, verb)
	}
	return kind, verb, nil
}
