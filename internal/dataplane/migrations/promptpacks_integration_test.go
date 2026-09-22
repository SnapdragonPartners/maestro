//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"orchestrator/internal/dataplane/migrations"
)

// Tests for migration 000023 (Phase 3 item 4 design, D4 through D8).
//
// Four groups: the row constraints at the head, the reciprocal dispatch key
// under real commits, the up migration's guards and conversion over rows
// written under the OLD schema, and the down migration's refusals -- each
// guard proven by the exact defect it claims to catch, per the design's
// mutant table.

const (
	packScheme    = "pack-jcs-sha256-v1"
	legacyScheme  = "v1-manifest-sha256"
	phase3Lower   = "v2.0.0-phase.3.0.0"
	phase3Upper   = "v2.0.0-phase.4.0.0"
	contentInsert = `INSERT INTO prompt_pack_contents (content_id, organization_id, scheme, digest, entries)
	                 VALUES ($1,$2,$3,$4,$5::jsonb)`
	installInsert = `INSERT INTO prompt_pack_installations
	                   (installation_id, organization_id, content_id, display_name,
	                    min_maestro_version, max_maestro_version, declared_roles,
	                    installed_by_kind, installed_by_user_id, builtin_maestro_version,
	                    validated_maestro_version, revision)
	                 VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10,$11,$12)`
	principalInsert = `INSERT INTO principal_instances
	                     (principal_instance_id, organization_id, kind, model, agent_type, user_id,
	                      prompt_pack_origin, prompt_pack_name, prompt_pack_scheme, prompt_hash,
	                      prompt_pack_content_id, prompt_pack_installation_id,
	                      prompt_pack_installation_revision, prompt_pack_metadata_snapshot)
	                   VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb)`
)

// ppFixture is the shared transaction fixture plus one installed pack.
type ppFixture struct {
	*fixture
	content, installation string
}

// A DISPOSABLE database, not the shared plane openPlane migrates: that one is
// migrated once and is idempotent afterwards, so a change to 000023 -- or a
// mutant of it -- never reaches the schema a test on it observes. Every
// constraint case below runs against a schema this binary's SQL just built.
func seedPack(t *testing.T) *ppFixture {
	t.Helper()
	db, err := sql.Open("pgx", disposableDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := seed(t, db)
	p := &ppFixture{fixture: f,
		content:      "51000000-0000-7000-8000-000000000001",
		installation: "51000000-0000-7000-8000-000000000002"}
	if _, err := f.tx.Exec(contentInsert, p.content, f.org, packScheme, digestA, `{"coder.system":"x"}`); err != nil {
		t.Fatalf("seed content: %v", err)
	}
	p.mustInstall(t, p.installation, p.content, `{"coder": true}`)
	return p
}

func (p *ppFixture) mustInstall(t *testing.T, id, content, roles string) {
	t.Helper()
	if _, err := p.tx.Exec(installInsert, id, p.org, content, "fixture", phase3Lower, phase3Upper, roles,
		"builtin", nil, phase3Lower, phase3Lower, 1); err != nil {
		t.Fatalf("seed installation: %v", err)
	}
}

// --- row constraints -------------------------------------------------------

func TestPromptPackContentConstraints(t *testing.T) {
	p := seedPack(t)
	const id = "52000000-0000-7000-8000-000000000001"

	// The scheme is a closed enumeration of one: the legacy scheme is never
	// computed by the plane, and a third string is refused rather than
	// passed through.
	// A well-formed bare digest, so the scheme is the only thing wrong and
	// the digest-form check cannot be what refuses it.
	p.rejectsWith(t, "prompt_pack_contents_scheme_check",
		"a content row under the legacy scheme is a pack the plane claims to own and could not have digested",
		contentInsert, id, p.org, legacyScheme, digestB, `{}`)
	p.rejectsWith(t, "prompt_pack_contents_scheme_check",
		"an unknown scheme was passed through",
		contentInsert, id, p.org, "pack-jcs-sha256-v2", digestB, `{}`)
	p.rejectsWith(t, "prompt_pack_contents_digest_check",
		"a prefixed digest under the plane's scheme, whose form is bare hex",
		contentInsert, id, p.org, packScheme, "sha256:"+digestB, `{}`)
	p.rejectsWith(t, "prompt_pack_contents_digest_check",
		"uppercase hex", contentInsert, id, p.org, packScheme, strings.ToUpper(digestB), `{}`)
	for name, entries := range map[string]string{
		"array":         `[]`,
		"string":        `"x"`,
		"non-string":    `{"coder.system": 1}`,
		"nested":        `{"coder.system": {"text": "x"}}`,
		"empty key":     `{"": "x"}`,
		"null value":    `{"coder.system": null}`,
		"scalar object": `{"a": true}`,
	} {
		p.rejectsWith(t, "prompt_pack_contents_entries_check",
			"entries that are not a slot-to-text object: "+name,
			contentInsert, id, p.org, packScheme, digestB, entries)
	}
	p.rejectsWith(t, "prompt_pack_contents_identity_key",
		"a second row with the same identity in the same organization",
		contentInsert, id, p.org, packScheme, digestA, `{"coder.system":"x"}`)

	// Positive control: the empty pack, which is the built-in at item 4.
	if _, err := p.tx.Exec(contentInsert, id, p.org, packScheme, digestB, `{}`); err != nil {
		t.Fatalf("a well-formed empty content row was refused: %v", err)
	}
}

// TestPromptPackContentsAreImmutable: the schema's first trigger. UNIQUE
// refuses a second identity; only the trigger refuses a rewrite under the
// original primary key, whatever column it touches.
func TestPromptPackContentsAreImmutable(t *testing.T) {
	p := seedPack(t)
	for name, stmt := range map[string]string{
		"entries and digest together": `UPDATE prompt_pack_contents SET entries='{"coder.system":"y"}', digest=$2 WHERE content_id=$1`,
		"entries alone":               `UPDATE prompt_pack_contents SET entries='{"coder.system":"y"}' WHERE content_id=$1`,
		"a no-op rewrite":             `UPDATE prompt_pack_contents SET created_at=created_at WHERE content_id=$1`,
	} {
		args := []any{p.content}
		if strings.Contains(stmt, "$2") {
			args = append(args, digestB)
		}
		if _, err := p.tx.Exec("SAVEPOINT immutable"); err != nil {
			t.Fatal(err)
		}
		_, err := p.tx.Exec(stmt, args...)
		var pgErr *pgconn.PgError
		switch {
		case err == nil:
			t.Fatalf("%s: a content row was rewritten in place", name)
		case !errors.As(err, &pgErr):
			t.Fatalf("%s: non-Postgres error: %v", name, err)
		case pgErr.Code != "23000" || !strings.Contains(pgErr.Message, "immutable"):
			t.Fatalf("%s: refused, but not by the trigger: %s %s", name, pgErr.Code, pgErr.Message)
		}
		if _, err := p.tx.Exec("ROLLBACK TO SAVEPOINT immutable"); err != nil {
			t.Fatal(err)
		}
	}

	// Referenced content cannot go; unreferenced content can.
	p.rejectsWith(t, "prompt_pack_installations_content_fkey",
		"content with an installation was deleted from under it",
		`DELETE FROM prompt_pack_contents WHERE content_id=$1`, p.content)
	const orphan = "52000000-0000-7000-8000-000000000009"
	if _, err := p.tx.Exec(contentInsert, orphan, p.org, packScheme, digestB, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.tx.Exec(`DELETE FROM prompt_pack_contents WHERE content_id=$1`, orphan); err != nil {
		t.Fatalf("unreferenced content could not be deleted: %v", err)
	}
}

func TestPromptPackInstallationConstraints(t *testing.T) {
	p := seedPack(t)
	const (
		id       = "53000000-0000-7000-8000-000000000001"
		content2 = "53000000-0000-7000-8000-000000000002"
	)
	if _, err := p.tx.Exec(contentInsert, content2, p.org, packScheme, digestB, `{}`); err != nil {
		t.Fatal(err)
	}
	probe := func(constraint, because string, override map[string]any) {
		t.Helper()
		args := map[string]any{
			"id": id, "content": content2, "name": "fixture", "min": phase3Lower, "max": phase3Upper,
			"roles": `{"coder": true}`, "kind": "builtin", "user": nil, "builtin": phase3Lower,
			"validated": phase3Lower, "revision": 1,
		}
		for k, v := range override {
			args[k] = v
		}
		p.rejectsWith(t, constraint, because, installInsert,
			args["id"], p.org, args["content"], args["name"], args["min"], args["max"], args["roles"],
			args["kind"], args["user"], args["builtin"], args["validated"], args["revision"])
	}

	// Coverage is a set: identical keys, unequal objects (design D6).
	probe("prompt_pack_installations_declared_roles_check", `{"coder": 1} written directly`, map[string]any{"roles": `{"coder": 1}`})
	probe("prompt_pack_installations_declared_roles_check", "a role with an empty name", map[string]any{"roles": `{"": true}`})
	probe("prompt_pack_installations_declared_roles_check", "an array of roles", map[string]any{"roles": `["coder"]`})
	probe("prompt_pack_installations_declared_roles_check", "a false value", map[string]any{"roles": `{"coder": false}`})
	// The revision check, not the seam, is what refuses 0 and -1.
	probe("prompt_pack_installations_revision_check", "revision 0 written directly", map[string]any{"revision": 0})
	probe("prompt_pack_installations_revision_check", "revision -1 written directly", map[string]any{"revision": -1})
	// The governed installer.
	probe("prompt_pack_installations_installer_kind_check", "an installer kind outside the enumeration", map[string]any{"kind": "operator", "builtin": nil, "user": p.user})
	probe("prompt_pack_installations_installer_user_check", "kind user with no user", map[string]any{"kind": "user", "builtin": nil})
	probe("prompt_pack_installations_installer_user_check", "kind builtin carrying a user", map[string]any{"user": p.user})
	probe("prompt_pack_installations_installer_builtin_check", "kind builtin with no binary version", map[string]any{"builtin": nil})
	probe("prompt_pack_installations_installer_builtin_check", "kind user carrying a binary version", map[string]any{"kind": "user", "user": p.user})
	// The range's FORM; ordering is the seam's.
	probe("prompt_pack_installations_min_version_check", "a lower bound without its v", map[string]any{"min": "2.0.0"})
	probe("prompt_pack_installations_max_version_check", "an upper bound that is the dev sentinel", map[string]any{"max": "dev"})
	probe("prompt_pack_installations_validated_version_check", "a blank validated version", map[string]any{"validated": " "})
	probe("prompt_pack_installations_display_name_check", "a blank display name", map[string]any{"name": " \t"})
	// Cardinality: at most one installation per content.
	probe("prompt_pack_installations_one_per_content_key", "a second installation of the same content", map[string]any{"content": p.content})
	// Positive control: a user-installed pack.
	if _, err := p.tx.Exec(installInsert, id, p.org, content2, "second", phase3Lower, phase3Upper, `{}`,
		"user", p.user, nil, phase3Lower, 1); err != nil {
		t.Fatalf("a well-formed user installation was refused: %v", err)
	}
}

// TestPrincipalPromptShapeConstraint: each stray shape is refused by the
// disjunction BY NAME and not by a neighbouring rule (design D5). The
// NULL-origin row is the one a biconditional would have passed.
func TestPrincipalPromptShapeConstraint(t *testing.T) {
	p := seedPack(t)
	const id = "54000000-0000-7000-8000-000000000001"
	const shape = "principal_instances_prompt_pack_shape_check"
	const scheme = "principal_instances_prompt_pack_scheme_check"
	agent := "coder"
	legacyHash := "sha256:" + digestA

	type row struct {
		kind, model          string
		agentType, user      any
		origin, name, scheme any
		hash                 any
		content, install     any
		revision, snapshot   any
	}
	insert := func(r row) error {
		_, err := p.tx.Exec(principalInsert, id, p.org, r.kind, r.model, r.agentType, r.user,
			r.origin, r.name, r.scheme, r.hash, r.content, r.install, r.revision, r.snapshot)
		return err
	}
	rejects := func(constraint, because string, r row) {
		t.Helper()
		p.rejectsWith(t, constraint, because, principalInsert, id, p.org, r.kind, r.model, r.agentType, r.user,
			r.origin, r.name, r.scheme, r.hash, r.content, r.install, r.revision, r.snapshot)
	}

	foreign := row{kind: "agent", model: "opus", agentType: agent,
		origin: "foreign", name: "default", scheme: legacyScheme, hash: legacyHash}
	resolved := row{kind: "agent", model: "opus", agentType: agent,
		origin: "resolved", name: "fixture", scheme: packScheme, hash: digestA,
		content: p.content, install: p.installation, revision: 1, snapshot: `{}`}

	// Stray shapes.
	rejects(shape, "a system principal carrying a pack name",
		row{kind: "system", model: "system-x", name: "default"})
	rejects(shape, "a human principal carrying a hash",
		row{kind: "human", model: "human-u", user: p.user, hash: legacyHash})
	rejects(shape, "a foreign agent carrying one reference",
		func() row { r := foreign; r.revision = 1; return r }())
	// The NULL-origin row is refused twice over -- the per-scheme enumeration
	// guards on origin too -- so the sibling is dropped inside a savepoint
	// and the disjunction is shown to refuse it ALONE, which is what design
	// D5 requires of it: a biconditional would have passed this row.
	if _, err := p.tx.Exec("SAVEPOINT without_sibling"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.tx.Exec(`ALTER TABLE principal_instances DROP CONSTRAINT ` + scheme); err != nil {
		t.Fatal(err)
	}
	rejects(shape, "an agent with a NULL origin -- the row a biconditional passes",
		func() row { r := foreign; r.origin = nil; return r }())
	if _, err := p.tx.Exec("ROLLBACK TO SAVEPOINT without_sibling"); err != nil {
		t.Fatal(err)
	}
	rejects(shape, "an agent with no prompt identity at all",
		row{kind: "agent", model: "opus", agentType: agent})
	rejects(shape, "a resolved agent with no references",
		func() row { r := resolved; r.content, r.install, r.revision, r.snapshot = nil, nil, nil, nil; return r }())
	rejects(shape, "a resolved agent missing one reference",
		func() row { r := resolved; r.snapshot = nil; return r }())
	rejects("principal_instances_prompt_pack_origin_check", "an origin outside the enumeration",
		func() row { r := foreign; r.origin = "imported"; return r }())

	// The per-scheme enumeration.
	rejects(scheme, "a foreign agent under the plane's scheme: an identity with no references",
		func() row { r := foreign; r.scheme, r.hash = packScheme, digestA; return r }())
	rejects(scheme, "a resolved agent under the legacy scheme",
		func() row { r := resolved; r.scheme, r.hash = legacyScheme, legacyHash; return r }())
	rejects(scheme, "a third scheme string",
		func() row { r := foreign; r.scheme = "v2-manifest-sha256"; return r }())
	rejects(scheme, "a bare-hex digest under the legacy scheme",
		func() row { r := foreign; r.hash = digestA; return r }())
	rejects(scheme, "a prefixed digest under the plane's scheme",
		func() row { r := resolved; r.hash = "sha256:" + digestA; return r }())
	rejects("principal_instances_prompt_pack_name_check", "a blank name",
		func() row { r := foreign; r.name = " "; return r }())
	rejects("principal_instances_prompt_pack_revision_check", "revision 0 on a principal",
		func() row { r := resolved; r.revision = 0; return r }())
	rejects("principal_instances_prompt_pack_snapshot_check", "a snapshot that is not an object",
		func() row { r := resolved; r.snapshot = `[]`; return r }())

	// The identity-binding references (design D5): content A with digest B,
	// and an installation of some other content, both satisfied an
	// organization-only key and are refused here.
	rejects("principal_instances_prompt_pack_content_fkey", "content A recorded with digest B",
		func() row { r := resolved; r.hash = digestB; return r }())
	const content2, install2 = "54000000-0000-7000-8000-000000000002", "54000000-0000-7000-8000-000000000003"
	if _, err := p.tx.Exec(contentInsert, content2, p.org, packScheme, digestB, `{}`); err != nil {
		t.Fatal(err)
	}
	p.mustInstall(t, install2, content2, `{}`)
	rejects("principal_instances_prompt_pack_installation_fkey", "an installation of other content",
		func() row { r := resolved; r.install = install2; return r }())

	// Positive controls, one per agent shape.
	if err := insert(foreign); err != nil {
		t.Fatalf("a well-formed foreign agent was refused: %v", err)
	}
	if _, err := p.tx.Exec(`DELETE FROM principal_instances WHERE principal_instance_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := insert(resolved); err != nil {
		t.Fatalf("a well-formed resolved agent was refused: %v", err)
	}
	// And the referenced rows now cannot go.
	p.rejectsWith(t, "principal_instances_prompt_pack_installation_fkey",
		"an installation a live principal names was deleted",
		`DELETE FROM prompt_pack_installations WHERE installation_id=$1`, p.installation)
}

// --- the reciprocal key, under real commits --------------------------------

// TestDispatchAndResolutionAreBoundBothWays: design D8's four refused
// statements, each on the reciprocal constraint BY NAME. Real commits,
// because the dispatch's reference is DEFERRED and only a commit checks it.
func TestDispatchAndResolutionAreBoundBothWays(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabase(t)
	db, openErr := sql.Open("pgx", dsn)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = db.Close() }()

	f := seedForBackfill(t, db)
	_, product, feature, epic, story := seedExecutionForPlane(t, db, f) // the positive control: dispatch + resolution, committed

	var dispatch, storyPlan, epicPlan string
	if err := db.QueryRow(`SELECT story_dispatch_id, story_version_artifact_id, epic_version_artifact_id
	                        FROM story_dispatches`).Scan(&dispatch, &storyPlan, &epicPlan); err != nil {
		t.Fatal(err)
	}
	const reciprocal = "story_dispatches_prompt_resolution_fkey"
	expectConstraint := func(because, constraint string, err error) {
		t.Helper()
		var pgErr *pgconn.PgError
		switch {
		case err == nil:
			t.Fatalf("%s: succeeded", because)
		case !errors.As(err, &pgErr):
			t.Fatalf("%s: non-Postgres error: %v", because, err)
		case pgErr.ConstraintName != constraint:
			t.Fatalf("%s: refused by %q, want %q", because, pgErr.ConstraintName, constraint)
		}
	}

	// Steady state: the resolution a dispatch names can neither go nor
	// change identity from under it.
	_, deleteErr := db.Exec(`DELETE FROM dispatch_prompt_resolutions WHERE story_dispatch_id=$1`, dispatch)
	expectConstraint("deleting a resolution a dispatch names", reciprocal, deleteErr)
	_, repointErr := db.Exec(`UPDATE dispatch_prompt_resolutions SET resolution_id=$2 WHERE story_dispatch_id=$1`,
		dispatch, "55000000-0000-7000-8000-0000000000ee")
	expectConstraint("changing a resolution's id under its dispatch", reciprocal, repointErr)

	// Insert time: a dispatch naming a resolution that never arrives fails
	// at COMMIT, on the deferred pair.
	newDispatch := `INSERT INTO story_dispatches
	    (story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id, work_group_id,
	     disposition, story_version_artifact_id, story_version_effective_digest, story_version_effective_sequence,
	     epic_version_artifact_id, epic_version_effective_digest, epic_version_effective_sequence,
	     prompt_resolution_id)
	  SELECT $1, organization_id, product_id, feature_id, epic_id, story_id, work_group_id,
	         'pending', $2, $3, 0, $4, $5, 0, $6
	    FROM story_dispatches WHERE story_dispatch_id=$7`
	const orphan = "55000000-0000-7000-8000-000000000001"
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(newDispatch, orphan, storyPlan, digestA, epicPlan, digestB,
		"55000000-0000-7000-8000-000000000002", dispatch); err != nil {
		t.Fatalf("the orphan insert failed BEFORE commit, so the pair is not deferred: %v", err)
	}
	expectConstraint("committing a dispatch with no resolution", reciprocal, tx.Commit())

	// And a writer that predates the column cannot omit it: the same
	// statement as a pre-000023 seam would issue.
	oldCode := `INSERT INTO story_dispatches
	    (story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id, work_group_id,
	     disposition, story_version_artifact_id, story_version_effective_digest, story_version_effective_sequence,
	     epic_version_artifact_id, epic_version_effective_digest, epic_version_effective_sequence)
	  SELECT $1, organization_id, product_id, feature_id, epic_id, story_id, work_group_id,
	         'pending', $2, $3, 0, $4, $5, 0
	    FROM story_dispatches WHERE story_dispatch_id=$6`
	_, err = db.Exec(oldCode, orphan, storyPlan, digestA, epicPlan, digestB, dispatch)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23502" || pgErr.ColumnName != "prompt_resolution_id" {
		t.Fatalf("an old-code insert omitting the column: %v, want NOT NULL on prompt_resolution_id", err)
	}

	// Parent-first insertion in one transaction is the shape that commits.
	const second, secondRes = "55000000-0000-7000-8000-000000000003", "75000000-0000-7000-8000-000000000003"
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(newDispatch, second, storyPlan, digestA, epicPlan, digestB, secondRes, dispatch); err != nil {
		t.Fatal(err)
	}
	seedPromptResolution(t, tx, dispatchLineage{dispatch: second, org: f.org, product: product,
		feature: feature, epic: epic, story: story})
	if err := tx.Commit(); err != nil {
		t.Fatalf("dispatch then resolution, one transaction: %v", err)
	}
}

// --- the up migration over rows written under the old schema ---------------

const unconvertibleAgentID = "56000000-0000-7000-8000-000000000001"

// oldAgent writes an agent under the pre-000023 schema with the given
// identity columns, nil for NULL.
func oldAgent(t *testing.T, db *sql.DB, org string, packID, hash any) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO principal_instances
	    (principal_instance_id, organization_id, kind, model, agent_type, prompt_pack_id, prompt_hash)
	  VALUES ($1,$2,'agent','opus','coder',$3,$4)`, unconvertibleAgentID, org, packID, hash); err != nil {
		t.Fatalf("seed old agent: %v", err)
	}
}

func TestPromptPacksUpConvertsForeignAgentsTotally(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabaseAt(t, 22)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	f := seedForBackfill(t, db) // one agent, legacy identity

	if err := migrations.Up(ctx, dsn); err != nil {
		t.Fatalf("Up over a convertible plane: %v", err)
	}
	var origin, name, scheme, hash string
	var refs int
	if err := db.QueryRow(`SELECT prompt_pack_origin, prompt_pack_name, prompt_pack_scheme, prompt_hash,
	        num_nonnulls(prompt_pack_content_id, prompt_pack_installation_id,
	                     prompt_pack_installation_revision, prompt_pack_metadata_snapshot)
	   FROM principal_instances WHERE principal_instance_id=$1`, f.principal).
		Scan(&origin, &name, &scheme, &hash, &refs); err != nil {
		t.Fatal(err)
	}
	if origin != "foreign" || name != fixturePackName || scheme != legacyScheme || hash != fixturePromptHash || refs != 0 {
		t.Fatalf("converted row = origin %q name %q scheme %q hash %q refs %d; digest bytes must be unchanged "+
			"and the prefix kept as data under the legacy scheme", origin, name, scheme, hash, refs)
	}
	if hasColumn(t, db, "principal_instances", "prompt_pack_id") {
		t.Fatal("prompt_pack_id survived the split")
	}
}

// TestPromptPacksUpRefusesUnconvertibleRows: the guard itself, classified
// over what the OLD schema permits. Each case asserts the class count and
// remedy text, so a mutant that removes the guard and dies at the later
// shape constraint is caught: the constraint's message is not the guard's.
func TestPromptPacksUpRefusesUnconvertibleRows(t *testing.T) {
	ctx := context.Background()
	const sysID = "56000000-0000-7000-8000-000000000002"
	for name, tc := range map[string]struct {
		seed func(t *testing.T, db *sql.DB, org string)
		want string
	}{
		"agent with a NULL hash": {func(t *testing.T, db *sql.DB, org string) {
			oldAgent(t, db, org, "default", nil)
		}, "1 agent principal(s) have a missing or invalid prompt identity"},
		"agent with a NULL name": {func(t *testing.T, db *sql.DB, org string) {
			oldAgent(t, db, org, nil, fixturePromptHash)
		}, "1 agent principal(s) have"},
		"agent with a blank name": {func(t *testing.T, db *sql.DB, org string) {
			oldAgent(t, db, org, "  ", fixturePromptHash)
		}, "1 agent principal(s) have"},
		"agent with an empty hash -- passes IS NOT NULL, must not pass the guard": {func(t *testing.T, db *sql.DB, org string) {
			oldAgent(t, db, org, "default", "")
		}, "1 agent principal(s) have"},
		"agent with a bare-hex hash": {func(t *testing.T, db *sql.DB, org string) {
			oldAgent(t, db, org, "default", digestA)
		}, "1 agent principal(s) have"},
		"system principal carrying a pack name": {func(t *testing.T, db *sql.DB, org string) {
			if _, err := db.Exec(`INSERT INTO principal_instances
			    (principal_instance_id, organization_id, kind, model, prompt_pack_id)
			  VALUES ($1,$2,'system','system-x','default')`, sysID, org); err != nil {
				t.Fatal(err)
			}
		}, "0 agent principal(s) have a missing or invalid prompt identity"},
		"one of each class": {func(t *testing.T, db *sql.DB, org string) {
			oldAgent(t, db, org, nil, nil)
			if _, err := db.Exec(`INSERT INTO principal_instances
			    (principal_instance_id, organization_id, kind, model, prompt_hash)
			  VALUES ($1,$2,'system','system-x',$3)`, sysID, org, fixturePromptHash); err != nil {
				t.Fatal(err)
			}
		}, "1 agent principal(s) have"},
	} {
		t.Run(name, func(t *testing.T) {
			dsn := disposableDatabaseAt(t, 22)
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			f := seedForBackfill(t, db)
			tc.seed(t, db, f.org)

			err = migrations.Up(ctx, dsn)
			if err == nil {
				t.Fatal("000023 applied over a row it cannot honestly convert")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refused, but not by the guard's classification: %v", err)
			}
			wantSecond := "0 non-agent principal(s) carry prompt fields"
			if strings.Contains(name, "system") || strings.Contains(name, "each") {
				wantSecond = "1 non-agent principal(s) carry prompt fields"
			}
			if !strings.Contains(err.Error(), wantSecond) {
				t.Fatalf("the second class was miscounted: want %q in %v", wantSecond, err)
			}
			if !strings.Contains(err.Error(), "make dataplane-force-version VERSION=22 FORCE=1") {
				t.Fatalf("the refusal does not carry its remedy: %v", err)
			}
		})
	}
}

func TestPromptPacksUpRefusesExistingDispatchesAndRecovers(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabaseAt(t, 22)
	db, openErr := sql.Open("pgx", dsn)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = db.Close() }()
	f := seedForBackfill(t, db)
	execution, _, _, _, _ := seedExecutionForPlane(t, db, f) //nolint:dogsled // at 22: a dispatch with no resolution; only its execution is needed

	upErr := migrations.Up(ctx, dsn)
	if upErr == nil || !strings.Contains(upErr.Error(), "1 story dispatch(es) predate prompt-pack resolution") {
		t.Fatalf("Up over a pre-000023 dispatch = %v, want the dispatch-rows guard", upErr)
	}

	// The recovery the message documents, walked. The metadata says 23 and
	// dirty while the schema is really at 22.
	version, dirty, err := migrations.Version(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if version != 23 || !dirty {
		t.Fatalf("after the refusal the recorded version is %d dirty=%v; the instruction assumes 23 dirty", version, dirty)
	}
	if hasColumn(t, db, "principal_instances", "prompt_pack_origin") {
		t.Fatal("the refusal left the schema partially applied; it must roll back whole")
	}
	for _, stmt := range []string{
		`DELETE FROM executions WHERE execution_id = '` + execution + `'`,
		`DELETE FROM story_dispatches`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrations.Up(ctx, dsn); err == nil {
		t.Fatal("Up succeeded from a dirty version; the two-step instruction is over-stated")
	}
	if stepErr := migrations.Force(dsn, 22); stepErr != nil {
		t.Fatal(stepErr)
	}
	if stepErr := migrations.Up(ctx, dsn); stepErr != nil {
		t.Fatalf("the documented recovery did not work: %v", stepErr)
	}
	version, dirty, err = migrations.Version(dsn)
	if err != nil || version != 23 || dirty {
		t.Fatalf("after recovery: version %d dirty=%v err=%v, want 23 clean", version, dirty, err)
	}
}

// TestPromptPacksUpLocksBeforeItScans, on 000022's forced-interleaving
// pattern: an agent with no identity is held uncommitted, the migration is
// started and observed BLOCKED on principal_instances, and only then does the
// writer commit. With the lock first the guard sees the row and refuses with
// ITS message. Without it the scan sees nothing, the ALTER waits for the
// writer, and the migration then dies on the shape constraint -- for the
// wrong reason, which the message assertion distinguishes.
func TestPromptPacksUpLocksBeforeItScans(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabaseAt(t, 22)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	f := seedForBackfill(t, db)

	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback() }()
	if _, err := writer.ExecContext(ctx, `INSERT INTO principal_instances
	    (principal_instance_id, organization_id, kind, model, agent_type)
	  VALUES ($1,$2,'agent','opus','coder')`, "57000000-0000-7000-8000-000000000001", f.org); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- migrations.Up(ctx, dsn) }()
	if err := waitForBlockedLock(t, db, "principal_instances"); err != nil {
		t.Fatalf("the migration never blocked on principal_instances, so it did not take the lock before "+
			"scanning: %v", err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		switch {
		case err == nil:
			t.Fatal("the migration applied while a concurrent writer added an agent with no identity")
		case !strings.Contains(err.Error(), "1 agent principal(s) have a missing or invalid prompt identity"):
			t.Fatalf("the migration failed, but not by the guard observing the concurrent row: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the migration did not finish after the writer committed")
	}
}

// --- the down migration ----------------------------------------------------

func TestPromptPacksDownRefusesPlaneOwnedState(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		seed func(t *testing.T, db *sql.DB, f planeFixture)
		want string
	}{
		"a resolved principal": {func(t *testing.T, db *sql.DB, f planeFixture) {
			seedPromptResolutionRows(t, db, f.org)
			if _, err := db.Exec(principalInsert, "58000000-0000-7000-8000-000000000001", f.org, "agent", "opus", "coder", nil,
				"resolved", "fixture", packScheme, fixtureContentDigest,
				fixtureContentID(f.org), fixtureInstallationID(f.org), 1, `{}`); err != nil {
				t.Fatal(err)
			}
		}, "1 principal(s) carry a resolved prompt pack"},
		"a dispatch resolution": {func(t *testing.T, db *sql.DB, f planeFixture) {
			seedExecutionForPlane(t, db, f)
		}, "1 dispatch(es) carry a prompt-pack resolution"},
		"a prompt.pack selector": {func(t *testing.T, db *sql.DB, f planeFixture) {
			if _, err := db.Exec(`INSERT INTO configuration_records
			    (configuration_record_id, organization_id, key, scope_type, scope_organization_id, value)
			  VALUES ($1,$2,'prompt.pack','organization',$2,'{}')`, "58000000-0000-7000-8000-000000000002", f.org); err != nil {
				t.Fatal(err)
			}
		}, "1 prompt.pack selector(s)"},
		"an installation": {func(t *testing.T, db *sql.DB, f planeFixture) {
			seedPromptResolutionRows(t, db, f.org)
		}, "1 prompt pack installation(s)"},
		"content alone": {func(t *testing.T, db *sql.DB, f planeFixture) {
			if _, err := db.Exec(contentInsert, fixtureContentID(f.org), f.org, packScheme, digestA, `{}`); err != nil {
				t.Fatal(err)
			}
		}, "1 prompt pack content record(s)"},
	} {
		t.Run(name, func(t *testing.T) {
			dsn := disposableDatabase(t)
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			f := seedForBackfill(t, db)
			tc.seed(t, db, f)

			err = migrations.To(ctx, dsn, 22)
			if err == nil {
				t.Fatal("000023 reversed over plane-owned state")
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "VERSION=23 FORCE=1") {
				t.Fatalf("refused, but not by the expected class with its remedy: %v", err)
			}
			version, dirty, err := migrations.Version(dsn)
			if err != nil || version != 22 || !dirty {
				t.Fatalf("after the refusal: version %d dirty=%v err=%v; the remedy assumes 22 dirty", version, dirty, err)
			}
			if !hasColumn(t, db, "principal_instances", "prompt_pack_origin") {
				t.Fatal("the refusal left the schema partially reversed")
			}
		})
	}
}

// seedPromptResolutionRows writes the content and installation the fixture
// resolution would name, without a dispatch.
func seedPromptResolutionRows(t *testing.T, db *sql.DB, org string) {
	t.Helper()
	if _, err := db.Exec(contentInsert, fixtureContentID(org), org, packScheme, fixtureContentDigest, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(installInsert, fixtureInstallationID(org), org, fixtureContentID(org), "fixture",
		phase3Lower, phase3Upper, `{}`, "builtin", nil, phase3Lower, phase3Lower, 1); err != nil {
		t.Fatal(err)
	}
}

// TestPromptPacksDownRecoversAndRestoresTheLegacyColumn: the documented
// recovery in the down direction, then a successful reversal over a plane
// holding only foreign principals, which is the one expressible case.
func TestPromptPacksDownRecoversAndRestoresTheLegacyColumn(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabase(t)
	db, openErr := sql.Open("pgx", dsn)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = db.Close() }()
	f := seedForBackfill(t, db)
	if _, err := db.Exec(contentInsert, fixtureContentID(f.org), f.org, packScheme, digestA, `{}`); err != nil {
		t.Fatal(err)
	}

	if err := migrations.To(ctx, dsn, 22); err == nil {
		t.Fatal("reversed over a content row")
	}
	if _, err := db.Exec(`DELETE FROM prompt_pack_contents`); err != nil {
		t.Fatal(err)
	}
	if err := migrations.To(ctx, dsn, 22); err == nil {
		t.Fatal("reversing succeeded from a dirty version; the two-step instruction is over-stated")
	}
	if stepErr := migrations.Force(dsn, 23); stepErr != nil {
		t.Fatal(stepErr)
	}
	if stepErr := migrations.To(ctx, dsn, 22); stepErr != nil {
		t.Fatalf("the documented recovery did not work: %v", stepErr)
	}

	var packID, hash string
	if err := db.QueryRow(`SELECT prompt_pack_id, prompt_hash FROM principal_instances WHERE principal_instance_id=$1`,
		f.principal).Scan(&packID, &hash); err != nil {
		t.Fatal(err)
	}
	if packID != fixturePackName || hash != fixturePromptHash {
		t.Fatalf("after reversal the foreign principal reads %q %q, want the name and the untouched digest", packID, hash)
	}
	for _, leftover := range []string{"prompt_pack_contents_refuse_update", "prompt_pack_roles_canonical", "prompt_pack_entries_canonical"} {
		var present bool
		if stepErr := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname=$1)`, leftover).Scan(&present); stepErr != nil {
			t.Fatal(stepErr)
		}
		if present {
			t.Fatalf("function %s survived the reversal", leftover)
		}
	}
	version, dirty, err := migrations.Version(dsn)
	if err != nil || version != 22 || dirty {
		t.Fatalf("after recovery: version %d dirty=%v err=%v, want 22 clean", version, dirty, err)
	}
}

// TestPromptPacksDownLocksBeforeItScans: a prompt.pack selector held
// uncommitted; the reversal must block on configuration_records before it
// looks, then refuse. Without the lock the scan sees nothing, the tables are
// dropped, and the selector commits pointing at content that no longer
// exists -- the reversal succeeds and this test fails.
func TestPromptPacksDownLocksBeforeItScans(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabase(t)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	f := seedForBackfill(t, db)

	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback() }()
	if _, err := writer.ExecContext(ctx, `INSERT INTO configuration_records
	    (configuration_record_id, organization_id, key, scope_type, scope_organization_id, value)
	  VALUES ($1,$2,'prompt.pack','organization',$2,'{}')`, "59000000-0000-7000-8000-000000000001", f.org); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- migrations.To(ctx, dsn, 22) }()
	if err := waitForBlockedLock(t, db, "configuration_records"); err != nil {
		t.Fatalf("the reversal never blocked on configuration_records: %v", err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		switch {
		case err == nil:
			t.Fatal("the reversal committed while a concurrent writer seeded a prompt.pack selector, which now dangles")
		case !strings.Contains(err.Error(), "1 prompt.pack selector(s)"):
			t.Fatalf("the reversal failed, but not by observing the concurrent row: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the reversal did not finish after the writer committed")
	}
}

// --- the lock order, read from the files -----------------------------------

// The family's fixed order (design D5). Each direction locks what it scans,
// alters or drops, in this order, so two migrations can never deadlock each
// other.
var promptPackLockOrder = []string{
	"prompt_pack_contents",
	"prompt_pack_installations",
	"configuration_records",
	"story_dispatches",
	"dispatch_prompt_resolutions",
	"principal_instances",
}

func TestPromptPacksLockOrderMatchesTheFamilyOrder(t *testing.T) {
	lockStmt := regexp.MustCompile(`(?m)^LOCK TABLE\s+(\w+)\s+IN ACCESS EXCLUSIVE MODE;`)
	for file, want := range map[string][]string{
		"000023_prompt_packs.up.sql":   {"story_dispatches", "principal_instances"},
		"000023_prompt_packs.down.sql": promptPackLockOrder,
	} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range lockStmt.FindAllStringSubmatch(string(raw), -1) {
			got = append(got, m[1])
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s locks %v, want %v", file, got, want)
		}
		// And the locks come before the first scan.
		firstLock := strings.Index(string(raw), "LOCK TABLE")
		firstScan := strings.Index(string(raw), "SELECT count(*)")
		if firstLock < 0 || firstScan < 0 || firstLock > firstScan {
			t.Errorf("%s scans before it locks", file)
		}
	}
}
