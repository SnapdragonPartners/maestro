package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

func llmCallFromRow(row *gen.LlmCall) (store.LLMCall, error) {
	cost, err := fromNumeric(row.CostUsd)
	if err != nil {
		return store.LLMCall{}, err
	}
	return store.LLMCall{
		FinishedAt:   fromNullTimestamptz(row.FinishedAt),
		Succeeded:    row.Succeeded,
		ErrorMessage: fromNullString(row.ErrorMessage),
		Cost:         cost,
		UserID:       fromNullUUID(row.UserID),

		Lineage: store.Lineage{
			ProductID: fromNullUUID(row.ProductID),
			FeatureID: fromNullUUID(row.FeatureID),
			EpicID:    fromNullUUID(row.EpicID),
			StoryID:   fromNullUUID(row.StoryID),
		},
		StartedAt: fromTimestamptz(row.StartedAt),

		Provider: row.Provider,
		Model:    row.Model,

		Tokens: store.TokenCounts{
			Input:     row.InputTokens,
			Output:    row.OutputTokens,
			Reasoning: row.ReasoningTokens,
			Cached:    row.CachedTokens,
		},

		LLMCallID:           fromUUID(row.LlmCallID),
		OrganizationID:      fromUUID(row.OrganizationID),
		PrincipalInstanceID: fromUUID(row.PrincipalInstanceID),
	}, nil
}

// CreateLLMCall opens a call.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) CreateLLMCall(ctx context.Context, input store.CreateLLMCallInput) (*store.LLMCall, error) {
	callID, err := newIdentifier(uuid.Nil)
	if err != nil {
		return nil, err
	}
	if lineageErr := checkLineageChain(input.Lineage); lineageErr != nil {
		return nil, lineageErr
	}
	if nameErr := requireName(input.Provider, "provider"); nameErr != nil {
		return nil, nameErr
	}
	if nameErr := requireName(input.Model, "model"); nameErr != nil {
		return nil, nameErr
	}

	row, err := t.queries.CreateLLMCall(ctx, gen.CreateLLMCallParams{
		LlmCallID:           toUUID(callID),
		OrganizationID:      toUUID(input.OrganizationID),
		UserID:              toNullUUID(input.UserID),
		PrincipalInstanceID: toUUID(input.PrincipalInstanceID),
		ProductID:           toNullUUID(input.Lineage.ProductID),
		FeatureID:           toNullUUID(input.Lineage.FeatureID),
		EpicID:              toNullUUID(input.Lineage.EpicID),
		StoryID:             toNullUUID(input.Lineage.StoryID),
		Provider:            input.Provider,
		Model:               input.Model,
		StartedAt:           toNullTimestamptz(input.StartedAt),
	})
	if err != nil {
		return nil, fmt.Errorf("create llm call: %w", err)
	}
	created, err := llmCallFromRow(&row)
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// CompleteLLMCall records the outcome, once only.
//
// Lock and classify BEFORE validating the proposed outcome. The order is
// the contract: a repeat is a repeat whatever it proposes, so validating
// first would turn a losing supervisor's incoherent second opinion into an
// error instead of returning the winner's recorded outcome, which is
// exactly the spurious failure design D1 exists to prevent. The proposed
// outcome is checked only while the row is still open, when it is the
// outcome that will actually be stored.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) CompleteLLMCall(ctx context.Context, input store.CompleteLLMCallInput) (store.LLMCompletion, error) {
	locked, err := t.queries.LockLLMCall(ctx, gen.LockLLMCallParams{
		LlmCallID:      toUUID(input.LLMCallID),
		OrganizationID: toUUID(input.OrganizationID),
	})
	if err != nil {
		return store.LLMCompletion{}, notFound(err, "llm call", input.LLMCallID)
	}

	// Already complete: return what the winner recorded, whole and
	// unchanged. Two paths observing one call ending is normal, so the
	// loser is not an error -- but it can tell from Recorded.
	if locked.FinishedAt.Valid {
		recorded, convErr := llmCallFromRow(&locked)
		if convErr != nil {
			return store.LLMCompletion{}, convErr
		}
		return store.LLMCompletion{Call: recorded, Recorded: false}, nil
	}

	if outcomeErr := checkOutcomeCoherence(input.Succeeded, input.ErrorMessage); outcomeErr != nil {
		return store.LLMCompletion{}, outcomeErr
	}
	if tokenErr := checkTokenCounts(input.Tokens); tokenErr != nil {
		return store.LLMCompletion{}, tokenErr
	}
	started := fromTimestamptz(locked.StartedAt)
	if intervalErr := checkCompletionInterval(input.FinishedAt, started, input.LLMCallID); intervalErr != nil {
		return store.LLMCompletion{}, intervalErr
	}

	cost, err := toNumeric(input.Cost)
	if err != nil {
		return store.LLMCompletion{}, err
	}
	affected, err := t.queries.CompleteLLMCall(ctx, gen.CompleteLLMCallParams{
		FinishedAt:      toNullTimestamptz(input.FinishedAt),
		Succeeded:       &input.Succeeded,
		ErrorMessage:    input.ErrorMessage,
		InputTokens:     input.Tokens.Input,
		OutputTokens:    input.Tokens.Output,
		ReasoningTokens: input.Tokens.Reasoning,
		CachedTokens:    input.Tokens.Cached,
		CostUsd:         cost,
		LlmCallID:       toUUID(input.LLMCallID),
		OrganizationID:  toUUID(input.OrganizationID),
	})
	if err != nil {
		return store.LLMCompletion{}, fmt.Errorf("complete llm call %s: %w", input.LLMCallID, err)
	}
	if affected != 1 {
		return store.LLMCompletion{}, fmt.Errorf(
			"%w: completing llm call %s affected no rows while holding its lock with a null finished_at",
			store.ErrInvariant, input.LLMCallID)
	}

	// Re-read rather than assembling the outcome from the input: finished_at
	// defaults to now() in SQL when the caller does not supply one, so the
	// stored instant is only knowable by reading it back.
	completed, err := t.queries.GetLLMCall(ctx, gen.GetLLMCallParams{
		LlmCallID:      toUUID(input.LLMCallID),
		OrganizationID: toUUID(input.OrganizationID),
	})
	if err != nil {
		return store.LLMCompletion{}, notFound(err, "llm call", input.LLMCallID)
	}
	call, err := llmCallFromRow(&completed)
	if err != nil {
		return store.LLMCompletion{}, err
	}
	return store.LLMCompletion{Call: call, Recorded: true}, nil
}

func (t *tx) GetLLMCall(ctx context.Context, organizationID, callID uuid.UUID) (*store.LLMCall, error) {
	row, err := t.queries.GetLLMCall(ctx, gen.GetLLMCallParams{
		LlmCallID:      toUUID(callID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return nil, notFound(err, "llm call", callID)
	}
	call, err := llmCallFromRow(&row)
	if err != nil {
		return nil, err
	}
	return &call, nil
}

// AggregateCost totals one cohort in one window, with its completeness.
func (t *tx) AggregateCost(ctx context.Context, organizationID uuid.UUID, provider, model string,
	from, to time.Time,
) (store.CostAggregate, error) {
	// Blank, not merely empty: a cohort named by whitespace matches nothing,
	// and a total of zero over a cohort that cannot exist reads exactly like
	// a real cohort that cost nothing.
	if err := requireName(provider, "aggregate cohort provider"); err != nil {
		return store.CostAggregate{}, fmt.Errorf("%w: the same model name is served by different providers "+
			"at different prices, so a cohort of one is not a cohort", err)
	}
	if err := requireName(model, "aggregate cohort model"); err != nil {
		return store.CostAggregate{}, err
	}
	if !to.After(from) {
		return store.CostAggregate{}, fmt.Errorf("window end %s is not after window start %s", to, from)
	}

	row, err := t.queries.AggregateLLMCost(ctx, gen.AggregateLLMCostParams{
		OrganizationID: toUUID(organizationID),
		Provider:       provider,
		Model:          model,
		WindowStart:    toTimestamptz(from),
		WindowEnd:      toTimestamptz(to),
	})
	if err != nil {
		return store.CostAggregate{}, fmt.Errorf("aggregate llm cost: %w", err)
	}
	total, err := fromNumericTotal(row.TotalCostUsd)
	if err != nil {
		return store.CostAggregate{}, err
	}
	return store.CostAggregate{
		TotalCost: total,
		Tokens: store.TokenCounts{
			Input:     row.TotalInputTokens,
			Output:    row.TotalOutputTokens,
			Reasoning: row.TotalReasoningTokens,
			Cached:    row.TotalCachedTokens,
		},
		MeasuredCalls:   row.MeasuredCalls,
		UnmeasuredCalls: row.UnmeasuredCalls,
		OpenCalls:       row.OpenCalls,
		SucceededCalls:  row.SucceededCalls,
		FailedCalls:     row.FailedCalls,
	}, nil
}

// checkLineageChain enforces the prefix rule the schema's shape check
// expresses, so a caller learns WHICH level is missing rather than reading
// a constraint name.
func checkLineageChain(lineage store.Lineage) error {
	switch {
	case lineage.StoryID != nil && lineage.EpicID == nil:
		return errors.New("lineage names a Story but no Epic; lineage is a prefix chain")
	case lineage.EpicID != nil && lineage.FeatureID == nil:
		return errors.New("lineage names an Epic but no Feature; lineage is a prefix chain")
	case lineage.FeatureID != nil && lineage.ProductID == nil:
		return errors.New("lineage names a Feature but no Product; lineage is a prefix chain")
	}
	return nil
}

// checkOutcomeCoherence mirrors the schema's coherence constraint, so the
// caller gets a diagnostic naming the field rather than a constraint name.
// The constraint remains as the backstop for writes that bypass the seam.
func checkOutcomeCoherence(succeeded bool, errorMessage *string) error {
	blank := errorMessage == nil || strings.TrimSpace(*errorMessage) == ""
	switch {
	case succeeded && !blank:
		return errors.New("a successful call must not carry an error message; the row would be one no " +
			"reader can interpret")
	case !succeeded && blank:
		return errors.New("a failed call must carry a non-blank diagnostic; the failure path is exactly " +
			"when someone reads the record")
	}
	return nil
}

// requireName rejects a blank identifying name.
//
// Blank, not empty. An empty check passes a tab, and migration 000011
// learned the same lesson from the other side: `btrim(x)` with one argument
// strips SPACES ONLY, so a newline-only name satisfied a "non-blank"
// constraint while being blank to every reader. strings.TrimSpace covers a
// superset of the constraint's character list, so nothing the seam accepts
// can fail the column for this reason.
func requireName(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is blank; an unnamed call or event is unattributable, and every cost, MPH "+
			"and reliability aggregate groups by exactly these names", field)
	}
	return nil
}

// checkTokenCounts refuses negative counters, naming the field.
//
// The schema's check refuses the row as a whole; this one says which
// counter was wrong. A caller reading `llm_calls_tokens_nonnegative_check`
// off a failed write has to go and read the migration to learn that much.
func checkTokenCounts(tokens store.TokenCounts) error {
	for _, counter := range []struct {
		field string
		value int64
	}{
		{"input_tokens", tokens.Input},
		{"output_tokens", tokens.Output},
		{"reasoning_tokens", tokens.Reasoning},
		{"cached_tokens", tokens.Cached},
	} {
		if counter.value < 0 {
			return fmt.Errorf("%s is %d; a token counter is a count and cannot be negative",
				counter.field, counter.value)
		}
	}
	return nil
}

// checkCompletionInterval refuses a completion that ends before the call
// started, against the LOCKED row's start rather than a caller-supplied one.
//
// A nil finishedAt is not checkable here and does not need to be: SQL fills
// it with now(), and the schema's interval constraint is the backstop for
// the one case that can still be wrong -- a call whose start was recorded
// in the future.
func checkCompletionInterval(finishedAt *time.Time, startedAt time.Time, callID uuid.UUID) error {
	if finishedAt == nil || !finishedAt.Before(startedAt) {
		return nil
	}
	return fmt.Errorf("call %s would finish at %s, before it started at %s; that is not an interval",
		callID, finishedAt.UTC(), startedAt.UTC())
}

// requiredJSON prepares a NOT NULL jsonb column.
//
// Absent becomes an empty object rather than an error: a tool call with no
// arguments and an event with no labels are ordinary, and the column's
// default says the same thing. Malformed JSON is refused here so the caller
// reads which field it mangled instead of a driver-level syntax error.
func requiredJSON(value json.RawMessage, field string) ([]byte, error) {
	if len(value) == 0 {
		return []byte("{}"), nil
	}
	if !json.Valid(value) {
		return nil, fmt.Errorf("%s is not valid JSON", field)
	}
	return value, nil
}

// optionalJSON prepares a nullable jsonb column, preserving absence as NULL.
// A tool call that failed has no result, and an empty object would claim it
// returned one.
func optionalJSON(value json.RawMessage, field string) ([]byte, error) {
	if len(value) == 0 {
		return nil, nil
	}
	if !json.Valid(value) {
		return nil, fmt.Errorf("%s is not valid JSON", field)
	}
	return value, nil
}
