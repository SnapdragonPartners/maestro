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
		        '22222222-2222-4222-8222-222222222222', 'anthropic', 'test-model');`); err != nil {
		t.Fatalf("seed open call: %v", err)
	}
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
	dsn := disposableDatabaseAt(t, versionBeforeOutcome)
	db := openDB(t, dsn)
	seedOpenLLMCall(t, db)

	// Complete it under v10, where no outcome column exists to record how.
	if _, err := db.Exec(`
		UPDATE llm_calls SET finished_at = now()
		WHERE llm_call_id = '33333333-3333-4333-8333-333333333333'`); err != nil {
		t.Fatalf("complete call under v10: %v", err)
	}

	err := migrations.Up(context.Background(), dsn)
	if err == nil {
		t.Fatal("the migration accepted a completed call with no recoverable outcome; it must refuse " +
			"rather than record a success that was never observed")
	}
	if !strings.Contains(err.Error(), "will not invent one") {
		t.Fatalf("error does not explain the refusal: %v", err)
	}
}

// TestOutcomeConstraintsRejectIncoherentRows covers the backstop. The seam
// checks these too, but the seam is not the only way a row is written.
func TestOutcomeConstraintsRejectIncoherentRows(t *testing.T) {
	dsn := disposableDatabase(t) // migrated to head
	db := openDB(t, dsn)
	seedOpenLLMCall(t, db)

	const callID = "'33333333-3333-4333-8333-333333333333'"
	cases := []struct {
		name   string
		update string
	}{
		{"success carrying an error", `SET finished_at = now(), succeeded = true, error_message = 'boom'`},
		{"failure with no diagnostic", `SET finished_at = now(), succeeded = false, error_message = NULL`},
		{"failure with a blank diagnostic", `SET finished_at = now(), succeeded = false, error_message = '   '`},
		{"open call carrying an error", `SET error_message = 'boom'`},
		{"finished with no outcome", `SET finished_at = now()`},
		{"outcome with no finish", `SET succeeded = true`},
	}

	// Reset before every case. The subtests share one row, and under working
	// constraints each UPDATE fails so the row stays pristine by accident --
	// but if a guard is ever missing, state leaks forward and later cases
	// stop testing what they name. That is how a mutation run reported six
	// kills where only four were about the guard removed.
	reset := func(t *testing.T) {
		t.Helper()
		if _, err := db.Exec(`UPDATE llm_calls SET finished_at = NULL, succeeded = NULL, error_message = NULL
			WHERE llm_call_id = ` + callID); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			reset(t)
			_, err := db.Exec(`UPDATE llm_calls ` + testCase.update + ` WHERE llm_call_id = ` + callID)
			if err == nil {
				t.Fatal("the database accepted an incoherent outcome")
			}
			if !strings.Contains(err.Error(), "check constraint") {
				t.Fatalf("rejected for the wrong reason: %v", err)
			}
		})
	}

	// The coherent completions must still be accepted, or the constraints
	// would be satisfied by rejecting everything.
	for _, update := range []string{
		`SET finished_at = now(), succeeded = true`,
		`SET finished_at = now(), succeeded = false, error_message = 'provider timeout'`,
	} {
		reset(t)
		if _, err := db.Exec(`UPDATE llm_calls ` + update + ` WHERE llm_call_id = ` + callID); err != nil {
			t.Fatalf("a coherent completion was rejected (%s): %v", update, err)
		}
	}
}
