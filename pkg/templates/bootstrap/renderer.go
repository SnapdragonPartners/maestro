// Package bootstrap provides template rendering for bootstrap specifications.
package bootstrap

import (
	"embed"
	"fmt"
	"strings"
	"text/template"

	"orchestrator/pkg/bootstrap"
	"orchestrator/pkg/workspace"
)

const templateFileName = "bootstrap.tpl.md"

//go:embed bootstrap.tpl.md
var bootstrapTemplateFS embed.FS

// Renderer handles rendering of bootstrap specification templates.
type Renderer struct {
	template *template.Template
}

// NewRenderer creates a new bootstrap template renderer.
func NewRenderer() (*Renderer, error) {
	templateContent, err := bootstrapTemplateFS.ReadFile(templateFileName)
	if err != nil {
		return nil, fmt.Errorf("failed to read bootstrap template: %w", err)
	}

	tmpl, err := template.New("bootstrap").Parse(string(templateContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse bootstrap template: %w", err)
	}

	return &Renderer{template: tmpl}, nil
}

// RenderBootstrapSpec generates a bootstrap specification from verification failures.
func (r *Renderer) RenderBootstrapSpec(projectName, platform, containerImage string, failures []workspace.BootstrapFailure) (string, error) {
	// Get platform display name
	platformDisplayName := platform
	if supportedPlatform, exists := bootstrap.PlatformWhitelist[platform]; exists {
		platformDisplayName = supportedPlatform.DisplayName
	}

	// Create template data
	data := NewTemplateData(projectName, platform, platformDisplayName, containerImage, "", failures)
	data.TemplateName = templateFileName

	// Load and render the language pack (ignore errors - template renders without pack)
	_, _ = data.SetPack()

	// Render the template
	var buf strings.Builder
	if err := r.template.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render bootstrap template: %w", err)
	}

	return buf.String(), nil
}

// RenderBootstrapSpecEnhanced generates an enhanced bootstrap specification with git repo URL.
func (r *Renderer) RenderBootstrapSpecEnhanced(projectName, platform, containerImage, gitRepoURL, dockerfilePath string, failures []workspace.BootstrapFailure) (string, error) {
	// Get platform display name
	platformDisplayName := platform
	if supportedPlatform, exists := bootstrap.PlatformWhitelist[platform]; exists {
		platformDisplayName = supportedPlatform.DisplayName
	}

	// Create template data with git repo URL
	data := NewTemplateData(projectName, platform, platformDisplayName, containerImage, gitRepoURL, failures)
	data.TemplateName = templateFileName

	// Set dockerfile path if provided
	if dockerfilePath != "" {
		data.DockerfilePath = dockerfilePath
	}

	// Load and render the language pack (ignore errors - template renders without pack)
	_, _ = data.SetPack()

	// Render the template
	var buf strings.Builder
	if err := r.template.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render bootstrap template: %w", err)
	}

	return buf.String(), nil
}

// RenderPlatformSpecificBootstrap renders a bootstrap spec with platform-specific customizations.
func (r *Renderer) RenderPlatformSpecificBootstrap(projectName, platform, containerImage string, failures []workspace.BootstrapFailure, customizations map[string]any) (string, error) {
	// Start with base template
	baseSpec, err := r.RenderBootstrapSpec(projectName, platform, containerImage, failures)
	if err != nil {
		return "", fmt.Errorf("failed to render base bootstrap spec: %w", err)
	}

	// Apply platform-specific customizations
	if customizations != nil {
		// For now, return base spec
		// Future enhancement: apply customizations based on platform
		return baseSpec, nil
	}

	return baseSpec, nil
}

// GenerateBootstrapSpecFromReport creates a bootstrap specification from a verification report.
func GenerateBootstrapSpecFromReport(projectName, platform, containerImage string, report *workspace.VerifyReport) (string, error) {
	if !report.RequiresBootstrap() {
		return "", fmt.Errorf("no bootstrap required - verification passed")
	}

	renderer, err := NewRenderer()
	if err != nil {
		return "", fmt.Errorf("failed to create bootstrap renderer: %w", err)
	}

	return renderer.RenderBootstrapSpec(projectName, platform, containerImage, report.BootstrapFailures)
}

// GenerateBootstrapSpecFromReportEnhanced creates an enhanced bootstrap specification with git repo URL and config data from global singleton.
// In bootstrap mode, always generate a spec regardless of verification results - empty repos need setup too.
func GenerateBootstrapSpecFromReportEnhanced(projectName, platform, containerImage, gitRepoURL, dockerfilePath string, report *workspace.VerifyReport) (string, error) {
	renderer, err := NewRenderer()
	if err != nil {
		return "", fmt.Errorf("failed to create bootstrap renderer: %w", err)
	}

	// Use bootstrap failures if they exist, otherwise pass empty list for clean repos
	var failures []workspace.BootstrapFailure
	if report != nil {
		failures = report.BootstrapFailures
	}

	return renderer.RenderBootstrapSpecWithConfig(projectName, platform, containerImage, gitRepoURL, dockerfilePath, failures)
}

// RenderBootstrapSpecWithConfig generates a bootstrap specification with config data from global singleton.
func (r *Renderer) RenderBootstrapSpecWithConfig(projectName, platform, containerImage, gitRepoURL, dockerfilePath string, failures []workspace.BootstrapFailure) (string, error) {
	// Get platform display name
	platformDisplayName := platform
	if supportedPlatform, exists := bootstrap.PlatformWhitelist[platform]; exists {
		platformDisplayName = supportedPlatform.DisplayName
	}

	// Create template data with config data from global singleton
	data := NewTemplateDataWithConfig(projectName, platform, platformDisplayName, containerImage, gitRepoURL, dockerfilePath, failures)
	data.TemplateName = templateFileName

	// Load and render the language pack (ignore errors - template renders without pack)
	_, _ = data.SetPack()

	// Render the template
	var buf strings.Builder
	if err := r.template.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render bootstrap template: %w", err)
	}

	return buf.String(), nil
}
