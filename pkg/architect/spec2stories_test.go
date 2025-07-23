package architect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSpecParser(t *testing.T) {
	storiesDir := "/tmp/test_stories"
	parser := NewSpecParser(storiesDir)

	if parser == nil {
		t.Fatal("NewSpecParser returned nil")
	}

	if parser.storiesDir != storiesDir {
		t.Errorf("Expected storiesDir %s, got %s", storiesDir, parser.storiesDir)
	}
}

func TestParseSpecContent(t *testing.T) {
	parser := NewSpecParser("/tmp/test")

	specContent := `# Project Specification

## Overview
This is a test specification.

## User Authentication
Implement user authentication system.

**Acceptance Criteria**
- Users can register with email/password
- Users can login
- JWT tokens are issued

## API Endpoints
Create REST API endpoints.

1. GET /users - list users
2. POST /users - create user
3. GET /users/{id} - get user by ID

## Database Schema
Set up database tables.

- User table with id, email, password_hash
- Timestamps for created_at, updated_at
`

	requirements, err := parser.parseSpecContent(specContent)
	if err != nil {
		t.Fatalf("Failed to parse spec content: %v", err)
	}

	if len(requirements) != 3 {
		t.Errorf("Expected 3 requirements, got %d", len(requirements))
	}

	// Check first requirement.
	req1 := requirements[0]
	if req1.Title != "User Authentication" {
		t.Errorf("Expected title 'User Authentication', got '%s'", req1.Title)
	}

	if len(req1.AcceptanceCriteria) != 3 {
		t.Errorf("Expected 3 acceptance criteria, got %d", len(req1.AcceptanceCriteria))
	}

	expectedCriteria := []string{
		"Users can register with email/password",
		"Users can login",
		"JWT tokens are issued",
	}

	for i, expected := range expectedCriteria {
		if i < len(req1.AcceptanceCriteria) && req1.AcceptanceCriteria[i] != expected {
			t.Errorf("Expected criteria '%s', got '%s'", expected, req1.AcceptanceCriteria[i])
		}
	}

	// Check second requirement (numbered lists)
	req2 := requirements[1]
	if req2.Title != "API Endpoints" {
		t.Errorf("Expected title 'API Endpoints', got '%s'", req2.Title)
	}

	if len(req2.Description) == 0 {
		t.Error("Expected description for API Endpoints requirement")
	}

	// Check third requirement.
	req3 := requirements[2]
	if req3.Title != "Database Schema" {
		t.Errorf("Expected title 'Database Schema', got '%s'", req3.Title)
	}
}

func TestShouldSkipHeader(t *testing.T) {
	parser := NewSpecParser("/tmp/test")

	testCases := []struct {
		title    string
		expected bool
	}{
		{"Table of Contents", true},
		{"Overview", true},
		{"User Authentication", false},
		{"API Design", false},
		{"Glossary", true},
		{"Background Information", true},
		{"Implementation Details", false},
	}

	for _, tc := range testCases {
		result := parser.shouldSkipHeader(tc.title)
		if result != tc.expected {
			t.Errorf("For title '%s', expected %v, got %v", tc.title, tc.expected, result)
		}
	}
}

func TestEstimatePoints(t *testing.T) {
	parser := NewSpecParser("/tmp/test")

	testCases := []struct {
		title    string
		expected int
	}{
		{"Simple Health Check", 1},
		{"User Authentication System", 3},
		{"Database Migration", 3},
		{"REST API Endpoint", 2},
		{"Component Configuration", 2},
		{"Basic Validation", 1},
		{"Security Integration", 3},
	}

	for _, tc := range testCases {
		result := parser.estimatePoints(tc.title, 0, []string{})
		if result != tc.expected {
			t.Errorf("For title '%s', expected %d points, got %d", tc.title, tc.expected, result)
		}
	}
}

func TestFindNextStoryID(t *testing.T) {
	// Create temporary directory.
	tempDir, err := os.MkdirTemp("", "test_stories")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	parser := NewSpecParser(tempDir)

	// Test with no existing files.
	nextID, err := parser.findNextStoryID()
	if err != nil {
		t.Fatalf("Failed to find next story ID: %v", err)
	}

	if nextID != 50 {
		t.Errorf("Expected next ID 50 for empty directory, got %d", nextID)
	}

	// Create some story files.
	testFiles := []string{"001.md", "002.md", "055.md", "100.md"}
	for _, filename := range testFiles {
		filePath := filepath.Join(tempDir, filename)
		err := os.WriteFile(filePath, []byte("test content"), 0644) //nolint:govet // Shadow variable acceptable in test context
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", filename, err)
		}
	}

	// Test with existing files.
	nextID, err = parser.findNextStoryID()
	if err != nil {
		t.Fatalf("Failed to find next story ID: %v", err)
	}

	if nextID != 101 {
		t.Errorf("Expected next ID 101, got %d", nextID)
	}
}

func TestGenerateStoryContent(t *testing.T) {
	parser := NewSpecParser("/tmp/test")

	req := Requirement{
		Title:       "User Authentication",
		Description: "Implement user authentication system",
		AcceptanceCriteria: []string{
			"Users can register",
			"Users can login",
		},
		EstimatedPoints: 3,
	}

	content := parser.generateStoryContent("050", &req)

	// Check front matter.
	if !strings.Contains(content, "id: 050") {
		t.Error("Content should contain story ID")
	}

	if !strings.Contains(content, `title: "User Authentication"`) {
		t.Error("Content should contain title")
	}

	if !strings.Contains(content, "est_points: 3") {
		t.Error("Content should contain estimated points")
	}

	// Check task section.
	if !strings.Contains(content, "**Task**") {
		t.Error("Content should contain Task section")
	}

	if !strings.Contains(content, req.Description) {
		t.Error("Content should contain description")
	}

	// Check acceptance criteria.
	if !strings.Contains(content, "**Acceptance Criteria**") {
		t.Error("Content should contain Acceptance Criteria section")
	}

	if !strings.Contains(content, "* Users can register") {
		t.Error("Content should contain acceptance criteria")
	}
}

func TestGenerateStoryFiles(t *testing.T) {
	// Create temporary directory.
	tempDir, err := os.MkdirTemp("", "test_stories")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	parser := NewSpecParser(tempDir)

	requirements := []Requirement{
		{
			Title:           "Health Check Endpoint",
			Description:     "Create a simple health check",
			EstimatedPoints: 1,
			AcceptanceCriteria: []string{
				"GET /health returns 200",
				"Response includes timestamp",
			},
		},
		{
			Title:           "User Registration",
			Description:     "Allow users to register",
			EstimatedPoints: 2,
			AcceptanceCriteria: []string{
				"Accept email and password",
				"Validate input",
				"Store in database",
			},
		},
	}

	storyFiles, err := parser.GenerateStoryFiles(requirements)
	if err != nil {
		t.Fatalf("Failed to generate story files: %v", err)
	}

	if len(storyFiles) != 2 {
		t.Errorf("Expected 2 story files, got %d", len(storyFiles))
	}

	// Check first story file.
	story1 := storyFiles[0]
	if story1.ID != "050" {
		t.Errorf("Expected story ID '050', got '%s'", story1.ID)
	}

	if story1.Title != "Health Check Endpoint" {
		t.Errorf("Expected title 'Health Check Endpoint', got '%s'", story1.Title)
	}

	// Verify file was actually created.
	if _, err := os.Stat(story1.FilePath); os.IsNotExist(err) { //nolint:govet // Shadow variable acceptable in test context
		t.Errorf("Story file was not created: %s", story1.FilePath)
	}

	// Check file content.
	content, err := os.ReadFile(story1.FilePath)
	if err != nil {
		t.Fatalf("Failed to read story file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "Health Check Endpoint") {
		t.Error("File content should contain story title")
	}

	if !strings.Contains(contentStr, "GET /health returns 200") {
		t.Error("File content should contain acceptance criteria")
	}
}

func TestProcessSpecFile(t *testing.T) {
	// Create temporary directories.
	tempDir, err := os.MkdirTemp("", "test_stories")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	specDir, err := os.MkdirTemp("", "test_spec")
	if err != nil {
		t.Fatalf("Failed to create spec dir: %v", err)
	}
	defer os.RemoveAll(specDir)

	// Create test spec file.
	specContent := `# Test Project

## Health Endpoint
Create a health check endpoint that returns server status.

**Acceptance Criteria**
- GET /health returns 200 OK
- Response includes timestamp
- Response includes server status

## User Authentication
Implement basic user authentication.

1. User registration with email/password
2. User login with JWT tokens
3. Password hashing with bcrypt
`

	specFile := filepath.Join(specDir, "project.md")
	err = os.WriteFile(specFile, []byte(specContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create spec file: %v", err)
	}

	parser := NewSpecParser(tempDir)
	storyFiles, err := parser.ProcessSpecFile(specFile)
	if err != nil {
		t.Fatalf("Failed to process spec file: %v", err)
	}

	if len(storyFiles) != 2 {
		t.Errorf("Expected 2 story files, got %d", len(storyFiles))
	}

	// Verify both files were created.
	for _, story := range storyFiles {
		if _, err := os.Stat(story.FilePath); os.IsNotExist(err) {
			t.Errorf("Story file was not created: %s", story.FilePath)
		}
	}

	// Check first story.
	healthStory := storyFiles[0]
	if healthStory.Title != "Health Endpoint" {
		t.Errorf("Expected title 'Health Endpoint', got '%s'", healthStory.Title)
	}

	if len(healthStory.DependsOn) != 0 {
		t.Errorf("Expected no dependencies initially, got %v", healthStory.DependsOn)
	}
}

func TestProcessSpecFileNotFound(t *testing.T) {
	parser := NewSpecParser("/tmp/test")

	_, err := parser.ProcessSpecFile("/nonexistent/file.md")
	if err == nil {
		t.Error("Expected error for nonexistent file")
	}

	if !strings.Contains(err.Error(), "failed to parse spec file") {
		t.Errorf("Expected 'failed to parse spec file' error, got: %v", err)
	}
}
