//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/harness"
	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
	"orchestrator/internal/orchestrator"
	"orchestrator/internal/prompt"
)

// The pack family through the seam (Phase 3 item 4 design, D2, D4, D6,
// D10). The gate is the REAL internal/prompt.Registry over fixture slots,
// supplied through the same store.PromptContract seat the composition root
// fills, so a pack refused here was refused by the path a production install
// takes; nothing is validated through a side door.

const (
	slotCoderSystem = "coder.system"
	slotCoderPlan   = "coder.plan"
	phase3Lower     = "v2.0.0-phase.3.0.0"
	phase3Upper     = "v2.0.0-phase.4.0.0"
)

func fixturePrompts(t *testing.T) *prompt.Registry {
	t.Helper()
	built, err := prompt.New(map[prompt.SlotKey]prompt.Slot{
		slotCoderSystem: {Roles: []prompt.Role{"coder"}, Variables: []prompt.Variable{"Story"}},
		slotCoderPlan:   {Roles: []prompt.Role{"coder"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return built
}

// packStore is the fixture's store re-composed with the fixture registry and
// the given harness version.
func packStore(t *testing.T, f *fixture, running harness.Version) *postgres.Store {
	t.Helper()
	built, err := postgres.New(f.pool, testRegistry(t), f.blob, f.rootKey, running,
		postgres.WithPromptContract(fixturePrompts(t)))
	if err != nil {
		t.Fatal(err)
	}
	return built
}

func validPack() map[string]string {
	return map[string]string{
		slotCoderSystem: "You are working on {{.Story}}.\n",
		slotCoderPlan:   "Plan first.",
	}
}

func builtin(version string) store.PromptPackInstaller {
	return store.PromptPackInstaller{Kind: store.PromptPackInstalledByBuiltin, BuiltinMaestroVersion: version}
}

func (f *fixture) installInput(entries map[string]string, roles ...string) store.InstallPromptPackInput {
	return store.InstallPromptPackInput{
		Entries: entries, DisplayName: "fixture", MinMaestroVersion: phase3Lower, MaxMaestroVersion: phase3Upper,
		DeclaredRoles: roles, Installer: builtin(planetest.HarnessVersion), OrganizationID: f.organizationID,
	}
}

func TestInstallPromptPackWritesBothRowsAndIsIdempotentByIdentity(t *testing.T) {
	f := newFixture(t)
	s := packStore(t, f, planetest.Harness(t))
	ctx := context.Background()

	first, err := s.InstallPromptPack(ctx, f.installInput(validPack(), "coder"))
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !first.Created {
		t.Fatal("first install reported Created=false")
	}
	pack := first.Record
	if pack.Content.Scheme != store.PromptSchemePackJCS || len(pack.Content.Digest) != 64 {
		t.Fatalf("content identity = %s", pack.Content.Identity())
	}
	// The harness side computes the same identity over the same projection.
	if want, _ := prompt.Digest(validPack()); pack.Content.Digest != want {
		t.Fatalf("seam digest %s, harness digest %s: the two projections differ", pack.Content.Digest, want)
	}
	inst := pack.Installation
	switch {
	case inst.ContentID != pack.Content.ContentID:
		t.Fatal("installation names other content")
	case inst.Revision != 1:
		t.Fatalf("revision = %d, want 1", inst.Revision)
	case inst.ValidatedMaestroVersion != planetest.HarnessVersion:
		t.Fatalf("validated against %q, want the composition's %q", inst.ValidatedMaestroVersion, planetest.HarnessVersion)
	case inst.Installer.Kind != store.PromptPackInstalledByBuiltin || inst.Installer.BuiltinMaestroVersion != planetest.HarnessVersion:
		t.Fatalf("installer = %+v", inst.Installer)
	case strings.Join(inst.DeclaredRoles, ",") != "coder":
		t.Fatalf("declared roles = %v", inst.DeclaredRoles)
	}

	// Idempotent by identity: same content, same declaration, reordered
	// and duplicated roles -- one stored value, Created=false, same rows.
	again, err := s.InstallPromptPack(ctx, f.installInput(validPack(), "coder", "coder"))
	if err != nil {
		t.Fatalf("re-install: %v", err)
	}
	if again.Created || again.Record.Installation.InstallationID != inst.InstallationID ||
		again.Record.Content.ContentID != pack.Content.ContentID {
		t.Fatalf("re-install: Created=%v, installation %s (want %s)", again.Created,
			again.Record.Installation.InstallationID, inst.InstallationID)
	}

	// A later binary re-installing identical content is a no-op, not a
	// conflict: the installer is provenance, recorded once (design D11).
	later := f.installInput(validPack(), "coder")
	later.Installer = builtin("v2.0.0-phase.3.9.9")
	if got, err := s.InstallPromptPack(ctx, later); err != nil || got.Created {
		t.Fatalf("re-install from a later binary: Created=%v err=%v", got.Created, err)
	}

	// Same content, differing declaration: exactly one installation to
	// disagree with, so a typed conflict.
	for name, change := range map[string]func(*store.InstallPromptPackInput){
		"display name": func(in *store.InstallPromptPackInput) { in.DisplayName = "other" },
		"range":        func(in *store.InstallPromptPackInput) { in.MaxMaestroVersion = "v2.0.0-phase.5.0.0" },
		"roles":        func(in *store.InstallPromptPackInput) { in.DeclaredRoles = nil },
	} {
		in := f.installInput(validPack(), "coder")
		change(&in)
		var conflict *store.BootstrapConflict
		if _, err := s.InstallPromptPack(ctx, in); !errors.As(err, &conflict) {
			t.Errorf("%s changed: err = %v, want a BootstrapConflict", name, err)
		}
	}

	// The reads.
	byIdentity, err := s.GetPromptPackByIdentity(ctx, f.organizationID, pack.Content.Identity())
	if err != nil || byIdentity.Installation.InstallationID != inst.InstallationID {
		t.Fatalf("by identity: %+v, %v", byIdentity, err)
	}
	if _, err := s.GetPromptPackByIdentity(ctx, f.otherOrgID, pack.Content.Identity()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another organization resolved this identity: %v", err)
	}
	// A legacy identity names no content row -- and the refusal says so,
	// rather than reporting the same "not found" a mistyped digest gets.
	legacy := store.PromptIdentity{Scheme: store.PromptSchemeV1Manifest, Digest: "sha256:" + pack.Content.Digest}
	_, err = s.GetPromptPackByIdentity(ctx, f.organizationID, legacy)
	if !errors.Is(err, store.ErrNotFound) || !strings.Contains(err.Error(), "only pack-jcs-sha256-v1 identities") {
		t.Fatalf("a legacy identity: %v, want ErrNotFound naming the scheme rule", err)
	}
	listed, err := s.ListPromptPackInstallations(ctx, f.organizationID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list: %d, %v", len(listed), err)
	}
}

// TestInstallPromptPackRunsTheGateThroughTheContract: design D10's three
// deliberate faults, plus the declaration's own invariants, each refused by
// InstallPromptPack -- and each paired with the valid pack through the same
// call, so a refusal caused by an unrelated rule is distinguishable.
func TestInstallPromptPackRunsTheGateThroughTheContract(t *testing.T) {
	f := newFixture(t)
	s := packStore(t, f, planetest.Harness(t))
	ctx := context.Background()

	for name, tc := range map[string]struct {
		change func(*store.InstallPromptPackInput)
		want   error
		msg    string
	}{
		"a declared role with a missing slot": {func(in *store.InstallPromptPackInput) {
			delete(in.Entries, slotCoderPlan)
		}, store.ErrPromptPackRefused, prompt.ErrMissingSlot.Error()},
		"an unparseable entry": {func(in *store.InstallPromptPackInput) {
			in.Entries[slotCoderPlan] = "{{.Story"
		}, store.ErrPromptPackRefused, prompt.ErrParse.Error()},
		"a variable the slot does not supply": {func(in *store.InstallPromptPackInput) {
			in.Entries[slotCoderPlan] = "{{.Story}}"
		}, store.ErrPromptPackRefused, prompt.ErrUndeclaredVariable.Error()},
		"an unregistered slot": {func(in *store.InstallPromptPackInput) {
			in.Entries["coder.review"] = "x"
		}, store.ErrPromptPackRefused, prompt.ErrUnknownSlot.Error()},
		"a role no slot names": {func(in *store.InstallPromptPackInput) {
			in.DeclaredRoles = []string{"coder", "pm"}
		}, store.ErrPromptPackRefused, prompt.ErrUnknownRole.Error()},
		"an inverted range": {func(in *store.InstallPromptPackInput) {
			in.MinMaestroVersion, in.MaxMaestroVersion = phase3Upper, phase3Lower
		}, harness.ErrMalformedRange, "does not sort below"},
		"a bound without its v": {func(in *store.InstallPromptPackInput) {
			in.MinMaestroVersion = "2.0.0"
		}, harness.ErrMalformedRange, "2.0.0"},
		"an entry with NUL": {func(in *store.InstallPromptPackInput) {
			in.Entries[slotCoderPlan] = "plan\x00"
		}, nil, "NUL"},
		"an entry that is not UTF-8": {func(in *store.InstallPromptPackInput) {
			in.Entries[slotCoderPlan] = "plan\xff"
		}, nil, "UTF-8"},
		"a blank role": {func(in *store.InstallPromptPackInput) {
			in.DeclaredRoles = []string{" coder"}
		}, nil, "whitespace"},
		"a builtin with a mis-stamped version": {func(in *store.InstallPromptPackInput) {
			in.Installer = builtin("2.0.0")
		}, harness.ErrMalformedVersion, ""},
		"a user installer with no user": {func(in *store.InstallPromptPackInput) {
			in.Installer = store.PromptPackInstaller{Kind: store.PromptPackInstalledByUser}
		}, nil, "installing user"},
		"a blank display name": {func(in *store.InstallPromptPackInput) {
			in.DisplayName = " "
		}, nil, "blank"},
	} {
		t.Run(name, func(t *testing.T) {
			in := f.installInput(validPack(), "coder")
			tc.change(&in)
			_, err := s.InstallPromptPack(ctx, in)
			if err == nil {
				t.Fatal("installed")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %q, want it to name %q", err, tc.msg)
			}
			// Nothing landed: a refusal writes no content and no installation.
			if listed, _ := s.ListPromptPackInstallations(ctx, f.organizationID); len(listed) != 0 {
				t.Fatalf("a refused install left %d installation(s)", len(listed))
			}
			var contents int
			if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_pack_contents WHERE organization_id=$1`,
				f.organizationID).Scan(&contents); err != nil || contents != 0 {
				t.Fatalf("a refused install left %d content row(s) (%v)", contents, err)
			}
		})
	}

	// The positive control, through the same call.
	if _, err := s.InstallPromptPack(ctx, f.installInput(validPack(), "coder")); err != nil {
		t.Fatalf("the valid pack was refused: %v", err)
	}
}

// TestInstallPromptPackIsOneTransaction: content commits only beside its
// installation. The installation insert is made to fail AFTER the content
// insert by a row the schema refuses -- a display name the seam's check did
// not see because the failure is injected below it -- and no content row
// survives.
func TestInstallPromptPackIsOneTransaction(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := packStore(t, f, planetest.Harness(t))

	// The installation is refused by the schema's user reference: a user of
	// ANOTHER organization installing here. The seam checks the installer's
	// shape, not the user's tenancy; the composite key does.
	otherUser := uuid.New()
	if _, err := f.pool.Exec(ctx, `INSERT INTO users (user_id, organization_id, handle, display_name)
	    VALUES ($1, $2, 'outsider', 'Outsider')`, otherUser, f.otherOrgID); err != nil {
		t.Fatal(err)
	}
	in := f.installInput(validPack(), "coder")
	in.Installer = store.PromptPackInstaller{Kind: store.PromptPackInstalledByUser, UserID: &otherUser}
	if _, err := s.InstallPromptPack(ctx, in); err == nil {
		t.Fatal("a cross-tenant installer was accepted")
	}
	var contents int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_pack_contents WHERE organization_id=$1`,
		f.organizationID).Scan(&contents); err != nil {
		t.Fatal(err)
	}
	if contents != 0 {
		t.Fatalf("%d content row(s) committed with no installation: the two writes are not one transaction", contents)
	}
}

func TestUpdatePromptPackInstallationIsConditionalAndGated(t *testing.T) {
	f := newFixture(t)
	s := packStore(t, f, planetest.Harness(t))
	ctx := context.Background()

	installed, err := s.InstallPromptPack(ctx, f.installInput(validPack(), "coder"))
	if err != nil {
		t.Fatal(err)
	}
	inst := installed.Record.Installation
	update := func(revision int) store.UpdatePromptPackInstallationInput {
		return store.UpdatePromptPackInstallationInput{
			DisplayName: "renamed", MinMaestroVersion: phase3Lower, MaxMaestroVersion: phase3Upper,
			DeclaredRoles: []string{"coder"}, ExpectedRevision: revision,
			OrganizationID: f.organizationID, InstallationID: inst.InstallationID,
		}
	}

	// Under a MOVED harness, so the write of the validated version is
	// observable: the update records the version that re-ran the gate.
	moved, err := harness.Parse("v2.0.0-phase.3.2.0")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := packStore(t, f, moved).UpdatePromptPackInstallation(ctx, update(1))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Revision != 2 || updated.DisplayName != "renamed" || updated.ValidatedMaestroVersion != "v2.0.0-phase.3.2.0" {
		t.Fatalf("after update: revision %d name %q validated %q", updated.Revision, updated.DisplayName, updated.ValidatedMaestroVersion)
	}

	// The stale writer is told the revision moved; the row is untouched.
	if _, err := s.UpdatePromptPackInstallation(ctx, update(1)); !errors.Is(err, store.ErrPromptPackConflict) {
		t.Fatalf("stale update: %v, want ErrPromptPackConflict", err)
	}
	// A missing installation is not a conflict.
	missing := update(2)
	missing.InstallationID = uuid.New()
	if _, err := s.UpdatePromptPackInstallation(ctx, missing); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update of a missing installation: %v, want ErrNotFound", err)
	}

	// The gate runs on update over the STORED entries: declaring a role
	// the entries cannot cover is refused, as is an inverted range, and the
	// revision does not move.
	for name, change := range map[string]func(*store.UpdatePromptPackInstallationInput){
		"coverage the content cannot satisfy": func(in *store.UpdatePromptPackInstallationInput) {
			in.DeclaredRoles = []string{"coder", "pm"}
		},
		"an inverted range": func(in *store.UpdatePromptPackInstallationInput) {
			in.MinMaestroVersion, in.MaxMaestroVersion = phase3Upper, phase3Lower
		},
	} {
		in := update(2)
		change(&in)
		if _, err := s.UpdatePromptPackInstallation(ctx, in); err == nil {
			t.Errorf("%s: the update was accepted", name)
		}
	}
	current, err := s.GetPromptPackInstallation(ctx, f.organizationID, inst.InstallationID)
	if err != nil || current.Revision != 2 {
		t.Fatalf("after refused updates: revision %d, %v; want 2", current.Revision, err)
	}

	// Set semantics on update too: reordered, duplicated roles store once.
	// Only one role is registered, so the set is exercised on the
	// declaration's shape rather than on two names.
	in := update(2)
	in.DeclaredRoles = []string{"coder", "coder"}
	if got, err := s.UpdatePromptPackInstallation(ctx, in); err != nil || strings.Join(got.DeclaredRoles, ",") != "coder" {
		t.Fatalf("duplicated roles: %v, %v", got, err)
	}
}

// TestConcurrentPromptPackUpdatesSerialize: two writers holding revision 1;
// exactly one wins, on the lock, and the loser is told the version moved.
func TestConcurrentPromptPackUpdatesSerialize(t *testing.T) {
	f := newFixture(t)
	s := packStore(t, f, planetest.Harness(t))
	ctx := context.Background()
	installed, err := s.InstallPromptPack(ctx, f.installInput(validPack(), "coder"))
	if err != nil {
		t.Fatal(err)
	}
	inst := installed.Record.Installation

	results := make(chan error, 2)
	for i := range 2 {
		go func() {
			_, err := s.UpdatePromptPackInstallation(ctx, store.UpdatePromptPackInstallationInput{
				DisplayName: "writer-" + string(rune('a'+i)), MinMaestroVersion: phase3Lower, MaxMaestroVersion: phase3Upper,
				DeclaredRoles: []string{"coder"}, ExpectedRevision: 1,
				OrganizationID: f.organizationID, InstallationID: inst.InstallationID,
			})
			results <- err
		}()
	}
	var won, lost int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			won++
		case errors.Is(err, store.ErrPromptPackConflict):
			lost++
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("won %d lost %d, want exactly one of each", won, lost)
	}
	current, err := s.GetPromptPackInstallation(ctx, f.organizationID, inst.InstallationID)
	if err != nil || current.Revision != 2 {
		t.Fatalf("revision %d after two racing updates, want 2", current.Revision)
	}
}

// TestStoreWithoutAPromptContractRefusesEveryPack: the default gate is
// closed, on the key registry's rule -- a store nobody gave a slot
// vocabulary cannot judge a pack usable.
func TestStoreWithoutAPromptContractRefusesEveryPack(t *testing.T) {
	f := newFixture(t) // composed with no contract
	_, err := f.store.InstallPromptPack(context.Background(), f.installInput(map[string]string{}))
	if !errors.Is(err, postgres.ErrNoPromptContract) {
		t.Fatalf("install through a store with no contract: %v, want ErrNoPromptContract", err)
	}
}

// TestPromptPackContentIsImmutableThroughTheSeam: there is no update path
// for content, and the row refuses one from any client.
func TestPromptPackContentIsImmutableThroughTheSeam(t *testing.T) {
	f := newFixture(t)
	s := packStore(t, f, planetest.Harness(t))
	ctx := context.Background()
	installed, err := s.InstallPromptPack(ctx, f.installInput(validPack(), "coder"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.pool.Exec(ctx, `UPDATE prompt_pack_contents SET entries = '{}' WHERE content_id = $1`,
		installed.Record.Content.ContentID)
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("a raw UPDATE of content: %v, want the trigger's refusal", err)
	}
	var entries string
	if err := f.pool.QueryRow(ctx, `SELECT entries::text FROM prompt_pack_contents WHERE content_id=$1`,
		installed.Record.Content.ContentID).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(entries, "Plan first.") {
		t.Fatalf("content changed: %s", entries)
	}
}

// acceptEverything is a contract that judges every pack usable, so the
// seam's own checks are the only thing standing between an entry and the
// row. It is what proves the seam does not rely on the caller's contract for
// the properties the DIGEST depends on.
type acceptEverything struct{}

func (acceptEverything) ValidatePack(map[string]string, []string) error { return nil }

// TestSeamRefusesUndigestableEntriesWhateverTheContractSays: invalid UTF-8
// and NUL are identity defects -- json.Marshal would substitute U+FFFD and
// two different entries would share one digest -- so the seam refuses them
// itself, and a permissive contract cannot let them through.
func TestSeamRefusesUndigestableEntriesWhateverTheContractSays(t *testing.T) {
	f := newFixture(t)
	s, err := postgres.New(f.pool, testRegistry(t), f.blob, f.rootKey, planetest.Harness(t),
		postgres.WithPromptContract(acceptEverything{}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Each refusal is asserted by the seam's OWN message. Postgres refuses
	// U+0000 in jsonb too, so "refused at all" would not show the seam did
	// it; the message does.
	for name, tc := range map[string]struct {
		entries map[string]string
		msg     string
	}{
		"NUL in text":    {map[string]string{"any.slot": "text\x00"}, "NUL"},
		"NUL in key":     {map[string]string{"any\x00slot": "text"}, "NUL"},
		"invalid UTF-8":  {map[string]string{"any.slot": "text\xff"}, "UTF-8"},
		"blank slot key": {map[string]string{" ": "text"}, "blank slot key"},
	} {
		_, err := s.InstallPromptPack(ctx, f.installInput(tc.entries))
		if err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: err = %v; want the seam's own refusal naming %q", name, err, tc.msg)
		}
	}
	// The control: under the same contract, an entry the seam has no
	// opinion about installs.
	if _, err := s.InstallPromptPack(ctx, f.installInput(map[string]string{"any.slot": "text"})); err != nil {
		t.Fatalf("the control was refused: %v", err)
	}
}

// seedPromptPack provisions the organization's prompt pack the way the
// composition root does: the EMBEDDED built-in, loaded through
// orchestrator.LoadBuiltin, provisioned through the seam's verb under the
// Orchestrator's key vocabulary and its empty slot registry (design D1, D2,
// D9). Idempotent, so a lineage seeded twice holds one pack and one
// selector. Every fixture that needs a resolvable selector travels the
// production path to get one.
func (f *fixture) seedPromptPack(t *testing.T) store.InstalledPromptPack {
	t.Helper()
	s, err := postgres.New(f.pool, testRegistry(t), f.blob, f.rootKey, planetest.Harness(t),
		postgres.WithConfigKeys(orchestrator.Keys()), postgres.WithPromptContract(orchestrator.Prompts()))
	if err != nil {
		t.Fatal(err)
	}
	builtin, err := orchestrator.LoadBuiltin(orchestrator.BuiltinPack())
	if err != nil {
		t.Fatalf("load the embedded built-in: %v", err)
	}
	provisioned, err := s.ProvisionOrganizationPromptPack(context.Background(), f.organizationID, builtin)
	if err != nil {
		t.Fatalf("provision the built-in pack: %v", err)
	}
	return provisioned.Record.Pack
}
