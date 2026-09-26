package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// Resolution at dispatch (ADR 0031 section 4; Phase 3 item 4 design, D8).
//
// Runs inside CreateDispatch's transaction, after the basis is measured and
// before the dispatch row is written. Every refusal here returns before any
// row exists, which is why a resolution row has exactly two range results and
// never a third: a refused dispatch has no parent for a refusal to hang from.

// resolvedPack is what resolution decided, before it is persisted.
type resolvedPack struct {
	pack       *store.InstalledPromptPack
	validated  string
	rangeCheck store.PromptRangeCheck
	rerun      bool
}

// resolvePromptPack finds the pack a dispatch runs under and validates it
// against the running harness. Inputs in precedence order: the explicit
// selector, then scoped configuration read through the ordinary path.
func (t *tx) resolvePromptPack(ctx context.Context, operation string, story *store.Story, epic *store.Epic, explicit *store.PromptSelector) (*resolvedPack, error) {
	selector, err := t.selectorFor(ctx, operation, story, epic, explicit)
	if err != nil {
		return nil, err
	}
	pack, err := t.packFor(ctx, story.OrganizationID, selector)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, rejectDispatch(operation, story.StoryID, store.ReasonPromptSelectorUnresolved, selector.String())
		}
		return nil, err
	}
	installation := pack.Installation

	// The declared range can refuse and never authorize. Under a
	// development build the comparison is not made: a pass would assert a
	// comparison that never ran, and a refusal would make every local build
	// undispatchable.
	rangeCheck := store.PromptRangeNotEvaluated
	if !t.harness.IsDev() {
		inRange, rangeErr := t.harness.InRange(installation.MinMaestroVersion, installation.MaxMaestroVersion)
		if rangeErr != nil {
			return nil, fmt.Errorf("check prompt pack %s against the running version: %w", pack.Content.Identity(), rangeErr)
		}
		if !inRange {
			return nil, rejectDispatch(operation, story.StoryID, store.ReasonPromptPackIncompatible,
				fmt.Sprintf("%s declares [%s, %s), harness is %s", pack.Content.Identity(),
					installation.MinMaestroVersion, installation.MaxMaestroVersion, t.harness))
		}
		rangeCheck = store.PromptRangePassed
	}

	// The harness contract is re-run when the version has moved since the
	// gate last validated this installation, or when EITHER is the
	// development sentinel -- "dev" == "dev" says nothing about two local
	// binaries built an hour apart. Under a real pair that match, the
	// installation's validation stands.
	validated := installation.ValidatedMaestroVersion
	rerun := installation.ValidatedMaestroVersion != t.harness.String() ||
		t.harness.IsDev() || installation.ValidatedMaestroVersion == "dev"
	if rerun {
		if contractErr := t.prompts.ValidatePack(pack.Content.Entries, installation.DeclaredRoles); contractErr != nil {
			return nil, rejectDispatch(operation, story.StoryID, store.ReasonPromptPackUnusable,
				fmt.Sprintf("%s: %v", pack.Content.Identity(), contractErr))
		}
		validated = t.harness.String()
	}
	return &resolvedPack{pack: pack, validated: validated, rangeCheck: rangeCheck, rerun: rerun}, nil
}

// selectorFor returns the selector that applies: the explicit one, or the
// most specific configuration record for the Epic's repository.
//
// The lineage limit is named rather than discovered (design D8):
// configuration has no Epic or Story level, so a choice at that grain
// arrives only as an explicit selector.
func (t *tx) selectorFor(ctx context.Context, operation string, story *store.Story, epic *store.Epic, explicit *store.PromptSelector) (store.PromptSelector, error) {
	if explicit != nil {
		if explicit.ContentID == nil && explicit.Identity == nil {
			return store.PromptSelector{}, rejectDispatch(operation, story.StoryID, store.ReasonNoPromptSelector, "the explicit selector is empty")
		}
		// Exactly one field, as the wire form requires: with both set the
		// lookup would take the content id and silently ignore the identity
		// the caller also asserted.
		if explicit.ContentID != nil && explicit.Identity != nil {
			return store.PromptSelector{}, rejectDispatch(operation, story.StoryID, store.ReasonPromptSelectorUnresolved,
				"the explicit selector names both a content id and an identity; a selector names exactly one")
		}
		return *explicit, nil
	}
	record, err := t.ResolveConfiguration(ctx, story.OrganizationID, epic.RepositoryID, store.PromptPackKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.PromptSelector{}, rejectDispatch(operation, story.StoryID, store.ReasonNoPromptSelector,
				fmt.Sprintf("no %s record at repository %s, its primary product, or the organization", store.PromptPackKey, epic.RepositoryID))
		}
		return store.PromptSelector{}, err
	}
	selector, err := store.ParsePromptSelector(record.Value)
	if err != nil {
		// A stored value the registry admitted cannot fail to parse; this is
		// the registry and the parser disagreeing.
		return store.PromptSelector{}, fmt.Errorf("%w: configuration record %s holds a %s value the parser refuses: %w",
			store.ErrInvariant, record.ID, store.PromptPackKey, err)
	}
	return selector, nil
}

func (t *tx) packFor(ctx context.Context, organizationID uuid.UUID, selector store.PromptSelector) (*store.InstalledPromptPack, error) {
	if selector.ContentID != nil {
		return t.GetPromptPackByContent(ctx, organizationID, *selector.ContentID)
	}
	return t.GetPromptPackByIdentity(ctx, organizationID, *selector.Identity)
}

// writeResolution persists what resolution decided, after the dispatch row
// that names it.
func (t *tx) writeResolution(ctx context.Context, resolutionID uuid.UUID, dispatch *gen.StoryDispatch, resolved *resolvedPack) (*store.PromptResolution, error) {
	installation := resolved.pack.Installation
	snapshot, err := json.Marshal(store.PromptResolutionSnapshot{
		DisplayName: installation.DisplayName, MinMaestroVersion: installation.MinMaestroVersion,
		MaxMaestroVersion: installation.MaxMaestroVersion, DeclaredRoles: installation.DeclaredRoles,
		ContractRerun: resolved.rerun,
	})
	if err != nil {
		return nil, fmt.Errorf("encode resolution snapshot: %w", err)
	}
	row, err := t.queries.InsertDispatchPromptResolution(ctx, gen.InsertDispatchPromptResolutionParams{
		ResolutionID: toUUID(resolutionID), StoryDispatchID: dispatch.StoryDispatchID,
		OrganizationID: dispatch.OrganizationID, ProductID: dispatch.ProductID, FeatureID: dispatch.FeatureID,
		EpicID: dispatch.EpicID, StoryID: dispatch.StoryID,
		ResolvedName: installation.DisplayName, Scheme: string(resolved.pack.Content.Scheme), Digest: resolved.pack.Content.Digest,
		ContentID: toUUID(resolved.pack.Content.ContentID), InstallationID: toUUID(installation.InstallationID),
		InstallationRevision:    int32(installation.Revision), //nolint:gosec // revisions are small
		MetadataSnapshot:        snapshot,
		ValidatedMaestroVersion: resolved.validated,
		RangeCheck:              string(resolved.rangeCheck),
	})
	if err != nil {
		return nil, fmt.Errorf("insert prompt resolution of dispatch %s: %w", fromUUID(dispatch.StoryDispatchID), err)
	}
	return resolutionFromRow(&row)
}

// resolutionOf reads a dispatch's resolution. Absence is an invariant
// failure, never a legacy state: migration 000023 refused every dispatch
// that predates resolution, and the reciprocal key refuses one without it.
func (t *tx) resolutionOf(ctx context.Context, organizationID, dispatchID uuid.UUID) (*store.PromptResolution, error) {
	row, err := t.queries.GetDispatchPromptResolution(ctx, gen.GetDispatchPromptResolutionParams{
		OrganizationID: toUUID(organizationID), StoryDispatchID: toUUID(dispatchID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: dispatch %s has no prompt resolution; every dispatch since 000023 carries one",
				store.ErrInvariant, dispatchID)
		}
		return nil, fmt.Errorf("read prompt resolution of dispatch %s: %w", dispatchID, err)
	}
	return resolutionFromRow(&row)
}

func resolutionFromRow(row *gen.DispatchPromptResolution) (*store.PromptResolution, error) {
	var snapshot store.PromptResolutionSnapshot
	if err := json.Unmarshal(row.MetadataSnapshot, &snapshot); err != nil {
		return nil, fmt.Errorf("decode resolution %s snapshot: %w", fromUUID(row.ResolutionID), err)
	}
	return &store.PromptResolution{
		ResolvedName:            row.ResolvedName,
		Identity:                store.PromptIdentity{Scheme: store.PromptScheme(row.Scheme), Digest: row.Digest},
		Snapshot:                snapshot,
		ValidatedMaestroVersion: row.ValidatedMaestroVersion,
		RangeCheck:              store.PromptRangeCheck(row.RangeCheck),
		ResolvedAt:              fromTimestamptz(row.ResolvedAt),
		InstallationRevision:    int(row.InstallationRevision),
		ResolutionID:            fromUUID(row.ResolutionID),
		StoryDispatchID:         fromUUID(row.StoryDispatchID),
		ContentID:               fromUUID(row.ContentID),
		InstallationID:          fromUUID(row.InstallationID),
	}, nil
}
