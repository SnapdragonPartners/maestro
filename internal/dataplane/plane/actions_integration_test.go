//go:build integration

package plane_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/plane"
	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/prompt"
)

// recordingActions is a contract that admits everything and remembers what
// it was asked. A stub rather than the boundary's registry, because what
// is under test is the THREADING: whether the seam plane.Open builds
// consults the caller's contract at all, and with the canonical set.
type recordingActions struct {
	asked [][]string
}

func (r *recordingActions) ValidateCapabilities(identities []string) error {
	r.asked = append(r.asked, slices.Clone(identities))
	return nil
}

// TestOpenThreadsTheCallerActionContract: the seam validates a capability
// set through the contract the caller supplied (item 5 design, D12).
//
// THE MUTANT this must kill: drop postgres.WithActionContract(c.Actions)
// from plane.Open. The store then keeps its fail-closed empty contract, the
// caller's is never asked, and the set is refused as unknown -- which is
// exactly the state every composer was in before this checkpoint, when
// Caller had no Actions field at all.
//
// No Story is dispatched. AcceptDispatch validates the configuration BEFORE
// it touches the dispatch row (a refused configuration must leave the
// dispatch pending), so the contract is consulted -- and its verdict
// observable -- for a dispatch that does not exist: with the contract
// threaded, the call gets past validation to the row lookup and fails
// there; without it, the refusal is the capability's.
func TestOpenThreadsTheCallerActionContract(t *testing.T) {
	ctx := context.Background()
	types, err := registry.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	actions := &recordingActions{}
	blob, _ := planetest.Blob(t, "actions")
	seam, err := plane.Open(ctx, plane.Composition{
		DSN: planetest.DSN(t, "actions"), Objects: blob, RootKey: planetest.RootKey(t),
		Caller: plane.Caller{Types: types, Keys: configkeys.MustNew(nil), Prompts: prompt.MustNew(nil), Actions: actions, Harness: planetest.Harness(t)},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(seam.Close)

	_, acceptErr := seam.AcceptDispatch(ctx, uuid.New(), uuid.New(), store.ExecutionConfiguration{
		CapabilitySet: []string{"test/beta", "test/alpha", "test/alpha"}, ActingUserID: uuid.New(),
	})
	if acceptErr == nil {
		t.Fatal("a dispatch that does not exist was accepted")
	}
	var rejected *store.ExecutionRejected
	if errors.As(acceptErr, &rejected) && rejected.Reason == store.ReasonCapabilityUnknown {
		t.Fatalf("the set was refused as unknown, so the caller's contract was never consulted: %v", acceptErr)
	}
	if len(actions.asked) != 1 || !slices.Equal(actions.asked[0], []string{"test/alpha", "test/beta"}) {
		t.Fatalf("the contract was asked %v; want exactly once, with the canonical set [test/alpha test/beta]", actions.asked)
	}
}
