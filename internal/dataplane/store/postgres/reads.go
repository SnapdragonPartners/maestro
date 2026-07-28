package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// The call family's bounded list reads (design D8).
//
// Every list takes a mandatory limit and a keyset cursor of (timestamp,
// id), never OFFSET. The predicates, the ordering and the tie-breaking all
// live in the SQL, where the index plan can be read against them.
//
// The two helpers below carry the page contract, because it is one concept
// rather than eight that merely look alike: eight hand-written copies are
// eight places for "validate, translate, bound" to drift. What genuinely
// differs per list — its query and that query's parameters — stays written
// out at each call site.

// keyset is one page request in driver terms.
type keyset struct {
	afterTime pgtype.Timestamptz
	afterID   pgtype.UUID
	limit     int32
}

// toKeyset validates a page and converts its cursor.
//
// Both halves of the cursor go together: after_time null means "first
// page", and the SQL guards on exactly that. Sending a time without an id
// would make the row comparison null and return an empty page that reads
// like the end of the table, which is why Page.Validate refuses it.
func toKeyset(page store.Page) (keyset, error) {
	if err := page.Validate(); err != nil {
		return keyset{}, fmt.Errorf("invalid page: %w", err)
	}
	converted := keyset{limit: page.Limit}
	if page.After != nil {
		converted.afterTime = toTimestamptz(page.After.At)
		converted.afterID = toUUID(page.After.ID)
	}
	return converted, nil
}

// entityList runs a keyset read scoped to one entity: validate the page,
// build the query's parameters, run it, convert its rows.
func entityList[Params, Row, Item any](ctx context.Context, what string, page store.Page,
	params func(keyset) Params,
	query func(context.Context, Params) ([]Row, error),
	convert func([]Row) ([]Item, error),
) ([]Item, error) {
	keys, err := toKeyset(page)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", what, err)
	}
	rows, err := query(ctx, params(keys))
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", what, err)
	}
	return convert(rows)
}

// windowList is entityList with a mandatory time window.
//
// The window is checked before anything else: a window read states its
// population, and an empty or inverted one states nothing while returning
// nothing, which is indistinguishable from a genuinely quiet period.
func windowList[Params, Row, Item any](ctx context.Context, what string, from, to time.Time, page store.Page,
	params func(start, end pgtype.Timestamptz, keys keyset) Params,
	query func(context.Context, Params) ([]Row, error),
	convert func([]Row) ([]Item, error),
) ([]Item, error) {
	if !to.After(from) {
		return nil, fmt.Errorf("list %s: window end %s is not after window start %s",
			what, to.UTC(), from.UTC())
	}
	return entityList(ctx, what, page, func(keys keyset) Params {
		return params(toTimestamptz(from), toTimestamptz(to), keys)
	}, query, convert)
}

// llmCallsFromRows converts a page of rows, failing on the first row whose
// stored cost cannot be read rather than returning a partial page.
func llmCallsFromRows(rows []gen.LlmCall) ([]store.LLMCall, error) {
	calls := make([]store.LLMCall, 0, len(rows))
	for i := range rows {
		call, err := llmCallFromRow(&rows[i])
		if err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	return calls, nil
}

// The remaining converters return an error only to share the helpers'
// shape. Cost is the one stored value whose conversion can fail, and it
// lives on llm_calls alone.

func toolCallsFromRows(rows []gen.ToolCall) ([]store.ToolCall, error) {
	calls := make([]store.ToolCall, 0, len(rows))
	for i := range rows {
		calls = append(calls, toolCallFromRow(&rows[i]))
	}
	return calls, nil
}

func metricEventsFromRows(rows []gen.MetricEvent) ([]store.MetricEvent, error) {
	events := make([]store.MetricEvent, 0, len(rows))
	for i := range rows {
		events = append(events, metricEventFromRow(&rows[i]))
	}
	return events, nil
}

func auditEventsFromRows(rows []gen.AuditEvent) ([]store.AuditEvent, error) {
	events := make([]store.AuditEvent, 0, len(rows))
	for i := range rows {
		events = append(events, auditEventFromRow(&rows[i]))
	}
	return events, nil
}

//nolint:gocritic // hugeParam: Page by value, matching the seam interface
func (t *tx) ListLLMCallsByStory(ctx context.Context, organizationID, storyID uuid.UUID, page store.Page) ([]store.LLMCall, error) {
	return entityList(ctx, "llm calls by story", page, func(keys keyset) gen.ListLLMCallsByStoryParams {
		return gen.ListLLMCallsByStoryParams{
			OrganizationID: toUUID(organizationID),
			StoryID:        toUUID(storyID),
			AfterTime:      keys.afterTime,
			AfterID:        keys.afterID,
			RowLimit:       keys.limit,
		}
	}, t.queries.ListLLMCallsByStory, llmCallsFromRows)
}

//nolint:gocritic // hugeParam: Page by value, matching the seam interface
func (t *tx) ListLLMCallsByPrincipal(ctx context.Context, organizationID, instanceID uuid.UUID, page store.Page) ([]store.LLMCall, error) {
	return entityList(ctx, "llm calls by principal", page, func(keys keyset) gen.ListLLMCallsByPrincipalParams {
		return gen.ListLLMCallsByPrincipalParams{
			OrganizationID:      toUUID(organizationID),
			PrincipalInstanceID: toUUID(instanceID),
			AfterTime:           keys.afterTime,
			AfterID:             keys.afterID,
			RowLimit:            keys.limit,
		}
	}, t.queries.ListLLMCallsByPrincipal, llmCallsFromRows)
}

//nolint:gocritic,dupl // hugeParam: Page by value; the four window reads share one shape and four generated param types
func (t *tx) ListLLMCallsInWindow(ctx context.Context, organizationID uuid.UUID, from, to time.Time, page store.Page) ([]store.LLMCall, error) {
	return windowList(ctx, "llm calls in window", from, to, page,
		func(start, end pgtype.Timestamptz, keys keyset) gen.ListLLMCallsInWindowParams {
			return gen.ListLLMCallsInWindowParams{
				OrganizationID: toUUID(organizationID),
				WindowStart:    start,
				WindowEnd:      end,
				AfterTime:      keys.afterTime,
				AfterID:        keys.afterID,
				RowLimit:       keys.limit,
			}
		}, t.queries.ListLLMCallsInWindow, llmCallsFromRows)
}

//nolint:gocritic // hugeParam: Page by value, matching the seam interface
func (t *tx) ListToolCallsByStory(ctx context.Context, organizationID, storyID uuid.UUID, page store.Page) ([]store.ToolCall, error) {
	return entityList(ctx, "tool calls by story", page, func(keys keyset) gen.ListToolCallsByStoryParams {
		return gen.ListToolCallsByStoryParams{
			OrganizationID: toUUID(organizationID),
			StoryID:        toUUID(storyID),
			AfterTime:      keys.afterTime,
			AfterID:        keys.afterID,
			RowLimit:       keys.limit,
		}
	}, t.queries.ListToolCallsByStory, toolCallsFromRows)
}

//nolint:gocritic // hugeParam: Page by value, matching the seam interface
func (t *tx) ListToolCallsByPrincipal(ctx context.Context, organizationID, instanceID uuid.UUID, page store.Page) ([]store.ToolCall, error) {
	return entityList(ctx, "tool calls by principal", page, func(keys keyset) gen.ListToolCallsByPrincipalParams {
		return gen.ListToolCallsByPrincipalParams{
			OrganizationID:      toUUID(organizationID),
			PrincipalInstanceID: toUUID(instanceID),
			AfterTime:           keys.afterTime,
			AfterID:             keys.afterID,
			RowLimit:            keys.limit,
		}
	}, t.queries.ListToolCallsByPrincipal, toolCallsFromRows)
}

//nolint:gocritic,dupl // hugeParam: Page by value; the four window reads share one shape and four generated param types
func (t *tx) ListToolCallsInWindow(ctx context.Context, organizationID uuid.UUID, from, to time.Time, page store.Page) ([]store.ToolCall, error) {
	return windowList(ctx, "tool calls in window", from, to, page,
		func(start, end pgtype.Timestamptz, keys keyset) gen.ListToolCallsInWindowParams {
			return gen.ListToolCallsInWindowParams{
				OrganizationID: toUUID(organizationID),
				WindowStart:    start,
				WindowEnd:      end,
				AfterTime:      keys.afterTime,
				AfterID:        keys.afterID,
				RowLimit:       keys.limit,
			}
		}, t.queries.ListToolCallsInWindow, toolCallsFromRows)
}

//nolint:gocritic,dupl // hugeParam: Page by value; the four window reads share one shape and four generated param types
func (t *tx) ListMetricEventsInWindow(ctx context.Context, organizationID uuid.UUID, from, to time.Time, page store.Page) ([]store.MetricEvent, error) {
	return windowList(ctx, "metric events in window", from, to, page,
		func(start, end pgtype.Timestamptz, keys keyset) gen.ListMetricEventsInWindowParams {
			return gen.ListMetricEventsInWindowParams{
				OrganizationID: toUUID(organizationID),
				WindowStart:    start,
				WindowEnd:      end,
				AfterTime:      keys.afterTime,
				AfterID:        keys.afterID,
				RowLimit:       keys.limit,
			}
		}, t.queries.ListMetricEventsInWindow, metricEventsFromRows)
}

//nolint:gocritic,dupl // hugeParam: Page by value; the four window reads share one shape and four generated param types
func (t *tx) ListAuditEventsInWindow(ctx context.Context, organizationID uuid.UUID, from, to time.Time, page store.Page) ([]store.AuditEvent, error) {
	return windowList(ctx, "audit events in window", from, to, page,
		func(start, end pgtype.Timestamptz, keys keyset) gen.ListAuditEventsInWindowParams {
			return gen.ListAuditEventsInWindowParams{
				OrganizationID: toUUID(organizationID),
				WindowStart:    start,
				WindowEnd:      end,
				AfterTime:      keys.afterTime,
				AfterID:        keys.afterID,
				RowLimit:       keys.limit,
			}
		}, t.queries.ListAuditEventsInWindow, auditEventsFromRows)
}
