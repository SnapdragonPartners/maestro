// Package pm provides the PM agent implementation.
// This file contains bootstrap detection logic - PM is the sole authority on bootstrap status.
package pm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/mirror"
	"orchestrator/pkg/workspace"
)

// BootstrapDetector detects missing bootstrap components in a project.
// PM is the sole authority on bootstrap status - this detector runs against the PM workspace.
type BootstrapDetector struct {
	logger     *logx.Logger
	projectDir string
}

// ContainerStatus describes the current state of container configuration.
// Used for detailed logging during bootstrap detection.
//
//nolint:govet // Field alignment optimized for clarity over memory efficiency
type ContainerStatus struct {
	HasValidContainer   bool   // True if a valid project container is configured and available
	IsBootstrapFallback bool   // True if using maestro-bootstrap fallback container
	ContainerName       string // Name of configured container (empty if none)
	PinnedImageID       string // Pinned image ID (empty if not configured)
	DockerfilePath      string // Path to Dockerfile (e.g., .maestro/Dockerfile)
	DockerfileExists    bool   // True if Dockerfile exists at the path
	Reason              string // Human-readable explanation of the status
}

// BootstrapRequirements describes what bootstrap components are missing.
// Platform detection is NOT part of bootstrap - PM LLM handles platform confirmation
// with the user since it's a user-level requirement and too many possibilities exist
// for reliable programmatic detection.
//
//nolint:govet // Field alignment optimized for clarity over memory efficiency
type BootstrapRequirements struct {
	NeedsBuildTargets   []string        // List of missing Makefile targets
	MissingComponents   []string        // Human-readable list of missing items
	NeedsProjectConfig  bool            // True if project name or platform is missing
	NeedsGitRepo        bool            // True if no git repository is configured
	NeedsDockerfile     bool            // True if no Dockerfile exists
	NeedsMakefile       bool            // True if no Makefile exists or missing required targets
	NeedsKnowledgeGraph bool            // True if .maestro/knowledge.dot doesn't exist
	NeedsClaudeCode     bool            // True if coder_mode is "claude-code" but Claude Code not in container
	NeedsGitignore      bool            // True if no .gitignore exists
	ContainerStatus     ContainerStatus // Detailed container status for logging
}

// NeedsBootstrapGate returns true if project metadata (name/platform/git) is missing.
// This determines whether to use bootstrap gate template (focused questions)
// vs full interview template (with bootstrap context).
func (r *BootstrapRequirements) NeedsBootstrapGate() bool {
	return r.NeedsProjectConfig || r.NeedsGitRepo
}

// HasAnyMissingComponents returns true if any bootstrap components are missing.
func (r *BootstrapRequirements) HasAnyMissingComponents() bool {
	return len(r.MissingComponents) > 0
}

// ToRequirementIDs converts BootstrapRequirements to a slice of BootstrapRequirementID.
// This is used by spec_submit to pass structured requirements to the architect,
// who then renders the full technical specification.
func (r *BootstrapRequirements) ToRequirementIDs() []workspace.BootstrapRequirementID {
	var ids []workspace.BootstrapRequirementID

	// Container-related requirements
	if r.ContainerStatus.IsBootstrapFallback && !r.ContainerStatus.HasValidContainer {
		ids = append(ids, workspace.BootstrapReqContainer)
	}
	if r.NeedsDockerfile {
		ids = append(ids, workspace.BootstrapReqDockerfile)
	}

	// Build system requirements
	if r.NeedsMakefile {
		ids = append(ids, workspace.BootstrapReqBuildSystem)
	}

	// Infrastructure requirements
	if r.NeedsKnowledgeGraph {
		ids = append(ids, workspace.BootstrapReqKnowledgeGraph)
	}

	// Git requirements
	if r.NeedsGitRepo {
		ids = append(ids, workspace.BootstrapReqGitAccess)
	}

	return ids
}

// ToBootstrapFailures converts requirements to workspace.BootstrapFailure slice
// for use with the bootstrap template renderer.
// Note: Platform is NOT included in failures - PM LLM handles platform confirmation
// with the user and passes it to the bootstrap MCP tool.
func (r *BootstrapRequirements) ToBootstrapFailures() []workspace.BootstrapFailure {
	var failures []workspace.BootstrapFailure

	// Dockerfile failure (highest priority - blocks everything)
	if r.NeedsDockerfile {
		failures = append(failures, workspace.BootstrapFailure{
			Type:        workspace.BootstrapFailureContainer,
			Component:   "dockerfile",
			Description: "Development container not configured - Dockerfile required",
			Details: map[string]string{
				"action": "create_dockerfile",
			},
			Priority: 1,
		})
	}

	// Makefile failure
	if r.NeedsMakefile {
		details := map[string]string{
			"action": "create_makefile",
		}
		if len(r.NeedsBuildTargets) > 0 {
			details["missing_targets"] = strings.Join(r.NeedsBuildTargets, ", ")
		}
		failures = append(failures, workspace.BootstrapFailure{
			Type:        workspace.BootstrapFailureBuildSystem,
			Component:   "makefile",
			Description: "Makefile missing or incomplete - required targets: build, test, lint, run",
			Details:     details,
			Priority:    2,
		})
	}

	// Knowledge graph failure
	if r.NeedsKnowledgeGraph {
		failures = append(failures, workspace.BootstrapFailure{
			Type:        workspace.BootstrapFailureInfrastructure,
			Component:   "knowledge_graph",
			Description: "Knowledge graph documentation missing - .maestro/knowledge.dot required",
			Details: map[string]string{
				"action": "create_knowledge_graph",
				"path":   ".maestro/knowledge.dot",
			},
			Priority: 3,
		})
	}

	// Gitignore failure
	if r.NeedsGitignore {
		failures = append(failures, workspace.BootstrapFailure{
			Type:        workspace.BootstrapFailureBuildSystem,
			Component:   "gitignore",
			Description: ".gitignore file missing - required for clean repository",
			Details: map[string]string{
				"action": "create_gitignore",
			},
			Priority: 4,
		})
	}

	// Claude Code failure
	if r.NeedsClaudeCode {
		failures = append(failures, workspace.BootstrapFailure{
			Type:        workspace.BootstrapFailureContainer,
			Component:   "claude_code",
			Description: "Claude Code not installed in container - required for coder agents",
			Details: map[string]string{
				"action": "install_claude_code",
			},
			Priority: 2,
		})
	}

	return failures
}

// NewBootstrapDetector creates a new bootstrap detector.
// projectDir should be the PM workspace path (e.g., projectDir/pm-001).
func NewBootstrapDetector(projectDir string) *BootstrapDetector {
	return &BootstrapDetector{
		projectDir: projectDir,
		logger:     logx.NewLogger("pm-bootstrap"),
	}
}

// Detect analyzes the project and returns bootstrap requirements.
func (bd *BootstrapDetector) Detect(_ context.Context) (*BootstrapRequirements, error) {
	bd.logger.Info("Detecting bootstrap requirements in: %s", bd.projectDir)

	reqs := &BootstrapRequirements{
		MissingComponents: []string{},
		NeedsBuildTargets: []string{},
	}

	// Check for project metadata (name, platform)
	reqs.NeedsProjectConfig = bd.detectMissingProjectConfig()
	if reqs.NeedsProjectConfig {
		reqs.MissingComponents = append(reqs.MissingComponents, "project configuration")
	}

	// Check for git repository configuration
	reqs.NeedsGitRepo = bd.detectMissingGitRepo()
	if reqs.NeedsGitRepo {
		reqs.MissingComponents = append(reqs.MissingComponents, "git repository")
	}

	// Check for Dockerfile and container status
	reqs.NeedsDockerfile, reqs.ContainerStatus = bd.detectMissingDockerfile()
	if reqs.NeedsDockerfile {
		reqs.MissingComponents = append(reqs.MissingComponents, "Dockerfile")
	}

	// Check for Makefile with required targets
	reqs.NeedsMakefile, reqs.NeedsBuildTargets = bd.detectMissingMakefile()
	if reqs.NeedsMakefile {
		reqs.MissingComponents = append(reqs.MissingComponents, "Makefile with required targets")
	}

	// Check for knowledge graph
	reqs.NeedsKnowledgeGraph = bd.detectMissingKnowledgeGraph()
	if reqs.NeedsKnowledgeGraph {
		reqs.MissingComponents = append(reqs.MissingComponents, "knowledge graph documentation")
	}

	// Check for Claude Code if coder_mode is "claude-code"
	reqs.NeedsClaudeCode = bd.detectMissingClaudeCode()
	if reqs.NeedsClaudeCode {
		reqs.MissingComponents = append(reqs.MissingComponents, "Claude Code in development container")
	}

	// Check for .gitignore
	reqs.NeedsGitignore = bd.detectMissingGitignore()
	if reqs.NeedsGitignore {
		reqs.MissingComponents = append(reqs.MissingComponents, ".gitignore file")
	}

	// Note: Platform detection is NOT done here - PM LLM handles platform confirmation
	// with the user since it's a user-level requirement

	bd.logger.Info("Bootstrap detection complete: %d components needed",
		len(reqs.MissingComponents))

	return reqs, nil
}

// detectMissingProjectConfig checks if project metadata is configured.
func (bd *BootstrapDetector) detectMissingProjectConfig() bool {
	cfg, err := config.GetConfig()
	if err != nil {
		bd.logger.Warn("Failed to get config: %v", err)
		return true
	}

	// Project config is missing if name or platform is empty
	if cfg.Project == nil || cfg.Project.Name == "" || cfg.Project.PrimaryPlatform == "" {
		bd.logger.Debug("Project configuration incomplete (name: %q, platform: %q)",
			func() string {
				if cfg.Project != nil {
					return cfg.Project.Name
				}
				return ""
			}(),
			func() string {
				if cfg.Project != nil {
					return cfg.Project.PrimaryPlatform
				}
				return ""
			}())
		return true
	}

	bd.logger.Debug("Project configured: name=%s, platform=%s", cfg.Project.Name, cfg.Project.PrimaryPlatform)
	return false
}

// detectMissingGitRepo checks if git repository is properly configured.
func (bd *BootstrapDetector) detectMissingGitRepo() bool {
	cfg, err := config.GetConfig()
	if err != nil {
		bd.logger.Warn("Failed to get config: %v", err)
		return true
	}

	// Git repo is missing if config has no Git section or no RepoURL
	if cfg.Git == nil || cfg.Git.RepoURL == "" {
		bd.logger.Debug("No git repository configured")
		return true
	}

	// Validate that the git URL is well-formed
	if !bd.validateGitURL(cfg.Git.RepoURL) {
		bd.logger.Debug("Git repository URL is invalid: %s", cfg.Git.RepoURL)
		return true
	}

	bd.logger.Debug("Git repository configured: %s", cfg.Git.RepoURL)
	return false
}

// validateGitURL validates that a git URL is properly formatted and accessible.
// Validates GitHub HTTPS URLs and tests accessibility via gh CLI.
func (bd *BootstrapDetector) validateGitURL(repoURL string) bool {
	// Must be non-empty
	if repoURL == "" {
		return false
	}

	// Must start with https:// (we require HTTPS for GitHub)
	if !strings.HasPrefix(repoURL, "https://") {
		bd.logger.Debug("Git URL must use HTTPS protocol: %s", repoURL)
		return false
	}

	// Must be a GitHub URL
	if !strings.Contains(repoURL, "github.com/") {
		bd.logger.Debug("Git URL must be a GitHub repository: %s", repoURL)
		return false
	}

	// Must have at least owner/repo after github.com/
	// Expected format: https://github.com/owner/repo[.git]
	parts := strings.Split(repoURL, "github.com/")
	if len(parts) != 2 || parts[1] == "" {
		bd.logger.Debug("Git URL missing owner/repo: %s", repoURL)
		return false
	}

	// Check that we have owner/repo format
	repoPath := strings.TrimSuffix(parts[1], ".git")
	pathParts := strings.Split(repoPath, "/")
	if len(pathParts) < 2 || pathParts[0] == "" || pathParts[1] == "" {
		bd.logger.Debug("Git URL must have format github.com/owner/repo: %s", repoURL)
		return false
	}

	// Reject placeholder/example URLs
	if pathParts[0] == "user" || pathParts[0] == "username" || pathParts[0] == "your-username" {
		bd.logger.Debug("Git URL appears to be a placeholder: %s", repoURL)
		return false
	}

	// Validate accessibility via gh CLI
	// This tests that:
	// 1. The repository exists
	// 2. GITHUB_TOKEN has access to it
	// 3. Network connectivity works
	if !bd.validateGitHubAccess(repoPath) {
		bd.logger.Debug("Git repository is not accessible: %s", repoURL)
		return false
	}

	return true
}

// validateGitHubAccess validates that a GitHub repository is accessible via gh CLI.
// Takes repo path in format "owner/repo" (without https://github.com/).
func (bd *BootstrapDetector) validateGitHubAccess(repoPath string) bool {
	// Use gh repo view to validate access
	// This command will fail if:
	// - Repository doesn't exist
	// - GITHUB_TOKEN is invalid or lacks permissions
	// - Network is unreachable
	cmd := exec.Command("gh", "repo", "view", repoPath, "--json", "name")
	output, err := cmd.CombinedOutput()

	if err != nil {
		bd.logger.Debug("gh repo view failed for %s: %v (output: %s)", repoPath, err, string(output))
		return false
	}

	bd.logger.Debug("GitHub repository accessible: %s", repoPath)
	return true
}

// detectMissingDockerfile checks if development container is properly configured.
// Returns (false, status) - no bootstrap needed if:
//  1. Container is configured with valid pinned image (warns if Dockerfile missing), OR
//  2. Container invalid but .maestro/Dockerfile exists (can be built)
//
// Returns (true, status) - bootstrap needed if no valid container AND no Dockerfile.
func (bd *BootstrapDetector) detectMissingDockerfile() (bool, ContainerStatus) {
	status := ContainerStatus{
		DockerfilePath: config.GetDockerfilePath(),
	}

	cfg, err := config.GetConfig()
	if err != nil {
		bd.logger.Debug("Failed to get config: %v", err)
		status.Reason = "config unavailable"
		return true, status
	}

	fullPath := filepath.Join(bd.projectDir, status.DockerfilePath)
	status.DockerfileExists = bd.fileExists(fullPath)

	// Extract container info from config
	if cfg.Container != nil {
		status.ContainerName = cfg.Container.Name
		status.PinnedImageID = cfg.Container.PinnedImageID

		// Check if using bootstrap fallback container
		if cfg.Container.Name == config.BootstrapContainerTag {
			status.IsBootstrapFallback = true
		}
	}

	// Check 1: Is there already a working project container configured?
	if bd.hasValidContainer(&cfg) {
		status.HasValidContainer = true
		// Container is valid - warn if Dockerfile missing but don't require bootstrap
		if !status.DockerfileExists {
			bd.logger.Warn("Development container is valid but %s not found. "+
				"Future container rebuilds may fail.", status.DockerfilePath)
			status.Reason = "valid container, Dockerfile missing (rebuild may fail)"
		} else {
			status.Reason = "valid container with Dockerfile"
		}
		bd.logger.Debug("Development container configured and available: %s (image: %s)",
			cfg.Container.Name, cfg.Container.PinnedImageID)
		return false, status // No bootstrap needed
	}

	// Container is not valid - check if we can build from Dockerfile
	if status.DockerfileExists {
		bd.logger.Debug("Found Maestro Dockerfile at %s - can rebuild container", fullPath)
		if status.IsBootstrapFallback {
			status.Reason = "using bootstrap fallback, Dockerfile exists (can build project container)"
		} else {
			status.Reason = "no project container, but Dockerfile exists (can build)"
		}
		return false, status // Dockerfile exists, can rebuild
	}

	// No valid container and no Dockerfile
	bd.logger.Debug("No valid container and no Dockerfile at %s", fullPath)
	if status.IsBootstrapFallback {
		status.Reason = "using bootstrap fallback, no Dockerfile to build project container"
	} else {
		status.Reason = "no container configured and no Dockerfile"
	}
	return true, status // Bootstrap needed - must create Dockerfile
}

// hasValidContainer checks if a working container is already configured.
func (bd *BootstrapDetector) hasValidContainer(cfg *config.Config) bool {
	if cfg.Container == nil || cfg.Container.Name == "" {
		return false
	}

	if cfg.Container.Name == config.BootstrapContainerTag {
		return false // Still using bootstrap fallback
	}

	if cfg.Container.PinnedImageID == "" {
		return false // Not built/configured
	}

	return bd.validateDockerImage(cfg.Container.PinnedImageID)
}

// fileExists checks if a file exists and is readable.
func (bd *BootstrapDetector) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// validateDockerImage checks if a Docker image exists locally.
// Takes an image ID or tag and verifies it's available.
func (bd *BootstrapDetector) validateDockerImage(imageID string) bool {
	// Use docker inspect to check if image exists
	cmd := exec.Command("docker", "inspect", "--type=image", imageID)
	err := cmd.Run()

	if err != nil {
		bd.logger.Debug("Docker image not found: %s", imageID)
		return false
	}

	bd.logger.Debug("Docker image available: %s", imageID)
	return true
}

// detectMissingMakefile checks if Makefile exists and has required targets.
func (bd *BootstrapDetector) detectMissingMakefile() (bool, []string) {
	makefilePath := filepath.Join(bd.projectDir, "Makefile")

	// Check if Makefile exists
	content, err := os.ReadFile(makefilePath)
	if err != nil {
		bd.logger.Debug("Makefile not found at: %s", makefilePath)
		// All targets are missing if Makefile doesn't exist
		return true, []string{"build", "test", "lint", "run"}
	}

	// Check for required targets
	requiredTargets := []string{"build", "test", "lint", "run"}
	missingTargets := []string{}

	makefileStr := string(content)
	for _, target := range requiredTargets {
		// Look for target definition: "target:" at start of line
		targetPattern := "\n" + target + ":"
		if !strings.Contains(makefileStr, targetPattern) && !strings.HasPrefix(makefileStr, target+":") {
			missingTargets = append(missingTargets, target)
		}
	}

	if len(missingTargets) > 0 {
		bd.logger.Debug("Makefile missing targets: %v", missingTargets)
		return true, missingTargets
	}

	bd.logger.Debug("Makefile found with all required targets")
	return false, nil
}

// detectMissingKnowledgeGraph checks if knowledge graph documentation exists.
func (bd *BootstrapDetector) detectMissingKnowledgeGraph() bool {
	knowledgePath := filepath.Join(bd.projectDir, ".maestro", "knowledge.dot")
	if _, err := os.Stat(knowledgePath); os.IsNotExist(err) {
		bd.logger.Debug("Knowledge graph not found at: %s", knowledgePath)
		return true
	}

	bd.logger.Debug("Knowledge graph found at: %s", knowledgePath)
	return false
}

// detectMissingGitignore checks if .gitignore file exists in the project root.
func (bd *BootstrapDetector) detectMissingGitignore() bool {
	gitignorePath := filepath.Join(bd.projectDir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		bd.logger.Debug(".gitignore not found at: %s", gitignorePath)
		return true
	}

	bd.logger.Debug(".gitignore found at: %s", gitignorePath)
	return false
}

// detectMissingClaudeCode checks if Claude Code is needed but not available in the container.
// This only applies when coder_mode is set to "claude-code".
func (bd *BootstrapDetector) detectMissingClaudeCode() bool {
	cfg, err := config.GetConfig()
	if err != nil {
		bd.logger.Debug("Failed to get config for Claude Code detection: %v", err)
		return false
	}

	// Only check if coder_mode is "claude-code"
	if cfg.Agents == nil || cfg.Agents.CoderMode != config.CoderModeClaudeCode {
		bd.logger.Debug("Coder mode is not claude-code, skipping Claude Code detection")
		return false
	}

	// Get the container image to check
	containerImage := ""
	if cfg.Container != nil && cfg.Container.Name != "" && cfg.Container.Name != "detect" {
		containerImage = cfg.Container.Name
	}

	// If no container configured yet, check will happen after bootstrap
	if containerImage == "" {
		bd.logger.Debug("No container configured yet, deferring Claude Code check")
		return false
	}

	// Check if Claude Code is available in the container
	if bd.checkClaudeCodeInContainer(containerImage) {
		bd.logger.Debug("Claude Code available in container: %s", containerImage)
		return false
	}

	bd.logger.Info("Claude Code not found in container %s - will need bootstrap story to install", containerImage)
	return true
}

// checkClaudeCodeInContainer runs "claude --version" in a container to check if Claude Code is available.
func (bd *BootstrapDetector) checkClaudeCodeInContainer(imageName string) bool {
	// Run claude --version in the container
	// Don't specify --platform to use the native architecture of the image
	cmd := exec.Command("docker", "run", "--rm", imageName, "claude", "--version")
	output, err := cmd.CombinedOutput()

	if err != nil {
		bd.logger.Debug("Claude Code check failed in %s: %v (output: %s)", imageName, err, string(output))
		return false
	}

	bd.logger.Debug("Claude Code version in %s: %s", imageName, strings.TrimSpace(string(output)))
	return true
}

// RefreshMirrorAndWorkspaces ensures the git mirror exists and refreshes workspaces.
// This should be called during bootstrap detection to:
// 1. Create the mirror if git is configured but mirror doesn't exist yet (Case 2: repo via CLI)
// 2. Refresh existing mirrors to get latest changes
// Non-fatal - logs warnings but does not return errors.
func (bd *BootstrapDetector) RefreshMirrorAndWorkspaces(ctx context.Context) {
	cfg, err := config.GetConfig()
	if err != nil {
		bd.logger.Debug("Cannot refresh mirrors - failed to get config: %v", err)
		return
	}

	// Only proceed if git is configured
	if cfg.Git == nil || cfg.Git.RepoURL == "" {
		bd.logger.Debug("Skipping mirror setup - no git repository configured")
		return
	}

	// Use parent directory of projectDir (which is the agent workspace)
	// The mirror is at projectDir/../.mirrors/
	parentDir := filepath.Dir(bd.projectDir)
	mirrorDir := filepath.Join(parentDir, ".mirrors")

	// Check if mirror directory exists
	mirrorExists := true
	if _, err := os.Stat(mirrorDir); os.IsNotExist(err) {
		mirrorExists = false
		bd.logger.Info("🔧 Git configured but mirror doesn't exist - creating mirror")
	} else {
		bd.logger.Info("🔄 Refreshing git mirror and workspaces")
	}

	// Use the mirror manager to create or update the mirror
	mirrorMgr := mirror.NewManager(parentDir)
	if _, mirrorErr := mirrorMgr.EnsureMirror(ctx); mirrorErr != nil {
		bd.logger.Warn("Failed to ensure mirror: %v", mirrorErr)
		// Continue to try workspace refresh anyway if mirror already existed
		if !mirrorExists {
			return // Can't refresh workspaces without a mirror
		}
	} else {
		if mirrorExists {
			bd.logger.Info("✅ Git mirror refreshed")
		} else {
			bd.logger.Info("✅ Git mirror created")
		}
	}

	// Refresh architect workspace if it exists
	architectDir := filepath.Join(parentDir, "architect-001")
	if _, statErr := os.Stat(architectDir); statErr == nil {
		if _, updateErr := workspace.EnsureArchitectWorkspace(ctx, parentDir); updateErr != nil {
			bd.logger.Warn("Failed to refresh architect workspace: %v", updateErr)
		} else {
			bd.logger.Info("✅ Architect workspace refreshed")
		}
	}

	// Refresh PM workspace if it exists
	pmDir := filepath.Join(parentDir, "pm-001")
	if _, statErr := os.Stat(pmDir); statErr == nil {
		if _, updateErr := workspace.EnsurePMWorkspace(ctx, parentDir); updateErr != nil {
			bd.logger.Warn("Failed to refresh PM workspace: %v", updateErr)
		} else {
			bd.logger.Info("✅ PM workspace refreshed")
		}
	}
}
