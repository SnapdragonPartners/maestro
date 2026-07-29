package postgres

import (
	"context"
	"fmt"
	"math"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// Metric and audit events are born final: no lock, no classification, no
// completion. The whole row is caller-supplied, which is why these take the
// domain type rather than a separate input struct -- there is no field the
// seam derives and none it ignores.
//
// Two fields are conventionally left zero. A zero identifier is allocated
// here; a zero timestamp is filled by SQL's now(). Supplying either is
// legitimate: an importer replaying history has its own instants, and item
// 6's cross-store commit order needs the identifier before the row exists.

func metricEventFromRow(row *gen.MetricEvent) store.MetricEvent {
	return store.MetricEvent{
		UserID:              fromNullUUID(row.UserID),
		PrincipalInstanceID: fromNullUUID(row.PrincipalInstanceID),

		Lineage: store.Lineage{
			ProductID: fromNullUUID(row.ProductID),
			FeatureID: fromNullUUID(row.FeatureID),
			EpicID:    fromNullUUID(row.EpicID),
			StoryID:   fromNullUUID(row.StoryID),
		},
		RecordedAt: fromTimestamptz(row.RecordedAt),

		MetricName: row.MetricName,
		Labels:     row.Labels,
		Value:      row.Value,

		MetricEventID:  fromUUID(row.MetricEventID),
		OrganizationID: fromUUID(row.OrganizationID),
	}
}

func auditEventFromRow(row *gen.AuditEvent) store.AuditEvent {
	return store.AuditEvent{
		UserID:              fromNullUUID(row.UserID),
		PrincipalInstanceID: fromNullUUID(row.PrincipalInstanceID),

		OccurredAt: fromTimestamptz(row.OccurredAt),

		EventType: row.EventType,
		Detail:    row.Detail,

		AuditEventID:   fromUUID(row.AuditEventID),
		OrganizationID: fromUUID(row.OrganizationID),
	}
}

// CreateMetricEvent records a measurement.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) CreateMetricEvent(ctx context.Context, event store.MetricEvent) (*store.MetricEvent, error) {
	eventID, err := newIdentifier(event.MetricEventID)
	if err != nil {
		return nil, err
	}
	if lineageErr := checkLineageChain(event.Lineage); lineageErr != nil {
		return nil, lineageErr
	}
	if nameErr := requireName(event.MetricName, "metric_name"); nameErr != nil {
		return nil, nameErr
	}
	// value is double precision, which admits NaN and both infinities. One
	// non-finite value poisons every aggregate that touches it, and it
	// cannot be removed afterwards by any query that averages.
	if math.IsNaN(event.Value) || math.IsInf(event.Value, 0) {
		return nil, fmt.Errorf("metric %q has non-finite value %v; a measurement that is not a number "+
			"poisons every aggregate that reads it", event.MetricName, event.Value)
	}
	labels, err := requiredJSON(event.Labels, "labels")
	if err != nil {
		return nil, err
	}

	row, err := t.queries.CreateMetricEvent(ctx, gen.CreateMetricEventParams{
		MetricEventID:       toUUID(eventID),
		OrganizationID:      toUUID(event.OrganizationID),
		UserID:              toNullUUID(event.UserID),
		PrincipalInstanceID: toNullUUID(event.PrincipalInstanceID),
		ProductID:           toNullUUID(event.Lineage.ProductID),
		FeatureID:           toNullUUID(event.Lineage.FeatureID),
		EpicID:              toNullUUID(event.Lineage.EpicID),
		StoryID:             toNullUUID(event.Lineage.StoryID),
		MetricName:          event.MetricName,
		Labels:              labels,
		Value:               event.Value,
		RecordedAt:          optionalInstant(event.RecordedAt),
	})
	if err != nil {
		return nil, fmt.Errorf("create metric event: %w", err)
	}
	created := metricEventFromRow(&row)
	return &created, nil
}

// CreateAuditEvent records something that happened.
//
// It carries no work lineage at all: audit_events has no product, feature,
// epic or story column, so there is nothing to validate beyond the name.
// The seam offers no way to supply one, which is why "lineage silently
// dropped" is not a failure mode here.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) CreateAuditEvent(ctx context.Context, event store.AuditEvent) (*store.AuditEvent, error) {
	eventID, err := newIdentifier(event.AuditEventID)
	if err != nil {
		return nil, err
	}
	if nameErr := requireName(event.EventType, "event_type"); nameErr != nil {
		return nil, nameErr
	}
	detail, err := requiredJSON(event.Detail, "detail")
	if err != nil {
		return nil, err
	}

	row, err := t.queries.CreateAuditEvent(ctx, gen.CreateAuditEventParams{
		AuditEventID:        toUUID(eventID),
		OrganizationID:      toUUID(event.OrganizationID),
		UserID:              toNullUUID(event.UserID),
		PrincipalInstanceID: toNullUUID(event.PrincipalInstanceID),
		EventType:           event.EventType,
		Detail:              detail,
		OccurredAt:          optionalInstant(event.OccurredAt),
	})
	if err != nil {
		return nil, fmt.Errorf("create audit event: %w", err)
	}
	created := auditEventFromRow(&row)
	return &created, nil
}
