// Package utils provides utility functions for type assertions, identifiers, and other common operations.
package utils

import "fmt"

// SafeAssert safely performs type assertion and returns the value and success status.
func SafeAssert[T any](value any) (T, bool) {
	if v, ok := value.(T); ok {
		return v, true
	}
	var zero T
	return zero, false
}

// MustAssert performs type assertion and panics with descriptive message if it fails.
func MustAssert[T any](value any, context string) T {
	if v, ok := value.(T); ok {
		return v
	}
	panic(fmt.Sprintf("type assertion failed in %s: expected %T, got %T", context, *new(T), value))
}

// AssertMapStringAny safely asserts a value as map[string]any (common pattern in tests).
func AssertMapStringAny(value any) (map[string]any, error) {
	if m, ok := value.(map[string]any); ok {
		return m, nil
	}
	return nil, fmt.Errorf("expected map[string]any, got %T", value)
}

// GetMapField safely gets a field from a map[string]any and asserts its type.
func GetMapField[T any](m map[string]any, key string) (T, error) {
	var zero T
	value, exists := m[key]
	if !exists {
		return zero, fmt.Errorf("field '%s' not found in map", key)
	}

	if typedValue, ok := value.(T); ok {
		return typedValue, nil
	}

	return zero, fmt.Errorf("field '%s' expected type %T, got %T", key, zero, value)
}

// GetMapFieldOr safely gets a field from a map[string]any with a default value.
func GetMapFieldOr[T any](m map[string]any, key string, defaultValue T) T {
	if value, err := GetMapField[T](m, key); err == nil {
		return value
	}
	return defaultValue
}

// StateValueGetter represents any object that can get state values (like state machine).
type StateValueGetter interface {
	GetStateValue(key string) (any, bool)
}

// GetStateValue safely gets and asserts a value from a state machine-like object.
func GetStateValue[T any](sg StateValueGetter, key string) (T, bool) {
	var zero T
	if value, exists := sg.GetStateValue(key); exists {
		if typedValue, ok := value.(T); ok {
			return typedValue, true
		}
	}
	return zero, false
}

// GetStateValueOr safely gets a value from state with a default.
func GetStateValueOr[T any](sg StateValueGetter, key string, defaultValue T) T {
	if value, exists := GetStateValue[T](sg, key); exists {
		return value
	}
	return defaultValue
}

// MustGetStateValue gets a state value or panics with descriptive message.
func MustGetStateValue[T any](sg StateValueGetter, key, context string) T {
	if value, exists := GetStateValue[T](sg, key); exists {
		return value
	}
	panic(fmt.Sprintf("required state value '%s' not found in %s", key, context))
}
