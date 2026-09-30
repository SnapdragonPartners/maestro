package boundary_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/boundary"
	"orchestrator/internal/boundary/family"
)

// TestRegistryAcceptsTheTestFamilies is the positive control: the three
// test families load, look up by identity and list sorted. Without it the
// refusal table below would pass against a constructor that refused
// everything.
func TestRegistryAcceptsTheTestFamilies(t *testing.T) {
	r, err := boundary.NewRegistry(testFamilies()...)
	if err != nil {
		t.Fatalf("the test families were refused: %v", err)
	}
	want := []string{"test/fail_after_commit", "test/never_returns", "test/noop"}
	if got := r.Identities(); !slices.Equal(got, want) {
		t.Fatalf("Identities() = %v, want %v", got, want)
	}
	f, ok := r.Lookup("test/noop")
	if !ok || f.Verb != "noop" {
		t.Fatalf("Lookup(test/noop) = %+v, %v", f, ok)
	}
	if _, ok := r.Lookup("test/other"); ok {
		t.Fatal("an unregistered identity was found")
	}
	// The identities slice is a copy: mutating it changes nothing.
	r.Identities()[0] = "mutated"
	if got := r.Identities(); got[0] != want[0] {
		t.Fatalf("Identities() returned its backing array: %v", got)
	}
}

// TestRegistryRefusesEveryMalformedFamily is design D3's construction
// validation, one refusal per rule. Each case takes a valid family and
// breaks ONE thing, so a refusal is attributable to that thing and not to a
// fixture that was never valid.
//
// THE MUTANT: remove any one check from validateFamily or validateSchema
// and its row here loads a family the design says cannot be loaded.
func TestRegistryRefusesEveryMalformedFamily(t *testing.T) {
	for name, tc := range map[string]struct {
		breakIt func(*family.Family)
		wantIn  string
	}{
		"blank kind":      {func(f *family.Family) { f.Kind = "" }, "kind"},
		"upper-case verb": {func(f *family.Family) { f.Verb = "Noop" }, "verb"},
		"slash in verb":   {func(f *family.Family) { f.Verb = "no/op" }, "verb"},
		"no description":  {func(f *family.Family) { f.Description = " " }, "no description"},
		"unknown effect site": {func(f *family.Family) { f.EffectSite = "somewhere" },
			"effect site"},
		"no checkability": {func(f *family.Family) { f.Checkability = "" }, "checkability"},
		"no commit point": {func(f *family.Family) { f.CommitPoint = "" }, "commit point"},
		"no resolver":     {func(f *family.Family) { f.Resolve = nil }, "resolver"},
		"no effect":       {func(f *family.Family) { f.Effect = nil }, "no effect"},
		"no reconcile":    {func(f *family.Family) { f.Reconcile = nil }, "reconciliation probe"},
		"field name not a key": {func(f *family.Family) { f.Schema.Fields[0].Name = "Note-1" },
			"not a lower-case"},
		"field declared twice": {func(f *family.Family) { f.Schema.Fields[1].Name = f.Schema.Fields[0].Name },
			"declared twice"},
		"unknown field type": {func(f *family.Family) { f.Schema.Fields[0].Type = "object" }, "type"},
		"unclassified field": {func(f *family.Family) { f.Schema.Fields[0].Classification = "" },
			"classification"},
		"keyed commitment declared": {
			func(f *family.Family) { f.Schema.Fields[0].Classification = family.KeyedCommitment },
			"not implemented"},
		"secret slot without a slot": {func(f *family.Family) { f.Schema.Fields[5].Secret = nil },
			"go together"},
		"slot without the classification": {
			func(f *family.Family) { f.Schema.Fields[5].Classification = family.Persist }, "go together"},
		"required secret slot": {func(f *family.Family) { f.Schema.Fields[5].Required = true },
			"never required"},
		"secret slot not a string": {func(f *family.Family) { f.Schema.Fields[5].Type = family.Integer },
			"a secret slot is a string field"},
		"slot naming no secret": {func(f *family.Family) { f.Schema.Fields[5].Secret.Name = "" },
			"names no secret"},
		"slot at an unknown scope": {func(f *family.Family) { f.Schema.Fields[5].Secret.Scope = "planet" },
			"secret scope"},
		"secret slot in the result": {func(f *family.Family) {
			f.ResultSchema.Fields = append(f.ResultSchema.Fields, family.Field{
				Name: "leak", Type: family.String, Classification: family.SecretSlot,
				Secret: &family.Slot{Name: "x", Scope: family.ScopeRepository},
			})
		}, "result schema"},
	} {
		t.Run(name, func(t *testing.T) {
			f := noopFamily()
			tc.breakIt(&f)
			_, err := boundary.NewRegistry(f)
			if !errors.Is(err, boundary.ErrInvalidFamily) {
				t.Fatalf("err = %v, want ErrInvalidFamily", err)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("the refusal does not name the rule: %v (want %q in it)", err, tc.wantIn)
			}
		})
	}

	t.Run("an identity declared twice", func(t *testing.T) {
		_, err := boundary.NewRegistry(noopFamily(), noopFamily())
		if !errors.Is(err, boundary.ErrInvalidFamily) || !strings.Contains(err.Error(), "twice") {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestValidateCapabilitiesNamesEveryUnknownIdentity is the seam's
// dispatch-time check (D12) at the unit: known identities pass, every
// unknown one is named, and the empty set passes against the empty
// registry -- which is what a root that dispatches nothing declares.
func TestValidateCapabilitiesNamesEveryUnknownIdentity(t *testing.T) {
	r := boundary.MustNewRegistry(testFamilies()...)
	if err := r.ValidateCapabilities([]string{"test/noop", "test/never_returns"}); err != nil {
		t.Fatalf("registered identities were refused: %v", err)
	}
	if err := r.ValidateCapabilities(nil); err != nil {
		t.Fatalf("the empty set was refused: %v", err)
	}
	err := r.ValidateCapabilities([]string{"test/noop", "forge/story_pull_request", "test/nope", "test/nope"})
	if !errors.Is(err, boundary.ErrUnknownFamily) {
		t.Fatalf("err = %v, want ErrUnknownFamily", err)
	}
	for _, want := range []string{"forge/story_pull_request, test/nope", "registered: test/fail_after_commit"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not contain %q", err, want)
		}
	}
	if strings.Count(err.Error(), "test/nope") != 1 {
		t.Fatalf("a repeated unknown identity is named once: %v", err)
	}

	empty := boundary.MustNewRegistry()
	if err := empty.ValidateCapabilities([]string{}); err != nil {
		t.Fatalf("the empty registry refused the empty set: %v", err)
	}
	if err := empty.ValidateCapabilities([]string{"test/noop"}); !errors.Is(err, boundary.ErrUnknownFamily) {
		t.Fatalf("the empty registry admitted an identity: %v", err)
	}
}

// TestFamiliesIsEmptyInCommitTwo pins what the production set holds today,
// so the commit that adds the first family changes this test deliberately.
func TestFamiliesIsEmptyInCommitTwo(t *testing.T) {
	if got := boundary.Families().Identities(); len(got) != 0 {
		t.Fatalf("the production set holds %v; the sequence adds the first family in commit 4", got)
	}
}

// TestRegistryHoldsAnIndependentCopy is PR review round 1's second P1: the
// copy taken at construction must be deep. A shallow copy left the field
// slices and slot pointers shared, so a caller flipping its own declaration
// from digest_only to persist after registration changed what the
// registered schema persisted -- without revalidation, and without any
// Lookup result being touched.
//
// THE MUTANT: `f := families[i]` in place of cloneFamily -- the registered
// field reads persist, and the slot reads the changed name.
func TestRegistryHoldsAnIndependentCopy(t *testing.T) {
	declared := noopFamily()
	r := boundary.MustNewRegistry(declared)

	declared.Schema.Fields[1].Classification = family.Persist // hint: digest_only → persist
	declared.Schema.Fields[5].Secret.Name = "forge.other"     // token's slot
	declared.ResultSchema.Fields[0].Classification = family.DigestOnly
	declared.Description = "changed after registration"

	registered, ok := r.Lookup("test/noop")
	if !ok {
		t.Fatal("the family is not registered")
	}
	if _, found := r.Lookup("test/absent"); found {
		t.Fatal("an unregistered identity was found")
	}
	hint, _ := registered.Schema.Field("hint")
	if hint.Classification != family.DigestOnly {
		t.Fatalf("hint is %q on the registered schema after the caller changed its own declaration", hint.Classification)
	}
	token, _ := registered.Schema.Field("token")
	if token.Secret.Name != "forge.token" {
		t.Fatalf("the registered slot names %q after the caller changed its own", token.Secret.Name)
	}
	if registered.ResultSchema.Fields[0].Classification != family.Persist || registered.Description == declared.Description {
		t.Fatal("the result schema or description followed the caller's later change")
	}
	// Through Lookup too (PR #384 review): the result is a copy, so mutating
	// it -- its field slices and slot pointers included -- changes nothing a
	// later lookup sees.
	leaked, _ := r.Lookup("test/noop")
	leaked.Schema.Fields[1].Classification = family.Persist
	leaked.Schema.Fields[5].Secret.Name = "forge.leaked"
	leaked.ResultSchema.Fields[0].Name = "leaked"
	again, _ := r.Lookup("test/noop")
	if hint, _ := again.Schema.Field("hint"); hint.Classification != family.DigestOnly {
		t.Fatalf("hint is %q after a Lookup result was mutated", hint.Classification)
	}
	if token, _ := again.Schema.Field("token"); token.Secret.Name != "forge.token" {
		t.Fatalf("the slot names %q after a Lookup result's slot was mutated", token.Secret.Name)
	}
	if again.ResultSchema.Fields[0].Name != "echo" {
		t.Fatal("the result schema followed a Lookup result's mutation")
	}
	registered = again

	// And the consequence the finding named: substitution still treats the
	// field as digest-only.
	got, err := boundary.Substitute(registered.Schema, map[string]any{"note": "x", "hint": "unrecorded"},
		map[string]boundary.SecretReference{"token": reference(t)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Projection), "unrecorded") {
		t.Fatalf("a digest-only value was persisted after the caller's post-registration change: %s", got.Projection)
	}
}
