package architect

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Requirement represents a parsed requirement from the spec
type Requirement struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	EstimatedPoints    int      `json:"estimated_points"`
	Dependencies       []string `json:"dependencies,omitempty"`
}

// StoryFile represents a generated story file
type StoryFile struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	DependsOn []string `json:"depends_on"`
	EstPoints int      `json:"est_points"`
	Content   string   `json:"content"`
	FilePath  string   `json:"file_path"`
}

// SpecParser handles parsing project specifications into requirements
type SpecParser struct {
	storiesDir string
}

// NewSpecParser creates a new spec parser instance
func NewSpecParser(storiesDir string) *SpecParser {
	return &SpecParser{
		storiesDir: storiesDir,
	}
}

// ParseSpecFile reads and parses a specification file to extract requirements
func (sp *SpecParser) ParseSpecFile(specFilePath string) ([]Requirement, error) {
	content, err := os.ReadFile(specFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read spec file %s: %w", specFilePath, err)
	}

	return sp.parseSpecContent(string(content))
}

// parseSpecContent parses the spec content and extracts discrete requirements
func (sp *SpecParser) parseSpecContent(content string) ([]Requirement, error) {
	var requirements []Requirement

	lines := strings.Split(content, "\n")
	var currentRequirement *Requirement
	var inCodeBlock bool
	var inAcceptanceCriteria bool

	for i, line := range lines {
		line = strings.TrimSpace(line)

		// Track code blocks to avoid parsing content inside them
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}

		if inCodeBlock {
			continue
		}

		// Look for requirement headers (## or ### only, skip single #)
		if headerMatch := regexp.MustCompile(`^(#{2,})\s+(.+)`).FindStringSubmatch(line); headerMatch != nil {
			// Save previous requirement if exists
			if currentRequirement != nil {
				requirements = append(requirements, *currentRequirement)
			}

			// Start new requirement
			title := strings.TrimSpace(headerMatch[2])

			// Skip certain header types that aren't requirements
			if sp.shouldSkipHeader(title) {
				currentRequirement = nil
				continue
			}

			currentRequirement = &Requirement{
				Title:              title,
				Description:        "",
				AcceptanceCriteria: []string{},
				EstimatedPoints:    sp.estimatePoints(title, i, lines),
				Dependencies:       []string{},
			}
			inAcceptanceCriteria = false
		} else if currentRequirement != nil {
			// Look for acceptance criteria section
			if strings.Contains(strings.ToLower(line), "acceptance criteria") ||
				strings.Contains(strings.ToLower(line), "requirements") {
				inAcceptanceCriteria = true
				continue
			}

			// Parse bullet points as acceptance criteria or description
			if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
				criterion := strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* ")
				if criterion != "" {
					if inAcceptanceCriteria {
						currentRequirement.AcceptanceCriteria = append(currentRequirement.AcceptanceCriteria, criterion)
					} else {
						// Add to description if not in acceptance criteria
						if currentRequirement.Description != "" {
							currentRequirement.Description += "\n"
						}
						currentRequirement.Description += "- " + criterion
					}
				}
			} else if strings.HasPrefix(line, "1. ") || regexp.MustCompile(`^\d+\.\s`).MatchString(line) {
				// Handle numbered lists
				parts := regexp.MustCompile(`^\d+\.\s+(.+)`).FindStringSubmatch(line)
				if len(parts) > 1 {
					criterion := strings.TrimSpace(parts[1])
					if inAcceptanceCriteria {
						currentRequirement.AcceptanceCriteria = append(currentRequirement.AcceptanceCriteria, criterion)
					} else {
						if currentRequirement.Description != "" {
							currentRequirement.Description += "\n"
						}
						currentRequirement.Description += "- " + criterion
					}
				}
			} else if line != "" && !strings.HasPrefix(line, "#") {
				// Add to description if it's regular text
				if currentRequirement.Description != "" {
					currentRequirement.Description += "\n"
				}
				currentRequirement.Description += line
			}
		}
	}

	// Don't forget the last requirement
	if currentRequirement != nil {
		requirements = append(requirements, *currentRequirement)
	}

	return requirements, nil
}

// shouldSkipHeader determines if a header should be skipped (not a requirement)
func (sp *SpecParser) shouldSkipHeader(title string) bool {
	skipPatterns := []string{
		"table of contents",
		"overview",
		"introduction",
		"background",
		"assumptions",
		"glossary",
		"references",
		"appendix",
		"notes",
		"changelog",
		"version",
		"project specification",
		"test project",
	}

	lowerTitle := strings.ToLower(title)
	for _, pattern := range skipPatterns {
		if strings.Contains(lowerTitle, pattern) {
			return true
		}
	}

	return false
}

// estimatePoints provides a simple heuristic for estimating story points
func (sp *SpecParser) estimatePoints(title string, lineNum int, lines []string) int {
	// Simple heuristic based on keywords and content complexity
	lowerTitle := strings.ToLower(title)

	// High complexity keywords (3 points)
	highComplexity := []string{
		"integration", "authentication", "security", "database", "migration",
		"api gateway", "microservice", "deployment", "monitoring", "analytics",
	}

	// Medium complexity keywords (2 points)
	mediumComplexity := []string{
		"endpoint", "service", "component", "module", "interface",
		"configuration", "logging", "testing",
	}

	// Check for high complexity
	for _, keyword := range highComplexity {
		if strings.Contains(lowerTitle, keyword) {
			return 3
		}
	}

	// Check for medium complexity
	for _, keyword := range mediumComplexity {
		if strings.Contains(lowerTitle, keyword) {
			return 2
		}
	}

	// Count lines of content to estimate complexity
	contentLines := 0
	for i := lineNum + 1; i < len(lines) && i < lineNum+20; i++ {
		line := strings.TrimSpace(lines[i])
		if line != "" && !strings.HasPrefix(line, "#") {
			contentLines++
		}
		if strings.HasPrefix(line, "#") {
			break // Next section
		}
	}

	if contentLines > 10 {
		return 3
	} else if contentLines > 5 {
		return 2
	}

	return 1 // Default to simple story
}

// GenerateStoryFiles creates story markdown files from requirements
func (sp *SpecParser) GenerateStoryFiles(requirements []Requirement) ([]StoryFile, error) {
	if err := os.MkdirAll(sp.storiesDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create stories directory %s: %w", sp.storiesDir, err)
	}

	// Find the next available story ID
	nextID, err := sp.findNextStoryID()
	if err != nil {
		return nil, fmt.Errorf("failed to determine next story ID: %w", err)
	}

	var storyFiles []StoryFile

	for i, req := range requirements {
		storyID := fmt.Sprintf("%03d", nextID+i)

		// Generate story content
		storyContent := sp.generateStoryContent(storyID, req)

		// Create file path
		fileName := fmt.Sprintf("%s.md", storyID)
		filePath := filepath.Join(sp.storiesDir, fileName)

		// Write story file
		if err := os.WriteFile(filePath, []byte(storyContent), 0644); err != nil {
			return nil, fmt.Errorf("failed to write story file %s: %w", filePath, err)
		}

		storyFile := StoryFile{
			ID:        storyID,
			Title:     req.Title,
			DependsOn: req.Dependencies,
			EstPoints: req.EstimatedPoints,
			Content:   storyContent,
			FilePath:  filePath,
		}

		storyFiles = append(storyFiles, storyFile)
	}

	return storyFiles, nil
}

// findNextStoryID finds the next available story ID by scanning existing files
func (sp *SpecParser) findNextStoryID() (int, error) {
	// Ensure directory exists
	if _, err := os.Stat(sp.storiesDir); os.IsNotExist(err) {
		return 50, nil // Start at 050 for Phase 4 stories
	}

	entries, err := os.ReadDir(sp.storiesDir)
	if err != nil {
		return 0, fmt.Errorf("failed to read stories directory: %w", err)
	}

	maxID := 49 // Start at 049 so next will be 050
	storyPattern := regexp.MustCompile(`^(\d{3})\.md$`)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		matches := storyPattern.FindStringSubmatch(entry.Name())
		if len(matches) > 1 {
			if id, err := strconv.Atoi(matches[1]); err == nil {
				if id > maxID {
					maxID = id
				}
			}
		}
	}

	return maxID + 1, nil
}

// generateStoryContent creates the markdown content for a story file
func (sp *SpecParser) generateStoryContent(storyID string, req Requirement) string {
	var content strings.Builder

	// Front matter
	content.WriteString("---\n")
	content.WriteString(fmt.Sprintf("id: %s\n", storyID))
	content.WriteString(fmt.Sprintf("title: \"%s\"\n", req.Title))

	// Write dependencies
	content.WriteString("depends_on: [")
	if len(req.Dependencies) > 0 {
		for i, dep := range req.Dependencies {
			if i > 0 {
				content.WriteString(", ")
			}
			content.WriteString(fmt.Sprintf("\"%s\"", dep))
		}
	}
	content.WriteString("]\n")

	content.WriteString(fmt.Sprintf("est_points: %d\n", req.EstimatedPoints))
	content.WriteString("---\n\n")

	// Task section
	content.WriteString("**Task**\n")
	if req.Description != "" {
		content.WriteString(req.Description)
	} else {
		content.WriteString(fmt.Sprintf("Implement: %s", req.Title))
	}
	content.WriteString("\n\n")

	// Acceptance criteria section
	content.WriteString("**Acceptance Criteria**\n")
	if len(req.AcceptanceCriteria) > 0 {
		for _, criterion := range req.AcceptanceCriteria {
			content.WriteString(fmt.Sprintf("* %s\n", criterion))
		}
	} else {
		content.WriteString("* Implementation completes successfully\n")
		content.WriteString("* All tests pass\n")
		content.WriteString("* Code follows project conventions\n")
	}

	return content.String()
}

// ProcessSpecFile is the main entry point for spec processing
func (sp *SpecParser) ProcessSpecFile(specFilePath string) ([]StoryFile, error) {
	// Parse the specification file
	requirements, err := sp.ParseSpecFile(specFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse spec file: %w", err)
	}

	if len(requirements) == 0 {
		return nil, fmt.Errorf("no requirements found in spec file")
	}

	// Generate story files
	storyFiles, err := sp.GenerateStoryFiles(requirements)
	if err != nil {
		return nil, fmt.Errorf("failed to generate story files: %w", err)
	}

	return storyFiles, nil
}
