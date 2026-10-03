package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// The execution's boundary verbs (Phase 3 item 5 design, D9-D12): the
// configuration at dispatch, closure, supersession and the terminal result.
// Each transition runs under the execution row's exclusive lock in the
// caller's transaction; registration (attempts.go) takes the same row FOR
// SHARE, which is what linearizes the two.

func rejectExecution(operation string, executionID uuid.UUID, reason store.ExecutionReason, detail string) error {
	return &store.ExecutionRejected{Operation: operation, ExecutionID: executionID, Reason: reason, Detail: detail}
}

// canonicalCapabilitySet is the stored form (D12): sorted, de-duplicated,
// no blank identity. A blank is refused here because no registry could name
// it; every other identity is judged by the composition's ActionContract,
// which the caller consults with the canonical slice this returns.
func canonicalCapabilitySet(operation string, subject uuid.UUID, capabilities []string) ([]byte, []string, error) {
	canonical := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		if strings.TrimSpace(capability) == "" {
			return nil, nil, rejectExecution(operation, subject, store.ReasonCapabilityBlank, "")
		}
		canonical = append(canonical, capability)
	}
	slices.Sort(canonical)
	canonical = slices.Compact(canonical)
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, nil, fmt.Errorf("encode capability set: %w", err)
	}
	return encoded, canonical, nil
}

func capabilitySetFromRow(encoded []byte) ([]string, error) {
	var capabilities []string
	if err := json.Unmarshal(encoded, &capabilities); err != nil {
		return nil, fmt.Errorf("%w: stored capability set is not a JSON array of strings: %w", store.ErrInvariant, err)
	}
	if capabilities == nil {
		capabilities = []string{}
	}
	return capabilities, nil
}

func executionFromRow(row *gen.Execution) (*store.Execution, error) {
	capabilities, err := capabilitySetFromRow(row.CapabilitySet)
	if err != nil {
		return nil, fmt.Errorf("execution %s: %w", fromUUID(row.ExecutionID), err)
	}
	execution := &store.Execution{
		AdmissionClosedAt: fromNullTimestamptz(row.AdmissionClosedAt), CreatedAt: fromTimestamptz(row.CreatedAt),
		TerminatedAt:   fromNullTimestamptz(row.TerminatedAt),
		CapabilitySet:  capabilities,
		AuthorityState: store.AuthorityState(row.AuthorityState),
		ExecutionID:    fromUUID(row.ExecutionID), OrganizationID: fromUUID(row.OrganizationID),
		ProductID: fromUUID(row.ProductID), FeatureID: fromUUID(row.FeatureID), EpicID: fromUUID(row.EpicID),
		StoryID: fromUUID(row.StoryID), StoryDispatchID: fromUUID(row.StoryDispatchID),
		ActingUserID: fromUUID(row.ActingUserID),
		Headless:     row.Headless,
	}
	if row.Status != nil {
		execution.Terminal = &store.TerminalResult{
			Status:                store.ExecutionStatus(*row.Status),
			CompletionDisposition: (*store.CompletionDisposition)(row.CompletionDisposition),
			CancellationReason:    (*store.CancellationReason)(row.CancellationReason),
			FailureClass:          (*store.FailureClass)(row.FailureClass),
			BlockedToolCallID:     fromNullUUID(row.BlockedToolCallID),
		}
		if row.ErrorMessage != nil {
			execution.Terminal.ErrorMessage = *row.ErrorMessage
		}
	}
	return execution, nil
}

// GetExecution reads one execution by id, tenant-scoped.
func (t *tx) GetExecution(ctx context.Context, organizationID, executionID uuid.UUID) (*store.Execution, error) {
	row, err := t.queries.GetExecution(ctx, gen.GetExecutionParams{OrganizationID: toUUID(organizationID), ExecutionID: toUUID(executionID)})
	if err != nil {
		return nil, notFound(err, "execution", executionID)
	}
	return executionFromRow(&row)
}

// LockExecution reads the execution FOR UPDATE, for the caller's
// transaction (D8's T2). The same statement every transition in this file
// begins with, exposed so gate 3's revalidation and the attempt updates
// that follow it share one lock.
func (t *tx) LockExecution(ctx context.Context, organizationID, executionID uuid.UUID) (*store.Execution, error) {
	row, err := t.queries.LockExecution(ctx, gen.LockExecutionParams{OrganizationID: toUUID(organizationID), ExecutionID: toUUID(executionID)})
	if err != nil {
		return nil, notFound(err, "execution", executionID)
	}
	return executionFromRow(&row)
}

// CloseAdmission closes admission under the row's exclusive lock (D9).
// Idempotent, and the lock is not optional: a closure that did not wait for
// in-flight registrations (FOR SHARE) would close admission with attempts
// registering behind it.
func (t *tx) CloseAdmission(ctx context.Context, organizationID, executionID uuid.UUID) error {
	if _, err := t.queries.LockExecution(ctx, gen.LockExecutionParams{OrganizationID: toUUID(organizationID), ExecutionID: toUUID(executionID)}); err != nil {
		return notFound(err, "execution", executionID)
	}
	if _, err := t.queries.CloseExecutionAdmission(ctx, gen.CloseExecutionAdmissionParams{OrganizationID: toUUID(organizationID), ExecutionID: toUUID(executionID)}); err != nil {
		return fmt.Errorf("close admission of execution %s: %w", executionID, err)
	}
	return nil
}

// SupersedeExecution marks authority superseded, closes admission, settles
// the waits stale and returns the drain list (D8, D10), in one transaction.
func (t *tx) SupersedeExecution(ctx context.Context, organizationID, executionID uuid.UUID) (store.Supersession, error) {
	const operation = "SupersedeExecution"
	var none store.Supersession
	locked, err := t.queries.LockExecution(ctx, gen.LockExecutionParams{OrganizationID: toUUID(organizationID), ExecutionID: toUUID(executionID)})
	if err != nil {
		return none, notFound(err, "execution", executionID)
	}
	if store.AuthorityState(locked.AuthorityState) != store.AuthorityCurrent {
		return none, rejectExecution(operation, executionID, store.ReasonAlreadySuperseded, "")
	}
	rows, err := t.queries.SupersedeExecution(ctx, gen.SupersedeExecutionParams{OrganizationID: toUUID(organizationID), ExecutionID: toUUID(executionID)})
	if err != nil {
		return none, fmt.Errorf("%s %s: %w", operation, executionID, err)
	}
	if rows != 1 {
		return none, fmt.Errorf("%w: superseding execution %s affected %d rows under its lock", store.ErrInvariant, executionID, rows)
	}
	staled, err := t.queries.StaleSupersededWaits(ctx, gen.StaleSupersededWaitsParams{OrganizationID: toUUID(organizationID), ExecutionID: toNullUUID(&executionID)})
	if err != nil {
		return none, fmt.Errorf("%s %s: settle waits stale: %w", operation, executionID, err)
	}
	// Read AFTER the waits are settled, so the drain list is exactly the
	// open attempts -- the ones an effect may still land for.
	open, err := t.queries.ListUnsettledExecutionAttempts(ctx, gen.ListUnsettledExecutionAttemptsParams{OrganizationID: toUUID(organizationID), ExecutionID: toNullUUID(&executionID)})
	if err != nil {
		return none, fmt.Errorf("%s %s: list drain: %w", operation, executionID, err)
	}
	return store.Supersession{Drain: attemptsFromRows(open), Staled: attemptsFromRows(staled)}, nil
}

// RecordTerminalResult records the four-axis result, once (D11).
//
// Order: the row under its lock; closed admission; no prior result; the
// result's own shape; the receipt's shape; then the receipt's action half
// checked AGAINST THE ROWS -- every attempt settled with a resolved drain
// disposition -- and for a blocked result, that the named attempt is this
// execution's settled blocked one. The schema repeats the last two as a
// composite key and a trigger; the seam checks first so the caller reads a
// reason.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) RecordTerminalResult(ctx context.Context, organizationID, executionID uuid.UUID, result store.TerminalResult, receipt store.FenceReceipt) error {
	const operation = "RecordTerminalResult"
	locked, err := t.queries.LockExecution(ctx, gen.LockExecutionParams{OrganizationID: toUUID(organizationID), ExecutionID: toUUID(executionID)})
	if err != nil {
		return notFound(err, "execution", executionID)
	}
	if !locked.AdmissionClosedAt.Valid {
		return rejectExecution(operation, executionID, store.ReasonAdmissionOpen, "")
	}
	if locked.Status != nil {
		return rejectExecution(operation, executionID, store.ReasonAlreadyTerminal, *locked.Status)
	}
	if validateErr := result.Validate(); validateErr != nil {
		return fmt.Errorf("%s %s: %w", operation, executionID, validateErr)
	}
	if receiptErr := receipt.Validate(); receiptErr != nil {
		return fmt.Errorf("%s %s: %w", operation, executionID, receiptErr)
	}
	undrained, err := t.queries.CountUndrainedExecutionAttempts(ctx, gen.CountUndrainedExecutionAttemptsParams{OrganizationID: toUUID(organizationID), ExecutionID: toNullUUID(&executionID)})
	if err != nil {
		return fmt.Errorf("%s %s: count undrained attempts: %w", operation, executionID, err)
	}
	if undrained != 0 {
		return rejectExecution(operation, executionID, store.ReasonActionsNotDrained,
			fmt.Sprintf("%d attempt(s) unsettled or with unresolved drainage", undrained))
	}
	if result.BlockedToolCallID != nil {
		if blockedErr := t.checkBlockedAttempt(ctx, operation, organizationID, executionID, *result.BlockedToolCallID); blockedErr != nil {
			return blockedErr
		}
	}
	// The same presence rule Validate applied: a blank diagnostic is absent,
	// and is stored as NULL rather than as the blank the validator excused
	// (PR #383 review).
	var errorMessage *string
	if strings.TrimSpace(result.ErrorMessage) != "" {
		errorMessage = &result.ErrorMessage
	}
	status := string(result.Status)
	rows, err := t.queries.RecordExecutionTerminalResult(ctx, gen.RecordExecutionTerminalResultParams{
		Status:                &status,
		CompletionDisposition: (*string)(result.CompletionDisposition),
		CancellationReason:    (*string)(result.CancellationReason),
		FailureClass:          (*string)(result.FailureClass),
		BlockedToolCallID:     toNullUUID(result.BlockedToolCallID),
		ErrorMessage:          errorMessage,
		OrganizationID:        toUUID(organizationID),
		ExecutionID:           toUUID(executionID),
	})
	if err != nil {
		return fmt.Errorf("%s %s: %w", operation, executionID, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: recording the terminal result of execution %s affected %d rows under its lock", store.ErrInvariant, executionID, rows)
	}
	return nil
}

// checkBlockedAttempt is the blocked reference's meaning (D11, D12): a
// settled, blocked attempt of THIS execution.
func (t *tx) checkBlockedAttempt(ctx context.Context, operation string, organizationID, executionID, toolCallID uuid.UUID) error {
	attempt, err := t.GetToolCall(ctx, organizationID, toolCallID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return rejectExecution(operation, executionID, store.ReasonBlockedAttemptInvalid, "no such attempt")
		}
		return err
	}
	switch {
	case attempt.ExecutionID == nil || *attempt.ExecutionID != executionID:
		return rejectExecution(operation, executionID, store.ReasonBlockedAttemptInvalid, "attempt belongs to another execution")
	case attempt.Outcome == nil || *attempt.Outcome != store.ToolOutcomeBlocked:
		return rejectExecution(operation, executionID, store.ReasonBlockedAttemptInvalid, "attempt is not settled blocked")
	}
	return nil
}
