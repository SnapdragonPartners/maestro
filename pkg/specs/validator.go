package specs

import (
	"fmt"
	"regexp"
	"strings"
)

var requirementIDPattern = regexp.MustCompile(`^R-\d{3}$`)

// Validate performs binary pass/fail validation on a SpecPack.
// Returns LintResult with blocking errors if validation fails.
//
//nolint:gocyclo,cyclop // This function intentionally implements all 7 validation checks.
func Validate(spec *SpecPack) LintResult {
	var errors []string

	// Check 1: YAML frontmatter parsed correctly (implicitly validated by parser)
	if spec.Version == "" {
		errors = append(errors, "YAML frontmatter missing 'version' field")
	}
	if spec.Priority == "" {
		errors = append(errors, "YAML frontmatter missing 'priority' field")
	}

	// Check 2: Required sections present
	if spec.Title == "" {
		errors = append(errors, "Missing required section: Feature title (# Feature: ...)")
	}
	if spec.Vision == "" {
		errors = append(errors, "Missing required section: Vision (## Vision)")
	}
	if len(spec.InScope) == 0 && len(spec.OutOfScope) == 0 {
		errors = append(errors, "Missing required section: Scope (## Scope with In/Out sections)")
	}
	if len(spec.Requirements) == 0 {
		errors = append(errors, "Missing required section: Requirements (## Requirements)")
	}

	// Check 3: All requirement IDs unique and correctly formatted
	seenIDs := make(map[string]int) // Map ID to line number
	//nolint:gocritic // Range copy is acceptable for validation logic; using pointers would complicate code.
	for _, req := range spec.Requirements {
		if !requirementIDPattern.MatchString(req.ID) {
			errors = append(errors, fmt.Sprintf("Requirement ID '%s' (line %d) does not match format R-###", req.ID, req.LineNumber))
		}
		if prevLine, exists := seenIDs[req.ID]; exists {
			errors = append(errors, fmt.Sprintf("Duplicate requirement ID '%s' (lines %d and %d)", req.ID, prevLine, req.LineNumber))
		}
		seenIDs[req.ID] = req.LineNumber
	}

	// Check 4: All requirements have ≥1 acceptance criterion
	//nolint:gocritic // Range copy is acceptable for validation logic; using pointers would complicate code.
	for _, req := range spec.Requirements {
		if len(req.AcceptanceCriteria) == 0 {
			errors = append(errors, fmt.Sprintf("Requirement %s (line %d) has no acceptance criteria", req.ID, req.LineNumber))
		}
	}

	// Check 5: Priority values valid (must | should | could)
	validPriorities := map[string]bool{"must": true, "should": true, "could": true}

	// Check spec-level priority
	if !validPriorities[strings.ToLower(spec.Priority)] {
		errors = append(errors, fmt.Sprintf("Spec priority '%s' is invalid (must be: must, should, or could)", spec.Priority))
	}

	// Check requirement-level priorities
	//nolint:gocritic // Range copy is acceptable for validation logic; using pointers would complicate code.
	for _, req := range spec.Requirements {
		if req.Priority != "" && !validPriorities[strings.ToLower(req.Priority)] {
			errors = append(errors, fmt.Sprintf("Requirement %s (line %d) has invalid priority '%s' (must be: must, should, or could)", req.ID, req.LineNumber, req.Priority))
		}
	}

	// Check 6: Dependency graph is acyclic
	if cycleErr := checkCycles(spec.Requirements); cycleErr != nil {
		errors = append(errors, cycleErr.Error())
	}

	// Check 7: In-scope list has ≥1 item
	if len(spec.InScope) == 0 {
		errors = append(errors, "In Scope section must have at least one item")
	}

	return LintResult{
		Passed:   len(errors) == 0,
		Blocking: errors,
	}
}

// checkCycles detects cycles in the dependency graph using DFS.
func checkCycles(requirements []Requirement) error {
	// Build adjacency list
	graph := make(map[string][]string)
	allIDs := make(map[string]bool)

	//nolint:gocritic // Range copy is acceptable for validation logic; using pointers would complicate code.
	for _, req := range requirements {
		allIDs[req.ID] = true
		graph[req.ID] = req.Dependencies
	}

	// Verify all dependencies exist
	//nolint:gocritic // Range copy is acceptable for validation logic; using pointers would complicate code.
	for _, req := range requirements {
		for _, dep := range req.Dependencies {
			if !allIDs[dep] {
				return fmt.Errorf("Requirement %s depends on non-existent requirement %s", req.ID, dep)
			}
		}
	}

	// Check for cycles using DFS
	visiting := make(map[string]bool)
	visited := make(map[string]bool)

	var visit func(id string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("dependency cycle detected involving requirement %s", id)
		}
		if visited[id] {
			return nil
		}

		visiting[id] = true
		for _, dep := range graph[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}

	for id := range graph {
		if !visited[id] {
			if err := visit(id); err != nil {
				return err
			}
		}
	}

	return nil
}
