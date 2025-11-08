package specs

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// Section field names.
	fieldDescription = "description"
	fieldAcceptance  = "acceptance"
)

var (
	// Regex patterns for parsing.
	frontmatterDelimiter = regexp.MustCompile(`^---\s*$`)
	titlePattern         = regexp.MustCompile(`^#\s+Feature:\s+(.+)$`)
	visionPattern        = regexp.MustCompile(`^##\s+Vision\s*$`)
	scopePattern         = regexp.MustCompile(`^##\s+Scope\s*$`)
	inScopePattern       = regexp.MustCompile(`^###\s+In Scope\s*$`)
	outOfScopePattern    = regexp.MustCompile(`^###\s+Out of Scope\s*$`)
	requirementsPattern  = regexp.MustCompile(`^##\s+Requirements\s*$`)
	requirementPattern   = regexp.MustCompile(`^###\s+(R-\d{3}):\s+(.+)$`)
	listItemPattern      = regexp.MustCompile(`^-\s+(.+)$`)
	fieldPattern         = regexp.MustCompile(`^\*\*([^:]+):\*\*\s*(.*)$`) // Changed to match any characters except colon.
	checkboxPattern      = regexp.MustCompile(`^-\s+\[[ x]\]\s+(.+)$`)
)

// Parse parses a markdown specification into a SpecPack.
func Parse(markdown string) (*SpecPack, error) {
	spec := &SpecPack{
		RawMarkdown: markdown,
	}

	// Split into frontmatter and body
	frontmatter, body, err := splitFrontmatter(markdown)
	if err != nil {
		return nil, fmt.Errorf("failed to split frontmatter: %w", err)
	}

	// Parse YAML frontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), spec); err != nil {
		return nil, fmt.Errorf("failed to parse YAML frontmatter: %w", err)
	}

	// Parse markdown body
	if err := parseBody(body, spec); err != nil {
		return nil, fmt.Errorf("failed to parse markdown body: %w", err)
	}

	return spec, nil
}

// splitFrontmatter splits markdown into YAML frontmatter and body.
//
//nolint:gocritic // Separate return values are clearer than a struct for this simple case.
func splitFrontmatter(markdown string) (frontmatter string, body string, err error) {
	lines := strings.Split(markdown, "\n")
	if len(lines) < 3 {
		return "", "", fmt.Errorf("markdown too short to contain frontmatter")
	}

	// Check for opening delimiter
	if !frontmatterDelimiter.MatchString(strings.TrimSpace(lines[0])) {
		return "", "", fmt.Errorf("missing frontmatter opening delimiter (---)")
	}

	// Find closing delimiter
	closingIdx := -1
	for i := 1; i < len(lines); i++ {
		if frontmatterDelimiter.MatchString(strings.TrimSpace(lines[i])) {
			closingIdx = i
			break
		}
	}

	if closingIdx == -1 {
		return "", "", fmt.Errorf("missing frontmatter closing delimiter (---)")
	}

	// Extract frontmatter and body
	frontmatter = strings.Join(lines[1:closingIdx], "\n")
	body = strings.Join(lines[closingIdx+1:], "\n")

	return frontmatter, body, nil
}

// parseBody parses the markdown body into structured sections.
//
//nolint:gocyclo,cyclop // This function intentionally handles complex markdown parsing logic.
func parseBody(body string, spec *SpecPack) error {
	scanner := bufio.NewScanner(strings.NewReader(body))
	lineNum := 0
	var currentSection string
	var currentRequirement *Requirement
	var currentField string

	for scanner.Scan() {
		line := scanner.Text()
		lineNum++

		// Title (# Feature: ...)
		if matches := titlePattern.FindStringSubmatch(line); matches != nil {
			spec.Title = strings.TrimSpace(matches[1])
			currentSection = ""
			continue
		}

		// Vision section
		if visionPattern.MatchString(line) {
			currentSection = "vision"
			continue
		}

		// Scope section
		if scopePattern.MatchString(line) {
			currentSection = "scope"
			continue
		}

		// In Scope subsection
		if inScopePattern.MatchString(line) {
			currentSection = "in_scope"
			continue
		}

		// Out of Scope subsection
		if outOfScopePattern.MatchString(line) {
			currentSection = "out_of_scope"
			continue
		}

		// Requirements section
		if requirementsPattern.MatchString(line) {
			currentSection = "requirements"
			currentRequirement = nil
			continue
		}

		// Individual requirement (### R-001: Title)
		if matches := requirementPattern.FindStringSubmatch(line); matches != nil {
			// Save previous requirement if exists
			if currentRequirement != nil {
				spec.Requirements = append(spec.Requirements, *currentRequirement)
			}

			// Start new requirement
			currentRequirement = &Requirement{
				ID:         matches[1],
				Title:      strings.TrimSpace(matches[2]),
				LineNumber: lineNum,
			}
			currentField = ""
			continue
		}

		// Parse requirement fields
		if currentRequirement != nil {
			if matches := fieldPattern.FindStringSubmatch(line); matches != nil {
				fieldName := strings.ToLower(matches[1])
				fieldValue := strings.TrimSpace(matches[2])

				switch fieldName {
				case "type":
					currentRequirement.Type = fieldValue
				case "priority":
					currentRequirement.Priority = fieldValue
				case "dependencies":
					currentRequirement.Dependencies = parseDependencies(fieldValue)
				case fieldDescription:
					currentRequirement.Description = fieldValue
					currentField = fieldDescription
				case "acceptance criteria", fieldAcceptance:
					currentField = fieldAcceptance
				}
				continue
			}

			// Acceptance criteria checkboxes
			if currentField == fieldAcceptance {
				if matches := checkboxPattern.FindStringSubmatch(line); matches != nil {
					criterion := strings.TrimSpace(matches[1])
					currentRequirement.AcceptanceCriteria = append(currentRequirement.AcceptanceCriteria, criterion)
					continue
				}
				// If it's not a checkbox and we're in acceptance, continue to next line
				if strings.TrimSpace(line) == "" {
					continue
				}
			}

			// Continuation of description
			if currentField == fieldDescription && strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "**") {
				currentRequirement.Description += " " + strings.TrimSpace(line)
				continue
			}
		}

		// Parse list items in sections
		if matches := listItemPattern.FindStringSubmatch(line); matches != nil {
			item := strings.TrimSpace(matches[1])
			switch currentSection {
			case "in_scope":
				spec.InScope = append(spec.InScope, item)
			case "out_of_scope":
				spec.OutOfScope = append(spec.OutOfScope, item)
			}
			continue
		}

		// Parse vision text (multi-line)
		if currentSection == "vision" && strings.TrimSpace(line) != "" {
			if spec.Vision == "" {
				spec.Vision = strings.TrimSpace(line)
			} else {
				spec.Vision += " " + strings.TrimSpace(line)
			}
		}
	}

	// Save last requirement if exists
	if currentRequirement != nil {
		spec.Requirements = append(spec.Requirements, *currentRequirement)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}

	return nil
}

// parseDependencies parses dependency list from string like "[R-001, R-002]" or "[]".
func parseDependencies(value string) []string {
	value = strings.TrimSpace(value)
	if value == "[]" {
		return []string{}
	}

	// Remove brackets
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")

	// Split by comma
	parts := strings.Split(value, ",")
	deps := make([]string, 0, len(parts))
	for _, part := range parts {
		dep := strings.TrimSpace(part)
		if dep != "" {
			deps = append(deps, dep)
		}
	}

	return deps
}
