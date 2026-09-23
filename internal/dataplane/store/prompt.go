package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// PromptContract is the import gate for prompt packs, as the seam sees it
// (ADR 0031 section 5; Phase 3 item 4 design, D3 and D10).
//
// CONSUMER-OWNED, and that is the point of declaring it here. Whether a pack
// is usable is decided by a slot vocabulary, a per-slot variable contract, a
// template parser and a renderer -- harness machinery, which lives in
// internal/prompt. The seam must consult that gate on every pack write, or
// the gate is advisory: a pack written around it is a pack the plane holds
// and nothing can render. But the seam must not import it, or the renderer
// joins the plane's closure to buy a direct call. So the seam declares what
// it needs, internal/prompt.Registry implements it, and the composition root
// supplies it beside Types and Keys, with their semantics: what slots exist
// is a property of the caller's job.
//
// The parameters are plain types for the same reason. Entries are keyed by
// slot; declaredRoles is a set, so order and repetition carry no meaning.
//
// Implementations must be safe for concurrent use and must not retain or
// modify what they are given: one contract is shared by every pack write the
// seam serves.
type PromptContract interface {
	// ValidatePack reports whether these entries, declaring coverage of
	// these roles, are usable by the harness that supplied this contract.
	ValidatePack(entries map[string]string, declaredRoles []string) error
}

// PromptScheme names the function that produced a prompt-pack digest
// (ADR 0031 section 1). It is persisted protocol: two writers that disagree
// about what a scheme means produce identities that compare equal and are
// not, so both are fixed here and a change to either's meaning is a NEW
// scheme.
type PromptScheme string

const (
	// PromptSchemePackJCS identifies packs the plane owns: SHA-256 over the
	// RFC 8785 serialization of the slot-key to entry-text object. Stored as
	// bare lowercase hex.
	PromptSchemePackJCS PromptScheme = "pack-jcs-sha256-v1"

	// PromptSchemeV1Manifest identifies imported v1 runs: whatever the
	// runner computed, recorded as received with its "sha256:" prefix, and
	// never rederived by the plane.
	PromptSchemeV1Manifest PromptScheme = "v1-manifest-sha256"
)

// PromptIdentity is a scheme-qualified digest. A digest is comparable only
// within its scheme, and this struct is how the seam's query surface makes a
// cross-scheme comparison unexpressible rather than merely discouraged
// (design D4): there is no way to ask for a digest without saying which
// function produced it.
type PromptIdentity struct {
	Scheme PromptScheme
	Digest string
}

// String renders the identity in the selector form, `<scheme>:<digest>`.
// A rendered form, not a storage form: nothing parses it to discover the
// scheme, which is always its own column.
func (p PromptIdentity) String() string { return string(p.Scheme) + ":" + p.Digest }

// Sentinel errors for the pack family.
var (
	// ErrPromptPackConflict reports a conditional installation update that
	// affected no rows because the revision had moved.
	ErrPromptPackConflict = errors.New("prompt pack installation was modified concurrently")

	// ErrPromptPackRefused reports a pack the harness contract refused: it
	// wraps the contract's own error, which names the slot, role or
	// variable at fault.
	ErrPromptPackRefused = errors.New("prompt pack refused by the harness contract")
)

// PromptPackInstallerKind is the governed source an installation records
// (design D6): the built-in records the binary that carried it; an
// operator-supplied pack records the user.
type PromptPackInstallerKind string

// The two installer kinds the row admits.
const (
	PromptPackInstalledByBuiltin PromptPackInstallerKind = "builtin"
	PromptPackInstalledByUser    PromptPackInstallerKind = "user"
)

// PromptPackInstaller is who or what installed a pack. Exactly one of
// UserID and BuiltinMaestroVersion is set, decided by Kind.
type PromptPackInstaller struct {
	Kind                  PromptPackInstallerKind
	UserID                *uuid.UUID
	BuiltinMaestroVersion string
}

// PromptPackContent is an immutable, content-addressed pack version.
type PromptPackContent struct {
	CreatedAt      time.Time
	Entries        map[string]string
	Digest         string
	Scheme         PromptScheme
	ContentID      uuid.UUID
	OrganizationID uuid.UUID
}

// Identity returns the content's scheme-qualified digest.
//
//nolint:gocritic // hugeParam: a value receiver so a read result's Identity() is callable without taking its address
func (c PromptPackContent) Identity() PromptIdentity {
	return PromptIdentity{Scheme: c.Scheme, Digest: c.Digest}
}

// PromptPackInstallation is the mutable record beside a content row.
type PromptPackInstallation struct {
	CreatedAt               time.Time
	UpdatedAt               time.Time
	Installer               PromptPackInstaller
	DisplayName             string
	MinMaestroVersion       string
	MaxMaestroVersion       string
	ValidatedMaestroVersion string
	DeclaredRoles           []string
	Revision                int
	InstallationID          uuid.UUID
	OrganizationID          uuid.UUID
	ContentID               uuid.UUID
}

// InstalledPromptPack is a content row and its one installation.
type InstalledPromptPack struct {
	Content      PromptPackContent
	Installation PromptPackInstallation
}

// InstallPromptPackInput carries entries AND installation metadata, because
// the import gate spans both (design D6): declared-role coverage cannot be
// checked against content alone, and content with no installation is a row
// nothing can select.
type InstallPromptPackInput struct {
	Entries           map[string]string
	Installer         PromptPackInstaller
	DisplayName       string
	MinMaestroVersion string
	MaxMaestroVersion string
	DeclaredRoles     []string
	OrganizationID    uuid.UUID
}

// UpdatePromptPackInstallationInput is a metadata-only edit under the
// revision the caller read.
type UpdatePromptPackInstallationInput struct {
	DisplayName       string
	MinMaestroVersion string
	MaxMaestroVersion string
	DeclaredRoles     []string
	ExpectedRevision  int

	OrganizationID uuid.UUID
	InstallationID uuid.UUID
}

// PromptPackReader resolves packs by identity and by record.
type PromptPackReader interface {
	GetPromptPackContent(ctx context.Context, organizationID, contentID uuid.UUID) (*PromptPackContent, error)
	GetPromptPackInstallation(ctx context.Context, organizationID, installationID uuid.UUID) (*PromptPackInstallation, error)

	// GetPromptPackByIdentity resolves a scheme-qualified digest to the
	// content row and its installation in one organization. Only the
	// plane's own scheme can resolve: a legacy identity names no content
	// row, and asking for one is ErrNotFound, never a cross-scheme match.
	GetPromptPackByIdentity(ctx context.Context, organizationID uuid.UUID, identity PromptIdentity) (*InstalledPromptPack, error)

	// GetPromptPackByContent resolves a content id to the pack.
	GetPromptPackByContent(ctx context.Context, organizationID, contentID uuid.UUID) (*InstalledPromptPack, error)

	ListPromptPackInstallations(ctx context.Context, organizationID uuid.UUID) ([]PromptPackInstallation, error)
}

// PromptPackWriter is the pack family's write surface: one atomic validated
// install, and one conditional metadata update. There is no public content
// write and no unconditional installation update (design D6).
type PromptPackWriter interface {
	// InstallPromptPack runs the whole import gate through the composition's
	// PromptContract, then inserts content and installation in one
	// transaction. Idempotent by identity: identical content with identical
	// declared metadata returns the existing installation with
	// Created=false; identical content with differing metadata is a
	// BootstrapConflict, since there is exactly one installation to disagree
	// with. The installer is provenance, recorded at first install and not
	// compared afterwards.
	InstallPromptPack(ctx context.Context, input InstallPromptPackInput) (Bootstrapped[InstalledPromptPack], error)

	// UpdatePromptPackInstallation edits declared metadata under the
	// revision the caller read, re-running the gate against the stored
	// entries. ErrPromptPackConflict when somebody moved first; ErrNotFound
	// when the installation is gone.
	UpdatePromptPackInstallation(ctx context.Context, input UpdatePromptPackInstallationInput) (*PromptPackInstallation, error)
}
