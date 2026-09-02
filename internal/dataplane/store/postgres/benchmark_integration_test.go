//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
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
// provisioned is the neutral shape of what a provisioning call RETURNED.
//
// It carries the record, not just the flag, because the returned record is
// part of the contract: a method that reported Created correctly while
// returning a zero or unrelated record satisfied the earlier version of these
// tests, which read identity back from the database instead of looking at
// what the call handed them.
type provisioned struct {
	ID          uuid.UUID
	DisplayName string
	Created     bool
}

type provisioner struct {
	name string
	// bootstrap provisions with the given natural key and display name,
	// returning what the call itself produced.
	bootstrap func(t *testing.T, f *fixture, key, display string) (provisioned, error)
	// stored reads the row back, so the returned record can be compared
	// against what is actually persisted.
	stored func(t *testing.T, f *fixture, key string) provisioned
}

func provisioners() []provisioner {
	return []provisioner{
		{
			name: "organization",
			bootstrap: func(t *testing.T, f *fixture, key, display string) (provisioned, error) {
				t.Helper()
				out, err := f.store.BootstrapOrganization(context.Background(),
					store.BootstrapOrganizationInput{Slug: key, DisplayName: display})
				return provisioned{
					ID: out.Record.OrganizationID, DisplayName: out.Record.DisplayName, Created: out.Created,
				}, err
			},
			stored: func(t *testing.T, f *fixture, key string) provisioned {
				t.Helper()
				row, err := f.store.GetOrganizationBySlug(context.Background(), key)
				if err != nil {
					t.Fatalf("read organization %q: %v", key, err)
				}
				return provisioned{ID: row.OrganizationID, DisplayName: row.DisplayName}
			},
		},
		{
			name: "user",
			bootstrap: func(t *testing.T, f *fixture, key, display string) (provisioned, error) {
				t.Helper()
				out, err := f.store.BootstrapUser(context.Background(),
					store.BootstrapUserInput{
						Handle: key, DisplayName: display, OrganizationID: f.organizationID,
					})
				return provisioned{
					ID: out.Record.UserID, DisplayName: out.Record.DisplayName, Created: out.Created,
				}, err
			},
			stored: func(t *testing.T, f *fixture, key string) provisioned {
				t.Helper()
				row, err := f.store.GetUserByHandle(context.Background(), f.organizationID, key)
				if err != nil {
					t.Fatalf("read user %q: %v", key, err)
				}
				return provisioned{ID: row.UserID, DisplayName: row.DisplayName}
			},
		},
		{
			name: "product",
			bootstrap: func(t *testing.T, f *fixture, key, display string) (provisioned, error) {
				t.Helper()
				out, err := f.store.ProvisionProduct(context.Background(), store.ProvisionProductInput{
					Slug: key, DisplayName: display, OrganizationID: f.organizationID, UserID: f.userID,
				})
				return provisioned{ID: out.Record.ProductID, DisplayName: out.Record.DisplayName, Created: out.Created}, err
			},
			stored: func(t *testing.T, f *fixture, key string) provisioned {
				t.Helper()
				row, err := f.store.GetProductBySlug(context.Background(), f.organizationID, key)
				if err != nil {
					t.Fatalf("read product %q: %v", key, err)
				}
				return provisioned{ID: row.ProductID, DisplayName: row.DisplayName}
			},
		},
		{
			name: "repository",
			bootstrap: func(t *testing.T, f *fixture, key, display string) (provisioned, error) {
				t.Helper()
				product := provisionProduct(t, f, "primary-of-"+key)
				out, err := f.store.ProvisionRepository(context.Background(), store.ProvisionRepositoryInput{
					Slug: key, DisplayName: display, OrganizationID: f.organizationID,
					PrimaryProductID: product, UserID: f.userID,
				})
				return provisioned{ID: out.Record.RepositoryID, DisplayName: out.Record.DisplayName, Created: out.Created}, err
			},
			stored: func(t *testing.T, f *fixture, key string) provisioned {
				t.Helper()
				row, err := f.store.GetRepositoryBySlug(context.Background(), f.organizationID, key)
				if err != nil {
					t.Fatalf("read repository %q: %v", key, err)
				}
				return provisioned{ID: row.RepositoryID, DisplayName: row.DisplayName}
			},
		},
	}
}

// provisionProduct is the fixture's product, idempotent so a provisioner can
// call it on every bootstrap.
func provisionProduct(t *testing.T, f *fixture, slug string) uuid.UUID {
	t.Helper()
	out, err := f.store.ProvisionProduct(context.Background(), store.ProvisionProductInput{
		Slug: slug, DisplayName: slug, OrganizationID: f.organizationID, UserID: f.userID,
	})
	if err != nil {
		t.Fatalf("provision product %q: %v", slug, err)
	}
	return out.Record.ProductID
}

// TestBootstrapConflictSemantics drives all three outcomes of D10's table,
// for both provisioning paths, asserting the RETURNED record each time.
func TestBootstrapConflictSemantics(t *testing.T) {
	for _, provision := range provisioners() {
		t.Run(provision.name, func(t *testing.T) {
			f := newFixture(t)

			created, err := provision.bootstrap(t, f, "acme", "Acme Inc")
			if err != nil {
				t.Fatalf("first bootstrap: %v", err)
			}
			if !created.Created {
				t.Error("the first bootstrap must report Created")
			}
			// The call's OWN record, compared against what is persisted: a
			// correct flag beside a zero record is not a correct answer.
			stored := provision.stored(t, f, "acme")
			if created.ID != stored.ID || created.DisplayName != stored.DisplayName {
				t.Errorf("the returned record %+v is not the stored row %+v", created, stored)
			}

			// Matching display data: the no-op, distinguishable from creation,
			// and returning the SAME row rather than a second one.
			again, err := provision.bootstrap(t, f, "acme", "Acme Inc")
			if err != nil {
				t.Fatalf("matching re-bootstrap must be a no-op, got: %v", err)
			}
			if again.Created {
				t.Error("a re-bootstrap must report Created=false")
			}
			if again.ID != created.ID || again.DisplayName != created.DisplayName {
				t.Errorf("a re-bootstrap returned %+v, want the first call's %+v", again, created)
			}

			// Differing display data: a typed conflict, and nothing changes.
			if _, err := provision.bootstrap(t, f, "acme", "Acme Ltd"); !errors.Is(err, store.ErrBootstrapConflict) {
				t.Fatalf("differing display data must be a typed conflict, got: %v", err)
			}
			if after := provision.stored(t, f, "acme"); after.DisplayName != "Acme Inc" {
				t.Errorf("the refused bootstrap renamed the record to %q", after.DisplayName)
			}
		})
	}
}

// TestBootstrapRejectsMalformedInput asserts the validation happens before
// SQL, so an operator's mistake is reported in the vocabulary of the flag
// they typed rather than as a constraint name.
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
		})
	}
}

// TestIdentifiersAcceptTheRunnersOwnShape exercises the path the defect was
// ON.
//
// The bug was EnsureBenchmarkRun refusing suite ids the runner produces and
// migration 000017 admits — so controls routed through the bootstrap paths
// prove nothing about it: switching EnsureBenchmarkRun back to the stricter
// pattern restores the defect while they stay green. The accepted cases
// therefore go through EnsureBenchmarkRun itself, and the whole point is
// which values are ACCEPTED, since this defect refused valid input rather
// than admitting invalid input.
//
// D8 fixes the two shapes: an attempt's run id must be a single path
// component beginning with an alphanumeric, and a suite run id follows the
// runner's own rule, which permits a leading `_` or `-`.
func TestIdentifiersAcceptTheRunnersOwnShape(t *testing.T) {
	ctx := context.Background()
	for name, suiteRunID := range map[string]string{
		"leading underscore": "_golden_all",
		"leading hyphen":     "-golden-all",
		"underscores only":   "___",
		"ordinary":           "golden-all-2026-08-04",
		"digits first":       "2026-suite",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			run, err := f.store.EnsureBenchmarkRun(ctx, f.organizationID, suiteRunID)
			if err != nil {
				t.Fatalf("%q is a valid suite run id under the runner's own rule "+
					"(and migration 000017 accepts it): %v", suiteRunID, err)
			}
			if !run.Created || run.Record.SuiteRunID != suiteRunID {
				t.Errorf("EnsureBenchmarkRun returned %+v for %q", run.Record, suiteRunID)
			}
		})
	}

	// And the shapes that are refused, so the rule is not merely permissive.
	for name, suiteRunID := range map[string]string{
		"uppercase":      "Golden",
		"with separator": "golden/all",
		"dot":            "..",
		"blank":          "",
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.store.EnsureBenchmarkRun(ctx, f.organizationID, suiteRunID); err == nil {
				t.Fatalf("%q must be refused", suiteRunID)
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
// Created, EVERY caller must receive the stored row, and none may fail.
func TestConcurrentBootstrapConverges(t *testing.T) {
	for _, provision := range provisioners() {
		t.Run(provision.name, func(t *testing.T) {
			f := newFixture(t)
			const racers = 8
			var wait sync.WaitGroup
			results := make([]provisioned, racers)
			failures := make([]error, racers)
			wait.Add(racers)
			for i := range racers {
				go func() {
					defer wait.Done()
					results[i], failures[i] = provision.bootstrap(t, f, "racer", "Racer")
				}()
			}
			wait.Wait()

			stored := provision.stored(t, f, "racer")
			createdCount := 0
			for i := range racers {
				if failures[i] != nil {
					t.Fatalf("racer %d failed; matching creates must converge, not error: %v", i, failures[i])
				}
				if results[i].Created {
					createdCount++
				}
				// Every racer, not just the winner, must be handed the row
				// that is actually there.
				if results[i].ID != stored.ID || results[i].DisplayName != stored.DisplayName {
					t.Errorf("racer %d received %+v, want the stored row %+v", i, results[i], stored)
				}
			}
			if createdCount != 1 {
				t.Errorf("%d racers reported Created, want exactly 1", createdCount)
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
//
// Through the SEAM, which is what makes it evidence. An earlier version
// inserted the row with raw SQL because scopeColumns had no benchmark case
// and refused the scope outright — so the ledger tests were proving something
// about a hand-written INSERT, and the scope the whole family is built on had
// never been written by the code that writes it. Item 9 closes that case, and
// this asserts the round trip: a scope written wrong lands in the wrong column
// and comes back as the wrong type or a nil id.
func (f *fixture) newBenchmarkAuditArtifact(t *testing.T, runID uuid.UUID) uuid.UUID {
	t.Helper()
	artifact, err := f.store.CreateAuditArtifact(context.Background(), store.CreateAuditArtifactInput{
		Type:             "test_event",
		Summary:          "attempt",
		Payload:          json.RawMessage(`{"title":"attempt"}`),
		Scope:            store.Scope{Type: store.ScopeBenchmark, ID: runID},
		AuthorInstanceID: f.systemAgent,
		OrganizationID:   f.organizationID,
	})
	if err != nil {
		t.Fatalf("create benchmark-scoped audit artifact: %v", err)
	}
	if artifact.Scope.Type != store.ScopeBenchmark || artifact.Scope.ID != runID {
		t.Fatalf("artifact came back scoped to %s/%s, want benchmark/%s",
			artifact.Scope.Type, artifact.Scope.ID, runID)
	}
	stored, err := f.store.ListAuditArtifactsByScope(context.Background(), f.organizationID,
		store.Scope{Type: store.ScopeBenchmark, ID: runID})
	if err != nil {
		t.Fatalf("list by benchmark scope: %v", err)
	}
	// The list is what a benchmark scope is FOR: scope_id is a generated
	// column, and a benchmark row whose scope column went unwritten would be
	// invisible to exactly this query while still existing.
	var found bool
	for i := range stored {
		found = found || stored[i].ArtifactID == artifact.ArtifactID
	}
	if !found {
		t.Fatalf("artifact %s is not listed under its own benchmark scope", artifact.ArtifactID)
	}
	return artifact.ArtifactID
}

// newOrganization returns a second tenant's id for cross-tenant assertions.
func (f *fixture) newOrganization(t *testing.T) uuid.UUID {
	t.Helper()
	return f.otherOrgID
}
