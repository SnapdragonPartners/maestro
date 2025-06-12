package architect

import (
	"testing"
)

func TestParseSpecAnalysisJSON(t *testing.T) {
	driver := NewDriver("test-architect", nil, "/tmp/test", "/tmp/stories")

	// Test valid LLM response
	validResponse := `
Here is my analysis of the specification:

{
  "analysis": "The specification contains 2 main requirements for user management",
  "requirements": [
    {
      "title": "User Registration",
      "description": "Implement user registration with email and password",
      "acceptance_criteria": [
        "POST /register endpoint accepts JSON",
        "Email validation is performed",
        "Password is hashed before storage"
      ],
      "estimated_points": 3,
      "dependencies": []
    },
    {
      "title": "User Login",
      "description": "Allow users to authenticate and receive JWT tokens",
      "acceptance_criteria": [
        "POST /login endpoint for authentication",
        "Returns JWT token on success",
        "Invalid credentials return 401"
      ],
      "estimated_points": 2,
      "dependencies": ["User Registration"]
    }
  ],
  "next_action": "STORY_GENERATION"
}

This analysis extracts the core requirements for implementation.
`

	requirements, err := driver.parseSpecAnalysisJSON(validResponse)
	if err != nil {
		t.Fatalf("Failed to parse valid LLM response: %v", err)
	}

	if len(requirements) != 2 {
		t.Errorf("Expected 2 requirements, got %d", len(requirements))
	}

	// Check first requirement
	req1 := requirements[0]
	if req1.Title != "User Registration" {
		t.Errorf("Expected title 'User Registration', got '%s'", req1.Title)
	}

	if req1.EstimatedPoints != 3 {
		t.Errorf("Expected 3 points, got %d", req1.EstimatedPoints)
	}

	if len(req1.AcceptanceCriteria) != 3 {
		t.Errorf("Expected 3 acceptance criteria, got %d", len(req1.AcceptanceCriteria))
	}

	// Check second requirement
	req2 := requirements[1]
	if req2.Title != "User Login" {
		t.Errorf("Expected title 'User Login', got '%s'", req2.Title)
	}

	if len(req2.Dependencies) != 1 || req2.Dependencies[0] != "User Registration" {
		t.Errorf("Expected dependency on 'User Registration', got %v", req2.Dependencies)
	}
}

func TestParseSpecAnalysisJSONInvalid(t *testing.T) {
	driver := NewDriver("test-architect", nil, "/tmp/test", "/tmp/stories")

	testCases := []struct {
		name     string
		response string
	}{
		{
			name:     "No JSON",
			response: "This is just plain text with no JSON structure",
		},
		{
			name:     "Invalid JSON",
			response: `{"invalid": json syntax without closing`,
		},
		{
			name:     "Empty requirements",
			response: `{"analysis": "No requirements found", "requirements": [], "next_action": "STORY_GENERATION"}`,
		},
		{
			name:     "Missing required fields",
			response: `{"analysis": "Test", "requirements": [{"title": ""}], "next_action": "STORY_GENERATION"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := driver.parseSpecAnalysisJSON(tc.response)
			if err == nil {
				t.Errorf("Expected error for invalid response, but got none")
			}
		})
	}
}

func TestParseSpecAnalysisJSONDefaults(t *testing.T) {
	driver := NewDriver("test-architect", nil, "/tmp/test", "/tmp/stories")

	// Test response with edge cases that should get defaults
	responseWithDefaults := `{
  "analysis": "Test analysis",
  "requirements": [
    {
      "title": "Valid Requirement",
      "description": "Test description",
      "acceptance_criteria": [],
      "estimated_points": 10,
      "dependencies": []
    },
    {
      "title": "Another Requirement", 
      "description": "Another test",
      "acceptance_criteria": ["One criterion"],
      "estimated_points": -1,
      "dependencies": []
    }
  ],
  "next_action": "STORY_GENERATION"
}`

	requirements, err := driver.parseSpecAnalysisJSON(responseWithDefaults)
	if err != nil {
		t.Fatalf("Failed to parse response with defaults: %v", err)
	}

	if len(requirements) != 2 {
		t.Errorf("Expected 2 requirements, got %d", len(requirements))
	}

	// Check that invalid points got defaulted
	req1 := requirements[0]
	if req1.EstimatedPoints != 2 {
		t.Errorf("Expected invalid points (10) to default to 2, got %d", req1.EstimatedPoints)
	}

	// Check that empty acceptance criteria got defaults
	if len(req1.AcceptanceCriteria) != 3 {
		t.Errorf("Expected default acceptance criteria (3), got %d", len(req1.AcceptanceCriteria))
	}

	// Check that negative points got defaulted
	req2 := requirements[1]
	if req2.EstimatedPoints != 2 {
		t.Errorf("Expected invalid points (-1) to default to 2, got %d", req2.EstimatedPoints)
	}

	// Check that existing acceptance criteria is preserved
	if len(req2.AcceptanceCriteria) != 1 {
		t.Errorf("Expected existing acceptance criteria (1) to be preserved, got %d", len(req2.AcceptanceCriteria))
	}
}
