package plane

import (
	"context"
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
)

// These tests reach no database. What they cover is the OWNERSHIP half of the
// composition — which resources get released, when, and in what order — and
// that is the half a signature cannot enforce and a working plane will not
// reveal. A leaked lifecycle lock does not fail anything until the next
// lifecycle operation blocks forever on a holder nobody can name.

// closeOnlyStore is a seam that can be closed and nothing else.
//
// The embedded interface is nil on purpose: every other method would panic if
// reached, which is the assertion that this double is used for exactly one
// thing. composedSeam adds behaviour to Close and delegates the rest, so Close
// is the whole of its contract.
type closeOnlyStore struct {
	store.Store
	closed bool
}

func (s *closeOnlyStore) Close() { s.closed = true }

// TestComposedSeamReleasesWhatItOwnsWhenClosed pins the lifetime rule: owned
// resources belong to the SEAM, not to the call that opened it.
//
// A version that released as soon as the store was built would satisfy any
// test that opens and immediately closes, and would leave the work the lock
// was taken for entirely unprotected. What has to hold is that Close, and only
// Close, gives them back.
func TestComposedSeamReleasesWhatItOwnsWhenClosed(t *testing.T) {
	released := false
	inner := &closeOnlyStore{}
	seam := &composedSeam{Store: inner, owned: []Owned{
		{What: "data-plane lifecycle lock", Close: func() error { released = true; return nil }},
	}}

	if released {
		t.Fatal("the resource was released before the seam was closed")
	}
	seam.Close()

	if !inner.closed {
		t.Error("closing the seam did not close the plane beneath it")
	}
	if !released {
		t.Error("closing the seam did not release what it owns; every later lifecycle operation " +
			"would block until this process exits")
	}
}

// TestComposedSeamClosesThePlaneBeforeReleasing pins the other ordering, the
// one between the store and the resources it depends on.
//
// Releasing first would let a destructive lifecycle operation start against a
// seam that is still open, which is the race the lock exists to prevent — so
// this is not stylistic sequencing but the property the lock provides.
func TestComposedSeamClosesThePlaneBeforeReleasing(t *testing.T) {
	var order []string
	inner := &orderedStore{onClose: func() { order = append(order, "store") }}
	seam := &composedSeam{Store: inner, owned: []Owned{
		{What: "lock", Close: func() error { order = append(order, "release"); return nil }},
	}}
	seam.Close()

	if len(order) != 2 || order[0] != "store" || order[1] != "release" {
		t.Fatalf("closed in order %v, want [store release]: releasing before the plane is closed "+
			"lets a destructive lifecycle operation begin against a live seam", order)
	}
}

type orderedStore struct {
	store.Store
	onClose func()
}

func (s *orderedStore) Close() { s.onClose() }

// TestComposedSeamReleasesInReverseOrder covers the ordering, which matters
// because ownership nests: a lock taken before the plane was opened is the one
// that must outlive it.
func TestComposedSeamReleasesInReverseOrder(t *testing.T) {
	var order []string
	seam := &composedSeam{Store: &closeOnlyStore{}, owned: []Owned{
		{What: "outer", Close: func() error { order = append(order, "outer"); return nil }},
		{What: "inner", Close: func() error { order = append(order, "inner"); return nil }},
	}}
	seam.Close()

	if len(order) != 2 || order[0] != "inner" || order[1] != "outer" {
		t.Fatalf("released %v, want [inner outer]: releasing in acquisition order frees the outer "+
			"resource while the inner one still depends on it", order)
	}
}

// TestComposedSeamReleasesEveryResourceDespiteAFailure guards against one
// misbehaving resource stranding the rest, which is the opposite of what a
// release path is for.
func TestComposedSeamReleasesEveryResourceDespiteAFailure(t *testing.T) {
	second := false
	seam := &composedSeam{Store: &closeOnlyStore{}, owned: []Owned{
		{What: "stubborn", Close: func() error { return errors.New("will not close") }},
		{What: "ordinary", Close: func() error { second = true; return nil }},
	}}
	seam.Close()

	if !second {
		t.Fatal("a failing release stopped the remaining resources being released, so they are " +
			"held because something else misbehaved")
	}
}

// TestOpenReleasesOwnedResourcesWhenItFails is the failure-path obligation,
// and it is the reason this function exists rather than callers invoking
// postgres.Open directly.
//
// A composer acquires its resources BEFORE opening the plane — the local one
// takes the lifecycle lock, a cloud one builds an object client — so an open
// that fails partway must give them back. One that did not would block every
// later lifecycle operation on a holder nobody is using, and the failure it
// followed would look like the only problem.
func TestOpenReleasesOwnedResourcesWhenItFails(t *testing.T) {
	released := false
	// An invalid composition, so it fails before reaching the database: the
	// property under test is about the release, not about how the open failed.
	_, err := Open(context.Background(), Composition{
		DSN:     "",
		Objects: stubObjects{},
		RootKey: stubRootKey{},
		Types:   emptyRegistry(t),
		Owned: []Owned{
			{What: "data-plane lifecycle lock", Close: func() error { released = true; return nil }},
		},
	})
	if err == nil {
		t.Fatal("an invalid composition opened")
	}
	if !released {
		t.Fatal("a failed open kept the resources it was given; the next lifecycle operation " +
			"would block forever on a lock nobody is using")
	}
}

// TestOpenReportsAReleaseFailureAlongsideTheOpenFailure covers the case where
// cleanup also fails. The open failure must survive: it is the one the caller
// asked about, and a release problem is additional context rather than a
// replacement.
func TestOpenReportsAReleaseFailureAlongsideTheOpenFailure(t *testing.T) {
	_, err := Open(context.Background(), Composition{
		Objects: stubObjects{},
		RootKey: stubRootKey{},
		Types:   emptyRegistry(t),
		Owned: []Owned{
			{What: "lifecycle lock", Close: func() error { return errors.New("descriptor gone") }},
		},
	})
	if err == nil {
		t.Fatal("an invalid composition opened")
	}
	for _, want := range []string{"no database DSN", "lifecycle lock", "descriptor gone"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should carry both the open failure and the release failure; missing "+
				"%q in: %v", want, err)
		}
	}
}

// TestOpenSurvivesReleasingAnUnclosableResource drives the path that actually
// panicked: Open validates, rejects a resource with no close function, and
// then its deferred cleanup is handed that same rejected composition.
//
// The release path cannot assume validated input, because the failure it
// exists for INCLUDES validation. Calling the nil turned a clear error into a
// crash inside the recovery path — the worst place for one, since it is
// reached only when something has already gone wrong.
//
// It is driven through Open on purpose. Testing validate directly isolates the
// guard but never runs the cleanup, and isolating the guard is how this case
// stopped being covered once.
func TestOpenSurvivesReleasingAnUnclosableResource(t *testing.T) {
	released := false
	_, err := Open(context.Background(), Composition{
		DSN:     "postgres://example",
		Objects: stubObjects{},
		RootKey: stubRootKey{},
		Types:   emptyRegistry(t),
		Owned: []Owned{
			{What: "closable", Close: func() error { released = true; return nil }},
			{What: "unclosable"},
		},
	})
	if err == nil {
		t.Fatal("a resource with no close function was accepted")
	}
	if !released {
		t.Fatal("the resources that COULD be released were not, because an unclosable one was " +
			"encountered first")
	}
	if !strings.Contains(err.Error(), "unclosable") {
		t.Fatalf("the leak was not named, so nothing says what was lost: %v", err)
	}
}

// TestValidateRefusesAnIncompleteComposition covers the inputs that cannot
// open, and it calls validate DIRECTLY rather than going through Open.
//
// That is deliberate and was forced by a mutation. Driving these through Open
// cannot isolate this guard: postgres.New carries its own nilcheck one layer
// down, so replacing nilcheck.IsNil here with a plain `== nil` still produced
// an error — from somewhere else — and the test passed with the defect
// present. A guard that only appears to work because something downstream
// repeats it is not tested by anything that lets the downstream check run.
//
// The typed-nil cases are why the distinction matters. `blob == nil` was
// correct while the parameter was a pointer and stopped being correct the
// moment it became an interface, and that exact defect shipped earlier in this
// work and deferred the failure to a panic on the first object read.
func TestValidateRefusesAnIncompleteComposition(t *testing.T) {
	complete := func() Composition {
		return Composition{
			DSN:     "postgres://example",
			Objects: stubObjects{},
			RootKey: stubRootKey{},
			Types:   emptyRegistry(t),
		}
	}
	var typedNilObjects *objects.GCS
	var typedNilKey *stubRootKeyPtr

	for name, breakIt := range map[string]func(*Composition){
		"no DSN":              func(c *Composition) { c.DSN = "" },
		"no objects":          func(c *Composition) { c.Objects = nil },
		"typed-nil objects":   func(c *Composition) { c.Objects = typedNilObjects },
		"no root key":         func(c *Composition) { c.RootKey = nil },
		"typed-nil root key":  func(c *Composition) { c.RootKey = typedNilKey },
		"no registry":         func(c *Composition) { c.Types = nil },
		"owned without close": func(c *Composition) { c.Owned = []Owned{{What: "lock"}} },
		"owned without name": func(c *Composition) {
			c.Owned = []Owned{{Close: func() error { return nil }}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			composition := complete()
			breakIt(&composition)
			if err := composition.validate(); err == nil {
				t.Fatal("an incomplete composition validated, deferring the failure to first use")
			}
		})
	}
}

// TestValidateAcceptsACompleteComposition is the control: without it the table
// above would pass against a validate that refused everything.
func TestValidateAcceptsACompleteComposition(t *testing.T) {
	composition := Composition{
		DSN:     "postgres://example",
		Objects: stubObjects{},
		RootKey: stubRootKey{},
		Types:   emptyRegistry(t),
		Owned:   []Owned{{What: "lock", Close: func() error { return nil }}},
	}
	if err := composition.validate(); err != nil {
		t.Fatalf("a complete composition was refused: %v", err)
	}
}

func emptyRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	types, err := registry.New(nil)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	return types
}

// stubObjects satisfies the object seam without reaching a provider. Only the
// capability is answered; the rest would panic, which is the assertion that
// validation never touches them.
type stubObjects struct{ objects.Store }

func (stubObjects) IncompleteWrites() objects.IncompleteWriteSupport {
	return objects.IncompleteWritesProviderReclaimed
}

type stubRootKey struct{}

func (stubRootKey) RootKey() ([]byte, error) { return []byte("material"), nil }
func (stubRootKey) Backend() secret.Backend  { return secret.BackendOperatorProvided }

// stubRootKeyPtr exists only so a typed-nil POINTER can be stored in the
// RootKeyProvider interface; a struct value cannot be nil.
type stubRootKeyPtr struct{}

func (*stubRootKeyPtr) RootKey() ([]byte, error) { return nil, nil }
func (*stubRootKeyPtr) Backend() secret.Backend  { return secret.BackendOperatorProvided }
