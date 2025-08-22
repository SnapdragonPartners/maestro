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

//go:embed *.tpl.md
var bootstrapTemplateFS embed.FS

// Renderer handles rendering of bootstrap specification templates.
type Renderer struct {
	templates map[string]*template.Template
}

// NewRenderer creates a new bootstrap template renderer.
func NewRenderer() (*Renderer, error) {
	renderer := &Renderer{
		templates: make(map[string]*template.Template),
	}

	// Load bootstrap templates
	templateFiles := []string{"bootstrap.tpl.md", "golang.tpl.md"}

	for _, filename := range templateFiles {
		templateContent, err := bootstrapTemplateFS.ReadFile(filename)
		if err != nil {
			return nil, fmt.Errorf("failed to read template %s: %w", filename, err)
		}

		tmpl, err := template.New(filename).Parse(string(templateContent))
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", filename, err)
		}

		renderer.templates[filename] = tmpl
	}

	return renderer, nil
}

// RenderBootstrapSpec generates a bootstrap specification from verification failures.
func (r *Renderer) RenderBootstrapSpec(projectName, platform, containerImage string, failures []workspace.BootstrapFailure) (string, error) {
	// Get platform display name
	platformDisplayName := platform
	if supportedPlatform, exists := bootstrap.PlatformWhitelist[platform]; exists {
		platformDisplayName = supportedPlatform.DisplayName
	}

	// Create template data (legacy without git repo URL)
	data := NewTemplateData(projectName, platform, platformDisplayName, containerImage, "", failures)

	// Select template based on platform
	templateName := r.selectTemplateForPlatform(platform)
	data.TemplateName = templateName

	// Get the template
	tmpl, exists := r.templates[templateName]
	if !exists {
		return "", fmt.Errorf("template %s not found", templateName)
	}

	// Render the template
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
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

	// Set dockerfile path if provided
	if dockerfilePath != "" {
		data.DockerfilePath = dockerfilePath
	}

	// Select template based on platform
	templateName := r.selectTemplateForPlatform(platform)
	data.TemplateName = templateName

	// Get the template
	tmpl, exists := r.templates[templateName]
	if !exists {
		return "", fmt.Errorf("template %s not found", templateName)
	}

	// Render the template
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render bootstrap template: %w", err)
	}

	return buf.String(), nil
}

// RenderPlatformSpecificBootstrap renders a bootstrap spec with platform-specific customizations.
func (r *Renderer) RenderPlatformSpecificBootstrap(projectName, platform, containerImage string, failures []workspace.BootstrapFailure, customizations map[string]interface{}) (string, error) {
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

// GetTemplateNames returns the available template names.
func (r *Renderer) GetTemplateNames() []string {
	names := make([]string, 0, len(r.templates))
	for name := range r.templates {
		names = append(names, name)
	}
	return names
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

	// Select template based on platform
	templateName := r.selectTemplateForPlatform(platform)
	data.TemplateName = templateName

	// Get the template
	tmpl, exists := r.templates[templateName]
	if !exists {
		return "", fmt.Errorf("template %s not found", templateName)
	}

	// Render the template
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render bootstrap template: %w", err)
	}

	return buf.String(), nil
}

// selectTemplateForPlatform returns the appropriate template filename for the given platform.
func (r *Renderer) selectTemplateForPlatform(platform string) string {
	switch platform {
	case "go":
		return "golang.tpl.md"
	default:
		return "bootstrap.tpl.md"
	}
}
