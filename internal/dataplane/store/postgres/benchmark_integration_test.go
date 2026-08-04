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

// provisioner abstracts the two bootstrap paths so D10's outcomes are driven
// against BOTH.
//
// They are not one path with two names: separate SQL, separate uniqueness
// (a slug is global, a handle is per-organization), separate comparison and
// separate tenancy. A suite that exercised only organizations stayed green
// with the user display-data comparison deleted.
type provisioner struct {
	name string
	// bootstrap provisions with the given natural key and display name.
	bootstrap func(t *testing.T, f *fixture, key, display string) (bool, error)
	// storedDisplay reads back the display name for the key.
	storedDisplay func(t *testing.T, f *fixture, key string) string
	// identity returns the row's own id, so a re-bootstrap can be shown to
	// return the SAME row rather than a second one.
	identity func(t *testing.T, f *fixture, key string) uuid.UUID
}

func provisioners() []provisioner {
	return []provisioner{
		{
			name: "organization",
			bootstrap: func(t *testing.T, f *fixture, key, display string) (bool, error) {
				t.Helper()
				out, err := f.store.BootstrapOrganization(context.Background(),
					store.BootstrapOrganizationInput{Slug: key, DisplayName: display})
				return out.Created, err
			},
			storedDisplay: func(t *testing.T, f *fixture, key string) string {
				t.Helper()
				row, err := f.store.GetOrganizationBySlug(context.Background(), key)
				if err != nil {
					t.Fatalf("read organization %q: %v", key, err)
				}
				return row.DisplayName
			},
			identity: func(t *testing.T, f *fixture, key string) uuid.UUID {
				t.Helper()
				row, err := f.store.GetOrganizationBySlug(context.Background(), key)
				if err != nil {
					t.Fatalf("read organization %q: %v", key, err)
				}
				return row.OrganizationID
			},
		},
		{
			name: "user",
			bootstrap: func(t *testing.T, f *fixture, key, display string) (bool, error) {
				t.Helper()
				out, err := f.store.BootstrapUser(context.Background(),
					store.BootstrapUserInput{
						Handle: key, DisplayName: display, OrganizationID: f.organizationID,
					})
				return out.Created, err
			},
			storedDisplay: func(t *testing.T, f *fixture, key string) string {
				t.Helper()
				row, err := f.store.GetUserByHandle(context.Background(), f.organizationID, key)
				if err != nil {
					t.Fatalf("read user %q: %v", key, err)
				}
				return row.DisplayName
			},
			identity: func(t *testing.T, f *fixture, key string) uuid.UUID {
				t.Helper()
				row, err := f.store.GetUserByHandle(context.Background(), f.organizationID, key)
				if err != nil {
					t.Fatalf("read user %q: %v", key, err)
				}
				return row.UserID
			},
		},
	}
}

// TestBootstrapConflictSemantics drives all three outcomes of D10's table,
// for both provisioning paths.
//
// "Idempotent" alone would have been satisfied by silently ignoring differing
// display data, and that is the failure to design out: it makes
// `bootstrap --org acme --org-name "Acme Ltd"` appear to succeed while the
// plane still says "Acme Inc", and the operator finds out from a report
// months later.
func TestBootstrapConflictSemantics(t *testing.T) {
	for _, provision := range provisioners() {
		t.Run(provision.name, func(t *testing.T) {
			f := newFixture(t)

			created, err := provision.bootstrap(t, f, "acme", "Acme Inc")
			if err != nil {
				t.Fatalf("first bootstrap: %v", err)
			}
			if !created {
				t.Error("the first bootstrap must report Created")
			}
			firstID := provision.identity(t, f, "acme")

			// Matching display data: the no-op, distinguishable from creation.
			again, err := provision.bootstrap(t, f, "acme", "Acme Inc")
			if err != nil {
				t.Fatalf("matching re-bootstrap must be a no-op, got: %v", err)
			}
			if again {
				t.Error("a re-bootstrap must report Created=false")
			}
			if provision.identity(t, f, "acme") != firstID {
				t.Error("a re-bootstrap must return the SAME row, not a second one")
			}

			// Differing display data: a typed conflict, and nothing changes.
			if _, err := provision.bootstrap(t, f, "acme", "Acme Ltd"); !errors.Is(err, store.ErrBootstrapConflict) {
				t.Fatalf("differing display data must be a typed conflict, got: %v", err)
			}
			if stored := provision.storedDisplay(t, f, "acme"); stored != "Acme Inc" {
				t.Errorf("the refused bootstrap renamed the record to %q", stored)
			}
		})
	}
}

// TestBootstrapRejectsMalformedInput asserts the validation happens before
// SQL, so an operator's mistake is reported in the vocabulary of the flag
// they typed rather than as a constraint name.
//
// The accepted cases matter as much as the refused ones: the identifier rule
// is the runner's own suite-id shape, so a leading `_` or `-` is VALID, and
// an earlier stricter pattern here refused suite ids the schema admits.
func TestBootstrapRejectsMalformedInput(t *testing.T) {
	for _, provision := range provisioners() {
		t.Run(provision.name, func(t *testing.T) {
			for name, key := range map[string]string{
				"blank":            "",
				"whitespace":       "   ",
				"uppercase":        "Acme",
				"with a separator": "acme/inc",
				"dot":              "..",
				"with a space":     "acme inc",
			} {
				t.Run("refuses "+name, func(t *testing.T) {
					f := newFixture(t)
					if _, err := provision.bootstrap(t, f, key, "X"); err == nil {
						t.Fatal("malformed input must be refused")
					}
				})
			}
			t.Run("refuses a blank display name", func(t *testing.T) {
				f := newFixture(t)
				if _, err := provision.bootstrap(t, f, "acme", "  "); err == nil {
					t.Fatal("a blank display name must be refused")
				}
			})
			for name, key := range map[string]string{
				"leading underscore": "_acme",
				"leading hyphen":     "-acme",
				"digits and dashes":  "golden-all-2026-08-04",
			} {
				t.Run("accepts "+name, func(t *testing.T) {
					f := newFixture(t)
					if _, err := provision.bootstrap(t, f, key, "X"); err != nil {
						t.Fatalf("%q is valid under the runner's own identifier rule: %v", key, err)
					}
				})
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
	for _, provision := range provisioners() {
		t.Run(provision.name, func(t *testing.T) {
			f := newFixture(t)
			const racers = 8
			var wait sync.WaitGroup
			created := make([]bool, racers)
			failures := make([]error, racers)
			wait.Add(racers)
			for i := range racers {
				go func() {
					defer wait.Done()
					created[i], failures[i] = provision.bootstrap(t, f, "racer", "Racer")
				}()
			}
			wait.Wait()

			createdCount := 0
			for i := range racers {
				if failures[i] != nil {
					t.Fatalf("racer %d failed; matching creates must converge, not error: %v", i, failures[i])
				}
				if created[i] {
					createdCount++
				}
			}
			if createdCount != 1 {
				t.Errorf("%d racers reported Created, want exactly 1", createdCount)
			}
			// One row, whichever racer wrote it.
			if provision.identity(t, f, "racer") == uuid.Nil {
				t.Error("the racers did not converge on a readable row")
			}
		})
	}
}

// TestConcurrentBootstrapConflictIsTyped covers the other race: racers
// supplying DIFFERENT display data. Whoever commits first wins, and the
// losers must receive the typed conflict rather than a driver error.
func TestConcurrentBootstrapConflictIsTyped(t *testing.T) {
	for _, provision := range provisioners() {
		t.Run(provision.name, func(t *testing.T) {
			f := newFixture(t)
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
					_, failures[i] = provision.bootstrap(t, f, "contested", name)
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
		})
	}
}

// TestBootstrapUserIsTenantScoped: a handle is unique PER ORGANIZATION, not
// globally, so the same handle in two tenants is two users. The organization
// path has no equivalent case — a slug is global — which is one of the
// reasons the two are not interchangeable.
func TestBootstrapUserIsTenantScoped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	first, err := f.store.BootstrapUser(ctx, store.BootstrapUserInput{
		Handle: "dan", DisplayName: "Dan", OrganizationID: f.organizationID,
	})
	if err != nil {
		t.Fatalf("bootstrap in the first tenant: %v", err)
	}
	second, err := f.store.BootstrapUser(ctx, store.BootstrapUserInput{
		Handle: "dan", DisplayName: "Dan", OrganizationID: f.otherOrgID,
	})
	if err != nil {
		t.Fatalf("the same handle in another tenant must be allowed: %v", err)
	}
	if !second.Created {
		t.Error("the second tenant's user must be created, not resolved to the first's")
	}
	if second.Record.UserID == first.Record.UserID {
		t.Error("two tenants' users collapsed onto one row")
	}
}

// recordAttempt ledgers through WithTx, which is the only way to reach it.
//
// That is the contract made structural: RecordBenchmarkAttempt lives on Tx
// alone, so a caller CANNOT commit the ledger row apart from the Audit
// artifact it names. This helper is what the importer's own call looks like,
// minus the artifact write that shares the transaction.
func recordAttempt(t *testing.T, f *fixture, input store.RecordBenchmarkAttemptInput) (store.Bootstrapped[store.BenchmarkAttempt], error) {
	t.Helper()
	var outcome store.Bootstrapped[store.BenchmarkAttempt]
	err := f.store.WithTx(context.Background(), func(tx store.Tx) error {
		var txErr error
		outcome, txErr = tx.RecordBenchmarkAttempt(context.Background(), input)
		return txErr
	})
	return outcome, err
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

	first, err := recordAttempt(t, f, input)
	if err != nil {
		t.Fatalf("record attempt: %v", err)
	}
	if !first.Created {
		t.Error("the first record must report Created")
	}

	repeat, err := recordAttempt(t, f, input)
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
	_, err = recordAttempt(t, f, conflicting)
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
