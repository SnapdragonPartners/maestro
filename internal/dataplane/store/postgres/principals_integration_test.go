//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
	"orchestrator/internal/orchestrator"
)

// The three principal writers (item 4 design, D5): one per shape of the
// prompt-pack columns, and no caller chooses the origin. The general path's
// refusal of agents is covered beside its other kind rules in
// TestPrincipalKindFieldRulesAreEnforcedAtTheSeam.

// TestDispatchedPrincipalCopiesTheResolution: a live agent's pack is the
// persisted resolution of its execution's dispatch, field for field, and
// its lineage and Maestro version are derived rather than supplied.
//
// THE MUTANT: have the INSERT ... SELECT take resolved_name from the
// installation's current display_name instead of the resolution's
// resolved_name (a join to prompt_pack_installations). The second half of
// this test updates the installation between the dispatch and a second
// principal, and the mutant copies the NEW name -- the principal now claims
// the dispatch was decided on a fact that did not yet exist, which is
// exactly what ADR 0031 section 1 forbids.
func TestDispatchedPrincipalCopiesTheResolution(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	input := f.dispatchedInput(t)
	hash := "harness-config-hash"
	input.HarnessConfigHash = &hash
	created, err := f.store.CreateDispatchedPrincipalInstance(ctx, input)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	execution, err := f.store.GetExecutionByDispatch(ctx, f.organizationID, mustExecutionDispatch(t, f, input.ExecutionID))
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := f.store.GetDispatch(ctx, f.organizationID, execution.StoryDispatchID)
	if err != nil {
		t.Fatal(err)
	}
	resolution := dispatch.PromptResolution

	// Read back rather than trusting the returned struct: the question is
	// what the DATABASE holds.
	stored, err := f.store.GetPrincipalInstance(ctx, f.organizationID, created.PrincipalInstanceID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Kind != store.PrincipalAgent || stored.AgentType == nil || *stored.AgentType != "coder" {
		t.Fatalf("kind/agent type: %+v", stored)
	}
	if stored.PromptPack == nil {
		t.Fatal("a dispatched principal carries no prompt pack")
	}
	want := store.PrincipalPromptPack{
		Origin:   store.PromptPackOriginResolved,
		Name:     resolution.ResolvedName,
		Identity: resolution.Identity,
		Resolution: &store.PrincipalPromptPackResolution{
			Snapshot:             resolution.Snapshot,
			InstallationRevision: resolution.InstallationRevision,
			ContentID:            resolution.ContentID,
			InstallationID:       resolution.InstallationID,
		},
	}
	if !reflect.DeepEqual(*stored.PromptPack, want) {
		t.Fatalf("prompt pack =\n  %+v\nwant the resolution's\n  %+v", *stored.PromptPack, want)
	}
	if stored.PromptPack.Identity.Scheme != store.PromptSchemePackJCS {
		t.Fatalf("scheme %q; a resolved principal is under the plane's own scheme", stored.PromptPack.Identity.Scheme)
	}

	// Lineage is the execution's, not the caller's -- the input has none.
	gotLineage := store.Lineage{ProductID: stored.Lineage.ProductID, FeatureID: stored.Lineage.FeatureID, EpicID: stored.Lineage.EpicID, StoryID: stored.Lineage.StoryID}
	wantLineage := store.Lineage{ProductID: &execution.ProductID, FeatureID: &execution.FeatureID, EpicID: &execution.EpicID, StoryID: &execution.StoryID}
	if !reflect.DeepEqual(gotLineage, wantLineage) {
		t.Fatalf("lineage %s, want the execution's %s", describeLineage(gotLineage), describeLineage(wantLineage))
	}
	// The Maestro version is the running harness's, read from the seam's
	// one authority for it (design D3).
	if stored.MaestroVersion == nil || *stored.MaestroVersion != planetest.HarnessVersion {
		t.Fatalf("maestro_version = %v, want the running harness %q", stored.MaestroVersion, planetest.HarnessVersion)
	}
	if stored.HarnessConfigHash == nil || *stored.HarnessConfigHash != hash {
		t.Fatalf("harness_config_hash = %v, want the supplied %q", stored.HarnessConfigHash, hash)
	}
	if stored.StopTime != nil || stored.StopReason != nil {
		t.Fatal("a dispatched principal was created closed")
	}

	// --- historical, not current -------------------------------------------
	//
	// Update the installation. A later principal under the SAME execution
	// still copies what the resolution recorded, because the resolution is
	// what decided the dispatch and the installation is what it says now.
	packs := packStoreWithBuiltin(t, f)
	installed, err := packs.GetPromptPackByContent(ctx, f.organizationID, resolution.ContentID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := packs.UpdatePromptPackInstallation(ctx, store.UpdatePromptPackInstallationInput{
		DisplayName:       "renamed after dispatch",
		MinMaestroVersion: installed.Installation.MinMaestroVersion,
		MaxMaestroVersion: installed.Installation.MaxMaestroVersion,
		DeclaredRoles:     installed.Installation.DeclaredRoles,
		ExpectedRevision:  installed.Installation.Revision,
		OrganizationID:    f.organizationID,
		InstallationID:    resolution.InstallationID,
	})
	if err != nil {
		t.Fatalf("update the installation: %v", err)
	}
	if updated.DisplayName == resolution.ResolvedName || updated.Revision == resolution.InstallationRevision {
		t.Fatalf("the update did not move the installation (name %q, revision %d); the historical assertion below would be vacuous",
			updated.DisplayName, updated.Revision)
	}

	replacement, err := f.store.CreateDispatchedPrincipalInstance(ctx, input)
	if err != nil {
		t.Fatalf("create a second principal under the same execution: %v", err)
	}
	stored, err = f.store.GetPrincipalInstance(ctx, f.organizationID, replacement.PrincipalInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PromptPack.Name != resolution.ResolvedName {
		t.Fatalf("name = %q after the installation was renamed; want the resolution's %q -- the principal was copied from the installation, not the resolution",
			stored.PromptPack.Name, resolution.ResolvedName)
	}
	if stored.PromptPack.Resolution.InstallationRevision != resolution.InstallationRevision {
		t.Fatalf("revision = %d, want the resolution's %d", stored.PromptPack.Resolution.InstallationRevision, resolution.InstallationRevision)
	}
	if stored.PromptPack.Resolution.Snapshot.DisplayName != resolution.Snapshot.DisplayName {
		t.Fatalf("snapshot display name = %q, want the resolution's %q", stored.PromptPack.Resolution.Snapshot.DisplayName, resolution.Snapshot.DisplayName)
	}
}

// mustExecutionDispatch finds the dispatch an execution belongs to, going
// through the fixture's pool because the seam reads executions by dispatch
// and not the other way round.
func mustExecutionDispatch(t *testing.T, f *fixture, executionID uuid.UUID) uuid.UUID {
	t.Helper()
	var dispatchID uuid.UUID
	if err := f.pool.QueryRow(context.Background(),
		`SELECT story_dispatch_id FROM executions WHERE execution_id = $1`, executionID).Scan(&dispatchID); err != nil {
		t.Fatalf("find the execution's dispatch: %v", err)
	}
	return dispatchID
}

func describeLineage(l store.Lineage) string {
	show := func(id *uuid.UUID) string {
		if id == nil {
			return "<nil>"
		}
		return id.String()
	}
	return "{" + show(l.ProductID) + " " + show(l.FeatureID) + " " + show(l.EpicID) + " " + show(l.StoryID) + "}"
}

// packStoreWithBuiltin is the fixture's store composed the way
// seedPromptPack composes it, so the built-in's installation can be updated
// through the same contract that installed it.
func packStoreWithBuiltin(t *testing.T, f *fixture) *postgres.Store {
	t.Helper()
	s, err := postgres.New(f.pool, testRegistry(t), f.blob, f.rootKey, planetest.Harness(t),
		postgres.WithConfigKeys(orchestrator.Keys()), postgres.WithPromptContract(orchestrator.Prompts()))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestDispatchedPrincipalNeedsAnExecutionInTheOrganization: the execution
// is looked up under the organization, so a foreign or invented execution
// is ErrNotFound, and nothing is written.
func TestDispatchedPrincipalNeedsAnExecutionInTheOrganization(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	valid := f.dispatchedInput(t)
	before := f.countInstances(t)

	invented := valid
	invented.ExecutionID = uuid.New()
	if _, err := f.store.CreateDispatchedPrincipalInstance(ctx, invented); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invented execution: err = %v, want ErrNotFound", err)
	}

	// A real execution, asked for from the other organization.
	foreign := valid
	foreign.OrganizationID = f.otherOrgID
	if _, err := f.store.CreateDispatchedPrincipalInstance(ctx, foreign); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other organization's execution: err = %v, want ErrNotFound", err)
	}

	blank := valid
	blank.AgentType = " "
	if _, err := f.store.CreateDispatchedPrincipalInstance(ctx, blank); err == nil || strings.Contains(err.Error(), "SQLSTATE") {
		t.Fatalf("blank agent type: err = %v, want a seam refusal", err)
	}

	if after := f.countInstances(t); after != before {
		t.Fatalf("instance count went %d -> %d on refused creates", before, after)
	}

	// The positive control, so the refusals above are not "refuse everything".
	if _, err := f.store.CreateDispatchedPrincipalInstance(ctx, valid); err != nil {
		t.Fatalf("the valid input was refused: %v", err)
	}
}

// TestForeignAgentPrincipalRoundTripsAndIsRefusedAtTheSeam covers the
// import path: the recorded shape, and every refusal the verb owns,
// diagnosed before the row exists.
//
// THE MUTANTS, one per refusal: skip the lifetime check (a live agent
// recorded as foreign -- ADR 0031 section 2's "only for imports" violated by
// the verb that exists to honour it; the zero-lifetime case is the one the
// schema does NOT catch, since year 1 is a legal timestamp); admit the
// plane's scheme (a plane-owned identity with no content behind it, which the
// schema refuses -- the SQLSTATE assertion is what tells the seam's refusal
// from the schema's); admit a bare-hex digest under the legacy scheme.
func TestForeignAgentPrincipalRoundTripsAndIsRefusedAtTheSeam(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	created, err := f.store.RecordForeignAgentPrincipal(ctx, f.foreignInput())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	stored, err := f.store.GetPrincipalInstance(ctx, f.organizationID, created.PrincipalInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	want := store.PrincipalPromptPack{
		Origin:   store.PromptPackOriginForeign,
		Name:     fixturePackName,
		Identity: store.PromptIdentity{Scheme: store.PromptSchemeV1Manifest, Digest: fixturePromptHash},
	}
	if stored.PromptPack == nil || !reflect.DeepEqual(*stored.PromptPack, want) {
		t.Fatalf("prompt pack = %+v, want %+v", stored.PromptPack, want)
	}
	if stored.StopTime == nil || !stored.StopTime.Equal(fixtureLifetime.StopTime) {
		t.Fatalf("a foreign principal arrived open or with the wrong stop: %v", stored.StopTime)
	}

	before := f.countInstances(t)
	bare := strings.TrimPrefix(fixturePromptHash, "sha256:")
	for _, testCase := range []struct {
		name   string
		mutate func(*store.RecordForeignAgentPrincipalInput)
		want   string
	}{
		{"zero lifetime", func(in *store.RecordForeignAgentPrincipalInput) { in.Lifetime = store.RecordedLifetime{} }, "start time"},
		{"open lifetime", func(in *store.RecordForeignAgentPrincipalInput) { in.Lifetime.StopTime = time.Time{} }, "stop time"},
		{"blank agent type", func(in *store.RecordForeignAgentPrincipalInput) { in.AgentType = "" }, "agent type"},
		{"blank pack name", func(in *store.RecordForeignAgentPrincipalInput) { in.Pack.Name = " \t" }, "name"},
		{"the plane's own scheme", func(in *store.RecordForeignAgentPrincipalInput) {
			in.Pack.Scheme, in.Pack.Digest = store.PromptSchemePackJCS, bare
		}, "legacy-scheme"},
		{"an unknown scheme", func(in *store.RecordForeignAgentPrincipalInput) { in.Pack.Scheme = "sha1-of-something" }, "legacy-scheme"},
		{"bare hex under the legacy scheme", func(in *store.RecordForeignAgentPrincipalInput) { in.Pack.Digest = bare }, "storage form"},
		{"the plane's form with the prefix doubled", func(in *store.RecordForeignAgentPrincipalInput) { in.Pack.Digest = "sha256:" + fixturePromptHash }, "storage form"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := f.foreignInput()
			testCase.mutate(&input)
			_, err := f.store.RecordForeignAgentPrincipal(ctx, input)
			if err == nil {
				t.Fatal("expected the seam to refuse this input")
			}
			if strings.Contains(err.Error(), "SQLSTATE") {
				t.Fatalf("refused by the database rather than the seam: %v", err)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error %q does not say what was wrong (want %q)", err, testCase.want)
			}
		})
	}
	if after := f.countInstances(t); after != before {
		t.Fatalf("instance count went %d -> %d on refused records", before, after)
	}
}

// TestMPHQueryGroupsWithinAScheme is design D4's proof at the row level,
// now that both shapes have a writer: a foreign identity and a resolved
// identity never group, even when asked by digest alone would find both.
func TestMPHQueryGroupsWithinAScheme(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	live := f.dispatchedAgent(t)
	foreignInput := f.foreignInput()
	// The same hex as the live principal's digest, under the legacy scheme
	// with its prefix: the pair a digest-only query would conflate.
	foreignInput.Pack.Digest = "sha256:" + live.PromptPack.Identity.Digest
	foreign, err := f.store.RecordForeignAgentPrincipal(ctx, foreignInput)
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	for name, expect := range map[string]struct {
		identity store.PromptIdentity
		want     uuid.UUID
	}{
		"the plane's scheme": {live.PromptPack.Identity, live.PrincipalInstanceID},
		"the legacy scheme":  {foreign.PromptPack.Identity, foreign.PrincipalInstanceID},
	} {
		identity := expect.identity
		found, err := f.store.FindPrincipalInstances(ctx, store.MPHQuery{OrganizationID: f.organizationID, PromptIdentity: &identity})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(found) != 1 || found[0].PrincipalInstanceID != expect.want {
			t.Fatalf("%s: found %d instances, want exactly %s", name, len(found), expect.want)
		}
	}
}
