//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/harness"
	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
	"orchestrator/internal/orchestrator"
	"orchestrator/internal/prompt"
)

// Organization provisioning and the import-and-select verb through the
// seam (Phase 3 item 4 design, D2, D9, D11).
//
// Every pack here is LOADED: a fstest.MapFS through orchestrator.LoadBuiltin,
// the function the composition root calls on the embed. That is design D2's
// requirement and the structure test in internal/orchestrator enforces it --
// a hand-built store.BuiltinPromptPack in this file would fail that test
// before it could pass this one.

// fixturePackFS is a pack with content: two slots, one declared role. The
// entries reference only what fixturePrompts' slots supply.
func fixturePackFS(displayName string, entries map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{
		prompt.ManifestFile: {Data: []byte(`{"display_name":"` + displayName + `","maestro_version":{"min":"` +
			phase3Lower + `","max":"` + phase3Upper + `"},"roles":["coder"]}`)},
	}
	for slot, text := range entries {
		fsys[prompt.EntriesDir+"/"+slot+prompt.EntryExtension] = &fstest.MapFile{Data: []byte(text)}
	}
	return fsys
}

func loadPack(t *testing.T, fsys fstest.MapFS) store.BuiltinPromptPack {
	t.Helper()
	loaded, err := orchestrator.LoadBuiltin(fsys)
	if err != nil {
		t.Fatalf("load the fixture pack: %v", err)
	}
	return loaded
}

// packA and packB are two distinct built-ins, as two binary versions would
// carry: B is what an upgrade brings.
func packA(t *testing.T) store.BuiltinPromptPack {
	t.Helper()
	return loadPack(t, fixturePackFS("pack A", validPack()))
}

func packB(t *testing.T) store.BuiltinPromptPack {
	t.Helper()
	return loadPack(t, fixturePackFS("pack B", map[string]string{
		slotCoderSystem: "Version B: you are working on {{.Story}}.\n",
		slotCoderPlan:   "Plan first, then act.",
	}))
}

// provisioningStore is a store composed as the Orchestrator's root composes
// it -- its key vocabulary -- over the FIXTURE slot registry, so the packs
// above are admitted.
func provisioningStore(t *testing.T, f *fixture) *postgres.Store {
	t.Helper()
	s, err := postgres.New(f.pool, testRegistry(t), f.blob, f.rootKey, planetest.Harness(t),
		postgres.WithConfigKeys(orchestrator.Keys()), postgres.WithPromptContract(fixturePrompts(t)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *fixture) countInstallations(t *testing.T, s *postgres.Store) int {
	t.Helper()
	installations, err := s.ListPromptPackInstallations(context.Background(), f.organizationID)
	if err != nil {
		t.Fatal(err)
	}
	return len(installations)
}

func TestProvisionOrganizationPromptPackWritesAllThreeAndIsIdempotent(t *testing.T) {
	f := newFixture(t)
	s := provisioningStore(t, f)
	ctx := context.Background()
	pack := packA(t)

	first, err := s.ProvisionOrganizationPromptPack(ctx, f.organizationID, pack)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !first.Created {
		t.Fatal("first provisioning reported Created=false")
	}
	selection := first.Record
	// The content is the loaded fixture's, digested by the seam, and the
	// installer is the COMPOSITION's version, which the loaded pack never
	// carried.
	wantDigest, _ := prompt.Digest(pack.Entries)
	switch {
	case selection.Pack.Content.Digest != wantDigest:
		t.Fatalf("digest %s, want the fixture's %s", selection.Pack.Content.Digest, wantDigest)
	case len(selection.Pack.Content.Entries) != 2:
		t.Fatalf("entries %v: the loaded content did not reach the row", selection.Pack.Content.Entries)
	case selection.Pack.Installation.DisplayName != "pack A":
		t.Fatalf("display name %q", selection.Pack.Installation.DisplayName)
	case selection.Pack.Installation.Installer.Kind != store.PromptPackInstalledByBuiltin ||
		selection.Pack.Installation.Installer.BuiltinMaestroVersion != planetest.HarnessVersion:
		t.Fatalf("installer %+v, want built-in at the composition's %s", selection.Pack.Installation.Installer, planetest.HarnessVersion)
	case selection.Selector.Key != store.PromptPackKey || selection.Selector.Scope.Type != configkeys.ScopeOrganization ||
		selection.Selector.Scope.ID != f.organizationID || selection.Selector.Version != 1:
		t.Fatalf("selector %+v", selection.Selector)
	}
	named, err := store.ParsePromptSelector(selection.Selector.Value)
	if err != nil || named.ContentID == nil || *named.ContentID != selection.Pack.Content.ContentID {
		t.Fatalf("selector value %s names %v (%v), want content %s", selection.Selector.Value, named, err, selection.Pack.Content.ContentID)
	}

	// The read verb resolves it to the same rows.
	read, err := s.GetOrganizationPromptPackSelection(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("read the selection: %v", err)
	}
	if read.Pack.Installation.InstallationID != selection.Pack.Installation.InstallationID || read.Selector.ID != selection.Selector.ID {
		t.Fatalf("read back %+v", read)
	}

	// Idempotent: the same call again writes nothing and reports so.
	again, err := s.ProvisionOrganizationPromptPack(ctx, f.organizationID, pack)
	if err != nil {
		t.Fatalf("re-provision: %v", err)
	}
	if again.Created || again.Record.Selector.ID != selection.Selector.ID || again.Record.Selector.Version != 1 {
		t.Fatalf("re-provision: Created=%v selector %s v%d", again.Created, again.Record.Selector.ID, again.Record.Selector.Version)
	}
	if n := f.countInstallations(t, s); n != 1 {
		t.Fatalf("%d installations after two provisionings", n)
	}
}

// TestProvisionKeepsAnExistingSelectorAndImportsNothing is design D9's
// "upgrades move nothing": a newer binary provisioning an organization
// that already has a selector leaves it, and does not even install its own
// built-in beside it.
func TestProvisionKeepsAnExistingSelectorAndImportsNothing(t *testing.T) {
	f := newFixture(t)
	s := provisioningStore(t, f)
	ctx := context.Background()

	seeded, err := s.ProvisionOrganizationPromptPack(ctx, f.organizationID, packA(t))
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := s.ProvisionOrganizationPromptPack(ctx, f.organizationID, packB(t))
	if err != nil {
		t.Fatalf("provision under the upgraded binary: %v", err)
	}
	switch {
	case upgraded.Created:
		t.Fatal("an upgrade provisioning reported Created=true")
	case upgraded.Record.Pack.Content.ContentID != seeded.Record.Pack.Content.ContentID:
		t.Fatalf("the selection moved to %s", upgraded.Record.Pack.Content.ContentID)
	case upgraded.Record.Pack.Installation.DisplayName != "pack A":
		t.Fatalf("selection is %q", upgraded.Record.Pack.Installation.DisplayName)
	}
	if n := f.countInstallations(t, s); n != 1 {
		t.Fatalf("%d installations: the upgraded binary's built-in was imported without being selected", n)
	}
}

// TestProvisionIsAllThreeWritesOrNone: a fault after the install and before
// the selector must leave no content and no installation behind. The fault
// is real machinery -- a store whose key registry does not declare
// prompt.pack refuses the selector write -- so the mutant that splits the
// verb into three transactions commits two rows here and fails.
func TestProvisionIsAllThreeWritesOrNone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	noSelectorKey, err := postgres.New(f.pool, testRegistry(t), f.blob, f.rootKey, planetest.Harness(t),
		postgres.WithConfigKeys(configkeys.MustNew(nil)), postgres.WithPromptContract(fixturePrompts(t)))
	if err != nil {
		t.Fatal(err)
	}

	_, err = noSelectorKey.ProvisionOrganizationPromptPack(ctx, f.organizationID, packA(t))
	if !errors.Is(err, configkeys.ErrUnknownKey) {
		t.Fatalf("err = %v, want the key registry's refusal", err)
	}
	if contents, installations := f.countRows(t, "prompt_pack_contents"), f.countRows(t, "prompt_pack_installations"); contents != 0 || installations != 0 {
		t.Fatalf("%d content rows and %d installations survive a failed provisioning: the three writes are not one transaction",
			contents, installations)
	}
	if _, err := noSelectorKey.GetOrganizationPromptPackSelection(ctx, f.organizationID); !errors.Is(err, store.ErrNoPromptSelector) {
		t.Fatalf("selection = %v, want ErrNoPromptSelector", err)
	}

	// And the gate's refusal, before any write: a pack referencing a
	// variable its slot does not supply.
	s := provisioningStore(t, f)
	bad := loadPack(t, fixturePackFS("bad", map[string]string{slotCoderSystem: "{{.Nope}}", slotCoderPlan: "x"}))
	if _, err := s.ProvisionOrganizationPromptPack(ctx, f.organizationID, bad); !errors.Is(err, store.ErrPromptPackRefused) {
		t.Fatalf("err = %v, want ErrPromptPackRefused", err)
	}
	if contents := f.countRows(t, "prompt_pack_contents"); contents != 0 {
		t.Fatalf("%d content rows after a refused pack", contents)
	}

	// The positive control: the same store, a valid pack, all three rows.
	if _, err := s.ProvisionOrganizationPromptPack(ctx, f.organizationID, packA(t)); err != nil {
		t.Fatalf("the control failed: %v", err)
	}
	if contents, installations, selectors := f.countRows(t, "prompt_pack_contents"), f.countRows(t, "prompt_pack_installations"),
		f.countRows(t, "configuration_records"); contents != 1 || installations != 1 || selectors != 1 {
		t.Fatalf("control wrote %d/%d/%d rows", contents, installations, selectors)
	}
}

func TestProvisionRefusesAMissingOrganizationAndReportsADanglingSelector(t *testing.T) {
	f := newFixture(t)
	s := provisioningStore(t, f)
	ctx := context.Background()

	if _, err := s.ProvisionOrganizationPromptPack(ctx, uuid.New(), packA(t)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	// A selector written by hand naming content no row carries: reported,
	// with the record, and NOT repaired by provisioning.
	stray := uuid.New()
	if _, err := s.CreateConfigurationRecord(ctx, store.CreateConfigurationRecordInput{
		Value: selectorValue(t, stray), Key: store.PromptPackKey,
		Scope:          store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID},
		OrganizationID: f.organizationID,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.ProvisionOrganizationPromptPack(ctx, f.organizationID, packA(t))
	var unresolved *store.PromptSelectorUnresolved
	if !errors.As(err, &unresolved) || !errors.Is(err, store.ErrPromptSelectorUnresolved) {
		t.Fatalf("err = %v, want PromptSelectorUnresolved", err)
	}
	if unresolved.Names.ContentID == nil || *unresolved.Names.ContentID != stray || unresolved.Selector.Version != 1 {
		t.Fatalf("unresolved carries %+v", unresolved)
	}
	if n := f.countInstallations(t, s); n != 0 {
		t.Fatalf("%d installations: provisioning imported a pack under a dangling selector", n)
	}
}

// TestConcurrentProvisionersSeedOneSelector: N provisioners of one
// organization, at once. Exactly one creates; every one returns the same
// rows; one installation and one record exist. The organization lock is
// what serialises the read-then-seed, and without it two callers each find
// no selector and the second's CreateConfigurationRecord fails on the
// unique constraint -- a raw driver error, neither created nor existing.
func TestConcurrentProvisionersSeedOneSelector(t *testing.T) {
	f := newFixture(t)
	s := provisioningStore(t, f)
	ctx := context.Background()
	pack := packA(t)

	const provisioners = 8
	results := make([]store.Bootstrapped[store.PromptPackSelection], provisioners)
	errs := make([]error, provisioners)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range provisioners {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			results[i], errs[i] = s.ProvisionOrganizationPromptPack(ctx, f.organizationID, pack)
		}()
	}
	start.Done()
	done.Wait()

	created := 0
	for i := range provisioners {
		if errs[i] != nil {
			t.Fatalf("provisioner %d: %v", i, errs[i])
		}
		if results[i].Created {
			created++
		}
		if results[i].Record.Selector.ID != results[0].Record.Selector.ID ||
			results[i].Record.Pack.Installation.InstallationID != results[0].Record.Pack.Installation.InstallationID {
			t.Fatalf("provisioner %d saw different rows", i)
		}
	}
	if created != 1 {
		t.Fatalf("%d provisioners reported Created=true, want exactly 1", created)
	}
	if installations, selectors := f.countRows(t, "prompt_pack_installations"), f.countRows(t, "configuration_records"); installations != 1 || selectors != 1 {
		t.Fatalf("%d installations, %d selectors", installations, selectors)
	}
}

func TestSelectBuiltinMovesTheSelectorUnderItsVersion(t *testing.T) {
	f := newFixture(t)
	s := provisioningStore(t, f)
	ctx := context.Background()

	seeded, err := s.ProvisionOrganizationPromptPack(ctx, f.organizationID, packA(t))
	if err != nil {
		t.Fatal(err)
	}
	b := packB(t)
	moved, err := s.SelectBuiltinPromptPack(ctx, f.organizationID, b, seeded.Record.Token())
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	wantDigest, _ := prompt.Digest(b.Entries)
	switch {
	case !moved.Moved:
		t.Fatal("Moved=false after a move")
	case moved.Selection.Pack.Content.Digest != wantDigest:
		t.Fatalf("selected %s, want pack B %s", moved.Selection.Pack.Content.Digest, wantDigest)
	case moved.Selection.Selector.ID != seeded.Record.Selector.ID:
		t.Fatal("the selector is a different record: the verb updates, never re-creates")
	case moved.Selection.Selector.Version != 2:
		t.Fatalf("selector version %d, want 2", moved.Selection.Selector.Version)
	}
	// Both are installed; A is kept, B is selected.
	if n := f.countInstallations(t, s); n != 2 {
		t.Fatalf("%d installations", n)
	}
	read, err := s.GetOrganizationPromptPackSelection(ctx, f.organizationID)
	if err != nil || read.Pack.Content.Digest != wantDigest {
		t.Fatalf("selection %+v, %v", read, err)
	}

	// Selecting what is already selected moves nothing and bumps nothing.
	same, err := s.SelectBuiltinPromptPack(ctx, f.organizationID, b, moved.Selection.Token())
	if err != nil {
		t.Fatalf("re-select: %v", err)
	}
	if same.Moved || same.Selection.Selector.Version != 2 {
		t.Fatalf("re-select: Moved=%v version %d", same.Moved, same.Selection.Selector.Version)
	}
}

// TestSelectBuiltinRefusesAStaleVersion is the ADR 0027 row: two
// operators, and the second's read is stale. The mutant that makes the
// update unconditional overwrites the first operator's move and passes the
// rest of this file. And nothing is installed by the refused call -- which
// is the transaction's doing: a mutant that installed BEFORE classifying
// survived this test, because the conflict rolls the install back either
// way. The installation count below is therefore the atomicity claim, not
// an ordering claim.
func TestSelectBuiltinRefusesAStaleVersion(t *testing.T) {
	f := newFixture(t)
	s := provisioningStore(t, f)
	ctx := context.Background()

	seeded, err := s.ProvisionOrganizationPromptPack(ctx, f.organizationID, packA(t))
	if err != nil {
		t.Fatal(err)
	}
	stale := seeded.Record.Token()

	// Operator one moves the selector -- to a third pack, so the stale
	// operator's B is provably absent afterwards.
	c := loadPack(t, fixturePackFS("pack C", map[string]string{slotCoderSystem: "C {{.Story}}", slotCoderPlan: "C"}))
	if _, err := s.SelectBuiltinPromptPack(ctx, f.organizationID, c, stale); err != nil {
		t.Fatal(err)
	}
	before := f.countInstallations(t, s)

	// Operator two, holding the version from before the move.
	_, err = s.SelectBuiltinPromptPack(ctx, f.organizationID, packB(t), stale)
	if !errors.Is(err, store.ErrConfigurationConflict) {
		t.Fatalf("err = %v, want ErrConfigurationConflict", err)
	}
	if n := f.countInstallations(t, s); n != before {
		t.Fatalf("%d installations after a refused select, was %d: the refused call installed its pack", n, before)
	}
	read, err := s.GetOrganizationPromptPackSelection(ctx, f.organizationID)
	if err != nil || read.Pack.Installation.DisplayName != "pack C" {
		t.Fatalf("selection is %q (%v), want operator one's pack C", read.Pack.Installation.DisplayName, err)
	}
}

func TestSelectBuiltinNeedsASelectorAndRepairsADanglingOne(t *testing.T) {
	f := newFixture(t)
	s := provisioningStore(t, f)
	ctx := context.Background()

	// Never provisioned: seeding is provisioning's act.
	_, err := s.SelectBuiltinPromptPack(ctx, f.organizationID, packA(t), store.PromptSelectorToken{RecordID: uuid.New(), Version: 1})
	if !errors.Is(err, store.ErrNoPromptSelector) {
		t.Fatalf("err = %v, want ErrNoPromptSelector", err)
	}
	if n := f.countInstallations(t, s); n != 0 {
		t.Fatalf("%d installations after a refused select", n)
	}

	// Dangling: the record exists, so the verb moves it.
	stray := uuid.New()
	record, err := s.CreateConfigurationRecord(ctx, store.CreateConfigurationRecordInput{
		Value: selectorValue(t, stray), Key: store.PromptPackKey,
		Scope:          store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID},
		OrganizationID: f.organizationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := s.SelectBuiltinPromptPack(ctx, f.organizationID, packA(t), store.PromptSelectorToken{RecordID: record.ID, Version: record.Version})
	if err != nil {
		t.Fatalf("select over a dangling selector: %v", err)
	}
	if !repaired.Moved || repaired.Selection.Pack.Installation.DisplayName != "pack A" || repaired.Selection.Selector.Version != 2 {
		t.Fatalf("repaired: %+v", repaired)
	}
}

// TestSelectBuiltinRefusesAReplacedSelectorRecord (review round 6, P1): a
// selector deleted and recreated is a DIFFERENT record that starts again at
// version 1. A caller holding the original's "version 1" must not match
// it: the token is the record identity and the version together, or a
// stale operator overwrites a selection they never read.
func TestSelectBuiltinRefusesAReplacedSelectorRecord(t *testing.T) {
	f := newFixture(t)
	s := provisioningStore(t, f)
	ctx := context.Background()

	seeded, err := s.ProvisionOrganizationPromptPack(ctx, f.organizationID, packA(t))
	if err != nil {
		t.Fatal(err)
	}
	stale := seeded.Record.Token()

	// Another caller deletes the selector and recreates it, at version 1,
	// naming pack C -- both supported configuration operations.
	if err := s.DeleteConfigurationRecord(ctx, f.organizationID, stale.RecordID, stale.Version); err != nil {
		t.Fatal(err)
	}
	c, err := s.InstallPromptPack(ctx, f.installInput(map[string]string{slotCoderSystem: "C {{.Story}}", slotCoderPlan: "C"}, "coder"))
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := s.CreateConfigurationRecord(ctx, store.CreateConfigurationRecordInput{
		Value: selectorValue(t, c.Record.Content.ContentID), Key: store.PromptPackKey,
		Scope:          store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID},
		OrganizationID: f.organizationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Version != stale.Version || replacement.ID == stale.RecordID {
		t.Fatalf("the replacement is record %s v%d; the scenario needs the same version on a different record", replacement.ID, replacement.Version)
	}

	// The stale operator, holding the ORIGINAL record's token.
	_, err = s.SelectBuiltinPromptPack(ctx, f.organizationID, packB(t), stale)
	if !errors.Is(err, store.ErrConfigurationConflict) {
		t.Fatalf("err = %v, want ErrConfigurationConflict", err)
	}
	read, err := s.GetOrganizationPromptPackSelection(ctx, f.organizationID)
	if err != nil || read.Pack.Content.ContentID != c.Record.Content.ContentID || read.Selector.Version != 1 {
		t.Fatalf("selection %+v (%v), want the replacement's pack C untouched at version 1", read, err)
	}

	// The control: a caller who read the replacement moves it.
	moved, err := s.SelectBuiltinPromptPack(ctx, f.organizationID, packB(t), read.Token())
	if err != nil || !moved.Moved {
		t.Fatalf("the control failed: %+v, %v", moved, err)
	}
}

// TestTheProductionBuiltinProvisionsAResolvableSelector is Checkpoint 1's
// sentence through the seam: the EMBEDDED built-in, loaded as the root
// loads it, provisioned under the Orchestrator's own empty registry, and a
// dispatch resolving through the selector it seeded.
func TestTheProductionBuiltinProvisionsAResolvableSelector(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s, err := postgres.New(f.pool, testRegistry(t), f.blob, f.rootKey, planetest.Harness(t),
		postgres.WithConfigKeys(orchestrator.Keys()), postgres.WithPromptContract(orchestrator.Prompts()))
	if err != nil {
		t.Fatal(err)
	}
	builtin, err := orchestrator.LoadBuiltin(orchestrator.BuiltinPack())
	if err != nil {
		t.Fatal(err)
	}
	provisioned, err := s.ProvisionOrganizationPromptPack(ctx, f.organizationID, builtin)
	if err != nil {
		t.Fatalf("provision the production built-in: %v", err)
	}
	if !provisioned.Created || len(provisioned.Record.Pack.Content.Entries) != 0 || len(provisioned.Record.Pack.Installation.DeclaredRoles) != 0 {
		t.Fatalf("provisioned %+v", provisioned.Record.Pack)
	}

	g := provisionGoverned(t, f) // its own seeding finds the selector above and keeps it
	dispatch, err := s.CreateDispatch(ctx, f.organizationID, g.story.StoryID, nil)
	if err != nil {
		t.Fatalf("dispatch through the seeded selector: %v", err)
	}
	if dispatch.PromptResolution.ContentID != provisioned.Record.Pack.Content.ContentID {
		t.Fatalf("dispatch resolved %s, want the provisioned built-in %s", dispatch.PromptResolution.ContentID, provisioned.Record.Pack.Content.ContentID)
	}
	// Resolvable, and not executable: the built-in declares no role, and the
	// snapshot says so rather than claiming coverage.
	if len(dispatch.PromptResolution.Snapshot.DeclaredRoles) != 0 {
		t.Fatalf("the built-in's snapshot declares roles %v", dispatch.PromptResolution.Snapshot.DeclaredRoles)
	}
}

// TestProvisionRecordsTheCompositionVersionNotTheCallers: the installer
// version on a provisioned installation is the harness the store was
// composed with. A second store composed with a different version
// provisioning a fresh organization records ITS version -- and the loaded
// pack, which is shared, carries neither.
func TestProvisionRecordsTheCompositionVersionNotTheCallers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	other, err := harness.Parse("v2.0.0-phase.3.0.1")
	if err != nil {
		t.Fatal(err)
	}
	s, err := postgres.New(f.pool, testRegistry(t), f.blob, f.rootKey, other,
		postgres.WithConfigKeys(orchestrator.Keys()), postgres.WithPromptContract(fixturePrompts(t)))
	if err != nil {
		t.Fatal(err)
	}
	provisioned, err := s.ProvisionOrganizationPromptPack(ctx, f.organizationID, packA(t))
	if err != nil {
		t.Fatal(err)
	}
	installer := provisioned.Record.Pack.Installation.Installer
	if installer.BuiltinMaestroVersion != other.String() || provisioned.Record.Pack.Installation.ValidatedMaestroVersion != other.String() {
		t.Fatalf("installer %+v validated %q, want %s", installer, provisioned.Record.Pack.Installation.ValidatedMaestroVersion, other)
	}
}
