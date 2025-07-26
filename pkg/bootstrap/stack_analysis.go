package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/templates"
)

// StackAnalyzer handles architect-based stack analysis for bootstrap.
//
//nolint:govet // Analysis struct, logical grouping preferred
type StackAnalyzer struct {
	specFilePath string
	llmClient    agent.LLMClient
	renderer     *templates.Renderer
	logger       *logx.Logger
}

// NewStackAnalyzer creates a new stack analyzer.
func NewStackAnalyzer(specFilePath string, llmClient agent.LLMClient) *StackAnalyzer {
	renderer, err := templates.NewRenderer()
	if err != nil {
		// For now, panic - in production we'd handle this more gracefully.
		panic(fmt.Sprintf("failed to create template renderer: %v", err))
	}

	return &StackAnalyzer{
		specFilePath: specFilePath,
		llmClient:    llmClient,
		renderer:     renderer,
		logger:       logx.NewLogger("stack-analyzer"),
	}
}

// StackAnalysisResult represents the result of stack analysis.
//
//nolint:govet // Large complex result struct, logical grouping preferred over memory optimization
type StackAnalysisResult struct {
	Analysis       string                 `json:"analysis"`
	Recommendation PlatformRecommendation `json:"recommendation"`
	Evidence       []string               `json:"evidence"`
	Assumptions    []string               `json:"assumptions"`
	Questions      []string               `json:"questions"`
	NextAction     string                 `json:"next_action"`
}

// AnalyzeStack analyzes a specification file and returns stack recommendations.
func (s *StackAnalyzer) AnalyzeStack(ctx context.Context) (*StackAnalysisResult, error) {
	// Read specification file.
	specContent, err := s.readSpecFile()
	if err != nil {
		return nil, fmt.Errorf("failed to read spec file: %w", err)
	}

	return s.AnalyzeSpecContent(ctx, specContent)
}

// AnalyzeSpecContent analyzes specification content directly and returns stack recommendations.
func (s *StackAnalyzer) AnalyzeSpecContent(ctx context.Context, specContent string) (*StackAnalysisResult, error) {
	// Generate the stack analysis prompt.
	prompt, err := s.generateStackAnalysisPrompt(specContent)
	if err != nil {
		return nil, fmt.Errorf("failed to generate prompt: %w", err)
	}

	s.logger.Info("Analyzing stack for specification content")

	// Get architect's response.
	completionReq := agent.CompletionRequest{
		Messages: []agent.CompletionMessage{
			{
				Role:    agent.RoleUser,
				Content: prompt,
			},
		},
	}

	completionResp, err := s.llmClient.Complete(ctx, completionReq)
	if err != nil {
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}

	response := completionResp.Content

	// Parse the JSON response.
	result, err := s.parseStackAnalysisResponse(response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Validate the recommendation.
	if err := ValidatePlatformRecommendation(&result.Recommendation); err != nil {
		return nil, fmt.Errorf("invalid recommendation: %w", err)
	}

	s.logger.Info("Stack analysis completed: primary=%s, confidence=%.2f, multi_stack=%t",
		result.Recommendation.Platform, result.Recommendation.Confidence, result.Recommendation.MultiStack)

	return result, nil
}

// readSpecFile reads the specification file content.
func (s *StackAnalyzer) readSpecFile() (string, error) {
	if s.specFilePath == "" {
		return "", fmt.Errorf("spec file path is required")
	}

	// Check if file exists.
	if _, err := os.Stat(s.specFilePath); os.IsNotExist(err) {
		return "", fmt.Errorf("spec file not found: %s", s.specFilePath)
	}

	// Read file content.
	content, err := os.ReadFile(s.specFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to read spec file: %w", err)
	}

	return string(content), nil
}

// generateStackAnalysisPrompt generates the LLM prompt for stack analysis.
func (s *StackAnalyzer) generateStackAnalysisPrompt(specContent string) (string, error) {
	// Prepare template data.
	templateData := &templates.TemplateData{
		TaskContent: specContent,
		Extra: map[string]any{
			"spec_file_path": s.specFilePath,
		},
	}

	// Render the stack analysis template.
	prompt, err := s.renderer.Render(templates.StateTemplate("stack_analysis.tpl.md"), templateData)
	if err != nil {
		return "", fmt.Errorf("failed to render template: %w", err)
	}

	return prompt, nil
}

// parseStackAnalysisResponse parses the LLM response into a structured result.
func (s *StackAnalyzer) parseStackAnalysisResponse(response string) (*StackAnalysisResult, error) {
	// Find JSON block in response.
	jsonStart := strings.Index(response, "```json")
	if jsonStart == -1 {
		return nil, fmt.Errorf("no JSON block found in response")
	}

	jsonStart += 7 // Skip "```json"
	jsonEnd := strings.Index(response[jsonStart:], "```")
	if jsonEnd == -1 {
		return nil, fmt.Errorf("unclosed JSON block in response")
	}

	jsonContent := strings.TrimSpace(response[jsonStart : jsonStart+jsonEnd])

	// Parse JSON.
	var result StackAnalysisResult
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &result, nil
}

// RequiresHumanApproval checks if the recommendation requires human approval.
func (s *StackAnalyzer) RequiresHumanApproval(result *StackAnalysisResult) bool {
	return RequiresHumanApproval(&result.Recommendation)
}

// GetFallbackRecommendation returns a safe fallback recommendation.
func (s *StackAnalyzer) GetFallbackRecommendation(reason string) *PlatformRecommendation {
	return &PlatformRecommendation{
		Platform:   GetDefaultPlatform(),
		Confidence: 0.1,
		Rationale:  fmt.Sprintf("Fallback recommendation: %s", reason),
		MultiStack: false,
		Platforms:  []string{GetDefaultPlatform()},
		Versions:   map[string]string{},
	}
}

// StackAnalysisConfig holds configuration for stack analysis.
//
//nolint:govet // Configuration struct, logical grouping preferred
type StackAnalysisConfig struct {
	SpecFilePath     string  `json:"spec_file_path"`
	MinConfidence    float64 `json:"min_confidence"`    // Minimum confidence to proceed
	RequireApproval  bool    `json:"require_approval"`  // Always require human approval
	AllowUnstable    bool    `json:"allow_unstable"`    // Allow unstable platforms
	FallbackPlatform string  `json:"fallback_platform"` // Platform to use if analysis fails
	MaxRetries       int     `json:"max_retries"`       // Max retries for LLM calls
	TimeoutSeconds   int     `json:"timeout_seconds"`   // Timeout for LLM calls
}

// DefaultStackAnalysisConfig returns default configuration.
func DefaultStackAnalysisConfig() *StackAnalysisConfig {
	return &StackAnalysisConfig{
		SpecFilePath:     "",
		MinConfidence:    0.3,
		RequireApproval:  false,
		AllowUnstable:    false,
		FallbackPlatform: "null",
		MaxRetries:       3,
		TimeoutSeconds:   30,
	}
}

// FindSpecFile searches for a specification file in the project.
func FindSpecFile(projectRoot string) (string, error) {
	// Common spec file patterns.
	patterns := []string{
		"*spec*.md",
		"*spec*.txt",
		"*requirements*.md",
		"*requirements*.txt",
		"spec.md",
		"specification.md",
		"requirements.md",
		"README.md",
	}

	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(projectRoot, pattern))
		if err != nil {
			continue
		}

		if len(matches) > 0 {
			return matches[0], nil
		}

		// Also search in common directories.
		for _, dir := range []string{"docs", "spec", "requirements"} {
			matches, err := filepath.Glob(filepath.Join(projectRoot, dir, pattern))
			if err != nil {
				continue
			}

			if len(matches) > 0 {
				return matches[0], nil
			}
		}
	}

	return "", fmt.Errorf("no specification file found in project")
}

// ValidateStackAnalysisConfig validates the stack analysis configuration.
func ValidateStackAnalysisConfig(config *StackAnalysisConfig) error {
	if config.SpecFilePath == "" {
		return fmt.Errorf("spec_file_path is required")
	}

	if config.MinConfidence < 0.0 || config.MinConfidence > 1.0 {
		return fmt.Errorf("min_confidence must be between 0.0 and 1.0")
	}

	if config.FallbackPlatform != "" && !IsSupportedPlatform(config.FallbackPlatform) {
		return fmt.Errorf("fallback_platform '%s' is not supported", config.FallbackPlatform)
	}

	if config.MaxRetries < 1 {
		return fmt.Errorf("max_retries must be at least 1")
	}

	if config.TimeoutSeconds < 1 {
		return fmt.Errorf("timeout_seconds must be at least 1")
	}

	return nil
}
