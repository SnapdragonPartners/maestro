// Package prompt is the harness side of prompt packs (ADR 0031; Phase 3
// item 4 design, D3): the code-resident slot vocabulary, the per-slot
// variable contract, the entry parser and renderer, and the projection a
// pack's identity is digested over.
//
// A pack's persisted records live in the data plane. What lives here is
// everything that decides whether a pack is USABLE by the harness that will
// run it, which is a property of the harness and not of the plane: what slots
// exist, which roles require them, and what variables a slot's entry may
// reference. The seam reaches this package through a contract it declares
// itself (design D3), so the plane validates every pack write without
// importing a renderer.
//
// # Vocabulary growth
//
// This package ships no seed vocabulary: a slot is registered by the item
// that first renders it (design D1), which is configkeys' rule one level up.
// Item 4 has no model caller, so item 4 registers nothing and the tests
// register their own fixtures. A registered slot with no renderer is a guess
// about a future call site.
//
// # The per-slot variable contract
//
// v1's renderer hands every template one bag carrying every field any
// template might want, so nothing states which variables a given template may
// use and a template referencing a field the harness stopped supplying fails
// at render, mid-run. Here each slot declares the variables the harness
// supplies for it. An entry referencing anything else is refused at import,
// and a render call supplying anything other than the declared set is refused
// too: the contract binds the harness as well as the pack.
package prompt

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
)

// SlotKey is the canonical dotted name of a slot. It is persisted protocol:
// it is a key of the object pack-jcs-sha256-v1 digests, so renaming a slot
// changes the identity of every pack that fills it.
type SlotKey string

// Role names an agent role a slot is required for. Roles have no vocabulary
// of their own: a role is known exactly when some registered slot names it,
// so a pack cannot declare coverage of a role the harness asks nothing of.
type Role string

// Variable names one value the harness supplies when it renders a slot.
type Variable string

// Sentinel errors. Each is distinguished because the remedy differs: a slot
// or role the harness does not know, a declared role the entries do not
// satisfy, text the parser rejects, text outside the admitted dialect, a
// variable the slot does not supply, and a harness that broke its own
// contract are six different things to fix.
var (
	// ErrUnknownSlot reports a slot key absent from the registry.
	ErrUnknownSlot = errors.New("prompt slot is not registered")

	// ErrUnknownRole reports a declared role no registered slot names.
	ErrUnknownRole = errors.New("prompt role is not known to any registered slot")

	// ErrMissingSlot reports a slot a declared role requires and the pack
	// does not supply.
	ErrMissingSlot = errors.New("prompt pack does not supply a slot a declared role requires")

	// ErrInvalidEntry reports entry text that cannot be an entry at all:
	// blank, or bytes the pack's identity could not faithfully cover.
	ErrInvalidEntry = errors.New("prompt entry text is not admissible")

	// ErrParse reports an entry the running renderer cannot parse.
	ErrParse = errors.New("prompt entry does not parse")

	// ErrDialect reports an entry that parses and uses a construct the
	// admitted dialect excludes.
	ErrDialect = errors.New("prompt entry uses a construct outside the admitted dialect")

	// ErrUndeclaredVariable reports an entry referencing a variable its slot
	// does not supply.
	ErrUndeclaredVariable = errors.New("prompt entry references a variable its slot does not supply")

	// ErrVariableSet reports a render call whose values are not exactly the
	// slot's declared variables.
	ErrVariableSet = errors.New("render values are not exactly the slot's declared variables")

	// ErrRender reports an entry that passed every static check and still
	// failed to execute.
	ErrRender = errors.New("prompt entry failed to render")
)

var (
	// slotPattern is configkeys' canonical form, for configkeys' reason: the
	// key is an identity, and admitting one spelling leaves nothing to
	// reconcile.
	slotPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)*$`)

	// rolePattern is one lowercase segment. Role names are also the keys of
	// an installation's declared-coverage object, so they are canonicalised
	// here rather than at the column.
	rolePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

	// variablePattern admits what the dialect can reference as `.Name`: an
	// identifier, capitalised by the convention every Go template follows.
	variablePattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
)

// Slot is one slot's registration.
type Slot struct {
	// Roles are the roles that require this slot: a pack declaring coverage
	// of any of them must supply it. At least one, because a slot no role
	// requires is one no coverage check could ever demand.
	Roles []Role

	// Variables are the values the harness supplies when it renders this
	// slot, all of them and no others. May be empty: a slot whose entry is
	// fixed text has an empty contract, not a missing one.
	Variables []Variable
}

// Registry is an immutable set of slot registrations.
//
// Immutable for configkeys' reason: it is consulted on every pack write to
// decide what a slot IS, and a registry mutable at run time would let two
// installs of one pack be judged against different contracts with nothing
// recording which.
type Registry struct {
	slots map[SlotKey]Slot
}

// New validates and freezes a set of registrations.
func New(slots map[SlotKey]Slot) (*Registry, error) {
	frozen := make(map[SlotKey]Slot, len(slots))

	// Sorted so a set with two bad registrations reports the same one on
	// every run.
	for _, key := range slices.Sorted(maps.Keys(slots)) {
		slot := slots[key]
		if err := checkSlot(key, slot); err != nil {
			return nil, err
		}
		frozen[key] = Slot{
			Roles:     slices.Clone(slot.Roles),
			Variables: slices.Clone(slot.Variables),
		}
	}
	return &Registry{slots: frozen}, nil
}

// MustNew is New for package-level registrations, where a malformed slot is a
// build-time mistake and there is no caller to return an error to.
func MustNew(slots map[SlotKey]Slot) *Registry {
	built, err := New(slots)
	if err != nil {
		panic(err)
	}
	return built
}

func checkSlot(key SlotKey, slot Slot) error {
	if !slotPattern.MatchString(string(key)) {
		return fmt.Errorf("prompt: slot %q is not a canonical dotted name "+
			"(lowercase segments of letters, digits and hyphens, each starting with a letter)", key)
	}
	if len(slot.Roles) == 0 {
		return fmt.Errorf("prompt: slot %q names no role, so no coverage check could ever require it", key)
	}
	seenRoles := make(map[Role]bool, len(slot.Roles))
	for _, role := range slot.Roles {
		if !rolePattern.MatchString(string(role)) {
			return fmt.Errorf("prompt: slot %q names role %q, which is not a canonical role name "+
				"(one lowercase segment of letters, digits and hyphens, starting with a letter)", key, role)
		}
		if seenRoles[role] {
			return fmt.Errorf("prompt: slot %q lists role %q twice", key, role)
		}
		seenRoles[role] = true
	}
	seenVariables := make(map[Variable]bool, len(slot.Variables))
	for _, variable := range slot.Variables {
		if !variablePattern.MatchString(string(variable)) {
			return fmt.Errorf("prompt: slot %q declares variable %q, which an entry could not reference "+
				"(an identifier starting with a capital letter)", key, variable)
		}
		if seenVariables[variable] {
			return fmt.Errorf("prompt: slot %q lists variable %q twice", key, variable)
		}
		seenVariables[variable] = true
	}
	return nil
}

// Slots returns every registered slot key in a stable order.
func (r *Registry) Slots() []SlotKey {
	return slices.Sorted(maps.Keys(r.slots))
}

// Roles returns every role some registered slot names, in a stable order.
func (r *Registry) Roles() []Role {
	known := make(map[Role]bool)
	for key := range r.slots {
		for _, role := range r.slots[key].Roles {
			known[role] = true
		}
	}
	return slices.Sorted(maps.Keys(known))
}
