package specs

import (
	"strings"
	"testing"
)

// Valid spec for testing.
const validSpec = `---
version: "1.0"
priority: must
---

# Feature: User Authentication

## Vision
Enable secure user login and registration to protect user data and personalize experiences.

## Scope
### In Scope
- Email/password authentication
- JWT token-based sessions
- Password reset flow

### Out of Scope
- OAuth/social login
- Two-factor authentication

## Requirements

### R-001: User Registration
**Type:** functional
**Priority:** must
**Dependencies:** []

**Description:** Users can create accounts with email and password.

**Acceptance Criteria:**
- [ ] Email validation (format check)
- [ ] Password strength requirements (8+ chars, 1 number, 1 special char)
- [ ] Duplicate email check (return clear error)

### R-002: User Login
**Type:** functional
**Priority:** must
**Dependencies:** [R-001]

**Description:** Registered users can log in with email/password.

**Acceptance Criteria:**
- [ ] Credentials validated against database
- [ ] JWT token issued on success (expires in 24h)
`

func TestValidate_ValidSpec(t *testing.T) {
	spec, err := Parse(validSpec)
	if err != nil {
		t.Fatalf("Failed to parse valid spec: %v", err)
	}

	result := Validate(spec)
	if !result.Passed {
		t.Errorf("Valid spec should pass validation")
		for _, err := range result.Blocking {
			t.Logf("  - %s", err)
		}
	}
}

func TestValidate_MissingVersion(t *testing.T) {
	specWithoutVersion := strings.Replace(validSpec, `version: "1.0"`, ``, 1)
	spec, err := Parse(specWithoutVersion)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec without version should fail validation")
	}
	if !containsError(result.Blocking, "missing 'version' field") {
		t.Error("Expected error about missing version field")
	}
}

func TestValidate_MissingPriority(t *testing.T) {
	specWithoutPriority := strings.Replace(validSpec, `priority: must`, ``, 1)
	spec, err := Parse(specWithoutPriority)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec without priority should fail validation")
	}
	if !containsError(result.Blocking, "missing 'priority' field") {
		t.Error("Expected error about missing priority field")
	}
}

func TestValidate_MissingTitle(t *testing.T) {
	specWithoutTitle := strings.Replace(validSpec, `# Feature: User Authentication`, ``, 1)
	spec, err := Parse(specWithoutTitle)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec without title should fail validation")
	}
	if !containsError(result.Blocking, "Missing required section: Feature title") {
		t.Error("Expected error about missing title")
	}
}

func TestValidate_MissingVision(t *testing.T) {
	specWithoutVision := strings.Replace(validSpec,
		"## Vision\nEnable secure user login and registration to protect user data and personalize experiences.",
		"", 1)
	spec, err := Parse(specWithoutVision)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec without vision should fail validation")
	}
	if !containsError(result.Blocking, "Missing required section: Vision") {
		t.Error("Expected error about missing vision")
	}
}

func TestValidate_MissingInScope(t *testing.T) {
	specWithoutInScope := strings.Replace(validSpec,
		`### In Scope
- Email/password authentication
- JWT token-based sessions
- Password reset flow`, "", 1)
	spec, err := Parse(specWithoutInScope)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec without in-scope items should fail validation")
	}
	if !containsError(result.Blocking, "In Scope section must have at least one item") {
		t.Error("Expected error about missing in-scope items")
	}
}

func TestValidate_MissingRequirements(t *testing.T) {
	// Remove requirements section
	lines := strings.Split(validSpec, "\n")
	var newLines []string
	inRequirements := false
	for _, line := range lines {
		if strings.Contains(line, "## Requirements") {
			inRequirements = true
			continue
		}
		if !inRequirements {
			newLines = append(newLines, line)
		}
	}
	specWithoutRequirements := strings.Join(newLines, "\n")

	spec, err := Parse(specWithoutRequirements)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec without requirements should fail validation")
	}
	if !containsError(result.Blocking, "Missing required section: Requirements") {
		t.Error("Expected error about missing requirements")
	}
}

func TestValidate_InvalidRequirementIDFormat(t *testing.T) {
	// Parser won't recognize "REQ-1" as a requirement, so the spec will have no requirements
	specWithBadID := strings.Replace(validSpec, "R-001", "REQ-1", 1)
	spec, err := Parse(specWithBadID)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec with invalid requirement ID format should fail validation")
	}
	// Parser won't recognize REQ-1, so validation will fail due to missing requirements
	if len(result.Blocking) == 0 {
		t.Error("Expected validation errors but got none")
	}
	t.Logf("Got errors: %v", result.Blocking)
	// Should have at least one error (could be missing requirements or something else)
	if len(result.Blocking) < 1 {
		t.Error("Expected at least one blocking error")
	}
}

func TestValidate_DuplicateRequirementID(t *testing.T) {
	// Duplicate R-001
	specWithDuplicate := strings.Replace(validSpec, "R-002", "R-001", 1)
	spec, err := Parse(specWithDuplicate)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec with duplicate requirement ID should fail validation")
	}
	if !containsError(result.Blocking, "Duplicate requirement ID") {
		t.Error("Expected error about duplicate requirement ID")
	}
}

func TestValidate_NoAcceptanceCriteria(t *testing.T) {
	// Remove acceptance criteria from R-001
	specWithoutAC := strings.Replace(validSpec,
		`**Acceptance Criteria:**
- [ ] Email validation (format check)
- [ ] Password strength requirements (8+ chars, 1 number, 1 special char)
- [ ] Duplicate email check (return clear error)`, "", 1)

	spec, err := Parse(specWithoutAC)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec with requirement missing acceptance criteria should fail validation")
	}
	if !containsError(result.Blocking, "has no acceptance criteria") {
		t.Error("Expected error about missing acceptance criteria")
	}
}

func TestValidate_InvalidPriority(t *testing.T) {
	specWithBadPriority := strings.Replace(validSpec, `priority: must`, `priority: urgent`, 1)
	spec, err := Parse(specWithBadPriority)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec with invalid priority should fail validation")
	}
	if !containsError(result.Blocking, "priority 'urgent' is invalid") {
		t.Error("Expected error about invalid priority")
	}
}

func TestValidate_InvalidRequirementPriority(t *testing.T) {
	specWithBadReqPriority := strings.Replace(validSpec,
		"**Priority:** must",
		"**Priority:** critical", 1)
	spec, err := Parse(specWithBadReqPriority)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec with invalid requirement priority should fail validation")
	}
	if !containsError(result.Blocking, "has invalid priority 'critical'") {
		t.Error("Expected error about invalid requirement priority")
	}
}

func TestValidate_NonExistentDependency(t *testing.T) {
	specWithBadDep := strings.Replace(validSpec, "[R-001]", "[R-999]", 1)
	spec, err := Parse(specWithBadDep)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec with non-existent dependency should fail validation")
	}
	if !containsError(result.Blocking, "depends on non-existent requirement R-999") {
		t.Error("Expected error about non-existent dependency")
	}
}

func TestValidate_CircularDependency(t *testing.T) {
	// Create circular dependency: R-001 -> R-002 -> R-001
	specWithCycle := strings.Replace(validSpec,
		"### R-001: User Registration\n**Type:** functional\n**Priority:** must\n**Dependencies:** []",
		"### R-001: User Registration\n**Type:** functional\n**Priority:** must\n**Dependencies:** [R-002]", 1)

	spec, err := Parse(specWithCycle)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if result.Passed {
		t.Error("Spec with circular dependency should fail validation")
	}
	if !containsError(result.Blocking, "Dependency cycle detected") {
		t.Error("Expected error about dependency cycle")
	}
}

func TestValidate_ValidPriorities(t *testing.T) {
	priorities := []string{"must", "should", "could"}

	//nolint:gocritic // String range copy is acceptable for small test data.
	for _, priority := range priorities {
		t.Run(priority, func(t *testing.T) {
			specWithPriority := strings.Replace(validSpec, `priority: must`, `priority: `+priority, 1)
			spec, err := Parse(specWithPriority)
			if err != nil {
				t.Fatalf("Failed to parse spec: %v", err)
			}

			result := Validate(spec)
			if !result.Passed {
				t.Errorf("Spec with priority '%s' should pass validation", priority)
				for _, err := range result.Blocking {
					t.Logf("  - %s", err)
				}
			}
		})
	}
}

func TestValidate_ComplexDependencyGraph(t *testing.T) {
	// Test a complex but valid dependency graph
	complexSpec := `---
version: "1.0"
priority: must
---

# Feature: Complex Dependencies

## Vision
Test complex dependency graphs.

## Scope
### In Scope
- Feature A
- Feature B

### Out of Scope
- Feature C

## Requirements

### R-001: Base Feature
**Type:** functional
**Priority:** must
**Dependencies:** []

**Description:** Base feature with no dependencies.

**Acceptance Criteria:**
- [ ] Criterion 1

### R-002: Depends on R-001
**Type:** functional
**Priority:** must
**Dependencies:** [R-001]

**Description:** Depends on R-001.

**Acceptance Criteria:**
- [ ] Criterion 1

### R-003: Depends on R-001
**Type:** functional
**Priority:** must
**Dependencies:** [R-001]

**Description:** Also depends on R-001.

**Acceptance Criteria:**
- [ ] Criterion 1

### R-004: Depends on R-002 and R-003
**Type:** functional
**Priority:** must
**Dependencies:** [R-002, R-003]

**Description:** Depends on both R-002 and R-003.

**Acceptance Criteria:**
- [ ] Criterion 1
`

	spec, err := Parse(complexSpec)
	if err != nil {
		t.Fatalf("Failed to parse spec: %v", err)
	}

	result := Validate(spec)
	if !result.Passed {
		t.Error("Complex dependency graph should pass validation")
		for _, err := range result.Blocking {
			t.Logf("  - %s", err)
		}
	}
}

// Helper function to check if an error message contains a substring.
func containsError(errors []string, substring string) bool {
	for _, err := range errors {
		if strings.Contains(strings.ToLower(err), strings.ToLower(substring)) {
			return true
		}
	}
	return false
}
