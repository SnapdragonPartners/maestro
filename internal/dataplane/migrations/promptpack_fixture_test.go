//go:build integration

package migrations_test

import (
	"database/sql"
	"strings"
	"testing"
)

// Fixture helpers for the principal and dispatch shapes migration 000023
// requires (Phase 3 item 4 design, D5 and D8).
//
// After 000023 an agent principal MUST carry a prompt identity and a dispatch
// MUST name its resolution. Fixtures in this package seed both at whatever
// version their test runs -- some at the latest, some stopped before an
// earlier migration to exercise its backfill -- so the helpers here read the
// schema and write the shape it holds. Below 000023 they write the single
// prompt_pack_id column, which is also what lets 000023's own conversion be
// exercised by the tests that then migrate up.

// The legacy identity every pre-000023 agent fixture carries: the importer's
// content-identity form, which is the only form any writer ever stored.
const (
	fixturePackName   = "default"
	fixturePromptHash = "sha256:" + digestA
)

// The plane-owned identity the resolution fixtures install. Ids are DERIVED
// from the organization and the dispatch -- one content and one installation
// per organization, one resolution per dispatch -- so fixtures holding two
// organizations, or two dispatches, do not collide on a primary key and then
// fail on the reference that follows.
const fixtureContentDigest = digestA

func fixtureContentID(org string) string      { return "5" + org[1:] }
func fixtureInstallationID(org string) string { return "6" + org[1:] }
func fixtureResolutionID(dispatch string) string {
	return "7" + dispatch[1:]
}

// execer is what both a *sql.DB and a *sql.Tx can do.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// hasColumn reports whether the schema this connection sees holds a column.
func hasColumn(t *testing.T, db execer, table, column string) bool {
	t.Helper()
	var present bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
	                        WHERE table_name = $1 AND column_name = $2)`, table, column).Scan(&present); err != nil {
		t.Fatalf("inspect %s.%s: %v", table, column, err)
	}
	return present
}

// atPromptPacks reports whether 000023 has been applied.
func atPromptPacks(t *testing.T, db execer) bool {
	t.Helper()
	return hasColumn(t, db, "principal_instances", "prompt_pack_origin")
}

// insertAgentPrincipal writes one agent principal in the shape the schema
// holds: the foreign shape at 000023 and later, the single column before it.
func insertAgentPrincipal(t *testing.T, db execer, id, org, model string) {
	t.Helper()
	var err error
	if atPromptPacks(t, db) {
		_, err = db.Exec(`INSERT INTO principal_instances
		    (principal_instance_id, organization_id, kind, model, agent_type,
		     prompt_pack_origin, prompt_pack_name, prompt_pack_scheme, prompt_hash)
		  VALUES ($1,$2,'agent',$3,'coder','foreign',$4,'v1-manifest-sha256',$5)`,
			id, org, model, fixturePackName, fixturePromptHash)
	} else {
		_, err = db.Exec(`INSERT INTO principal_instances
		    (principal_instance_id, organization_id, kind, model, agent_type, prompt_pack_id, prompt_hash)
		  VALUES ($1,$2,'agent',$3,'coder',$4,$5)`,
			id, org, model, fixturePackName, fixturePromptHash)
	}
	if err != nil {
		t.Fatalf("seed agent principal %s: %v", id, err)
	}
}

// dispatchLineage is what a resolution repeats from its dispatch.
type dispatchLineage struct {
	dispatch, org, product, feature, epic, story string
}

// seedPromptResolution installs a content record and an installation in the
// organization if they are absent, then writes the dispatch's resolution.
// The dispatch row must already exist in this transaction, because the
// resolution's reference to it is NOT deferred; the dispatch's reference
// back is, and is satisfied by this write at commit.
//
// Below 000023 there is nothing to write, and this returns.
func seedPromptResolution(t *testing.T, db execer, l dispatchLineage) {
	t.Helper()
	if !atPromptPacks(t, db) {
		return
	}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO prompt_pack_contents (content_id, organization_id, scheme, digest, entries)
		  VALUES ($1,$2,'pack-jcs-sha256-v1',$3,'{}'::jsonb)
		  ON CONFLICT DO NOTHING`, []any{fixtureContentID(l.org), l.org, fixtureContentDigest}},
		{`INSERT INTO prompt_pack_installations
		    (installation_id, organization_id, content_id, display_name,
		     min_maestro_version, max_maestro_version, declared_roles,
		     installed_by_kind, builtin_maestro_version, validated_maestro_version, revision)
		  VALUES ($1,$2,$3,'fixture','v2.0.0-phase.3.0.0','v2.0.0-phase.4.0.0','{}'::jsonb,
		          'builtin','v2.0.0-phase.3.0.0','v2.0.0-phase.3.0.0',1)
		  ON CONFLICT DO NOTHING`, []any{fixtureInstallationID(l.org), l.org, fixtureContentID(l.org)}},
		{`INSERT INTO dispatch_prompt_resolutions
		    (resolution_id, story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id,
		     resolved_name, scheme, digest, content_id, installation_id, installation_revision,
		     metadata_snapshot, validated_maestro_version, range_check)
		  VALUES ($1,$2,$3,$4,$5,$6,$7,'fixture','pack-jcs-sha256-v1',$8,$9,$10,1,'{}'::jsonb,
		          'v2.0.0-phase.3.0.0','passed')`,
			[]any{fixtureResolutionID(l.dispatch), l.dispatch, l.org, l.product, l.feature, l.epic, l.story,
				fixtureContentDigest, fixtureContentID(l.org), fixtureInstallationID(l.org)}},
	} {
		if _, err := db.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed prompt resolution %q: %v", firstLine(stmt.sql), err)
		}
	}
}

// promptResolutionColumn returns the column-list and value-list fragments a
// dispatch INSERT needs at this schema version, so one statement serves both
// sides of 000023. The value is the resolution id seedPromptResolution
// writes for that dispatch afterwards.
func promptResolutionColumn(t *testing.T, db execer, dispatch string) (column, value string) {
	t.Helper()
	if !hasColumn(t, db, "story_dispatches", "prompt_resolution_id") {
		return "", ""
	}
	return ", prompt_resolution_id", ", '" + fixtureResolutionID(dispatch) + "'"
}

func firstLine(sql string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(sql), "\n")
	return line
}
