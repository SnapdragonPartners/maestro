//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"orchestrator/internal/dataplane/migrations"
)

// versionBeforeBenchmarkScope is the last schema version without the
// benchmark scope column, used to seed rows under the old expression and
// migrate over them.
const versionBeforeBenchmarkScope = 16

// Two organizations, because the organization half of every composite key
// here is load-bearing and a single-tenant fixture cannot exercise it.
const (
	orgA = "11111111-1111-4111-8111-111111111111"
	orgB = "1b1b1b1b-1b1b-4b1b-8b1b-1b1b1b1b1b1b"

	userA      = "99999999-9999-4999-8999-999999999999"
	principalA = "22222222-2222-4222-8222-222222222222"
	principalB = "2b2b2b2b-2b2b-4b2b-8b2b-2b2b2b2b2b2b"

	managementA = "33333333-3333-4333-8333-333333333333"
	auditA      = "44444444-4444-4444-8444-444444444444"
	runA        = "55555555-5555-4555-8555-555555555555"
	runB        = "5b5b5b5b-5b5b-4b5b-8b5b-5b5b5b5b5b5b"
	productA    = "88888888-8888-4888-8888-888888888888"
)

// seedTwoTenants writes both organizations with a user, a principal and a
// Product each side, plus one organization-scoped artifact per family in A.
//
// The Product is real rather than invented: a work-lineage rejection test
// naming a nonexistent Product would be refused by the product foreign key
// before the lineage rule was ever consulted, and would then pass while the
// rule it claims to test was absent.
const seedTwoTenants = `
	INSERT INTO organizations (organization_id, slug, display_name)
	VALUES ('` + orgA + `', 'scope-a', 'Scope A'),
	       ('` + orgB + `', 'scope-b', 'Scope B');

	INSERT INTO users (user_id, organization_id, handle, display_name)
	VALUES ('` + userA + `', '` + orgA + `', 'op', 'Operator');

	INSERT INTO principal_instances (principal_instance_id, organization_id, kind, model, agent_type)
	VALUES ('` + principalA + `', '` + orgA + `', 'agent', 'test-model', 'coder'),
	       ('` + principalB + `', '` + orgB + `', 'agent', 'test-model', 'coder');

	INSERT INTO products (product_id, organization_id, user_id, slug, display_name)
	VALUES ('` + productA + `', '` + orgA + `', '` + userA + `', 'prod', 'Product');

	INSERT INTO management_artifacts (artifact_id, organization_id, user_id, artifact_type,
	                                  status, scope_type, scope_organization_id,
	                                  author_instance_id, schema_version, summary,
	                                  payload, payload_digest, review_digest)
	VALUES ('` + managementA + `', '` + orgA + `', '` + userA + `', 'probe', 'draft', 'organization',
	        '` + orgA + `', '` + principalA + `', 1, 'probe', '{}'::jsonb,
	        repeat('a', 64), repeat('b', 64));

	INSERT INTO audit_artifacts (artifact_id, organization_id, artifact_type, scope_type,
	                             scope_organization_id, author_instance_id, schema_version,
	                             summary, payload, payload_digest)
	VALUES ('` + auditA + `', '` + orgA + `', 'probe', 'organization', '` + orgA + `',
	        '` + principalA + `', 1, 'probe', '{}'::jsonb, repeat('c', 64));`

// benchmarkScopedInsert builds an insert for one family, so both are driven
// by the same cases instead of one standing in for the other.
//
// The families are NOT interchangeable here: the suite report is a Management
// artifact and the run records are Audit ones, so a suite that exercises only
// Audit leaves the Management arm of every rule unproven — its generated
// expression, its scope constraint and its foreign key.
func benchmarkScopedInsert(family, artifactID, org, runID, extraColumns, extraValues string) string {
	if family == "management_artifacts" {
		return fmt.Sprintf(`
			INSERT INTO management_artifacts (artifact_id, organization_id, user_id, artifact_type,
			                                  status, scope_type, scope_benchmark_run_id%s,
			                                  author_instance_id, schema_version, summary,
			                                  payload, payload_digest, review_digest)
			VALUES ('%s', '%s', '%s', 'benchmark.suite_report', 'draft', 'benchmark', %s%s,
			        '%s', 1, 'report', '{}'::jsonb, repeat('a', 64), repeat('b', 64))`,
			extraColumns, artifactID, org, userA, runID, extraValues, principalA)
	}
	return fmt.Sprintf(`
		INSERT INTO audit_artifacts (artifact_id, organization_id, artifact_type, scope_type,
		                             scope_benchmark_run_id%s, author_instance_id,
		                             schema_version, summary, payload, payload_digest)
		VALUES ('%s', '%s', 'benchmark.run_record', 'benchmark', %s%s, '%s',
		        1, 'attempt', '{}'::jsonb, repeat('d', 64))`,
		extraColumns, artifactID, org, runID, extraValues, principalA)
}

// requireConstraint asserts the statement was refused BY A NAMED RULE.
//
// A bare "some rejection" assertion is weak wherever a row can break more
// than one rule at once, which is most of the cases below: the row that is
// supposed to prove the lineage check also has a foreign key to satisfy, and
// a test that only asks whether an error happened cannot tell which of them
// spoke.
func requireConstraint(t *testing.T, db *sql.DB, statement, want string) {
	t.Helper()
	_, err := db.Exec(statement)
	if err == nil {
		t.Fatalf("the schema accepted a row it must refuse (expected %s)", want)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a Postgres error naming %s, got %v", want, err)
	}
	if pgErr.ConstraintName != want {
		t.Fatalf("refused by %q, want %q: the row broke a different rule than the one under test",
			pgErr.ConstraintName, want)
	}
}

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
	dsn := disposableDatabase(t)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test handle

	if _, err := db.Exec(seedTwoTenants); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO benchmark_runs (benchmark_run_id, organization_id, suite_run_id)
		VALUES ('` + runA + `', '` + orgA + `', 'golden-all-probe'),
		       ('` + runB + `', '` + orgB + `', 'golden-all-probe');`); err != nil {
		t.Fatalf("seed benchmark runs: %v", err)
	}

	// BOTH families, same cases. The report is Management and the records are
	// Audit, so neither can stand in for the other.
	for _, family := range []string{"management_artifacts", "audit_artifacts"} {
		t.Run(family, func(t *testing.T) {
			artifactID := "66666666-6666-4666-8666-666666666666"
			if family == "audit_artifacts" {
				artifactID = "6a6a6a6a-6a6a-4a6a-8a6a-6a6a6a6a6a6a"
			}
			if _, err := db.Exec(benchmarkScopedInsert(family, artifactID, orgA, "'"+runA+"'", "", "")); err != nil {
				t.Fatalf("insert benchmark-scoped artifact: %v", err)
			}

			var scopeID string
			query := "SELECT scope_id::text FROM " + family + " WHERE artifact_id = $1"
			if err := db.QueryRow(query, artifactID).Scan(&scopeID); err != nil {
				t.Fatalf("read scope_id: %v", err)
			}
			if scopeID != runA {
				t.Fatalf("scope_id = %q, want the benchmark run id; a null here reads as uuid.Nil "+
					"at the seam and the row is unlistable", scopeID)
			}

			var found int
			listed := "SELECT count(*) FROM " + family + `
				WHERE organization_id = $1 AND scope_type = 'benchmark' AND scope_id = $2`
			if err := db.QueryRow(listed, orgA, runA).Scan(&found); err != nil {
				t.Fatalf("scope query: %v", err)
			}
			if found != 1 {
				t.Errorf("the scope query found %d rows, want 1", found)
			}
		})
	}
}

// TestBenchmarkScopeConstraints drives every rule the new column brings,
// across both families, asserting WHICH rule refused each row.
func TestBenchmarkScopeConstraints(t *testing.T) {
	dsn := disposableDatabase(t)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test handle

	if _, err := db.Exec(seedTwoTenants); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO benchmark_runs (benchmark_run_id, organization_id, suite_run_id)
		VALUES ('` + runA + `', '` + orgA + `', 'golden-all-probe'),
		       ('` + runB + `', '` + orgB + `', 'golden-all-probe');`); err != nil {
		t.Fatalf("seed benchmark runs: %v", err)
	}

	const victim = "77777777-7777-4777-8777-777777777777"
	for _, family := range []string{"management_artifacts", "audit_artifacts"} {
		prefix := "management_artifacts"
		if family == "audit_artifacts" {
			prefix = "audit_artifacts"
		}
		t.Run(family, func(t *testing.T) {
			t.Run("benchmark scope naming no run", func(t *testing.T) {
				requireConstraint(t, db,
					benchmarkScopedInsert(family, victim, orgA, "NULL", "", ""),
					prefix+"_one_scope_check")
			})
			t.Run("two scopes at once", func(t *testing.T) {
				requireConstraint(t, db,
					benchmarkScopedInsert(family, victim, orgA, "'"+runA+"'",
						", scope_organization_id", ", '"+orgA+"'"),
					prefix+"_one_scope_check")
			})
			t.Run("benchmark scope carrying work lineage", func(t *testing.T) {
				// The Product EXISTS, so the row's only fault is the lineage
				// rule. Naming a nonexistent one would have been refused by
				// the product foreign key first, and this case would have
				// passed with the lineage check absent.
				requireConstraint(t, db,
					benchmarkScopedInsert(family, victim, orgA, "'"+runA+"'",
						", product_id", ", '"+productA+"'"),
					prefix+"_lineage_check")
			})
			t.Run("scope names another tenant's run", func(t *testing.T) {
				// The organization half of the composite foreign key is what
				// stops one tenant's artifact scoping to another's run. A
				// plain reference to benchmark_run_id would accept this.
				requireConstraint(t, db,
					benchmarkScopedInsert(family, victim, orgA, "'"+runB+"'", "", ""),
					prefix+"_scope_benchmark_fkey")
			})
		})
	}
}

// TestBenchmarkAttemptTenancy covers the ledger's two composite references
// INDEPENDENTLY, so weakening either one cannot be masked by the other.
func TestBenchmarkAttemptTenancy(t *testing.T) {
	dsn := disposableDatabase(t)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test handle

	if _, err := db.Exec(seedTwoTenants); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const (
		auditB    = "4b4b4b4b-4b4b-4b4b-8b4b-4b4b4b4b4b4b"
		auditRunA = "4a4a4a4a-4a4a-4a4a-8a4a-4a4a4a4a4a4a"
	)
	if _, err := db.Exec(`
		INSERT INTO benchmark_runs (benchmark_run_id, organization_id, suite_run_id)
		VALUES ('` + runA + `', '` + orgA + `', 'golden-all-probe'),
		       ('` + runB + `', '` + orgB + `', 'golden-all-probe');

		INSERT INTO audit_artifacts (artifact_id, organization_id, artifact_type, scope_type,
		                             scope_benchmark_run_id, author_instance_id, schema_version,
		                             summary, payload, payload_digest)
		VALUES ('` + auditRunA + `', '` + orgA + `', 'benchmark.run_record', 'benchmark', '` + runA + `',
		        '` + principalA + `', 1, 'a', '{}'::jsonb, repeat('d', 64));

		INSERT INTO audit_artifacts (artifact_id, organization_id, artifact_type, scope_type,
		                             scope_benchmark_run_id, author_instance_id, schema_version,
		                             summary, payload, payload_digest)
		VALUES ('` + auditB + `', '` + orgB + `', 'benchmark.run_record', 'benchmark', '` + runB + `',
		        '` + principalB + `', 1, 'b', '{}'::jsonb, repeat('d', 64));`); err != nil {
		t.Fatalf("seed artifacts: %v", err)
	}

	attempt := func(org, run, artifact, runID string) string {
		return fmt.Sprintf(`
			INSERT INTO benchmark_attempts (benchmark_attempt_id, organization_id, benchmark_run_id,
			                                run_id, record_digest, audit_artifact_id, calls_unavailable)
			VALUES ('%s', '%s', '%s', '%s', repeat('f', 64), '%s', '')`,
			"7a7a7a7a-7a7a-4a7a-8a7a-7a7a7a7a7a7a", org, run, runID, artifact)
	}

	t.Run("run belongs to another tenant", func(t *testing.T) {
		requireConstraint(t, db, attempt(orgA, runB, auditRunA, "attempt-one"),
			"benchmark_attempts_run_fkey")
	})
	t.Run("artifact belongs to another tenant", func(t *testing.T) {
		requireConstraint(t, db, attempt(orgA, runA, auditB, "attempt-one"),
			"benchmark_attempts_artifact_fkey")
	})
	t.Run("run_id is not a single path component", func(t *testing.T) {
		for _, bad := range []string{"../escape", "a/b", ".", ".."} {
			requireConstraint(t, db, attempt(orgA, runA, auditRunA, bad),
				"benchmark_attempts_run_id_check")
		}
	})

	// The control, and the identity rule: a well-formed attempt is accepted,
	// and a second one with the same identity is refused by the unique key
	// rather than duplicating.
	if _, err := db.Exec(attempt(orgA, runA, auditRunA, "attempt-one")); err != nil {
		t.Fatalf("a well-formed attempt must be accepted: %v", err)
	}
	duplicate := fmt.Sprintf(`
		INSERT INTO benchmark_attempts (benchmark_attempt_id, organization_id, benchmark_run_id,
		                                run_id, record_digest, audit_artifact_id, calls_unavailable)
		VALUES ('7b7b7b7b-7b7b-4b7b-8b7b-7b7b7b7b7b7b', '%s', '%s', 'attempt-one',
		        repeat('e', 64), '%s', '')`, orgA, runA, auditRunA)
	requireConstraint(t, db, duplicate, "benchmark_attempts_identity_key")
}

// TestBenchmarkScopeMigrationPreservesExistingScopes covers the other half of
// rebuilding a generated column: rows written under the OLD expression must
// come out of the rewrite with the scope they had, and the indexes that
// depend on the column must still be there.
//
// SET EXPRESSION rewrites the table, so both are claims about Postgres's
// behaviour on the pinned image rather than about our SQL — which is exactly
// why they are asserted here instead of assumed. The up/down/up round trip is
// included because the down migration restores the five-way expression, and a
// column or index that survived the first rewrite could still be lost by the
// second.
func TestBenchmarkScopeMigrationPreservesExistingScopes(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabaseAt(t, versionBeforeBenchmarkScope)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test handle

	if _, err := db.Exec(seedTwoTenants); err != nil {
		t.Fatalf("seed under the old expression: %v", err)
	}

	assertState := func(stage string) {
		t.Helper()
		for table, id := range map[string]string{
			"management_artifacts": managementA,
			"audit_artifacts":      auditA,
		} {
			var got sql.NullString
			query := "SELECT scope_id::text FROM " + table + " WHERE artifact_id = $1"
			if err := db.QueryRow(query, id).Scan(&got); err != nil {
				t.Fatalf("%s: read %s scope_id: %v", stage, table, err)
			}
			if !got.Valid || got.String != orgA {
				t.Errorf("%s: %s scope_id = %v, want %s; the rewrite lost an existing scope",
					stage, table, got, orgA)
			}
			// The index depends on the generated column, so a DROP/ADD would
			// have taken it silently. SET EXPRESSION is supposed to keep it.
			var indexes int
			if err := db.QueryRow(`SELECT count(*) FROM pg_indexes
				WHERE tablename = $1 AND indexname = $2`, table, table+"_scope_idx").Scan(&indexes); err != nil {
				t.Fatalf("%s: read %s indexes: %v", stage, table, err)
			}
			if indexes != 1 {
				t.Errorf("%s: %s_scope_idx is missing; the rewrite dropped the dependent index",
					stage, table)
			}
		}
	}

	assertState("before")
	if err := migrations.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	assertState("after up")
	if err := migrations.To(ctx, dsn, versionBeforeBenchmarkScope); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	assertState("after down")
	if err := migrations.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
	assertState("after up again")
}

// A ledger row must STATE whether its calls were read.
//
// The column carries no default, and that is the whole guard. A default
// would let a writer omit it and be handed an answer it never observed --
// and the answer a text default gives, empty, is precisely the one that
// fabricates a measurement: it means "the calls were read". A surface-v1 log
// and a missing log both import their attempt with zero call rows and a
// reason, so "unavailable" is an ordinary outcome and not an edge case.
//
// An earlier revision added this column in a later migration with an empty
// default, which would have converted every already-imported unavailable
// attempt into an available one. Folding it into the ledger's own migration
// removes the conversion rather than describing it: no row can predate the
// column, so there is no historical unknown to invent a meaning for.
func TestAnAttemptMustStateWhetherItsCallsWereRead(t *testing.T) {
	dsn := disposableDatabase(t)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	var organizationID, benchmarkRunID, artifactID string
	if err := db.QueryRow(`
		WITH org AS (
			INSERT INTO organizations (organization_id, slug, display_name)
			VALUES (gen_random_uuid(), 'stated', 'Stated')
			RETURNING organization_id),
		principal AS (
			INSERT INTO principal_instances (principal_instance_id, organization_id, kind, model)
			SELECT gen_random_uuid(), organization_id, 'system', 'system-benchmark-importer' FROM org
			RETURNING principal_instance_id, organization_id),
		run AS (
			INSERT INTO benchmark_runs (benchmark_run_id, organization_id, suite_run_id)
			SELECT gen_random_uuid(), organization_id, 'golden-stated' FROM org
			RETURNING benchmark_run_id, organization_id)
		INSERT INTO audit_artifacts (artifact_id, organization_id, artifact_type, scope_type,
		                             scope_benchmark_run_id, author_instance_id,
		                             schema_version, summary, payload, payload_digest)
		SELECT gen_random_uuid(), run.organization_id, 'benchmark.run_record', 'benchmark',
		       run.benchmark_run_id, principal.principal_instance_id,
		       1, 'a record', '{}'::jsonb, repeat('a', 64)
		FROM run JOIN principal ON principal.organization_id = run.organization_id
		RETURNING organization_id, scope_benchmark_run_id, artifact_id`).
		Scan(&organizationID, &benchmarkRunID, &artifactID); err != nil {
		t.Fatalf("seed the suite: %v", err)
	}

	// The column omitted: refused, because the writer did not say.
	_, err = db.Exec(`
		INSERT INTO benchmark_attempts (benchmark_attempt_id, organization_id, benchmark_run_id,
		                                run_id, record_digest, audit_artifact_id)
		VALUES (gen_random_uuid(), $1, $2, 'story-a--config--r1--aaaa1111', $3, $4)`,
		organizationID, benchmarkRunID, strings.Repeat("b", 64), artifactID)
	if err == nil {
		t.Fatal("an attempt was ledgered without stating whether its calls were read; a default " +
			"here answers a question the writer never asked the store")
	}

	// Stated, and the statement is kept verbatim -- including the empty
	// string, which is a real answer rather than an absent one.
	for _, stated := range []string{"", "the usage log is surface v1"} {
		if _, err := db.Exec(`
			INSERT INTO benchmark_attempts (benchmark_attempt_id, organization_id, benchmark_run_id,
			                                run_id, record_digest, audit_artifact_id, calls_unavailable)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6)`,
			organizationID, benchmarkRunID, "story-a--config--r"+strconv.Itoa(len(stated))+"--aaaa1111",
			strings.Repeat("c", 64), artifactID, stated); err != nil {
			t.Fatalf("ledger an attempt stating %q: %v", stated, err)
		}
	}
}
