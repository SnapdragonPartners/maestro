//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
)

// TestBootstrapConflictSemantics drives all three outcomes of D10's table.
//
// "Idempotent" alone would have been satisfied by silently ignoring differing
// display data, and that is the failure to design out: it makes
// `bootstrap --org acme --org-name "Acme Ltd"` appear to succeed while the
// plane still says "Acme Inc", and the operator finds out from a report
// months later.
func TestBootstrapConflictSemantics(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	created, err := f.store.BootstrapOrganization(ctx, store.BootstrapOrganizationInput{
		Slug: "acme", DisplayName: "Acme Inc",
	})
	if err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if !created.Created {
		t.Error("the first bootstrap must report Created")
	}

	// Matching display data: the no-op, distinguishable from the creation.
	again, err := f.store.BootstrapOrganization(ctx, store.BootstrapOrganizationInput{
		Slug: "acme", DisplayName: "Acme Inc",
	})
	if err != nil {
		t.Fatalf("matching re-bootstrap must be a no-op, got: %v", err)
	}
	if again.Created {
		t.Error("a re-bootstrap must report Created=false")
	}
	if again.Record.OrganizationID != created.Record.OrganizationID {
		t.Error("a re-bootstrap must return the SAME row, not a second one")
	}

	// Differing display data: a typed conflict, and nothing changes.
	_, err = f.store.BootstrapOrganization(ctx, store.BootstrapOrganizationInput{
		Slug: "acme", DisplayName: "Acme Ltd",
	})
	if !errors.Is(err, store.ErrBootstrapConflict) {
		t.Fatalf("differing display data must be a typed conflict, got: %v", err)
	}
	stored, err := f.store.GetOrganizationBySlug(ctx, "acme")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.DisplayName != "Acme Inc" {
		t.Errorf("the refused bootstrap renamed the organization to %q", stored.DisplayName)
	}
}

// TestBootstrapRejectsMalformedInput asserts the validation happens before
// SQL, so an operator's mistake is reported in the vocabulary of the flag
// they typed rather than as a constraint name.
func TestBootstrapRejectsMalformedInput(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for name, input := range map[string]store.BootstrapOrganizationInput{
		"blank slug":            {Slug: "", DisplayName: "X"},
		"whitespace slug":       {Slug: "   ", DisplayName: "X"},
		"uppercase slug":        {Slug: "Acme", DisplayName: "X"},
		"slug with a separator": {Slug: "acme/inc", DisplayName: "X"},
		"slug with a dot":       {Slug: "..", DisplayName: "X"},
		"blank display name":    {Slug: "acme", DisplayName: "  "},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := f.store.BootstrapOrganization(ctx, input); err == nil {
				t.Fatal("malformed input must be refused")
			}
		})
	}
}

// TestConcurrentBootstrapConverges is the reason this is insert-or-nothing
// rather than check-then-insert.
//
// Two operators racing would both observe no row, both insert, and one would
// receive a raw 23505 — an outcome that is neither of the two successes and
// leaks a driver error through the seam. Exactly one caller must report
// Created, every caller must see the same row, and none may fail.
func TestConcurrentBootstrapConverges(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	const racers = 8
	var wait sync.WaitGroup
	results := make([]store.Bootstrapped[store.Organization], racers)
	failures := make([]error, racers)
	wait.Add(racers)
	for i := range racers {
		go func() {
			defer wait.Done()
			results[i], failures[i] = f.store.BootstrapOrganization(ctx,
				store.BootstrapOrganizationInput{Slug: "racer", DisplayName: "Racer"})
		}()
	}
	wait.Wait()

	createdCount := 0
	for i := range racers {
		if failures[i] != nil {
			t.Fatalf("racer %d failed; matching creates must converge, not error: %v", i, failures[i])
		}
		if results[i].Created {
			createdCount++
		}
		if results[i].Record.OrganizationID != results[0].Record.OrganizationID {
			t.Errorf("racer %d saw a different row; the racers did not converge", i)
		}
	}
	if createdCount != 1 {
		t.Errorf("%d racers reported Created, want exactly 1", createdCount)
	}
}

// TestConcurrentBootstrapConflictIsTyped covers the other race: racers
// supplying DIFFERENT display data. Whoever commits first wins, and the
// losers must receive the typed conflict rather than a driver error.
func TestConcurrentBootstrapConflictIsTyped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	const racers = 8
	var wait sync.WaitGroup
	failures := make([]error, racers)
	wait.Add(racers)
	for i := range racers {
		go func() {
			defer wait.Done()
			name := "Name A"
			if i%2 == 1 {
				name = "Name B"
			}
			_, failures[i] = f.store.BootstrapOrganization(ctx,
				store.BootstrapOrganizationInput{Slug: "contested", DisplayName: name})
		}()
	}
	wait.Wait()

	conflicts := 0
	for i := range racers {
		switch {
		case failures[i] == nil:
			continue // agreed with whoever won
		case errors.Is(failures[i], store.ErrBootstrapConflict):
			conflicts++
		default:
			t.Fatalf("racer %d got a raw error rather than a typed conflict: %v", i, failures[i])
		}
	}
	if conflicts == 0 {
		t.Error("no racer saw a conflict; the fixture is not exercising contention")
	}
}

// TestBenchmarkAttemptIdempotency covers the ledger's three outcomes: the
// first import writes, an identical re-offer is a no-op, and a DIFFERENT
// payload for the same identity is refused.
//
// Rejecting rather than overwriting is the point. Run records are append-only
// on disk and never rewritten, so a differing digest means the file changed —
// and overwriting would erase the evidence of exactly that.
func TestBenchmarkAttemptIdempotency(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	run, err := f.store.EnsureBenchmarkRun(ctx, f.organizationID, "golden-all-probe")
	if err != nil {
		t.Fatalf("ensure run: %v", err)
	}
	if !run.Created {
		t.Error("the first EnsureBenchmarkRun must report Created")
	}
	// A benchmark run carries nothing a second call could change, which is
	// what makes re-import a read rather than a write.
	again, err := f.store.EnsureBenchmarkRun(ctx, f.organizationID, "golden-all-probe")
	if err != nil {
		t.Fatalf("re-ensure run: %v", err)
	}
	if again.Created || again.Record.BenchmarkRunID != run.Record.BenchmarkRunID {
		t.Errorf("re-ensure must return the same row with Created=false: %+v", again)
	}

	artifact := f.newBenchmarkAuditArtifact(t, run.Record.BenchmarkRunID)
	const digest = "1111111111111111111111111111111111111111111111111111111111111111"
	input := store.RecordBenchmarkAttemptInput{
		RunID:           "story-a--config--r1--abcd1234",
		RecordDigest:    digest,
		OrganizationID:  f.organizationID,
		BenchmarkRunID:  run.Record.BenchmarkRunID,
		AuditArtifactID: artifact,
	}

	first, err := f.store.RecordBenchmarkAttempt(ctx, input)
	if err != nil {
		t.Fatalf("record attempt: %v", err)
	}
	if !first.Created {
		t.Error("the first record must report Created")
	}

	repeat, err := f.store.RecordBenchmarkAttempt(ctx, input)
	if err != nil {
		t.Fatalf("an identical re-offer must be a no-op, got: %v", err)
	}
	if repeat.Created {
		t.Error("a re-offer must report Created=false")
	}
	if repeat.Record.BenchmarkAttemptID != first.Record.BenchmarkAttemptID {
		t.Error("a re-offer must return the same ledger row")
	}

	conflicting := input
	conflicting.RecordDigest = "2222222222222222222222222222222222222222222222222222222222222222"
	_, err = f.store.RecordBenchmarkAttempt(ctx, conflicting)
	if !errors.Is(err, store.ErrImportConflict) {
		t.Fatalf("a differing payload must be refused, got: %v", err)
	}
	// The message has to carry both digests and the suite: the operator's
	// next question is always which side is wrong.
	var conflict *store.ImportConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("the conflict must carry its detail, got %T", err)
	}
	if conflict.StoredDigest != digest || conflict.OfferedDigest != conflicting.RecordDigest ||
		conflict.SuiteRunID != "golden-all-probe" {
		t.Errorf("conflict detail is incomplete: %+v", conflict)
	}

	// And nothing moved: the stored row still carries the first digest.
	stored, err := f.store.GetBenchmarkAttempt(ctx, f.organizationID, run.Record.BenchmarkRunID, input.RunID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.RecordDigest != digest {
		t.Errorf("the refused import overwrote the ledger: digest is now %s", stored.RecordDigest)
	}

	listed, err := f.store.ListBenchmarkAttempts(ctx, f.organizationID, run.Record.BenchmarkRunID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 {
		t.Errorf("the suite has %d ledger rows, want 1; a re-offer duplicated", len(listed))
	}
}

// TestBenchmarkLookupsAreTenantScoped: a caller must not be able to reach
// another organization's run or attempt, and the two must be indistinguishable
// from "does not exist" so an identifier cannot be probed.
func TestBenchmarkLookupsAreTenantScoped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	run, err := f.store.EnsureBenchmarkRun(ctx, f.organizationID, "tenant-probe")
	if err != nil {
		t.Fatalf("ensure run: %v", err)
	}
	other := f.newOrganization(t)

	if _, err := f.store.GetBenchmarkRunBySuite(ctx, other, "tenant-probe"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another tenant's suite must read as not found, got: %v", err)
	}
	if _, err := f.store.GetBenchmarkAttempt(ctx, other, run.Record.BenchmarkRunID, "anything"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another tenant's attempt must read as not found, got: %v", err)
	}
	// Same suite id in two organizations is legitimate and must not collide.
	otherRun, err := f.store.EnsureBenchmarkRun(ctx, other, "tenant-probe")
	if err != nil {
		t.Fatalf("the same suite id in another tenant must be allowed: %v", err)
	}
	if otherRun.Record.BenchmarkRunID == run.Record.BenchmarkRunID {
		t.Error("two tenants' suites collapsed onto one row")
	}
}

// newBenchmarkAuditArtifact writes one benchmark-scoped Audit artifact, the
// shape a run record becomes, so the ledger has a real artifact to name.
func (f *fixture) newBenchmarkAuditArtifact(t *testing.T, runID uuid.UUID) uuid.UUID {
	t.Helper()
	artifactID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("allocate artifact id: %v", err)
	}
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO audit_artifacts (artifact_id, organization_id, artifact_type, scope_type,
		                             scope_benchmark_run_id, author_instance_id, schema_version,
		                             summary, payload, payload_digest)
		VALUES ($1, $2, 'benchmark.run_record', 'benchmark', $3, $4, 1, 'attempt', '{}'::jsonb,
		        repeat('d', 64))`,
		artifactID, f.organizationID, runID, f.systemAgent); err != nil {
		t.Fatalf("seed benchmark audit artifact: %v", err)
	}
	return artifactID
}

// newOrganization returns a second tenant's id for cross-tenant assertions.
func (f *fixture) newOrganization(t *testing.T) uuid.UUID {
	t.Helper()
	return f.otherOrgID
}
