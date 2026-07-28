//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// Truncation is the one destructive operation in the phase, and until now
// its predicates had been read carefully and executed never. These tests
// run the generated statements against a real database in dependency order.
//
// Every table is seeded in EVERY state that applies to it — deletable,
// pinned, open, referenced, and open-and-referenced — because a suite of
// only deletable rows passes against a delete with no guard clauses at all.

// horizon is the cutoff. Everything seeded is older than it except where a
// case deliberately says otherwise.
func horizon() time.Time { return time.Now().Add(-1 * time.Hour) }

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

// seedLLMCall writes a call directly, so a test can produce states the seam
// would refuse to create — an open call older than the horizon, for one.
func seedLLMCall(t *testing.T, f *fixture, org uuid.UUID, finished bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	principal := f.principalFor(org)
	var finishedAt any
	if finished {
		finishedAt = horizon().Add(-30 * time.Minute)
	}
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO llm_calls (llm_call_id, organization_id, principal_instance_id, provider, model,
			started_at, finished_at, succeeded)
		VALUES ($1, $2, $3, 'anthropic', 'test', $4, $5, CASE WHEN $5::timestamptz IS NULL THEN NULL ELSE true END)`,
		id, org, principal, horizon().Add(-2*time.Hour), finishedAt); err != nil {
		t.Fatalf("seed llm call: %v", err)
	}
	return id
}

// seedToolCall optionally claims an LLM call. The provenance key requires
// the same principal and the same lineage, and both are left null here so
// the generated lineage_key matches.
func seedToolCall(t *testing.T, f *fixture, org uuid.UUID, finished bool, llmCall *uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	principal := f.principalFor(org)
	var finishedAt any
	if finished {
		finishedAt = horizon().Add(-30 * time.Minute)
	}
	var claims any
	if llmCall != nil {
		claims = *llmCall
	}
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO tool_calls (tool_call_id, organization_id, principal_instance_id, llm_call_id,
			tool_name, arguments, started_at, finished_at, succeeded)
		VALUES ($1, $2, $3, $4, 'shell', '{}'::jsonb, $5, $6,
			CASE WHEN $6::timestamptz IS NULL THEN NULL ELSE true END)`,
		id, org, principal, claims, horizon().Add(-2*time.Hour), finishedAt); err != nil {
		t.Fatalf("seed tool call: %v", err)
	}
	return id
}

func seedAuditArtifact(t *testing.T, f *fixture, org uuid.UUID, producedBy *uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var tool any
	if producedBy != nil {
		tool = *producedBy
	}
	author := f.principalFor(org)
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO audit_artifacts (artifact_id, organization_id, artifact_type, artifact_category,
			scope_type, scope_organization_id, author_instance_id, produced_by_tool_call_id,
			schema_version, summary, payload, payload_digest, created_at)
		VALUES ($1, $2, 'test_event', 'audit', 'organization', $2, $3, $4, 1, 's', '{}'::jsonb,
			repeat('a', 64), $5)`,
		id, org, author, tool, horizon().Add(-2*time.Hour)); err != nil {
		t.Fatalf("seed audit artifact: %v", err)
	}
	return id
}

func seedEvents(t *testing.T, f *fixture, org uuid.UUID) {
	t.Helper()
	principal := f.principalFor(org)
	at := horizon().Add(-2 * time.Hour)

	// One statement per Exec: pgx prepares, and a prepared statement cannot
	// carry multiple commands.
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO metric_events (metric_event_id, organization_id, principal_instance_id, metric_name,
			value, recorded_at) VALUES (gen_random_uuid(), $1, $2, 'tokens', 1.0, $3)`,
		org, principal, at); err != nil {
		t.Fatalf("seed metric event: %v", err)
	}
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO audit_events (audit_event_id, organization_id, principal_instance_id, event_type,
			occurred_at) VALUES (gen_random_uuid(), $1, $2, 'agent.started', $3)`,
		org, principal, at); err != nil {
		t.Fatalf("seed audit event: %v", err)
	}
}

// TestTruncationReconcilesAndRespectsRetention is the core behavioural
// test: every candidate lands in exactly one bucket, and the identity
// candidates = deleted + pinned + open + referenced holds per table.
//
// Asserting the IDENTITY rather than the individual counts matters: four
// numbers that each look plausible can still describe no consistent set of
// rows, which is exactly what precedence assignment exists to prevent.
func TestTruncationReconcilesAndRespectsRetention(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	queries := gen.New(f.pool)
	org := f.organizationID
	before := pgtype.Timestamptz{Time: horizon(), Valid: true}

	// --- LLM calls: deletable, open, referenced, open-and-referenced ----
	deletableLLM := seedLLMCall(t, f, org, true)
	openLLM := seedLLMCall(t, f, org, false)
	// Referenced by an OPEN tool call, which survives -- so the LLM call
	// stays referenced through the whole pass. Referencing it from a
	// DELETABLE tool call would not test retention at all: dependency order
	// removes the referrer first and frees this row, which is the cascade
	// asserted separately below.
	referencedLLM := seedLLMCall(t, f, org, true)
	seedToolCall(t, f, org, false, &referencedLLM)

	// Deletable, but referenced by a tool call that is itself deleted in
	// this pass -- the cascade.
	cascadedLLM := seedLLMCall(t, f, org, true)
	cascadedTool := seedToolCall(t, f, org, true, &cascadedLLM)

	// --- tool calls: deletable, open, referenced ------------------------
	deletableTool := seedToolCall(t, f, org, true, nil)
	openTool := seedToolCall(t, f, org, false, nil)

	// Referenced by a MANAGEMENT artifact, which is never truncated -- so
	// this tool call is retained permanently. An Audit artifact would not
	// serve: it is itself deletable and goes first, freeing the tool call.
	referencedTool := seedToolCall(t, f, org, true, nil)
	if _, err := f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
		Payload:              json.RawMessage(`{"title":"cites a tool call"}`),
		ProducedByToolCallID: &referencedTool,
		Type:                 testType,
		Summary:              "durable provenance",
		Scope:                f.scope(),
		OrganizationID:       org,
		UserID:               f.userID,
		AuthorInstanceID:     f.author,
	}); err != nil {
		t.Fatalf("create citing management artifact: %v", err)
	}

	// --- audit artifacts: deletable and pinned --------------------------
	deletableArtifact := seedAuditArtifact(t, f, org, nil)
	pinnedArtifact := seedAuditArtifact(t, f, org, nil)
	pinner := acceptedOriginal(t, f, `{"title":"pinner"}`)
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO retention_pins (retention_pin_id, organization_id, pinned_by_artifact_id,
			pinned_audit_artifact_id, pinned_digest)
		VALUES (gen_random_uuid(), $1, $2, $3, repeat('b', 64))`,
		org, pinner.ArtifactID, pinnedArtifact); err != nil {
		t.Fatalf("pin: %v", err)
	}

	seedEvents(t, f, org)

	// --- run in dependency order ----------------------------------------
	auditEventCandidates, err := queries.CountAuditEventCandidates(ctx,
		gen.CountAuditEventCandidatesParams{OrganizationID: pgUUID(org), Before: before})
	if err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	auditEventsDeleted, err := queries.TruncateAuditEvents(ctx,
		gen.TruncateAuditEventsParams{OrganizationID: pgUUID(org), Before: before})
	if err != nil {
		t.Fatalf("truncate audit events: %v", err)
	}
	if auditEventsDeleted != auditEventCandidates {
		t.Errorf("audit events: %d candidates but %d deleted; nothing retains an audit event",
			auditEventCandidates, auditEventsDeleted)
	}

	metricCandidates, err := queries.CountMetricEventCandidates(ctx,
		gen.CountMetricEventCandidatesParams{OrganizationID: pgUUID(org), Before: before})
	if err != nil {
		t.Fatalf("count metric events: %v", err)
	}
	metricsDeleted, err := queries.TruncateMetricEvents(ctx,
		gen.TruncateMetricEventsParams{OrganizationID: pgUUID(org), Before: before})
	if err != nil {
		t.Fatalf("truncate metric events: %v", err)
	}
	if metricsDeleted != metricCandidates {
		t.Errorf("metric events: %d candidates but %d deleted", metricCandidates, metricsDeleted)
	}

	artifactCounts, err := queries.CountAuditArtifactTruncation(ctx,
		gen.CountAuditArtifactTruncationParams{OrganizationID: pgUUID(org), Before: before})
	if err != nil {
		t.Fatalf("count audit artifacts: %v", err)
	}
	artifactsDeleted, err := queries.TruncateAuditArtifacts(ctx,
		gen.TruncateAuditArtifactsParams{OrganizationID: pgUUID(org), Before: before})
	if err != nil {
		t.Fatalf("truncate audit artifacts: %v", err)
	}
	if got := artifactsDeleted + artifactCounts.RetainedPinned; got != artifactCounts.Candidates {
		t.Errorf("audit artifacts do not reconcile: %d deleted + %d pinned = %d, want %d candidates",
			artifactsDeleted, artifactCounts.RetainedPinned, got, artifactCounts.Candidates)
	}
	if artifactCounts.RetainedPinned == 0 {
		t.Error("no artifact was retained as pinned, so the pin guard was never exercised")
	}

	toolCounts, err := queries.CountToolCallTruncation(ctx,
		gen.CountToolCallTruncationParams{OrganizationID: pgUUID(org), Before: before})
	if err != nil {
		t.Fatalf("count tool calls: %v", err)
	}
	toolsDeleted, err := queries.TruncateToolCalls(ctx,
		gen.TruncateToolCallsParams{OrganizationID: pgUUID(org), Before: before})
	if err != nil {
		t.Fatalf("truncate tool calls: %v", err)
	}
	if got := toolsDeleted + toolCounts.RetainedOpen + toolCounts.RetainedReferenced; got != toolCounts.Candidates {
		t.Errorf("tool calls do not reconcile: %d deleted + %d open + %d referenced = %d, want %d",
			toolsDeleted, toolCounts.RetainedOpen, toolCounts.RetainedReferenced, got, toolCounts.Candidates)
	}

	llmCounts, err := queries.CountLLMCallTruncation(ctx,
		gen.CountLLMCallTruncationParams{OrganizationID: pgUUID(org), Before: before})
	if err != nil {
		t.Fatalf("count llm calls: %v", err)
	}
	llmDeleted, err := queries.TruncateLLMCalls(ctx,
		gen.TruncateLLMCallsParams{OrganizationID: pgUUID(org), Before: before})
	if err != nil {
		t.Fatalf("truncate llm calls: %v", err)
	}
	if got := llmDeleted + llmCounts.RetainedOpen + llmCounts.RetainedReferenced; got != llmCounts.Candidates {
		t.Errorf("llm calls do not reconcile: %d deleted + %d open + %d referenced = %d, want %d",
			llmDeleted, llmCounts.RetainedOpen, llmCounts.RetainedReferenced, got, llmCounts.Candidates)
	}

	// --- the rows that had to survive, individually ---------------------
	if !rowExists(t, f, "llm_calls", "llm_call_id", openLLM) {
		t.Error("an open llm call was deleted; an unfinished call is in-progress work, not history")
	}
	if !rowExists(t, f, "llm_calls", "llm_call_id", referencedLLM) {
		t.Error("an llm call referenced by a surviving (open) tool call was deleted")
	}
	if !rowExists(t, f, "tool_calls", "tool_call_id", openTool) {
		t.Error("an open tool call was deleted")
	}
	if !rowExists(t, f, "audit_artifacts", "artifact_id", pinnedArtifact) {
		t.Error("a PINNED audit artifact was deleted; the retention pin is the whole mechanism")
	}
	if !rowExists(t, f, "tool_calls", "tool_call_id", referencedTool) {
		t.Error("a tool call referenced by an audit artifact was deleted")
	}

	// --- and the rows that had to go ------------------------------------
	if rowExists(t, f, "llm_calls", "llm_call_id", deletableLLM) {
		t.Error("a completed, unreferenced llm call past the horizon survived")
	}
	if rowExists(t, f, "tool_calls", "tool_call_id", deletableTool) {
		t.Error("a completed, unreferenced tool call past the horizon survived")
	}
	if rowExists(t, f, "audit_artifacts", "artifact_id", deletableArtifact) {
		t.Error("an unpinned audit artifact past the horizon survived")
	}

	// The cascade, asserted as the DESIGNED behaviour it is. Deletion runs
	// in dependency order precisely so that a tool call freed by removing
	// its artifact, and an LLM call freed by removing that tool call, both
	// go in the same pass rather than needing one run per level.
	if rowExists(t, f, "tool_calls", "tool_call_id", cascadedTool) {
		t.Error("a deletable tool call survived")
	}
	if rowExists(t, f, "llm_calls", "llm_call_id", cascadedLLM) {
		t.Error("an llm call whose only referrer was deleted earlier in this same pass survived; " +
			"dependency order exists so one pass frees and removes the whole chain")
	}
}

// TestTruncationIsOrganizationScoped is the multi-tenant boundary on the
// destructive path. A single-organization test passes against statements
// that omit the column entirely.
func TestTruncationIsOrganizationScoped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	queries := gen.New(f.pool)
	before := pgtype.Timestamptz{Time: horizon(), Valid: true}

	mine := seedLLMCall(t, f, f.organizationID, true)
	theirs := seedLLMCall(t, f, f.otherOrgID, true)
	seedEvents(t, f, f.organizationID)
	seedEvents(t, f, f.otherOrgID)
	theirArtifact := seedAuditArtifact(t, f, f.otherOrgID, nil)

	for _, truncate := range []func() error{
		func() error {
			_, err := queries.TruncateAuditEvents(ctx,
				gen.TruncateAuditEventsParams{OrganizationID: pgUUID(f.organizationID), Before: before})
			return err
		},
		func() error {
			_, err := queries.TruncateMetricEvents(ctx,
				gen.TruncateMetricEventsParams{OrganizationID: pgUUID(f.organizationID), Before: before})
			return err
		},
		func() error {
			_, err := queries.TruncateAuditArtifacts(ctx,
				gen.TruncateAuditArtifactsParams{OrganizationID: pgUUID(f.organizationID), Before: before})
			return err
		},
		func() error {
			_, err := queries.TruncateToolCalls(ctx,
				gen.TruncateToolCallsParams{OrganizationID: pgUUID(f.organizationID), Before: before})
			return err
		},
		func() error {
			_, err := queries.TruncateLLMCalls(ctx,
				gen.TruncateLLMCallsParams{OrganizationID: pgUUID(f.organizationID), Before: before})
			return err
		},
	} {
		if err := truncate(); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}

	if rowExists(t, f, "llm_calls", "llm_call_id", mine) {
		t.Error("this organization's deletable call survived its own truncation")
	}
	if !rowExists(t, f, "llm_calls", "llm_call_id", theirs) {
		t.Error("another organization's call was deleted by this organization's truncation")
	}
	if !rowExists(t, f, "audit_artifacts", "artifact_id", theirArtifact) {
		t.Error("another organization's audit artifact was deleted")
	}
	for _, table := range []string{"metric_events", "audit_events"} {
		var remaining int
		if err := f.pool.QueryRow(ctx,
			`SELECT count(*) FROM `+table+` WHERE organization_id = $1`, f.otherOrgID).Scan(&remaining); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if remaining == 0 {
			t.Errorf("another organization's %s were deleted", table)
		}
	}
}

func rowExists(t *testing.T, f *fixture, table, column string, id uuid.UUID) bool {
	t.Helper()
	var exists bool
	if err := f.pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM `+table+` WHERE `+column+` = $1)`, id).Scan(&exists); err != nil {
		t.Fatalf("check %s: %v", table, err)
	}
	return exists
}
