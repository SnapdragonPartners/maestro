package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// The call family: LLM calls, tool calls, metric events and audit events.
//
// Two shapes live here. Metric and audit events are born final. Calls are
// created OPEN and completed exactly once — `finished_at` is nullable
// precisely because a call in flight has not ended, and treating the whole
// family as immutable would leave no way to record an outcome.

// ErrConcurrentTruncation reports that truncation could not obtain a
// consistent snapshot within its retry bound.
//
// Truncation runs at REPEATABLE READ, where Postgres aborts a transaction
// whose reads have been invalidated (SQLSTATE 40001). That is a normal
// outcome under concurrent truncation, not a defect, so it is a typed error
// a caller can retry later rather than a raw driver failure every caller
// would have to decode for itself.
var ErrConcurrentTruncation = errors.New("truncation could not obtain a consistent snapshot")

// ErrInvalidProvenance reports that a tool call claimed an LLM call it may
// not claim.
//
// Deliberately generic. The composite foreign key it comes from cannot
// distinguish a nonexistent parent from a principal, lineage, accountable-
// user or organization mismatch — `lineage_key` folds the user in — so
// naming one cause would be a guess presented as a diagnosis. The error
// carries what the caller claimed; it does not claim to know which part
// was wrong.
var ErrInvalidProvenance = errors.New("tool call may not claim that LLM call")

// LLMCall is one model invocation (ADR 0022: calls are metrics and traces).
type LLMCall struct {
	FinishedAt   *time.Time
	Succeeded    *bool
	ErrorMessage *string
	Cost         *USD
	UserID       *uuid.UUID

	Lineage   Lineage
	StartedAt time.Time

	Provider string
	Model    string

	Tokens TokenCounts

	LLMCallID           uuid.UUID
	OrganizationID      uuid.UUID
	PrincipalInstanceID uuid.UUID
}

// TokenCounts is the four counters a completed call reports.
type TokenCounts struct {
	Input     int64
	Output    int64
	Reasoning int64
	Cached    int64
}

// ToolCall is ADR 0022's atomic Audit action unit.
type ToolCall struct {
	FinishedAt   *time.Time
	Succeeded    *bool
	ErrorMessage *string
	Result       json.RawMessage
	UserID       *uuid.UUID
	LLMCallID    *uuid.UUID

	Lineage   Lineage
	StartedAt time.Time

	ToolName  string
	Arguments json.RawMessage

	ToolCallID          uuid.UUID
	OrganizationID      uuid.UUID
	PrincipalInstanceID uuid.UUID
}

// MetricEvent is a measurement. Born final.
type MetricEvent struct {
	UserID              *uuid.UUID
	PrincipalInstanceID *uuid.UUID

	Lineage    Lineage
	RecordedAt time.Time

	MetricName string
	Labels     json.RawMessage
	Value      float64

	MetricEventID  uuid.UUID
	OrganizationID uuid.UUID
}

// AuditEvent is something that happened. Born final, and carries NO work
// lineage — it cannot be queried by Story, and offering that would mean
// inventing a column the schema does not have.
type AuditEvent struct {
	UserID              *uuid.UUID
	PrincipalInstanceID *uuid.UUID

	OccurredAt time.Time

	EventType string
	Detail    json.RawMessage

	AuditEventID   uuid.UUID
	OrganizationID uuid.UUID
}

// CreateLLMCallInput opens a call. It carries no outcome: an outcome is
// what a call learns by ENDING, and creation that could set one would write
// a terminal row and bypass the once-only completion guard.
type CreateLLMCallInput struct {
	UserID    *uuid.UUID
	StartedAt *time.Time

	Lineage Lineage

	Provider string
	Model    string

	OrganizationID      uuid.UUID
	PrincipalInstanceID uuid.UUID
}

// CreateToolCallInput opens a tool call, optionally claiming the LLM call
// that produced it.
type CreateToolCallInput struct {
	UserID    *uuid.UUID
	LLMCallID *uuid.UUID
	StartedAt *time.Time

	Lineage Lineage

	ToolName  string
	Arguments json.RawMessage

	OrganizationID      uuid.UUID
	PrincipalInstanceID uuid.UUID
}

// CompleteLLMCallInput records what the call learned by ending.
//
// Cost is optional and its absence is load-bearing: null on a completed
// call means not knowable — `paired-local`'s local models — which is a
// different fact from zero, a real measurement.
type CompleteLLMCallInput struct {
	Cost         *USD
	ErrorMessage *string
	FinishedAt   *time.Time

	Tokens TokenCounts

	OrganizationID uuid.UUID
	LLMCallID      uuid.UUID

	Succeeded bool
}

// CompleteToolCallInput records a tool call's outcome.
type CompleteToolCallInput struct {
	ErrorMessage *string
	FinishedAt   *time.Time

	Result json.RawMessage

	OrganizationID uuid.UUID
	ToolCallID     uuid.UUID

	Succeeded bool
}

// LLMCompletion reports the result of completing an LLM call: the call as
// it now stands, and whether this caller is the one who recorded it.
//
// Completion is once-only and idempotent: two paths can observe one call
// ending — a normal return and a supervisor's error handler — and the first
// outcome is the true one. A repeat returns what was recorded with
// Recorded=false rather than an error, since making the loser fail would
// turn correct shutdown into spurious failure.
//
// It carries the whole call rather than a summary. A repeat is promised
// "the recorded outcome", and an outcome is everything the call learned by
// ending — tokens and cost as much as success. A loser handed only the
// flags could not tell whether its own figures were the ones stored, which
// is the single question it has.
type LLMCompletion struct {
	Call     LLMCall
	Recorded bool
}

// ToolCompletion is the tool-call equivalent, carrying the result payload
// for the same reason.
type ToolCompletion struct {
	Call     ToolCall
	Recorded bool
}

// CostAggregate is a cost and token total over one (provider, model) cohort
// in one window, reported WITH its completeness.
//
// A bare SUM would describe a number that looks complete whatever
// proportion of its cohort it covers. The three call states are separate
// facts: measured, unmeasured (completed, cost not knowable) and open (not
// ended, so no cost exists yet). Folding open calls into unmeasured would
// make a running campaign under-report its own cost and never correct
// itself.
type CostAggregate struct {
	TotalCost USDTotal
	Tokens    TokenCounts

	MeasuredCalls   int64
	UnmeasuredCalls int64
	OpenCalls       int64

	SucceededCalls int64
	FailedCalls    int64
}

// The tables one truncation pass covers, in the order it deletes them.
// Callers index TruncationResult.PerTable by these rather than by a string
// literal that no compiler checks.
const (
	TableAuditEvents    = "audit_events"
	TableMetricEvents   = "metric_events"
	TableAuditArtifacts = "audit_artifacts"
	TableToolCalls      = "tool_calls"
	TableLLMCalls       = "llm_calls"
)

// TruncationResult reports one truncation pass.
//
// Retention reasons are assigned by PRECEDENCE — pinned, then open, then
// referenced — so a row appears in exactly one bucket and
// Candidates == Deleted + RetainedPinned + RetainedOpen + RetainedReferenced
// holds. Independent counts would not sum to anything, since a call can be
// both open and referenced at once.
type TruncationResult struct {
	PerTable map[string]TableTruncation
}

// TableTruncation is one table's contribution.
type TableTruncation struct {
	Candidates         int64
	Deleted            int64
	RetainedPinned     int64
	RetainedOpen       int64
	RetainedReferenced int64
}

// Reconciles reports whether the buckets account for every candidate. A
// result that does not reconcile describes no consistent set of rows.
func (t TableTruncation) Reconciles() bool {
	return t.Candidates == t.Deleted+t.RetainedPinned+t.RetainedOpen+t.RetainedReferenced
}

// Page bounds a list read. Every list is bounded: these are the largest
// tables in the system, and an unbounded scan over them is not a query
// anyone should be able to write by accident.
type Page struct {
	// After is the exclusive keyset cursor. Nil means the first page.
	After *Cursor
	Limit int32
}

// MaxPageLimit is the documented ceiling on a single page.
//
// A limit is mandatory, so the ceiling is what stops "bounded" from meaning
// "bounded by whatever number the caller felt like". The value is a
// judgement, not a measurement: large enough that an importer does not
// spend its life paging, small enough that one page is a bounded amount of
// memory on the largest tables in the system.
const MaxPageLimit int32 = 1000

// Validate checks a page request before it reaches SQL.
func (p Page) Validate() error {
	switch {
	case p.Limit <= 0:
		return fmt.Errorf("page limit %d is not positive; every list read is bounded and the limit is "+
			"mandatory, so there is no value meaning 'all rows'", p.Limit)
	case p.Limit > MaxPageLimit:
		return fmt.Errorf("page limit %d exceeds the maximum of %d", p.Limit, MaxPageLimit)
	case p.After != nil && p.After.ID == uuid.Nil:
		// Not a formality. The cursor is compared as a row value, so a null
		// id makes `(ts, id) > (ts, NULL)` evaluate to NULL and the page
		// comes back EMPTY — indistinguishable from having reached the end.
		return errors.New("keyset cursor carries a timestamp but no id; the id is the tie-breaker, and " +
			"without it the comparison is null and the page reads as empty")
	case p.After != nil && p.After.At.IsZero():
		// The other half, and it fails in the opposite direction: the zero
		// time is year 1, so every row sorts after it and paging RESTARTS
		// from the beginning instead of resuming — an infinite loop for any
		// caller that pages until a short page.
		return errors.New("keyset cursor carries an id but no timestamp; the zero time precedes every " +
			"row, so paging would restart from the beginning rather than resume")
	}
	return nil
}

// Cursor is the (timestamp, id) position paging resumes from. The id is not
// optional: rows written in the same instant share a timestamp, and a
// cursor on time alone loses the tail of every tied group.
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

// CallReader is the call family's read surface.
type CallReader interface {
	GetLLMCall(ctx context.Context, organizationID, callID uuid.UUID) (*LLMCall, error)
	GetToolCall(ctx context.Context, organizationID, callID uuid.UUID) (*ToolCall, error)

	ListLLMCallsByStory(ctx context.Context, organizationID, storyID uuid.UUID, page Page) ([]LLMCall, error)
	ListLLMCallsByPrincipal(ctx context.Context, organizationID, instanceID uuid.UUID, page Page) ([]LLMCall, error)
	ListLLMCallsInWindow(ctx context.Context, organizationID uuid.UUID, from, to time.Time, page Page) ([]LLMCall, error)

	ListToolCallsByStory(ctx context.Context, organizationID, storyID uuid.UUID, page Page) ([]ToolCall, error)
	ListToolCallsByPrincipal(ctx context.Context, organizationID, instanceID uuid.UUID, page Page) ([]ToolCall, error)
	ListToolCallsInWindow(ctx context.Context, organizationID uuid.UUID, from, to time.Time, page Page) ([]ToolCall, error)

	ListMetricEventsInWindow(ctx context.Context, organizationID uuid.UUID, from, to time.Time, page Page) ([]MetricEvent, error)
	ListAuditEventsInWindow(ctx context.Context, organizationID uuid.UUID, from, to time.Time, page Page) ([]AuditEvent, error)

	// AggregateCost takes a mandatory window and cohort, so the result
	// always describes a stated population rather than "everything retained".
	AggregateCost(ctx context.Context, organizationID uuid.UUID, provider, model string, from, to time.Time) (CostAggregate, error)
}

// CallWriter is the call family's write surface.
type CallWriter interface {
	CreateLLMCall(ctx context.Context, input CreateLLMCallInput) (*LLMCall, error)
	CreateToolCall(ctx context.Context, input CreateToolCallInput) (*ToolCall, error)

	CompleteLLMCall(ctx context.Context, input CompleteLLMCallInput) (LLMCompletion, error)
	CompleteToolCall(ctx context.Context, input CompleteToolCallInput) (ToolCompletion, error)

	CreateMetricEvent(ctx context.Context, event MetricEvent) (*MetricEvent, error)
	CreateAuditEvent(ctx context.Context, event AuditEvent) (*AuditEvent, error)
}

// Maintenance is the retention surface: destructive operations that own
// their own transaction.
//
// Separate from Writer, and embedded by Store alone, because truncation is
// only sound under ONE snapshot. Store opens a REPEATABLE READ transaction
// for it; WithTx opens at the pool's default, and offers no way to ask for
// anything else. Advertising truncation on Tx would advertise an operation
// that necessarily refuses wherever a caller could reach it — an interface
// promising something no implementation can honour there.
type Maintenance interface {
	// TruncateAuditBefore removes Audit history older than the horizon,
	// organization-scoped, in dependency order, under one REPEATABLE READ
	// snapshot with a bounded retry on serialization failure.
	//
	// There is no "delete all": an unbounded destructive operation should
	// not be reachable by accident.
	TruncateAuditBefore(ctx context.Context, organizationID uuid.UUID, before time.Time) (TruncationResult, error)
}
