package postgres

import (
	"context"
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
	if input.Provider == "" || input.Model == "" {
		return nil, errors.New("provider and model are required; every cost and MPH aggregate groups by them")
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
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) CompleteLLMCall(ctx context.Context, input store.CompleteLLMCallInput) (store.CompletionOutcome, error) {
	if err := checkOutcomeCoherence(input.Succeeded, input.ErrorMessage); err != nil {
		return store.CompletionOutcome{}, err
	}

	locked, err := t.queries.LockLLMCall(ctx, gen.LockLLMCallParams{
		LlmCallID:      toUUID(input.LLMCallID),
		OrganizationID: toUUID(input.OrganizationID),
	})
	if err != nil {
		return store.CompletionOutcome{}, notFound(err, "llm call", input.LLMCallID)
	}

	// Already complete: return what the winner recorded, unchanged. Two
	// paths observing one call ending is normal, so the loser is not an
	// error -- but it can tell from Recorded.
	if locked.FinishedAt.Valid {
		return store.CompletionOutcome{
			FinishedAt:   fromTimestamptz(locked.FinishedAt),
			Succeeded:    locked.Succeeded != nil && *locked.Succeeded,
			ErrorMessage: fromNullString(locked.ErrorMessage),
			Recorded:     false,
		}, nil
	}

	cost, err := toNumeric(input.Cost)
	if err != nil {
		return store.CompletionOutcome{}, err
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
		return store.CompletionOutcome{}, fmt.Errorf("complete llm call %s: %w", input.LLMCallID, err)
	}
	if affected != 1 {
		return store.CompletionOutcome{}, fmt.Errorf(
			"%w: completing llm call %s affected no rows while holding its lock with a null finished_at",
			store.ErrInvariant, input.LLMCallID)
	}

	completed, err := t.queries.GetLLMCall(ctx, gen.GetLLMCallParams{
		LlmCallID:      toUUID(input.LLMCallID),
		OrganizationID: toUUID(input.OrganizationID),
	})
	if err != nil {
		return store.CompletionOutcome{}, notFound(err, "llm call", input.LLMCallID)
	}
	return store.CompletionOutcome{
		FinishedAt:   fromTimestamptz(completed.FinishedAt),
		Succeeded:    input.Succeeded,
		ErrorMessage: input.ErrorMessage,
		Recorded:     true,
	}, nil
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
	if provider == "" || model == "" {
		return store.CostAggregate{}, errors.New("an aggregate needs both provider and model: the same " +
			"model name is served by different providers at different prices, so a cohort of one is not a cohort")
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
