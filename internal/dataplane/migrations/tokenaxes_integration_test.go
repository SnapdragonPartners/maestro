//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"orchestrator/internal/dataplane/migrations"
)

// versionBeforeTokenAxes is the last schema version written under the old
// token contract: four NOT NULL counters, no cache-write axis, and no way to
// say "this call has no measurement".
const versionBeforeTokenAxes = 15

// TestTokenAxesMigrationNullsLegacyMeasurements pins what migration 16 does
// to rows that already exist.
//
// The tempting alternative — keep the four counters and default the new
// cache-write axis to zero — is the fabrication this migration exists to
// remove, arriving through the migration instead of the writer. No row in
// this table was written by a surface that could report cache writes, so a
// zero there is a measurement nobody made; and availability is all-or-none,
// so a row that cannot prove the fifth axis cannot keep the other four.
//
// Legacy FAILED rows are the sharper case: their four zeros exist precisely
// because the old surface could not tell "no usage reported" from "no usage".
// Keeping them would leave the defect in the history while forbidding it in
// new data.
func TestTokenAxesMigrationNullsLegacyMeasurements(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabaseAt(t, versionBeforeTokenAxes)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test handle

	// Three shapes the old contract could produce, seeded under it.
	if _, err := db.Exec(`
		INSERT INTO organizations (organization_id, slug, display_name)
		VALUES ('11111111-1111-4111-8111-111111111111', 'legacy', 'Legacy');
		INSERT INTO principal_instances (principal_instance_id, organization_id, kind, model, agent_type)
		VALUES ('22222222-2222-4222-8222-222222222222', '11111111-1111-4111-8111-111111111111',
		        'agent', 'test-model', 'coder');

		-- A completed SUCCESS with genuine counters.
		INSERT INTO llm_calls (llm_call_id, organization_id, principal_instance_id, provider, model,
		                       finished_at, succeeded, input_tokens, output_tokens,
		                       reasoning_tokens, cached_tokens)
		VALUES ('33333333-3333-4333-8333-333333333333', '11111111-1111-4111-8111-111111111111',
		        '22222222-2222-4222-8222-222222222222', 'anthropic', 'test-model',
		        now(), true, 100, 50, 10, 5);

		-- A completed FAILURE carrying the four fabricated zeros.
		INSERT INTO llm_calls (llm_call_id, organization_id, principal_instance_id, provider, model,
		                       finished_at, succeeded, error_message)
		VALUES ('44444444-4444-4444-8444-444444444444', '11111111-1111-4111-8111-111111111111',
		        '22222222-2222-4222-8222-222222222222', 'anthropic', 'test-model',
		        now(), false, 'provider refused');

		-- An OPEN call, whose zeros were never a measurement either.
		INSERT INTO llm_calls (llm_call_id, organization_id, principal_instance_id, provider, model)
		VALUES ('55555555-5555-4555-8555-555555555555', '11111111-1111-4111-8111-111111111111',
		        '22222222-2222-4222-8222-222222222222', 'anthropic', 'test-model');`); err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}

	// Guard against the test passing because the seed never landed: the
	// success row must genuinely carry counters before the migration runs.
	var seeded int
	if err := db.QueryRow(`SELECT input_tokens FROM llm_calls
		WHERE llm_call_id = '33333333-3333-4333-8333-333333333333'`).Scan(&seeded); err != nil {
		t.Fatalf("read seeded counters: %v", err)
	}
	if seeded != 100 {
		t.Fatalf("seeded input_tokens = %d, want 100; the fixture is not exercising the migration", seeded)
	}

	if err := migrations.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up over legacy rows: %v", err)
	}

	// Every row, every axis, null. Counted rather than sampled so a row the
	// migration missed cannot hide behind one it caught.
	var total, allNull int
	if err := db.QueryRow(`
		SELECT count(*),
		       count(*) FILTER (WHERE num_nonnulls(input_tokens, output_tokens, reasoning_tokens,
		                                           cache_read_tokens, cache_write_tokens) = 0)
		FROM llm_calls`).Scan(&total, &allNull); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3 seeded rows, found %d", total)
	}
	if allNull != total {
		t.Errorf("%d of %d legacy rows kept a token measurement; a row that cannot prove all five "+
			"axes must keep none", total-allNull, total)
	}

	// And the new contract is enforced from here on: a partial measurement
	// is refused, so nothing can reintroduce the shape by hand.
	_, err = db.Exec(`
		UPDATE llm_calls SET input_tokens = 1
		WHERE llm_call_id = '33333333-3333-4333-8333-333333333333'`)
	if err == nil {
		t.Error("setting one axis alone must violate the availability check")
	}
}
