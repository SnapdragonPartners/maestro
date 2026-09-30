package boundary_test

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"orchestrator/internal/boundary"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TestRequirementDigestIsOrderIndependent is item 2's deferred test --
// "two evaluations collecting the same requirements in different orders
// produce the same digest" -- written here against real requirements
// rather than opaque keys (design D4). Order of collection AND order of a
// requirement's scopes: the first is the object's key order, which RFC 8785
// sorts; the second is an array's, which it does not, so Canonical sorts it.
//
// THE MUTANT: leave PermittedScopes in the order given and the two sets
// below digest differently, which is the second assertion.
func TestRequirementDigestIsOrderIndependent(t *testing.T) {
	qualified, err := boundary.OperatorApprovalOf("forge/story_pull_request")
	if err != nil {
		t.Fatal(err)
	}
	first := boundary.RequirementSet{
		boundary.OperatorApproval(): {Question: "open a pull request?", PermittedScopes: []boundary.RequirementScope{boundary.ScopeOnce, boundary.ScopeForStory}},
		qualified:                   {Question: "this family?", PermittedScopes: []boundary.RequirementScope{boundary.ScopeOnce}},
	}
	second := boundary.RequirementSet{
		qualified:                   {Question: "this family?", PermittedScopes: []boundary.RequirementScope{boundary.ScopeOnce}},
		boundary.OperatorApproval(): {Question: "open a pull request?", PermittedScopes: []boundary.RequirementScope{boundary.ScopeForStory, boundary.ScopeOnce}},
	}
	a, err := first.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !hex64.MatchString(a.Digest) {
		t.Fatalf("digest %q is not 64 lowercase hex", a.Digest)
	}
	if a.Digest != b.Digest {
		t.Fatalf("the same requirements collected in different orders digest differently:\n%s\n%s", a.JSON, b.JSON)
	}
	// The recorded JSON is the object 000022 requires, keyed by identity.
	var decoded map[string]struct {
		Question string   `json:"question"`
		Scopes   []string `json:"permitted_scopes"`
	}
	if decodeErr := json.Unmarshal(a.JSON, &decoded); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if got := decoded["policy/operator_approval"].Scopes; len(got) != 2 || got[0] != "for_story" || got[1] != "once" {
		t.Fatalf("scopes recorded as %v, want sorted [for_story once]", got)
	}
	// The caller's set was not modified by canonicalisation.
	if scopes := first[boundary.OperatorApproval()].PermittedScopes; scopes[0] != boundary.ScopeOnce {
		t.Fatal("Canonical sorted the caller's own slice")
	}

	// A different question is a different requirement.
	changed := boundary.RequirementSet{
		boundary.OperatorApproval(): {Question: "open a DIFFERENT pull request?", PermittedScopes: []boundary.RequirementScope{boundary.ScopeOnce, boundary.ScopeForStory}},
		qualified:                   {Question: "this family?", PermittedScopes: []boundary.RequirementScope{boundary.ScopeOnce}},
	}
	c, err := changed.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if c.Digest == a.Digest {
		t.Fatal("a changed question digested the same")
	}
}

// TestRequirementIdentityIsAClosedEnumeration: the two item 5 identities
// parse, and everything else is refused by the rule it breaks.
func TestRequirementIdentityIsAClosedEnumeration(t *testing.T) {
	parsed, err := boundary.ParseRequirementIdentity(boundary.OperatorApproval())
	if err != nil || parsed.Gate != boundary.GatePolicy || parsed.Kind != boundary.KindOperatorApproval || parsed.Qualifier != "" {
		t.Fatalf("ParseRequirementIdentity(unqualified) = %+v, %v", parsed, err)
	}
	qualified, err := boundary.OperatorApprovalOf("forge/story_pull_request")
	if err != nil {
		t.Fatal(err)
	}
	if qualified != "policy/operator_approval/forge/story_pull_request" {
		t.Fatalf("qualified identity = %q", qualified)
	}
	parsed, err = boundary.ParseRequirementIdentity(qualified)
	if err != nil || parsed.Qualifier != "forge/story_pull_request" {
		t.Fatalf("ParseRequirementIdentity(qualified) = %+v, %v", parsed, err)
	}
	if _, err := boundary.OperatorApprovalOf("not-a-family"); !errors.Is(err, boundary.ErrInvalidRequirement) {
		t.Fatalf("a malformed qualifier was accepted: %v", err)
	}

	for name, identity := range map[string]boundary.RequirementIdentity{
		"upper-case":            "Policy/operator_approval",
		"no slash":              "policy",
		"empty kind":            "policy/",
		"unknown gate":          "resource/operator_approval",
		"unknown kind":          "policy/human_review",
		"malformed qualifier":   "policy/operator_approval/forge",
		"qualifier with a dash": "policy/operator_approval/forge/story-pr",
		"trailing slash":        "policy/operator_approval/",
		"empty":                 "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := boundary.ParseRequirementIdentity(identity); !errors.Is(err, boundary.ErrInvalidRequirement) {
				t.Fatalf("%q was accepted: %v", identity, err)
			}
		})
	}
}

// TestRequirementSetValidateRefusesWhatCannotBeAnswered: each rule, one
// case, with the error naming the identity so an operator can find it.
func TestRequirementSetValidateRefusesWhatCannotBeAnswered(t *testing.T) {
	for name, set := range map[string]boundary.RequirementSet{
		"unknown identity": {"policy/telepathy": {Question: "?", PermittedScopes: []boundary.RequirementScope{boundary.ScopeOnce}}},
		"no question":      {boundary.OperatorApproval(): {Question: " ", PermittedScopes: []boundary.RequirementScope{boundary.ScopeOnce}}},
		"no scope":         {boundary.OperatorApproval(): {Question: "?"}},
		"unknown scope":    {boundary.OperatorApproval(): {Question: "?", PermittedScopes: []boundary.RequirementScope{"forever"}}},
		"repeated scope":   {boundary.OperatorApproval(): {Question: "?", PermittedScopes: []boundary.RequirementScope{boundary.ScopeOnce, boundary.ScopeOnce}}},
	} {
		t.Run(name, func(t *testing.T) {
			err := set.Validate()
			if !errors.Is(err, boundary.ErrInvalidRequirement) {
				t.Fatalf("err = %v, want ErrInvalidRequirement", err)
			}
			if _, canonErr := set.Canonical(); canonErr == nil {
				t.Fatal("Canonical produced a recorded form for a set Validate refuses")
			}
			if name != "unknown identity" && !strings.Contains(err.Error(), "policy/operator_approval") {
				t.Fatalf("the refusal does not name the identity: %v", err)
			}
		})
	}
	// The empty set is valid: an evaluation that raised nothing.
	if err := (boundary.RequirementSet{}).Validate(); err != nil {
		t.Fatalf("the empty set was refused: %v", err)
	}
}
