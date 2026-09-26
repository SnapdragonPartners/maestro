package prompt

import (
	"fmt"
	"maps"
	"slices"

	"orchestrator/internal/dataplane/canonical"
)

// ValidatePack is the import gate (ADR 0031 section 5; design D10): whether
// these entries, declaring coverage of these roles, are usable by THIS
// harness. It is the one governing call, for configkeys.ValidateWrite's
// reason -- the checks are ordered, and a caller reconstructing the order is
// a second, untested copy of the policy.
//
// Entries are keyed by slot and roles are plain strings, not this package's
// types, because the caller is the persistence seam reaching here through a
// contract it declares itself (design D3) and it names nothing of ours.
//
// The order, strongest statement first:
//
//  1. Every entry fills a registered slot. A pack is keyed by the
//     code-resident vocabulary, so an unknown key is not extra content, it is
//     content nothing will ever render.
//  2. Every declared role is known. Coverage of a role no slot names would
//     pass with nothing checked.
//  3. Coverage: every slot a declared role requires is supplied.
//  4. Every entry -- required or not -- parses, stays inside the dialect and
//     inside its slot's variables, and renders.
//
// An empty pack declaring no roles passes all four vacuously, which is the
// built-in's honest state at item 4 (design D1) and proves nothing about this
// function; the fixtures do that.
//
// declaredRoles is a set: order and repetition do not matter.
func (r *Registry) ValidatePack(entries map[string]string, declaredRoles []string) error {
	keys := slices.Sorted(maps.Keys(entries))

	for _, key := range keys {
		if _, ok := r.slots[SlotKey(key)]; !ok {
			return fmt.Errorf("%w: %q (registered: %v)", ErrUnknownSlot, key, r.Slots())
		}
	}

	known := r.Roles()
	declared := make(map[Role]bool, len(declaredRoles))
	for _, role := range slices.Sorted(slices.Values(declaredRoles)) {
		if !slices.Contains(known, Role(role)) {
			return fmt.Errorf("%w: %q (known: %v)", ErrUnknownRole, role, known)
		}
		declared[Role(role)] = true
	}

	for _, key := range r.Slots() {
		if _, supplied := entries[string(key)]; supplied {
			continue
		}
		for _, role := range r.slots[key].Roles {
			if declared[role] {
				return fmt.Errorf("%w: role %q requires slot %q", ErrMissingSlot, role, key)
			}
		}
	}

	for _, key := range keys {
		slot := r.slots[SlotKey(key)]
		tree, err := parseEntry(SlotKey(key), slot, entries[key])
		if err != nil {
			return err
		}
		// The render half of the contract. The walk already proved every
		// reference is declared; this proves the running renderer executes
		// the entry, over a value for each variable the slot supplies. It
		// takes one path through the entry's conditionals, which is why the
		// dialect is restricted to constructs chosen not to fail on strings
		// (see template.go).
		values := make(map[string]string, len(slot.Variables))
		for _, variable := range slot.Variables {
			values[string(variable)] = string(variable)
		}
		if _, err := execute(SlotKey(key), tree, values); err != nil {
			return err
		}
	}
	return nil
}

// Digest is the pack-jcs-sha256-v1 digest of a pack's entries: bare lowercase
// hex, the scheme being a separate field wherever the pair is stored (design
// D4).
//
// The projection is the slot-key to entry-text object and nothing else -- no
// name, no declared range, no roles, no organization -- so identical content
// has one identity wherever it is installed and correcting an installation's
// metadata does not mint a new pack (ADR 0031 section 1). The signature is
// how that is kept: there is no parameter a name could arrive through.
//
// JCS canonicalises the container; the text is hashed as written. That is
// only true of text JSON can carry without substitution, which ValidatePack
// requires and this function does not re-check.
func Digest(entries map[string]string) (string, error) {
	// A nil map marshals as null, a different document from {} with a
	// different digest, and "no entries" must have exactly one identity.
	if entries == nil {
		entries = map[string]string{}
	}
	digest, err := canonical.Digest(entries)
	if err != nil {
		return "", fmt.Errorf("digest prompt pack entries: %w", err)
	}
	return digest, nil
}
