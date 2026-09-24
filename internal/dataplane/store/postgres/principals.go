package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// checkKindFields mirrors the schema's rule that kind = 'human' exactly
// when user_id is present, and refuses kind = 'agent' outright.
//
// The database enforces the first already. Repeating it here is not
// belt-and-braces -- it is the difference between a caller reading
// "principal_instances_human_fields_check" and reading which field it
// omitted.
//
// The agent refusal is the partition (item 4 design, D5): every agent
// principal has a pack, and which kind of pack is decided by the path that
// creates it, so there is no general agent path. A general path that
// admitted agents would be a fourth door into a three-shape schema -- one
// that derives no origin.
//
//nolint:gocritic // by value deliberately: the seam must not alias a caller's input struct
func checkKindFields(input store.CreatePrincipalInstanceInput) error {
	switch input.Kind {
	case store.PrincipalAgent:
		return errors.New("CreatePrincipalInstance does not create agents: an agent principal carries the pack it " +
			"ran under, so it arrives through RecordForeignAgentPrincipal (an import) or " +
			"CreateDispatchedPrincipalInstance (a live agent under an execution)")
	case store.PrincipalHuman:
		if input.UserID == nil {
			return errors.New("a human principal requires a user id")
		}
	case store.PrincipalSystem:
		if input.UserID != nil {
			return errors.New("a system principal must not carry a user id")
		}
	default:
		return fmt.Errorf("unknown principal kind %q", input.Kind)
	}
	return nil
}

// checkRecordedLifetime rejects a historical lifetime that could not have
// happened.
//
// The zero time is year 1: present in the struct and no more a timestamp
// than an unset field, and an instance carrying it sorts before every window
// a query could ask about. A stop before its start is a lifetime that ran
// backwards. Neither is caught by the schema -- its only stop constraint is
// that time and reason are null together -- so the seam is where a caller
// finds out, before the row exists rather than after.
func checkRecordedLifetime(recorded *store.RecordedLifetime) error {
	switch {
	case recorded.StartTime.IsZero():
		return errors.New("a recorded lifetime needs a start time; the zero time is year 1, not a timestamp")
	case recorded.StopTime.IsZero():
		return errors.New("a recorded lifetime needs a stop time; the zero time is year 1, not a timestamp")
	case recorded.StopTime.Before(recorded.StartTime):
		return fmt.Errorf("recorded lifetime stops at %s, before it starts at %s",
			recorded.StopTime, recorded.StartTime)
	case recorded.StopReason == "":
		return errors.New("a recorded lifetime needs a stop reason; it is the diagnostic that says why the instance ended")
	}
	return nil
}

// v1ManifestDigestPattern is the storage form of a v1-manifest-sha256
// digest -- the seam's copy of the schema's per-scheme check
// (principal_instances_prompt_pack_scheme_check), so a caller reads which
// form its digest failed rather than a constraint name. Under the legacy
// scheme the sha256: prefix is DATA, not a scheme marker; the scheme column
// says which form to expect (design D4).
//
// It is the only form a foreign pack has: the scheme is a literal in the
// statement, not an input, and there is no enumeration to widen (design
// D5a; backwards compatibility with another legacy form is a non-goal).
var v1ManifestDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func checkForeignPromptPack(pack *store.ForeignPromptPack) error {
	if strings.TrimSpace(pack.Name) == "" {
		return errors.New("a foreign prompt pack needs a name; it is the label the run record carried")
	}
	if !v1ManifestDigestPattern.MatchString(pack.Digest) {
		return fmt.Errorf("prompt digest %q is not the storage form of scheme %s (%s)",
			pack.Digest, store.PromptSchemeV1Manifest, v1ManifestDigestPattern)
	}
	return nil
}

func principalFromRow(row *gen.PrincipalInstance) (*store.PrincipalInstance, error) {
	var pack *store.PrincipalPromptPack
	if row.PromptPackOrigin != nil {
		projected, err := principalPromptPackFromRow(row)
		if err != nil {
			return nil, err
		}
		pack = projected
	}
	return &store.PrincipalInstance{
		AgentType:         fromNullString(row.AgentType),
		HarnessConfigHash: fromNullString(row.HarnessConfigHash),
		MaestroVersion:    fromNullString(row.MaestroVersion),
		UserID:            fromNullUUID(row.UserID),
		StopTime:          fromNullTimestamptz(row.StopTime),
		StopReason:        fromNullString(row.StopReason),

		PromptPack: pack,

		Kind:  store.PrincipalKind(row.Kind),
		Model: row.Model,

		Lineage: store.Lineage{
			ProductID: fromNullUUID(row.ProductID),
			FeatureID: fromNullUUID(row.FeatureID),
			EpicID:    fromNullUUID(row.EpicID),
			StoryID:   fromNullUUID(row.StoryID),
		},

		PrincipalInstanceID: fromUUID(row.PrincipalInstanceID),
		OrganizationID:      fromUUID(row.OrganizationID),

		StartTime: fromTimestamptz(row.StartTime),
	}, nil
}

// principalPromptPackFromRow projects the two agent shapes the schema
// admits, on a row whose origin is present. The shape constraint guarantees
// the columns arrive all-or-nothing per shape, so a row that disagrees with
// it is an invariant failure, not a case to tolerate.
func principalPromptPackFromRow(row *gen.PrincipalInstance) (*store.PrincipalPromptPack, error) {
	if row.PromptPackName == nil || row.PromptPackScheme == nil || row.PromptHash == nil {
		return nil, fmt.Errorf("%w: principal %s carries origin %q with an incomplete identity",
			store.ErrInvariant, fromUUID(row.PrincipalInstanceID), *row.PromptPackOrigin)
	}
	pack := &store.PrincipalPromptPack{
		Origin:   store.PromptPackOrigin(*row.PromptPackOrigin),
		Name:     *row.PromptPackName,
		Identity: store.PromptIdentity{Scheme: store.PromptScheme(*row.PromptPackScheme), Digest: *row.PromptHash},
	}
	if pack.Origin != store.PromptPackOriginResolved {
		return pack, nil
	}
	if !row.PromptPackContentID.Valid || !row.PromptPackInstallationID.Valid ||
		row.PromptPackInstallationRevision == nil || row.PromptPackMetadataSnapshot == nil {
		return nil, fmt.Errorf("%w: resolved principal %s is missing a reference the shape constraint requires",
			store.ErrInvariant, fromUUID(row.PrincipalInstanceID))
	}
	var snapshot store.PromptResolutionSnapshot
	if err := json.Unmarshal(row.PromptPackMetadataSnapshot, &snapshot); err != nil {
		return nil, fmt.Errorf("decode principal %s prompt snapshot: %w", fromUUID(row.PrincipalInstanceID), err)
	}
	pack.Resolution = &store.PrincipalPromptPackResolution{
		Snapshot:             snapshot,
		InstallationRevision: int(*row.PromptPackInstallationRevision),
		ContentID:            fromUUID(row.PromptPackContentID),
		InstallationID:       fromUUID(row.PromptPackInstallationID),
	}
	return pack, nil
}

// CreatePrincipalInstance writes a human or system instance and its seeding
// set in ONE transaction (design D7). It refuses agents; see checkKindFields.
//
// ADR 0021 promises that "what was this agent given to start?" is always a
// query. An instance observable without its inputs makes that promise false
// for exactly as long as the gap, so there is no version of this that
// writes the instance first and the seeds afterwards.
//
// A RecordedLifetime is written in the same INSERT for the same reason at a
// smaller scale: an instance whose whole lifetime is already over must never
// be observable open, and create-then-stop makes it observable open for the
// width of a statement.
//
// The input is taken by value so a caller cannot mutate it after the call
// begins. One struct copy per instance creation is not worth trading that
// guarantee for.
//
//nolint:gocritic // hugeParam: by value, deliberately -- see above
func (t *tx) CreatePrincipalInstance(ctx context.Context, input store.CreatePrincipalInstanceInput) (*store.PrincipalInstance, error) {
	if err := checkKindFields(input); err != nil {
		return nil, err
	}
	var startTime, stopTime *time.Time
	var stopReason *string
	if input.Recorded != nil {
		// Copied, not pointed at. The input struct is taken by value so a
		// caller cannot mutate it mid-call, and a pointer field would hand
		// that guarantee straight back -- the validation below would then be
		// checking values the INSERT need not still be using.
		recorded := *input.Recorded
		if err := checkRecordedLifetime(&recorded); err != nil {
			return nil, err
		}
		startTime, stopTime = &recorded.StartTime, &recorded.StopTime
		stopReason = &recorded.StopReason
	}

	instanceID, err := newIdentifier(uuid.Nil)
	if err != nil {
		return nil, err
	}
	row, createErr := t.queries.CreatePrincipalInstance(ctx, gen.CreatePrincipalInstanceParams{
		PrincipalInstanceID: toUUID(instanceID),
		OrganizationID:      toUUID(input.OrganizationID),
		Kind:                string(input.Kind),
		Model:               input.Model,
		HarnessConfigHash:   input.HarnessConfigHash,
		MaestroVersion:      input.MaestroVersion,
		UserID:              toNullUUID(input.UserID),
		ProductID:           toNullUUID(input.Lineage.ProductID),
		FeatureID:           toNullUUID(input.Lineage.FeatureID),
		EpicID:              toNullUUID(input.Lineage.EpicID),
		StoryID:             toNullUUID(input.Lineage.StoryID),
		StartTime:           toNullTimestamptz(startTime),
		StopTime:            toNullTimestamptz(stopTime),
		StopReason:          stopReason,
	})
	if createErr != nil {
		return nil, fmt.Errorf("create principal instance: %w", createErr)
	}
	if err := t.seedInstance(ctx, input.OrganizationID, instanceID, input.Seeds); err != nil {
		return nil, err
	}
	return principalFromRow(&row)
}

// RecordForeignAgentPrincipal is the import path (design D5): a closed
// lifetime, a foreign pack identity, origin foreign and the legacy scheme
// -- the last two written by the statement, not by this code.
//
// The lifetime is required and validated whole. That is the one property
// separating an import from a live agent: an agent whose lifetime is not
// over is not something an importer has, and recording one here would be
// ADR 0031 section 2's "only for imports" violated by the verb that exists
// to honour it.
//
//nolint:gocritic // hugeParam: by value, deliberately -- see CreatePrincipalInstance
func (t *tx) RecordForeignAgentPrincipal(ctx context.Context, input store.RecordForeignAgentPrincipalInput) (*store.PrincipalInstance, error) {
	if strings.TrimSpace(input.AgentType) == "" {
		return nil, errors.New("an agent principal requires an agent type; it is what MPH comparisons group by")
	}
	if err := checkForeignPromptPack(&input.Pack); err != nil {
		return nil, err
	}
	if err := checkRecordedLifetime(&input.Lifetime); err != nil {
		return nil, err
	}

	instanceID, err := newIdentifier(uuid.Nil)
	if err != nil {
		return nil, err
	}
	row, createErr := t.queries.RecordForeignAgentPrincipal(ctx, gen.RecordForeignAgentPrincipalParams{
		PrincipalInstanceID: toUUID(instanceID),
		OrganizationID:      toUUID(input.OrganizationID),
		Model:               input.Model,
		AgentType:           &input.AgentType,
		PromptPackName:      &input.Pack.Name,
		PromptHash:          &input.Pack.Digest,
		HarnessConfigHash:   input.HarnessConfigHash,
		MaestroVersion:      input.MaestroVersion,
		ProductID:           toNullUUID(input.Lineage.ProductID),
		FeatureID:           toNullUUID(input.Lineage.FeatureID),
		EpicID:              toNullUUID(input.Lineage.EpicID),
		StoryID:             toNullUUID(input.Lineage.StoryID),
		StartTime:           toNullTimestamptz(&input.Lifetime.StartTime),
		StopTime:            toNullTimestamptz(&input.Lifetime.StopTime),
		StopReason:          &input.Lifetime.StopReason,
	})
	if createErr != nil {
		return nil, fmt.Errorf("record foreign agent principal: %w", createErr)
	}
	if err := t.seedInstance(ctx, input.OrganizationID, instanceID, input.Seeds); err != nil {
		return nil, err
	}
	return principalFromRow(&row)
}

// CreateDispatchedPrincipalInstance is the live path (design D5): the
// principal's pack is the persisted resolution of its execution's dispatch,
// and its lineage is the execution's.
//
// The copy is a property of the INSERT ... SELECT, not of this function: no
// pack field passes through Go, so there is nothing here a caller or a
// later edit could substitute. The Maestro version is the running harness's,
// read from the seam's one authority for it (design D3) rather than taken
// from the caller.
//
// The execution's authority state is not consulted. Whether an agent may
// START under a superseded execution is a dispatch-and-start rule, and
// those are item 6's (design D11); this verb records what the caller is
// doing under the execution it names.
//
//nolint:gocritic // hugeParam: by value, deliberately -- see CreatePrincipalInstance
func (t *tx) CreateDispatchedPrincipalInstance(ctx context.Context, input store.CreateDispatchedPrincipalInput) (*store.PrincipalInstance, error) {
	if strings.TrimSpace(input.AgentType) == "" {
		return nil, errors.New("an agent principal requires an agent type; it is what MPH comparisons group by")
	}
	// Read first so a zero-row INSERT below is classifiable: an execution
	// that exists in this organization and yields no row has no
	// resolution, which 000023 and the reciprocal key promise cannot happen.
	if _, err := t.queries.GetExecution(ctx, gen.GetExecutionParams{
		OrganizationID: toUUID(input.OrganizationID), ExecutionID: toUUID(input.ExecutionID),
	}); err != nil {
		return nil, notFound(err, "execution", input.ExecutionID)
	}

	instanceID, err := newIdentifier(uuid.Nil)
	if err != nil {
		return nil, err
	}
	version := t.harness.String()
	row, createErr := t.queries.CreateDispatchedPrincipalInstance(ctx, gen.CreateDispatchedPrincipalInstanceParams{
		PrincipalInstanceID: toUUID(instanceID),
		Model:               input.Model,
		AgentType:           &input.AgentType,
		HarnessConfigHash:   input.HarnessConfigHash,
		MaestroVersion:      &version,
		ExecutionID:         toUUID(input.ExecutionID),
		OrganizationID:      toUUID(input.OrganizationID),
	})
	if createErr != nil {
		if errors.Is(createErr, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: execution %s has no prompt resolution on its dispatch; every dispatch since 000023 carries one",
				store.ErrInvariant, input.ExecutionID)
		}
		return nil, fmt.Errorf("create dispatched principal instance under execution %s: %w", input.ExecutionID, createErr)
	}
	if err := t.seedInstance(ctx, input.OrganizationID, instanceID, input.Seeds); err != nil {
		return nil, err
	}
	return principalFromRow(&row)
}

// seedInstance writes the seeding set inside the caller's transaction.
func (t *tx) seedInstance(ctx context.Context, organizationID, instanceID uuid.UUID, seeds []store.SeedInput) error {
	for i := range seeds {
		seed := &seeds[i]
		if seed.SeededDigest == "" {
			return fmt.Errorf("seed for artifact %s has an empty digest; the digest AS SEEDED is what "+
				"makes a later comparison against the artifact's current digest meaningful", seed.ArtifactID)
		}
		if _, err := t.queries.AddPrincipalInstanceInput(ctx, gen.AddPrincipalInstanceInputParams{
			PrincipalInstanceID: toUUID(instanceID),
			ArtifactID:          toUUID(seed.ArtifactID),
			OrganizationID:      toUUID(organizationID),
			SeededDigest:        seed.SeededDigest,
		}); err != nil {
			return fmt.Errorf("seed instance %s with artifact %s: %w", instanceID, seed.ArtifactID, err)
		}
	}
	return nil
}

// StopPrincipalInstance is once-only and idempotent (design D7).
//
// The lock is what makes this correct rather than merely conditional. Two
// paths finalise one agent lifecycle about a millisecond apart (ADR 0027
// P-6: the ERROR state-notification and the Run()-exit handler), and a
// read-committed statement without the lock would still see the pre-stop
// snapshot after the winner committed -- reporting a null stop time for an
// instance that has one, and losing the reason that says why it died.
//
// Repeat callers get the recorded values and Recorded=false rather than an
// error: two paths racing to finalise one lifecycle is normal, so making
// the loser fail would turn correct shutdown into spurious failure.
func (t *tx) StopPrincipalInstance(ctx context.Context, organizationID, instanceID uuid.UUID, reason string) (store.StopOutcome, error) {
	if reason == "" {
		return store.StopOutcome{}, errors.New("stop reason is empty; it is the diagnostic that says why the instance ended")
	}

	locked, err := t.queries.LockPrincipalInstance(ctx, gen.LockPrincipalInstanceParams{
		PrincipalInstanceID: toUUID(instanceID),
		OrganizationID:      toUUID(organizationID),
	})
	if err != nil {
		return store.StopOutcome{}, notFound(err, "principal instance", instanceID)
	}

	if locked.StopTime.Valid {
		// Already stopped. Return what the winner recorded, unchanged.
		existing := store.StopOutcome{
			StopTime: fromTimestamptz(locked.StopTime),
			Recorded: false,
		}
		if locked.StopReason != nil {
			existing.Reason = *locked.StopReason
		}
		return existing, nil
	}

	affected, err := t.queries.StopPrincipalInstance(ctx, gen.StopPrincipalInstanceParams{
		PrincipalInstanceID: toUUID(instanceID),
		OrganizationID:      toUUID(organizationID),
		StopReason:          &reason,
		StopTime:            toNullTimestamptz(nil),
	})
	if err != nil {
		return store.StopOutcome{}, fmt.Errorf("stop principal instance %s: %w", instanceID, err)
	}
	if affected != 1 {
		return store.StopOutcome{}, fmt.Errorf("%w: stopping instance %s affected no rows despite holding its lock "+
			"with a null stop_time", store.ErrInvariant, instanceID)
	}

	stopped, err := t.queries.GetPrincipalInstance(ctx, gen.GetPrincipalInstanceParams{
		PrincipalInstanceID: toUUID(instanceID),
		OrganizationID:      toUUID(organizationID),
	})
	if err != nil {
		return store.StopOutcome{}, notFound(err, "principal instance", instanceID)
	}
	return store.StopOutcome{
		StopTime: fromTimestamptz(stopped.StopTime),
		Reason:   reason,
		Recorded: true,
	}, nil
}

func (t *tx) GetPrincipalInstance(ctx context.Context, organizationID, instanceID uuid.UUID) (*store.PrincipalInstance, error) {
	row, err := t.queries.GetPrincipalInstance(ctx, gen.GetPrincipalInstanceParams{
		PrincipalInstanceID: toUUID(instanceID),
		OrganizationID:      toUUID(organizationID),
	})
	if err != nil {
		return nil, notFound(err, "principal instance", instanceID)
	}
	return principalFromRow(&row)
}

func (t *tx) ListSeededInputs(ctx context.Context, organizationID, instanceID uuid.UUID) ([]store.SeededInput, error) {
	rows, err := t.queries.ListPrincipalInstanceInputs(ctx, gen.ListPrincipalInstanceInputsParams{
		PrincipalInstanceID: toUUID(instanceID),
		OrganizationID:      toUUID(organizationID),
	})
	if err != nil {
		return nil, fmt.Errorf("list seeded inputs of %s: %w", instanceID, err)
	}
	inputs := make([]store.SeededInput, 0, len(rows))
	for i := range rows {
		inputs = append(inputs, store.SeededInput{
			ArtifactID:   fromUUID(rows[i].ArtifactID),
			SeededDigest: rows[i].SeededDigest,
			SeededAt:     fromTimestamptz(rows[i].SeededAt),
		})
	}
	return inputs, nil
}

// FindPrincipalInstances serves the MPH reads ADR 0021 says cost and
// comparison analysis anchor on. Exactly one axis must be set: a query with
// none would return the whole table, and a query with several would need a
// combined index this schema does not have.
func (t *tx) FindPrincipalInstances(ctx context.Context, query store.MPHQuery) ([]store.PrincipalInstance, error) {
	var (
		rows []gen.PrincipalInstance
		err  error
	)
	switch {
	case axisCount(query) != 1:
		return nil, fmt.Errorf("MPH query needs exactly one axis (model, prompt hash or harness config hash), got %d",
			axisCount(query))
	case query.Model != nil:
		rows, err = t.queries.ListPrincipalInstancesByModel(ctx, gen.ListPrincipalInstancesByModelParams{
			OrganizationID: toUUID(query.OrganizationID),
			Model:          *query.Model,
		})
	case query.PromptIdentity != nil:
		if query.PromptIdentity.Scheme == "" || query.PromptIdentity.Digest == "" {
			return nil, errors.New("MPH query on the prompt axis needs both a scheme and a digest; a digest is comparable only within its scheme")
		}
		scheme, digest := string(query.PromptIdentity.Scheme), query.PromptIdentity.Digest
		rows, err = t.queries.ListPrincipalInstancesByPromptIdentity(ctx, gen.ListPrincipalInstancesByPromptIdentityParams{
			OrganizationID:   toUUID(query.OrganizationID),
			PromptPackScheme: &scheme,
			PromptHash:       &digest,
		})
	default:
		rows, err = t.queries.ListPrincipalInstancesByHarnessConfigHash(ctx, gen.ListPrincipalInstancesByHarnessConfigHashParams{
			OrganizationID:    toUUID(query.OrganizationID),
			HarnessConfigHash: query.HarnessConfigHash,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("find principal instances: %w", err)
	}

	instances := make([]store.PrincipalInstance, 0, len(rows))
	for i := range rows {
		instance, err := principalFromRow(&rows[i])
		if err != nil {
			return nil, err
		}
		instances = append(instances, *instance)
	}
	return instances, nil
}

func axisCount(query store.MPHQuery) int {
	count := 0
	for _, set := range []bool{query.Model != nil, query.PromptIdentity != nil, query.HarnessConfigHash != nil} {
		if set {
			count++
		}
	}
	return count
}
