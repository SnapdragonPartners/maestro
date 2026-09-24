package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// Organization provisioning and the import-and-select verb (ADR 0031
// section 6; Phase 3 item 4 design, D9 and D11).
//
// Both take the built-in pack the composition root loaded and install it
// through InstallPromptPack, so the gate, the digest and the installer rule
// are the same ones every pack write crosses. The binary version recorded
// on the installation is the composition's harness version: the seam's
// install path takes it from the composition and never from a caller
// (design D3), and the loaded pack carries none.

// builtinInstallInput is the one place a built-in becomes an install input.
//
//nolint:gocritic // hugeParam: by value, the seam's convention for inputs
func (t *tx) builtinInstallInput(organizationID uuid.UUID, builtin store.BuiltinPromptPack) store.InstallPromptPackInput {
	return store.InstallPromptPackInput{
		Entries:           builtin.Entries,
		DisplayName:       builtin.DisplayName,
		MinMaestroVersion: builtin.MinMaestroVersion,
		MaxMaestroVersion: builtin.MaxMaestroVersion,
		DeclaredRoles:     builtin.DeclaredRoles,
		Installer: store.PromptPackInstaller{
			Kind:                  store.PromptPackInstalledByBuiltin,
			BuiltinMaestroVersion: t.harness.String(),
		},
		OrganizationID: organizationID,
	}
}

// organizationSelector reads the organization-scoped prompt.pack record.
func (t *tx) organizationSelector(ctx context.Context, organizationID uuid.UUID) (*store.ConfigurationRecord, error) {
	row, err := t.queries.GetConfigurationRecordAtScope(ctx, gen.GetConfigurationRecordAtScopeParams{
		OrganizationID: toUUID(organizationID),
		Key:            string(store.PromptPackKey),
		ScopeType:      string(configkeys.ScopeOrganization),
		ScopeID:        toUUID(organizationID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: organization %s; run `dataplanectl provision organization` to seed one",
				store.ErrNoPromptSelector, organizationID)
		}
		return nil, fmt.Errorf("read the %s selector of organization %s: %w", store.PromptPackKey, organizationID, err)
	}
	return configurationRecord(&row)
}

// selectionOf resolves a selector record to the pack it names.
func (t *tx) selectionOf(ctx context.Context, record *store.ConfigurationRecord) (*store.PromptPackSelection, error) {
	selector, err := store.ParsePromptSelector(record.Value)
	if err != nil {
		// The registry admitted the value, so the registry and the parser
		// disagree -- the same invariant dispatch's selectorFor names.
		return nil, fmt.Errorf("%w: configuration record %s holds a %s value the parser refuses: %w",
			store.ErrInvariant, record.ID, store.PromptPackKey, err)
	}
	pack, err := t.packFor(ctx, record.OrganizationID, selector)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, &store.PromptSelectorUnresolved{Selector: *record, Names: selector}
		}
		return nil, err
	}
	return &store.PromptPackSelection{Pack: *pack, Selector: *record}, nil
}

// GetOrganizationPromptPackSelection reads and resolves the organization's
// selector.
func (t *tx) GetOrganizationPromptPackSelection(ctx context.Context, organizationID uuid.UUID) (*store.PromptPackSelection, error) {
	record, err := t.organizationSelector(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	return t.selectionOf(ctx, record)
}

// ProvisionOrganizationPromptPack seeds an organization's selector, all
// three writes or none.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) ProvisionOrganizationPromptPack(ctx context.Context, organizationID uuid.UUID, builtin store.BuiltinPromptPack) (store.Bootstrapped[store.PromptPackSelection], error) {
	var empty store.Bootstrapped[store.PromptPackSelection]

	// The organization lock is what makes "read, then seed" one act. Taken
	// before the read, so a second provisioner waits here and then finds
	// the selector the first one wrote.
	if _, err := t.queries.LockOrganization(ctx, toUUID(organizationID)); err != nil {
		return empty, notFound(err, "organization", organizationID)
	}

	existing, err := t.organizationSelector(ctx, organizationID)
	switch {
	case err == nil:
		// Upgrades move nothing (design D9): the selector stays, whatever
		// it names, and this binary's built-in is NOT imported beside it.
		selection, selectErr := t.selectionOf(ctx, existing)
		if selectErr != nil {
			return empty, selectErr
		}
		return store.Bootstrapped[store.PromptPackSelection]{Record: *selection, Created: false}, nil
	case !errors.Is(err, store.ErrNoPromptSelector):
		return empty, err
	}

	installed, err := t.InstallPromptPack(ctx, t.builtinInstallInput(organizationID, builtin))
	if err != nil {
		return empty, fmt.Errorf("provision organization %s prompt pack: %w", organizationID, err)
	}
	record, err := t.createSelector(ctx, organizationID, installed.Record.Content.ContentID)
	if err != nil {
		return empty, fmt.Errorf("provision organization %s prompt pack: %w", organizationID, err)
	}
	return store.Bootstrapped[store.PromptPackSelection]{
		Record:  store.PromptPackSelection{Pack: installed.Record, Selector: *record},
		Created: true,
	}, nil
}

// createSelector writes the organization-scoped record naming a content
// row, through the configuration family so the key registry judges it.
func (t *tx) createSelector(ctx context.Context, organizationID, contentID uuid.UUID) (*store.ConfigurationRecord, error) {
	value, err := selectorValue(contentID)
	if err != nil {
		return nil, err
	}
	record, err := t.CreateConfigurationRecord(ctx, store.CreateConfigurationRecordInput{
		Value: value, Key: store.PromptPackKey,
		Scope:          store.ConfigScope{Type: configkeys.ScopeOrganization, ID: organizationID},
		OrganizationID: organizationID,
	})
	if err != nil {
		return nil, fmt.Errorf("seed the %s selector: %w", store.PromptPackKey, err)
	}
	return record, nil
}

// selectorValue is the stored form of a selector naming a content row. By
// content id rather than identity: both resolve, and the id is the stable
// handle the row was created under.
func selectorValue(contentID uuid.UUID) (json.RawMessage, error) {
	value, err := json.Marshal(store.PromptSelector{ContentID: &contentID})
	if err != nil {
		return nil, fmt.Errorf("encode the %s selector: %w", store.PromptPackKey, err)
	}
	return value, nil
}

// SelectBuiltinPromptPack imports the running binary's built-in and moves
// the selector to it, conditional on the version the caller read.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) SelectBuiltinPromptPack(ctx context.Context, organizationID uuid.UUID, builtin store.BuiltinPromptPack, expected store.PromptSelectorToken) (*store.PromptPackSelected, error) {
	existing, err := t.organizationSelector(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	// The record at this scope must be the one the caller read. A deleted
	// and recreated selector is a different record at version 1, and a
	// version-only check would let a caller holding the old "version 1"
	// overwrite it (review round 6). Refused before the lock, with the same
	// error a moved version produces: the caller's remedy is the same --
	// re-read, then decide again.
	if existing.ID != expected.RecordID {
		return nil, fmt.Errorf("%w: the %s selector of organization %s is record %s, caller read %s",
			store.ErrConfigurationConflict, store.PromptPackKey, organizationID, existing.ID, expected.RecordID)
	}
	// Locked and classified before the install. That a refused call leaves
	// no installation behind is the TRANSACTION's property, not this
	// order's -- a mutant that installs first still rolls back on the
	// conflict, and survived the test that claimed otherwise. The order
	// buys only that a stale operator is answered before the gate runs
	// over their pack, which is cheaper and reports the right fault first.
	locked, err := t.lockConfigurationRecord(ctx, organizationID, existing.ID, expected.Version)
	if err != nil {
		return nil, err
	}

	installed, err := t.InstallPromptPack(ctx, t.builtinInstallInput(organizationID, builtin))
	if err != nil {
		return nil, fmt.Errorf("select built-in prompt pack for organization %s: %w", organizationID, err)
	}

	current, err := t.selectionOf(ctx, locked)
	if err == nil && current.Pack.Content.ContentID == installed.Record.Content.ContentID {
		// Already selected: nothing to move, and the version stays where
		// the caller read it.
		return &store.PromptPackSelected{Selection: *current, Moved: false}, nil
	}
	if err != nil && !errors.Is(err, store.ErrPromptSelectorUnresolved) {
		return nil, err
	}
	// An unresolved selector is exactly what this verb repairs: moving it
	// to the built-in is the remedy, so the dangling value is replaced
	// rather than reported.

	value, err := selectorValue(installed.Record.Content.ContentID)
	if err != nil {
		return nil, err
	}
	updated, err := t.UpdateConfigurationRecord(ctx, organizationID, locked.ID, locked.Version, value)
	if err != nil {
		return nil, fmt.Errorf("select built-in prompt pack for organization %s: %w", organizationID, err)
	}
	return &store.PromptPackSelected{
		Selection: store.PromptPackSelection{Pack: installed.Record, Selector: *updated},
		Moved:     true,
	}, nil
}
