//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// seedLLMCallAt writes a call with an explicit start time and cost, so a
// test can construct tied timestamps and mixed cost states — neither of
// which the seam would produce on demand.
type tokenCounts struct{ input, output, reasoning, cached int64 }

func seedLLMCallAt(t *testing.T, f *fixture, org uuid.UUID, at time.Time,
	provider, model string, finished bool, succeeded bool, cost *string,
) uuid.UUID {
	t.Helper()
	return seedLLMCallWithTokens(t, f, org, at, provider, model, finished, succeeded, cost, tokenCounts{})
}

func seedLLMCallWithTokens(t *testing.T, f *fixture, org uuid.UUID, at time.Time,
	provider, model string, finished bool, succeeded bool, cost *string, tokens tokenCounts,
) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var finishedAt, outcome, costValue any
	if finished {
		finishedAt = at.Add(time.Second)
		outcome = succeeded
		if cost != nil {
			costValue = *cost
		}
	}
	errorMessage := any(nil)
	if finished && !succeeded {
		errorMessage = "provider timeout"
	}
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO llm_calls (llm_call_id, organization_id, principal_instance_id, provider, model,
			started_at, finished_at, succeeded, error_message, cost_usd,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		id, org, f.principalFor(org), provider, model, at, finishedAt, outcome, errorMessage, costValue,
		tokens.input, tokens.output, tokens.reasoning, tokens.cached); err != nil {
		t.Fatalf("seed call: %v", err)
	}
	return id
}

// seedCallWithStory writes a call carrying the full lineage tuple its
// composite foreign key requires.
func seedCallWithStory(t *testing.T, f *fixture, at time.Time, story uuid.UUID, llm bool) {
	t.Helper()
	ctx := context.Background()
	if llm {
		if _, err := f.pool.Exec(ctx, `
			INSERT INTO llm_calls (llm_call_id, organization_id, principal_instance_id, product_id,
				feature_id, epic_id, story_id, provider, model, started_at)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, 'anthropic', 'sonnet', $7)`,
			f.organizationID, f.author, f.product, f.feature, f.epic, story, at); err != nil {
			t.Fatalf("seed story llm call: %v", err)
		}
		return
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO tool_calls (tool_call_id, organization_id, principal_instance_id, product_id,
			feature_id, epic_id, story_id, tool_name, arguments, started_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, 'shell', '{}'::jsonb, $7)`,
		f.organizationID, f.author, f.product, f.feature, f.epic, story, at); err != nil {
		t.Fatalf("seed story tool call: %v", err)
	}
}

func seedToolCallAt(t *testing.T, f *fixture, at time.Time) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO tool_calls (tool_call_id, organization_id, principal_instance_id, tool_name,
			arguments, started_at) VALUES (gen_random_uuid(), $1, $2, 'shell', '{}'::jsonb, $3)`,
		f.organizationID, f.author, at); err != nil {
		t.Fatalf("seed tool call: %v", err)
	}
}

func seedMetricEventAt(t *testing.T, f *fixture, at time.Time) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO metric_events (metric_event_id, organization_id, principal_instance_id, metric_name,
			value, recorded_at) VALUES (gen_random_uuid(), $1, $2, 'tokens', 1.0, $3)`,
		f.organizationID, f.author, at); err != nil {
		t.Fatalf("seed metric event: %v", err)
	}
}

func seedAuditEventAt(t *testing.T, f *fixture, at time.Time) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO audit_events (audit_event_id, organization_id, principal_instance_id, event_type,
			occurred_at) VALUES (gen_random_uuid(), $1, $2, 'agent.started', $3)`,
		f.organizationID, f.author, at); err != nil {
		t.Fatalf("seed audit event: %v", err)
	}
}

// cursorRow is the (timestamp, id) pair every keyset list returns.
type cursorRow struct {
	at pgtype.Timestamptz
	id pgtype.UUID
}

// listCase is one retained keyset read: how to seed a row it will return,
// and how to page it.
//
// Table-driven because every list carries the SAME two properties and can
// lose either independently. An earlier version exercised only
// ListLLMCallsInWindow, so the other seven could drop their first-page
// guard or their tie-breaker with this suite still green.
type listCase struct {
	name string
	seed func(t *testing.T, f *fixture, at time.Time)
	page func(t *testing.T, f *fixture, after cursorRow, limit int32) []cursorRow
}

func allListCases(story uuid.UUID) []listCase {
	window := func(at time.Time) (pgtype.Timestamptz, pgtype.Timestamptz) {
		return pgtype.Timestamptz{Time: at.Add(-time.Hour), Valid: true},
			pgtype.Timestamptz{Time: at.Add(time.Hour), Valid: true}
	}
	_ = window

	return []listCase{
		{
			name: "llm calls by story",
			seed: func(t *testing.T, f *fixture, at time.Time) { seedCallWithStory(t, f, at, story, true) },
			page: func(t *testing.T, f *fixture, after cursorRow, limit int32) []cursorRow {
				rows, err := gen.New(f.pool).ListLLMCallsByStory(context.Background(),
					gen.ListLLMCallsByStoryParams{OrganizationID: pgUUID(f.organizationID),
						StoryID: pgUUID(story), AfterTime: after.at, AfterID: after.id, RowLimit: limit})
				failIf(t, err)
				out := make([]cursorRow, 0, len(rows))
				for _, r := range rows {
					out = append(out, cursorRow{r.StartedAt, r.LlmCallID})
				}
				return out
			},
		},
		{
			name: "tool calls by story",
			seed: func(t *testing.T, f *fixture, at time.Time) { seedCallWithStory(t, f, at, story, false) },
			page: func(t *testing.T, f *fixture, after cursorRow, limit int32) []cursorRow {
				rows, err := gen.New(f.pool).ListToolCallsByStory(context.Background(),
					gen.ListToolCallsByStoryParams{OrganizationID: pgUUID(f.organizationID),
						StoryID: pgUUID(story), AfterTime: after.at, AfterID: after.id, RowLimit: limit})
				failIf(t, err)
				out := make([]cursorRow, 0, len(rows))
				for _, r := range rows {
					out = append(out, cursorRow{r.StartedAt, r.ToolCallID})
				}
				return out
			},
		},
		{
			name: "llm calls by principal",
			seed: func(t *testing.T, f *fixture, at time.Time) {
				seedLLMCallAt(t, f, f.organizationID, at, "anthropic", "sonnet", true, true, nil)
			},
			page: func(t *testing.T, f *fixture, after cursorRow, limit int32) []cursorRow {
				rows, err := gen.New(f.pool).ListLLMCallsByPrincipal(context.Background(),
					gen.ListLLMCallsByPrincipalParams{OrganizationID: pgUUID(f.organizationID),
						PrincipalInstanceID: pgUUID(f.author), AfterTime: after.at, AfterID: after.id,
						RowLimit: limit})
				failIf(t, err)
				out := make([]cursorRow, 0, len(rows))
				for _, r := range rows {
					out = append(out, cursorRow{r.StartedAt, r.LlmCallID})
				}
				return out
			},
		},
		{
			name: "tool calls by principal",
			seed: func(t *testing.T, f *fixture, at time.Time) { seedToolCallAt(t, f, at) },
			page: func(t *testing.T, f *fixture, after cursorRow, limit int32) []cursorRow {
				rows, err := gen.New(f.pool).ListToolCallsByPrincipal(context.Background(),
					gen.ListToolCallsByPrincipalParams{OrganizationID: pgUUID(f.organizationID),
						PrincipalInstanceID: pgUUID(f.author), AfterTime: after.at, AfterID: after.id,
						RowLimit: limit})
				failIf(t, err)
				out := make([]cursorRow, 0, len(rows))
				for _, r := range rows {
					out = append(out, cursorRow{r.StartedAt, r.ToolCallID})
				}
				return out
			},
		},
		{
			name: "llm calls in window",
			seed: func(t *testing.T, f *fixture, at time.Time) {
				seedLLMCallAt(t, f, f.organizationID, at, "anthropic", "sonnet", true, true, nil)
			},
			page: func(t *testing.T, f *fixture, after cursorRow, limit int32) []cursorRow {
				start, end := window(time.Now().Add(-time.Hour))
				rows, err := gen.New(f.pool).ListLLMCallsInWindow(context.Background(),
					gen.ListLLMCallsInWindowParams{OrganizationID: pgUUID(f.organizationID),
						WindowStart: start, WindowEnd: end, AfterTime: after.at, AfterID: after.id,
						RowLimit: limit})
				failIf(t, err)
				out := make([]cursorRow, 0, len(rows))
				for _, r := range rows {
					out = append(out, cursorRow{r.StartedAt, r.LlmCallID})
				}
				return out
			},
		},
		{
			name: "tool calls in window",
			seed: func(t *testing.T, f *fixture, at time.Time) { seedToolCallAt(t, f, at) },
			page: func(t *testing.T, f *fixture, after cursorRow, limit int32) []cursorRow {
				start, end := window(time.Now().Add(-time.Hour))
				rows, err := gen.New(f.pool).ListToolCallsInWindow(context.Background(),
					gen.ListToolCallsInWindowParams{OrganizationID: pgUUID(f.organizationID),
						WindowStart: start, WindowEnd: end, AfterTime: after.at, AfterID: after.id,
						RowLimit: limit})
				failIf(t, err)
				out := make([]cursorRow, 0, len(rows))
				for _, r := range rows {
					out = append(out, cursorRow{r.StartedAt, r.ToolCallID})
				}
				return out
			},
		},
		{
			name: "metric events in window",
			seed: func(t *testing.T, f *fixture, at time.Time) { seedMetricEventAt(t, f, at) },
			page: func(t *testing.T, f *fixture, after cursorRow, limit int32) []cursorRow {
				start, end := window(time.Now().Add(-time.Hour))
				rows, err := gen.New(f.pool).ListMetricEventsInWindow(context.Background(),
					gen.ListMetricEventsInWindowParams{OrganizationID: pgUUID(f.organizationID),
						WindowStart: start, WindowEnd: end, AfterTime: after.at, AfterID: after.id,
						RowLimit: limit})
				failIf(t, err)
				out := make([]cursorRow, 0, len(rows))
				for _, r := range rows {
					out = append(out, cursorRow{r.RecordedAt, r.MetricEventID})
				}
				return out
			},
		},
		{
			name: "audit events in window",
			seed: func(t *testing.T, f *fixture, at time.Time) { seedAuditEventAt(t, f, at) },
			page: func(t *testing.T, f *fixture, after cursorRow, limit int32) []cursorRow {
				start, end := window(time.Now().Add(-time.Hour))
				rows, err := gen.New(f.pool).ListAuditEventsInWindow(context.Background(),
					gen.ListAuditEventsInWindowParams{OrganizationID: pgUUID(f.organizationID),
						WindowStart: start, WindowEnd: end, AfterTime: after.at, AfterID: after.id,
						RowLimit: limit})
				failIf(t, err)
				out := make([]cursorRow, 0, len(rows))
				for _, r := range rows {
					out = append(out, cursorRow{r.OccurredAt, r.AuditEventID})
				}
				return out
			},
		},
	}
}

func failIf(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("page: %v", err)
	}
}

// TestKeysetListsReturnAFirstPage is the regression for the bug this cursor
// shipped with, across EVERY retained list. With no cursor the comparison
// evaluated to NULL rather than true, so a list returned an empty first
// page — indistinguishable from an empty table. A test that paginated from
// page two would never have caught it.
func TestKeysetListsReturnAFirstPage(t *testing.T) {
	for _, testCase := range allListCases(uuid.Nil) {
		t.Run(testCase.name, func(t *testing.T) {
			f := newFixture(t)
			story := f.seedLineage(t)
			at := time.Now().Add(-time.Hour)
			for _, listCase := range allListCases(story) {
				if listCase.name != testCase.name {
					continue
				}
				for i := range 3 {
					listCase.seed(t, f, at.Add(time.Duration(i)*time.Minute))
				}
				if got := listCase.page(t, f, cursorRow{}, 10); len(got) != 3 {
					t.Fatalf("first page returned %d rows, want 3; with no cursor the keyset comparison "+
						"must not filter everything out", len(got))
				}
			}
		})
	}
}

// TestKeysetListsPageThroughTiedTimestamps: rows written in the same
// instant share a timestamp, and a cursor on time alone either loses the
// tail of a tied group or returns it forever. This is why the cursor is
// (timestamp, id) and why every keyset index ends in the primary key.
func TestKeysetListsPageThroughTiedTimestamps(t *testing.T) {
	for _, outer := range allListCases(uuid.Nil) {
		t.Run(outer.name, func(t *testing.T) {
			f := newFixture(t)
			story := f.seedLineage(t)
			tied := time.Now().Add(-time.Hour).Truncate(time.Millisecond)

			var listCase listCase
			for _, candidate := range allListCases(story) {
				if candidate.name == outer.name {
					listCase = candidate
				}
			}
			const total = 5
			for range total {
				listCase.seed(t, f, tied)
			}

			seen := map[uuid.UUID]int{}
			cursor := cursorRow{}
			for page := 0; page < 10; page++ {
				rows := listCase.page(t, f, cursor, 2)
				if len(rows) == 0 {
					break
				}
				for _, row := range rows {
					seen[uuid.UUID(row.id.Bytes)]++
				}
				cursor = rows[len(rows)-1]
			}

			if len(seen) != total {
				t.Fatalf("paged over %d distinct rows, want %d: a cursor on time alone loses the tail of "+
					"a tied group", len(seen), total)
			}
			for id, count := range seen {
				if count != 1 {
					t.Errorf("row %s returned %d times; the tie-breaker must make paging exact", id, count)
				}
			}
		})
	}
}

// TestAggregateReportsCompletenessAndOutcome pins the cost aggregate's
// whole contract. A bare SUM would report a number that looks complete
// whatever proportion of its cohort it actually covers.
func TestAggregateReportsCompletenessAndOutcome(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	queries := gen.New(f.pool)
	base := time.Now().Add(-time.Hour)
	org := f.organizationID

	cost := func(s string) *string { return &s }

	// Cohort under test: anthropic/sonnet. Token counters are distinct and
	// non-zero per column, so summing the wrong one is visible: all-zero
	// counters make every total zero and every mistake invisible.
	seedLLMCallWithTokens(t, f, org, base, "anthropic", "sonnet", true, true, cost("1.50000000"),
		tokenCounts{input: 100, output: 20, reasoning: 3, cached: 4000})
	seedLLMCallWithTokens(t, f, org, base.Add(time.Minute), "anthropic", "sonnet", true, true,
		cost("2.25000000"), tokenCounts{input: 200, output: 30, reasoning: 5, cached: 6000})
	// Completed, cost genuinely not knowable — paired-local's local models.
	seedLLMCallAt(t, f, org, base.Add(2*time.Minute), "anthropic", "sonnet", true, true, nil)
	// Completed and FAILED, but with a cost incurred: outcome and cost are
	// independent axes.
	seedLLMCallAt(t, f, org, base.Add(3*time.Minute), "anthropic", "sonnet", true, false, cost("0.25000000"))
	// Still running: pending, not unmeasured.
	seedLLMCallAt(t, f, org, base.Add(4*time.Minute), "anthropic", "sonnet", false, false, nil)

	// Same model name, DIFFERENT provider, at a different price. Grouping on
	// model alone would fold this into the total above.
	seedLLMCallAt(t, f, org, base.Add(5*time.Minute), "bedrock", "sonnet", true, true, cost("99.00000000"))
	// Same PROVIDER, different model. Without this, dropping the model
	// predicate entirely leaves the aggregate unchanged.
	seedLLMCallAt(t, f, org, base.Add(7*time.Minute), "anthropic", "opus", true, true, cost("55.00000000"))
	// Another organization, same cohort.
	seedLLMCallAt(t, f, f.otherOrgID, base.Add(6*time.Minute), "anthropic", "sonnet", true, true, cost("77.00000000"))

	got, err := queries.AggregateLLMCost(ctx, gen.AggregateLLMCostParams{
		OrganizationID: pgUUID(org),
		Provider:       "anthropic",
		Model:          "sonnet",
		WindowStart:    pgtype.Timestamptz{Time: base.Add(-time.Minute), Valid: true},
		WindowEnd:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	// 1.50 + 2.25 + 0.25; the unmeasured call contributes nothing and the
	// open one is excluded entirely.
	if total := numericText(t, got.TotalCostUsd); total != "4.00000000" {
		t.Errorf("total = %s, want 4.00000000 (the other provider and the other organization must not "+
			"be folded in, and an open call has no cost yet)", total)
	}
	if got.MeasuredCalls != 3 {
		t.Errorf("measured = %d, want 3", got.MeasuredCalls)
	}
	if got.UnmeasuredCalls != 1 {
		t.Errorf("unmeasured = %d, want 1 (completed, cost not knowable)", got.UnmeasuredCalls)
	}
	if got.OpenCalls != 1 {
		t.Errorf("open = %d, want 1 (pending is NOT unmeasured: a running campaign that classified its "+
			"in-flight calls as permanently unmeasured would under-report its own cost and never correct "+
			"itself)", got.OpenCalls)
	}
	if got.SucceededCalls != 3 || got.FailedCalls != 1 {
		t.Errorf("outcome = %d succeeded, %d failed; want 3 and 1",
			got.SucceededCalls, got.FailedCalls)
	}

	// All four token totals, each a distinct value, so summing the wrong
	// column or dropping a total is visible rather than absorbed.
	for _, total := range []struct {
		name string
		got  int64
		want int64
	}{
		{"input", got.TotalInputTokens, 300},
		{"output", got.TotalOutputTokens, 50},
		{"reasoning", got.TotalReasoningTokens, 8},
		{"cached", got.TotalCachedTokens, 10000},
	} {
		if total.got != total.want {
			t.Errorf("%s tokens = %d, want %d", total.name, total.got, total.want)
		}
	}

	// The identity the design states: every completed call has an outcome,
	// by the completion constraint.
	if got.SucceededCalls+got.FailedCalls != got.MeasuredCalls+got.UnmeasuredCalls {
		t.Errorf("succeeded + failed = %d but measured + unmeasured = %d; every completed call has both "+
			"an outcome and a cost state, so these must agree",
			got.SucceededCalls+got.FailedCalls, got.MeasuredCalls+got.UnmeasuredCalls)
	}
}

// TestAggregateWindowExcludesOutsideCalls: the window is mandatory so an
// aggregate always describes a stated population, and it must actually bound
// the result.
func TestAggregateWindowExcludesOutsideCalls(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	queries := gen.New(f.pool)
	base := time.Now().Add(-time.Hour)
	cost := "5.00000000"

	inside := base.Add(10 * time.Minute)
	seedLLMCallAt(t, f, f.organizationID, inside, "anthropic", "sonnet", true, true, &cost)
	// Before the lower bound...
	seedLLMCallAt(t, f, f.organizationID, base, "anthropic", "sonnet", true, true, &cost)
	// ...and after the upper one. Seeding only the earlier row leaves the
	// window_end predicate removable with this test still green.
	seedLLMCallAt(t, f, f.organizationID, inside.Add(10*time.Minute), "anthropic", "sonnet", true, true, &cost)

	got, err := queries.AggregateLLMCost(ctx, gen.AggregateLLMCostParams{
		OrganizationID: pgUUID(f.organizationID),
		Provider:       "anthropic",
		Model:          "sonnet",
		WindowStart:    pgtype.Timestamptz{Time: inside.Add(-time.Second), Valid: true},
		WindowEnd:      pgtype.Timestamptz{Time: inside.Add(time.Second), Valid: true},
	})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if got.MeasuredCalls != 1 || numericText(t, got.TotalCostUsd) != "5.00000000" {
		t.Errorf("window did not bound the aggregate: %d measured, total %s",
			got.MeasuredCalls, numericText(t, got.TotalCostUsd))
	}
}

func fromPgUUID(id pgtype.UUID) uuid.UUID { return id.Bytes }

// numericText renders a pgtype.Numeric at the column's scale.
//
// The aggregate crosses the seam as numeric, and comparing it as TEXT is
// deliberate: a float comparison here would defeat the point of storing
// cost exactly in the first place.
func numericText(t *testing.T, value pgtype.Numeric) string {
	t.Helper()
	if !value.Valid {
		return "<null>"
	}
	rendered, err := value.Value()
	if err != nil {
		t.Fatalf("render numeric: %v", err)
	}
	text, ok := rendered.(string)
	if !ok {
		t.Fatalf("numeric rendered as %T, want a string", rendered)
	}
	// Postgres may return an unpadded value; compare through the exact type
	// so scale differences do not read as value differences.
	amount, err := store.ParseUSDTotal(text)
	if err != nil {
		t.Fatalf("parse aggregate %q: %v", text, err)
	}
	return amount.String()
}
