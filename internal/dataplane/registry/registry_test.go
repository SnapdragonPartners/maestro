package registry

import (
	"errors"
	"strings"
	"testing"
)

func passing() Validator { return ValidatorFunc(func([]byte) error { return nil }) }

func validEntries() map[Type]Entry {
	return map[Type]Entry{
		"example": {
			Category:       CategoryManagement,
			CurrentVersion: 2,
			Validators:     map[int]Validator{1: passing(), 2: passing()},
		},
	}
}

func TestNewRejectsMalformedRegistrations(t *testing.T) {
	cases := []struct {
		name    string
		entries map[Type]Entry
		want    string
	}{
		{
			name:    "empty type",
			entries: map[Type]Entry{"": {Category: CategoryManagement, CurrentVersion: 1, Validators: map[int]Validator{1: passing()}}},
			want:    "is empty",
		},
		{
			name:    "unknown category",
			entries: map[Type]Entry{"x": {Category: "archive", CurrentVersion: 1, Validators: map[int]Validator{1: passing()}}},
			want:    "want \"management\" or \"audit\"",
		},
		{
			name:    "missing category",
			entries: map[Type]Entry{"x": {CurrentVersion: 1, Validators: map[int]Validator{1: passing()}}},
			want:    "has category \"\"",
		},
		{
			name:    "zero current version",
			entries: map[Type]Entry{"x": {Category: CategoryAudit, Validators: map[int]Validator{1: passing()}}},
			want:    "current version 0",
		},
		{
			name:    "no validators",
			entries: map[Type]Entry{"x": {Category: CategoryAudit, CurrentVersion: 1}},
			want:    "no validators",
		},
		{
			name:    "nil validator",
			entries: map[Type]Entry{"x": {Category: CategoryAudit, CurrentVersion: 1, Validators: map[int]Validator{1: nil}}},
			want:    "nil validator",
		},
		{
			name:    "validator for version zero",
			entries: map[Type]Entry{"x": {Category: CategoryAudit, CurrentVersion: 1, Validators: map[int]Validator{0: passing(), 1: passing()}}},
			want:    "validator for version 0",
		},
		{
			// The case that passes every write and fails every read.
			name:    "current version has no validator",
			entries: map[Type]Entry{"x": {Category: CategoryManagement, CurrentVersion: 3, Validators: map[int]Validator{1: passing(), 2: passing()}}},
			want:    "writes at version 3 but has no validator for it",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := New(testCase.entries)
			if err == nil {
				t.Fatal("expected a construction error, got none")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error %q does not contain %q", err, testCase.want)
			}
		})
	}
}

func TestNewAcceptsAValidRegistration(t *testing.T) {
	built, err := New(validEntries())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	entry, err := built.Lookup("example")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if entry.Category != CategoryManagement {
		t.Fatalf("category = %q, want %q", entry.Category, CategoryManagement)
	}
	if entry.CurrentVersion != 2 {
		t.Fatalf("current version = %d, want 2", entry.CurrentVersion)
	}
}

func TestLookupRejectsUnregisteredType(t *testing.T) {
	built, err := New(validEntries())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = built.Lookup("not-registered")
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("error is not ErrUnknownType: %v", err)
	}
	// The message names what is registered, so an operator who mistyped a
	// type can see the near miss without reading the source.
	if !strings.Contains(err.Error(), "example") {
		t.Fatalf("error %q does not list the registered types", err)
	}
}

func TestValidatorForRejectsUnreadableVersion(t *testing.T) {
	built, err := New(validEntries())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, version := range []int{0, 3, 99} {
		_, err := built.ValidatorFor("example", version)
		if !errors.Is(err, ErrVersionOutOfRange) {
			t.Fatalf("version %d: error is not ErrVersionOutOfRange: %v", version, err)
		}
		if !strings.Contains(err.Error(), "readable: [1 2]") {
			t.Fatalf("version %d: error %q does not report the readable range", version, err)
		}
	}
}

func TestValidatorForReturnsThePerVersionValidator(t *testing.T) {
	var called int
	entries := map[Type]Entry{
		"example": {
			Category:       CategoryManagement,
			CurrentVersion: 2,
			Validators: map[int]Validator{
				1: ValidatorFunc(func([]byte) error { called = 1; return nil }),
				2: ValidatorFunc(func([]byte) error { called = 2; return nil }),
			},
		},
	}
	built, err := New(entries)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Version 1 must reach version 1's validator, not the current one.
	// This is what makes amendments to an old artifact check against the
	// schema their payload was actually written for.
	validator, err := built.ValidatorFor("example", 1)
	if err != nil {
		t.Fatalf("ValidatorFor: %v", err)
	}
	if err := validator.Validate(nil); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if called != 1 {
		t.Fatalf("reached validator %d, want the version 1 validator", called)
	}
}

// TestRegistryIsImmutableAfterConstruction guards the freeze. A registry
// that shared its caller's map would let a later write of the same type
// disagree with an earlier one about category or version, and the stored
// rows carry no record of which registration produced them.
func TestRegistryIsImmutableAfterConstruction(t *testing.T) {
	entries := validEntries()
	built, err := New(entries)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Mutate the caller's map and the nested validator map after construction.
	entries["injected"] = Entry{Category: CategoryAudit, CurrentVersion: 1, Validators: map[int]Validator{1: passing()}}
	entries["example"].Validators[7] = passing()

	if _, err := built.Lookup("injected"); !errors.Is(err, ErrUnknownType) {
		t.Fatal("a type added to the caller's map after construction became visible")
	}
	if _, err := built.ValidatorFor("example", 7); !errors.Is(err, ErrVersionOutOfRange) {
		t.Fatal("a validator added to the caller's map after construction became visible")
	}
}

func TestTypesIsSortedAndComplete(t *testing.T) {
	built, err := New(map[Type]Entry{
		"zebra":  {Category: CategoryAudit, CurrentVersion: 1, Validators: map[int]Validator{1: passing()}},
		"alpha":  {Category: CategoryManagement, CurrentVersion: 1, Validators: map[int]Validator{1: passing()}},
		"middle": {Category: CategoryManagement, CurrentVersion: 1, Validators: map[int]Validator{1: passing()}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := built.Types()
	want := []Type{"alpha", "middle", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("Types() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Types() = %v, want %v", got, want)
		}
	}
}

// TestValidatorErrorsReachTheCaller confirms a failing validator's own
// message survives, since that message is what tells an author which field
// of their payload is wrong.
func TestValidatorErrorsReachTheCaller(t *testing.T) {
	sentinel := errors.New("field \"title\" is required")
	built, err := New(map[Type]Entry{
		"example": {
			Category:       CategoryManagement,
			CurrentVersion: 1,
			Validators:     map[int]Validator{1: ValidatorFunc(func([]byte) error { return sentinel })},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	validator, err := built.ValidatorFor("example", 1)
	if err != nil {
		t.Fatalf("ValidatorFor: %v", err)
	}
	if err := validator.Validate([]byte(`{}`)); !errors.Is(err, sentinel) {
		t.Fatalf("validator error did not reach the caller: %v", err)
	}
}
