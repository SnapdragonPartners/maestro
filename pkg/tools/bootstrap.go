package tools

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/mirror"
	"orchestrator/pkg/templates/packs"
	"orchestrator/pkg/workspace"
)

// BootstrapTool allows PM agent to configure bootstrap requirements for new projects.
// This tool validates and stores project metadata in config.json, creates git mirror,
// and refreshes agent workspaces.
type BootstrapTool struct {
	logger     *logx.Logger
	projectDir string
}

// NewBootstrapTool creates a new bootstrap tool instance.
// projectDir is the project root (where config.json lives).
// The second parameter is ignored (kept for backwards compatibility with registry).
func NewBootstrapTool(projectDir, _ string) *BootstrapTool {
	return &BootstrapTool{
		logger:     logx.NewLogger("bootstrap-tool"),
		projectDir: projectDir,
	}
}

// Definition returns the tool's definition in Claude API format.
func (b *BootstrapTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "bootstrap",
		Description: "Configure bootstrap requirements for a new project. IMPORTANT: You must ask the user for these values first - never make up or infer project details. Only call this after the user has provided: project name, GitHub repository URL, and primary platform.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"project_name": {
					Type:        "string",
					Description: "The name of the project (used in config and documentation)",
				},
				"git_url": {
					Type:        "string",
					Description: "GitHub repository URL (format: https://github.com/user/repo)",
				},
				"platform": {
					Type:        "string",
					Description: "Primary development platform (e.g., 'go', 'python', 'node', 'rust')",
				},
			},
			Required: []string{"project_name", "git_url", "platform"},
		},
	}
}

// Name returns the tool identifier.
func (b *BootstrapTool) Name() string {
	return "bootstrap"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (b *BootstrapTool) PromptDocumentation() string {
	return `- **bootstrap** - Configure bootstrap requirements for a new project
  - **CRITICAL**: You MUST ask the user for these values first - never make them up or infer them
  - Parameters:
    - project_name (string, required): Project name (ask the user)
    - git_url (string, required): GitHub repository URL (ask the user for https://github.com/user/repo format)
    - platform (string, required): Primary platform like go, python, node, rust (ask the user or infer from their description)
  - Only call after gathering all required information from the user
  - Must be called before spec_submit if project is not yet configured
  - Updates config.json with project metadata`
}

// Exec executes the bootstrap configuration.
//
//nolint:cyclop // Bootstrap involves validation, config updates, mirror creation, and workspace refresh
func (b *BootstrapTool) Exec(ctx context.Context, params map[string]any) (*ExecResult, error) {
	// Extract and validate project_name
	projectName, ok := params["project_name"].(string)
	if !ok || projectName == "" {
		return nil, fmt.Errorf("project_name parameter is required")
	}

	// Extract and validate git_url
	gitURL, ok := params["git_url"].(string)
	if !ok || gitURL == "" {
		return nil, fmt.Errorf("git_url parameter is required")
	}

	// Validate git URL format
	parsedURL, err := url.Parse(gitURL)
	if err != nil {
		return nil, fmt.Errorf("invalid git_url format: %w", err)
	}
	if parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("git_url must use https protocol")
	}
	if !strings.HasPrefix(parsedURL.Host, "github.com") {
		return nil, fmt.Errorf("git_url must be a GitHub repository (github.com)")
	}

	// Extract and validate platform
	platform, ok := params["platform"].(string)
	if !ok || platform == "" {
		return nil, fmt.Errorf("platform parameter is required")
	}

	// Normalize platform to lowercase
	platform = strings.ToLower(platform)

	// Update project info (saves to disk automatically)
	// Note: Project description is now handled via MAESTRO.md file, not config
	projectInfo := &config.ProjectInfo{
		Name:            projectName,
		PrimaryPlatform: platform,
	}
	if updateErr := config.UpdateProject(projectInfo); updateErr != nil {
		return nil, fmt.Errorf("failed to update project info: %w", updateErr)
	}

	// Store the language pack version used for this platform
	// This enables future "pack updated since bootstrap" detection
	pack, _, packErr := packs.Get(platform)
	if packErr == nil && pack != nil {
		if updateErr := config.UpdateProjectPack(pack.Name, pack.Version); updateErr != nil {
			b.logger.Warn("Failed to store pack version: %v", updateErr)
			// Non-fatal - bootstrap continues
		} else {
			b.logger.Info("📦 Using %s Pack v%s for platform '%s'", pack.DisplayName, pack.Version, platform)
		}
	}

	// Update git config (saves to disk automatically)
	gitCfg := &config.GitConfig{
		RepoURL:       gitURL,
		TargetBranch:  "main", // Default target branch
		MirrorDir:     ".mirrors",
		BranchPattern: "story-{STORY_ID}",
		GitUserName:   "Maestro {AGENT_ID}",
		GitUserEmail:  "maestro-{AGENT_ID}@localhost",
	}
	if updateErr := config.UpdateGit(gitCfg); updateErr != nil {
		return nil, fmt.Errorf("failed to update git config: %w", updateErr)
	}

	// Create or update git mirror (validates URL is accessible)
	b.logger.Info("Creating/updating git mirror for repository: %s", gitURL)
	mirrorMgr := mirror.NewManager(b.projectDir)
	mirrorPath, mirrorErr := mirrorMgr.EnsureMirror(ctx)
	if mirrorErr != nil {
		return nil, fmt.Errorf("failed to setup git mirror: %w", mirrorErr)
	}
	b.logger.Info("✅ Git mirror ready at: %s", mirrorPath)

	// Refresh PM and architect workspaces if they already exist
	// This populates them with clones from the newly created mirror
	if refreshErr := b.refreshWorkspacesIfExist(ctx); refreshErr != nil {
		// Non-fatal - just log warning
		b.logger.Warn("Failed to refresh workspaces: %v", refreshErr)
	}

	// Return success - PM will run detection again to check what's still missing
	// PM is the sole authority on bootstrap status
	return &ExecResult{
		Content: "Bootstrap configuration saved successfully. Project configured with git mirror ready.",
		ProcessEffect: &ProcessEffect{
			Signal: SignalBootstrapComplete,
			Data: map[string]any{
				"project_name":  projectName,
				"git_url":       gitURL,
				"platform":      platform,
				"reset_context": true, // Signal PM to reset context after this tool
			},
		},
	}, nil
}

// refreshWorkspacesIfExist updates PM and architect workspaces if they already exist.
// This is called after mirror creation to populate the workspaces with clones.
// Non-fatal - returns error but bootstrap continues if this fails.
func (b *BootstrapTool) refreshWorkspacesIfExist(ctx context.Context) error {
	// Check if architect workspace directory exists
	architectDir := filepath.Join(b.projectDir, "architect-001")
	if _, err := os.Stat(architectDir); err == nil {
		b.logger.Info("Refreshing architect workspace at %s", architectDir)
		if _, updateErr := workspace.EnsureArchitectWorkspace(ctx, b.projectDir); updateErr != nil {
			b.logger.Warn("Failed to refresh architect workspace: %v", updateErr)
			// Continue to try PM workspace
		} else {
			b.logger.Info("✅ Architect workspace refreshed")
		}
	}

	// Check if PM workspace directory exists
	pmDir := filepath.Join(b.projectDir, "pm-001")
	if _, err := os.Stat(pmDir); err == nil {
		b.logger.Info("Refreshing PM workspace at %s", pmDir)
		if _, updateErr := workspace.EnsurePMWorkspace(ctx, b.projectDir); updateErr != nil {
			b.logger.Warn("Failed to refresh PM workspace: %v", updateErr)
			return fmt.Errorf("PM workspace refresh failed: %w", updateErr)
		}
		b.logger.Info("✅ PM workspace refreshed")
	}

	return nil
}
