//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	"orchestrator/internal/dataplane/migrations"
)

// A PENDING claim -- one naming an artifact that does not exist yet -- is a
// state the forward protocol deliberately permits: it is what a caller is in
// between reserving an identifier and writing under it, and it is what makes
// a death in that window recoverable rather than ambiguous.
//
// So the down migration has to survive it. Re-adding the foreign key over a
// pending claim fails, which would make 000020 reversible only on planes that
// never used the feature -- reversibility that holds exactly where it is not
// needed.
//
// The round trip is run over a POPULATED table, because an empty one cannot
// tell a down migration that discards pending intents from one that does not
// have to.
func TestReportClaimMigrationReversesOverAPendingClaim(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabase(t)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	var organizationID, benchmarkRunID string
	if err := db.QueryRow(`
		WITH org AS (
			INSERT INTO organizations (organization_id, slug, display_name)
			VALUES (gen_random_uuid(), 'reversal', 'Reversal')
			RETURNING organization_id)
		INSERT INTO benchmark_runs (benchmark_run_id, organization_id, suite_run_id)
		SELECT gen_random_uuid(), organization_id, 'golden-reversal' FROM org
		RETURNING organization_id, benchmark_run_id`).
		Scan(&organizationID, &benchmarkRunID); err != nil {
		t.Fatalf("seed the suite: %v", err)
	}

	// The pending claim: an identifier reserved, and nothing written under it.
	if _, err := db.Exec(`
		INSERT INTO benchmark_reports (benchmark_report_id, organization_id,
		                               benchmark_run_id, report_artifact_id)
		VALUES (gen_random_uuid(), $1, $2, gen_random_uuid())`,
		organizationID, benchmarkRunID); err != nil {
		t.Fatalf("reserve an identifier: %v", err)
	}

	// 000019 is the version below 000020, so this runs 000020's down over
	// the pending claim it must survive.
	if err := migrations.To(ctx, dsn, 19); err != nil {
		t.Fatalf("migrate down over a pending claim: %v", err)
	}

	// The intent is discarded rather than left dangling: under the restored
	// constraint it cannot be expressed, and a row that survived would be a
	// reference to nothing.
	var pending int
	if err := db.QueryRow(`SELECT count(*) FROM benchmark_reports`).Scan(&pending); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if pending != 0 {
		t.Errorf("%d claims survived the reversal; the restored foreign key cannot hold one that "+
			"names nothing", pending)
	}

	if err := migrations.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate back up: %v", err)
	}
	// And the plane is usable again: a fresh reservation succeeds, which is
	// what the forward protocol needs and what a half-applied constraint
	// would have broken.
	if _, err := db.Exec(`
		INSERT INTO benchmark_reports (benchmark_report_id, organization_id,
		                               benchmark_run_id, report_artifact_id)
		VALUES (gen_random_uuid(), $1, $2, gen_random_uuid())`,
		organizationID, benchmarkRunID); err != nil {
		t.Fatalf("reserve an identifier after the round trip: %v", err)
	}
}
