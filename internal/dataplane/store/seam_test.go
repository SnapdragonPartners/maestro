package store

import (
	"reflect"
	"testing"
)

// The seam's shape, asserted rather than assumed.
//
// Which interface a method sits on is a contract with callers, and nothing
// about moving one back would fail to compile: the Postgres transaction
// type carries the truncation pass either way, so it would go on satisfying
// a Tx that advertised it. Only a test can hold this line.

// TestTxDoesNotAdvertiseTruncation guards the split.
//
// WithTx opens at the pool's default isolation, READ COMMITTED, and offers
// a caller no way to ask for another. A truncation pass is only sound under
// one snapshot, so a Tx advertising TruncateAuditBefore would be promising
// an operation that necessarily refuses wherever a caller could reach it.
func TestTxDoesNotAdvertiseTruncation(t *testing.T) {
	const method = "TruncateAuditBefore"

	for _, surface := range []struct {
		name string
		typ  reflect.Type
	}{
		{"Tx", reflect.TypeOf((*Tx)(nil)).Elem()},
		{"Writer", reflect.TypeOf((*Writer)(nil)).Elem()},
		{"CallWriter", reflect.TypeOf((*CallWriter)(nil)).Elem()},
	} {
		if _, found := surface.typ.MethodByName(method); found {
			t.Errorf("%s advertises %s. Truncation opens its own REPEATABLE READ transaction; reached "+
				"through a caller's transaction it can only refuse, so it belongs on Maintenance, which "+
				"only Store embeds.", surface.name, method)
		}
	}

	// The other direction: a split that removed it from the seam entirely
	// would also pass the checks above.
	if _, found := reflect.TypeOf((*Store)(nil)).Elem().MethodByName(method); !found {
		t.Errorf("Store no longer offers %s, so the operation is unreachable through the seam", method)
	}
}

// TestTxDoesNotAdvertiseObjectOperations guards the other split.
//
// Every object operation makes REMOTE calls, and the write path opens its
// own transaction so it can hold the digest lock and the lease's row lock
// across a server-side copy and a read-back. Reached through a caller's
// transaction it could take neither in the order it needs, nor bound them —
// and nothing about moving one back would fail to compile, because the
// Postgres store satisfies both surfaces either way.
//
// The guarded set is DERIVED from ObjectStore rather than listed here. A
// hand-written list guards the methods someone remembered: this test
// already missed AttachEvidence, Pin, Unpin and ListPins when they landed,
// and would have missed CleanUpStaging too. Deriving it means an operation
// added to the seam is guarded by having been added.
func TestTxDoesNotAdvertiseObjectOperations(t *testing.T) {
	objectStore := reflect.TypeOf((*ObjectStore)(nil)).Elem()
	if objectStore.NumMethod() == 0 {
		t.Fatal("ObjectStore declares no methods, so this test guards nothing")
	}

	for _, surface := range []struct {
		name string
		typ  reflect.Type
	}{
		{"Tx", reflect.TypeOf((*Tx)(nil)).Elem()},
		{"Writer", reflect.TypeOf((*Writer)(nil)).Elem()},
		{"Reader", reflect.TypeOf((*Reader)(nil)).Elem()},
	} {
		for i := range objectStore.NumMethod() {
			method := objectStore.Method(i).Name
			if _, found := surface.typ.MethodByName(method); found {
				t.Errorf("%s advertises %s. The object module opens its own transaction and holds it "+
					"across remote calls; reached through a caller's transaction it cannot do that, so "+
					"it belongs on ObjectStore, which only Store embeds.", surface.name, method)
			}
		}
	}

	// The other direction: a split that dropped them from the seam would
	// pass every check above.
	storeType := reflect.TypeOf((*Store)(nil)).Elem()
	for i := range objectStore.NumMethod() {
		method := objectStore.Method(i).Name
		if _, found := storeType.MethodByName(method); !found {
			t.Errorf("Store no longer offers %s, so the operation is unreachable through the seam", method)
		}
	}
}

// TestTxDoesNotAdvertiseRecovery guards the third split, for the same
// reason as truncation: OpenWork opens its own REPEATABLE READ snapshot and
// locks nothing, which a caller's READ COMMITTED transaction cannot promise.
func TestTxDoesNotAdvertiseRecovery(t *testing.T) {
	const method = "OpenWork"
	for _, surface := range []struct {
		name string
		typ  reflect.Type
	}{
		{"Tx", reflect.TypeOf((*Tx)(nil)).Elem()},
		{"Reader", reflect.TypeOf((*Reader)(nil)).Elem()},
		{"Writer", reflect.TypeOf((*Writer)(nil)).Elem()},
	} {
		if _, found := surface.typ.MethodByName(method); found {
			t.Errorf("%s advertises %s; the projection's read needs its own snapshot and belongs on Recovery, "+
				"which only Store embeds", surface.name, method)
		}
	}
	if _, found := reflect.TypeOf((*Store)(nil)).Elem().MethodByName(method); !found {
		t.Errorf("Store no longer offers %s, so recovery is unreachable through the seam", method)
	}
}
