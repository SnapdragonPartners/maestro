// Package utils provides utilities for managing .maestro directory and user instruction files.
package utils

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// MaestroDir is the directory name for maestro-specific files.
	MaestroDir = ".maestro"

	// CommonInstructionsFile is the filename for common user instructions.
	CommonInstructionsFile = "COMMON.md"
	// CoderInstructionsFile is the filename for coder-specific user instructions.
	CoderInstructionsFile = "CODER.md"
	// ArchitectInstructionsFile is the filename for architect-specific user instructions.
	ArchitectInstructionsFile = "ARCHITECT.md"

	// UserInstructionsTokenLimit is the token limit for user instruction files (2000 tokens ~ 8000 chars).
	UserInstructionsTokenLimit = 2000
	// UserInstructionsCharLimit is the character limit for user instruction files (~8000 chars).
	UserInstructionsCharLimit = 8000
)

// UserInstructions holds the content of user instruction files.
type UserInstructions struct {
	Common    string
	Coder     string
	Architect string
}

// CreateMaestroDirectory creates the .maestro directory structure with empty instruction files.
func CreateMaestroDirectory(workDir string) error {
	maestroPath := filepath.Join(workDir, MaestroDir)

	// Create .maestro directory
	if err := os.MkdirAll(maestroPath, 0755); err != nil {
		return fmt.Errorf("failed to create .maestro directory: %w", err)
	}

	// Create subdirectories for states and stories
	statesPath := filepath.Join(maestroPath, "states")
	if err := os.MkdirAll(statesPath, 0755); err != nil {
		return fmt.Errorf("failed to create .maestro/states directory: %w", err)
	}

	storiesPath := filepath.Join(maestroPath, "stories")
	if err := os.MkdirAll(storiesPath, 0755); err != nil {
		return fmt.Errorf("failed to create .maestro/stories directory: %w", err)
	}

	// Create empty instruction files
	instructionFiles := map[string]string{
		CommonInstructionsFile:    "# Common Instructions\n\n<!-- Add instructions that apply to both CODER and ARCHITECT agents here -->\n<!-- Maximum 2,000 tokens (≈8,000 characters) -->\n",
		CoderInstructionsFile:     "# Coder Instructions\n\n<!-- Add coding-specific instructions here -->\n<!-- Examples: coding standards, file naming conventions, testing requirements -->\n<!-- Maximum 2,000 tokens (≈8,000 characters) -->\n",
		ArchitectInstructionsFile: "# Architect Instructions\n\n<!-- Add architecture-specific instructions here -->\n<!-- Examples: design patterns, system constraints, review criteria -->\n<!-- Maximum 2,000 tokens (≈8,000 characters) -->\n",
	}

	for filename, defaultContent := range instructionFiles {
		filePath := filepath.Join(maestroPath, filename)

		// Only create if file doesn't exist
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if err := os.WriteFile(filePath, []byte(defaultContent), 0644); err != nil {
				return fmt.Errorf("failed to create %s: %w", filename, err)
			}
		}
	}

	// Create README.md with usage instructions
	readmePath := filepath.Join(maestroPath, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		readmeContent := `# .maestro Directory

This directory contains maestro-specific files for customizing agent behavior.

## User Instruction Files

- **COMMON.md**: Instructions that apply to both CODER and ARCHITECT agents
- **CODER.md**: Instructions specific to the coding agent (coding standards, patterns, etc.)
- **ARCHITECT.md**: Instructions specific to the architect agent (design patterns, review criteria, etc.)

Each instruction file has a limit of 2,000 tokens (≈8,000 characters) to prevent prompt bloat.

## System Directories

- **states/**: Agent state persistence (moved from workspace root)
- **stories/**: Generated stories and specifications (moved from workspace root)

## Usage

Add your project-specific instructions to the appropriate .md files. The content will be automatically appended to agent system prompts.
`

		if err := os.WriteFile(readmePath, []byte(readmeContent), 0644); err != nil {
			return fmt.Errorf("failed to create README.md: %w", err)
		}
	}

	return nil
}

// LoadUserInstructions loads user instruction files from the .maestro directory.
// Returns empty strings for missing/empty files, returns error for unreadable files.
func LoadUserInstructions(workDir string) (*UserInstructions, error) {
	maestroPath := filepath.Join(workDir, MaestroDir)

	instructions := &UserInstructions{}

	// Load each instruction file
	files := map[string]*string{
		CommonInstructionsFile:    &instructions.Common,
		CoderInstructionsFile:     &instructions.Coder,
		ArchitectInstructionsFile: &instructions.Architect,
	}

	for filename, target := range files {
		filePath := filepath.Join(maestroPath, filename)

		// Check if file exists
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			// File doesn't exist, use empty string
			*target = ""
			continue
		}

		// Read file content
		content, err := os.ReadFile(filePath)
		if err != nil {
			// Unreadable file is a fatal error
			return nil, fmt.Errorf("failed to read %s: %w (please check file permissions)", filename, err)
		}

		contentStr := string(content)

		// Check token/character limits
		if len(contentStr) > UserInstructionsCharLimit {
			return nil, fmt.Errorf("%s exceeds character limit of %d (current: %d)",
				filename, UserInstructionsCharLimit, len(contentStr))
		}

		// Use tiktoken for more accurate token counting
		tokenCount := CountTokensSimple(contentStr)
		if tokenCount > UserInstructionsTokenLimit {
			return nil, fmt.Errorf("%s exceeds token limit of %d (current: %d)",
				filename, UserInstructionsTokenLimit, tokenCount)
		}

		*target = contentStr
	}

	return instructions, nil
}

// FormatUserInstructions formats user instructions for inclusion in system prompts.
// Returns empty string if no instructions are provided.
func FormatUserInstructions(instructions *UserInstructions, agentType string) string {
	if instructions == nil {
		return ""
	}

	var parts []string

	// Add common instructions
	if instructions.Common != "" {
		parts = append(parts, "---\n## Common Instructions\n"+instructions.Common)
	}

	// Add agent-specific instructions
	switch agentType {
	case "CODER":
		if instructions.Coder != "" {
			parts = append(parts, "---\n## Agent-Specific Instructions\n"+instructions.Coder)
		}
	case "ARCHITECT":
		if instructions.Architect != "" {
			parts = append(parts, "---\n## Agent-Specific Instructions\n"+instructions.Architect)
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return "\n" + joinStrings(parts, "\n")
}

// joinStrings joins strings with a separator (helper to avoid extra imports).
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	if len(strs) == 1 {
		return strs[0]
	}

	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
