package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

const (
	// Platform names.
	platformGeneric = "generic"
	platformGo      = "go"
	platformPython  = "python"
	platformNode    = "node"
	platformRust    = "rust"

	// Expertise levels.
	expertiseLevelExpert = "EXPERT"
)

// BootstrapDetector detects missing bootstrap components in a project.
type BootstrapDetector struct {
	logger     *logx.Logger
	projectDir string
}

// BootstrapRequirements describes what bootstrap components are missing.
//
//nolint:govet // Field alignment optimized for clarity over memory efficiency
type BootstrapRequirements struct {
	NeedsBuildTargets   []string // List of missing Makefile targets
	MissingComponents   []string // Human-readable list of missing items
	DetectedPlatform    string   // Detected platform: go, python, node, generic
	PlatformConfidence  float64  // Confidence score 0.0 to 1.0
	NeedsProjectConfig  bool     // True if project name or platform is missing
	NeedsGitRepo        bool     // True if no git repository is configured
	NeedsDockerfile     bool     // True if no Dockerfile exists
	NeedsMakefile       bool     // True if no Makefile exists or missing required targets
	NeedsKnowledgeGraph bool     // True if .maestro/knowledge.dot doesn't exist
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

// BootstrapContext provides context for determining required questions.
type BootstrapContext struct {
	Expertise         string // NON_TECHNICAL, BASIC, EXPERT
	Platform          string
	ProjectDir        string
	HasRepo           bool
	HasDockerfile     bool
	HasMakefile       bool
	HasKnowledgeGraph bool
}

// NewBootstrapDetector creates a new bootstrap detector.
func NewBootstrapDetector(projectDir string) *BootstrapDetector {
	return &BootstrapDetector{
		projectDir: projectDir,
		logger:     logx.NewLogger("bootstrap-detector"),
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

	// Check for Dockerfile
	reqs.NeedsDockerfile = bd.detectMissingDockerfile()
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

	// Detect platform
	reqs.DetectedPlatform, reqs.PlatformConfidence = bd.detectPlatform()

	bd.logger.Info("Bootstrap detection complete: %d components needed (platform: %s @ %.0f%%)",
		len(reqs.MissingComponents), reqs.DetectedPlatform, reqs.PlatformConfidence*100)

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
// This includes:
// - Dockerfile exists in project root.
// - Container is configured in config (name is not bootstrap container).
// - Container has pinned image ID (has been built and configured).
func (bd *BootstrapDetector) detectMissingDockerfile() bool {
	// Check if Dockerfile exists
	dockerfilePath := filepath.Join(bd.projectDir, "Dockerfile")
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		bd.logger.Debug("Dockerfile not found at: %s", dockerfilePath)
		return true
	}

	// Check if container is configured (not using bootstrap fallback)
	cfg, err := config.GetConfig()
	if err != nil {
		bd.logger.Debug("Failed to get config: %v", err)
		return true
	}

	// Container must be configured with a name
	if cfg.Container == nil || cfg.Container.Name == "" {
		bd.logger.Debug("Container not configured in config")
		return true
	}

	// Container must not be the bootstrap fallback container
	if cfg.Container.Name == config.BootstrapContainerTag {
		bd.logger.Debug("Container is still bootstrap fallback: %s", cfg.Container.Name)
		return true
	}

	// Container should have a pinned image ID (indicates it was built and configured)
	if cfg.Container.PinnedImageID == "" {
		bd.logger.Debug("Container has no pinned image ID (not built/configured): %s", cfg.Container.Name)
		return true
	}

	// Verify the pinned image actually exists in Docker
	if !bd.validateDockerImage(cfg.Container.PinnedImageID) {
		bd.logger.Debug("Pinned container image not found in Docker: %s", cfg.Container.PinnedImageID)
		return true
	}

	bd.logger.Debug("Development container configured and available: %s (image: %s)", cfg.Container.Name, cfg.Container.PinnedImageID)
	return false
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

// detectPlatform attempts to detect the project platform from files.
func (bd *BootstrapDetector) detectPlatform() (string, float64) {
	// If platform is already set in config, use it with 100% confidence
	cfg, err := config.GetConfig()
	if err == nil && cfg.Project.PrimaryPlatform != "" {
		platform := cfg.Project.PrimaryPlatform
		bd.logger.Debug("Using platform from config: %s (100%% confidence)", platform)
		return platform, 1.0
	}

	// Otherwise, scan files to detect platform
	// Platform indicators with their confidence weights
	platformScores := map[string]float64{
		platformGo:     0.0,
		platformPython: 0.0,
		platformNode:   0.0,
		platformRust:   0.0,
	}

	// Check for platform-specific files
	bd.checkPlatformFile("go.mod", platformGo, 0.9, platformScores)
	bd.checkPlatformFile("go.sum", platformGo, 0.3, platformScores)
	bd.checkPlatformFile("requirements.txt", platformPython, 0.7, platformScores)
	bd.checkPlatformFile("pyproject.toml", platformPython, 0.9, platformScores)
	bd.checkPlatformFile("setup.py", platformPython, 0.6, platformScores)
	bd.checkPlatformFile("package.json", platformNode, 0.9, platformScores)
	bd.checkPlatformFile("package-lock.json", platformNode, 0.5, platformScores)
	bd.checkPlatformFile("yarn.lock", platformNode, 0.5, platformScores)
	bd.checkPlatformFile("Cargo.toml", platformRust, 0.9, platformScores)

	// Find platform with highest score
	maxPlatform := platformGeneric
	maxScore := 0.0

	for platform, score := range platformScores {
		if score > maxScore {
			maxScore = score
			maxPlatform = platform
		}
	}

	// If no strong signal, default to generic with low confidence
	if maxScore < 0.5 {
		bd.logger.Debug("Platform detection uncertain, defaulting to generic")
		return platformGeneric, 0.3
	}

	bd.logger.Debug("Detected platform: %s (confidence: %.0f%%)", maxPlatform, maxScore*100)
	return maxPlatform, maxScore
}

// checkPlatformFile checks if a platform indicator file exists and updates scores.
func (bd *BootstrapDetector) checkPlatformFile(filename, platform string, weight float64, scores map[string]float64) {
	filePath := filepath.Join(bd.projectDir, filename)
	if _, err := os.Stat(filePath); err == nil {
		scores[platform] += weight
		bd.logger.Debug("Found %s indicator: %s (weight: %.1f)", platform, filename, weight)
	}
}

// GetRequiredQuestions returns questions needed based on bootstrap context and expertise.
func (bd *BootstrapDetector) GetRequiredQuestions(ctx *BootstrapContext) []Question {
	questions := []Question{}

	// Git repository question (always needed if missing)
	if !ctx.HasRepo {
		questions = append(questions, Question{
			ID:       "git_repo",
			Text:     "What's the GitHub repository URL for this project? (I can help you create it if needed)",
			Required: true,
		})
	}

	// Platform confirmation (only for BASIC and EXPERT)
	if ctx.Expertise != "NON_TECHNICAL" && ctx.Platform != "" && ctx.Platform != platformGeneric {
		questions = append(questions, Question{
			ID:       "confirm_platform",
			Text:     fmt.Sprintf("This looks like a %s project. Is that correct?", ctx.Platform),
			Required: false,
		})
	}

	// Custom Dockerfile question (only for EXPERT)
	if ctx.Expertise == expertiseLevelExpert && !ctx.HasDockerfile {
		questions = append(questions, Question{
			ID:       "custom_dockerfile",
			Text:     "Would you like me to include a custom Dockerfile in the setup, or use the default development container?",
			Required: false,
		})
	}

	// Knowledge graph question (only for EXPERT)
	if ctx.Expertise == expertiseLevelExpert && !ctx.HasKnowledgeGraph {
		questions = append(questions, Question{
			ID:       "initial_patterns",
			Text:     "Are there any initial architectural patterns or rules you'd like documented in the knowledge graph?",
			Required: false,
		})
	}

	return questions
}

// Question represents an interview question.
type Question struct {
	ID       string
	Text     string
	Required bool
}
