//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"orchestrator/internal/dataplane/migrations"
)

// Migration 000011 adds the LLM call outcome columns and the coherence
// constraints for both call tables. Its behaviour on a POPULATED database
// is the part that cannot be checked by applying it to an empty one, which
// is what every other migration test does.
//
// The claim under test is deliberately narrow: the migration refuses to
// invent an outcome it cannot recover, and refuses incoherent rows
// afterwards. An earlier version backfilled succeeded = true for
// already-completed rows and warned in a comment that the value was not
// evidence -- a warning no query can honour.
const versionBeforeOutcome = 10

// seedCall inserts the organization, principal and call rows a call record
// needs, and returns the call id. Written against v10's schema, so it must
// not name the columns migration 11 adds.
func seedOpenLLMCall(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO organizations (organization_id, slug, display_name)
		VALUES ('11111111-1111-4111-8111-111111111111', 'probe', 'Probe');
		INSERT INTO principal_instances (principal_instance_id, organization_id, kind, model, agent_type)
		VALUES ('22222222-2222-4222-8222-222222222222', '11111111-1111-4111-8111-111111111111',
		        'agent', 'test-model', 'coder');
		INSERT INTO llm_calls (llm_call_id, organization_id, principal_instance_id, provider, model)
		VALUES ('33333333-3333-4333-8333-333333333333', '11111111-1111-4111-8111-111111111111',
		        '22222222-2222-4222-8222-222222222222', 'anthropic', 'test-model');
		INSERT INTO tool_calls (tool_call_id, organization_id, principal_instance_id, tool_name, arguments)
		VALUES ('44444444-4444-4444-8444-444444444444', '11111111-1111-4111-8111-111111111111',
		        '22222222-2222-4222-8222-222222222222', 'shell', '{}'::jsonb);`); err != nil {
		t.Fatalf("seed open calls: %v", err)
	}
}

const (
	llmCallID  = "'33333333-3333-4333-8333-333333333333'"
	toolCallID = "'44444444-4444-4444-8444-444444444444'"
)

// singularID maps a call table to its primary key column.
func singularID(table string) string {
	if table == "llm_calls" {
		return "llm_call_id"
	}
	return "tool_call_id"
}

func openDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestOutcomeMigrationPreservesOpenCalls is the benign path: an in-flight
// call predates the migration, has no outcome to recover, and must survive
// with its openness intact rather than being completed by the backfill.
func TestOutcomeMigrationPreservesOpenCalls(t *testing.T) {
	dsn := disposableDatabaseAt(t, versionBeforeOutcome)
	db := openDB(t, dsn)
	seedOpenLLMCall(t, db)

	if err := migrations.Up(context.Background(), dsn); err != nil {
		t.Fatalf("migrate to head over an open call: %v", err)
	}

	var finished, succeeded sql.NullString
	if err := db.QueryRow(`
		SELECT finished_at::text, succeeded::text FROM llm_calls
		WHERE llm_call_id = '33333333-3333-4333-8333-333333333333'`).Scan(&finished, &succeeded); err != nil {
		t.Fatalf("read call: %v", err)
	}
	if finished.Valid {
		t.Fatalf("an open call was completed by the migration: finished_at = %s", finished.String)
	}
	if succeeded.Valid {
		t.Fatalf("an open call was given an outcome: succeeded = %s", succeeded.String)
	}
}

// TestOutcomeMigrationRefusesToInventAnOutcome is the point of the change.
// A completed call predating the migration has an unrecoverable outcome,
// and the migration must stop rather than record a success that was never
// observed.
func TestOutcomeMigrationRefusesToInventAnOutcome(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabaseAt(t, versionBeforeOutcome)
	db := openDB(t, dsn)
	seedOpenLLMCall(t, db)

	// Complete it under v10, where no outcome column exists to record how.
	if _, err := db.Exec(`
		UPDATE llm_calls SET finished_at = now()
		WHERE llm_call_id = '33333333-3333-4333-8333-333333333333'`); err != nil {
		t.Fatalf("complete call under v10: %v", err)
	}

	err := migrations.Up(ctx, dsn)
	if err == nil {
		t.Fatal("the migration accepted a completed call with no recoverable outcome; it must refuse " +
			"rather than record a success that was never observed")
	}
	// Match the FORMATTED counts, not the message text. golang-migrate
	// echoes the whole migration SOURCE in its error, and the RAISE
	// message lives in that source -- so matching on wording passes
	// whenever the migration fails for any reason at all, including
	// reasons this test is not about. Only the substituted counts prove
	// the pre-flight assertion is what fired.
	if !strings.Contains(err.Error(), "found 1 completed llm_calls and 0 incoherent tool_calls") {
		t.Fatalf("the pre-flight assertion did not fire with the expected counts: %v", err)
	}

	// The failure leaves the recorded version dirty -- golang-migrate marks
	// BEFORE executing -- which is why the message names forcing it back
	// rather than only deleting rows. The first version of that message was
	// mechanically wrong, so the recovery is walked end to end here.
	version, dirty, versionErr := migrations.Version(dsn)
	if versionErr != nil {
		t.Fatalf("read version: %v", versionErr)
	}
	if !dirty {
		t.Fatal("expected the failed migration to leave the version dirty; if that ever changes, the " +
			"recovery instruction in the migration is wrong")
	}
	if version != versionBeforeOutcome+1 {
		t.Fatalf("version = %d, want %d", version, versionBeforeOutcome+1)
	}

	// Re-running without clearing the flag must fail, or forcing would be
	// unnecessary and the instruction overstated.
	if rerun := migrations.Up(ctx, dsn); rerun == nil {
		t.Fatal("a re-run succeeded despite the dirty flag, so the documented recovery is overstated")
	}

	// The documented recovery, end to end.
	if _, delErr := db.Exec(`DELETE FROM llm_calls WHERE llm_call_id = ` + llmCallID); delErr != nil {
		t.Fatalf("delete the offending row: %v", delErr)
	}
	if forceErr := migrations.Force(dsn, versionBeforeOutcome); forceErr != nil {
		t.Fatalf("force back to v%d: %v", versionBeforeOutcome, forceErr)
	}
	if upErr := migrations.Up(ctx, dsn); upErr != nil {
		t.Fatalf("recovery re-run failed, so the migration's own instructions do not work: %v", upErr)
	}

	version, dirty, versionErr = migrations.Version(dsn)
	if versionErr != nil {
		t.Fatalf("read version after recovery: %v", versionErr)
	}
	if dirty || version != versionBeforeOutcome+1 {
		t.Fatalf("after recovery version = %d dirty = %v, want %d and clean",
			version, dirty, versionBeforeOutcome+1)
	}
}

// TestOutcomeMigrationRefusesIncoherentToolCalls is the tool-call half of
// the same refusal. v10 permits a completed tool call carrying BOTH a
// success and an error message, since only the finished/succeeded pairing
// was constrained then -- so migration 11 must refuse rather than adopt it.
func TestOutcomeMigrationRefusesIncoherentToolCalls(t *testing.T) {
	dsn := disposableDatabaseAt(t, versionBeforeOutcome)
	db := openDB(t, dsn)
	seedOpenLLMCall(t, db)

	if _, err := db.Exec(`UPDATE tool_calls SET finished_at = now(), succeeded = true,
		error_message = 'contradictory' WHERE tool_call_id = ` + toolCallID); err != nil {
		t.Fatalf("write an incoherent tool call under v10: %v", err)
	}

	err := migrations.Up(context.Background(), dsn)
	if err == nil {
		t.Fatal("the migration adopted a tool call that is both successful and errored")
	}
	// Formatted counts again, for the same reason: the bare phrase appears
	// in the migration source that golang-migrate echoes back.
	if !strings.Contains(err.Error(), "found 0 completed llm_calls and 1 incoherent tool_calls") {
		t.Fatalf("the pre-flight assertion did not catch the tool call -- the ALTER TABLE did, which is a "+
			"different guarantee and a worse error: %v", err)
	}
}

// TestOutcomeConstraintsRejectIncoherentRows covers the backstop on BOTH
// call tables. The seam checks these too, but the seam is not the only way
// a row is written.
//
// An earlier version exercised only llm_calls, so removing
// tool_calls_outcome_coherence_check left the suite green -- a constraint
// with no test that could fail for it.
func TestOutcomeConstraintsRejectIncoherentRows(t *testing.T) {
	dsn := disposableDatabase(t) // migrated to head
	db := openDB(t, dsn)
	seedOpenLLMCall(t, db)

	tables := []struct{ table, id string }{
		{"llm_calls", llmCallID},
		{"tool_calls", toolCallID},
	}
	incoherent := []struct{ name, update string }{
		{"success carrying an error", `SET finished_at = now(), succeeded = true, error_message = 'boom'`},
		{"failure with no diagnostic", `SET finished_at = now(), succeeded = false, error_message = NULL`},
		{"failure with a blank diagnostic", `SET finished_at = now(), succeeded = false, error_message = '   '`},
		{"open call carrying an error", `SET error_message = 'boom'`},
		{"finished with no outcome", `SET finished_at = now()`},
		{"outcome with no finish", `SET succeeded = true`},
	}
	coherent := []string{
		`SET finished_at = now(), succeeded = true`,
		`SET finished_at = now(), succeeded = false, error_message = 'provider timeout'`,
	}

	for _, target := range tables {
		t.Run(target.table, func(t *testing.T) {
			// Reset before every case. The cases share one row, and under
			// working constraints each UPDATE fails so the row stays
			// pristine BY ACCIDENT -- with a guard missing, state leaks
			// forward and later cases stop testing what they name.
			reset := func(t *testing.T) {
				t.Helper()
				if _, err := db.Exec(`UPDATE ` + target.table + ` SET finished_at = NULL, succeeded = NULL,
					error_message = NULL WHERE ` + singularID(target.table) + ` = ` + target.id); err != nil {
					t.Fatalf("reset: %v", err)
				}
			}

			for _, testCase := range incoherent {
				t.Run(testCase.name, func(t *testing.T) {
					reset(t)
					_, err := db.Exec(`UPDATE ` + target.table + ` ` + testCase.update +
						` WHERE ` + singularID(target.table) + ` = ` + target.id)
					if err == nil {
						t.Fatal("the database accepted an incoherent outcome")
					}
					if !strings.Contains(err.Error(), "check constraint") {
						t.Fatalf("rejected for the wrong reason: %v", err)
					}
				})
			}

			// The coherent completions must still be accepted, or the
			// constraints would be satisfied by rejecting everything.
			for _, update := range coherent {
				reset(t)
				if _, err := db.Exec(`UPDATE ` + target.table + ` ` + update +
					` WHERE ` + singularID(target.table) + ` = ` + target.id); err != nil {
					t.Fatalf("a coherent completion was rejected (%s): %v", update, err)
				}
			}
		})
	}
}
