package postgres

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
)

// fakeBlob is an objects.Store whose capability the test chooses. It
// implements no reclaimer, so embedding it is how a test says "declares
// enumeration and cannot enumerate".
type fakeBlob struct {
	support objects.IncompleteWriteSupport
}

func (f *fakeBlob) PutStaged(context.Context, string, int64, io.Reader) (string, error) {
	return "", errors.New("not used")
}
func (f *fakeBlob) Promote(context.Context, string, string, string) (string, error) {
	return "", errors.New("not used")
}
func (f *fakeBlob) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not used")
}
func (f *fakeBlob) Exists(context.Context, string) (bool, error) { return false, nil }
func (f *fakeBlob) ListVersions(context.Context, string) ([]objects.Version, error) {
	return nil, nil
}
func (f *fakeBlob) DeleteVersion(context.Context, string, string) error { return nil }
func (f *fakeBlob) IncompleteWrites() objects.IncompleteWriteSupport    { return f.support }

// reclaimingBlob adds the reclaimer half, so a test can distinguish "declares
// enumeration and can" from "declares enumeration and cannot".
type reclaimingBlob struct{ fakeBlob }

func (r *reclaimingBlob) ListUploadsForKey(context.Context, string) ([]objects.Upload, error) {
	return nil, nil
}
func (r *reclaimingBlob) ListUploadsUnder(context.Context, string) ([]objects.Upload, error) {
	return nil, nil
}
func (r *reclaimingBlob) AbortUpload(context.Context, string, string) error { return nil }

// TestValidateIncompleteWritesRejectsUnknownCapability proves the capability
// check does not fail open.
//
// The zero value is the case that matters most: an adapter that simply never
// sets the field would, under a `!= enumerable` test, be silently classified
// as reclaiming its own incomplete writes. Nothing would break, storage would
// stop being reclaimed, and the only symptom would be a bill.
func TestValidateIncompleteWritesRejectsUnknownCapability(t *testing.T) {
	for _, support := range []objects.IncompleteWriteSupport{
		"",                   // zero value: an adapter that never set it
		"enumarable",         // a typo of the valid value
		"provider_reclaimed", // right idea, wrong spelling
	} {
		err := validateIncompleteWrites(&reclaimingBlob{fakeBlob{support: support}})
		if err == nil {
			t.Fatalf("capability %q was accepted; unknown values must be rejected rather than "+
				"defaulting to provider-reclaimed", support)
		}
		if !errors.Is(err, store.ErrInvariant) {
			t.Fatalf("capability %q: got %v, want an ErrInvariant", support, err)
		}
	}
}

// TestValidateIncompleteWritesRejectsEnumerableWithoutReclaimer proves the
// second half: declaring enumeration is a promise the adapter must be able to
// keep.
func TestValidateIncompleteWritesRejectsEnumerableWithoutReclaimer(t *testing.T) {
	err := validateIncompleteWrites(&fakeBlob{support: objects.IncompleteWritesEnumerable})
	if err == nil {
		t.Fatal("an adapter declaring enumeration without implementing the reclaimer was accepted")
	}
	if !errors.Is(err, store.ErrInvariant) {
		t.Fatalf("got %v, want an ErrInvariant", err)
	}
}

// TestValidateIncompleteWritesAcceptsBothValidShapes is the control. Without
// it the two rejection tests above would also pass against a function that
// rejected everything.
func TestValidateIncompleteWritesAcceptsBothValidShapes(t *testing.T) {
	if err := validateIncompleteWrites(&reclaimingBlob{
		fakeBlob{support: objects.IncompleteWritesEnumerable},
	}); err != nil {
		t.Fatalf("an enumerating adapter with a reclaimer was rejected: %v", err)
	}
	// A provider-reclaimed adapter implements no reclaimer, and must not be
	// required to.
	if err := validateIncompleteWrites(&fakeBlob{
		support: objects.IncompleteWritesProviderReclaimed,
	}); err != nil {
		t.Fatalf("a provider-reclaimed adapter was rejected: %v", err)
	}
}

// TestNewRejectsTypedNilObjectAdapter proves the constructor guard survives
// the object seam becoming an interface.
//
// `blob == nil` was correct while the parameter was *objects.Blob and stopped
// being correct the moment it became objects.Store: an interface holding a
// typed-nil pointer is not equal to nil, so the plain comparison admitted one
// and deferred the failure to a panic on the first object read.
//
// The pool and registry are non-nil so their guards do not fire first. A
// version of this test that passed nil for them would pass against a New with
// no object-adapter check at all, which is the shape of test this repository
// has already paid for twice.
func TestNewRejectsTypedNilObjectAdapter(t *testing.T) {
	var typedNil *fakeBlob            // nil pointer...
	var blob objects.Store = typedNil // ...in a non-nil interface

	// No runtime check that `blob != nil` here: govet's nilness analyser
	// rejects the comparison as an impossible condition, which is a stronger
	// statement than the assertion would have been. The linter proves at
	// build time that this interface is not equal to nil, which is exactly
	// the property that makes the plain `== nil` guard insufficient.

	types, err := registry.New(nil)
	if err != nil {
		t.Fatalf("build an empty registry: %v", err)
	}

	_, err = New(&pgxpool.Pool{}, types, blob, nil)
	if err == nil {
		t.Fatal("a typed-nil object adapter was accepted")
	}
	if !strings.Contains(err.Error(), "object adapter is nil") {
		t.Fatalf("got %v, want the object-adapter guard to have rejected it — any other error means "+
			"an earlier guard fired and this test proves nothing about the typed-nil case", err)
	}
}
