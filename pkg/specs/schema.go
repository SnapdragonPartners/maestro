// Package specs provides specification parsing and validation.
package specs

import (
	"time"
)

// SpecPack represents a complete specification with metadata and requirements.
//
//nolint:govet // Field alignment optimization would hurt readability; logical grouping is more important.
type SpecPack struct {
	// YAML Frontmatter fields
	Version  string    `yaml:"version"`
	Priority string    `yaml:"priority"` // must | should | could
	Created  time.Time `yaml:"created,omitempty"`

	// Markdown content (parsed sections)
	Title        string        // Extracted from # Feature: Title
	Vision       string        // ## Vision section
	InScope      []string      // ### In Scope items
	OutOfScope   []string      // ### Out of Scope items
	Requirements []Requirement // Parsed requirements

	// Raw content (for reference)
	RawMarkdown string `yaml:"-"` // Original markdown content
}

// Requirement represents a single requirement with metadata.
type Requirement struct {
	ID                 string   // R-001, R-002, etc.
	Title              string   // Extracted from ### R-001: Title
	Type               string   // functional, non-functional, etc.
	Priority           string   // must | should | could
	Dependencies       []string // List of requirement IDs
	Description        string   // Requirement description
	AcceptanceCriteria []string // List of acceptance criteria
	LineNumber         int      // Line number in original markdown (for error reporting)
}

// LintResult represents the result of spec validation.
//
//nolint:govet // Field alignment optimization would hurt readability; logical grouping is more important.
type LintResult struct {
	Passed   bool     // true if all checks passed.
	Blocking []string // List of blocking errors (validation failures).
}

// ValidationError represents a specific validation error.
type ValidationError struct {
	Check   string // Name of the validation check.
	Message string // Error message.
}

func (v ValidationError) Error() string {
	return v.Message
}
