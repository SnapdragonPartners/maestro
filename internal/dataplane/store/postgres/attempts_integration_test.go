//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
)

// The attempt and execution verbs on a real ephemeral plane (Phase 3 item
// 5 design, D4-D12; checkpoint 1). Each verb's contract is exercised at the
// seam, below the boundary that composes them: the boundary's own tests
// (checkpoint 3) drive these through Mediate, and a property that only the
// SQL provides -- exactly-one consumption, closure linearizing with
// registration, the live mutation key -- is proved here, where nothing
// above the seam can shield it (review round 22).

const (
	testFamily   = "test/noop"
	testTarget   = "t"
	requirements = `{"policy/operator_approval":{"question":"open a pull request?","permitted_scopes":["once"]}}`
)

var (
	requestDigest   = "1111111111111111111111111111111111111111111111111111111111111111"
	argumentsDigest = "2222222222222222222222222222222222222222222222222222222222222222"
	requirementHash = "3333333333333333333333333333333333333333333333333333333333333333"
)

// boundaryFixture is an accepted dispatch with its execution, a live
// principal under it, and an Orchestrator instance id to claim with.
type boundaryFixture struct {
	*fixture
	governed
	execution *store.Execution
	principal *store.PrincipalInstance
	instance  uuid.UUID
}

func newBoundaryFixture(t *testing.T) *boundaryFixture {
	t.Helper()
	f := newFixture(t)
	return f.boundaryFor(t, provisionGoverned(t, f), f.configured())
}

func (f *fixture) boundaryFor(t *testing.T, g governed, configuration store.ExecutionConfiguration) *boundaryFixture {
	t.Helper()
	ctx := context.Background()
	dispatch, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID, nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	execution, err := f.store.AcceptDispatch(ctx, f.organizationID, dispatch.StoryDispatchID, configuration)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	principal, err := f.store.CreateDispatchedPrincipalInstance(ctx, store.CreateDispatchedPrincipalInput{
		Model: "m", AgentType: "coder", ExecutionID: execution.ExecutionID, OrganizationID: f.organizationID,
	})
	if err != nil {
		t.Fatalf("live principal: %v", err)
	}
	return &boundaryFixture{fixture: f, governed: g, execution: execution, principal: principal, instance: v7(t)}
}

func v7(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (b *boundaryFixture) registration(id uuid.UUID, mutationKey *string) store.RegisterAttemptInput {
	return store.RegisterAttemptInput{
		Family: testFamily, ToolName: testFamily, RequestDigest: requestDigest, ArgumentsDigest: argumentsDigest,
		TargetKey: testTarget, MutationKey: mutationKey, Arguments: json.RawMessage(`{"title":"x"}`),
		ToolCallID: id, OrganizationID: b.organizationID, ExecutionID: b.execution.ExecutionID,
		PrincipalInstanceID: b.principal.PrincipalInstanceID, ClaimedBy: b.instance,
	}
}

func (b *boundaryFixture) register(t *testing.T, mutationKey *string) store.ToolCall {
	t.Helper()
	registered, err := b.store.RegisterAttempt(context.Background(), b.registration(v7(t), mutationKey))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !registered.Registered {
		t.Fatal("a fresh id was not registered")
	}
	return registered.Call
}

func (b *boundaryFixture) wait(t *testing.T) store.ToolCall {
	t.Helper()
	call := b.register(t, nil)
	if err := b.store.EnterOperatorWait(context.Background(), b.organizationID, call.ToolCallID, json.RawMessage(requirements), requirementHash); err != nil {
		t.Fatalf("enter wait: %v", err)
	}
	return b.get(t, call.ToolCallID)
}

func (b *boundaryFixture) get(t *testing.T, id uuid.UUID) store.ToolCall {
	t.Helper()
	call, err := b.store.GetToolCall(context.Background(), b.organizationID, id)
	if err != nil {
		t.Fatal(err)
	}
	return *call
}

func (b *boundaryFixture) settle(t *testing.T, id uuid.UUID, outcome store.ToolOutcome, disposition store.DrainDisposition, reason *store.ReasonCode) store.ToolCall {
	t.Helper()
	completed, err := b.store.SettleAttempt(context.Background(), store.SettleAttemptInput{
		Outcome: outcome, Disposition: &disposition, ReasonCode: reason,
		OrganizationID: b.organizationID, ToolCallID: id,
	})
	if err != nil {
		t.Fatalf("settle %s: %v", outcome, err)
	}
	if !completed.Recorded {
		t.Fatalf("settling %s was not recorded", id)
	}
	return completed.Call
}

func reason(code store.ReasonCode) *store.ReasonCode { return &code }

func assertAttemptRejected(t *testing.T, err error, want store.AttemptReason) {
	t.Helper()
	var rejection *store.AttemptRejected
	if !errors.As(err, &rejection) {
		t.Fatalf("want an AttemptRejected with reason %q, got: %v", want, err)
	}
	if rejection.Reason != want {
		t.Fatalf("reason %q, want %q: %v", rejection.Reason, want, err)
	}
	if !errors.Is(err, store.ErrAttemptRejected) {
		t.Fatal("the rejection does not match the sentinel")
	}
}

func assertExecutionRejected(t *testing.T, err error, want store.ExecutionReason) {
	t.Helper()
	var rejection *store.ExecutionRejected
	if !errors.As(err, &rejection) {
		t.Fatalf("want an ExecutionRejected with reason %q, got: %v", want, err)
	}
	if rejection.Reason != want {
		t.Fatalf("reason %q, want %q: %v", rejection.Reason, want, err)
	}
	if !errors.Is(err, store.ErrExecutionRejected) {
		t.Fatal("the rejection does not match the sentinel")
	}
}

// --- AcceptDispatch: the configuration ---------------------------------------

func TestAcceptDispatchStoresTheConfigurationCanonically(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	dispatchOf := func(t *testing.T, g governed) *store.StoryDispatch {
		t.Helper()
		d, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID, nil)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	t.Run("sorted and de-duplicated, with the acting user and headless", func(t *testing.T) {
		g := provisionGoverned(t, f)
		d := dispatchOf(t, g)
		execution, err := f.store.AcceptDispatch(ctx, f.organizationID, d.StoryDispatchID, store.ExecutionConfiguration{
			CapabilitySet: []string{"b", "a", "a"}, Headless: true, ActingUserID: f.userID,
		})
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
		if got := execution.CapabilitySet; len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Fatalf("stored capability set %v, want [a b]", got)
		}
		if !execution.Headless || execution.ActingUserID != f.userID || execution.Terminal != nil {
			t.Fatalf("execution %+v", execution)
		}
		read, err := f.store.GetExecution(ctx, f.organizationID, execution.ExecutionID)
		if err != nil || read.CapabilitySet[1] != "b" || !read.Headless {
			t.Fatalf("GetExecution: %+v %v", read, err)
		}
		if _, err := f.store.GetExecution(ctx, f.otherOrgID, execution.ExecutionID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("cross-tenant GetExecution = %v, want ErrNotFound", err)
		}
	})

	t.Run("a blank identity is refused and the dispatch stays pending", func(t *testing.T) {
		g := provisionGoverned(t, f)
		d := dispatchOf(t, g)
		_, err := f.store.AcceptDispatch(ctx, f.organizationID, d.StoryDispatchID, store.ExecutionConfiguration{
			CapabilitySet: []string{"a", " "}, ActingUserID: f.userID,
		})
		assertExecutionRejected(t, err, store.ReasonCapabilityBlank)
		after, err := f.store.GetDispatch(ctx, f.organizationID, d.StoryDispatchID)
		if err != nil || after.Disposition != store.DispositionPending {
			t.Fatalf("after a refused configuration the dispatch is %v (%v); it must still be pending", after.Disposition, err)
		}
	})

	t.Run("no acting user is refused; another organization's user is refused by the key", func(t *testing.T) {
		g := provisionGoverned(t, f)
		d := dispatchOf(t, g)
		if _, err := f.store.AcceptDispatch(ctx, f.organizationID, d.StoryDispatchID, store.ExecutionConfiguration{}); err == nil {
			t.Fatal("an execution with no acting user was accepted")
		}
		stranger := f.otherUser(t)
		if _, err := f.store.AcceptDispatch(ctx, f.organizationID, d.StoryDispatchID, store.ExecutionConfiguration{
			CapabilitySet: []string{}, ActingUserID: stranger,
		}); err == nil {
			t.Fatal("an execution bound another organization's member as its acting user")
		}
		after, err := f.store.GetDispatch(ctx, f.organizationID, d.StoryDispatchID)
		if err != nil || after.Disposition != store.DispositionPending {
			t.Fatalf("the dispatch is %v (%v), want pending after both refusals", after.Disposition, err)
		}
	})
}

// otherUser is a member of the OTHER organization.
func (f *fixture) otherUser(t *testing.T) uuid.UUID {
	t.Helper()
	user, err := f.store.BootstrapUser(context.Background(), store.BootstrapUserInput{
		Handle: "stranger", DisplayName: "Stranger", OrganizationID: f.otherOrgID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return user.Record.UserID
}

// --- registration and denial -------------------------------------------------

func TestRegisterAttemptIsIdempotentByIdAndBoundToItsExecution(t *testing.T) {
	ctx := context.Background()
	b := newBoundaryFixture(t)

	id := v7(t)
	first, err := b.store.RegisterAttempt(ctx, b.registration(id, nil))
	if err != nil || !first.Registered {
		t.Fatalf("first registration: %+v %v", first, err)
	}
	call := first.Call
	switch {
	case call.State != store.AttemptOpen, call.ExecutionID == nil || *call.ExecutionID != b.execution.ExecutionID:
		t.Fatalf("registered row %+v", call)
	case call.ClaimedBy == nil || *call.ClaimedBy != b.instance:
		t.Fatalf("claim %v, want %s", call.ClaimedBy, b.instance)
	case call.Family == nil || *call.Family != testFamily || call.RequestDigest == nil || *call.RequestDigest != requestDigest:
		t.Fatalf("identity %+v", call)
	case call.UserID == nil || *call.UserID != b.execution.ActingUserID:
		t.Fatalf("accountable user %v, want the execution's acting user %s", call.UserID, b.execution.ActingUserID)
	case call.Lineage.StoryID == nil || *call.Lineage.StoryID != b.execution.StoryID:
		t.Fatalf("lineage %+v, want the execution's", call.Lineage)
	case call.MutationKey != nil || call.DrainDisposition != nil || call.RevalidatedAt != nil:
		t.Fatalf("a fresh non-mutating attempt carries %+v", call)
	}

	// A transport retry with the same id: the row, not a second attempt.
	second, err := b.store.RegisterAttempt(ctx, b.registration(id, nil))
	if err != nil || second.Registered || second.Call.ToolCallID != id {
		t.Fatalf("re-presentation: %+v %v; want the existing row with Registered=false", second, err)
	}
	// The same id under a different logical action is a CORRELATION
	// MISMATCH (D5), refused and not replayed -- for the execution, the
	// family and the request digest, through both verbs (PR #383 review).
	other := b.boundaryFor(t, provisionGoverned(t, b.fixture), b.configured())
	for _, tc := range []struct {
		because string
		mutate  func(in *store.RegisterAttemptInput)
	}{
		{"another execution", func(in *store.RegisterAttemptInput) {
			in.ExecutionID, in.PrincipalInstanceID = other.execution.ExecutionID, other.principal.PrincipalInstanceID
		}},
		{"another family", func(in *store.RegisterAttemptInput) { in.Family, in.ToolName = "other/family", "other/family" }},
		{"another request digest", func(in *store.RegisterAttemptInput) { in.RequestDigest = requirementHash }},
	} {
		input := b.registration(id, nil)
		tc.mutate(&input)
		if _, err := b.store.RegisterAttempt(ctx, input); !errors.Is(err, store.ErrCorrelationMismatch) {
			t.Errorf("re-presenting %s under %s = %v, want ErrCorrelationMismatch", id, tc.because, err)
		}
		denial := store.RecordDeniedAttemptInput{
			Family: input.Family, ToolName: input.ToolName, RequestDigest: input.RequestDigest, ArgumentsDigest: input.ArgumentsDigest,
			TargetKey: input.TargetKey, ReasonCode: "authority/superseded", ToolCallID: id,
			OrganizationID: input.OrganizationID, ExecutionID: input.ExecutionID, PrincipalInstanceID: input.PrincipalInstanceID,
		}
		if _, err := b.store.RecordDeniedAttempt(ctx, denial); !errors.Is(err, store.ErrCorrelationMismatch) {
			t.Errorf("recording a denial for %s under %s = %v, want ErrCorrelationMismatch", id, tc.because, err)
		}
	}
	// A different arguments digest alone is NOT a mismatch: the correlation
	// key is the request digest, so a secret rotation between two
	// presentations stays a replay (D5).
	rotated := b.registration(id, nil)
	rotated.ArgumentsDigest = requirementHash
	if replay, err := b.store.RegisterAttempt(ctx, rotated); err != nil || replay.Registered {
		t.Fatalf("re-presentation with a rotated arguments digest: %+v %v; want the existing row", replay, err)
	}
	// An id taken by another organization (same database, other tenant): a
	// mismatch that says only that the id is taken. Planted directly, since
	// no seam verb of this fixture writes under the other organization.
	taken := v7(t)
	if _, err := b.pool.Exec(ctx, `INSERT INTO tool_calls (tool_call_id, organization_id, principal_instance_id, tool_name, arguments)
	    VALUES ($1, $2, $3, 't', '{}'::jsonb)`, taken, b.otherOrgID, b.otherAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err := b.store.RegisterAttempt(ctx, b.registration(taken, nil)); !errors.Is(err, store.ErrCorrelationMismatch) {
		t.Fatalf("an id taken by another organization = %v, want ErrCorrelationMismatch", err)
	}
	if _, err := b.store.GetToolCall(ctx, b.organizationID, taken); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the taken id became readable in this organization: %v", err)
	}

	// Identity rules the seam refuses before the row does.
	for _, bad := range []struct {
		because string
		mutate  func(in *store.RegisterAttemptInput)
	}{
		{"a v4 id", func(in *store.RegisterAttemptInput) { in.ToolCallID = uuid.New() }},
		{"a family that is not <kind>/<verb>", func(in *store.RegisterAttemptInput) { in.Family = "noop" }},
		{"a malformed digest", func(in *store.RegisterAttemptInput) { in.ArgumentsDigest = "abc" }},
		{"a blank target", func(in *store.RegisterAttemptInput) { in.TargetKey = " " }},
		{"no claim", func(in *store.RegisterAttemptInput) { in.ClaimedBy = uuid.Nil }},
		{"a blank mutation key", func(in *store.RegisterAttemptInput) { blank := ""; in.MutationKey = &blank }},
	} {
		input := b.registration(v7(t), nil)
		bad.mutate(&input)
		if _, err := b.store.RegisterAttempt(ctx, input); err == nil {
			t.Errorf("%s was registered", bad.because)
		}
	}
	if _, err := b.store.RegisterAttempt(ctx, b.registration(v7(t), nil)); err != nil {
		t.Fatalf("the positive control registration failed: %v", err)
	}
}

// A denial after closure is still recorded (D4, D9); registration is not.
func TestClosureRefusesRegistrationAndStillRecordsADenial(t *testing.T) {
	ctx := context.Background()
	b := newBoundaryFixture(t)
	before := b.register(t, nil)
	if err := b.store.CloseAdmission(ctx, b.organizationID, b.execution.ExecutionID); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := b.store.CloseAdmission(ctx, b.organizationID, b.execution.ExecutionID); err != nil {
		t.Fatalf("a second closure is not idempotent: %v", err)
	}
	if _, err := b.store.RegisterAttempt(ctx, b.registration(v7(t), nil)); !errors.Is(err, store.ErrAdmissionClosed) {
		t.Fatalf("registration after closure = %v, want ErrAdmissionClosed", err)
	}
	// An id registered BEFORE closure is classified after it, not refused:
	// a transport retry receives its row (D5; PR #383 review). A mismatch
	// on that id is still a mismatch, not a closure refusal.
	if retry, err := b.store.RegisterAttempt(ctx, b.registration(before.ToolCallID, nil)); err != nil || retry.Registered || retry.Call.ToolCallID != before.ToolCallID {
		t.Fatalf("re-presenting a registered id after closure: %+v %v; want the existing row with Registered=false", retry, err)
	}
	mismatch := b.registration(before.ToolCallID, nil)
	mismatch.Family, mismatch.ToolName = "other/family", "other/family"
	if _, err := b.store.RegisterAttempt(ctx, mismatch); !errors.Is(err, store.ErrCorrelationMismatch) {
		t.Fatalf("a mismatched id after closure = %v, want ErrCorrelationMismatch", err)
	}

	id := v7(t)
	denied, err := b.store.RecordDeniedAttempt(ctx, store.RecordDeniedAttemptInput{
		Family: testFamily, ToolName: testFamily, RequestDigest: requestDigest, ArgumentsDigest: argumentsDigest,
		TargetKey: testTarget, ReasonCode: "authority/admission_closed", Arguments: json.RawMessage(`{}`),
		ToolCallID: id, OrganizationID: b.organizationID, ExecutionID: b.execution.ExecutionID,
		PrincipalInstanceID: b.principal.PrincipalInstanceID,
	})
	if err != nil || !denied.Registered {
		t.Fatalf("denial after closure: %+v %v", denied, err)
	}
	row := denied.Call
	switch {
	case row.State != store.AttemptSettled, row.Outcome == nil || *row.Outcome != store.ToolOutcomeDenied:
		t.Fatalf("denied row %+v", row)
	case row.ReasonCode == nil || *row.ReasonCode != "authority/admission_closed":
		t.Fatalf("reason %v", row.ReasonCode)
	case row.DrainDisposition == nil || *row.DrainDisposition != store.DrainStoppedBeforeCommit:
		t.Fatalf("disposition %v", row.DrainDisposition)
	case row.ClaimedBy != nil, row.FinishedAt == nil:
		t.Fatalf("a settled denial is claimed or unfinished: %+v", row)
	}
	// Idempotent by id, like registration.
	again, err := b.store.RecordDeniedAttempt(ctx, store.RecordDeniedAttemptInput{
		Family: testFamily, ToolName: testFamily, RequestDigest: requestDigest, ArgumentsDigest: argumentsDigest,
		TargetKey: testTarget, ReasonCode: "authority/admission_closed", ToolCallID: id,
		OrganizationID: b.organizationID, ExecutionID: b.execution.ExecutionID, PrincipalInstanceID: b.principal.PrincipalInstanceID,
	})
	if err != nil || again.Registered {
		t.Fatalf("a repeated denial: %+v %v", again, err)
	}
}

// Registration linearizes with closure (D9): a registration holding the
// share lock makes closure WAIT, and once closure lands no registration
// passes. The barrier is the uncommitted transaction itself.
func TestRegistrationLinearizesWithClosure(t *testing.T) {
	ctx := context.Background()
	b := newBoundaryFixture(t)

	registering := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	// Released on the way out whatever the verdict: a fatal above a held
	// transaction would otherwise leave the fixture's cleanup waiting on it.
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var inFlight store.ToolCall
	var registerErr error
	go func() {
		registerErr = b.store.WithTx(ctx, func(tx store.Tx) error {
			registered, err := tx.RegisterAttempt(ctx, b.registration(v7(t), nil))
			if err != nil {
				return err
			}
			inFlight = registered.Call
			close(registering)
			<-release
			return nil
		})
	}()
	<-registering

	closed := make(chan error, 1)
	go func() { closed <- b.store.CloseAdmission(ctx, b.organizationID, b.execution.ExecutionID) }()
	select {
	case err := <-closed:
		t.Fatalf("closure completed (%v) while a registration held the execution's share lock; it did not wait", err)
	case <-time.After(750 * time.Millisecond):
		// Blocked, as the lock order requires.
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-closed; err != nil {
		t.Fatalf("closure after the registration committed: %v", err)
	}
	if registerErr != nil {
		t.Fatalf("the registration that began before closure failed: %v", registerErr)
	}

	// The attempt registered before closure is one closure must settle: it
	// is in the drain list. A registration after closure is refused.
	open, err := b.store.ListExecutionAttempts(ctx, b.organizationID, b.execution.ExecutionID)
	if err != nil || len(open) != 1 || open[0].ToolCallID != inFlight.ToolCallID || open[0].State != store.AttemptOpen {
		t.Fatalf("attempts after closure: %+v %v; want the one registered before it, open", open, err)
	}
	if _, err := b.store.RegisterAttempt(ctx, b.registration(v7(t), nil)); !errors.Is(err, store.ErrAdmissionClosed) {
		t.Fatalf("registration after closure = %v, want ErrAdmissionClosed", err)
	}

	// THE ORDER THAT SEES THE SHARE LOCK. The half above is also satisfied
	// by the foreign key's implicit KEY SHARE on the execution row, which
	// the insert takes whether the seam locks or not (the design's mutant
	// survived it). Here closure holds FOR UPDATE uncommitted while a
	// registration begins: with FOR SHARE the registration waits, then
	// reads the row closure wrote and is refused; WITHOUT it the
	// registration reads the pre-closure snapshot, inserts behind the
	// committed closure, and an attempt registers after admission closed.
	b2 := newBoundaryFixture(t)
	closing := make(chan struct{})
	commit := make(chan struct{})
	var commitOnce sync.Once
	t.Cleanup(func() { commitOnce.Do(func() { close(commit) }) })
	go func() {
		_ = b2.store.WithTx(ctx, func(tx store.Tx) error {
			if err := tx.CloseAdmission(ctx, b2.organizationID, b2.execution.ExecutionID); err != nil {
				return err
			}
			close(closing)
			<-commit
			return nil
		})
	}()
	<-closing
	late := make(chan error, 1)
	go func() {
		_, err := b2.store.RegisterAttempt(ctx, b2.registration(v7(t), nil))
		late <- err
	}()
	select {
	case err := <-late:
		t.Fatalf("a registration completed (%v) while closure held the execution row; it did not wait", err)
	case <-time.After(750 * time.Millisecond):
	}
	commitOnce.Do(func() { close(commit) })
	if err := <-late; !errors.Is(err, store.ErrAdmissionClosed) {
		t.Fatalf("a registration that began before closure committed = %v, want ErrAdmissionClosed: it "+
			"registered behind the closure", err)
	}
	if attempts, err := b2.store.ListExecutionAttempts(ctx, b2.organizationID, b2.execution.ExecutionID); err != nil || len(attempts) != 0 {
		t.Fatalf("attempts after a refused registration: %+v %v; want none", attempts, err)
	}
}

// One live attempt per mutated resource, held through unresolved drainage
// (D12), at the seam: the same key is refused while the first is open and
// while it is settled unresolved, and admitted once drainage resolves; a
// second FAMILY on the key is refused just the same; NULL keys never
// collide.
func TestOneLiveAttemptPerMutatedResourceAtTheSeam(t *testing.T) {
	ctx := context.Background()
	b := newBoundaryFixture(t)
	key := "forge:" + b.repository.String() + "/maestro/story/s/maestro/epic/e"

	first := b.register(t, &key)
	if _, err := b.store.RegisterAttempt(ctx, b.registration(v7(t), &key)); !errors.Is(err, store.ErrTargetBusy) {
		t.Fatalf("second attempt on a busy resource = %v, want ErrTargetBusy", err)
	}
	other := b.registration(v7(t), &key)
	other.Family, other.ToolName = "other/family", "other/family"
	if _, err := b.store.RegisterAttempt(ctx, other); !errors.Is(err, store.ErrTargetBusy) {
		t.Fatalf("a second family on a busy resource = %v, want ErrTargetBusy", err)
	}

	b.settle(t, first.ToolCallID, store.ToolOutcomeUnknown, store.DrainUnresolved, reason("attempt/interrupted"))
	if _, err := b.store.RegisterAttempt(ctx, b.registration(v7(t), &key)); !errors.Is(err, store.ErrTargetBusy) {
		t.Fatalf("an attempt on a resource with unresolved drainage = %v, want ErrTargetBusy", err)
	}

	if err := b.store.ResolveDrainDisposition(ctx, b.organizationID, first.ToolCallID, store.DrainCommitted); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := b.store.RegisterAttempt(ctx, b.registration(v7(t), &key)); err != nil {
		t.Fatalf("an attempt after drainage resolved: %v", err)
	}
	// And the disposition moved once; it does not move again.
	err := b.store.ResolveDrainDisposition(ctx, b.organizationID, first.ToolCallID, store.DrainStoppedBeforeCommit)
	assertAttemptRejected(t, err, store.ReasonDrainNotUnresolved)
	err = b.store.ResolveDrainDisposition(ctx, b.organizationID, first.ToolCallID, store.DrainUnresolved)
	assertAttemptRejected(t, err, store.ReasonDrainNotUnresolved)

	b.register(t, nil)
	b.register(t, nil)
}

// --- the operator wait, the decision, its consumption -----------------------

func TestOperatorWaitDecisionAndConsumption(t *testing.T) {
	ctx := context.Background()
	b := newBoundaryFixture(t)

	waiting := b.wait(t)
	if waiting.State != store.AttemptOperatorWaiting || waiting.RequirementSetDigest == nil || *waiting.RequirementSetDigest != requirementHash {
		t.Fatalf("after EnterOperatorWait: %+v", waiting)
	}
	// The Story-scoped guard's read sees it, and the lock it takes is the
	// Story row's: a second reader on another connection blocks.
	held, err := b.store.StoryWaitingAttempts(ctx, b.organizationID, b.story.StoryID)
	if err != nil || len(held) != 1 || held[0].ToolCallID != waiting.ToolCallID {
		t.Fatalf("StoryWaitingAttempts: %+v %v", held, err)
	}
	b.assertStoryLockIsExclusive(t)

	// A decision needs a member; a second decision is refused; approval
	// leaves the row waiting with the decision on it.
	if _, err := b.store.RecordOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, store.DecisionApproveOnce, uuid.Nil); err == nil {
		t.Fatal("a decision with no decider was recorded")
	}
	approved, err := b.store.RecordOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, store.DecisionApproveOnce, b.userID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	switch {
	case approved.State != store.AttemptOperatorWaiting:
		t.Fatalf("an approved attempt left the wait: %s", approved.State)
	case approved.OperatorDecision == nil || approved.OperatorDecision.Decision != store.DecisionApproveOnce ||
		approved.OperatorDecision.DecidedBy != b.userID || approved.OperatorDecision.ConsumedAt != nil:
		t.Fatalf("decision %+v", approved.OperatorDecision)
	}
	_, err = b.store.RecordOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, store.DecisionDenyOnce, b.userID)
	assertAttemptRejected(t, err, store.ReasonDecisionAlreadyRecorded)
	// Still holding the Story guard: approval does not release it (D4).
	if held, err := b.store.StoryWaitingAttempts(ctx, b.organizationID, b.story.StoryID); err != nil || len(held) != 1 {
		t.Fatalf("an approved attempt released the Story guard: %+v %v", held, err)
	}

	// Consumption: once, transferring the claim and recording revalidation.
	consumer := v7(t)
	consumed, err := b.store.ConsumeOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, consumer)
	if err != nil || !consumed.Consumed {
		t.Fatalf("consume: %+v %v", consumed, err)
	}
	row := consumed.Call
	switch {
	case row.State != store.AttemptOpen:
		t.Fatalf("consumed row is %s, want open", row.State)
	case row.OperatorDecision.ConsumedAt == nil || row.OperatorDecision.ConsumedBy == nil || *row.OperatorDecision.ConsumedBy != row.ToolCallID:
		t.Fatalf("consumption %+v", row.OperatorDecision)
	case row.ClaimedBy == nil || *row.ClaimedBy != consumer:
		t.Fatalf("claim %v did not transfer to the consumer %s", row.ClaimedBy, consumer)
	case row.RevalidatedAt == nil:
		t.Fatal("consumption did not record revalidation")
	}
	again, err := b.store.ConsumeOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, consumer)
	if err != nil || again.Consumed {
		t.Fatalf("a second consumption: %+v %v; want Consumed=false", again, err)
	}
	// Release follows consumption (D4).
	if held, err := b.store.StoryWaitingAttempts(ctx, b.organizationID, b.story.StoryID); err != nil || len(held) != 0 {
		t.Fatalf("after consumption the Story guard still holds: %+v %v", held, err)
	}

	// The deny path settles in one statement.
	denied := b.wait(t)
	settled, err := b.store.RecordOperatorDecision(ctx, b.organizationID, denied.ToolCallID, store.DecisionDenyOnce, b.userID)
	if err != nil {
		t.Fatalf("deny: %v", err)
	}
	switch {
	case settled.State != store.AttemptSettled, settled.Outcome == nil || *settled.Outcome != store.ToolOutcomeDenied:
		t.Fatalf("denied row %+v", settled)
	case settled.ReasonCode == nil || *settled.ReasonCode != store.ReasonOperatorDenied:
		t.Fatalf("reason %v", settled.ReasonCode)
	case settled.OperatorDecision == nil || settled.OperatorDecision.Decision != store.DecisionDenyOnce:
		t.Fatalf("decision %+v", settled.OperatorDecision)
	case settled.DrainDisposition == nil || *settled.DrainDisposition != store.DrainStoppedBeforeCommit || settled.ClaimedBy != nil:
		t.Fatalf("drainage/claim %+v", settled)
	}
	// A wait entered on a row that is not open is refused.
	err = b.store.EnterOperatorWait(ctx, b.organizationID, denied.ToolCallID, json.RawMessage(requirements), requirementHash)
	assertAttemptRejected(t, err, store.ReasonAttemptWrongState)
	// And a wait with nothing to wait on is refused before SQL.
	fresh := b.register(t, nil)
	if err := b.store.EnterOperatorWait(ctx, b.organizationID, fresh.ToolCallID, json.RawMessage(`{}`), requirementHash); err == nil {
		t.Fatal("an empty requirement set entered a wait")
	}
}

// assertStoryLockIsExclusive shows StoryWaitingAttempts takes FOR UPDATE:
// while one transaction holds it, a second on another connection blocks.
func (b *boundaryFixture) assertStoryLockIsExclusive(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	holding := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	go func() {
		_ = b.store.WithTx(ctx, func(tx store.Tx) error {
			if _, err := tx.StoryWaitingAttempts(ctx, b.organizationID, b.story.StoryID); err != nil {
				return err
			}
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding
	second := make(chan error, 1)
	go func() {
		_, err := b.store.StoryWaitingAttempts(ctx, b.organizationID, b.story.StoryID)
		second <- err
	}()
	select {
	case err := <-second:
		releaseOnce.Do(func() { close(release) })
		t.Fatalf("a second Story-guard read completed (%v) while the first held the row; the lock is not exclusive", err)
	case <-time.After(500 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-second; err != nil {
		t.Fatalf("the second read after release: %v", err)
	}
}

// Conditional consumption admits one consumer, AT THE SEAM (D7, D12; review
// round 22): two connections consume one approved row through a barrier;
// exactly one reports consumed and the row carries one consumption. The
// design's mutant removes both WHERE predicates from the statement, so both
// report consumed -- which the count below is the assertion for.
func TestConcurrentConsumptionAdmitsOneConsumer(t *testing.T) {
	ctx := context.Background()
	b := newBoundaryFixture(t)
	waiting := b.wait(t)
	if _, err := b.store.RecordOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, store.DecisionApproveOnce, b.userID); err != nil {
		t.Fatal(err)
	}

	const racers = 4
	var start sync.WaitGroup
	start.Add(racers)
	results := make(chan store.Consumption, racers)
	failures := make(chan error, racers)
	for i := 0; i < racers; i++ {
		go func() {
			instance := v7(t)
			start.Done()
			start.Wait()
			consumed, err := b.store.ConsumeOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, instance)
			if err != nil {
				failures <- err
				return
			}
			results <- consumed
		}()
	}
	consumers := 0
	for i := 0; i < racers; i++ {
		select {
		case err := <-failures:
			t.Fatalf("a racer failed: %v", err)
		case consumed := <-results:
			if consumed.Consumed {
				consumers++
			}
		}
	}
	if consumers != 1 {
		t.Fatalf("%d racers consumed the decision, want exactly 1", consumers)
	}
	var consumptions int
	if err := b.pool.QueryRow(ctx, `SELECT count(*) FROM tool_calls WHERE tool_call_id=$1 AND operator_decision_consumed_at IS NOT NULL
	    AND operator_decision_consumed_by = tool_call_id AND state='open'`, waiting.ToolCallID).Scan(&consumptions); err != nil {
		t.Fatal(err)
	}
	if consumptions != 1 {
		t.Fatalf("the row carries %d consumption(s), want 1", consumptions)
	}
}

// A re-request inherits a stale attempt's unconsumed approval, once (D5).
func TestInheritOperatorDecisionOnce(t *testing.T) {
	ctx := context.Background()
	b := newBoundaryFixture(t)
	waiting := b.wait(t)
	if _, err := b.store.RecordOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, store.DecisionApproveOnce, b.userID); err != nil {
		t.Fatal(err)
	}
	// Interrupted by a restart: a foreign instance's Recover stales it.
	if err := b.store.StaleInterruptedWait(ctx, b.organizationID, waiting.ToolCallID, b.instance); err != nil {
		t.Fatalf("stale: %v", err)
	}
	stale := b.get(t, waiting.ToolCallID)
	switch {
	case stale.Outcome == nil || *stale.Outcome != store.ToolOutcomeStale, stale.ReasonCode == nil || *stale.ReasonCode != store.ReasonStaleInterruptedWait:
		t.Fatalf("stale row %+v", stale)
	case stale.OperatorDecision == nil || stale.OperatorDecision.Decision != store.DecisionApproveOnce || stale.OperatorDecision.ConsumedAt != nil:
		t.Fatalf("the decision was not preserved: %+v", stale.OperatorDecision)
	case stale.RequirementSet == nil, stale.DrainDisposition == nil || *stale.DrainDisposition != store.DrainStoppedBeforeCommit:
		t.Fatalf("requirement/drainage %+v", stale)
	}

	found, err := b.store.FindInheritableDecision(ctx, b.organizationID, b.execution.ExecutionID, testFamily, argumentsDigest, testTarget)
	if err != nil || found.ToolCallID != waiting.ToolCallID {
		t.Fatalf("FindInheritableDecision: %+v %v", found, err)
	}
	if _, err := b.store.FindInheritableDecision(ctx, b.organizationID, b.execution.ExecutionID, testFamily, argumentsDigest, "other-target"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a different target found an inheritable decision: %v", err)
	}
	// Only THE logical action inherits (PR #383 review): an attempt of
	// another execution, another target, or presented under a changed
	// requirement set is refused, and the approval stays unconsumed.
	other := b.boundaryFor(t, provisionGoverned(t, b.fixture), b.configured())
	foreign := other.register(t, nil)
	err = b.store.InheritOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, foreign.ToolCallID, requirementHash)
	assertAttemptRejected(t, err, store.ReasonDecisionNotInheritable)
	elsewhere := b.registration(v7(t), nil)
	elsewhere.TargetKey = "other-target"
	if _, err := b.store.RegisterAttempt(ctx, elsewhere); err != nil {
		t.Fatal(err)
	}
	err = b.store.InheritOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, elsewhere.ToolCallID, requirementHash)
	assertAttemptRejected(t, err, store.ReasonDecisionNotInheritable)
	successor := b.register(t, nil)
	err = b.store.InheritOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, successor.ToolCallID, requestDigest)
	assertAttemptRejected(t, err, store.ReasonDecisionNotInheritable)
	if row := b.get(t, waiting.ToolCallID); row.OperatorDecision.ConsumedAt != nil {
		t.Fatal("a refused inheritance consumed the approval")
	}
	if err := b.store.InheritOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, successor.ToolCallID, requirementHash); err != nil {
		t.Fatalf("inherit: %v", err)
	}
	inherited := b.get(t, waiting.ToolCallID)
	if inherited.OperatorDecision.ConsumedBy == nil || *inherited.OperatorDecision.ConsumedBy != successor.ToolCallID {
		t.Fatalf("consumed_by %v, want the successor %s", inherited.OperatorDecision.ConsumedBy, successor.ToolCallID)
	}
	if _, err := b.store.FindInheritableDecision(ctx, b.organizationID, b.execution.ExecutionID, testFamily, argumentsDigest, testTarget); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a consumed decision is still inheritable: %v", err)
	}
	third := b.register(t, nil)
	err = b.store.InheritOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, third.ToolCallID, requirementHash)
	assertAttemptRejected(t, err, store.ReasonDecisionNotInheritable)
}

// --- settlement ----------------------------------------------------------------

func TestSettleAttemptEnforcesTheReasonCodeAndDispositionRules(t *testing.T) {
	ctx := context.Background()
	b := newBoundaryFixture(t)
	fresh := func() uuid.UUID { return b.register(t, nil).ToolCallID }
	settle := func(id uuid.UUID, in store.SettleAttemptInput) error {
		in.OrganizationID, in.ToolCallID = b.organizationID, id
		_, err := b.store.SettleAttempt(ctx, in)
		return err
	}
	stopped, committed, unresolved := store.DrainStoppedBeforeCommit, store.DrainCommitted, store.DrainUnresolved
	digest := requirementHash

	assertAttemptRejected(t, settle(fresh(), store.SettleAttemptInput{Outcome: store.ToolOutcomeDenied, Disposition: &stopped}), store.ReasonReasonCodeRequired)
	assertAttemptRejected(t, settle(fresh(), store.SettleAttemptInput{Outcome: store.ToolOutcomeSucceeded, Disposition: &committed, ReasonCode: reason("x/y")}), store.ReasonReasonCodeForbidden)
	assertAttemptRejected(t, settle(fresh(), store.SettleAttemptInput{Outcome: store.ToolOutcomeBlocked, Disposition: &stopped}), store.ReasonRequirementSetRequired)
	assertAttemptRejected(t, settle(fresh(), store.SettleAttemptInput{Outcome: store.ToolOutcomeSucceeded}), store.ReasonDispositionRequired)
	assertAttemptRejected(t, settle(fresh(), store.SettleAttemptInput{Outcome: store.ToolOutcomeDenied, Disposition: &committed, ReasonCode: reason("x/y")}), store.ReasonDispositionMismatch)
	assertAttemptRejected(t, settle(fresh(), store.SettleAttemptInput{Outcome: store.ToolOutcomeSucceeded, Disposition: &unresolved}), store.ReasonDispositionMismatch)
	if err := settle(fresh(), store.SettleAttemptInput{Outcome: store.ToolOutcomeDenied, Disposition: &stopped, ReasonCode: reason("Denied")}); err == nil {
		t.Fatal("a malformed reason code was accepted")
	}
	message := "boom"
	if err := settle(fresh(), store.SettleAttemptInput{Outcome: store.ToolOutcomeFailed, Disposition: &stopped}); err == nil {
		t.Fatal("a failure with no diagnostic was accepted")
	}
	if err := settle(fresh(), store.SettleAttemptInput{Outcome: store.ToolOutcomeSucceeded, Disposition: &committed, ErrorMessage: &message}); err == nil {
		t.Fatal("a success carrying an error message was accepted")
	}

	// A wait is never settled directly: it leaves through its own
	// transitions (PR #383 review).
	waiting := b.wait(t)
	assertAttemptRejected(t, settle(waiting.ToolCallID, store.SettleAttemptInput{Outcome: store.ToolOutcomeSucceeded, Disposition: &committed}), store.ReasonAttemptWrongState)
	assertAttemptRejected(t, settle(waiting.ToolCallID, store.SettleAttemptInput{Outcome: store.ToolOutcomeStale, Disposition: &stopped, ReasonCode: reason("x/y")}), store.ReasonAttemptWrongState)
	if row := b.get(t, waiting.ToolCallID); row.State != store.AttemptOperatorWaiting {
		t.Fatalf("a refused settlement moved the wait: %s", row.State)
	}

	// The recorded requirement set is never rewritten at settlement (PR #383
	// review): after approval and consumption the row is open again and
	// carries the question that was approved; a settlement offering another
	// is refused, and a plain settlement leaves the recorded digest in place.
	if _, err := b.store.RecordOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, store.DecisionApproveOnce, b.userID); err != nil {
		t.Fatal(err)
	}
	if consumed, err := b.store.ConsumeOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, b.instance); err != nil || !consumed.Consumed {
		t.Fatalf("consume: %+v %v", consumed, err)
	}
	other := "4444444444444444444444444444444444444444444444444444444444444444"
	assertAttemptRejected(t, settle(waiting.ToolCallID, store.SettleAttemptInput{
		Outcome: store.ToolOutcomeBlocked, Disposition: &stopped,
		RequirementSet: json.RawMessage(`{"policy/operator_approval":{"question":"rewritten"}}`), RequirementSetDigest: &other,
	}), store.ReasonRequirementSetRecorded)
	if err := settle(waiting.ToolCallID, store.SettleAttemptInput{Outcome: store.ToolOutcomeSucceeded, Disposition: &committed}); err != nil {
		t.Fatalf("settling the consumed attempt: %v", err)
	}
	if row := b.get(t, waiting.ToolCallID); row.RequirementSetDigest == nil || *row.RequirementSetDigest != requirementHash {
		t.Fatalf("the recorded requirement digest moved at settlement: %v", row.RequirementSetDigest)
	}
	// And a requirement set belongs to a blocked settlement only.
	assertAttemptRejected(t, settle(fresh(), store.SettleAttemptInput{
		Outcome: store.ToolOutcomeSucceeded, Disposition: &committed,
		RequirementSet: json.RawMessage(requirements), RequirementSetDigest: &digest,
	}), store.ReasonRequirementSetForbidden)

	// The headless block: the requirement set written at settlement.
	blocked := fresh()
	if err := settle(blocked, store.SettleAttemptInput{
		Outcome: store.ToolOutcomeBlocked, Disposition: &stopped,
		RequirementSet: json.RawMessage(requirements), RequirementSetDigest: &digest,
	}); err != nil {
		t.Fatalf("headless block: %v", err)
	}
	row := b.get(t, blocked)
	if row.RequirementSetDigest == nil || *row.RequirementSetDigest != requirementHash || row.ClaimedBy != nil {
		t.Fatalf("blocked row %+v", row)
	}

	// Every outcome in its valid shape, and a repeat is Recorded=false.
	for _, ok := range []store.SettleAttemptInput{
		{Outcome: store.ToolOutcomeSucceeded, Disposition: &committed, Result: json.RawMessage(`{"number":1}`)},
		{Outcome: store.ToolOutcomeFailed, Disposition: &stopped, ErrorMessage: &message},
		{Outcome: store.ToolOutcomeFailed, Disposition: &committed, ErrorMessage: &message, ReasonCode: reason("family/reported")},
		{Outcome: store.ToolOutcomeDenied, Disposition: &stopped, ReasonCode: reason("authority/superseded")},
		{Outcome: store.ToolOutcomeStale, Disposition: &stopped, ReasonCode: reason("stale/requirement_set_changed")},
		{Outcome: store.ToolOutcomeUnknown, Disposition: &unresolved, ReasonCode: reason("attempt/interrupted")},
	} {
		id := fresh()
		if err := settle(id, ok); err != nil {
			t.Fatalf("valid %s/%s settlement: %v", ok.Outcome, *ok.Disposition, err)
		}
		repeat := ok
		repeat.OrganizationID, repeat.ToolCallID = b.organizationID, id
		again, err := b.store.SettleAttempt(ctx, repeat)
		if err != nil || again.Recorded {
			t.Fatalf("a repeated settlement: %+v %v", again, err)
		}
	}

	// The importer's verb: a row outside any execution, no disposition; the
	// lifted refusal is now the reason-code rule.
	plain, err := b.store.CreateToolCall(ctx, store.CreateToolCallInput{
		ToolName: "t", Arguments: json.RawMessage(`{}`), OrganizationID: b.organizationID, PrincipalInstanceID: b.principal.PrincipalInstanceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.store.CompleteToolCall(ctx, store.CompleteToolCallInput{Outcome: store.ToolOutcomeDenied, OrganizationID: b.organizationID, ToolCallID: plain.ToolCallID})
	assertAttemptRejected(t, err, store.ReasonReasonCodeRequired)
	err = settle(plain.ToolCallID, store.SettleAttemptInput{Outcome: store.ToolOutcomeSucceeded, Disposition: &committed})
	assertAttemptRejected(t, err, store.ReasonDispositionForbidden)
	if _, err := b.store.CompleteToolCall(ctx, store.CompleteToolCallInput{Outcome: store.ToolOutcomeSucceeded, OrganizationID: b.organizationID, ToolCallID: plain.ToolCallID}); err != nil {
		t.Fatalf("the importer's completion: %v", err)
	}
}

// --- supersession and the terminal result ------------------------------------

func TestSupersedeExecutionStalesWaitsAndReturnsTheDrainList(t *testing.T) {
	ctx := context.Background()
	b := newBoundaryFixture(t)
	open := b.register(t, nil)
	waiting := b.wait(t)
	if _, err := b.store.RecordOperatorDecision(ctx, b.organizationID, waiting.ToolCallID, store.DecisionApproveOnce, b.userID); err != nil {
		t.Fatal(err)
	}

	superseded, err := b.store.SupersedeExecution(ctx, b.organizationID, b.execution.ExecutionID)
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if len(superseded.Drain) != 1 || superseded.Drain[0].ToolCallID != open.ToolCallID {
		t.Fatalf("drain list %+v, want the open attempt %s", superseded.Drain, open.ToolCallID)
	}
	if len(superseded.Staled) != 1 || superseded.Staled[0].ToolCallID != waiting.ToolCallID {
		t.Fatalf("staled %+v, want the waiting attempt", superseded.Staled)
	}
	stale := superseded.Staled[0]
	switch {
	case stale.Outcome == nil || *stale.Outcome != store.ToolOutcomeStale, stale.ReasonCode == nil || *stale.ReasonCode != store.ReasonStaleAuthoritySuperseded:
		t.Fatalf("stale row %+v", stale)
	case stale.OperatorDecision == nil || stale.OperatorDecision.Decision != store.DecisionApproveOnce:
		t.Fatalf("supersession cleared the decision: %+v", stale.OperatorDecision)
	case stale.RequirementSetDigest == nil:
		t.Fatal("supersession cleared the requirement set")
	}
	execution, err := b.store.GetExecution(ctx, b.organizationID, b.execution.ExecutionID)
	if err != nil || execution.AuthorityState != store.AuthoritySuperseded || execution.AdmissionClosedAt == nil {
		t.Fatalf("after supersession: %+v %v", execution, err)
	}
	_, err = b.store.SupersedeExecution(ctx, b.organizationID, b.execution.ExecutionID)
	assertExecutionRejected(t, err, store.ReasonAlreadySuperseded)
	if _, err := b.store.RegisterAttempt(ctx, b.registration(v7(t), nil)); !errors.Is(err, store.ErrAdmissionClosed) {
		t.Fatalf("registration under superseded authority = %v, want ErrAdmissionClosed", err)
	}
	if _, err := b.store.SupersedeExecution(ctx, b.otherOrgID, b.execution.ExecutionID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant supersession = %v, want ErrNotFound", err)
	}
}

func TestRecordTerminalResultRequiresClosureAndADrainedReceipt(t *testing.T) {
	ctx := context.Background()
	b := newBoundaryFixture(t)
	completed := store.TerminalResult{Status: store.ExecutionCompleted, CompletionDisposition: ptr(store.CompletionChanged)}
	receipt := store.FenceReceipt{ActionsDrained: true, Domain: store.DomainNoneHeld}
	record := func(result store.TerminalResult, receipt store.FenceReceipt) error {
		return b.store.RecordTerminalResult(ctx, b.organizationID, b.execution.ExecutionID, result, receipt)
	}

	assertExecutionRejected(t, record(completed, receipt), store.ReasonAdmissionOpen)

	// An execution with an attempt registered before closure: the receipt's
	// action half is checked against the rows.
	b2 := newBoundaryFixture(t)
	attempt := b2.register(t, nil)
	if err := b2.store.CloseAdmission(ctx, b2.organizationID, b2.execution.ExecutionID); err != nil {
		t.Fatal(err)
	}
	record2 := func(result store.TerminalResult, receipt store.FenceReceipt) error {
		return b2.store.RecordTerminalResult(ctx, b2.organizationID, b2.execution.ExecutionID, result, receipt)
	}
	assertExecutionRejected(t, record2(completed, receipt), store.ReasonActionsNotDrained)
	// Settled is not drained (D11): unknown/unresolved still refuses.
	b2.settle(t, attempt.ToolCallID, store.ToolOutcomeUnknown, store.DrainUnresolved, reason("attempt/interrupted"))
	assertExecutionRejected(t, record2(completed, receipt), store.ReasonActionsNotDrained)
	// The late commit reconciled: drainage resolves, the receipt is accepted.
	if err := b2.store.ResolveDrainDisposition(ctx, b2.organizationID, attempt.ToolCallID, store.DrainCommitted); err != nil {
		t.Fatal(err)
	}
	if err := record2(completed, store.FenceReceipt{Domain: store.DomainNoneHeld}); !errors.Is(err, store.ErrReceiptInvalid) {
		t.Fatalf("a receipt claiming nothing drained = %v, want ErrReceiptInvalid", err)
	}
	if err := record2(store.TerminalResult{Status: store.ExecutionCompleted}, receipt); !errors.Is(err, store.ErrTerminalResultInvalid) {
		t.Fatalf("an invalid shape = %v, want ErrTerminalResultInvalid", err)
	}
	if err := record2(completed, receipt); err != nil {
		t.Fatalf("record after drainage: %v", err)
	}
	assertExecutionRejected(t, record2(completed, receipt), store.ReasonAlreadyTerminal)
	execution, err := b2.store.GetExecution(ctx, b2.organizationID, b2.execution.ExecutionID)
	if err != nil || execution.Terminal == nil || execution.Terminal.Status != store.ExecutionCompleted ||
		execution.Terminal.CompletionDisposition == nil || *execution.Terminal.CompletionDisposition != store.CompletionChanged || execution.TerminatedAt == nil {
		t.Fatalf("terminal execution %+v %v", execution, err)
	}

	// The blocked reference: this execution's settled blocked attempt, and
	// nothing else.
	b3 := newBoundaryFixture(t)
	succeeded := b3.register(t, nil)
	b3.settle(t, succeeded.ToolCallID, store.ToolOutcomeSucceeded, store.DrainCommitted, nil)
	blocked := b3.register(t, nil)
	digest := requirementHash
	if _, err := b3.store.SettleAttempt(ctx, store.SettleAttemptInput{
		Outcome: store.ToolOutcomeBlocked, Disposition: ptr(store.DrainStoppedBeforeCommit),
		RequirementSet: json.RawMessage(requirements), RequirementSetDigest: &digest,
		OrganizationID: b3.organizationID, ToolCallID: blocked.ToolCallID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := b3.store.CloseAdmission(ctx, b3.organizationID, b3.execution.ExecutionID); err != nil {
		t.Fatal(err)
	}
	record3 := func(id uuid.UUID) error {
		return b3.store.RecordTerminalResult(ctx, b3.organizationID, b3.execution.ExecutionID,
			store.TerminalResult{Status: store.ExecutionBlocked, BlockedToolCallID: &id}, receipt)
	}
	assertExecutionRejected(t, record3(succeeded.ToolCallID), store.ReasonBlockedAttemptInvalid)
	// Another execution's BLOCKED attempt: only the execution check can
	// refuse it (a non-blocked foreign attempt would be refused by the
	// outcome check and prove nothing about this one).
	b4 := b3.boundaryFor(t, provisionGoverned(t, b3.fixture), b3.configured()) // the SAME organization
	foreign := b4.register(t, nil)
	if _, err := b4.store.SettleAttempt(ctx, store.SettleAttemptInput{
		Outcome: store.ToolOutcomeBlocked, Disposition: ptr(store.DrainStoppedBeforeCommit),
		RequirementSet: json.RawMessage(requirements), RequirementSetDigest: &digest,
		OrganizationID: b4.organizationID, ToolCallID: foreign.ToolCallID,
	}); err != nil {
		t.Fatal(err)
	}
	assertExecutionRejected(t, record3(foreign.ToolCallID), store.ReasonBlockedAttemptInvalid)
	if err := record3(blocked.ToolCallID); err != nil {
		t.Fatalf("blocked result naming its attempt: %v", err)
	}
}

// --- recovery's reads ---------------------------------------------------------

func TestRecoveryEnumerationsClaimsAndInterruptedWaits(t *testing.T) {
	ctx := context.Background()
	b := newBoundaryFixture(t)
	open := b.register(t, nil)
	waiting := b.wait(t)
	unresolvedAttempt := b.register(t, nil)
	b.settle(t, unresolvedAttempt.ToolCallID, store.ToolOutcomeUnknown, store.DrainUnresolved, reason("attempt/interrupted"))
	done := b.register(t, nil)
	b.settle(t, done.ToolCallID, store.ToolOutcomeSucceeded, store.DrainCommitted, nil)

	listed, err := b.store.ListAttemptsForRecovery(ctx, b.organizationID)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[uuid.UUID]bool{}
	for i := range listed {
		ids[listed[i].ToolCallID] = true
	}
	if len(ids) != 3 || !ids[open.ToolCallID] || !ids[waiting.ToolCallID] || !ids[unresolvedAttempt.ToolCallID] || ids[done.ToolCallID] {
		t.Fatalf("recovery enumerates %v; want the open, the waiting and the unresolved attempt and not the committed one", ids)
	}

	// A wait this instance holds is not interrupted; a foreign claim is.
	err = b.store.StaleInterruptedWait(ctx, b.organizationID, waiting.ToolCallID, v7(t))
	assertAttemptRejected(t, err, store.ReasonAttemptNotClaimed)
	if err := b.store.StaleInterruptedWait(ctx, b.organizationID, waiting.ToolCallID, b.instance); err != nil {
		t.Fatalf("stale the foreign-claimed wait: %v", err)
	}
	err = b.store.StaleInterruptedWait(ctx, b.organizationID, open.ToolCallID, b.instance)
	assertAttemptRejected(t, err, store.ReasonAttemptWrongState)

	// Exactly one reconciler takes a foreign claim (D5).
	me, you := v7(t), v7(t)
	if err := b.store.TakeClaim(ctx, b.organizationID, open.ToolCallID, b.instance, me); err != nil {
		t.Fatalf("take claim: %v", err)
	}
	err = b.store.TakeClaim(ctx, b.organizationID, open.ToolCallID, b.instance, you)
	assertAttemptRejected(t, err, store.ReasonAttemptNotClaimed)
	if row := b.get(t, open.ToolCallID); row.ClaimedBy == nil || *row.ClaimedBy != me {
		t.Fatalf("claim %v, want %s", row.ClaimedBy, me)
	}
	err = b.store.TakeClaim(ctx, b.organizationID, done.ToolCallID, b.instance, me)
	assertAttemptRejected(t, err, store.ReasonAttemptWrongState)

	// MarkRevalidated and the resource wait's transitions.
	if err := b.store.MarkRevalidated(ctx, b.organizationID, open.ToolCallID); err != nil {
		t.Fatal(err)
	}
	if row := b.get(t, open.ToolCallID); row.RevalidatedAt == nil {
		t.Fatal("revalidation was not recorded")
	}
	if err := b.store.EnterResourceWait(ctx, b.organizationID, open.ToolCallID); err != nil {
		t.Fatal(err)
	}
	assertAttemptRejected(t, b.store.MarkRevalidated(ctx, b.organizationID, open.ToolCallID), store.ReasonAttemptWrongState)
	if err := b.store.LeaveResourceWait(ctx, b.organizationID, open.ToolCallID); err != nil {
		t.Fatal(err)
	}
	assertAttemptRejected(t, b.store.LeaveResourceWait(ctx, b.organizationID, open.ToolCallID), store.ReasonAttemptWrongState)
}

// OpenWork leaves a terminal execution out and names the wait of one that
// is waiting (D11); the projection refuses a terminal row it is handed.
func TestOpenWorkLeavesTerminalExecutionsAndNamesTheWait(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	finished := f.boundaryFor(t, provisionGoverned(t, f), f.configured())
	if err := finished.store.CloseAdmission(ctx, f.organizationID, finished.execution.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if err := finished.store.RecordTerminalResult(ctx, f.organizationID, finished.execution.ExecutionID,
		store.TerminalResult{Status: store.ExecutionTimedOut}, store.FenceReceipt{ActionsDrained: true, Domain: store.DomainNoneHeld}); err != nil {
		t.Fatal(err)
	}
	waiting := f.boundaryFor(t, provisionGoverned(t, f), f.configured())
	wait := waiting.wait(t)

	open, err := f.store.OpenWork(ctx, f.organizationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(open.Accepted) != 1 || open.Accepted[0].Execution.ExecutionID != waiting.execution.ExecutionID {
		t.Fatalf("open work holds %d accepted row(s); want only the waiting execution, not the terminal one", len(open.Accepted))
	}
	row := open.Accepted[0]
	if row.Wait == nil || row.Wait.ToolCallID != wait.ToolCallID || row.Wait.State != store.AttemptOperatorWaiting {
		t.Fatalf("the waiting execution's row does not name its wait: %+v", row.Wait)
	}
}

// --- the principal's execution binding ---------------------------------------

func TestGetPrincipalForExecutionIsBoundToTheExecution(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	first := f.boundaryFor(t, provisionGoverned(t, f), f.configured())
	second := f.boundaryFor(t, provisionGoverned(t, f), f.configured())
	if first.principal.ExecutionID == nil || *first.principal.ExecutionID != first.execution.ExecutionID {
		t.Fatalf("a live principal does not carry its execution: %+v", first.principal.ExecutionID)
	}
	if _, err := f.store.GetPrincipalForExecution(ctx, f.organizationID, first.execution.ExecutionID, first.principal.PrincipalInstanceID); err != nil {
		t.Fatalf("own principal: %v", err)
	}
	if _, err := f.store.GetPrincipalForExecution(ctx, f.organizationID, second.execution.ExecutionID, first.principal.PrincipalInstanceID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a principal of another execution = %v, want ErrNotFound", err)
	}
	if _, err := f.store.GetPrincipalForExecution(ctx, f.organizationID, first.execution.ExecutionID, f.author); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a human principal under an execution = %v, want ErrNotFound", err)
	}
	// Liveness is the stop_time, not the binding: a stopped principal keeps
	// its execution and is still refused (D4 check 1; PR #383 review).
	if _, err := f.store.StopPrincipalInstance(ctx, f.organizationID, first.principal.PrincipalInstanceID, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetPrincipalForExecution(ctx, f.organizationID, first.execution.ExecutionID, first.principal.PrincipalInstanceID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a stopped principal under its execution = %v, want ErrNotFound", err)
	}
	if stopped, err := f.store.GetPrincipalInstance(ctx, f.organizationID, first.principal.PrincipalInstanceID); err != nil || stopped.ExecutionID == nil {
		t.Fatalf("the stopped principal lost its binding: %+v %v", stopped, err)
	}
}

// --- the version-atomic reveal --------------------------------------------------

func TestRevealSecretAtVersionIsVersionAtomic(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	g := provisionGoverned(t, f)
	created, err := f.store.CreateSharedSecret(ctx, store.CreateSecretInput{
		Name: "forge.token", Scope: store.ConfigScope{Type: configkeys.ScopeRepository, ID: g.repository},
		OrganizationID: f.organizationID, ActingUserID: f.userID, Plaintext: secret.NewValue([]byte("token-v1")),
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := f.store.RevealSecretAtVersion(ctx, f.organizationID, created.ID, f.userID, created.Version)
	if err != nil || string(value.Reveal()) != "token-v1" {
		t.Fatalf("reveal at the created version: %q %v", value.Reveal(), err)
	}
	if _, err := f.store.ReplaceSecret(ctx, f.organizationID, created.ID, f.userID, created.Version, secret.NewValue([]byte("token-v2"))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RevealSecretAtVersion(ctx, f.organizationID, created.ID, f.userID, created.Version); !errors.Is(err, store.ErrSecretVersionMoved) {
		t.Fatalf("reveal at a moved version = %v, want ErrSecretVersionMoved", err)
	}
	value, err = f.store.RevealSecretAtVersion(ctx, f.organizationID, created.ID, f.userID, created.Version+1)
	if err != nil || string(value.Reveal()) != "token-v2" {
		t.Fatalf("reveal at the new version: %q %v", value.Reveal(), err)
	}
	// Not a member: the same answer, deliberately.
	if _, err := f.store.RevealSecretAtVersion(ctx, f.organizationID, created.ID, f.otherUser(t), created.Version+1); !errors.Is(err, store.ErrSecretVersionMoved) {
		t.Fatalf("a non-member's reveal = %v, want ErrSecretVersionMoved", err)
	}
}

// --- forge bindings --------------------------------------------------------------

func TestBindRepositoryForgeIsIdempotentPerProvider(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	g := provisionGoverned(t, f)
	input := store.BindRepositoryForgeInput{
		Provider: store.ForgeProviderGitea, BaseURL: "http://gitea:3000", Owner: "acme", Repo: "api",
		RepositoryID: g.repository, OrganizationID: f.organizationID,
	}
	bound, err := f.store.BindRepositoryForge(ctx, input)
	if err != nil || !bound.Created || bound.Record.Owner != "acme" {
		t.Fatalf("bind: %+v %v", bound, err)
	}
	again, err := f.store.BindRepositoryForge(ctx, input)
	if err != nil || again.Created {
		t.Fatalf("re-bind: %+v %v; want Created=false", again, err)
	}
	moved := input
	moved.Repo = "api-2"
	if _, err := f.store.BindRepositoryForge(ctx, moved); !errors.Is(err, store.ErrBootstrapConflict) {
		t.Fatalf("a differing binding = %v, want ErrBootstrapConflict", err)
	}
	for _, bad := range []struct {
		because string
		mutate  func(in *store.BindRepositoryForgeInput)
	}{
		{"an unknown provider", func(in *store.BindRepositoryForgeInput) { in.Provider = "github" }},
		{"a non-http base URL", func(in *store.BindRepositoryForgeInput) { in.BaseURL = "gitea.local" }},
		{"a blank owner", func(in *store.BindRepositoryForgeInput) { in.Owner = " " }},
	} {
		in := input
		bad.mutate(&in)
		if _, err := f.store.BindRepositoryForge(ctx, in); err == nil {
			t.Errorf("%s was bound", bad.because)
		}
	}
	stranger := input
	stranger.OrganizationID = f.otherOrgID
	if _, err := f.store.BindRepositoryForge(ctx, stranger); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("binding another organization's repository = %v, want ErrNotFound", err)
	}
	repository, err := f.store.GetRepositoryBySlug(ctx, f.organizationID, "api")
	if err != nil || len(repository.ForgeBindings) != 1 || repository.ForgeBindings[0].BaseURL != "http://gitea:3000" {
		t.Fatalf("repository bindings %+v %v", repository, err)
	}
}

// --- retention -----------------------------------------------------------------

// age moves an attempt's start and finish before the truncation horizon,
// directly: the seam refuses to settle in the past, and the row's
// disposition trigger is indifferent to its timestamps.
func (b *boundaryFixture) age(t *testing.T, id uuid.UUID) {
	t.Helper()
	if _, err := b.pool.Exec(context.Background(),
		`UPDATE tool_calls SET started_at = $2::timestamptz,
		     finished_at = CASE WHEN finished_at IS NULL THEN NULL ELSE $3::timestamptz END
		 WHERE tool_call_id = $1`, id, horizon().Add(-2*time.Hour), horizon().Add(-30*time.Minute)); err != nil {
		t.Fatalf("age %s: %v", id, err)
	}
}

// Truncation cannot manufacture a receipt or a fresh attempt (D5, D11):
// every attempt of an execution with no terminal result is retained as
// open, however old -- the row is the at-most-once guarantee while the id
// can still be presented -- and a settled attempt whose drainage is
// unresolved is retained even after; an attempt a terminal result names is
// retained as referenced, and so is the attempt a stale row names as the
// consumer of its approval.
func TestTruncationRetainsUndrainedAndReferencedAttempts(t *testing.T) {
	ctx := context.Background()
	b := newBoundaryFixture(t)

	unresolved := b.register(t, nil)
	b.settle(t, unresolved.ToolCallID, store.ToolOutcomeUnknown, store.DrainUnresolved, reason("attempt/interrupted"))
	blocked := b.register(t, nil)
	digest := requirementHash
	if _, err := b.store.SettleAttempt(ctx, store.SettleAttemptInput{
		Outcome: store.ToolOutcomeBlocked, Disposition: ptr(store.DrainStoppedBeforeCommit),
		RequirementSet: json.RawMessage(requirements), RequirementSetDigest: &digest,
		OrganizationID: b.organizationID, ToolCallID: blocked.ToolCallID,
	}); err != nil {
		t.Fatal(err)
	}
	stale := b.wait(t)
	if _, err := b.store.RecordOperatorDecision(ctx, b.organizationID, stale.ToolCallID, store.DecisionApproveOnce, b.userID); err != nil {
		t.Fatal(err)
	}
	if err := b.store.StaleInterruptedWait(ctx, b.organizationID, stale.ToolCallID, b.instance); err != nil {
		t.Fatal(err)
	}
	successor := b.register(t, nil)
	if err := b.store.InheritOperatorDecision(ctx, b.organizationID, stale.ToolCallID, successor.ToolCallID, requirementHash); err != nil {
		t.Fatal(err)
	}
	b.settle(t, successor.ToolCallID, store.ToolOutcomeSucceeded, store.DrainCommitted, nil)
	plain := b.register(t, nil)
	b.settle(t, plain.ToolCallID, store.ToolOutcomeSucceeded, store.DrainCommitted, nil)
	all := []uuid.UUID{unresolved.ToolCallID, blocked.ToolCallID, stale.ToolCallID, successor.ToolCallID, plain.ToolCallID}
	for _, id := range all {
		b.age(t, id)
	}

	// 1. The execution has no terminal result: every attempt is retained
	// as open, and the settled plain attempt's id still classifies as a
	// replay -- the D5 guarantee truncation must not erase (review round 1).
	result, err := b.store.TruncateAuditBefore(ctx, b.organizationID, horizon())
	if err != nil {
		t.Fatal(err)
	}
	calls := result.PerTable[store.TableToolCalls]
	if calls.RetainedOpen != 5 || calls.Deleted != 0 || calls.RetainedReferenced != 0 || !calls.Reconciles() {
		t.Fatalf("under an open execution: %+v; want every attempt retained as open", calls)
	}
	replay, err := b.store.RegisterAttempt(ctx, b.registration(plain.ToolCallID, nil))
	if err != nil || replay.Registered {
		t.Fatalf("re-presenting a settled id after truncation: %+v %v; want the recorded row, not a fresh registration", replay, err)
	}

	// 2. Closed but not terminal -- the unresolved attempt blocks the
	// receipt -- still retained, and the receipt still refused.
	if err := b.store.CloseAdmission(ctx, b.organizationID, b.execution.ExecutionID); err != nil {
		t.Fatal(err)
	}
	completed := store.TerminalResult{Status: store.ExecutionCompleted, CompletionDisposition: ptr(store.CompletionChanged)}
	receipt := store.FenceReceipt{ActionsDrained: true, Domain: store.DomainNoneHeld}
	assertExecutionRejected(t, b.store.RecordTerminalResult(ctx, b.organizationID, b.execution.ExecutionID, completed, receipt),
		store.ReasonActionsNotDrained)
	if result, err = b.store.TruncateAuditBefore(ctx, b.organizationID, horizon()); err != nil {
		t.Fatal(err)
	}
	if calls = result.PerTable[store.TableToolCalls]; calls.RetainedOpen != 5 || calls.Deleted != 0 {
		t.Fatalf("under a closed, non-terminal execution: %+v", calls)
	}

	// 3. Drainage resolves and the result records: the attempts are history.
	// The stale row and its consumer go in two passes -- the consumer is
	// referenced for as long as the referrer is in the statement's snapshot.
	if err := b.store.ResolveDrainDisposition(ctx, b.organizationID, unresolved.ToolCallID, store.DrainCommitted); err != nil {
		t.Fatal(err)
	}
	if err := b.store.RecordTerminalResult(ctx, b.organizationID, b.execution.ExecutionID, completed, receipt); err != nil {
		t.Fatal(err)
	}
	if result, err = b.store.TruncateAuditBefore(ctx, b.organizationID, horizon()); err != nil {
		t.Fatal(err)
	}
	if calls = result.PerTable[store.TableToolCalls]; calls.Deleted != 4 || calls.RetainedReferenced != 1 || calls.RetainedOpen != 0 || !calls.Reconciles() {
		t.Fatalf("under a terminal execution: %+v; want four deleted and the approval's consumer retained as referenced", calls)
	}
	if _, err := b.store.GetToolCall(ctx, b.organizationID, successor.ToolCallID); err != nil {
		t.Fatalf("the consumer of a surviving stale row's approval was truncated: %v", err)
	}
	if result, err = b.store.TruncateAuditBefore(ctx, b.organizationID, horizon()); err != nil {
		t.Fatal(err)
	}
	if calls = result.PerTable[store.TableToolCalls]; calls.Deleted != 1 || calls.RetainedReferenced != 0 {
		t.Fatalf("second pass under a terminal execution: %+v; want the now-unreferenced consumer deleted", calls)
	}

	// 4. A blocked result's attempt is retained as referenced for as long as
	// the execution names it.
	b2 := newBoundaryFixture(t)
	blocked2 := b2.register(t, nil)
	if _, err := b2.store.SettleAttempt(ctx, store.SettleAttemptInput{
		Outcome: store.ToolOutcomeBlocked, Disposition: ptr(store.DrainStoppedBeforeCommit),
		RequirementSet: json.RawMessage(requirements), RequirementSetDigest: &digest,
		OrganizationID: b2.organizationID, ToolCallID: blocked2.ToolCallID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := b2.store.CloseAdmission(ctx, b2.organizationID, b2.execution.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if err := b2.store.RecordTerminalResult(ctx, b2.organizationID, b2.execution.ExecutionID,
		store.TerminalResult{Status: store.ExecutionBlocked, BlockedToolCallID: &blocked2.ToolCallID}, receipt); err != nil {
		t.Fatal(err)
	}
	b2.age(t, blocked2.ToolCallID)
	if result, err = b2.store.TruncateAuditBefore(ctx, b2.organizationID, horizon()); err != nil {
		t.Fatalf("truncation aborted on the terminal result's reference: %v", err)
	}
	if calls = result.PerTable[store.TableToolCalls]; calls.RetainedReferenced != 1 || calls.Deleted != 0 || !calls.Reconciles() {
		t.Fatalf("blocked attempt named by a terminal result: %+v", calls)
	}
}
