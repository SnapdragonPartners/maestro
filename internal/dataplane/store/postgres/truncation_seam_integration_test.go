//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
)

// Truncation through the seam.
//
// The statement-level suite (truncation_integration_test.go) proves the
// predicates retain what they must. These prove the ORCHESTRATION: the
// dependency order and accounting a caller actually reaches, the isolation
// the pass insists on, and what happens when two passes collide.

// seedAuditEvents writes n audit events past the horizon and returns their
// identifiers in insertion order.
func seedAuditEvents(t *testing.T, f *fixture, org uuid.UUID, n int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		id := uuid.New()
		if _, err := f.pool.Exec(context.Background(), `
			INSERT INTO audit_events (audit_event_id, organization_id, principal_instance_id, event_type,
				occurred_at) VALUES ($1, $2, $3, 'agent.started', $4)`,
			id, org, f.principalFor(org), horizon().Add(-2*time.Hour)); err != nil {
			t.Fatalf("seed audit event: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

// TestTruncationThroughTheSeamReportsEveryTable covers the pass a caller
// gets: every table accounted for, each bucket reconciling, and the rows
// actually gone.
//
// The count is asserted as well as the membership, so a table added to the
// pass without a row here is a failure rather than a silent omission --
// which is how binary_attachments arrived in item 6.
func TestTruncationThroughTheSeamReportsEveryTable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	org := f.organizationID

	deletableLLM := seedLLMCall(t, f, org, true)
	openLLM := seedLLMCall(t, f, org, false)
	deletableTool := seedToolCall(t, f, org, true, nil)
	deletableArtifact := seedAuditArtifact(t, f, org, nil)
	seedEvents(t, f, org)

	result, err := f.store.TruncateAuditBefore(ctx, org, horizon())
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}

	wanted := []string{
		store.TableAuditEvents, store.TableMetricEvents, store.TableAuditArtifacts,
		store.TableAttachments, store.TableToolCalls, store.TableLLMCalls,
	}
	if len(result.PerTable) != len(wanted) {
		t.Errorf("result covers %d tables, want %d", len(result.PerTable), len(wanted))
	}
	for _, table := range wanted {
		contribution, reported := result.PerTable[table]
		if !reported {
			t.Errorf("no contribution reported for %s", table)
			continue
		}
		if !contribution.Reconciles() {
			t.Errorf("%s does not reconcile: %+v", table, contribution)
		}
	}

	// The open call is counted and kept; everything else past the horizon
	// is gone. Both directions matter: a pass that deleted nothing would
	// also "retain" the open call.
	if got := result.PerTable[store.TableLLMCalls]; got.RetainedOpen != 1 || got.Deleted != 1 {
		t.Errorf("llm calls: %+v, want one deleted and one retained open", got)
	}
	if rowExists(t, f, "llm_calls", "llm_call_id", deletableLLM) {
		t.Error("a completed llm call past the horizon survived")
	}
	if !rowExists(t, f, "llm_calls", "llm_call_id", openLLM) {
		t.Error("an OPEN llm call was deleted; an in-progress record is exactly what must not be destroyed")
	}
	if rowExists(t, f, "tool_calls", "tool_call_id", deletableTool) {
		t.Error("a completed tool call past the horizon survived")
	}
	if rowExists(t, f, "audit_artifacts", "artifact_id", deletableArtifact) {
		t.Error("an unpinned audit artifact past the horizon survived")
	}
}

// TestTruncationNeedsAnExplicitHorizon: there is no "delete all", and the
// zero time is the shape that mistake would take.
func TestTruncationNeedsAnExplicitHorizon(t *testing.T) {
	f := newFixture(t)
	_, err := f.store.TruncateAuditBefore(context.Background(), f.organizationID, time.Time{})
	requireRejection(t, err, "explicit horizon")
}

// auditTruncator is the pass as the IMPLEMENTATION carries it.
//
// The seam deliberately does not offer truncation on Tx: WithTx opens at
// the pool default and there is no way to ask it for anything else, so a
// method that necessarily refuses there does not belong on the interface.
// The concrete transaction type still has it — that is how Store runs the
// pass — and this local interface reaches it to prove what happens if some
// future code inside a transaction ever does.
type auditTruncator interface {
	TruncateAuditBefore(ctx context.Context, organizationID uuid.UUID, before time.Time) (store.TruncationResult, error)
}

// TestTruncationRefusesReadCommitted is design D7's guard.
//
// A pass reached through a caller's own transaction inherits that
// transaction's isolation. WithTx opens at the pool default, READ
// COMMITTED, where every STATEMENT takes a fresh snapshot — so the five
// retention guards would be evaluated against five instants and a row
// protected when the pass began could still be deleted. Nothing about the
// call site would say so, which is why the pass asks the server.
func TestTruncationRefusesReadCommitted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	seedAuditEvents(t, f, f.organizationID, 1)

	err := f.store.WithTx(ctx, func(handle store.Tx) error {
		pass, reachable := handle.(auditTruncator)
		if !reachable {
			t.Fatal("the transaction implementation no longer carries a truncation pass at all")
		}
		_, passErr := pass.TruncateAuditBefore(ctx, f.organizationID, horizon())
		return passErr
	})
	if err == nil {
		t.Fatal("a truncation pass ran at READ COMMITTED")
	}
	if !errors.Is(err, store.ErrInvariant) {
		t.Errorf("error %v is not an ErrInvariant", err)
	}

	// Positive control: the same pass through Store, which opens its own
	// REPEATABLE READ transaction, succeeds. Without this, a guard that
	// refused everything would look identical.
	if _, err := f.store.TruncateAuditBefore(ctx, f.organizationID, horizon()); err != nil {
		t.Fatalf("the Store entry point was refused too: %v", err)
	}
}

// TestConcurrentTruncationRetriesOnItsOwnSnapshot is the live half of the
// serialization contract.
//
// Merely launching two truncations at once does NOT reliably produce a
// conflict — the naive version of this test is flaky when it fails and
// vacuous when it passes. So the collision is built deterministically:
//
//  1. three audit events are seeded past the horizon;
//  2. a competing transaction DELETES one and holds it uncommitted;
//  3. the pass starts, takes its snapshot (seeing all three), and BLOCKS on
//     the locked row — waited for in pg_stat_activity, not slept on;
//  4. the competitor commits, and Postgres raises 40001 on the blocked
//     delete because a row it wanted was changed after its snapshot.
//
// The evidence that a retry happened is the reported candidate count. The
// first attempt's snapshot saw three; the result reports TWO, so the pass
// that produced it ran on a snapshot taken after the competitor committed.
// A run with no conflict would report three.
func TestConcurrentTruncationRetriesOnItsOwnSnapshot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	events := seedAuditEvents(t, f, f.organizationID, 3)

	competitor, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin competitor: %v", err)
	}
	defer func() { _ = competitor.Rollback(ctx) }()
	if _, err := competitor.Exec(ctx,
		`DELETE FROM audit_events WHERE audit_event_id = $1`, events[0]); err != nil {
		t.Fatalf("competitor delete: %v", err)
	}

	type outcome struct {
		result store.TruncationResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, passErr := f.store.TruncateAuditBefore(ctx, f.organizationID, horizon())
		done <- outcome{result: result, err: passErr}
	}()

	waitForLockWait(t, f)
	if err := competitor.Commit(ctx); err != nil {
		t.Fatalf("competitor commit: %v", err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("truncation did not survive the conflict: %v", got.err)
		}
		events := got.result.PerTable[store.TableAuditEvents]
		if !events.Reconciles() {
			t.Errorf("buckets do not reconcile after a retry: %+v", events)
		}
		if events.Candidates != 2 || events.Deleted != 2 {
			t.Errorf("reported %d candidates and %d deleted, want 2 and 2; three were seeded and the "+
				"competitor removed one, so anything else means the result came from the snapshot that "+
				"lost the race", events.Candidates, events.Deleted)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("truncation never returned after the competitor committed")
	}

	var remaining int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE organization_id = $1`, f.organizationID).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 0 {
		t.Errorf("%d audit events survived both passes", remaining)
	}
}

// waitForLockWait blocks until a backend on this database is waiting on a
// lock, which is the barrier: it means the truncation has taken its
// snapshot and reached the row the competitor holds.
//
// Polling pg_stat_activity rather than sleeping, because a sleep long
// enough to be reliable is also long enough to hide the race it was meant
// to create.
// The deadline governs the CALL, not merely the iteration.
//
// An earlier version wrapped an unbounded `context.Background()` query in a
// 30-second loop. That bounds how many times the query is retried, which is not
// the failure mode: one query that never returns never reaches the next deadline
// check, so the loop's bound cannot fire. This is called while a writer is
// deliberately stalled holding a lock, so a hang here deadlocks the test until
// the package timeout.
func waitForLockWait(t *testing.T, f *fixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), barrierTimeout)
	defer cancel()

	for ctx.Err() == nil {
		var waiting int
		if err := f.pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			// Attributed by the ERROR, not by ambient deadline state.
			//
			// A deadline reached mid-query is this helper's own timeout rather
			// than a database fault, and reporting it as one would send the next
			// reader looking at Postgres. But asking `ctx.Err() != nil` here
			// answers a different question: it is true for ANY failure that
			// happens to coincide with the deadline expiring, so a genuine pool
			// or database fault arriving in that same instant would be swallowed
			// and reported below as "no backend ever blocked on a lock" -- the
			// one message guaranteed to send someone looking in the wrong place.
			// The window is narrow; the cost of landing in it is a debugging
			// session against a healthy database.
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no backend blocked on a lock within %s, so the collision this test depends on never "+
		"happened", barrierTimeout)
}
