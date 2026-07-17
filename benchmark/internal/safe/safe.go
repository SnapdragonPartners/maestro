// Package safe provides the module-local SafeAssert-style helper the
// repository's review policy prefers over bare type assertions.
package safe

// As performs a checked type assertion.
func As[T any](value any) (T, bool) {
	typed, ok := value.(T)
	return typed, ok
}
