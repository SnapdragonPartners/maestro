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
func seedLLMCallAt(t *testing.T, f *fixture, org uuid.UUID, at time.Time,
	provider, model string, finished bool, succeeded bool, cost *string,
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
			started_at, finished_at, succeeded, error_message, cost_usd)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, org, f.principalFor(org), provider, model, at, finishedAt, outcome, errorMessage, costValue); err != nil {
		t.Fatalf("seed call: %v", err)
	}
	return id
}

// TestKeysetFirstPageReturnsRows is the regression for the bug this cursor
// shipped with. The predicate was `(started_at, id) > (@after_time, @after_id)`
// and on the FIRST page the cursor is absent — so it evaluated to NULL
// rather than true and every list returned an empty first page, looking
// exactly like an empty table.
//
// A test that paginated from page two would never have caught it.
func TestKeysetFirstPageReturnsRows(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	queries := gen.New(f.pool)
	base := time.Now().Add(-time.Hour)

	for i := range 3 {
		seedLLMCallAt(t, f, f.organizationID, base.Add(time.Duration(i)*time.Minute),
			"anthropic", "sonnet", true, true, nil)
	}

	page, err := queries.ListLLMCallsInWindow(ctx, gen.ListLLMCallsInWindowParams{
		OrganizationID: pgUUID(f.organizationID),
		WindowStart:    pgtype.Timestamptz{Time: base.Add(-time.Minute), Valid: true},
		WindowEnd:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
		RowLimit:       10,
	})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(page) != 3 {
		t.Fatalf("first page returned %d rows, want 3. With no cursor the keyset comparison must not "+
			"filter everything out -- an empty first page is indistinguishable from an empty table.", len(page))
	}
}

// TestKeysetPagesThroughTiedTimestamps is why the cursor is (timestamp, id)
// and why every keyset index ends in the primary key. Calls written in the
// same instant share a started_at, and a cursor on time alone would either
// skip the rest of a tied group or return it forever.
func TestKeysetPagesThroughTiedTimestamps(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	queries := gen.New(f.pool)

	// Five calls, ALL at the same instant.
	tied := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	seeded := map[uuid.UUID]bool{}
	for range 5 {
		seeded[seedLLMCallAt(t, f, f.organizationID, tied, "anthropic", "sonnet", true, true, nil)] = true
	}

	seen := map[uuid.UUID]int{}
	var cursorTime pgtype.Timestamptz
	var cursorID pgtype.UUID

	for page := 0; page < 10; page++ {
		rows, err := queries.ListLLMCallsInWindow(ctx, gen.ListLLMCallsInWindowParams{
			OrganizationID: pgUUID(f.organizationID),
			WindowStart:    pgtype.Timestamptz{Time: tied.Add(-time.Minute), Valid: true},
			WindowEnd:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
			AfterTime:      cursorTime,
			AfterID:        cursorID,
			RowLimit:       2,
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			seen[fromPgUUID(row.LlmCallID)]++
		}
		last := rows[len(rows)-1]
		cursorTime, cursorID = last.StartedAt, last.LlmCallID
	}

	if len(seen) != len(seeded) {
		t.Fatalf("paged over %d distinct calls, want %d: a cursor on time alone loses the tail of a "+
			"tied group", len(seen), len(seeded))
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("call %s was returned %d times; the tie-breaker must make paging exact", id, count)
		}
		if !seeded[id] {
			t.Errorf("call %s was returned but never seeded", id)
		}
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

	// Cohort under test: anthropic/sonnet.
	seedLLMCallAt(t, f, org, base, "anthropic", "sonnet", true, true, cost("1.50000000"))
	seedLLMCallAt(t, f, org, base.Add(time.Minute), "anthropic", "sonnet", true, true, cost("2.25000000"))
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
	seedLLMCallAt(t, f, f.organizationID, base, "anthropic", "sonnet", true, true, &cost)

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
