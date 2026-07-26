//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/stack"
)

// A schema is only as good as what it REFUSES, and every constraint here
// guards a failure that a test inserting well-formed rows would never see.
// These run against a real Postgres because that is the only thing that can
// answer whether the DDL means what it says.

func openPlane(t *testing.T) *sql.DB {
	t.Helper()

	roots, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve roots: %v", err)
	}
	cfg, err := stack.NewConfig(roots)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	rootKey, err := paths.EnsureKey(roots.Config)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	dsn, err := cfg.DSN(rootKey)
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}

	if err := migrations.Up(context.Background(), dsn); err != nil {
		t.Skipf("data plane unavailable (run `make dataplane-up`): %v", err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seed builds a minimal but complete work hierarchy inside a transaction
// the caller rolls back, so cases never see each other's rows.
type fixture struct {
	tx                                *sql.Tx
	org, user, product, feature, repo string
	epic, story, principal            string
}

func seed(t *testing.T, db *sql.DB) *fixture {
	t.Helper()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })

	f := &fixture{
		tx:        tx,
		org:       "10000000-0000-7000-8000-000000000001",
		user:      "10000000-0000-7000-8000-000000000002",
		product:   "10000000-0000-7000-8000-000000000003",
		feature:   "10000000-0000-7000-8000-000000000004",
		epic:      "10000000-0000-7000-8000-000000000005",
		story:     "10000000-0000-7000-8000-000000000006",
		principal: "10000000-0000-7000-8000-000000000007",
		repo:      "10000000-0000-7000-8000-000000000008",
	}

	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1,'t','T')`, []any{f.org}},
		{`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1,$2,'u','U')`, []any{f.user, f.org}},
		{`INSERT INTO products (product_id, organization_id, user_id, slug, display_name) VALUES ($1,$2,$3,'p','P')`, []any{f.product, f.org, f.user}},
		{`INSERT INTO repositories (repository_id, organization_id, primary_product_id, user_id, slug, display_name) VALUES ($1,$2,$3,$4,'r','R')`, []any{f.repo, f.org, f.product, f.user}},
		{`INSERT INTO product_repositories (product_id, repository_id, organization_id) VALUES ($1,$2,$3)`, []any{f.product, f.repo, f.org}},
		{`INSERT INTO features (feature_id, organization_id, user_id, product_id, title) VALUES ($1,$2,$3,$4,'F')`, []any{f.feature, f.org, f.user, f.product}},
		{`INSERT INTO epics (epic_id, organization_id, user_id, product_id, feature_id, repository_id, title) VALUES ($1,$2,$3,$4,$5,$6,'E')`, []any{f.epic, f.org, f.user, f.product, f.feature, f.repo}},
		{`INSERT INTO stories (story_id, organization_id, user_id, product_id, feature_id, epic_id, title) VALUES ($1,$2,$3,$4,$5,$6,'S')`, []any{f.story, f.org, f.user, f.product, f.feature, f.epic}},
		{`INSERT INTO principal_instances (principal_instance_id, organization_id, kind, model, agent_type) VALUES ($1,$2,'agent','opus','coder')`, []any{f.principal, f.org}},
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s.sql, s.args...); err != nil {
			t.Fatalf("seed %q: %v", s.sql, err)
		}
	}
	return f
}

const digestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const digestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// insertStoryArtifact writes a well-formed story-scoped Management
// artifact, overriding whichever columns a case wants to break.
func (f *fixture) insertStoryArtifact(id string, overrides map[string]any) error {
	cols := map[string]any{
		"artifact_id":        id,
		"organization_id":    f.org,
		"user_id":            f.user,
		"artifact_type":      "story_plan",
		"scope_type":         "story",
		"scope_story_id":     f.story,
		"product_id":         f.product,
		"feature_id":         f.feature,
		"epic_id":            f.epic,
		"story_id":           f.story,
		"author_instance_id": f.principal,
		"schema_version":     1,
		"summary":            "s",
		"payload":            "{}",
		"payload_digest":     digestA,
		"review_digest":      digestB,
	}
	for k, v := range overrides {
		if v == nil {
			delete(cols, k)
			continue
		}
		cols[k] = v
	}

	names := make([]string, 0, len(cols))
	placeholders := make([]string, 0, len(cols))
	args := make([]any, 0, len(cols))
	i := 1
	for name, value := range cols {
		names = append(names, name)
		placeholders = append(placeholders, "$"+itoa(i))
		args = append(args, value)
		i++
	}

	_, err := f.tx.Exec(
		"INSERT INTO management_artifacts ("+strings.Join(names, ",")+") VALUES ("+strings.Join(placeholders, ",")+")",
		args...)
	return err
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestValidArtifactIsAccepted(t *testing.T) {
	f := seed(t, openPlane(t))
	if err := f.insertStoryArtifact("20000000-0000-7000-8000-000000000001", nil); err != nil {
		t.Fatalf("a well-formed artifact was rejected: %v", err)
	}
}

// The case that caught the previous design. A supertable whose foreign keys
// pointed into it would allow this delete, leaving the artifact resolving
// to a scope whose entity is gone.
func TestDeletingAScopedEntityWithArtifactsIsBlocked(t *testing.T) {
	f := seed(t, openPlane(t))
	if err := f.insertStoryArtifact("20000000-0000-7000-8000-000000000002", nil); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	_, err := f.tx.Exec(`DELETE FROM stories WHERE story_id = $1`, f.story)
	if err == nil {
		t.Fatal("deleting a story with artifacts succeeded; the artifact is now orphaned")
	}
	if !strings.Contains(err.Error(), "violates") {
		t.Errorf("unexpected failure (want a constraint violation): %v", err)
	}
}

func TestConstraintsRefuseMalformedArtifacts(t *testing.T) {
	db := openPlane(t)

	cases := []struct {
		name      string
		overrides map[string]any
		wantIn    string
	}{
		{
			// Scope-conditional lineage: invisible to any test that only
			// inserts well-formed rows, which is why it is in the schema.
			name:      "story scope missing epic lineage",
			overrides: map[string]any{"epic_id": nil},
			wantIn:    "lineage_check",
		},
		{
			name:      "two scope columns set",
			overrides: map[string]any{"scope_epic_id": "10000000-0000-7000-8000-000000000005"},
			wantIn:    "one_scope_check",
		},
		{
			// scope_type says epic while the populated column is the story
			// one. The first draft of this case set BOTH consistently and
			// therefore built a legitimately valid epic-scoped row — the
			// test failed, correctly, and the schema was right.
			name:      "scope_type disagrees with the populated column",
			overrides: map[string]any{"scope_type": "epic", "story_id": nil},
			wantIn:    "scope_agrees_check",
		},
		{
			// Self-enforcing until item 9 adds the column.
			name: "benchmark scope has no column to satisfy",
			overrides: map[string]any{
				"scope_type": "benchmark", "scope_story_id": nil,
				"product_id": nil, "feature_id": nil, "epic_id": nil, "story_id": nil,
			},
			wantIn: "one_scope_check",
		},
		{
			name:      "uppercase digest",
			overrides: map[string]any{"payload_digest": strings.ToUpper(digestA)},
			wantIn:    "payload_digest_check",
		},
		{
			name:      "prefixed digest",
			overrides: map[string]any{"review_digest": "sha256:" + digestB[:57]},
			wantIn:    "review_digest_check",
		},
		{
			name:      "status outside the vocabulary",
			overrides: map[string]any{"status": "approved"},
			wantIn:    "status_check",
		},
		{
			name:      "wrong category for this table",
			overrides: map[string]any{"artifact_category": "audit"},
			wantIn:    "category_check",
		},
		{
			name:      "accepted_at without accepted status",
			overrides: map[string]any{"accepted_at": "2026-01-01T00:00:00Z"},
			wantIn:    "accepted_at_check",
		},
		{
			name:      "schema_version below one",
			overrides: map[string]any{"schema_version": 0},
			wantIn:    "schema_version_check",
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := seed(t, db)
			err := f.insertStoryArtifact("2000000"+itoa(i)+"-0000-7000-8000-00000000000f", tc.overrides)
			if err == nil {
				t.Fatal("the row was accepted; this constraint does not hold")
			}
			if tc.wantIn != "" && !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("failed on the wrong constraint\n got: %v\nwant: %s", err, tc.wantIn)
			}
		})
	}
}

// The amendment order must be total by construction: without uniqueness the
// effective view ("accepted amendments in sequence order") is ambiguous.
func TestAmendmentSequenceIsUnique(t *testing.T) {
	f := seed(t, openPlane(t))

	original := "20000000-0000-7000-8000-0000000000a1"
	if err := f.insertStoryArtifact(original, nil); err != nil {
		t.Fatalf("seed original: %v", err)
	}

	amendment := map[string]any{
		"amends_artifact_id": original,
		"status":             "accepted",
		"accepted_at":        "2026-01-01T00:00:00Z",
		"amendment_sequence": 1,
		"artifact_type":      "amendment",
	}
	if err := f.insertStoryArtifact("20000000-0000-7000-8000-0000000000a2", amendment); err != nil {
		t.Fatalf("first amendment rejected: %v", err)
	}
	err := f.insertStoryArtifact("20000000-0000-7000-8000-0000000000a3", amendment)
	if err == nil {
		t.Fatal("a duplicate amendment sequence was accepted; the effective view would be ambiguous")
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	roots, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cfg, err := stack.NewConfig(roots)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	rootKey, err := paths.EnsureKey(roots.Config)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	dsn, err := cfg.DSN(rootKey)
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}

	if err := migrations.Up(context.Background(), dsn); err != nil {
		t.Skipf("data plane unavailable: %v", err)
	}
	// Re-running is the everyday path: dataplane-up migrates every time.
	if err := migrations.Up(context.Background(), dsn); err != nil {
		t.Fatalf("second Up: %v", err)
	}

	version, dirty, err := migrations.Version(dsn)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if dirty {
		t.Error("schema is dirty after a clean migration")
	}
	if version == 0 {
		t.Error("schema version is 0 after migrating")
	}
}

// Lineage consistency, cross-organization safety, and lifecycle rules.
//
// Every case here was ACCEPTED by an earlier version of this schema, where
// each level referenced its parent by a single column: a Story whose Epic
// belonged to a different Feature, a repository whose primary Product lived
// in another organization, an artifact whose scope named one Story while
// its lineage named another. Composite foreign keys make them
// unrepresentable rather than merely discouraged.

func TestRepositoryPrimaryProductMustExistInSameOrganization(t *testing.T) {
	f := seed(t, openPlane(t))

	otherOrg := "40000000-0000-7000-8000-0000000000f1"
	otherProduct := "40000000-0000-7000-8000-0000000000f2"
	otherUser := "40000000-0000-7000-8000-0000000000e1"
	if _, err := f.tx.Exec(`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1,'other','Other')`, otherOrg); err != nil {
		t.Fatalf("seed other org: %v", err)
	}
	if _, err := f.tx.Exec(`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1,$2,'ou','OU')`, otherUser, otherOrg); err != nil {
		t.Fatalf("seed other user: %v", err)
	}
	if _, err := f.tx.Exec(`INSERT INTO products (product_id, organization_id, user_id, slug, display_name) VALUES ($1,$2,$3,'op','OP')`, otherProduct, otherOrg, otherUser); err != nil {
		t.Fatalf("seed other product: %v", err)
	}

	_, err := f.tx.Exec(
		`INSERT INTO repositories (repository_id, organization_id, primary_product_id, user_id, slug, display_name)
		 VALUES ($1,$2,$3,$4,'x','X')`,
		"40000000-0000-7000-8000-0000000000f3", f.org, otherProduct, f.user)
	if err == nil {
		t.Fatal("a repository took its primary Product from another organization")
	}
}

// "Exactly one primary Product", which a partial unique index on a flag
// could only ever make "at most one".
func TestRepositoryMustHaveAPrimaryProduct(t *testing.T) {
	f := seed(t, openPlane(t))

	_, err := f.tx.Exec(
		`INSERT INTO repositories (repository_id, organization_id, user_id, slug, display_name) VALUES ($1,$2,$3,'y','Y')`,
		"40000000-0000-7000-8000-0000000000f4", f.org, f.user)
	if err == nil {
		t.Fatal("a repository was created with no primary Product")
	}
}

func TestStoryCannotTakeAnEpicFromAnotherFeature(t *testing.T) {
	f := seed(t, openPlane(t))

	otherFeature := "40000000-0000-7000-8000-0000000000f5"
	if _, err := f.tx.Exec(
		`INSERT INTO features (feature_id, organization_id, user_id, product_id, title) VALUES ($1,$2,$3,$4,'F2')`,
		otherFeature, f.org, f.user, f.product); err != nil {
		t.Fatalf("seed other feature: %v", err)
	}

	// The Epic belongs to f.feature; this Story claims otherFeature.
	_, err := f.tx.Exec(
		`INSERT INTO stories (story_id, organization_id, user_id, product_id, feature_id, epic_id, title)
		 VALUES ($1,$2,$3,$4,$5,$6,'S2')`,
		"40000000-0000-7000-8000-0000000000f6", f.org, f.user, f.product, otherFeature, f.epic)
	if err == nil {
		t.Fatal("a Story claimed an Epic belonging to a different Feature")
	}
}

func TestScopeMustNameTheSameEntityAsLineage(t *testing.T) {
	f := seed(t, openPlane(t))

	otherStory := "40000000-0000-7000-8000-0000000000f7"
	if _, err := f.tx.Exec(
		`INSERT INTO stories (story_id, organization_id, user_id, product_id, feature_id, epic_id, title)
		 VALUES ($1,$2,$3,$4,$5,$6,'S3')`,
		otherStory, f.org, f.user, f.product, f.feature, f.epic); err != nil {
		t.Fatalf("seed other story: %v", err)
	}

	err := f.insertStoryArtifact("40000000-0000-7000-8000-0000000000f8",
		map[string]any{"scope_story_id": otherStory})
	if err == nil {
		t.Fatal("scope named one Story while lineage named another")
	}
	if !strings.Contains(err.Error(), "scope_matches_lineage") {
		t.Errorf("failed on the wrong constraint: %v", err)
	}
}

// accepted_at records that an artifact WAS accepted, so it must survive the
// terminal states. The earlier constraint tied it to status = 'accepted'
// exactly, which made supersession impossible.
func TestAcceptedAtSurvivesSupersession(t *testing.T) {
	f := seed(t, openPlane(t))

	id := "40000000-0000-7000-8000-0000000000f9"
	if err := f.insertStoryArtifact(id, map[string]any{
		"status":      "accepted",
		"accepted_at": "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("insert accepted artifact: %v", err)
	}

	for _, terminal := range []string{"superseded", "archived"} {
		if _, err := f.tx.Exec(`UPDATE management_artifacts SET status = $1 WHERE artifact_id = $2`, terminal, id); err != nil {
			t.Errorf("accepted -> %s was rejected, erasing the record that it was ever accepted: %v", terminal, err)
		}
	}
}

// The amendment chain is flat (ADR 0021): amendments target the original
// only. Self-amendment falls out of the same constraint.
func TestAmendmentChainIsFlat(t *testing.T) {
	f := seed(t, openPlane(t))

	original := "40000000-0000-7000-8000-0000000000fa"
	if err := f.insertStoryArtifact(original, nil); err != nil {
		t.Fatalf("seed original: %v", err)
	}

	first := "40000000-0000-7000-8000-0000000000fb"
	if err := f.insertStoryArtifact(first, map[string]any{
		"amends_artifact_id": original,
		"status":             "accepted",
		"accepted_at":        "2026-01-01T00:00:00Z",
		"amendment_sequence": 1,
	}); err != nil {
		t.Fatalf("first amendment rejected: %v", err)
	}

	err := f.insertStoryArtifact("40000000-0000-7000-8000-0000000000fc", map[string]any{
		"amends_artifact_id": first,
		"status":             "accepted",
		"accepted_at":        "2026-01-01T00:00:00Z",
		"amendment_sequence": 1,
	})
	if err == nil {
		t.Fatal("an amendment targeted another amendment; the chain is not flat")
	}
}

func TestArtifactCannotNameAnotherOrganizationsAuthor(t *testing.T) {
	f := seed(t, openPlane(t))

	otherOrg := "40000000-0000-7000-8000-0000000000fd"
	otherPrincipal := "40000000-0000-7000-8000-0000000000fe"
	if _, err := f.tx.Exec(`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1,'o3','O3')`, otherOrg); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := f.tx.Exec(
		`INSERT INTO principal_instances (principal_instance_id, organization_id, kind, model, agent_type)
		 VALUES ($1,$2,'agent','opus','coder')`, otherPrincipal, otherOrg); err != nil {
		t.Fatalf("seed principal: %v", err)
	}

	err := f.insertStoryArtifact("40000000-0000-7000-8000-0000000000ff",
		map[string]any{"author_instance_id": otherPrincipal})
	if err == nil {
		t.Fatal("an artifact was authored by a principal from another organization")
	}
}
