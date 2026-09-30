package boundary

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"orchestrator/internal/boundary/family"
	"orchestrator/internal/dataplane/canonical"
)

// The requirement-identity vocabulary item 2 left opaque (design D4;
// migration 000022's requirement_set is "an OBJECT keyed by requirement
// identity" whose keys item 2 "treats as opaque"). An identity is
// "<gate>/<kind>" with an optional "/<qualifier>", lower-case, from a closed
// enumeration this package owns. For item 5 the enumeration is two entries:
//
//	policy/operator_approval
//	policy/operator_approval/<family>
//
// where <family> is a family identity, "<kind>/<verb>". The qualifier
// therefore contains a slash of its own; the identity is split at its first
// two, and what remains is parsed as a family identity by the one parser the
// registry uses, so the two vocabularies cannot disagree.

// RequirementIdentity is one key of a requirement set.
type RequirementIdentity string

// Gate is the gate that raised a requirement.
type Gate string

// RequirementKind is what the gate requires.
type RequirementKind string

// The closed enumeration for item 5. A new gate or kind is added here, beside
// the gate that produces it, and nowhere else.
const (
	GatePolicy Gate = "policy"

	KindOperatorApproval RequirementKind = "operator_approval"
)

// gateKinds is which kinds each gate may raise.
//
//nolint:gochecknoglobals // Immutable enumeration.
var gateKinds = map[Gate][]RequirementKind{
	GatePolicy: {KindOperatorApproval},
}

// ErrInvalidRequirement is what a malformed identity, requirement or set is
// refused with.
var ErrInvalidRequirement = errors.New("invalid requirement")

// OperatorApproval is the unqualified identity: an operator must approve.
func OperatorApproval() RequirementIdentity {
	return RequirementIdentity(string(GatePolicy) + "/" + string(KindOperatorApproval))
}

// OperatorApprovalOf is the family-qualified identity: an operator must
// approve this family's action. The family identity is validated by shape;
// whether it is registered is the caller's question to the registry.
func OperatorApprovalOf(familyIdentity string) (RequirementIdentity, error) {
	if _, _, err := family.ParseIdentity(familyIdentity); err != nil {
		return "", fmt.Errorf("%w: qualifier: %w", ErrInvalidRequirement, err)
	}
	return RequirementIdentity(string(OperatorApproval()) + "/" + familyIdentity), nil
}

// ParsedRequirement is an identity taken apart.
type ParsedRequirement struct {
	Gate Gate
	Kind RequirementKind
	// Qualifier is the family identity, or empty for the unqualified form.
	Qualifier string
}

// ParseRequirementIdentity refuses everything outside the closed enumeration.
func ParseRequirementIdentity(identity RequirementIdentity) (ParsedRequirement, error) {
	text := string(identity)
	if text != strings.ToLower(text) {
		return ParsedRequirement{}, fmt.Errorf("%w: identity %q is not lower-case", ErrInvalidRequirement, text)
	}
	gateText, rest, found := strings.Cut(text, "/")
	if !found || gateText == "" || rest == "" {
		return ParsedRequirement{}, fmt.Errorf("%w: identity %q is not <gate>/<kind>[/<qualifier>]", ErrInvalidRequirement, text)
	}
	kindText, qualifier, qualified := strings.Cut(rest, "/")
	if qualified && qualifier == "" {
		return ParsedRequirement{}, fmt.Errorf("%w: identity %q has a slash and no qualifier after it", ErrInvalidRequirement, text)
	}
	gate, kind := Gate(gateText), RequirementKind(kindText)
	kinds, known := gateKinds[gate]
	if !known {
		return ParsedRequirement{}, fmt.Errorf("%w: gate %q is not one this boundary has", ErrInvalidRequirement, gateText)
	}
	if !slices.Contains(kinds, kind) {
		return ParsedRequirement{}, fmt.Errorf("%w: gate %q raises no %q requirement", ErrInvalidRequirement, gateText, kindText)
	}
	if qualifier != "" {
		if _, _, err := family.ParseIdentity(qualifier); err != nil {
			return ParsedRequirement{}, fmt.Errorf("%w: qualifier of %q: %w", ErrInvalidRequirement, text, err)
		}
	}
	return ParsedRequirement{Gate: gate, Kind: kind, Qualifier: qualifier}, nil
}

// RequirementScope is what an operator's answer may cover (ADR 0030 section
// 4). Only "once" has a consumer in item 5; "for_story" is representable so
// the deferred grant is a producer, not a vocabulary change.
type RequirementScope string

// The scopes.
const (
	ScopeOnce     RequirementScope = "once"
	ScopeForStory RequirementScope = "for_story"
)

// requirementScopes is the closed set.
//
//nolint:gochecknoglobals // Immutable enumeration.
var requirementScopes = []RequirementScope{ScopeOnce, ScopeForStory}

// Requirement is the structured requirement under one identity: the
// question the operator is asked and the scopes an answer may take.
//
// PermittedScopes is a SET. It is stored as a JSON array, which RFC 8785
// leaves in the order given, so Canonical sorts and de-duplicates it before
// digesting: two evaluations that permit the same scopes in different orders
// raise the same requirement.
type Requirement struct {
	Question        string             `json:"question"`
	PermittedScopes []RequirementScope `json:"permitted_scopes"`
}

// RequirementSet is the complete set one evaluation collected, keyed by
// identity (000022's object shape). A map because the set is unordered:
// the digest must not depend on the order requirements were collected in.
type RequirementSet map[RequirementIdentity]Requirement

// Validate refuses a set that could not be recorded or answered: an
// identity outside the enumeration, a requirement with no question, or
// scopes that are empty, unknown or repeated.
func (s RequirementSet) Validate() error {
	for identity := range s {
		requirement := s[identity]
		if _, err := ParseRequirementIdentity(identity); err != nil {
			return err
		}
		if strings.TrimSpace(requirement.Question) == "" {
			return fmt.Errorf("%w: %s asks no question", ErrInvalidRequirement, identity)
		}
		if len(requirement.PermittedScopes) == 0 {
			return fmt.Errorf("%w: %s permits no scope, so no answer could satisfy it", ErrInvalidRequirement, identity)
		}
		seen := make(map[RequirementScope]bool, len(requirement.PermittedScopes))
		for _, scope := range requirement.PermittedScopes {
			if !slices.Contains(requirementScopes, scope) {
				return fmt.Errorf("%w: %s permits scope %q, not one of %v", ErrInvalidRequirement, identity, scope, requirementScopes)
			}
			if seen[scope] {
				return fmt.Errorf("%w: %s permits scope %q twice", ErrInvalidRequirement, identity, scope)
			}
			seen[scope] = true
		}
	}
	return nil
}

// CanonicalRequirements is a set in its recorded form: the JSON the seam
// stores in requirement_set and the digest gate 3 compares by.
type CanonicalRequirements struct {
	Digest string
	JSON   json.RawMessage
}

// Canonical validates the set and produces its recorded form.
//
// The JSON is what the seam stores; jsonb does not preserve key order, and
// the digest is over RFC 8785's canonical form (the plane's, from
// `canonical`), so two sets that differ only in collection order or in the
// order of a requirement's scopes digest identically. That is the item 2
// test this package writes against real requirements rather than opaque
// keys (D4).
func (s RequirementSet) Canonical() (CanonicalRequirements, error) {
	if err := s.Validate(); err != nil {
		return CanonicalRequirements{}, err
	}
	// The empty set is a valid VALUE -- it is what an evaluation that raised
	// nothing returns, and the identity when hook results are composed --
	// but it has no recorded form: the seam refuses an empty requirement set
	// at every wait entry ("a wait with nothing to wait on is not a wait",
	// store/postgres/attempts.go), and a boundary that built one would
	// discover that a transaction later. Refused here, by the same rule
	// (PR #384 review).
	if len(s) == 0 {
		return CanonicalRequirements{}, fmt.Errorf("%w: the empty set has no recorded form; a wait with nothing to wait on is not a wait", ErrInvalidRequirement)
	}
	// Scopes sorted per requirement, into a copy: the caller's set is not
	// modified, and the stored form is the set's, not the collection's.
	ordered := make(map[RequirementIdentity]Requirement, len(s))
	for identity := range s {
		scopes := slices.Clone(s[identity].PermittedScopes)
		slices.Sort(scopes)
		ordered[identity] = Requirement{Question: s[identity].Question, PermittedScopes: scopes}
	}
	raw, err := json.Marshal(ordered)
	if err != nil {
		return CanonicalRequirements{}, fmt.Errorf("encode requirement set: %w", err)
	}
	digest, err := canonical.DigestJSON(raw)
	if err != nil {
		return CanonicalRequirements{}, fmt.Errorf("digest requirement set: %w", err)
	}
	return CanonicalRequirements{JSON: raw, Digest: digest}, nil
}
