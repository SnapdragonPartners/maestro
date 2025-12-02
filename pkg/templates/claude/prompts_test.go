package claude

import (
	"strings"
	"testing"
)

func TestRenderQASection_Nil(t *testing.T) {
	result := renderQASection(nil)
	if result != "" {
		t.Errorf("expected empty string for nil QAPair, got: %s", result)
	}
}

func TestRenderQASection_EmptyQuestion(t *testing.T) {
	qa := &QAPair{
		Question: "",
		Answer:   "some answer",
	}
	result := renderQASection(qa)
	if result != "" {
		t.Errorf("expected empty string for empty question, got: %s", result)
	}
}

func TestRenderQASection_EmptyAnswer(t *testing.T) {
	qa := &QAPair{
		Question: "some question",
		Answer:   "",
	}
	result := renderQASection(qa)
	if result != "" {
		t.Errorf("expected empty string for empty answer, got: %s", result)
	}
}

func TestRenderQASection_ValidQA(t *testing.T) {
	qa := &QAPair{
		Question: "What is the database schema?",
		Answer:   "The schema uses PostgreSQL with three tables.",
	}
	result := renderQASection(qa)

	// Check that it contains the expected content
	if !strings.Contains(result, "Architect Clarification") {
		t.Error("expected result to contain 'Architect Clarification'")
	}
	if !strings.Contains(result, qa.Question) {
		t.Errorf("expected result to contain question: %s", qa.Question)
	}
	if !strings.Contains(result, qa.Answer) {
		t.Errorf("expected result to contain answer: %s", qa.Answer)
	}
}

func TestRenderPlanningInput_WithQA(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}

	data := &TemplateData{
		StoryID:       "story-123",
		StoryTitle:    "Test Story",
		StoryContent:  "Implement feature X",
		KnowledgePack: "Project uses Go",
		LastQA: &QAPair{
			Question: "Should I use interface{}?",
			Answer:   "Use generics instead.",
		},
	}

	result := renderer.RenderPlanningInput(data)

	// Check that Q&A is included
	if !strings.Contains(result, "Architect Clarification") {
		t.Error("expected planning input to contain Q&A section")
	}
	if !strings.Contains(result, data.LastQA.Question) {
		t.Error("expected planning input to contain question")
	}
	if !strings.Contains(result, data.LastQA.Answer) {
		t.Error("expected planning input to contain answer")
	}
}

func TestRenderPlanningInput_WithoutQA(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}

	data := &TemplateData{
		StoryID:       "story-123",
		StoryTitle:    "Test Story",
		StoryContent:  "Implement feature X",
		KnowledgePack: "Project uses Go",
	}

	result := renderer.RenderPlanningInput(data)

	// Check that Q&A section is not present
	if strings.Contains(result, "Architect Clarification") {
		t.Error("expected planning input to NOT contain Q&A section when no Q&A provided")
	}
	// But story content should be there
	if !strings.Contains(result, data.StoryContent) {
		t.Error("expected planning input to contain story content")
	}
}

func TestRenderCodingInput_WithQA(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}

	data := &TemplateData{
		StoryID:    "story-123",
		StoryTitle: "Test Story",
		Plan:       "1. Create interface\n2. Implement",
		LastQA: &QAPair{
			Question: "What error handling pattern?",
			Answer:   "Use sentinel errors.",
		},
	}

	result := renderer.RenderCodingInput(data)

	// Check that Q&A is included
	if !strings.Contains(result, "Architect Clarification") {
		t.Error("expected coding input to contain Q&A section")
	}
	if !strings.Contains(result, data.LastQA.Question) {
		t.Error("expected coding input to contain question")
	}
	if !strings.Contains(result, data.LastQA.Answer) {
		t.Error("expected coding input to contain answer")
	}
}

func TestRenderCodingInput_WithoutQA(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}

	data := &TemplateData{
		StoryID:    "story-123",
		StoryTitle: "Test Story",
		Plan:       "1. Create interface\n2. Implement",
	}

	result := renderer.RenderCodingInput(data)

	// Check that Q&A section is not present
	if strings.Contains(result, "Architect Clarification") {
		t.Error("expected coding input to NOT contain Q&A section when no Q&A provided")
	}
	// But plan should be there
	if !strings.Contains(result, data.Plan) {
		t.Error("expected coding input to contain plan")
	}
}

func TestRenderer_RenderPlanningPrompt(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}

	data := &TemplateData{
		StoryID:       "story-123",
		StoryTitle:    "Test Story",
		WorkspacePath: "/workspace",
	}

	result, err := renderer.RenderPlanningPrompt(data)
	if err != nil {
		t.Fatalf("failed to render planning prompt: %v", err)
	}

	// Check that mode was set
	if data.Mode != "PLANNING" {
		t.Errorf("expected mode to be PLANNING, got: %s", data.Mode)
	}

	// Check result is not empty
	if result == "" {
		t.Error("expected non-empty planning prompt")
	}
}

func TestRenderer_RenderCodingPrompt(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}

	data := &TemplateData{
		StoryID:       "story-123",
		StoryTitle:    "Test Story",
		WorkspacePath: "/workspace",
	}

	result, err := renderer.RenderCodingPrompt(data)
	if err != nil {
		t.Fatalf("failed to render coding prompt: %v", err)
	}

	// Check that mode was set
	if data.Mode != "CODING" {
		t.Errorf("expected mode to be CODING, got: %s", data.Mode)
	}

	// Check result is not empty
	if result == "" {
		t.Error("expected non-empty coding prompt")
	}
}
