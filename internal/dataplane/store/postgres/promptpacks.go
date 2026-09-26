package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/canonical"
	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/harness"
	"orchestrator/internal/dataplane/store"
)

// The prompt-pack family (ADR 0031; Phase 3 item 4 design, D4, D6, D10).
//
// One atomic validated install and one conditional metadata update. Every
// write consults the composition's PromptContract before touching a row, so
// a pack the plane holds is a pack the harness that supplied the contract
// can render -- that consultation is the whole difference between a
// governing gate and an advisory one, and it is the reason there is no
// public content write for a caller to reach around it.

// digestPrompt computes the plane's own identity for a set of entries:
// canonical.Digest over the slot-to-text object and nothing else, so the
// same content has one identity wherever it is installed and correcting an
// installation's metadata never mints a new pack (ADR 0031 section 1).
//
// This is the seam's OWN computation, not the harness's: the store may not
// import internal/prompt, and the digest is what the row is keyed by, so the
// row's owner computes it. internal/prompt.Digest is the same function over
// the same projection, and a test on the harness side pins the two to one
// vector.
func digestPrompt(entries map[string]string) (string, error) {
	if entries == nil {
		// nil marshals as null, a different document from {} with a
		// different digest; "no entries" has exactly one identity.
		entries = map[string]string{}
	}
	digest, err := canonical.Digest(entries)
	if err != nil {
		return "", fmt.Errorf("digest prompt pack entries: %w", err)
	}
	return digest, nil
}

// checkEntryText refuses what the identity could not faithfully cover:
// json.Marshal substitutes U+FFFD for invalid UTF-8, so two different byte
// strings would share a digest, and jsonb cannot hold U+0000. The harness
// contract refuses these too; the seam re-checks them because it is the
// seam that computes and stores the digest, and a contract is a caller's.
func checkEntryText(entries map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(entries)) {
		text := entries[key]
		switch {
		case strings.TrimSpace(key) == "":
			return errors.New("a prompt pack entry has a blank slot key")
		case !utf8.ValidString(key) || !utf8.ValidString(text):
			return fmt.Errorf("prompt pack entry %q is not valid UTF-8, so its digest would not cover its bytes", key)
		case strings.ContainsRune(key, 0) || strings.ContainsRune(text, 0):
			return fmt.Errorf("prompt pack entry %q contains a NUL character, which the plane cannot store", key)
		}
	}
	return nil
}

// canonicalRoles turns a declared role list into the set the row stores:
// trimmed, de-duplicated, sorted. Order and repetition carry no meaning
// (design D6), and a blank role is refused rather than dropped.
func canonicalRoles(declared []string) ([]string, error) {
	set := make(map[string]bool, len(declared))
	for _, role := range declared {
		if strings.TrimSpace(role) != role || role == "" {
			return nil, fmt.Errorf("declared role %q is blank or carries surrounding whitespace", role)
		}
		set[role] = true
	}
	return slices.Sorted(maps.Keys(set)), nil
}

// rolesObject is the stored form: {"<role>": true, ...}, which the row's
// check constraint holds canonical so jsonb equality is set equality.
func rolesObject(roles []string) ([]byte, error) {
	object := make(map[string]bool, len(roles))
	for _, role := range roles {
		object[role] = true
	}
	raw, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode declared roles: %w", err)
	}
	return raw, nil
}

func rolesFromObject(raw []byte) ([]string, error) {
	var object map[string]bool
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("decode declared roles %s: %w", raw, err)
	}
	return slices.Sorted(maps.Keys(object)), nil
}

// checkInstaller validates the governed installer identity against the
// composition's harness.
//
// A built-in installer records the binary that carried the pack, and the
// one authority for that version is the composition's (design D3): a caller
// that could name another would record the wrong carrier -- `dev` under a
// tagged harness, or a tagged version under `dev` -- and nothing could
// tell. So the value is not merely well-formed; it is the running version.
func checkInstaller(installer store.PromptPackInstaller, running harness.Version) error {
	switch installer.Kind {
	case store.PromptPackInstalledByBuiltin:
		if installer.UserID != nil {
			return errors.New("a built-in installation must not name a user")
		}
		if _, err := harness.Parse(installer.BuiltinMaestroVersion); err != nil {
			return fmt.Errorf("a built-in installation records the binary version that carried it: %w", err)
		}
		if installer.BuiltinMaestroVersion != running.String() {
			return fmt.Errorf("a built-in installation records the binary that carried it, which is this composition's %s, not %q",
				running, installer.BuiltinMaestroVersion)
		}
	case store.PromptPackInstalledByUser:
		if installer.UserID == nil {
			return errors.New("a user installation must name the installing user")
		}
		if installer.BuiltinMaestroVersion != "" {
			return errors.New("a user installation must not record a binary version")
		}
	default:
		return fmt.Errorf("unknown installer kind %q", installer.Kind)
	}
	return nil
}

// gate runs the import gate (design D10): the declaration's own invariants
// first, then the harness contract over entries and coverage. The order is
// deliberate -- a malformed range is a fact about the declaration and needs
// no harness to see, so it is refused before anything the contract says.
func (t *tx) gate(entries map[string]string, minVersion, maxVersion string, roles []string) error {
	if err := harness.CheckRange(minVersion, maxVersion); err != nil {
		return fmt.Errorf("declared maestro range: %w", err)
	}
	if err := checkEntryText(entries); err != nil {
		return err
	}
	if err := t.prompts.ValidatePack(entries, roles); err != nil {
		return fmt.Errorf("%w: %w", store.ErrPromptPackRefused, err)
	}
	return nil
}

// InstallPromptPack is the one way content enters the plane.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) InstallPromptPack(ctx context.Context, input store.InstallPromptPackInput) (store.Bootstrapped[store.InstalledPromptPack], error) {
	var empty store.Bootstrapped[store.InstalledPromptPack]
	if input.OrganizationID == uuid.Nil {
		return empty, errors.New("install prompt pack: organization id is required")
	}
	if err := checkDisplayName("prompt pack", input.DisplayName); err != nil {
		return empty, err
	}
	roles, err := canonicalRoles(input.DeclaredRoles)
	if err != nil {
		return empty, err
	}
	if installerErr := checkInstaller(input.Installer, t.harness); installerErr != nil {
		return empty, installerErr
	}
	if gateErr := t.gate(input.Entries, input.MinMaestroVersion, input.MaxMaestroVersion, roles); gateErr != nil {
		return empty, fmt.Errorf("install prompt pack: %w", gateErr)
	}

	content, err := t.ensureContent(ctx, input.OrganizationID, input.Entries)
	if err != nil {
		return empty, err
	}

	rolesRaw, err := rolesObject(roles)
	if err != nil {
		return empty, err
	}
	installationID, err := newIdentifier(uuid.Nil)
	if err != nil {
		return empty, err
	}
	var builtinVersion *string
	if input.Installer.Kind == store.PromptPackInstalledByBuiltin {
		builtinVersion = &input.Installer.BuiltinMaestroVersion
	}
	inserted, err := t.queries.InsertPromptPackInstallationIfAbsent(ctx, gen.InsertPromptPackInstallationIfAbsentParams{
		InstallationID:          toUUID(installationID),
		OrganizationID:          toUUID(input.OrganizationID),
		ContentID:               toUUID(content.ContentID),
		DisplayName:             input.DisplayName,
		MinMaestroVersion:       input.MinMaestroVersion,
		MaxMaestroVersion:       input.MaxMaestroVersion,
		DeclaredRoles:           rolesRaw,
		InstalledByKind:         string(input.Installer.Kind),
		InstalledByUserID:       toNullUUID(input.Installer.UserID),
		BuiltinMaestroVersion:   builtinVersion,
		ValidatedMaestroVersion: t.harness.String(),
	})
	if err != nil {
		return empty, fmt.Errorf("insert prompt pack installation of %s: %w", content.Identity(), err)
	}
	row, err := t.queries.GetPromptPackInstallationByContent(ctx, gen.GetPromptPackInstallationByContentParams{
		OrganizationID: toUUID(input.OrganizationID), ContentID: toUUID(content.ContentID),
	})
	if err != nil {
		return empty, fmt.Errorf("read prompt pack installation of %s: %w", content.Identity(), err)
	}
	installation, err := installationFromRow(&row)
	if err != nil {
		return empty, err
	}

	// Compared against the STORED row, which may be the other racer's.
	// The declared metadata is compared; the installer is provenance --
	// where the pack came from at first install -- and a later binary
	// re-installing identical content is the no-op D11 requires, not a
	// conflict.
	if inserted == 0 {
		if conflict := declaredMetadataConflict(installation, input.DisplayName, input.MinMaestroVersion,
			input.MaxMaestroVersion, roles, content.Identity()); conflict != nil {
			return empty, conflict
		}
	}
	return store.Bootstrapped[store.InstalledPromptPack]{
		Record:  store.InstalledPromptPack{Content: *content, Installation: *installation},
		Created: inserted == 1,
	}, nil
}

// ensureContent inserts the content row if absent and reads it back.
// Internal to InstallPromptPack: content with no installation is a row
// nothing can select.
func (t *tx) ensureContent(ctx context.Context, organizationID uuid.UUID, entries map[string]string) (*store.PromptPackContent, error) {
	digest, err := digestPrompt(entries)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = map[string]string{}
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("encode prompt pack entries: %w", err)
	}
	contentID, err := newIdentifier(uuid.Nil)
	if err != nil {
		return nil, err
	}
	scheme := string(store.PromptSchemePackJCS)
	if _, insertErr := t.queries.InsertPromptPackContentIfAbsent(ctx, gen.InsertPromptPackContentIfAbsentParams{
		ContentID: toUUID(contentID), OrganizationID: toUUID(organizationID),
		Scheme: scheme, Digest: digest, Entries: raw,
	}); insertErr != nil {
		return nil, fmt.Errorf("insert prompt pack content %s:%s: %w", scheme, digest, insertErr)
	}
	row, err := t.queries.GetPromptPackContentByIdentity(ctx, gen.GetPromptPackContentByIdentityParams{
		OrganizationID: toUUID(organizationID), Scheme: scheme, Digest: digest,
	})
	if err != nil {
		return nil, fmt.Errorf("read prompt pack content %s:%s: %w", scheme, digest, err)
	}
	return contentFromRow(&row)
}

func declaredMetadataConflict(stored *store.PromptPackInstallation, displayName, minVersion, maxVersion string,
	roles []string, identity store.PromptIdentity) error {
	conflict := func(key, storedValue, supplied string) error {
		return &store.BootstrapConflict{
			Kind: "prompt pack installation " + identity.String(), Key: key,
			Stored: storedValue, Supplied: supplied,
		}
	}
	switch {
	case stored.DisplayName != displayName:
		return conflict("display name", stored.DisplayName, displayName)
	case stored.MinMaestroVersion != minVersion:
		return conflict("minimum maestro version", stored.MinMaestroVersion, minVersion)
	case stored.MaxMaestroVersion != maxVersion:
		return conflict("maximum maestro version", stored.MaxMaestroVersion, maxVersion)
	case !slices.Equal(stored.DeclaredRoles, roles):
		return conflict("declared roles", fmt.Sprint(stored.DeclaredRoles), fmt.Sprint(roles))
	}
	return nil
}

// UpdatePromptPackInstallation edits declared metadata under the revision
// the caller read. The gate runs again over the STORED entries: an
// installation valid when written is not thereby valid when edited.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) UpdatePromptPackInstallation(ctx context.Context, input store.UpdatePromptPackInstallationInput) (*store.PromptPackInstallation, error) {
	expected, err := toInt32(input.ExpectedRevision, "expected prompt pack revision")
	if err != nil {
		return nil, err
	}
	if nameErr := checkDisplayName("prompt pack", input.DisplayName); nameErr != nil {
		return nil, nameErr
	}
	roles, err := canonicalRoles(input.DeclaredRoles)
	if err != nil {
		return nil, err
	}

	lockedRow, err := t.queries.LockPromptPackInstallation(ctx, gen.LockPromptPackInstallationParams{
		OrganizationID: toUUID(input.OrganizationID), InstallationID: toUUID(input.InstallationID),
	})
	if err != nil {
		return nil, notFound(err, "prompt pack installation", input.InstallationID)
	}
	locked, err := installationFromRow(&lockedRow)
	if err != nil {
		return nil, err
	}
	if locked.Revision != input.ExpectedRevision {
		return nil, fmt.Errorf("%w: installation %s is at revision %d, caller read %d",
			store.ErrPromptPackConflict, input.InstallationID, locked.Revision, input.ExpectedRevision)
	}

	// The entries come from the LOCKED installation's content, never from
	// the caller: an update names an installation, and the gate judges the
	// content that installation actually governs.
	content, err := t.GetPromptPackContent(ctx, input.OrganizationID, locked.ContentID)
	if err != nil {
		return nil, err
	}
	if gateErr := t.gate(content.Entries, input.MinMaestroVersion, input.MaxMaestroVersion, roles); gateErr != nil {
		return nil, fmt.Errorf("update prompt pack installation %s: %w", input.InstallationID, gateErr)
	}
	rolesRaw, err := rolesObject(roles)
	if err != nil {
		return nil, err
	}
	row, err := t.queries.UpdatePromptPackInstallation(ctx, gen.UpdatePromptPackInstallationParams{
		OrganizationID: toUUID(input.OrganizationID), InstallationID: toUUID(input.InstallationID),
		ExpectedRevision:        expected,
		DisplayName:             input.DisplayName,
		MinMaestroVersion:       input.MinMaestroVersion,
		MaxMaestroVersion:       input.MaxMaestroVersion,
		DeclaredRoles:           rolesRaw,
		ValidatedMaestroVersion: t.harness.String(),
	})
	if err != nil {
		if errors.Is(notFound(err, "prompt pack installation", input.InstallationID), store.ErrNotFound) {
			return nil, fmt.Errorf("%w: update of prompt pack installation %s at revision %d affected no rows "+
				"while the row was locked and classified as writable; the SQL guard and the seam disagree",
				store.ErrInvariant, input.InstallationID, input.ExpectedRevision)
		}
		return nil, fmt.Errorf("update prompt pack installation %s: %w", input.InstallationID, err)
	}
	return installationFromRow(&row)
}

// --- reads -----------------------------------------------------------------

func (t *tx) GetPromptPackContent(ctx context.Context, organizationID, contentID uuid.UUID) (*store.PromptPackContent, error) {
	row, err := t.queries.GetPromptPackContent(ctx, gen.GetPromptPackContentParams{
		OrganizationID: toUUID(organizationID), ContentID: toUUID(contentID),
	})
	if err != nil {
		return nil, notFound(err, "prompt pack content", contentID)
	}
	return contentFromRow(&row)
}

func (t *tx) GetPromptPackInstallation(ctx context.Context, organizationID, installationID uuid.UUID) (*store.PromptPackInstallation, error) {
	row, err := t.queries.GetPromptPackInstallation(ctx, gen.GetPromptPackInstallationParams{
		OrganizationID: toUUID(organizationID), InstallationID: toUUID(installationID),
	})
	if err != nil {
		return nil, notFound(err, "prompt pack installation", installationID)
	}
	return installationFromRow(&row)
}

func (t *tx) GetPromptPackByIdentity(ctx context.Context, organizationID uuid.UUID, identity store.PromptIdentity) (*store.InstalledPromptPack, error) {
	// Only the plane's own scheme names a content row. The query would
	// find nothing anyway -- contents admit one scheme -- but refusing here
	// says why, and stops a caller from reading "not found" as "not yet".
	if identity.Scheme != store.PromptSchemePackJCS {
		return nil, fmt.Errorf("%w: prompt pack %s: only %s identities name plane-owned content",
			store.ErrNotFound, identity, store.PromptSchemePackJCS)
	}
	row, err := t.queries.GetPromptPackContentByIdentity(ctx, gen.GetPromptPackContentByIdentityParams{
		OrganizationID: toUUID(organizationID), Scheme: string(identity.Scheme), Digest: identity.Digest,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: prompt pack %s in organization %s", store.ErrNotFound, identity, organizationID)
		}
		return nil, fmt.Errorf("read prompt pack %s: %w", identity, err)
	}
	content, err := contentFromRow(&row)
	if err != nil {
		return nil, err
	}
	return t.packOf(ctx, content)
}

func (t *tx) GetPromptPackByContent(ctx context.Context, organizationID, contentID uuid.UUID) (*store.InstalledPromptPack, error) {
	content, err := t.GetPromptPackContent(ctx, organizationID, contentID)
	if err != nil {
		return nil, err
	}
	return t.packOf(ctx, content)
}

// packOf joins a content row to its one installation. A content row with
// no installation is an invariant failure: InstallPromptPack writes both in
// one transaction, and nothing else writes content.
func (t *tx) packOf(ctx context.Context, content *store.PromptPackContent) (*store.InstalledPromptPack, error) {
	row, err := t.queries.GetPromptPackInstallationByContent(ctx, gen.GetPromptPackInstallationByContentParams{
		OrganizationID: toUUID(content.OrganizationID), ContentID: toUUID(content.ContentID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: prompt pack content %s (%s) has no installation; content is only "+
				"ever written beside one", store.ErrInvariant, content.ContentID, content.Identity())
		}
		return nil, fmt.Errorf("read installation of prompt pack %s: %w", content.Identity(), err)
	}
	installation, err := installationFromRow(&row)
	if err != nil {
		return nil, err
	}
	return &store.InstalledPromptPack{Content: *content, Installation: *installation}, nil
}

func (t *tx) ListPromptPackInstallations(ctx context.Context, organizationID uuid.UUID) ([]store.PromptPackInstallation, error) {
	rows, err := t.queries.ListPromptPackInstallations(ctx, toUUID(organizationID))
	if err != nil {
		return nil, fmt.Errorf("list prompt pack installations: %w", err)
	}
	installations := make([]store.PromptPackInstallation, 0, len(rows))
	for i := range rows {
		installation, err := installationFromRow(&rows[i])
		if err != nil {
			return nil, err
		}
		installations = append(installations, *installation)
	}
	return installations, nil
}

func contentFromRow(row *gen.PromptPackContent) (*store.PromptPackContent, error) {
	var entries map[string]string
	if err := json.Unmarshal(row.Entries, &entries); err != nil {
		return nil, fmt.Errorf("decode prompt pack %s entries: %w", fromUUID(row.ContentID), err)
	}
	return &store.PromptPackContent{
		Entries:        entries,
		Digest:         row.Digest,
		Scheme:         store.PromptScheme(row.Scheme),
		CreatedAt:      fromTimestamptz(row.CreatedAt),
		ContentID:      fromUUID(row.ContentID),
		OrganizationID: fromUUID(row.OrganizationID),
	}, nil
}

func installationFromRow(row *gen.PromptPackInstallation) (*store.PromptPackInstallation, error) {
	roles, err := rolesFromObject(row.DeclaredRoles)
	if err != nil {
		return nil, err
	}
	installer := store.PromptPackInstaller{
		Kind:   store.PromptPackInstallerKind(row.InstalledByKind),
		UserID: fromNullUUID(row.InstalledByUserID),
	}
	if row.BuiltinMaestroVersion != nil {
		installer.BuiltinMaestroVersion = *row.BuiltinMaestroVersion
	}
	return &store.PromptPackInstallation{
		DisplayName:             row.DisplayName,
		MinMaestroVersion:       row.MinMaestroVersion,
		MaxMaestroVersion:       row.MaxMaestroVersion,
		DeclaredRoles:           roles,
		ValidatedMaestroVersion: row.ValidatedMaestroVersion,
		Installer:               installer,
		CreatedAt:               fromTimestamptz(row.CreatedAt),
		UpdatedAt:               fromTimestamptz(row.UpdatedAt),
		Revision:                int(row.Revision),
		InstallationID:          fromUUID(row.InstallationID),
		OrganizationID:          fromUUID(row.OrganizationID),
		ContentID:               fromUUID(row.ContentID),
	}, nil
}
