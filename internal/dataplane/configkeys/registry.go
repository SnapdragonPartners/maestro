// Package configkeys is the code-resident registry of configuration keys
// (item 7 design, D1).
//
// A configuration record is a registered key, a scope on the
// organization/product/repository lineage, and a value. This package owns
// the first of those three: which keys exist, what their values must look
// like, and where they may be set. The seam consults it before every
// configuration write; nothing else decides these things.
//
// # Why a registry rather than a caller-declared type
//
// The alternative is a writer declaring the type of the value it is about
// to write, which is not a type system but a convention: every value
// validates against its own claim, so nothing is ever wrong. Two things go
// through that hole. A value no reader can use lands successfully and fails
// much later somewhere else. And — the one that matters — nothing stops a
// plaintext credential being written into an unencrypted family whose whole
// distinction from the vault is that it holds no secrets.
//
// So an unregistered key is REFUSED. That refusal is what makes this
// registry governing rather than advisory; letting unknown keys through
// unvalidated would reintroduce the same hole one level down.
//
// # Vocabulary growth
//
// This package deliberately ships no seed vocabulary, the same rule item 3
// applied to tables and item 4 applied to artifact types: a key is
// registered by the item that first writes it. Phase 2 has no consumer, so
// Phase 2 registers nothing and the tests register their own fixtures. A
// registered key with no writer is a guess about a future caller, and the
// registry's whole value is that an unregistered key cannot be written at
// all.
package configkeys

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
)

// Key is the canonical dotted name of a configuration key. It is the only
// one of a record's three parts a caller supplies from outside this
// package's vocabulary.
type Key string

// Scope names a level of the organization/product/repository lineage.
//
// This is ADR 0018's ownership chain, NOT the artifact scope arc in
// store.ScopeType — that one runs organization/product/feature/epic/story.
// The two are different chains that happen to share their first two names,
// so they are deliberately different types: a Scope and a store.ScopeType
// are not interchangeable, and making them one type would let an epic-scoped
// value reach a table whose check constraint has no such column.
type Scope string

// The lineage levels, matching the scope_type check constraint on
// configuration_records.
const (
	ScopeOrganization Scope = "organization"
	ScopeProduct      Scope = "product"
	ScopeRepository   Scope = "repository"
)

// Sentinel errors. Each is distinguished because a caller acts differently
// on it: a typo, a key that belongs in another store, a key set at the
// wrong level, and a bad value are four different things to fix.
var (
	// ErrUnknownKey reports a key absent from the registry.
	ErrUnknownKey = errors.New("configuration key is not registered")

	// ErrSensitiveKey reports a credential-shaped key, which does not
	// belong in the unencrypted configuration family at all.
	ErrSensitiveKey = errors.New("configuration key is credential-shaped and belongs in the secrets vault")

	// ErrScopeNotPermitted reports a key set at a lineage level its
	// registration does not admit.
	ErrScopeNotPermitted = errors.New("configuration key may not be set at this scope")

	// ErrInvalidValue reports a value that failed its registered schema.
	ErrInvalidValue = errors.New("configuration value failed its registered schema")
)

// Validator checks that a value is a valid instance of one key's schema.
//
// The value is the JSON encoding destined for the record's jsonb column,
// so a validator sees exactly the bytes that would be stored.
//
// This is deliberately NOT registry.Validator, despite the identical shape.
// That one checks artifact payloads against ADR 0028 schemas; this one
// checks configuration values. Sharing the type would couple two registries
// that have no reason to change together, to save an interface with one
// method.
type Validator interface {
	Validate(value []byte) error
}

// ValidatorFunc adapts a function to Validator.
type ValidatorFunc func(value []byte) error

// Validate implements Validator.
func (f ValidatorFunc) Validate(value []byte) error { return f(value) }

// keyPattern is the canonical form: lowercase dot-separated segments, each
// starting with a letter and continuing in letters, digits, or hyphens.
//
// Canonicalising at registration exists because the key is an identity —
// part of the unique constraint that makes most-specific-wins resolution
// well defined. Postgres text comparison is case-sensitive, so `Forge.Token`
// and `forge.token` would be two rows that every human reader would call one
// key, and a resolution returning either would look intermittently wrong
// rather than wrong. One spelling is admitted so there is nothing to
// reconcile.
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)*$`)

// Entry is one key's registration.
type Entry struct {
	// Schema validates a value BEFORE it is written, so an invalid value
	// never lands. Required for a writable key; refused for a sensitive
	// one, which has no writable path for it to guard.
	Schema Validator

	// PermittedScopes are the lineage levels at which this key may be set.
	// Some keys are organization-wide by nature, and a key that may be
	// overridden per repository is a different statement from one that may
	// not. Required for a writable key; refused for a sensitive one.
	PermittedScopes []Scope

	// Sensitive marks a key as credential-shaped. Such a key is refused by
	// this family and the caller is directed to the vault.
	//
	// Registering a key only to refuse it is the point: without the
	// registration the caller gets "not registered", which reads as a typo
	// and invites them to add it here. With it, they are told where the
	// value actually goes.
	Sensitive bool
}

// Registry is an immutable set of registrations.
//
// Immutable because it is consulted on every write to decide what a key IS.
// A registry mutable at run time would let two writes of the same key
// disagree about its schema or its permitted scopes, and the stored rows
// would carry no record of which registration admitted them.
type Registry struct {
	entries map[Key]Entry
}

// New validates and freezes a set of registrations.
//
// Validation happens at construction so a malformed registration is a
// startup failure rather than a failure on the first write of a rarely used
// key.
func New(entries map[Key]Entry) (*Registry, error) {
	frozen := make(map[Key]Entry, len(entries))

	// Sorted so that a set with two bad registrations reports the same one
	// on every run; map order would make the failure depend on the build.
	for _, key := range slices.Sorted(maps.Keys(entries)) {
		entry := entries[key]
		if err := checkEntry(key, entry); err != nil {
			return nil, err
		}
		frozen[key] = Entry{
			Schema:          entry.Schema,
			PermittedScopes: slices.Clone(entry.PermittedScopes),
			Sensitive:       entry.Sensitive,
		}
	}
	return &Registry{entries: frozen}, nil
}

// MustNew is New for package-level registrations, where a malformed entry
// is a build-time mistake and there is no caller to return an error to.
func MustNew(entries map[Key]Entry) *Registry {
	built, err := New(entries)
	if err != nil {
		panic(err)
	}
	return built
}

// checkEntry validates one registration. Split out of New so that adding a
// rule does not make the loop that applies them harder to read than the
// rules themselves.
func checkEntry(key Key, entry Entry) error {
	if key == "" {
		return errors.New("configkeys: a key is empty")
	}
	if !keyPattern.MatchString(string(key)) {
		return fmt.Errorf("configkeys: key %q is not a canonical dotted name "+
			"(lowercase segments of letters, digits and hyphens, each starting with a letter)", key)
	}

	// A sensitive key is refused before its schema or scopes are ever
	// consulted, so carrying either is a contradiction rather than
	// harmless extra detail. Refusing it here keeps a reader from
	// concluding the key is writable under some condition the refusal
	// does not cover.
	if entry.Sensitive {
		if entry.Schema != nil {
			return fmt.Errorf("configkeys: key %q is sensitive and also declares a schema; "+
				"a sensitive key has no writable path for a schema to guard", key)
		}
		if len(entry.PermittedScopes) > 0 {
			return fmt.Errorf("configkeys: key %q is sensitive and also declares permitted scopes; "+
				"a sensitive key may not be set at any scope", key)
		}
		return nil
	}

	if entry.Schema == nil {
		return fmt.Errorf("configkeys: key %q has no schema, so any value at all would be accepted", key)
	}
	if len(entry.PermittedScopes) == 0 {
		return fmt.Errorf("configkeys: key %q permits no scopes, so it could never be set anywhere", key)
	}
	seen := make(map[Scope]bool, len(entry.PermittedScopes))
	for _, scope := range entry.PermittedScopes {
		switch scope {
		case ScopeOrganization, ScopeProduct, ScopeRepository:
		default:
			return fmt.Errorf("configkeys: key %q permits unknown scope %q, want one of %q, %q or %q",
				key, scope, ScopeOrganization, ScopeProduct, ScopeRepository)
		}
		if seen[scope] {
			return fmt.Errorf("configkeys: key %q lists scope %q twice", key, scope)
		}
		seen[scope] = true
	}

	return nil
}

// Registration is what a lookup yields: a key's metadata, carrying no
// reference to the registry's internals.
//
// It is a separate type from Entry, the registration INPUT, because
// returning an Entry would hand the caller the live PermittedScopes slice
// and let it rewrite the registration after construction, defeating the
// freeze.
type Registration struct {
	// PermittedScopes is freshly allocated per call, in registration
	// order. Empty for a sensitive key, which may be set nowhere.
	PermittedScopes []Scope

	// Sensitive marks a credential-shaped key, refused by this family.
	Sensitive bool
}

// Lookup returns the registration for a key.
//
// It reports what a key IS. It does not decide whether a particular write
// is allowed — that is ValidateWrite, which applies the rules in an order
// callers must not have to reproduce.
func (r *Registry) Lookup(key Key) (Registration, error) {
	entry, ok := r.entries[key]
	if !ok {
		return Registration{}, fmt.Errorf("%w: %q (registered: %v)", ErrUnknownKey, key, r.Keys())
	}
	return Registration{
		PermittedScopes: slices.Clone(entry.PermittedScopes),
		Sensitive:       entry.Sensitive,
	}, nil
}

// ValidateWrite decides whether one value may be written to one key at one
// scope, and is the single governing call the seam makes before a create or
// an update.
//
// It is one call rather than a Lookup the caller interprets because the
// checks are ordered, and a caller reconstructing that order is a second,
// untested copy of the policy — the same reason resolution is one query
// rather than three the caller reconciles.
//
// The order is deliberate, strongest statement first:
//
//  1. Unregistered — there is no policy to apply, so nothing else can be
//     said about the write.
//  2. Sensitive — the key does not belong in this family at any scope with
//     any value, so reporting a scope or schema problem would send the
//     caller to fix something that would still leave the credential
//     heading for an unencrypted table.
//  3. Scope — the value cannot be judged useful at a level the key may not
//     be set at.
//  4. Schema — the value itself, last, once the key and place are settled.
func (r *Registry) ValidateWrite(key Key, scope Scope, value []byte) error {
	entry, ok := r.entries[key]
	if !ok {
		return fmt.Errorf("%w: %q (registered: %v)", ErrUnknownKey, key, r.Keys())
	}
	if entry.Sensitive {
		return fmt.Errorf("%w: %q", ErrSensitiveKey, key)
	}
	if !slices.Contains(entry.PermittedScopes, scope) {
		return fmt.Errorf("%w: %q at %q (permitted: %v)",
			ErrScopeNotPermitted, key, scope, entry.PermittedScopes)
	}
	if err := entry.Schema.Validate(value); err != nil {
		return fmt.Errorf("%w: %q: %w", ErrInvalidValue, key, err)
	}
	return nil
}

// Keys returns every registered key in a stable order, for error messages
// and for tests that assert the vocabulary.
func (r *Registry) Keys() []Key {
	return slices.Sorted(maps.Keys(r.entries))
}
