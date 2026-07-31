// Package registry is ADR 0028's code-resident artifact type registry.
//
// It maps an artifact type to its category, its current schema version, and
// a validator per readable version. The seam consults it on every artifact
// write and read; nothing else decides these values.
//
// The type is the only one of the four a caller supplies. Category comes
// from here because a caller choosing its own could write a Management
// artifact into the Audit family — two tables with opposite retention
// postures. Version comes from here because a caller choosing its own could
// claim a version whose validator does not match the payload it wrote.
//
// # Vocabulary growth
//
// ADR 0021 governs artifact types the way ADR 0017 governs doc types:
// prefer reuse, add a type only for a repeated class. This package
// deliberately ships no seed vocabulary. A type is registered by the item
// that first writes it, which is the same rule item 3 applied to tables —
// a registered type with no consumer is a guess about a future caller, and
// the registry's whole value is that an unregistered type cannot be
// written at all.
package registry

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/nilcheck"
)

// Type is an artifact type drawn from ADR 0021's governed vocabulary.
type Type string

// Category selects the storage family. The two have opposite retention
// postures, which is why it is not a caller's choice.
type Category string

const (
	// CategoryManagement is durable, reviewable work product.
	CategoryManagement Category = "management"
	// CategoryAudit is high-volume evidence, truncatable unless pinned.
	CategoryAudit Category = "audit"
)

// Sentinel errors, distinguished because callers act differently on each:
// an unknown type is a programming error, while an out-of-range version is
// a real artifact this build is too old or too new to read.
var (
	// ErrUnknownType reports a type absent from the registry.
	ErrUnknownType = errors.New("artifact type is not registered")
	// ErrVersionOutOfRange reports a schema version with no validator.
	ErrVersionOutOfRange = errors.New("schema version is outside the readable range")
)

// Validator checks that a payload is a valid instance of one version of one
// artifact type's schema.
//
// Implementations must be safe for concurrent use and must not retain or
// modify the payload they are given. New freezes the registrations, but it
// cannot freeze what is behind this interface: one Validator is shared by
// every artifact of its type and version, and the seam calls it from
// whatever goroutine is serving the request.
type Validator interface {
	Validate(payload []byte) error
}

// ValidatorFunc adapts a function to Validator.
type ValidatorFunc func(payload []byte) error

// Validate implements Validator.
func (f ValidatorFunc) Validate(payload []byte) error { return f(payload) }

// Reference is one piece of evidence a payload names.
//
// Exactly one target, mirroring the exclusive arc retention pins already
// use: an artifact cites an Audit record or an attachment, and a reference
// naming both, or neither, describes nothing the schema can pin.
type Reference struct {
	AuditArtifactID *uuid.UUID
	AttachmentID    *uuid.UUID
}

// Extractor reports the evidence a payload references.
//
// It exists because acceptance has to know what evidence was REVIEWED, and
// the pins cannot answer that: a set derived from the pins would be
// checked against itself. ADR 0028 binds the review digest over the whole
// reviewable envelope including the payload, so a set derived from the
// payload is a set the reviewer saw.
type Extractor interface {
	References(payload []byte) ([]Reference, error)
}

// ExtractorFunc adapts a function to Extractor.
//
// Implementations carry the same obligations as Validator: safe for
// concurrent use, and no retention or modification of the payload.
type ExtractorFunc func(payload []byte) ([]Reference, error)

// References implements Extractor.
func (f ExtractorFunc) References(payload []byte) ([]Reference, error) { return f(payload) }

// Entry is one type's registration.
type Entry struct {
	// Validators is keyed by schema version. Its key set *is* the readable
	// range: a version is readable exactly when there is a validator that
	// can check it. Keeping the range implicit in the keys means the two
	// cannot drift apart, which a separate min/max pair would allow.
	//
	// ADR 0028's evolution rule is additive-only, so old versions keep
	// their validators here rather than being dropped when a new one lands.
	Validators map[int]Validator

	// Extractors is keyed by schema version, like Validators, and is
	// OPTIONAL. Its absence is a statement, not an omission: a type with no
	// extractor carries no evidence, and acceptance therefore requires
	// exactly zero pins for it. Treating a missing extractor as "cannot
	// tell" would let an unreviewed retention claim through on every type
	// nobody had got round to registering.
	//
	// Versioned with the type because a v2 payload may name its evidence
	// differently from a v1, and item 4's rule that a v1 artifact stays v1
	// for life applies unchanged.
	Extractors map[int]Extractor

	// Category is the storage family for every artifact of this type.
	Category Category

	// CurrentVersion is the schema version new artifacts are written at.
	CurrentVersion int
}

// Registry is an immutable set of registrations.
//
// It is immutable because it is consulted on every write to decide what an
// artifact *is*. A registry that could be mutated at run time would let two
// writes of the same type disagree about category or version, and the
// stored rows would carry no record of which registration produced them.
type Registry struct {
	entries map[Type]Entry
}

// New validates and freezes a set of registrations.
//
// Validation happens here, at construction, so a malformed registration is
// a startup failure rather than a failure on the first write of a rarely
// used type.
func New(entries map[Type]Entry) (*Registry, error) {
	frozen := make(map[Type]Entry, len(entries))

	for _, artifactType := range slices.Sorted(maps.Keys(entries)) {
		entry := entries[artifactType]
		if err := checkEntry(artifactType, entry); err != nil {
			return nil, err
		}
		frozen[artifactType] = Entry{
			Category:       entry.Category,
			CurrentVersion: entry.CurrentVersion,
			Validators:     maps.Clone(entry.Validators),
			Extractors:     maps.Clone(entry.Extractors),
		}
	}
	return &Registry{entries: frozen}, nil
}

// checkEntry validates one registration. Split out of New so that adding a
// rule does not make the loop that applies them harder to read than the
// rules themselves.
func checkEntry(artifactType Type, entry Entry) error {
	if artifactType == "" {
		return errors.New("registry: an artifact type is empty")
	}
	switch entry.Category {
	case CategoryManagement, CategoryAudit:
	default:
		return fmt.Errorf("registry: type %q has category %q, want %q or %q",
			artifactType, entry.Category, CategoryManagement, CategoryAudit)
	}
	// Bounded above as well as below: schema versions are stored in an
	// int4 column, and a registration beyond that range would narrow
	// silently at the write rather than failing here.
	if entry.CurrentVersion < 1 || entry.CurrentVersion > math.MaxInt32 {
		return fmt.Errorf("registry: type %q has current version %d, want 1..%d",
			artifactType, entry.CurrentVersion, math.MaxInt32)
	}
	if len(entry.Validators) == 0 {
		return fmt.Errorf("registry: type %q has no validators, so nothing of that type could be read",
			artifactType)
	}
	for version, validator := range entry.Validators {
		if version < 1 || version > math.MaxInt32 {
			return fmt.Errorf("registry: type %q has a validator for version %d, want 1..%d",
				artifactType, version, math.MaxInt32)
		}
		// nilcheck.IsNil, not `validator == nil`: an interface holding a
		// typed nil -- ValidatorFunc(nil), or a nil pointer whose type has
		// the method -- is not equal to nil, so the plain comparison admits
		// it and the panic lands on the first artifact written at that
		// version instead of here at construction.
		if nilcheck.IsNil(validator) {
			return fmt.Errorf("registry: type %q has a nil validator for version %d "+
				"(a nil value, or an interface holding a typed nil)", artifactType, version)
		}
	}
	if err := checkExtractors(artifactType, entry); err != nil {
		return err
	}
	// The current version must be readable. Without this a type could
	// be written at a version nothing can validate, which passes every
	// write and fails every read.
	if _, ok := entry.Validators[entry.CurrentVersion]; !ok {
		return fmt.Errorf("registry: type %q writes at version %d but has no validator for it (has %v)",
			artifactType, entry.CurrentVersion, readableVersions(entry))
	}

	return nil
}

// checkExtractors validates the optional reference extractors, which have
// two rules of their own: every one must be for a readable version, and a
// type either registers them for all its readable versions or for none.
func checkExtractors(artifactType Type, entry Entry) error {
	for version, extractor := range entry.Extractors {
		if nilcheck.IsNil(extractor) {
			return fmt.Errorf("registry: type %q has a nil extractor for version %d "+
				"(a nil value, or an interface holding a typed nil)", artifactType, version)
		}
		// An extractor for a version nothing can read is one that will
		// never run, and its absence at the versions that ARE readable
		// would be read as "this type carries no evidence".
		if _, readable := entry.Validators[version]; !readable {
			return fmt.Errorf("registry: type %q has an extractor for version %d, which is not "+
				"readable (readable: %v)", artifactType, version, readableVersions(entry))
		}
	}
	// Partial extraction is refused. A type that extracts references at
	// one readable version and not another would accept an artifact
	// with zero pins at the second version -- silently, and only for
	// artifacts written before the extractor was added.
	if len(entry.Extractors) > 0 && len(entry.Extractors) != len(entry.Validators) {
		return fmt.Errorf("registry: type %q registers extractors for %d of its %d readable "+
			"versions; a version without one is a version whose evidence goes unpinned",
			artifactType, len(entry.Extractors), len(entry.Validators))
	}

	return nil
}

// MustNew is New for package-level registrations, where a malformed entry
// is a build-time mistake and there is no caller to return an error to.
func MustNew(entries map[Type]Entry) *Registry {
	built, err := New(entries)
	if err != nil {
		panic(err)
	}
	return built
}

// Registration is what a lookup yields: an artifact type's metadata,
// carrying no reference to the registry's internals.
//
// It is a separate type from Entry, which is the registration INPUT,
// because returning an Entry would hand the caller the live Validators map
// and let it add or replace a validator after construction — defeating the
// freeze entirely. There is no map here to alias, so immutability is a
// property of the type rather than a discipline callers must keep.
type Registration struct {
	// Category is the storage family for every artifact of this type.
	Category Category

	// ReadableVersions is ascending and freshly allocated per call.
	ReadableVersions []int

	// CurrentVersion is the schema version new artifacts are written at.
	CurrentVersion int
}

// Lookup returns the registration for a type.
func (r *Registry) Lookup(artifactType Type) (Registration, error) {
	entry, ok := r.entries[artifactType]
	if !ok {
		return Registration{}, fmt.Errorf("%w: %q (registered: %v)", ErrUnknownType, artifactType, r.Types())
	}
	return Registration{
		ReadableVersions: readableVersions(entry),
		Category:         entry.Category,
		CurrentVersion:   entry.CurrentVersion,
	}, nil
}

// ValidatorFor returns the validator for one version of one type.
//
// Writes pass the current version; reads pass the version stored on the
// row, and amendments pass the version of the original they amend — which
// is why this takes a version rather than always using the current one.
func (r *Registry) ValidatorFor(artifactType Type, version int) (Validator, error) {
	entry, ok := r.entries[artifactType]
	if !ok {
		return nil, fmt.Errorf("%w: %q (registered: %v)", ErrUnknownType, artifactType, r.Types())
	}
	validator, ok := entry.Validators[version]
	if !ok {
		return nil, fmt.Errorf("%w: type %q version %d (readable: %v)",
			ErrVersionOutOfRange, artifactType, version, readableVersions(entry))
	}
	return validator, nil
}

// ExtractorFor returns the reference extractor for a type and version.
//
// A registered type with NO extractor at all is reported as such rather
// than as an error: acceptance treats that as "this type carries no
// evidence" and requires zero pins. A type that has extractors but not for
// this version cannot happen -- New refuses partial registration -- so it
// is an invariant failure rather than a caller's mistake.
func (r *Registry) ExtractorFor(artifactType Type, version int) (Extractor, bool, error) {
	entry, ok := r.entries[artifactType]
	if !ok {
		return nil, false, fmt.Errorf("%w: %q (registered: %v)", ErrUnknownType, artifactType, r.Types())
	}
	if len(entry.Extractors) == 0 {
		return nil, false, nil
	}
	extractor, ok := entry.Extractors[version]
	if !ok {
		return nil, false, fmt.Errorf("registry: type %q has extractors but none for version %d (readable: %v)",
			artifactType, version, readableVersions(entry))
	}
	return extractor, true, nil
}

// Types returns every registered type in a stable order, for error
// messages and for tests that assert the vocabulary.
func (r *Registry) Types() []Type {
	return slices.Sorted(maps.Keys(r.entries))
}

// readableVersions lists an entry's readable versions in ascending order.
func readableVersions(entry Entry) []int {
	versions := slices.Collect(maps.Keys(entry.Validators))
	sort.Ints(versions)
	return versions
}
