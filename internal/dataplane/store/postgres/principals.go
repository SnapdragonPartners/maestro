package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// checkKindFields mirrors the schema's two biconditional constraints:
// kind = 'agent' exactly when agent_type is present, and kind = 'human'
// exactly when user_id is present.
//
// The database enforces both already. Repeating them here is not
// belt-and-braces — it is the difference between a caller reading
// "principal_instances_agent_fields_check" and reading which field it
// omitted. The constraints are biconditional, so both directions are
// checked: an agent_type on a human principal would silently corrupt every
// MPH comparison that groups by it, which is why the schema forbids it
// rather than merely tolerating it.
//
//nolint:gocritic // by value deliberately: the seam must not alias a caller's input struct
func checkKindFields(input store.CreatePrincipalInstanceInput) error {
	switch input.Kind {
	case store.PrincipalAgent:
		if input.AgentType == nil {
			return errors.New("an agent principal requires an agent type; it is what MPH comparisons group by")
		}
		if input.UserID != nil {
			return errors.New("an agent principal must not carry a user id; only a human principal is a user")
		}
	case store.PrincipalHuman:
		if input.UserID == nil {
			return errors.New("a human principal requires a user id")
		}
		if input.AgentType != nil {
			return errors.New("a human principal must not carry an agent type")
		}
	case store.PrincipalSystem:
		if input.AgentType != nil {
			return errors.New("a system principal must not carry an agent type")
		}
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
// backwards. Neither is caught by the schema — its only stop constraint is
// that time and reason are null together — so the seam is where a caller
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

func principalFromRow(row *gen.PrincipalInstance) store.PrincipalInstance {
	return store.PrincipalInstance{
		AgentType:         fromNullString(row.AgentType),
		PromptPackID:      fromNullString(row.PromptPackID),
		PromptHash:        fromNullString(row.PromptHash),
		HarnessConfigHash: fromNullString(row.HarnessConfigHash),
		MaestroVersion:    fromNullString(row.MaestroVersion),
		UserID:            fromNullUUID(row.UserID),
		StopTime:          fromNullTimestamptz(row.StopTime),
		StopReason:        fromNullString(row.StopReason),

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
	}
}

// CreatePrincipalInstance writes the instance and its seeding set in ONE
// transaction (design D7).
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
//nolint:gocritic // hugeParam: by value, deliberately — see above
func (t *tx) CreatePrincipalInstance(ctx context.Context, input store.CreatePrincipalInstanceInput) (*store.PrincipalInstance, error) {
	if err := checkKindFields(input); err != nil {
		return nil, err
	}
	var startTime, stopTime *time.Time
	var stopReason *string
	if input.Recorded != nil {
		// Copied, not pointed at. The input struct is taken by value so a
		// caller cannot mutate it mid-call, and a pointer field would hand
		// that guarantee straight back — the validation below would then be
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
		AgentType:           input.AgentType,
		PromptPackID:        input.PromptPackID,
		PromptHash:          input.PromptHash,
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

	for i := range input.Seeds {
		seed := &input.Seeds[i]
		if seed.SeededDigest == "" {
			return nil, fmt.Errorf("seed for artifact %s has an empty digest; the digest AS SEEDED is what "+
				"makes a later comparison against the artifact's current digest meaningful", seed.ArtifactID)
		}
		if _, err := t.queries.AddPrincipalInstanceInput(ctx, gen.AddPrincipalInstanceInputParams{
			PrincipalInstanceID: toUUID(instanceID),
			ArtifactID:          toUUID(seed.ArtifactID),
			OrganizationID:      toUUID(input.OrganizationID),
			SeededDigest:        seed.SeededDigest,
		}); err != nil {
			return nil, fmt.Errorf("seed instance %s with artifact %s: %w", instanceID, seed.ArtifactID, err)
		}
	}

	created := principalFromRow(&row)
	return &created, nil
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
	instance := principalFromRow(&row)
	return &instance, nil
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
	case query.PromptHash != nil:
		rows, err = t.queries.ListPrincipalInstancesByPromptHash(ctx, gen.ListPrincipalInstancesByPromptHashParams{
			OrganizationID: toUUID(query.OrganizationID),
			PromptHash:     query.PromptHash,
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
		instances = append(instances, principalFromRow(&rows[i]))
	}
	return instances, nil
}

func axisCount(query store.MPHQuery) int {
	count := 0
	for _, set := range []bool{query.Model != nil, query.PromptHash != nil, query.HarnessConfigHash != nil} {
		if set {
			count++
		}
	}
	return count
}
