//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

	// THE PRECEDENCE COLLISION: open AND referenced at once. Without a row
	// in this state, a count that put such a row in both buckets would
	// still reconcile, and the precedence rule would be untested.
	openReferencedLLM := seedLLMCall(t, f, org, false)
	seedToolCall(t, f, org, false, &openReferencedLLM)

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

	// The tool-call collision: open AND cited by a durable artifact.
	openReferencedTool := seedToolCall(t, f, org, false, nil)
	if _, err := f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
		Payload:              json.RawMessage(`{"title":"cites an open tool call"}`),
		ProducedByToolCallID: &openReferencedTool,
		Type:                 testType,
		Summary:              "durable provenance for an open call",
		Scope:                f.scope(),
		OrganizationID:       org,
		UserID:               f.userID,
		AuthorInstanceID:     f.author,
	}); err != nil {
		t.Fatalf("create second citing artifact: %v", err)
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

	// --- run in dependency order, in ONE REPEATABLE READ transaction ----
	//
	// The isolation level is part of the contract, not an implementation
	// detail. At READ COMMITTED every statement takes a fresh snapshot, so
	// the counts and the deletes would see four different instants and a
	// row protected when the pass began could still be removed. Running
	// these on the pool -- as an earlier version of this test did -- proves
	// the predicates and not the operation.
	//
	// This suite is single-threaded, so it would still pass at READ
	// COMMITTED: what it pins is that the operation RUNS in one
	// transaction, not that the isolation level is doing work. The
	// behavioural proof is the store-level concurrency test, which drives
	// two truncations at once and exercises the 40001 retry.
	tx, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatalf("begin repeatable read: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := gen.New(f.pool).WithTx(tx)
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

	// --- exact bucket values, not only the identity ---------------------
	//
	// The identity alone would accept a count that put an open-and-
	// referenced row in both buckets IF no such row existed. One does now,
	// and precedence says it belongs to open only.
	if llmCounts.RetainedOpen != 2 || llmCounts.RetainedReferenced != 1 {
		t.Errorf("llm buckets = %d open, %d referenced; want 2 and 1. An open-and-referenced call belongs "+
			"to OPEN alone: being referenced does not make a call that is still running less of an "+
			"operational problem.", llmCounts.RetainedOpen, llmCounts.RetainedReferenced)
	}
	if toolCounts.RetainedOpen != 4 || toolCounts.RetainedReferenced != 1 {
		t.Errorf("tool buckets = %d open, %d referenced; want 4 and 1",
			toolCounts.RetainedOpen, toolCounts.RetainedReferenced)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit truncation: %v", err)
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
	if !rowExists(t, f, "llm_calls", "llm_call_id", openReferencedLLM) {
		t.Error("an open-and-referenced llm call was deleted")
	}
	if !rowExists(t, f, "tool_calls", "tool_call_id", openReferencedTool) {
		t.Error("an open-and-referenced tool call was deleted")
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
// destructive path.
//
// It exercises the COUNTS as well as the deletes: an earlier version ran
// only the deletes, so removing organization_id from any candidate-count
// query stayed green — and a count is what an operator reads before
// deciding to run the delete. It also seeds every table in both
// organizations, since a check for tool-call isolation that seeds no tool
// calls asserts nothing.
func TestTruncationIsOrganizationScoped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := pgtype.Timestamptz{Time: horizon(), Valid: true}

	// Seed both organizations identically across every table.
	seeded := map[uuid.UUID]struct{ llm, tool, artifact uuid.UUID }{}
	for _, org := range []uuid.UUID{f.organizationID, f.otherOrgID} {
		llm := seedLLMCall(t, f, org, true)
		tool := seedToolCall(t, f, org, true, nil)
		artifact := seedAuditArtifact(t, f, org, nil)
		seedEvents(t, f, org)
		seeded[org] = struct{ llm, tool, artifact uuid.UUID }{llm, tool, artifact}
	}

	mine := pgUUID(f.organizationID)
	tx, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := gen.New(f.pool).WithTx(tx)

	// Every count must see ONE row -- this organization's -- not two.
	// Seeding both organizations identically is what makes that assertion
	// discriminate: an unscoped count returns 2.
	auditEvents, err := queries.CountAuditEventCandidates(ctx,
		gen.CountAuditEventCandidatesParams{OrganizationID: mine, Before: before})
	if err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	metricEvents, err := queries.CountMetricEventCandidates(ctx,
		gen.CountMetricEventCandidatesParams{OrganizationID: mine, Before: before})
	if err != nil {
		t.Fatalf("count metric events: %v", err)
	}
	artifacts, err := queries.CountAuditArtifactTruncation(ctx,
		gen.CountAuditArtifactTruncationParams{OrganizationID: mine, Before: before})
	if err != nil {
		t.Fatalf("count audit artifacts: %v", err)
	}
	tools, err := queries.CountToolCallTruncation(ctx,
		gen.CountToolCallTruncationParams{OrganizationID: mine, Before: before})
	if err != nil {
		t.Fatalf("count tool calls: %v", err)
	}
	calls, err := queries.CountLLMCallTruncation(ctx,
		gen.CountLLMCallTruncationParams{OrganizationID: mine, Before: before})
	if err != nil {
		t.Fatalf("count llm calls: %v", err)
	}

	for name, got := range map[string]int64{
		"audit events":    auditEvents,
		"metric events":   metricEvents,
		"audit artifacts": artifacts.Candidates,
		"tool calls":      tools.Candidates,
		"llm calls":       calls.Candidates,
	} {
		if got != 1 {
			t.Errorf("%s: counted %d candidates, want 1. Both organizations were seeded identically, so a "+
				"count that returns 2 is not organization-scoped -- and a count is what an operator reads "+
				"before deciding to run the delete.", name, got)
		}
	}

	// Then the deletes.
	for name, del := range map[string]func() (int64, error){
		"audit events": func() (int64, error) {
			return queries.TruncateAuditEvents(ctx, gen.TruncateAuditEventsParams{OrganizationID: mine, Before: before})
		},
		"metric events": func() (int64, error) {
			return queries.TruncateMetricEvents(ctx, gen.TruncateMetricEventsParams{OrganizationID: mine, Before: before})
		},
		"audit artifacts": func() (int64, error) {
			return queries.TruncateAuditArtifacts(ctx, gen.TruncateAuditArtifactsParams{OrganizationID: mine, Before: before})
		},
		"tool calls": func() (int64, error) {
			return queries.TruncateToolCalls(ctx, gen.TruncateToolCallsParams{OrganizationID: mine, Before: before})
		},
		"llm calls": func() (int64, error) {
			return queries.TruncateLLMCalls(ctx, gen.TruncateLLMCallsParams{OrganizationID: mine, Before: before})
		},
	} {
		deleted, err := del()
		if err != nil {
			t.Fatalf("truncate %s: %v", name, err)
		}
		if deleted != 1 {
			t.Errorf("%s: deleted %d rows, want exactly 1 (this organization's)", name, deleted)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Mine gone, theirs untouched, on every table.
	ours, theirs := seeded[f.organizationID], seeded[f.otherOrgID]
	for _, gone := range []struct {
		table, column string
		id            uuid.UUID
	}{
		{"llm_calls", "llm_call_id", ours.llm},
		{"tool_calls", "tool_call_id", ours.tool},
		{"audit_artifacts", "artifact_id", ours.artifact},
	} {
		if rowExists(t, f, gone.table, gone.column, gone.id) {
			t.Errorf("this organization's %s survived its own truncation", gone.table)
		}
	}
	for _, kept := range []struct {
		table, column string
		id            uuid.UUID
	}{
		{"llm_calls", "llm_call_id", theirs.llm},
		{"tool_calls", "tool_call_id", theirs.tool},
		{"audit_artifacts", "artifact_id", theirs.artifact},
	} {
		if !rowExists(t, f, kept.table, kept.column, kept.id) {
			t.Errorf("another organization's %s was deleted by this organization's truncation", kept.table)
		}
	}
	for _, table := range []string{"metric_events", "audit_events"} {
		var remaining int
		if err := f.pool.QueryRow(ctx,
			`SELECT count(*) FROM `+table+` WHERE organization_id = $1`, f.otherOrgID).Scan(&remaining); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if remaining != 1 {
			t.Errorf("another organization has %d %s remaining, want 1", remaining, table)
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
