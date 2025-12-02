// Package claude provides prompt templates for Claude Code integration.
package claude

import (
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed *.tpl.md
var claudeTemplateFS embed.FS

// QAPair represents a question-answer pair from architect clarification.
type QAPair struct {
	Question string
	Answer   string
}

// TemplateData contains data for Claude Code prompt templates.
type TemplateData struct {
	// Mode is the execution mode (PLANNING or CODING)
	Mode string

	// StoryID is the unique identifier for the story
	StoryID string

	// StoryTitle is the title of the story
	StoryTitle string

	// StoryContent is the full story content
	StoryContent string

	// Plan is the approved plan (for CODING mode)
	Plan string

	// WorkspacePath is the path to the workspace
	WorkspacePath string

	// ProjectInfo contains project context
	ProjectInfo string

	// KnowledgePack contains relevant project knowledge
	KnowledgePack string

	// LastQA contains the last Q&A pair when resuming from QUESTION state
	LastQA *QAPair
}

// Renderer handles rendering of Claude Code prompt templates.
type Renderer struct {
	planningTemplate *template.Template
	codingTemplate   *template.Template
}

// NewRenderer creates a new Claude template renderer.
func NewRenderer() (*Renderer, error) {
	renderer := &Renderer{}

	// Load planning template
	planningContent, err := claudeTemplateFS.ReadFile("planning.tpl.md")
	if err != nil {
		return nil, fmt.Errorf("failed to read planning template: %w", err)
	}
	renderer.planningTemplate, err = template.New("planning").Parse(string(planningContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse planning template: %w", err)
	}

	// Load coding template
	codingContent, err := claudeTemplateFS.ReadFile("coding.tpl.md")
	if err != nil {
		return nil, fmt.Errorf("failed to read coding template: %w", err)
	}
	renderer.codingTemplate, err = template.New("coding").Parse(string(codingContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse coding template: %w", err)
	}

	return renderer, nil
}

// RenderPlanningPrompt generates the system prompt for planning mode.
func (r *Renderer) RenderPlanningPrompt(data *TemplateData) (string, error) {
	data.Mode = "PLANNING"
	var sb strings.Builder
	if err := r.planningTemplate.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("failed to render planning template: %w", err)
	}
	return sb.String(), nil
}

// RenderCodingPrompt generates the system prompt for coding mode.
func (r *Renderer) RenderCodingPrompt(data *TemplateData) (string, error) {
	data.Mode = "CODING"
	var sb strings.Builder
	if err := r.codingTemplate.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("failed to render coding template: %w", err)
	}
	return sb.String(), nil
}

// RenderPlanningInput generates the initial input for planning mode.
func (r *Renderer) RenderPlanningInput(data *TemplateData) string {
	qaSection := renderQASection(data.LastQA)

	return fmt.Sprintf(`# Story to Implement

**Story ID:** %s
**Title:** %s

## Story Content

%s

## Project Knowledge

%s
%s
---

Analyze this story and create a detailed implementation plan. When ready, call maestro_submit_plan with your plan.`,
		data.StoryID, data.StoryTitle, data.StoryContent, data.KnowledgePack, qaSection)
}

// RenderCodingInput generates the initial input for coding mode.
func (r *Renderer) RenderCodingInput(data *TemplateData) string {
	qaSection := renderQASection(data.LastQA)

	return fmt.Sprintf(`# Approved Implementation Plan

## Story
**ID:** %s
**Title:** %s

## Plan

%s
%s
---

Implement the code according to this approved plan. When complete, call maestro_done with a summary.`,
		data.StoryID, data.StoryTitle, data.Plan, qaSection)
}

// renderQASection formats the Q&A section for inclusion in prompts.
func renderQASection(qa *QAPair) string {
	if qa == nil || qa.Question == "" || qa.Answer == "" {
		return ""
	}
	return fmt.Sprintf(`
## Architect Clarification

You previously asked the architect a question and received an answer:

**Question:** %s

**Answer:** %s

Continue your work with this information.

`, qa.Question, qa.Answer)
}
