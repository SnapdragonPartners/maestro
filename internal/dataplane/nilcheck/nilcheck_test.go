package nilcheck

import "testing"

type behaviour interface{ Do() }

type deref struct{ n int }

func (d *deref) Do() { _ = d.n }

type valueReceiver struct{}

func (valueReceiver) Do() {}

// TestIsNilDetectsTypedNils is the whole point: every case here is NOT
// equal to nil, so a `== nil` guard admits all of them.
func TestIsNilDetectsTypedNils(t *testing.T) {
	var nilPointer *deref
	var nilMap map[string]int
	var nilSlice []int
	var nilChan chan int
	var nilFunc func()
	var nilInterface behaviour

	tests := []struct {
		name string
		v    any
	}{
		{name: "typed-nil pointer behind an interface", v: behaviour(nilPointer)},
		{name: "typed-nil pointer as any", v: nilPointer},
		{name: "nil func", v: nilFunc},
		{name: "nil map", v: nilMap},
		{name: "nil slice", v: nilSlice},
		{name: "nil chan", v: nilChan},
		{name: "untyped nil", v: nil},
		{name: "nil interface variable", v: nilInterface},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !IsNil(tc.v) {
				t.Errorf("IsNil(%T) = false, want true", tc.v)
			}
		})
	}
}

// TestIsNilAcceptsUsableValues is the half that keeps the predicate from
// being "return true". Without it every guard built on IsNil would refuse
// every registration and the registries' own suites would be the only thing
// to notice.
func TestIsNilAcceptsUsableValues(t *testing.T) {
	tests := []struct {
		name string
		v    any
	}{
		{name: "pointer to a value", v: &deref{n: 1}},
		{name: "struct value implementing the interface", v: valueReceiver{}},
		{name: "non-nil func", v: func() {}},
		{name: "empty but non-nil map", v: map[string]int{}},
		{name: "empty but non-nil slice", v: []int{}},
		{name: "zero int", v: 0},
		{name: "empty string", v: ""},
		{name: "false", v: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if IsNil(tc.v) {
				t.Errorf("IsNil(%T) = true, want false", tc.v)
			}
		})
	}
}

// TestIsNilDoesNotPanicOnNonNilableKinds guards the enumerated switch.
// reflect.Value.IsNil panics for kinds that cannot be nil, so a default
// branch that called it would turn this predicate into the crash it exists
// to prevent.
func TestIsNilDoesNotPanicOnNonNilableKinds(t *testing.T) {
	for _, v := range []any{0, "", false, 3.14, struct{ A int }{}, [2]int{}, complex(1, 1)} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("IsNil(%T) panicked: %v", v, r)
				}
			}()
			_ = IsNil(v)
		}()
	}
}
