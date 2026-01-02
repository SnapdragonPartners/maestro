// Package sync provides synchronization between local Gitea and remote GitHub.
// Used when transitioning from airplane mode (offline) back to standard mode (online).
//
// This package is designed to be invoked from multiple contexts:
// - CLI via `maestro --sync`
// - WebUI via API endpoint
// - PM agent via tool call
package sync

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/forge"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/mirror"
)

// Sync result status constants.
const (
	StatusPushed   = "pushed"
	StatusUpToDate = "up-to-date"
)

// Result contains the results of a sync operation.
type Result struct {
	// BranchesPushed lists branches that were pushed to GitHub.
	BranchesPushed []string
	// Warnings contains non-fatal issues encountered during sync.
	Warnings []string
	// Success indicates if the sync completed successfully.
	Success bool
	// MainPushed indicates if the main branch was pushed.
	MainPushed bool
	// MainUpToDate indicates if main was already up-to-date.
	MainUpToDate bool
	// MirrorUpdated indicates if the mirror was updated from GitHub.
	MirrorUpdated bool
}

// gitHubTarget represents the GitHub remote target.
type gitHubTarget struct {
	repoURL      string
	targetBranch string
}

// giteaSource represents the local Gitea source.
type giteaSource struct {
	url   string
	owner string
	repo  string
}

// Syncer handles synchronization between Gitea and GitHub.
type Syncer struct {
	logger     *logx.Logger
	gitHub     *gitHubTarget
	gitea      *giteaSource
	projectDir string
	dryRun     bool
}

// NewSyncer creates a new Syncer for the given project directory.
func NewSyncer(projectDir string, dryRun bool) (*Syncer, error) {
	// Load config for GitHub URL
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	if cfg.Git == nil || cfg.Git.RepoURL == "" {
		return nil, fmt.Errorf("no git repository configured")
	}

	targetBranch := cfg.Git.TargetBranch
	if targetBranch == "" {
		targetBranch = "main"
	}

	// Load forge state for Gitea details
	state, err := forge.LoadState(projectDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load forge state: %w", err)
	}

	return &Syncer{
		projectDir: projectDir,
		dryRun:     dryRun,
		logger:     logx.NewLogger("syncer"),
		gitHub: &gitHubTarget{
			repoURL:      cfg.Git.RepoURL,
			targetBranch: targetBranch,
		},
		gitea: &giteaSource{
			url:   state.URL,
			owner: state.Owner,
			repo:  state.RepoName,
		},
	}, nil
}

// SyncToGitHub synchronizes changes from Gitea to GitHub.
func (s *Syncer) SyncToGitHub(ctx context.Context) (*Result, error) {
	result := &Result{}

	s.logger.Info("Starting sync from Gitea to GitHub")
	s.logger.Info("  Gitea: %s/%s/%s", s.gitea.url, s.gitea.owner, s.gitea.repo)
	s.logger.Info("  GitHub: %s", s.gitHub.repoURL)

	// Step 1: Create temporary directory for sync operations
	tmpDir, err := s.createTempDir()
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer s.cleanupTempDir(tmpDir)

	// Step 2: Clone from Gitea
	s.logger.Info("📥 Cloning from Gitea...")
	if cloneErr := s.cloneFromGitea(ctx, tmpDir); cloneErr != nil {
		return nil, fmt.Errorf("failed to clone from Gitea: %w", cloneErr)
	}

	// Step 3: Add GitHub as remote
	s.logger.Info("🔗 Adding GitHub remote...")
	if remoteErr := s.addGitHubRemote(ctx, tmpDir); remoteErr != nil {
		return nil, fmt.Errorf("failed to add GitHub remote: %w", remoteErr)
	}

	// Step 4: Fetch from GitHub to detect divergence
	s.logger.Info("📊 Checking GitHub state...")
	if fetchErr := s.fetchGitHub(ctx, tmpDir); fetchErr != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("Could not fetch from GitHub (may not exist yet): %v", fetchErr))
	}

	// Step 5: Push all branches to GitHub
	s.logger.Info("📤 Pushing branches to GitHub...")
	pushed, pushErr := s.pushAllBranches(ctx, tmpDir)
	if pushErr != nil {
		return nil, fmt.Errorf("failed to push branches: %w", pushErr)
	}
	result.BranchesPushed = pushed

	// Step 6: Push main branch
	mainResult, mainErr := s.pushMain(ctx, tmpDir)
	if mainErr != nil {
		return nil, fmt.Errorf("failed to push main branch: %w", mainErr)
	}
	result.MainPushed = mainResult == StatusPushed
	result.MainUpToDate = mainResult == StatusUpToDate

	// Step 7: Update mirror from GitHub
	if !s.dryRun {
		s.logger.Info("📥 Updating mirror from GitHub...")
		if mirrorErr := s.updateMirror(ctx); mirrorErr != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("Failed to update mirror: %v", mirrorErr))
		} else {
			result.MirrorUpdated = true
		}
	} else {
		s.logger.Info("[DRY-RUN] Would update mirror from GitHub")
		result.MirrorUpdated = true
	}

	result.Success = true
	return result, nil
}

// createTempDir creates a temporary directory for sync operations.
func (s *Syncer) createTempDir() (string, error) {
	tmpBase := filepath.Join(s.projectDir, ".tmp")
	if err := os.MkdirAll(tmpBase, 0755); err != nil {
		return "", fmt.Errorf("failed to create .tmp directory: %w", err)
	}

	tmpDir := filepath.Join(tmpBase, fmt.Sprintf("sync-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create sync directory: %w", err)
	}

	return tmpDir, nil
}

// cleanupTempDir removes the temporary directory.
func (s *Syncer) cleanupTempDir(tmpDir string) {
	if err := os.RemoveAll(tmpDir); err != nil {
		s.logger.Warn("Failed to cleanup temp directory: %v", err)
	}
}

// cloneFromGitea clones the repository from Gitea.
func (s *Syncer) cloneFromGitea(ctx context.Context, tmpDir string) error {
	giteaURL := fmt.Sprintf("%s/%s/%s.git", s.gitea.url, s.gitea.owner, s.gitea.repo)

	if s.dryRun {
		s.logger.Info("[DRY-RUN] Would clone from %s", giteaURL)
		// In dry-run, we still need to clone to analyze branches
	}

	cmd := exec.CommandContext(ctx, "git", "clone", giteaURL, "repo")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// addGitHubRemote adds GitHub as a remote named "github".
func (s *Syncer) addGitHubRemote(ctx context.Context, tmpDir string) error {
	repoDir := filepath.Join(tmpDir, "repo")

	cmd := exec.CommandContext(ctx, "git", "remote", "add", "github", s.gitHub.repoURL)
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git remote add failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// fetchGitHub fetches from GitHub to detect any upstream changes.
func (s *Syncer) fetchGitHub(ctx context.Context, tmpDir string) error {
	repoDir := filepath.Join(tmpDir, "repo")

	cmd := exec.CommandContext(ctx, "git", "fetch", "github")
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch github failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// pushAllBranches pushes all branches to GitHub.
func (s *Syncer) pushAllBranches(ctx context.Context, tmpDir string) ([]string, error) {
	repoDir := filepath.Join(tmpDir, "repo")

	// List all branches
	cmd := exec.CommandContext(ctx, "git", "branch", "-r", "--list", "origin/*")
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git branch list failed: %w\nOutput: %s", err, string(output))
	}

	branches := strings.Split(strings.TrimSpace(string(output)), "\n")
	pushed := make([]string, 0, len(branches))

	for _, branch := range branches {
		branch = strings.TrimSpace(branch)
		if branch == "" {
			continue
		}

		// Skip HEAD reference
		if strings.Contains(branch, "HEAD") {
			continue
		}

		// Extract branch name (remove "origin/" prefix)
		branchName := strings.TrimPrefix(branch, "origin/")

		// Skip main branch (handled separately)
		if branchName == s.gitHub.targetBranch {
			continue
		}

		if s.dryRun {
			s.logger.Info("[DRY-RUN] Would push branch: %s", branchName)
			pushed = append(pushed, branchName)
			continue
		}

		// Push to GitHub
		pushCmd := exec.CommandContext(ctx, "git", "push", "github", fmt.Sprintf("origin/%s:refs/heads/%s", branchName, branchName))
		pushCmd.Dir = repoDir
		pushOutput, pushErr := pushCmd.CombinedOutput()
		if pushErr != nil {
			s.logger.Warn("Failed to push branch %s: %v\nOutput: %s", branchName, pushErr, string(pushOutput))
			continue
		}

		pushed = append(pushed, branchName)
		s.logger.Info("✓ Pushed branch: %s", branchName)
	}

	return pushed, nil
}

// pushMain pushes the main branch to GitHub.
func (s *Syncer) pushMain(ctx context.Context, tmpDir string) (string, error) {
	repoDir := filepath.Join(tmpDir, "repo")

	if s.dryRun {
		s.logger.Info("[DRY-RUN] Would push main branch: %s", s.gitHub.targetBranch)
		return StatusPushed, nil
	}

	// Push main branch
	cmd := exec.CommandContext(ctx, "git", "push", "github",
		fmt.Sprintf("origin/%s:refs/heads/%s", s.gitHub.targetBranch, s.gitHub.targetBranch))
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if it's "already up-to-date"
		if strings.Contains(string(output), "Everything up-to-date") {
			return StatusUpToDate, nil
		}
		return "", fmt.Errorf("git push main failed: %w\nOutput: %s", err, string(output))
	}

	s.logger.Info("✓ Pushed main branch: %s", s.gitHub.targetBranch)
	return StatusPushed, nil
}

// updateMirror updates the local mirror from GitHub.
func (s *Syncer) updateMirror(ctx context.Context) error {
	mgr := mirror.NewManager(s.projectDir)

	// Switch upstream back to GitHub
	if err := mgr.SwitchUpstream(ctx, s.gitHub.repoURL); err != nil {
		return fmt.Errorf("failed to switch mirror upstream: %w", err)
	}

	return nil
}
