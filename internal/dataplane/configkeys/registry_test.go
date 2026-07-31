package configkeys

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"testing"
)

// acceptAll is a schema that admits anything, for tests whose subject is
// something other than the value.
var acceptAll = ValidatorFunc(func([]byte) error { return nil })

// rejectAll is its opposite, carrying a distinctive cause so a test can
// assert the registry preserved it rather than replacing it with a message
// of its own.
var errSchema = errors.New("value is not an object")

var rejectAll = ValidatorFunc(func([]byte) error { return errSchema })

func writableEntry(scopes ...Scope) Entry {
	return Entry{Schema: acceptAll, PermittedScopes: scopes}
}

// TestNewRefusesMalformedRegistrations covers every way a registration can
// be self-contradictory. Each is refused at construction, where it is a
// startup failure, rather than on the first write of a rarely used key.
func TestNewRefusesMalformedRegistrations(t *testing.T) {
	tests := []struct {
		name    string
		key     Key
		entry   Entry
		wantMsg string
	}{
		{
			name:    "empty key",
			key:     "",
			entry:   writableEntry(ScopeOrganization),
			wantMsg: "is empty",
		},
		{
			name:    "uppercase key",
			key:     "Forge.Token",
			entry:   writableEntry(ScopeOrganization),
			wantMsg: "canonical dotted name",
		},
		{
			name:    "leading dot",
			key:     ".forge",
			entry:   writableEntry(ScopeOrganization),
			wantMsg: "canonical dotted name",
		},
		{
			name:    "trailing dot",
			key:     "forge.",
			entry:   writableEntry(ScopeOrganization),
			wantMsg: "canonical dotted name",
		},
		{
			name:    "empty segment",
			key:     "forge..token",
			entry:   writableEntry(ScopeOrganization),
			wantMsg: "canonical dotted name",
		},
		{
			name:    "segment starting with a digit",
			key:     "forge.2fa",
			entry:   writableEntry(ScopeOrganization),
			wantMsg: "canonical dotted name",
		},
		{
			name:    "no schema",
			key:     "forge.retries",
			entry:   Entry{PermittedScopes: []Scope{ScopeOrganization}},
			wantMsg: "no schema",
		},
		{
			name:    "no permitted scopes",
			key:     "forge.retries",
			entry:   Entry{Schema: acceptAll},
			wantMsg: "permits no scopes",
		},
		{
			name:    "unknown scope",
			key:     "forge.retries",
			entry:   writableEntry("story"),
			wantMsg: "unknown scope",
		},
		{
			name:    "duplicate scope",
			key:     "forge.retries",
			entry:   writableEntry(ScopeProduct, ScopeProduct),
			wantMsg: "twice",
		},
		{
			name:    "sensitive with a schema",
			key:     "forge.token",
			entry:   Entry{Sensitive: true, Schema: acceptAll},
			wantMsg: "declares a schema",
		},
		{
			name:    "sensitive with permitted scopes",
			key:     "forge.token",
			entry:   Entry{Sensitive: true, PermittedScopes: []Scope{ScopeOrganization}},
			wantMsg: "declares permitted scopes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry, err := New(map[Key]Entry{tc.key: tc.entry})
			if err == nil {
				t.Fatalf("New accepted a malformed registration for %q", tc.key)
			}
			if registry != nil {
				t.Error("New returned a registry alongside an error")
			}
			if !bytes.Contains([]byte(err.Error()), []byte(tc.wantMsg)) {
				t.Errorf("error %q does not explain the problem (want it to mention %q)",
					err, tc.wantMsg)
			}
		})
	}
}

// TestNewAcceptsWellFormedRegistrations is the other half: the refusals
// above are only meaningful if these are admitted. Without it every rule
// could be "refuse everything" and the suite would still pass.
func TestNewAcceptsWellFormedRegistrations(t *testing.T) {
	keys := map[Key]Entry{
		"forge":          writableEntry(ScopeOrganization),
		"forge.retries":  writableEntry(ScopeOrganization, ScopeProduct, ScopeRepository),
		"forge.base-url": writableEntry(ScopeRepository),
		"a1.b2-c3.d4":    writableEntry(ScopeProduct),
		"forge.token":    {Sensitive: true},
	}

	registry, err := New(keys)
	if err != nil {
		t.Fatalf("New refused a well-formed set: %v", err)
	}
	want := []Key{"a1.b2-c3.d4", "forge", "forge.base-url", "forge.retries", "forge.token"}
	if got := registry.Keys(); !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}
}

// TestValidateWriteAcceptsAPermittedWrite pins the accepting path, and that
// the schema sees exactly the bytes destined for the column. A registry
// that refused everything would satisfy every other test in this file.
func TestValidateWriteAcceptsAPermittedWrite(t *testing.T) {
	value := []byte(`{"retries":3}`)

	var saw []byte
	registry := MustNew(map[Key]Entry{
		"forge.retries": {
			Schema: ValidatorFunc(func(v []byte) error {
				saw = v
				return nil
			}),
			PermittedScopes: []Scope{ScopeProduct, ScopeRepository},
		},
	})

	for _, scope := range []Scope{ScopeProduct, ScopeRepository} {
		if err := registry.ValidateWrite("forge.retries", scope, value); err != nil {
			t.Errorf("ValidateWrite at permitted scope %q: %v", scope, err)
		}
	}
	if !bytes.Equal(saw, value) {
		t.Errorf("the schema saw %q, want the stored bytes %q", saw, value)
	}
}

// TestValidateWriteRefusals covers the four refusals, each identified by
// its sentinel rather than by its message, because callers act differently
// on each and the message is not the contract.
func TestValidateWriteRefusals(t *testing.T) {
	registry := MustNew(map[Key]Entry{
		"forge.retries": {Schema: rejectAll, PermittedScopes: []Scope{ScopeProduct}},
		"forge.base-url": {
			Schema:          acceptAll,
			PermittedScopes: []Scope{ScopeOrganization},
		},
		"forge.token": {Sensitive: true},
	})

	tests := []struct {
		name  string
		key   Key
		scope Scope
		want  error
	}{
		{
			name:  "unregistered key",
			key:   "forge.unheard-of",
			scope: ScopeOrganization,
			want:  ErrUnknownKey,
		},
		{
			name:  "sensitive key",
			key:   "forge.token",
			scope: ScopeOrganization,
			want:  ErrSensitiveKey,
		},
		{
			name:  "scope the key does not permit",
			key:   "forge.base-url",
			scope: ScopeRepository,
			want:  ErrScopeNotPermitted,
		},
		{
			name:  "value failing the schema",
			key:   "forge.retries",
			scope: ScopeProduct,
			want:  ErrInvalidValue,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := registry.ValidateWrite(tc.key, tc.scope, []byte(`{}`))
			if !errors.Is(err, tc.want) {
				t.Fatalf("ValidateWrite returned %v, want %v", err, tc.want)
			}
		})
	}
}

// TestValidateWritePreservesTheSchemaCause keeps the reason a value was
// rejected reachable. ErrInvalidValue alone says a value was wrong without
// saying what about it was wrong, which is not enough to fix it.
func TestValidateWritePreservesTheSchemaCause(t *testing.T) {
	registry := MustNew(map[Key]Entry{
		"forge.retries": {Schema: rejectAll, PermittedScopes: []Scope{ScopeProduct}},
	})

	err := registry.ValidateWrite("forge.retries", ScopeProduct, []byte(`[]`))
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("ValidateWrite returned %v, want ErrInvalidValue", err)
	}
	if !errors.Is(err, errSchema) {
		t.Errorf("the schema's own cause was dropped: %v", err)
	}
}

// TestValidateWriteChecksInPrecedenceOrder is the test the ordering comment
// exists for.
//
// Every case here fails MORE than one rule, so a registry that applied the
// rules in a different order would still refuse the write and a test
// asserting only "refused" could not tell. What is pinned is WHICH refusal
// comes back, because that is what the caller is told to go and fix.
func TestValidateWriteChecksInPrecedenceOrder(t *testing.T) {
	registry := MustNew(map[Key]Entry{
		// Sensitive, so it is also at a scope it does not permit (it
		// permits none) with a value no schema admits (it has none).
		"forge.token": {Sensitive: true},
		// Permits only product, and rejects every value: a repository
		// write of anything fails both.
		"forge.retries": {Schema: rejectAll, PermittedScopes: []Scope{ScopeProduct}},
	})

	tests := []struct {
		name string
		key  Key
		want error
		why  string
	}{
		{
			name: "unregistered outranks everything",
			key:  "forge.unheard-of",
			want: ErrUnknownKey,
			why:  "an unregistered key has no policy to apply, so nothing else can be said about the write",
		},
		{
			name: "sensitive outranks scope and schema",
			key:  "forge.token",
			want: ErrSensitiveKey,
			why: "a scope or schema complaint sends the caller to fix something that would still " +
				"leave the credential heading for an unencrypted table",
		},
		{
			name: "scope outranks schema",
			key:  "forge.retries",
			want: ErrScopeNotPermitted,
			why:  "a value cannot be judged useful at a level the key may not be set at",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := registry.ValidateWrite(tc.key, ScopeRepository, []byte(`"nonsense"`))
			if !errors.Is(err, tc.want) {
				t.Fatalf("ValidateWrite returned %v, want %v — %s", err, tc.want, tc.why)
			}
		})
	}
}

// TestEmptyRegistryRefusesEveryKey pins the no-seed-vocabulary rule from
// the other side: with nothing registered, nothing at all can be written.
func TestEmptyRegistryRefusesEveryKey(t *testing.T) {
	registry := MustNew(nil)

	if got := registry.Keys(); len(got) != 0 {
		t.Errorf("an empty registry reports keys %v; this package ships no seed vocabulary", got)
	}
	err := registry.ValidateWrite("forge.retries", ScopeOrganization, []byte(`3`))
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("ValidateWrite returned %v, want ErrUnknownKey", err)
	}
}

// TestRegistryIsFrozenAgainstItsInput is why New clones.
//
// The registrations arrive in a map the caller still holds, containing a
// slice the caller can still write through. Without the clone a caller
// could widen a key's permitted scopes after construction, and the rows
// admitted before and after would be indistinguishable in the table.
func TestRegistryIsFrozenAgainstItsInput(t *testing.T) {
	scopes := []Scope{ScopeProduct}
	entries := map[Key]Entry{
		"forge.retries": {Schema: acceptAll, PermittedScopes: scopes},
	}
	registry := MustNew(entries)

	// Rewrite the slice the registration was built from, and add a key to
	// the map it was built from.
	scopes[0] = ScopeRepository
	entries["forge.base-url"] = writableEntry(ScopeOrganization)

	if err := registry.ValidateWrite("forge.retries", ScopeProduct, []byte(`3`)); err != nil {
		t.Errorf("the registered scope stopped working after the input slice was rewritten: %v", err)
	}
	if err := registry.ValidateWrite("forge.retries", ScopeRepository, []byte(`3`)); !errors.Is(err, ErrScopeNotPermitted) {
		t.Errorf("rewriting the input slice widened a key's scopes: got %v, want ErrScopeNotPermitted", err)
	}
	if err := registry.ValidateWrite("forge.base-url", ScopeOrganization, []byte(`""`)); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("adding to the input map registered a new key: got %v, want ErrUnknownKey", err)
	}
}

// TestLookupDoesNotLeakTheRegistration is the same freeze on the way out.
func TestLookupDoesNotLeakTheRegistration(t *testing.T) {
	registry := MustNew(map[Key]Entry{
		"forge.retries": writableEntry(ScopeProduct),
	})

	got, err := registry.Lookup("forge.retries")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !slices.Equal(got.PermittedScopes, []Scope{ScopeProduct}) {
		t.Fatalf("PermittedScopes = %v, want [product]", got.PermittedScopes)
	}
	got.PermittedScopes[0] = ScopeRepository

	if err := registry.ValidateWrite("forge.retries", ScopeRepository, []byte(`3`)); !errors.Is(err, ErrScopeNotPermitted) {
		t.Errorf("writing through a returned registration widened a key's scopes: got %v, want ErrScopeNotPermitted", err)
	}
}

// TestLookupReportsSensitiveKeys covers the registration a caller cannot
// reach through ValidateWrite's accepting path at all.
func TestLookupReportsSensitiveKeys(t *testing.T) {
	registry := MustNew(map[Key]Entry{"forge.token": {Sensitive: true}})

	got, err := registry.Lookup("forge.token")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !got.Sensitive {
		t.Error("Lookup reported a sensitive key as writable")
	}
	if len(got.PermittedScopes) != 0 {
		t.Errorf("a sensitive key reports permitted scopes %v; it may be set nowhere", got.PermittedScopes)
	}

	if _, err := registry.Lookup("forge.unheard-of"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("Lookup of an unregistered key returned %v, want ErrUnknownKey", err)
	}
}

// TestErrorsNameTheRegisteredVocabulary keeps the refusals diagnosable. A
// bare "not registered" leaves an operator with a typo and no list to check
// it against.
func TestErrorsNameTheRegisteredVocabulary(t *testing.T) {
	registry := MustNew(map[Key]Entry{
		"forge.retries":  writableEntry(ScopeProduct),
		"forge.base-url": writableEntry(ScopeOrganization),
	})

	err := registry.ValidateWrite("forge.retires", ScopeProduct, []byte(`3`))
	for _, want := range []string{"forge.retries", "forge.base-url"} {
		if !bytes.Contains([]byte(err.Error()), []byte(want)) {
			t.Errorf("the unknown-key error does not list %q: %v", want, err)
		}
	}

	err = registry.ValidateWrite("forge.base-url", ScopeRepository, []byte(`""`))
	if !bytes.Contains([]byte(err.Error()), []byte(ScopeOrganization)) {
		t.Errorf("the scope refusal does not say where the key MAY be set: %v", err)
	}
}

// TestMustNewPanicsOnAMalformedRegistration pins the build-time contract of
// the package-level constructor.
func TestMustNewPanicsOnAMalformedRegistration(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("MustNew accepted a malformed registration")
		}
		if _, ok := recovered.(error); !ok {
			t.Errorf("MustNew panicked with %T, want an error", recovered)
		}
	}()

	MustNew(map[Key]Entry{"Forge.Token": writableEntry(ScopeOrganization)})
}

// TestNewReportsTheSameFailureEveryRun guards the sorted iteration. Map
// order is randomised per run, so a set with two bad registrations would
// otherwise report whichever one the runtime reached first, and a build
// failure would name a different key each time it was reproduced.
func TestNewReportsTheSameFailureEveryRun(t *testing.T) {
	entries := map[Key]Entry{
		"aaa.bad": {Schema: acceptAll},
		"zzz.bad": {Schema: acceptAll},
	}

	first, err := New(entries)
	if err == nil {
		t.Fatalf("New accepted two malformed registrations (registry %v)", first)
	}
	for i := range 40 {
		_, again := New(entries)
		if again == nil {
			t.Fatal("New accepted two malformed registrations")
		}
		if again.Error() != err.Error() {
			t.Fatalf("run %d reported %q, first run reported %q; the failure depends on map order",
				i, again, err)
		}
	}
	if !bytes.Contains([]byte(err.Error()), []byte("aaa.bad")) {
		t.Errorf("the reported failure is %q, want the first key in sorted order", err)
	}
}

// Compile-time proof that ValidatorFunc satisfies Validator, so a change to
// the interface fails here rather than at every registration site.
var _ Validator = ValidatorFunc(nil)

// Compile-time proof that the sentinels are distinct values. errors.Is
// against the wrong one would otherwise pass silently if two were aliases.
func TestSentinelsAreDistinct(t *testing.T) {
	sentinels := []error{ErrUnknownKey, ErrSensitiveKey, ErrScopeNotPermitted, ErrInvalidValue}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel %d and %d are indistinguishable: %v / %v", i, j, a, b)
			}
		}
	}
	_ = fmt.Sprint(sentinels)
}
