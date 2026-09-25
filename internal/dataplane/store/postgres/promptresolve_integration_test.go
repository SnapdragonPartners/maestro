//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/harness"
	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
	"orchestrator/internal/prompt"
)

// Resolution at dispatch (Phase 3 item 4 design, D8): the precedence of
// inputs, the typed refusals that have a producer at item 4, both branches
// of the version semantics, and the row that records what was decided.

// resolutionStore composes a store the way a root does: the prompt.pack key
// registered, the given contract, the given harness version.
func resolutionStore(t *testing.T, f *fixture, running harness.Version, contract store.PromptContract) *postgres.Store {
	t.Helper()
	keys := configkeys.MustNew(map[configkeys.Key]configkeys.Entry{
		store.PromptPackKey: {Schema: store.PromptSelectorSchema(),
			PermittedScopes: []configkeys.Scope{configkeys.ScopeOrganization, configkeys.ScopeProduct, configkeys.ScopeRepository}},
	})
	s, err := postgres.New(f.pool, testRegistry(t), f.blob, f.rootKey, running,
		postgres.WithConfigKeys(keys), postgres.WithPromptContract(contract))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// selectorValue is the stored form of a content selector.
func selectorValue(t *testing.T, contentID uuid.UUID) json.RawMessage {
	t.Helper()
	value, err := json.Marshal(store.PromptSelector{ContentID: &contentID})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func expectRefusal(t *testing.T, err error, reason store.DispatchReason) *store.DispatchRejected {
	t.Helper()
	var rejected *store.DispatchRejected
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a DispatchRejected with reason %q", err, reason)
	}
	if rejected.Reason != reason {
		t.Fatalf("refused for %q, want %q", rejected.Reason, reason)
	}
	return rejected
}

// TestDispatchResolvesThePackAndPersistsTheDecision: the ordinary path --
// the selector provisioning seeded, read through ResolveConfiguration --
// and the resolution row beside the basis, read back by every reader.
func TestDispatchResolvesThePackAndPersistsTheDecision(t *testing.T) {
	f := newFixture(t)
	g := provisionGoverned(t, f) // seeds the empty pack + organization selector
	ctx := context.Background()

	dispatch, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID, nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	pack, err := f.store.GetPromptPackByIdentity(ctx, f.organizationID, dispatch.PromptResolution.Identity)
	if err != nil {
		t.Fatal(err)
	}
	r := dispatch.PromptResolution
	switch {
	case r.StoryDispatchID != dispatch.StoryDispatchID:
		t.Fatal("the resolution names another dispatch")
	case r.ContentID != pack.Content.ContentID || r.InstallationID != pack.Installation.InstallationID:
		t.Fatalf("resolution references %s/%s, want %s/%s", r.ContentID, r.InstallationID, pack.Content.ContentID, pack.Installation.InstallationID)
	case r.ResolvedName != "built-in" || r.InstallationRevision != 1:
		t.Fatalf("resolved name %q revision %d", r.ResolvedName, r.InstallationRevision)
	case r.RangeCheck != store.PromptRangePassed:
		t.Fatalf("range check %q under a real semver in the band, want passed", r.RangeCheck)
	case r.ValidatedMaestroVersion != planetest.HarnessVersion || r.Snapshot.ContractRerun:
		t.Fatalf("validated %q rerun=%v: the installation was validated by this very version, so nothing re-ran",
			r.ValidatedMaestroVersion, r.Snapshot.ContractRerun)
	case r.Snapshot.DisplayName != "built-in" || r.Snapshot.MaxMaestroVersion != phase3Upper:
		t.Fatalf("snapshot %+v", r.Snapshot)
	}

	// Every reader joins it.
	read, err := f.store.GetDispatch(ctx, f.organizationID, dispatch.StoryDispatchID)
	if err != nil || read.PromptResolution.ResolutionID != r.ResolutionID {
		t.Fatalf("GetDispatch: %+v, %v", read.PromptResolution.ResolutionID, err)
	}
	pending, err := f.store.ListDispatchesByDisposition(ctx, f.organizationID, store.DispositionPending)
	if err != nil || len(pending) != 1 || pending[0].PromptResolution.ResolutionID != r.ResolutionID {
		t.Fatalf("ListDispatchesByDisposition: %d, %v", len(pending), err)
	}
	open, err := f.store.OpenWork(ctx, f.organizationID)
	if err != nil || len(open.Pending) != 1 || open.Pending[0].Dispatch.PromptResolution.ResolutionID != r.ResolutionID {
		t.Fatalf("OpenWork: %+v, %v", open, err)
	}

	// History: a later correction to the installation, and a moved
	// selector, change nothing about what this dispatch resolved.
	s := resolutionStore(t, f, planetest.Harness(t), prompt.MustNew(nil))
	if _, err := s.UpdatePromptPackInstallation(ctx, store.UpdatePromptPackInstallationInput{
		DisplayName: "renamed", MinMaestroVersion: phase3Lower, MaxMaestroVersion: phase3Upper,
		ExpectedRevision: 1, OrganizationID: f.organizationID, InstallationID: pack.Installation.InstallationID,
	}); err != nil {
		t.Fatal(err)
	}
	again, err := f.store.GetDispatch(ctx, f.organizationID, dispatch.StoryDispatchID)
	if err != nil || again.PromptResolution.ResolvedName != "built-in" || again.PromptResolution.InstallationRevision != 1 {
		t.Fatalf("after the installation moved, the dispatch reads %q rev %d; a resolution is history",
			again.PromptResolution.ResolvedName, again.PromptResolution.InstallationRevision)
	}
}

// TestDispatchSelectorPrecedence: an explicit selector wins over
// configuration; configuration resolves most-specific-first; and a name is
// refused where a selector is written.
func TestDispatchSelectorPrecedence(t *testing.T) {
	f := newFixture(t)
	g := provisionGoverned(t, f)
	ctx := context.Background()
	s := resolutionStore(t, f, planetest.Harness(t), fixturePrompts(t))

	// A second, non-empty pack through the real registry.
	second, err := s.InstallPromptPack(ctx, f.installInput(validPack(), "coder"))
	if err != nil {
		t.Fatal(err)
	}
	secondID := second.Record.Content.ContentID

	// Repository scope overrides the organization's selector.
	if _, err := s.CreateConfigurationRecord(ctx, store.CreateConfigurationRecordInput{
		Value: selectorValue(t, secondID), Key: store.PromptPackKey,
		Scope:          store.ConfigScope{Type: configkeys.ScopeRepository, ID: g.repository},
		OrganizationID: f.organizationID,
	}); err != nil {
		t.Fatal(err)
	}
	viaConfig, err := s.CreateDispatch(ctx, f.organizationID, g.story.StoryID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if viaConfig.PromptResolution.ContentID != secondID {
		t.Fatalf("configuration resolved %s, want the repository-scoped %s", viaConfig.PromptResolution.ContentID, secondID)
	}

	// An explicit selector wins over every scope.
	empty, err := f.store.GetPromptPackByIdentity(ctx, f.organizationID, f.seedPromptPack(t).Content.Identity())
	if err != nil {
		t.Fatal(err)
	}
	explicit := &store.PromptSelector{Identity: ptr(empty.Content.Identity())}
	viaExplicit, err := s.CreateDispatch(ctx, f.organizationID, g.story.StoryID, explicit)
	if err != nil {
		t.Fatal(err)
	}
	if viaExplicit.PromptResolution.ContentID != empty.Content.ContentID {
		t.Fatalf("explicit selector resolved %s, want %s", viaExplicit.PromptResolution.ContentID, empty.Content.ContentID)
	}

	// An explicit selector naming BOTH a content id and an identity is
	// refused rather than resolved by whichever field the lookup prefers
	// (PR #367 review): the pair below disagree, and taking the content id
	// would silently ignore the identity the caller also asserted.
	both := &store.PromptSelector{ContentID: &empty.Content.ContentID, Identity: ptr(second.Record.Content.Identity())}
	_, err = s.CreateDispatch(ctx, f.organizationID, g.story.StoryID, both)
	assertDispatchRejected(t, err, store.ReasonPromptSelectorUnresolved)
	if !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("both-set selector: %v, want the exactly-one rule named", err)
	}

	// A name where a selector belongs is refused at the write, by name.
	_, err = s.CreateConfigurationRecord(ctx, store.CreateConfigurationRecordInput{
		Value: json.RawMessage(`"default"`), Key: store.PromptPackKey,
		Scope:          store.ConfigScope{Type: configkeys.ScopeProduct, ID: g.product},
		OrganizationID: f.organizationID,
	})
	if !errors.Is(err, configkeys.ErrInvalidValue) || !strings.Contains(err.Error(), "a name is a label") {
		t.Fatalf("a bare name as the selector: %v, want the registered schema's refusal teaching the rule", err)
	}
}

func ptr[T any](v T) *T { return &v }

// TestDispatchRefusalsLeaveNothingBehind: each typed refusal, and after each
// the dispatch table is exactly as it was -- a refused resolution has no
// parent row and writes none.
func TestDispatchRefusalsLeaveNothingBehind(t *testing.T) {
	f := newFixture(t)
	g := provisionGoverned(t, f)
	ctx := context.Background()
	seeded := f.seedPromptPack(t)

	// The organization's selector is removed so the no-selector case is
	// reachable; the others supply explicit selectors.
	record, err := f.store.ResolveConfiguration(ctx, f.organizationID, g.repository, store.PromptPackKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteConfigurationRecord(ctx, f.organizationID, record.ID, record.Version); err != nil {
		t.Fatal(err)
	}

	outOfBand, err := harness.Parse("v2.0.0-phase.4.0.0")
	if err != nil {
		t.Fatal(err)
	}
	moved, err := harness.Parse("v2.0.0-phase.3.5.0")
	if err != nil {
		t.Fatal(err)
	}
	refusing := resolutionStore(t, f, moved, prompt.MustNew(map[prompt.SlotKey]prompt.Slot{
		"other.slot": {Roles: []prompt.Role{"coder"}},
	}))
	// A non-empty pack the refusing registry cannot render, installed
	// through a registry that can.
	rendered, err := resolutionStore(t, f, planetest.Harness(t), fixturePrompts(t)).
		InstallPromptPack(ctx, f.installInput(validPack(), "coder"))
	if err != nil {
		t.Fatal(err)
	}
	foreign := uuid.New()

	for name, tc := range map[string]struct {
		store    *postgres.Store
		selector *store.PromptSelector
		reason   store.DispatchReason
		detail   string
	}{
		"no selector anywhere":             {f.store, nil, store.ReasonNoPromptSelector, "no prompt.pack record"},
		"an empty explicit selector":       {f.store, &store.PromptSelector{}, store.ReasonNoPromptSelector, "empty"},
		"a content id that does not exist": {f.store, &store.PromptSelector{ContentID: &foreign}, store.ReasonPromptSelectorUnresolved, foreign.String()},
		"another organization's identity": {f.store, &store.PromptSelector{Identity: ptr(store.PromptIdentity{
			Scheme: store.PromptSchemePackJCS, Digest: strings.Repeat("0", 64)})}, store.ReasonPromptSelectorUnresolved, "pack-jcs-sha256-v1:0000"},
		"a declared range that excludes this harness": {resolutionStore(t, f, outOfBand, prompt.MustNew(nil)),
			&store.PromptSelector{ContentID: &seeded.Content.ContentID}, store.ReasonPromptPackIncompatible, "harness is v2.0.0-phase.4.0.0"},
		"a moved harness whose contract refuses the pack": {refusing,
			&store.PromptSelector{ContentID: &rendered.Record.Content.ContentID}, store.ReasonPromptPackUnusable, prompt.ErrUnknownSlot.Error()},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tc.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID, tc.selector)
			rejected := expectRefusal(t, err, tc.reason)
			if !strings.Contains(rejected.Detail, tc.detail) {
				t.Fatalf("detail %q does not name %q", rejected.Detail, tc.detail)
			}
			var dispatches, resolutions int
			if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM story_dispatches WHERE organization_id=$1),
			        (SELECT count(*) FROM dispatch_prompt_resolutions WHERE organization_id=$1)`, f.organizationID).
				Scan(&dispatches, &resolutions); err != nil {
				t.Fatal(err)
			}
			if dispatches != 0 || resolutions != 0 {
				t.Fatalf("a refused dispatch left %d dispatch(es) and %d resolution(s)", dispatches, resolutions)
			}
		})
	}

	// Positive control through the same stores: the seeded pack under an
	// in-band harness dispatches, and under the refusing registry the EMPTY
	// pack still dispatches (it has nothing the registry could refuse).
	if _, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID,
		&store.PromptSelector{ContentID: &seeded.Content.ContentID}); err != nil {
		t.Fatalf("the control was refused: %v", err)
	}
}

// TestDispatchVersionSemantics: design D8's three branches. A real semver in
// the band passes without re-running the contract; a moved semver re-runs
// it and records the new version; the development sentinel records
// not-evaluated and re-runs on every dispatch.
func TestDispatchVersionSemantics(t *testing.T) {
	f := newFixture(t)
	g := provisionGoverned(t, f)
	ctx := context.Background()
	seeded := f.seedPromptPack(t)
	selector := &store.PromptSelector{ContentID: &seeded.Content.ContentID}

	// A contract that REFUSES everything, so "the contract was consulted"
	// is observable as a refusal and "was not" as a success.
	refuseAll := resolutionStore(t, f, planetest.Harness(t), refusingContract{})
	unmoved, err := refuseAll.CreateDispatch(ctx, f.organizationID, g.story.StoryID, selector)
	if err != nil {
		t.Fatalf("an unmoved real semver consulted the contract: %v", err)
	}
	if unmoved.PromptResolution.Snapshot.ContractRerun || unmoved.PromptResolution.RangeCheck != store.PromptRangePassed {
		t.Fatalf("unmoved: rerun=%v range=%q", unmoved.PromptResolution.Snapshot.ContractRerun, unmoved.PromptResolution.RangeCheck)
	}

	moved, err := harness.Parse("v2.0.0-phase.3.7.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolutionStore(t, f, moved, refusingContract{}).CreateDispatch(ctx, f.organizationID, g.story.StoryID, selector); err == nil {
		t.Fatal("a moved harness did not re-run the contract")
	} else {
		expectRefusal(t, err, store.ReasonPromptPackUnusable)
	}
	rerun, err := resolutionStore(t, f, moved, prompt.MustNew(nil)).CreateDispatch(ctx, f.organizationID, g.story.StoryID, selector)
	if err != nil {
		t.Fatal(err)
	}
	if !rerun.PromptResolution.Snapshot.ContractRerun || rerun.PromptResolution.ValidatedMaestroVersion != "v2.0.0-phase.3.7.0" ||
		rerun.PromptResolution.RangeCheck != store.PromptRangePassed {
		t.Fatalf("moved: %+v", rerun.PromptResolution)
	}

	dev, err := harness.Parse(harness.Development)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolutionStore(t, f, dev, refusingContract{}).CreateDispatch(ctx, f.organizationID, g.story.StoryID, selector); err == nil {
		t.Fatal("a development build did not re-run the contract")
	}
	underDev, err := resolutionStore(t, f, dev, prompt.MustNew(nil)).CreateDispatch(ctx, f.organizationID, g.story.StoryID, selector)
	if err != nil {
		t.Fatal(err)
	}
	if underDev.PromptResolution.RangeCheck != store.PromptRangeNotEvaluated || !underDev.PromptResolution.Snapshot.ContractRerun ||
		underDev.PromptResolution.ValidatedMaestroVersion != harness.Development {
		t.Fatalf("dev: %+v", underDev.PromptResolution)
	}

	// "dev" == "dev" says nothing: an installation validated by one local
	// binary is re-validated by the next, which may be a different harness
	// built an hour later. So a pack INSTALLED under dev, dispatched under
	// dev with a refusing contract, is refused -- the equal strings did not
	// stand in for a comparison.
	devInstalled, err := resolutionStore(t, f, dev, fixturePrompts(t)).InstallPromptPack(ctx, f.installInput(validPack(), "coder"))
	if err != nil {
		t.Fatal(err)
	}
	if devInstalled.Record.Installation.ValidatedMaestroVersion != harness.Development {
		t.Fatalf("installed under dev, validated %q", devInstalled.Record.Installation.ValidatedMaestroVersion)
	}
	devSelector := &store.PromptSelector{ContentID: &devInstalled.Record.Content.ContentID}
	if _, err := resolutionStore(t, f, dev, refusingContract{}).CreateDispatch(ctx, f.organizationID, g.story.StoryID, devSelector); err == nil {
		t.Fatal("dev == dev was taken as evidence that nothing moved; the contract was not re-run")
	} else {
		expectRefusal(t, err, store.ReasonPromptPackUnusable)
	}
}

type refusingContract struct{}

func (refusingContract) ValidatePack(map[string]string, []string) error {
	return errors.New("this contract refuses every pack")
}
