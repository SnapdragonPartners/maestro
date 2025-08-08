// Package config provides configuration validation for agent settings.
package config

import (
	"errors"
)

var (
	// ErrInvalidConfig indicates an invalid configuration was provided.
	ErrInvalidConfig = errors.New("invalid configuration")
)

// Note: Config validation methods have been moved to the runtime package
// to avoid import cycles and method definition restrictions.
