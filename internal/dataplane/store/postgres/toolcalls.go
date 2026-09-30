package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// provenanceConstraint is the composite foreign key a tool call's claim on
// an LLM call must satisfy: (llm_call_id, principal_instance_id,
// lineage_key, organization_id).
//
// Matched by CONSTRAINT NAME rather than by message text, which is
// localised and reworded between Postgres versions.
const provenanceConstraint = "tool_calls_llm_call_fkey"

func toolCallFromRow(row *gen.ToolCall) store.ToolCall {
	var outcome *store.ToolOutcome
	if row.Outcome != nil {
		converted := store.ToolOutcome(*row.Outcome)
		outcome = &converted
	}
	var decision *store.OperatorDecisionRecord
	if row.OperatorDecision != nil {
		decision = &store.OperatorDecisionRecord{
			Decision:   store.OperatorDecision(*row.OperatorDecision),
			DecidedBy:  fromUUID(row.OperatorDecidedBy),
			DecidedAt:  fromTimestamptz(row.OperatorDecidedAt),
			ConsumedAt: fromNullTimestamptz(row.OperatorDecisionConsumedAt),
			ConsumedBy: fromNullUUID(row.OperatorDecisionConsumedBy),
		}
	}
	return store.ToolCall{
		FinishedAt:   fromNullTimestamptz(row.FinishedAt),
		Outcome:      outcome,
		ErrorMessage: fromNullString(row.ErrorMessage),
		Result:       row.Result,
		UserID:       fromNullUUID(row.UserID),
		LLMCallID:    fromNullUUID(row.LlmCallID),

		ExecutionID:          fromNullUUID(row.ExecutionID),
		RequirementSet:       row.RequirementSet,
		RequirementSetDigest: fromNullString(row.RequirementSetDigest),
		Family:               fromNullString(row.Family),
		RequestDigest:        fromNullString(row.RequestDigest),
		ArgumentsDigest:      fromNullString(row.ArgumentsDigest),
		CallerRef:            fromNullString(row.CallerRef),
		TargetKey:            fromNullString(row.TargetKey),
		MutationKey:          fromNullString(row.MutationKey),
		ClaimedBy:            fromNullUUID(row.ClaimedBy),
		RevalidatedAt:        fromNullTimestamptz(row.RevalidatedAt),
		ReasonCode:           (*store.ReasonCode)(fromNullString(row.ReasonCode)),
		OperatorDecision:     decision,
		DrainDisposition:     (*store.DrainDisposition)(fromNullString(row.DrainDisposition)),

		Lineage: store.Lineage{
			ProductID: fromNullUUID(row.ProductID),
			FeatureID: fromNullUUID(row.FeatureID),
			EpicID:    fromNullUUID(row.EpicID),
			StoryID:   fromNullUUID(row.StoryID),
		},
		StartedAt: fromTimestamptz(row.StartedAt),

		State:     store.AttemptState(row.State),
		ToolName:  row.ToolName,
		Arguments: row.Arguments,

		ToolCallID:          fromUUID(row.ToolCallID),
		OrganizationID:      fromUUID(row.OrganizationID),
		PrincipalInstanceID: fromUUID(row.PrincipalInstanceID),
	}
}

// CreateToolCall opens a tool call, optionally claiming its parent LLM call.
//
// Provenance stays ONE atomic write (design D3). Reading the parent first
// would add a round trip to the hottest write path in the system and prove
// nothing the foreign key was not already proving -- the row it validated
// can change between the read and the insert.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) CreateToolCall(ctx context.Context, input store.CreateToolCallInput) (*store.ToolCall, error) {
	callID, err := newIdentifier(uuid.Nil)
	if err != nil {
		return nil, err
	}
	if lineageErr := checkLineageChain(input.Lineage); lineageErr != nil {
		return nil, lineageErr
	}
	if nameErr := requireName(input.ToolName, "tool_name"); nameErr != nil {
		return nil, nameErr
	}
	arguments, err := requiredJSON(input.Arguments, "arguments")
	if err != nil {
		return nil, err
	}

	row, err := t.queries.CreateToolCall(ctx, gen.CreateToolCallParams{
		ToolCallID:          toUUID(callID),
		OrganizationID:      toUUID(input.OrganizationID),
		UserID:              toNullUUID(input.UserID),
		PrincipalInstanceID: toUUID(input.PrincipalInstanceID),
		LlmCallID:           toNullUUID(input.LLMCallID),
		ProductID:           toNullUUID(input.Lineage.ProductID),
		FeatureID:           toNullUUID(input.Lineage.FeatureID),
		EpicID:              toNullUUID(input.Lineage.EpicID),
		StoryID:             toNullUUID(input.Lineage.StoryID),
		ToolName:            input.ToolName,
		Arguments:           arguments,
		StartedAt:           toNullTimestamptz(input.StartedAt),
	})
	if err != nil {
		if violatesProvenanceKey(err) {
			return nil, invalidProvenance(input, err)
		}
		return nil, fmt.Errorf("create tool call: %w", err)
	}
	created := toolCallFromRow(&row)
	return &created, nil
}

// CompleteToolCall records the outcome of a call OUTSIDE any execution,
// once only: the importer's verb, and the one every row without an
// execution settles through. It is SettleAttempt with no reason code and no
// disposition, which is exactly what such a row may carry -- so a caller
// asking for a boundary outcome here is refused by the reason-code rule,
// not by "no producer". Item 5 lifted that refusal (design D12) and this is
// what replaced it.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) CompleteToolCall(ctx context.Context, input store.CompleteToolCallInput) (store.ToolCompletion, error) {
	return t.SettleAttempt(ctx, store.SettleAttemptInput{
		ErrorMessage: input.ErrorMessage, FinishedAt: input.FinishedAt,
		Outcome: input.Outcome, Result: input.Result,
		OrganizationID: input.OrganizationID, ToolCallID: input.ToolCallID,
	})
}

func (t *tx) GetToolCall(ctx context.Context, organizationID, callID uuid.UUID) (*store.ToolCall, error) {
	row, err := t.queries.GetToolCall(ctx, gen.GetToolCallParams{
		ToolCallID:     toUUID(callID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return nil, notFound(err, "tool call", callID)
	}
	call := toolCallFromRow(&row)
	return &call, nil
}

// violatesProvenanceKey reports whether err is the provenance foreign key's
// refusal, matched by CONSTRAINT NAME.
//
// Named specifically rather than parameterised: it is the one constraint
// this seam translates, and the other foreign keys on tool_calls -- the
// principal, the lineage, the user -- must NOT be read as a provenance
// failure, which would blame the wrong claim.
func violatesProvenanceKey(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == provenanceConstraint
}

// invalidProvenance translates the composite foreign key's refusal.
//
// Deliberately generic (design D3). The constraint cannot tell its causes
// apart: the claimed parent may not exist, or belong to another principal,
// or to different work, or to another organization -- and lineage_key folds
// user_id in, so an accountable-user mismatch looks identical. Naming one
// would be a guess presented as a diagnosis.
//
// So it reports what the caller CLAIMED, and leaves the conclusion to
// whoever compares that against the LLM call they meant.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func invalidProvenance(input store.CreateToolCallInput, cause error) error {
	claimed := "none"
	if input.LLMCallID != nil {
		claimed = input.LLMCallID.String()
	}
	return fmt.Errorf("%w: claimed llm call %s for principal %s, user %s and lineage %s in organization %s: %w",
		store.ErrInvalidProvenance, claimed, input.PrincipalInstanceID,
		describeUUID(input.UserID), describeLineage(input.Lineage), input.OrganizationID, cause)
}

// describeUUID renders an optional identifier for a diagnostic.
func describeUUID(id *uuid.UUID) string {
	if id == nil {
		return "none"
	}
	return id.String()
}

// describeLineage renders the work tuple the provenance key encodes.
func describeLineage(lineage store.Lineage) string {
	return fmt.Sprintf("product=%s feature=%s epic=%s story=%s",
		describeUUID(lineage.ProductID), describeUUID(lineage.FeatureID),
		describeUUID(lineage.EpicID), describeUUID(lineage.StoryID))
}
