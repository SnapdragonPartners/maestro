package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateMaestroDirectory(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "maestro_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test creating maestro directory structure
	err = CreateMaestroDirectory(tempDir)
	if err != nil {
		t.Fatalf("CreateMaestroDirectory failed: %v", err)
	}

	// Verify .maestro directory exists
	maestroPath := filepath.Join(tempDir, MaestroDir)
	if _, err := os.Stat(maestroPath); os.IsNotExist(err) {
		t.Error(".maestro directory was not created")
	}

	// Verify subdirectories exist
	statesPath := filepath.Join(maestroPath, "states")
	if _, err := os.Stat(statesPath); os.IsNotExist(err) {
		t.Error(".maestro/states directory was not created")
	}

	storiesPath := filepath.Join(maestroPath, "stories")
	if _, err := os.Stat(storiesPath); os.IsNotExist(err) {
		t.Error(".maestro/stories directory was not created")
	}

	// Verify instruction files exist and have default content
	instructionFiles := []string{
		CommonInstructionsFile,
		CoderInstructionsFile,
		ArchitectInstructionsFile,
		"README.md",
	}

	for _, filename := range instructionFiles {
		filePath := filepath.Join(maestroPath, filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Errorf("%s was not created", filename)
			continue
		}

		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Errorf("Failed to read %s: %v", filename, err)
			continue
		}

		if len(content) == 0 {
			t.Errorf("%s is empty", filename)
		}
	}
}

func TestLoadUserInstructions(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "maestro_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create maestro directory first
	err = CreateMaestroDirectory(tempDir)
	if err != nil {
		t.Fatalf("CreateMaestroDirectory failed: %v", err)
	}

	// Test loading default (empty) instructions
	instructions, err := LoadUserInstructions(tempDir)
	if err != nil {
		t.Fatalf("LoadUserInstructions failed: %v", err)
	}

	if instructions == nil {
		t.Fatal("LoadUserInstructions returned nil")
	}

	// Test with custom content
	maestroPath := filepath.Join(tempDir, MaestroDir)
	customCommon := "# Custom Common Instructions\nUse proper error handling."
	customCoder := "# Custom Coder Instructions\nAlways write tests."

	err = os.WriteFile(filepath.Join(maestroPath, CommonInstructionsFile), []byte(customCommon), 0644)
	if err != nil {
		t.Fatalf("Failed to write custom common instructions: %v", err)
	}

	err = os.WriteFile(filepath.Join(maestroPath, CoderInstructionsFile), []byte(customCoder), 0644)
	if err != nil {
		t.Fatalf("Failed to write custom coder instructions: %v", err)
	}

	// Load again
	instructions, err = LoadUserInstructions(tempDir)
	if err != nil {
		t.Fatalf("LoadUserInstructions failed with custom content: %v", err)
	}

	if !strings.Contains(instructions.Common, "Custom Common Instructions") {
		t.Error("Common instructions not loaded correctly")
	}

	if !strings.Contains(instructions.Coder, "Custom Coder Instructions") {
		t.Error("Coder instructions not loaded correctly")
	}
}

func TestLoadUserInstructionsTokenLimit(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "maestro_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create maestro directory
	err = CreateMaestroDirectory(tempDir)
	if err != nil {
		t.Fatalf("CreateMaestroDirectory failed: %v", err)
	}

	// Create content that exceeds character limit
	maestroPath := filepath.Join(tempDir, MaestroDir)
	tooLongContent := strings.Repeat("This is a very long instruction. ", 500) // ~17,000 chars

	err = os.WriteFile(filepath.Join(maestroPath, CoderInstructionsFile), []byte(tooLongContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write long content: %v", err)
	}

	// Should fail due to character limit
	_, err = LoadUserInstructions(tempDir)
	if err == nil {
		t.Error("Expected error for content exceeding character limit")
	}

	if !strings.Contains(err.Error(), "exceeds character limit") {
		t.Errorf("Expected character limit error, got: %v", err)
	}
}

func TestFormatUserInstructions(t *testing.T) {
	tests := []struct {
		name         string
		instructions *UserInstructions
		agentType    string
		expectEmpty  bool
		expectCommon bool
		expectAgent  bool
	}{
		{
			name:         "nil instructions",
			instructions: nil,
			agentType:    "CODER",
			expectEmpty:  true,
		},
		{
			name: "empty instructions",
			instructions: &UserInstructions{
				Common: "",
				Coder:  "",
			},
			agentType:   "CODER",
			expectEmpty: true,
		},
		{
			name: "common only",
			instructions: &UserInstructions{
				Common: "Common instructions",
				Coder:  "",
			},
			agentType:    "CODER",
			expectCommon: true,
		},
		{
			name: "coder only",
			instructions: &UserInstructions{
				Common: "",
				Coder:  "Coder instructions",
			},
			agentType:   "CODER",
			expectAgent: true,
		},
		{
			name: "both common and coder",
			instructions: &UserInstructions{
				Common: "Common instructions",
				Coder:  "Coder instructions",
			},
			agentType:    "CODER",
			expectCommon: true,
			expectAgent:  true,
		},
		{
			name: "architect instructions",
			instructions: &UserInstructions{
				Common:    "Common instructions",
				Architect: "Architect instructions",
			},
			agentType:    "ARCHITECT",
			expectCommon: true,
			expectAgent:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatUserInstructions(tt.instructions, tt.agentType)

			if tt.expectEmpty && result != "" {
				t.Errorf("Expected empty result, got: %s", result)
			}

			if !tt.expectEmpty && result == "" {
				t.Error("Expected non-empty result, got empty string")
			}

			if tt.expectCommon && !strings.Contains(result, "Common Instructions") {
				t.Error("Expected common instructions in result")
			}

			if tt.expectAgent && !strings.Contains(result, "Agent-Specific Instructions") {
				t.Error("Expected agent-specific instructions in result")
			}
		})
	}
}
