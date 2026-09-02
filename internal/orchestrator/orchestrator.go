// Package orchestrator is the data plane's caller (Phase 3 item 3).
//
// It is provider-neutral by construction: it sees store.Store and the
// vocabulary packages beneath it, and nothing that knows where a plane
// lives. The composition root — cmd/dataplanectl today, the main binary
// after item 14 — hands it an Opener built from the local or cloud
// composer; this package never names either (design D2, D3). A guard over
// its transitive closure keeps that true.
//
// What it does in item 3: open the seam through the startup contract
// (design D5/D6), resolve the organization and operator it serves, and run
// the recovery projection (design D9) over the plane's open work. No agent
// runs; what is proved is that the Orchestrator's own workflow state
// survives a restart, because none of it lives anywhere but the plane.
package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/readiness"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/work"
)

// Opener opens the persistence seam. The composition root builds it; this
// package calls it once at Start and classifies its failure.
type Opener func(ctx context.Context) (store.Store, error)

// Config is what the composition root supplies: identities, never plane
// locations. Both are resolved against provisioned records; nothing is
// created at startup.
type Config struct {
	// OrganizationSlug names the tenant this Orchestrator serves.
	OrganizationSlug string
	// OperatorHandle names the accountable human whose actions the
	// Orchestrator performs on behalf of.
	OperatorHandle string
}

// Registry is the artifact vocabulary the Orchestrator declares: the work
// hierarchy's governing records and the Story completion.
func Registry() (*registry.Registry, error) {
	types, err := registry.New(work.RegistryEntries())
	if err != nil {
		return nil, fmt.Errorf("build the orchestrator's artifact registry: %w", err)
	}
	return types, nil
}

// Keys is the configuration vocabulary the Orchestrator declares. Empty in
// item 3, deliberately: no Orchestrator path here reads a configuration
// record, and a key registered without a reader is a guess about a future
// caller. Item 4 registers the pack selector, the first live reader
// (ADR 0031 §4; design D7).
func Keys() *configkeys.Registry {
	return configkeys.MustNew(nil)
}

// StartupRefused is a start that could not proceed because the plane is
// not ready. It renders the cause, what was observed, and what the operator
// does about it — the whole of what the startup contract owes.
type StartupRefused struct {
	Err    error
	Cause  readiness.Cause
	Detail string
	Remedy string
}

func (e *StartupRefused) Error() string {
	message := fmt.Sprintf("orchestrator cannot start: the data plane is not ready (%s).\n  observed: %s\n  remedy:   %s",
		e.Cause, e.Detail, e.Remedy)
	// The producer's own diagnostic is rendered, not only unwrap-able: for
	// an unreachable or unreadable plane it carries the endpoint, the driver
	// error or the version read that failed, which D5 owes the operator.
	if e.Err != nil {
		message += "\n  detail:   " + e.Err.Error()
	}
	return message
}

// Unwrap keeps the producer's chain reachable.
func (e *StartupRefused) Unwrap() error { return e.Err }

// ErrNotProvisioned reports an identity Start was configured with that the
// plane does not hold. Startup provisions nothing.
var ErrNotProvisioned = errors.New("not provisioned")

// Orchestrator is one running instance over one plane, serving one
// organization on behalf of one operator.
type Orchestrator struct {
	seam         store.Store
	projection   Projection
	organization store.Organization
	operator     store.User
}

// Start opens the seam, refuses a plane that is not ready, resolves the
// identities, and recovers the plane's open work.
//
// The order is the contract: readiness before identity, identity before
// recovery. A startup that resolved identities first would report "no such
// organization" against a plane that is merely stopped.
func Start(ctx context.Context, open Opener, cfg Config) (*Orchestrator, error) {
	if open == nil {
		return nil, errors.New("orchestrator: no opener was supplied")
	}
	seam, err := open(ctx)
	if err != nil {
		return nil, refuse(err)
	}
	o := &Orchestrator{seam: seam}
	if err := o.resolveIdentity(ctx, cfg); err != nil {
		seam.Close()
		return nil, err
	}
	if err := o.Recover(ctx); err != nil {
		seam.Close()
		return nil, err
	}
	return o, nil
}

// refuse turns an opener failure into the startup contract's refusal when
// it carries a readiness cause, and passes anything else through: a
// failure with no cause is a defect or an I/O error, not a plane state,
// and dressing it as one would send the operator to the wrong remedy.
func refuse(err error) error {
	var cause *readiness.Error
	if !errors.As(err, &cause) {
		return fmt.Errorf("orchestrator: open the data plane: %w", err)
	}
	return &StartupRefused{Err: err, Cause: cause.Cause, Detail: cause.Detail, Remedy: cause.Remedy}
}

func (o *Orchestrator) resolveIdentity(ctx context.Context, cfg Config) error {
	organization, err := o.seam.GetOrganizationBySlug(ctx, cfg.OrganizationSlug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: organization %q; run `dataplanectl provision organization` first",
				ErrNotProvisioned, cfg.OrganizationSlug)
		}
		return fmt.Errorf("resolve organization %q: %w", cfg.OrganizationSlug, err)
	}
	operator, err := o.seam.GetUserByHandle(ctx, organization.OrganizationID, cfg.OperatorHandle)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: user %q in organization %q; run `dataplanectl provision user` first",
				ErrNotProvisioned, cfg.OperatorHandle, cfg.OrganizationSlug)
		}
		return fmt.Errorf("resolve user %q: %w", cfg.OperatorHandle, err)
	}
	o.organization = *organization
	o.operator = *operator
	return nil
}

// Recover reads the plane's open work under one snapshot and classifies
// every row. It changes nothing: the plane wins, and every transition out
// of a diverged class is item 9's.
func (o *Orchestrator) Recover(ctx context.Context) error {
	open, err := o.seam.OpenWork(ctx, o.organization.OrganizationID)
	if err != nil {
		return fmt.Errorf("recover open work: %w", err)
	}
	projection, err := Project(open)
	if err != nil {
		return fmt.Errorf("recover open work: %w", err)
	}
	o.projection = projection
	return nil
}

// Projection is what the last Recover found.
func (o *Orchestrator) Projection() Projection { return o.projection }

// Organization is the tenant this Orchestrator serves.
func (o *Orchestrator) Organization() store.Organization { return o.organization }

// Operator is the accountable human.
func (o *Orchestrator) Operator() store.User { return o.operator }

// Store is the seam this Orchestrator routes through.
//
// Exposed to the composition root and the test harness, which drive the
// work hierarchy through it. It is never handed to an agent: ADR 0022's
// rule is that agents hold no connection and issue no queries, and the
// Orchestrator is the one caller that does.
func (o *Orchestrator) Store() store.Store { return o.seam }

// Dispatch issues a Story's dispatch, deriving its basis (design D10).
func (o *Orchestrator) Dispatch(ctx context.Context, storyID uuid.UUID) (*store.StoryDispatch, error) {
	dispatch, err := o.seam.CreateDispatch(ctx, o.organization.OrganizationID, storyID)
	if err != nil {
		return nil, fmt.Errorf("dispatch story %s: %w", storyID, err)
	}
	return dispatch, nil
}

// AcceptDispatch records a handshake accepted, creating the execution.
func (o *Orchestrator) AcceptDispatch(ctx context.Context, dispatchID uuid.UUID) (*store.Execution, error) {
	execution, err := o.seam.AcceptDispatch(ctx, o.organization.OrganizationID, dispatchID)
	if err != nil {
		return nil, fmt.Errorf("accept dispatch %s: %w", dispatchID, err)
	}
	return execution, nil
}

// Close closes the seam.
func (o *Orchestrator) Close() { o.seam.Close() }
