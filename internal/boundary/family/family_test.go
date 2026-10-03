package family_test

import (
	"errors"
	"testing"

	"orchestrator/internal/boundary/family"
)

// TestParseIdentityIsTheOneShape: "<kind>/<verb>", both lower-case segments,
// exactly one slash. The registry's lookup and the requirement vocabulary's
// qualifier both go through this, so a shape admitted here is admitted
// everywhere -- which is why the refusals are pinned.
func TestParseIdentityIsTheOneShape(t *testing.T) {
	kind, verb, err := family.ParseIdentity("forge/story_pull_request")
	if err != nil || kind != "forge" || verb != "story_pull_request" {
		t.Fatalf("ParseIdentity = %q, %q, %v", kind, verb, err)
	}
	for name, identity := range map[string]string{
		"no slash":      "forge",
		"two slashes":   "forge/story/pr",
		"empty kind":    "/pr",
		"empty verb":    "forge/",
		"upper-case":    "Forge/pr",
		"dash":          "forge/story-pr",
		"leading digit": "forge/1pr",
		"space":         "forge/story pr",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := family.ParseIdentity(identity); !errors.Is(err, family.ErrInvalidIdentity) {
				t.Fatalf("%q was accepted: %v", identity, err)
			}
		})
	}
	if f := (family.Family{Kind: "test", Verb: "noop"}); f.Identity() != "test/noop" {
		t.Fatalf("Identity() = %q", f.Identity())
	}
}

// TestFailureCarriesItsCommitState: the error a family returns unwraps to
// its cause and names the commit state the boundary settles by.
func TestFailureCarriesItsCommitState(t *testing.T) {
	cause := errors.New("connection reset")
	err := family.Fail(family.CommitUnknown, cause)
	var failure *family.Failure
	if !errors.As(err, &failure) || failure.Commit != family.CommitUnknown || !errors.Is(err, cause) {
		t.Fatalf("Fail() = %v", err)
	}
	slots := (family.Schema{Fields: []family.Field{
		{Name: "a", Classification: family.Persist},
		{Name: "t", Classification: family.SecretSlot, Secret: &family.Slot{Name: "x", Scope: family.ScopeRepository}},
	}}).SecretSlots()
	if len(slots) != 1 || slots[0].Name != "t" {
		t.Fatalf("SecretSlots() = %v", slots)
	}
}

// TestClosedSetsAreReadOnly (PR #384 review): the classification and
// effect-site sets are reachable only through Valid and a cloned view, so a
// package cannot widen the policy set before the registry validates
// against it.
func TestClosedSetsAreReadOnly(t *testing.T) {
	all := family.AllClassifications()
	all[0] = "widened"
	if family.Classification("widened").Valid() || !family.Persist.Valid() || family.Classification("").Valid() {
		t.Fatal("mutating the returned view changed the closed set, or a member is not valid")
	}
	sites := family.AllEffectSites()
	sites[0] = "elsewhere"
	if family.EffectSite("elsewhere").Valid() || !family.OrchestratorSide.Valid() || family.EffectSite("").Valid() {
		t.Fatal("mutating the returned view changed the closed set, or a member is not valid")
	}
	if len(all) != 5 || len(sites) != 3 {
		t.Fatalf("%d classifications, %d effect sites", len(all), len(sites))
	}
}
