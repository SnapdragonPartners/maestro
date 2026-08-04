//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"orchestrator/internal/dataplane/migrations"
)

// versionBeforeBenchmarkScope is the last schema version without the
// benchmark scope column, used to seed rows under the old expression and
// migrate over them.
const versionBeforeBenchmarkScope = 16

// seedScopedArtifacts writes one organization-scoped artifact of each family
// and returns nothing; the ids are fixed so the assertions can name them.
const seedScopedArtifacts = `
	INSERT INTO organizations (organization_id, slug, display_name)
	VALUES ('11111111-1111-4111-8111-111111111111', 'scope', 'Scope');
	INSERT INTO users (user_id, organization_id, handle, display_name)
	VALUES ('99999999-9999-4999-8999-999999999999', '11111111-1111-4111-8111-111111111111',
	        'op', 'Operator');
	INSERT INTO principal_instances (principal_instance_id, organization_id, kind, model, agent_type)
	VALUES ('22222222-2222-4222-8222-222222222222', '11111111-1111-4111-8111-111111111111',
	        'agent', 'test-model', 'coder');

	INSERT INTO management_artifacts (artifact_id, organization_id, user_id, artifact_type,
	                                  status, scope_type, scope_organization_id,
	                                  author_instance_id, schema_version, summary,
	                                  payload, payload_digest, review_digest)
	VALUES ('33333333-3333-4333-8333-333333333333', '11111111-1111-4111-8111-111111111111',
	        '99999999-9999-4999-8999-999999999999', 'probe', 'draft', 'organization',
	        '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222',
	        1, 'probe', '{}'::jsonb, repeat('a', 64), repeat('b', 64));

	INSERT INTO audit_artifacts (artifact_id, organization_id, artifact_type, scope_type,
	                             scope_organization_id, author_instance_id, schema_version,
	                             summary, payload, payload_digest)
	VALUES ('44444444-4444-4444-8444-444444444444', '11111111-1111-4111-8111-111111111111',
	        'probe', 'organization', '11111111-1111-4111-8111-111111111111',
	        '22222222-2222-4222-8222-222222222222', 1, 'probe', '{}'::jsonb, repeat('c', 64));`

// TestBenchmarkScopeIsQueryable is the assertion the first draft of this
// design would have failed.
//
// It proposed leaving scope_id's generated expression alone and letting
// benchmark-scoped rows carry a null. That reads as harmless — scope_id looks
// like an index input — but the converter reads it as the domain scope id and
// the scope queries filter on `scope_id = @scope_id`, so those rows would have
// come back as uuid.Nil and never been listed by the one query the scope
// exists to serve. An artifact the seam cannot read back is not stored.
func TestBenchmarkScopeIsQueryable(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabase(t)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test handle

	if _, err := db.Exec(seedScopedArtifacts); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO benchmark_runs (benchmark_run_id, organization_id, suite_run_id)
		VALUES ('55555555-5555-4555-8555-555555555555', '11111111-1111-4111-8111-111111111111',
		        'golden-all-probe');

		INSERT INTO audit_artifacts (artifact_id, organization_id, artifact_type, scope_type,
		                             scope_benchmark_run_id, author_instance_id, schema_version,
		                             summary, payload, payload_digest)
		VALUES ('66666666-6666-4666-8666-666666666666', '11111111-1111-4111-8111-111111111111',
		        'benchmark.run_record', 'benchmark', '55555555-5555-4555-8555-555555555555',
		        '22222222-2222-4222-8222-222222222222', 1, 'attempt', '{}'::jsonb, repeat('d', 64));`,
	); err != nil {
		t.Fatalf("seed benchmark-scoped artifact: %v", err)
	}

	// The generated column must resolve to the benchmark run, and the scope
	// query must therefore find it.
	var scopeID string
	if err := db.QueryRow(`SELECT scope_id::text FROM audit_artifacts
		WHERE artifact_id = '66666666-6666-4666-8666-666666666666'`).Scan(&scopeID); err != nil {
		t.Fatalf("read scope_id: %v", err)
	}
	if scopeID != "55555555-5555-4555-8555-555555555555" {
		t.Fatalf("scope_id = %q, want the benchmark run id; a null here reads as uuid.Nil "+
			"at the seam and the row is unlistable", scopeID)
	}

	var found int
	if err := db.QueryRow(`SELECT count(*) FROM audit_artifacts
		WHERE organization_id = '11111111-1111-4111-8111-111111111111'
		  AND scope_type = 'benchmark'
		  AND scope_id   = '55555555-5555-4555-8555-555555555555'`).Scan(&found); err != nil {
		t.Fatalf("scope query: %v", err)
	}
	if found != 1 {
		t.Errorf("the scope query found %d rows, want 1", found)
	}

	// The constraints the new column brings, each broken on its own.
	for name, statement := range map[string]string{
		"benchmark scope with no run": `
			INSERT INTO audit_artifacts (artifact_id, organization_id, artifact_type, scope_type,
			                             author_instance_id, schema_version, summary, payload, payload_digest)
			VALUES ('77777777-7777-4777-8777-777777777777', '11111111-1111-4111-8111-111111111111',
			        'benchmark.run_record', 'benchmark', '22222222-2222-4222-8222-222222222222',
			        1, 'x', '{}'::jsonb, repeat('e', 64))`,
		"two scopes at once": `
			INSERT INTO audit_artifacts (artifact_id, organization_id, artifact_type, scope_type,
			                             scope_benchmark_run_id, scope_organization_id,
			                             author_instance_id, schema_version, summary, payload, payload_digest)
			VALUES ('77777777-7777-4777-8777-777777777777', '11111111-1111-4111-8111-111111111111',
			        'benchmark.run_record', 'benchmark', '55555555-5555-4555-8555-555555555555',
			        '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222',
			        1, 'x', '{}'::jsonb, repeat('e', 64))`,
		"benchmark scope carrying work lineage": `
			INSERT INTO audit_artifacts (artifact_id, organization_id, artifact_type, scope_type,
			                             scope_benchmark_run_id, product_id,
			                             author_instance_id, schema_version, summary, payload, payload_digest)
			VALUES ('77777777-7777-4777-8777-777777777777', '11111111-1111-4111-8111-111111111111',
			        'benchmark.run_record', 'benchmark', '55555555-5555-4555-8555-555555555555',
			        '88888888-8888-4888-8888-888888888888', '22222222-2222-4222-8222-222222222222',
			        1, 'x', '{}'::jsonb, repeat('e', 64))`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.Exec(statement); err == nil {
				t.Error("the schema accepted a row it must refuse")
			}
		})
	}

	_ = ctx
}

// TestBenchmarkScopeMigrationPreservesExistingScopes covers the other half of
// rebuilding a generated column: rows written under the OLD expression must
// come out of the rewrite with the scope they had.
//
// SET EXPRESSION rewrites the table, so this is a claim about Postgres's
// behaviour on the pinned image rather than about our SQL — which is exactly
// why it is asserted here instead of assumed. The up/down/up round trip is
// included because the down migration restores the five-way expression, and a
// column that survived the first rewrite could still be lost by the second.
func TestBenchmarkScopeMigrationPreservesExistingScopes(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabaseAt(t, versionBeforeBenchmarkScope)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test handle

	if _, err := db.Exec(seedScopedArtifacts); err != nil {
		t.Fatalf("seed under the old expression: %v", err)
	}

	const wantScope = "11111111-1111-4111-8111-111111111111"
	assertScopes := func(stage string) {
		t.Helper()
		for table, id := range map[string]string{
			"management_artifacts": "33333333-3333-4333-8333-333333333333",
			"audit_artifacts":      "44444444-4444-4444-8444-444444444444",
		} {
			var got sql.NullString
			query := "SELECT scope_id::text FROM " + table + " WHERE artifact_id = $1"
			if err := db.QueryRow(query, id).Scan(&got); err != nil {
				t.Fatalf("%s: read %s scope_id: %v", stage, table, err)
			}
			if !got.Valid || got.String != wantScope {
				t.Errorf("%s: %s scope_id = %v, want %s; the rewrite lost an existing scope",
					stage, table, got, wantScope)
			}
		}
	}

	assertScopes("before")
	if err := migrations.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	assertScopes("after up")
	if err := migrations.To(ctx, dsn, versionBeforeBenchmarkScope); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	assertScopes("after down")
	if err := migrations.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
	assertScopes("after up again")
}
