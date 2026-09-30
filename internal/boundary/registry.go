// Package boundary is the mediated execution boundary (Phase 3 item 5): the
// one route through which an agent-initiated action reaches its effect.
//
// Sequence commit 2 (`registry`) puts in place what the gates are built on:
// the closed set of action families with its construction validation (D3),
// the requirement-identity vocabulary (D4), and substitution with the
// persisted projection (D6). The gates themselves -- Mediate, the hook, the
// waits, revalidation, reconciliation -- are commit 3's.
//
// Orchestrator-owned and inside item 3's closure rule (D1): this package
// imports the seam, its neutral helpers, the family leaf and nothing under
// pkg/. It does not import pkg/tools or the toolloop; the vocabulary those
// share with it lives in internal/action.
package boundary

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"orchestrator/internal/boundary/family"
)

// Registry is the closed set of action families (D3), assembled once at
// composition and consulted by every gate. Immutable after construction.
type Registry struct {
	byIdentity map[string]*family.Family
	identities []string
}

// ErrInvalidFamily is what a malformed family declaration is refused with.
var ErrInvalidFamily = errors.New("invalid action family")

// ErrUnknownFamily is what an identity the registry does not hold is refused
// with -- at dispatch, where a capability set is validated (D12), and at
// admission, where a request names its family (D4).
var ErrUnknownFamily = errors.New("unknown action family")

// NewRegistry validates every family and assembles the set.
//
// The validation is the whole point of construction (D3): identities
// unique and well-formed, every schema field classified from the closed
// set, every family with a checkability answer, a commit point, a target
// resolver, an effect and a reconciliation probe -- for every effect site,
// not only orchestrator_side, because every interrupted attempt is
// reconciled (D5) and a family without a probe would have no recovery. This
// is item 4's prompt-contract shape: the seam validates at construction, so
// a malformed family cannot be loaded, let alone recorded.
//
// An EMPTY registry is valid. A composition root that dispatches nothing
// declares that with one, as configkeys.MustNew(nil) declares no keys, and
// every capability set it then sees is refused as unknown.
func NewRegistry(families ...family.Family) (*Registry, error) {
	r := &Registry{byIdentity: make(map[string]*family.Family, len(families))}
	for i := range families {
		// The registry holds its own copy, DEEP: a caller that later mutates
		// the value it passed -- or the field slices and slot pointers it
		// still holds -- changes nothing the gates see. A shallow copy kept
		// the slices shared, and a caller flipping a field from digest_only
		// to persist after registration would have changed what the
		// substitution persisted, unvalidated (PR review round 1).
		f := cloneFamily(&families[i])
		if err := validateFamily(&f); err != nil {
			return nil, err
		}
		identity := f.Identity()
		if _, dup := r.byIdentity[identity]; dup {
			return nil, fmt.Errorf("%w: identity %q is declared twice", ErrInvalidFamily, identity)
		}
		r.byIdentity[identity] = &f
		r.identities = append(r.identities, identity)
	}
	slices.Sort(r.identities)
	return r, nil
}

// cloneFamily copies a declaration and everything it points to.
func cloneFamily(f *family.Family) family.Family {
	c := *f
	c.Schema = cloneSchema(f.Schema)
	c.ResultSchema = cloneSchema(f.ResultSchema)
	return c
}

func cloneSchema(s family.Schema) family.Schema {
	if s.Fields == nil {
		return family.Schema{}
	}
	fields := make([]family.Field, len(s.Fields))
	for i := range s.Fields {
		fields[i] = s.Fields[i]
		if s.Fields[i].Secret != nil {
			slot := *s.Fields[i].Secret
			fields[i].Secret = &slot
		}
	}
	return family.Schema{Fields: fields}
}

// MustNewRegistry is NewRegistry for a set fixed at compile time, where a
// refusal is a programming error and the program should not start.
func MustNewRegistry(families ...family.Family) *Registry {
	r, err := NewRegistry(families...)
	if err != nil {
		panic(fmt.Sprintf("boundary: the compiled-in family set is invalid: %v", err))
	}
	return r
}

// Families is the PRODUCTION set: every family under internal/boundary/families,
// assembled here and nowhere else (D2's first guard counts this package as
// the one importer). The composition roots hand it to the seam as
// plane.Caller.Actions.
//
// Empty in sequence commit 2. The one production family, the Story pull
// request (D13), arrives with the forge seam in commit 4; the test-only
// families the proofs run live under this package's tests and are never
// registered here.
func Families() *Registry {
	return MustNewRegistry()
}

// Lookup returns the family with this identity, if registered. The pointer
// is to the registry's own copy, which callers must not modify.
func (r *Registry) Lookup(identity string) (*family.Family, bool) {
	f, ok := r.byIdentity[identity]
	return f, ok
}

// Identities returns every registered identity, sorted, as a copy.
func (r *Registry) Identities() []string {
	return slices.Clone(r.identities)
}

// ValidateCapabilities is the seam's dispatch-time check (D12), reached
// through store.ActionContract: every identity must be well-formed and name
// a registered family, so an unknown family cannot be stored now to become
// live under a later registry. Order and repetition are not this method's
// business; the seam canonicalizes what it stores.
//
// Every offending identity is named, not only the first: an operator who
// typed two wrong finds out once.
func (r *Registry) ValidateCapabilities(identities []string) error {
	var unknown []string
	for _, identity := range identities {
		if _, ok := r.byIdentity[identity]; !ok {
			unknown = append(unknown, identity)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	unknown = slices.Compact(unknown)
	known := "none"
	if len(r.identities) != 0 {
		known = strings.Join(r.identities, ", ")
	}
	return fmt.Errorf("%w: %s (registered: %s)", ErrUnknownFamily, strings.Join(unknown, ", "), known)
}

// validateFamily is the per-family half of construction.
func validateFamily(f *family.Family) error {
	if _, _, err := family.ParseIdentity(f.Identity()); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidFamily, err)
	}
	identity := f.Identity()
	refuse := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s: %s", ErrInvalidFamily, identity, fmt.Sprintf(format, args...))
	}
	switch {
	case strings.TrimSpace(f.Description) == "":
		return refuse("no description; the model reads one in the definition")
	case !slices.Contains(family.EffectSites, f.EffectSite):
		return refuse("effect site %q is not one of %v", f.EffectSite, family.EffectSites)
	case strings.TrimSpace(f.Checkability) == "":
		return refuse("no checkability answer; a family that cannot say what prevents the resource " +
			"from performing this directly is mediated in documentation only (ADR 0030 section 7)")
	case strings.TrimSpace(f.CommitPoint) == "":
		return refuse("no commit point; every family declares the instant after which the effect is " +
			"no longer the Orchestrator's to withhold (ADR 0030 section 5)")
	case f.Resolve == nil:
		return refuse("no target resolver")
	case f.Effect == nil:
		return refuse("no effect")
	case f.Reconcile == nil:
		return refuse("no reconciliation probe; every interrupted attempt is reconciled regardless of " +
			"effect site (design D5), and a family without one would have no recovery")
	}
	if err := validateSchema(f.Schema, false); err != nil {
		return refuse("argument schema: %v", err)
	}
	if err := validateSchema(f.ResultSchema, true); err != nil {
		return refuse("result schema: %v", err)
	}
	return nil
}

// fieldTypes is the closed set of JSON types a field may declare.
//
//nolint:gochecknoglobals // Immutable enumeration.
var fieldTypes = []family.FieldType{family.String, family.Integer, family.Number, family.Boolean}

// secretScopes is the closed set a slot may resolve at.
//
//nolint:gochecknoglobals // Immutable enumeration.
var secretScopes = []family.SecretScope{family.ScopeRepository, family.ScopeProduct, family.ScopeOrganization}

// validateSchema checks one schema's declarations: unique keys, and each
// field by validateField. A result schema admits no secret slot: a result
// is what the effect returned, and there is nothing to fill.
func validateSchema(s family.Schema, isResult bool) error {
	seen := make(map[string]bool, len(s.Fields))
	for i := range s.Fields {
		f := &s.Fields[i]
		if seen[f.Name] {
			return fmt.Errorf("field %q is declared twice", f.Name)
		}
		if err := validateField(f, isResult); err != nil {
			return err
		}
		seen[f.Name] = true
	}
	return nil
}

// validateField checks one field's declaration against the closed sets.
func validateField(f *family.Field, isResult bool) error {
	switch {
	case !family.ValidIdentitySegment(f.Name):
		return fmt.Errorf("field %q is not a lower-case [a-z][a-z0-9_]* key", f.Name)
	case !slices.Contains(fieldTypes, f.Type):
		return fmt.Errorf("field %q: type %q is not one of %v", f.Name, f.Type, fieldTypes)
	case !slices.Contains(family.Classifications, f.Classification):
		return fmt.Errorf("field %q: classification %q is not one of %v", f.Name, f.Classification, family.Classifications)
	case f.Classification == family.KeyedCommitment:
		return fmt.Errorf("field %q: keyed commitments are declared and not implemented in item 5 "+
			"(design D6); the classification exists so a family that needs one cannot omit it silently, "+
			"and a family that declares one cannot load until an implementation exists", f.Name)
	case (f.Classification == family.SecretSlot) != (f.Secret != nil):
		return fmt.Errorf("field %q: a secret slot declaration and a %q classification go together", f.Name, family.SecretSlot)
	case f.Secret == nil:
		return nil
	case isResult:
		return fmt.Errorf("field %q: a result schema admits no secret slot", f.Name)
	case f.Required:
		return fmt.Errorf("field %q: a secret slot is never required of the caller, who supplies nothing for it", f.Name)
	case strings.TrimSpace(f.Secret.Name) == "":
		return fmt.Errorf("field %q: the secret slot names no secret", f.Name)
	case !slices.Contains(secretScopes, f.Secret.Scope):
		return fmt.Errorf("field %q: secret scope %q is not one of %v", f.Name, f.Secret.Scope, secretScopes)
	}
	return nil
}
