package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/configkeys"
)

// The configuration family (item 7 design, D1).
//
// A configuration record is a registered key, a scope on the
// organization/product/repository lineage, and a JSON value. It is
// unencrypted by construction: anything credential-shaped is refused by the
// key registry and belongs in the vault instead.

// ErrConfigurationConflict reports that a conditional update or delete
// affected no rows because the record's version had moved.
//
// Distinguished from ErrNotFound, which the seam establishes by reading the
// row: both make a conditional statement affect zero rows, and a rowcount
// carries no reason. A caller re-reads on a conflict and gives up on a
// missing row, so collapsing the two would make the right response
// unknowable.
var ErrConfigurationConflict = errors.New("configuration record was modified concurrently")

// ConfigScope is a level of the organization/product/repository lineage
// together with the entity at that level.
//
// Deliberately not the artifact Scope: that one runs
// organization/product/feature/epic/story and names a different chain
// (ADR 0018 ownership versus the work hierarchy). The two share their first
// two names and nothing else.
type ConfigScope struct {
	// Type is the lineage level. It also decides which column the ID lands
	// in, which is why the pair travels together rather than as two
	// arguments a caller could mismatch.
	Type configkeys.Scope

	// ID is the organization, product, or repository at that level.
	ID uuid.UUID
}

// ConfigurationRecord is one stored value.
type ConfigurationRecord struct {
	CreatedAt time.Time
	UpdatedAt time.Time

	// Value is the stored JSON as the column returns it, which is NOT
	// byte-identical to what was written: the column is jsonb, so Postgres
	// reparses the value and renders it in its own normal form — whitespace
	// collapsed, duplicate keys dropped, object key order not preserved.
	//
	// Compare it by decoding, never by comparing bytes against what you
	// wrote. A byte comparison is a comparison against a format the column
	// never promised to keep.
	Value json.RawMessage

	Key configkeys.Key

	// Scope is the level this record was set at. Resolution returns it so a
	// caller can say WHY a value is what it is — the question a settings
	// screen exists to answer, and one a bare value cannot support.
	Scope ConfigScope

	ID             uuid.UUID
	OrganizationID uuid.UUID

	// Version is the optimistic-concurrency token. Every update and delete
	// names the version it read (ADR 0027).
	Version int
}

// CreateConfigurationRecordInput is the public creation input.
//
// It carries no version: a new record starts at 1, and letting a caller
// choose would let two creations disagree about what "first" means.
type CreateConfigurationRecordInput struct {
	// Value is validated against the key's registered schema BEFORE the
	// write, so an invalid value never lands.
	Value json.RawMessage

	Key configkeys.Key

	Scope ConfigScope

	OrganizationID uuid.UUID
}

// ConfigurationReader is the configuration read surface.
type ConfigurationReader interface {
	// ResolveConfiguration returns the most specific record that applies to
	// a repository: repository, else its primary Product, else the
	// organization, else ErrNotFound.
	//
	// It resolves; it does not merge. A merged value is a function of what
	// was set where, which nothing can display honestly and which turns
	// "why is this value what it is?" into an investigation.
	//
	// One query, not three the caller reconciles: three reads can disagree
	// under concurrent writes, and a caller that reconciles them is a
	// second, untested copy of the precedence rule.
	ResolveConfiguration(ctx context.Context, organizationID, repositoryID uuid.UUID, key configkeys.Key) (*ConfigurationRecord, error)

	// GetConfigurationRecord reads one record by identity.
	GetConfigurationRecord(ctx context.Context, organizationID, recordID uuid.UUID) (*ConfigurationRecord, error)
}

// ConfigurationWriter is the configuration write surface.
//
// Every write consults the key registry first. That is what makes the
// registry governing rather than advisory, and it is the only thing
// standing between a plaintext credential and an unencrypted table.
type ConfigurationWriter interface {
	// CreateConfigurationRecord validates the key, the scope and the value,
	// then writes. An unregistered key, a credential-shaped key, a scope
	// the key does not permit, or a value failing its schema are all
	// refused before the statement runs.
	CreateConfigurationRecord(ctx context.Context, input CreateConfigurationRecordInput) (*ConfigurationRecord, error)

	// UpdateConfigurationRecord replaces a value, conditional on the
	// version the caller read. It returns ErrConfigurationConflict when
	// somebody else moved first and ErrNotFound when the record is gone.
	UpdateConfigurationRecord(ctx context.Context, organizationID, recordID uuid.UUID, expectedVersion int, value json.RawMessage) (*ConfigurationRecord, error)

	// DeleteConfigurationRecord removes an override, restoring inheritance
	// from the level above.
	//
	// Deletion is a first-class operation, not an omission: a record at a
	// specific level IS an override, and without a delete one set once
	// could only ever be overwritten with a value that matches its parent
	// today and diverges silently tomorrow. That is worse than no override,
	// because it looks intentional.
	//
	// Conditional on the version for the same reason updates are.
	DeleteConfigurationRecord(ctx context.Context, organizationID, recordID uuid.UUID, expectedVersion int) error
}
