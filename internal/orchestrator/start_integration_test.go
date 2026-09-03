//go:build integration

package orchestrator_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/plane"
	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/readiness"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/orchestrator"
)

// opener builds a provider-neutral Opener over a disposable plane. It is
// the shape the composition root supplies, minus the local composer: the
// Orchestrator under test must not know how this was built.
func opener(t *testing.T, dsn string) orchestrator.Opener {
	t.Helper()
	blob, _ := planetest.Blob(t, "orch")
	types, err := orchestrator.Registry()
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context) (store.Store, error) {
		return plane.Open(ctx, plane.Composition{
			DSN: dsn, Objects: blob, RootKey: planetest.RootKey(t), Types: types, Keys: orchestrator.Keys(),
		})
	}
}

// TestStartRefusesANotReadyPlaneWithCauseAndRemedy: the startup contract
// end to end through Start, for the one state a neutral opener can reach.
// The local marker and key states are driven through stack.OpenSeam in
// the proofs commit, where the composition root exists.
func TestStartRefusesANotReadyPlaneWithCauseAndRemedy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	dsn := fmt.Sprintf("postgres://maestro:x@%s/maestro?sslmode=disable&connect_timeout=2", addr)

	_, err = orchestrator.Start(context.Background(), opener(t, dsn), orchestrator.Config{OrganizationSlug: "acme", OperatorHandle: "dan"})
	var refused *orchestrator.StartupRefused
	if !errors.As(err, &refused) {
		t.Fatalf("want a StartupRefused, got %v", err)
	}
	if refused.Cause != readiness.Unreachable {
		t.Fatalf("cause %s, want unreachable", refused.Cause)
	}
	if refused.Remedy == "" || !strings.Contains(err.Error(), "remedy:") {
		t.Fatalf("the refusal does not render a remedy: %v", err)
	}
	// The producer's diagnostic -- the endpoint and the driver's refusal --
	// is rendered, not only unwrap-able. THE MUTANT: drop Err from Error().
	for _, want := range []string{"detail:", addr, "connection refused"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rendering lacks %q:\n%s", want, err)
		}
	}
}

// TestStartResolvesIdentityBeforeRecovering: an unprovisioned organization
// is a typed refusal, and startup provisions nothing.
func TestStartRefusesAnUnprovisionedIdentity(t *testing.T) {
	dsn := planetest.DSN(t, "orchid")
	_, err := orchestrator.Start(context.Background(), opener(t, dsn), orchestrator.Config{OrganizationSlug: "ghost", OperatorHandle: "nobody"})
	if !errors.Is(err, orchestrator.ErrNotProvisioned) {
		t.Fatalf("want ErrNotProvisioned, got %v", err)
	}
	seam, err := opener(t, dsn)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer seam.Close()
	if _, err := seam.GetOrganizationBySlug(context.Background(), "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("startup provisioned the organization: %v", err)
	}
}

// TestStartRecoversAnEmptyPlane: a provisioned tenant with no open work
// starts, and the projection is empty with every class present at zero.
func TestStartRecoversAnEmptyPlane(t *testing.T) {
	dsn := planetest.DSN(t, "orchempty")
	ctx := context.Background()
	seam, err := opener(t, dsn)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	org, err := seam.BootstrapOrganization(ctx, store.BootstrapOrganizationInput{Slug: "acme", DisplayName: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seam.BootstrapUser(ctx, store.BootstrapUserInput{Handle: "dan", DisplayName: "Dan", OrganizationID: org.Record.OrganizationID}); err != nil {
		t.Fatal(err)
	}
	seam.Close()

	o, err := orchestrator.Start(ctx, opener(t, dsn), orchestrator.Config{OrganizationSlug: "acme", OperatorHandle: "dan"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer o.Close()
	if o.Organization().Slug != "acme" || o.Operator().Handle != "dan" {
		t.Fatalf("identity %+v / %+v", o.Organization(), o.Operator())
	}
	p := o.Projection()
	if len(p.Rows) != 0 {
		t.Fatalf("an empty plane projected %d rows", len(p.Rows))
	}
	for _, c := range orchestrator.Classes {
		if n := p.Counts[c]; n != 0 {
			t.Fatalf("class %s counts %d on an empty plane", c, n)
		}
	}
}
