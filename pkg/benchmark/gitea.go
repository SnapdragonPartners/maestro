package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"orchestrator/pkg/forge"
	"orchestrator/pkg/forge/gitea"
	"orchestrator/pkg/logx"
)

// BenchGitea manages a Gitea instance for benchmark runs.
// It wraps the existing forge/gitea package for container and setup lifecycle,
// and adds benchmark-specific repo seeding and cleanup.
type BenchGitea struct {
	container *gitea.ContainerManager
	setup     *gitea.SetupManager
	logger    *logx.Logger

	// Populated after EnsureRunning.
	info    *gitea.ContainerInfo
	token   string
	baseURL string

	// ReposDir is the path to local bare clones of upstream repos.
	ReposDir string
}

// NewBenchGitea creates a benchmark Gitea manager.
// reposDir is the path containing bare clones of upstream repos
// (e.g., reposDir/psf/requests.git).
func NewBenchGitea(reposDir string) *BenchGitea {
	return &BenchGitea{
		container: gitea.NewContainerManager(),
		setup:     gitea.NewSetupManager(),
		logger:    logx.NewLogger("bench-gitea"),
		ReposDir:  reposDir,
	}
}

// EnsureRunning starts the Gitea container (idempotent) and performs initial setup
// (admin user, API token, organization). Must be called before CreateAndSeedRepo.
func (g *BenchGitea) EnsureRunning(ctx context.Context) error {
	cfg := gitea.ContainerConfig{
		ProjectName: "benchmark",
	}

	info, err := g.container.EnsureContainer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("ensure gitea container: %w", err)
	}
	g.info = info
	g.baseURL = info.URL

	// Run setup to create admin user, token, and organization.
	// RepoName is empty — we create per-instance repos separately.
	result, err := g.setup.Setup(ctx, gitea.SetupConfig{
		Container: info,
		RepoName:  "init", // Placeholder; real repos created per-instance.
	})
	if err != nil {
		return fmt.Errorf("gitea setup: %w", err)
	}

	g.token = result.Token
	g.logger.Info("Gitea ready at %s (token acquired)", g.baseURL)
	return nil
}

// CreateAndSeedRepo creates an ephemeral repo in Gitea and seeds it from a local
// bare clone at the given base commit. Returns the HTTP clone URL.
//
// Flow:
//  1. Create repo via Gitea API
//  2. Clone from local bare cache, detach at baseCommit
//  3. Push to Gitea
//  4. Tag benchmark-base for later diff
func (g *BenchGitea) CreateAndSeedRepo(ctx context.Context, instanceID, repo, baseCommit string) (string, error) {
	repoName := sanitizeRepoName(instanceID)

	// 1. Create repo via Gitea API.
	if err := g.createRepo(ctx, repoName); err != nil {
		return "", fmt.Errorf("create gitea repo %s: %w", repoName, err)
	}

	cloneURL := fmt.Sprintf("%s/%s/%s.git", g.baseURL, gitea.DefaultOrganization, repoName)
	authURL := g.authenticatedURL(cloneURL)

	// 2. Clone from local bare cache.
	barePath := filepath.Join(g.ReposDir, repo+".git")
	if _, err := os.Stat(barePath); os.IsNotExist(err) {
		// Try without .git suffix (some bare clones use the repo name directly).
		barePath = filepath.Join(g.ReposDir, repo)
		if _, statErr := os.Stat(barePath); os.IsNotExist(statErr) {
			return "", fmt.Errorf("bare clone not found at %s or %s.git", barePath, filepath.Join(g.ReposDir, repo))
		}
	}

	// Create a temp working copy at the specific commit.
	workDir, err := os.MkdirTemp("", "bench-seed-"+repoName+"-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	// Clone from bare repo.
	if out, cloneErr := exec.CommandContext(ctx, "git", "clone", "--quiet", barePath, workDir).CombinedOutput(); cloneErr != nil {
		return "", fmt.Errorf("clone from bare cache: %w\n%s", cloneErr, string(out))
	}

	// Checkout the specific base commit.
	if out, checkoutErr := exec.CommandContext(ctx, "git", "-C", workDir, "checkout", "--quiet", baseCommit).CombinedOutput(); checkoutErr != nil {
		return "", fmt.Errorf("checkout %s: %w\n%s", baseCommit, checkoutErr, string(out))
	}

	// Create a branch so we can push (detached HEAD can't push to main).
	if out, branchErr := exec.CommandContext(ctx, "git", "-C", workDir, "checkout", "-b", "main").CombinedOutput(); branchErr != nil {
		return "", fmt.Errorf("create main branch: %w\n%s", branchErr, string(out))
	}

	// 3. Push to Gitea.
	if out, pushErr := exec.CommandContext(ctx, "git", "-C", workDir, "push", "--quiet", authURL, "main").CombinedOutput(); pushErr != nil {
		return "", fmt.Errorf("push to gitea: %w\n%s", pushErr, string(out))
	}

	// 4. Tag benchmark-base for later diff collection.
	if out, tagErr := exec.CommandContext(ctx, "git", "-C", workDir, "tag", "benchmark-base").CombinedOutput(); tagErr != nil {
		return "", fmt.Errorf("create benchmark-base tag: %w\n%s", tagErr, string(out))
	}
	if out, pushTagErr := exec.CommandContext(ctx, "git", "-C", workDir, "push", "--quiet", authURL, "benchmark-base").CombinedOutput(); pushTagErr != nil {
		return "", fmt.Errorf("push benchmark-base tag: %w\n%s", pushTagErr, string(out))
	}

	g.logger.Info("Seeded repo %s at commit %s", repoName, baseCommit[:minInt(12, len(baseCommit))])
	return cloneURL, nil
}

// DeleteRepo removes a repo from Gitea via the API.
func (g *BenchGitea) DeleteRepo(ctx context.Context, instanceID string) error {
	repoName := sanitizeRepoName(instanceID)
	deleteURL := fmt.Sprintf("%s/api/v1/repos/%s/%s", g.baseURL, gitea.DefaultOrganization, repoName)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("create delete request: %w", err)
	}
	req.Header.Set("Authorization", "token "+g.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete repo request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 204 = deleted, 404 = already gone.
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete repo failed (status %d): %s", resp.StatusCode, string(body))
	}

	g.logger.Info("Deleted repo %s", repoName)
	return nil
}

// WriteForgeState writes forge_state.json into the given project directory
// so that Maestro discovers the Gitea instance.
func (g *BenchGitea) WriteForgeState(projectDir, instanceID string) error {
	repoName := sanitizeRepoName(instanceID)
	state := forge.NewGiteaState(
		g.baseURL,
		g.token,
		gitea.DefaultOrganization,
		repoName,
		g.info.HTTPPort,
		g.info.Name,
	)
	if err := forge.SaveState(projectDir, state); err != nil {
		return fmt.Errorf("save forge state: %w", err)
	}
	return nil
}

// ContainerName returns the Docker container name for cleanup purposes.
func (g *BenchGitea) ContainerName() string {
	if g.info != nil {
		return g.info.Name
	}
	return ""
}

// createRepo creates an empty repository via the Gitea API.
func (g *BenchGitea) createRepo(ctx context.Context, repoName string) error {
	repoURL := fmt.Sprintf("%s/api/v1/orgs/%s/repos", g.baseURL, gitea.DefaultOrganization)

	payload := map[string]any{
		"name":           repoName,
		"private":        false,
		"default_branch": "main",
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal repo request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, repoURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+g.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("repo creation request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 201 = created, 409/422 = already exists (idempotent).
	if resp.StatusCode != http.StatusCreated &&
		resp.StatusCode != http.StatusConflict &&
		resp.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("repo creation failed (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// authenticatedURL inserts token credentials into a Gitea clone URL.
func (g *BenchGitea) authenticatedURL(cloneURL string) string {
	return strings.Replace(cloneURL, "://", fmt.Sprintf("://%s:%s@", gitea.DefaultAdminUser, g.token), 1)
}

// sanitizeRepoName converts an instance ID to a valid Gitea repo name.
// Gitea repo names must be alphanumeric with hyphens/underscores.
func sanitizeRepoName(instanceID string) string {
	var b strings.Builder
	for _, r := range instanceID {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := b.String()
	if s == "" {
		return "instance"
	}
	return strings.ToLower(s)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
