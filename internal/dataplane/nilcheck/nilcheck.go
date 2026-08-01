// Package nilcheck answers one question the `== nil` operator answers
// wrongly: is there anything behind this interface value?
//
// It exists because both of the data plane's registries validate their
// registrations at construction, and both hold behaviour behind an
// interface. `iface == nil` is true only when the interface carries no type
// at all, so an interface holding a TYPED nil — `Validator(ValidatorFunc(nil))`,
// or a nil pointer whose type has the method — passes that test while being
// unusable. The registration is then admitted, construction reports success,
// and the nil call arrives much later at the seam, on the first use of
// whatever rarely exercised entry carried it.
//
// One implementation rather than one per registry: the two registries are
// deliberately independent and share no types, but this is not a policy
// either of them owns. It is a fixed property of the language, it is the
// same in both, and a second copy is a copy that can be fixed in one place
// only.
package nilcheck

import "reflect"

// IsNil reports whether v is nil or is an interface holding a typed nil.
//
// A nil pointer whose method never dereferences its receiver is legal and
// would work, so reporting it as nil is a false positive. That is the
// intended trade: a typed-nil implementation is not something anyone
// registers on purpose, and the alternative is a nil dereference at a seam
// long after the registration that caused it.
func IsNil(v any) bool {
	if v == nil {
		return true
	}
	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Func, reflect.Pointer, reflect.Interface,
		reflect.Map, reflect.Slice, reflect.Chan, reflect.UnsafePointer:
		return value.IsNil()
	default:
		// A struct, string, or other non-nilable value cannot be a typed
		// nil. Calling IsNil on one of those would panic, which is why the
		// kinds are enumerated rather than attempted.
		return false
	}
}
