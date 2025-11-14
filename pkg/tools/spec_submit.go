package tools

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/pkg/config"
	"orchestrator/pkg/specs"
	"orchestrator/pkg/templates"
)

// SpecSubmitTool allows PM agent to submit finalized specifications.
type SpecSubmitTool struct {
	projectDir string
}

// NewSpecSubmitTool creates a new spec submit tool instance.
func NewSpecSubmitTool(projectDir string) *SpecSubmitTool {
	return &SpecSubmitTool{
		projectDir: projectDir,
	}
}

// Definition returns the tool's definition in Claude API format.
func (s *SpecSubmitTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "spec_submit",
		Description: "Submit the finalized specification for user review (architect will provide feedback on the spec later). If bootstrap is required, prerequisite sections will be automatically prepended.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"markdown": {
					Type:        "string",
					Description: "The complete specification in markdown format (flexible format - architect will review)",
				},
				"summary": {
					Type:        "string",
					Description: "Brief summary of the specification (1-2 sentences)",
				},
			},
			Required: []string{"markdown", "summary"},
		},
	}
}

// Name returns the tool identifier.
func (s *SpecSubmitTool) Name() string {
	return "spec_submit"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (s *SpecSubmitTool) PromptDocumentation() string {
	return `- **spec_submit** - Submit finalized specification for user review
  - Parameters: markdown (required), summary (required)
  - Accepts flexible markdown format - architect will review and provide feedback
  - Use when you have completed the specification interview and drafted the full spec
  - If bootstrap requirements detected, they will be automatically prepended
  - If bootstrap needed but not configured, returns error (call bootstrap tool first)`
}

// Exec executes the spec submit operation.
//
//nolint:cyclop // Bootstrap detection and template rendering adds complexity
func (s *SpecSubmitTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract markdown parameter.
	markdown, ok := args["markdown"]
	if !ok {
		return nil, fmt.Errorf("markdown parameter is required")
	}

	markdownStr, ok := markdown.(string)
	if !ok {
		return nil, fmt.Errorf("markdown must be a string")
	}

	if markdownStr == "" {
		return nil, fmt.Errorf("markdown cannot be empty")
	}

	// Extract summary parameter.
	summary, ok := args["summary"]
	if !ok {
		return nil, fmt.Errorf("summary parameter is required")
	}

	summaryStr, ok := summary.(string)
	if !ok {
		return nil, fmt.Errorf("summary must be a string")
	}

	if summaryStr == "" {
		return nil, fmt.Errorf("summary cannot be empty")
	}

	// Check if bootstrap is required
	detector := NewBootstrapDetector(s.projectDir)
	bootstrapReqs, err := detector.Detect(ctx)
	if err != nil {
		// Non-fatal - just log and continue without bootstrap
		// (bootstrap detection is best-effort)
		bootstrapReqs = nil
	}

	// If bootstrap is required, check if project is configured
	finalMarkdown := markdownStr
	if bootstrapReqs != nil && len(bootstrapReqs.MissingComponents) > 0 {
		// Bootstrap is required - check if config has project info
		cfg, cfgErr := config.GetConfig()
		if cfgErr != nil {
			return nil, fmt.Errorf("bootstrap required but failed to load config: %w", cfgErr)
		}

		// Check if project info is configured
		if cfg.Project.Name == "" || cfg.Project.PrimaryPlatform == "" {
			return nil, fmt.Errorf("bootstrap required (missing: %v) but project not configured - call bootstrap tool first with project_name, git_url, and platform",
				bootstrapReqs.MissingComponents)
		}

		// Check if git is configured (if repository missing)
		hasRepoMissing := false
		for _, comp := range bootstrapReqs.MissingComponents {
			if comp == "repository" {
				hasRepoMissing = true
				break
			}
		}
		if hasRepoMissing && (cfg.Git == nil || cfg.Git.RepoURL == "") {
			return nil, fmt.Errorf("bootstrap required (missing repository) but git not configured - call bootstrap tool first")
		}

		// Project is configured - render bootstrap template and prepend
		renderer, rendErr := templates.NewRenderer()
		if rendErr != nil {
			return nil, fmt.Errorf("failed to create template renderer: %w", rendErr)
		}

		// Build template data for bootstrap prerequisites
		templateData := &templates.TemplateData{
			Extra: map[string]any{
				"BootstrapRequired":  true,
				"MissingComponents":  bootstrapReqs.MissingComponents,
				"DetectedPlatform":   bootstrapReqs.DetectedPlatform,
				"PlatformConfidence": bootstrapReqs.PlatformConfidence,
				"HasRepository":      cfg.Git != nil && cfg.Git.RepoURL != "",
				"NeedsDockerfile":    contains(bootstrapReqs.MissingComponents, "dockerfile"),
				"NeedsMakefile":      contains(bootstrapReqs.MissingComponents, "makefile"),
			},
		}

		// Render bootstrap prerequisites template
		bootstrapMarkdown, renderErr := renderer.Render(templates.PMBootstrapPrerequisitesTemplate, templateData)
		if renderErr != nil {
			return nil, fmt.Errorf("failed to render bootstrap prerequisites: %w", renderErr)
		}

		// Prepend bootstrap prerequisites to user's spec
		finalMarkdown = strings.TrimSpace(bootstrapMarkdown) + "\n\n" + strings.TrimSpace(markdownStr)
	}

	// Parse the specification to extract basic metadata (but don't enforce strict validation).
	// The architect will review the spec and provide feedback if needed.
	spec, err := specs.Parse(finalMarkdown)

	// Build metadata (best effort - use empty values if parsing failed)
	metadata := map[string]any{}
	if err == nil && spec != nil {
		metadata = map[string]any{
			"title":              spec.Title,
			"version":            spec.Version,
			"priority":           spec.Priority,
			"requirements_count": len(spec.Requirements),
		}
	}

	// Accept the spec and let architect review - no strict validation here.
	// This allows PM flexibility in spec format, and architect can request changes via feedback loop.
	return map[string]any{
		"success":       true,
		"message":       "Specification accepted and ready for user review",
		"summary":       summaryStr,
		"spec_markdown": finalMarkdown, // Store final markdown with bootstrap prerequisites
		"metadata":      metadata,
		// Signal to PM driver to transition to PREVIEW state
		"preview_ready": true,
	}, nil
}

// contains checks if a slice contains a string.
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
