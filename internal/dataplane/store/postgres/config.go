package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// The configuration family (item 7 design, D1).
//
// Every write consults the key registry BEFORE the statement runs, which is
// what makes the registry governing rather than advisory. The database
// enforces shape and identity; the registry enforces which keys exist, what
// their values must look like, and where they may be set.

// configScopeArc is the configuration family's exclusive arc: exactly one
// member is non-nil.
//
// Separate from scopeArc, which spreads the ARTIFACT scope across
// organization/product/feature/epic/story. This one is ADR 0018's ownership
// lineage and terminates at a repository. Merging them would produce a
// struct with eight members of which one is set, and every reader would
// have to know which three belong to which family.
type configScopeArc struct {
	organizationID *uuid.UUID
	productID      *uuid.UUID
	repositoryID   *uuid.UUID
}

// configScopeColumns spreads a lineage scope across that arc.
//
// One place decides which column a scope id lands in. Spread across the
// call sites it would be several chances to write a repository id into the
// product column — a row the check constraint rejects at best, and at worst
// one that resolves at a level nobody set it at.
//
// An unknown scope type is an error rather than all-nulls, which the schema
// would reject with a constraint violation naming num_nonnulls instead of
// the actual mistake.
func configScopeColumns(scope store.ConfigScope) (configScopeArc, error) {
	id := scope.ID
	switch scope.Type {
	case configkeys.ScopeOrganization:
		return configScopeArc{organizationID: &id}, nil
	case configkeys.ScopeProduct:
		return configScopeArc{productID: &id}, nil
	case configkeys.ScopeRepository:
		return configScopeArc{repositoryID: &id}, nil
	default:
		return configScopeArc{}, fmt.Errorf("%w: unknown configuration scope %q",
			store.ErrInvariant, scope.Type)
	}
}

// configurationRecord converts a stored row to the seam's type.
//
// The scope is reassembled from scope_type and the generated scope_id
// column rather than from whichever of the three arc columns is populated:
// the database already decided which one that is, and reading it back from
// the arc would be a second implementation of the same rule.
func configurationRecord(row *gen.ConfigurationRecord) (*store.ConfigurationRecord, error) {
	scopeType := configkeys.Scope(row.ScopeType)
	switch scopeType {
	case configkeys.ScopeOrganization, configkeys.ScopeProduct, configkeys.ScopeRepository:
	default:
		// A row whose scope_type the check constraint admits but this build
		// does not know is a schema that moved ahead of this code. Reported
		// rather than defaulted: silently treating it as an organization
		// would resolve it at the wrong level.
		return nil, fmt.Errorf("%w: configuration record %s has scope type %q",
			store.ErrInvariant, fromUUID(row.ConfigurationRecordID), row.ScopeType)
	}

	return &store.ConfigurationRecord{
		ID:             fromUUID(row.ConfigurationRecordID),
		OrganizationID: fromUUID(row.OrganizationID),
		Key:            configkeys.Key(row.Key),
		Scope:          store.ConfigScope{Type: scopeType, ID: fromUUID(row.ScopeID)},
		Value:          json.RawMessage(row.Value),
		Version:        int(row.Version),
		CreatedAt:      fromTimestamptz(row.CreatedAt),
		UpdatedAt:      fromTimestamptz(row.UpdatedAt),
	}, nil
}

// ResolveConfiguration returns the most specific record applying to a
// repository.
func (t *tx) ResolveConfiguration(
	ctx context.Context, organizationID, repositoryID uuid.UUID, key configkeys.Key,
) (*store.ConfigurationRecord, error) {
	row, err := t.queries.ResolveConfigurationForRepository(ctx, gen.ResolveConfigurationForRepositoryParams{
		RepositoryID:   toUUID(repositoryID),
		OrganizationID: toUUID(organizationID),
		Key:            string(key),
	})
	if err != nil {
		return nil, notFound(err, "configuration for repository", repositoryID)
	}
	return configurationRecord(&row)
}

// GetConfigurationRecord reads one record by identity.
func (t *tx) GetConfigurationRecord(
	ctx context.Context, organizationID, recordID uuid.UUID,
) (*store.ConfigurationRecord, error) {
	row, err := t.queries.GetConfigurationRecord(ctx, gen.GetConfigurationRecordParams{
		OrganizationID:        toUUID(organizationID),
		ConfigurationRecordID: toUUID(recordID),
	})
	if err != nil {
		return nil, notFound(err, "configuration record", recordID)
	}
	return configurationRecord(&row)
}

// CreateConfigurationRecord validates against the registry, then writes.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) CreateConfigurationRecord(
	ctx context.Context, input store.CreateConfigurationRecordInput,
) (*store.ConfigurationRecord, error) {
	// Before anything touches the database. A value that fails here must
	// leave no row behind, and the cheapest way to guarantee that is to
	// never issue the statement.
	if err := t.keys.ValidateWrite(input.Key, input.Scope.Type, input.Value); err != nil {
		return nil, fmt.Errorf("refuse configuration write in organization %s: %w",
			input.OrganizationID, err)
	}

	arc, err := configScopeColumns(input.Scope)
	if err != nil {
		return nil, err
	}

	row, err := t.queries.CreateConfigurationRecord(ctx, gen.CreateConfigurationRecordParams{
		ConfigurationRecordID: toUUID(uuid.New()),
		OrganizationID:        toUUID(input.OrganizationID),
		Key:                   string(input.Key),
		ScopeType:             string(input.Scope.Type),
		ScopeOrganizationID:   toNullUUID(arc.organizationID),
		ScopeProductID:        toNullUUID(arc.productID),
		ScopeRepositoryID:     toNullUUID(arc.repositoryID),
		Value:                 input.Value,
	})
	if err != nil {
		return nil, fmt.Errorf("create configuration record %q: %w", input.Key, err)
	}
	return configurationRecord(&row)
}

// UpdateConfigurationRecord replaces a value under its expected version.
func (t *tx) UpdateConfigurationRecord(
	ctx context.Context, organizationID, recordID uuid.UUID, expectedVersion int, value json.RawMessage,
) (*store.ConfigurationRecord, error) {
	version, err := toInt32(expectedVersion, "expected configuration version")
	if err != nil {
		return nil, err
	}

	// The key is read from the STORED row, not taken from the caller. An
	// update names a record, not a key, so a caller-supplied key would be a
	// second claim about what this row is — and validating the new value
	// against that claim rather than against the row's real key is how a
	// value passes the wrong schema.
	current, err := t.GetConfigurationRecord(ctx, organizationID, recordID)
	if err != nil {
		return nil, err
	}
	if validateErr := t.keys.ValidateWrite(current.Key, current.Scope.Type, value); validateErr != nil {
		return nil, fmt.Errorf("refuse configuration update of record %s: %w", recordID, validateErr)
	}

	row, err := t.queries.UpdateConfigurationRecord(ctx, gen.UpdateConfigurationRecordParams{
		OrganizationID:        toUUID(organizationID),
		ConfigurationRecordID: toUUID(recordID),
		ExpectedVersion:       version,
		Value:                 value,
	})
	if err != nil {
		if errors.Is(notFound(err, "configuration record", recordID), store.ErrNotFound) {
			// The row was read a moment ago, so its absence now is not a
			// missing record but a version that moved -- including the
			// case where somebody deleted it. Both are "your write did not
			// apply; re-read", which is what the conflict says.
			return nil, fmt.Errorf("%w: record %s at version %d",
				store.ErrConfigurationConflict, recordID, expectedVersion)
		}
		return nil, fmt.Errorf("update configuration record %s: %w", recordID, err)
	}
	return configurationRecord(&row)
}

// DeleteConfigurationRecord removes an override under its expected version.
func (t *tx) DeleteConfigurationRecord(
	ctx context.Context, organizationID, recordID uuid.UUID, expectedVersion int,
) error {
	version, err := toInt32(expectedVersion, "expected configuration version")
	if err != nil {
		return err
	}

	affected, err := t.queries.DeleteConfigurationRecord(ctx, gen.DeleteConfigurationRecordParams{
		OrganizationID:        toUUID(organizationID),
		ConfigurationRecordID: toUUID(recordID),
		ExpectedVersion:       version,
	})
	if err != nil {
		return fmt.Errorf("delete configuration record %s: %w", recordID, err)
	}
	if affected == 0 {
		// A rowcount carries no reason, so the reason is established by
		// reading the row rather than guessed from the zero.
		if _, getErr := t.GetConfigurationRecord(ctx, organizationID, recordID); getErr != nil {
			return getErr
		}
		return fmt.Errorf("%w: record %s at version %d",
			store.ErrConfigurationConflict, recordID, expectedVersion)
	}
	return nil
}
