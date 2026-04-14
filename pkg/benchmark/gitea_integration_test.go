//go:build integration

package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"orchestrator/pkg/config"
	"time"
)

// TestIntegration_BenchGitea_Lifecycle tests the full Gitea lifecycle:
// EnsureRunning → CreateAndSeedRepo → WriteForgeState → DeleteRepo.
//
// Requires: Docker running, bare clone at BENCH_REPOS_DIR/psf/requests.git
// Run: go test -tags=integration -run TestIntegration_BenchGitea -v ./pkg/benchmark/
func TestIntegration_BenchGitea_Lifecycle(t *testing.T) {
	reposDir := os.Getenv("BENCH_REPOS_DIR")
	if reposDir == "" {
		reposDir = "/tmp/benchmark-test/bare-repos"
	}

	barePath := filepath.Join(reposDir, "psf", "requests.git")
	if _, err := os.Stat(barePath); os.IsNotExist(err) {
		t.Skipf("Bare repo not found at %s — skipping (set BENCH_REPOS_DIR)", barePath)
	}

	// Verify Docker is available.
	if out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").Output(); err != nil {
		t.Skipf("Docker not available: %v", err)
	} else {
		t.Logf("Docker version: %s", string(out))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	g := NewBenchGitea(reposDir)

	// Step 1: EnsureRunning — start Gitea and perform setup.
	t.Log("Step 1: EnsureRunning")
	if err := g.EnsureRunning(ctx); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	t.Logf("  Gitea running at %s (container=%s)", g.baseURL, g.ContainerName())

	if g.token == "" {
		t.Fatal("  Token is empty after EnsureRunning")
	}
	t.Logf("  Token acquired (len=%d)", len(g.token))

	// Step 2: CreateAndSeedRepo — seed from bare clone.
	instanceID := "psf__requests-integration-test"
	repo := "psf/requests"
	baseCommit := "111d2b77790bf49943c0dfa09b365371c24aec7e" // v2.33.1

	t.Log("Step 2: CreateAndSeedRepo")
	cloneURL, err := g.CreateAndSeedRepo(ctx, instanceID, repo, baseCommit)
	if err != nil {
		t.Fatalf("CreateAndSeedRepo: %v", err)
	}
	t.Logf("  Clone URL: %s", cloneURL)

	// Verify: repo accessible via Gitea API.
	repoName := sanitizeRepoName(instanceID)
	apiURL := fmt.Sprintf("%s/api/v1/repos/maestro/%s", g.baseURL, repoName)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, http.NoBody)
	req.Header.Set("Authorization", "token "+g.token)
	resp, apiErr := http.DefaultClient.Do(req)
	if apiErr != nil {
		t.Fatalf("  API check failed: %v", apiErr)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("  API returned %d: %s", resp.StatusCode, string(body))
	}

	var repoInfo struct {
		DefaultBranch string `json:"default_branch"`
		Empty         bool   `json:"empty"`
	}
	if decErr := json.NewDecoder(resp.Body).Decode(&repoInfo); decErr != nil {
		t.Fatalf("  Decode repo info: %v", decErr)
	}
	t.Logf("  Repo API: default_branch=%s empty=%v", repoInfo.DefaultBranch, repoInfo.Empty)

	// Verify main branch has content via branches API (more reliable than repo.empty flag).
	branchURL := fmt.Sprintf("%s/api/v1/repos/maestro/%s/branches/main", g.baseURL, repoName)
	branchReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, branchURL, http.NoBody)
	branchReq.Header.Set("Authorization", "token "+g.token)
	branchResp, branchErr := http.DefaultClient.Do(branchReq)
	if branchErr != nil {
		t.Fatalf("  Branch API check failed: %v", branchErr)
	}
	defer func() { _ = branchResp.Body.Close() }()

	if branchResp.StatusCode != http.StatusOK {
		branchBody, _ := io.ReadAll(branchResp.Body)
		t.Fatalf("  Main branch not found (status %d): %s", branchResp.StatusCode, string(branchBody))
	}

	var branchInfo struct {
		Name   string `json:"name"`
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if decErr := json.NewDecoder(branchResp.Body).Decode(&branchInfo); decErr != nil {
		t.Fatalf("  Decode branch info: %v", decErr)
	}
	t.Logf("  Branch main exists: commit=%s", branchInfo.Commit.ID[:12])

	if branchInfo.Commit.ID != baseCommit {
		t.Errorf("  Branch HEAD = %s, want %s", branchInfo.Commit.ID, baseCommit)
	} else {
		t.Log("  Branch HEAD matches base commit")
	}

	// Verify: benchmark-base tag exists.
	tagsURL := fmt.Sprintf("%s/api/v1/repos/maestro/%s/tags", g.baseURL, repoName)
	tagReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, tagsURL, http.NoBody)
	tagReq.Header.Set("Authorization", "token "+g.token)
	tagResp, tagErr := http.DefaultClient.Do(tagReq)
	if tagErr != nil {
		t.Fatalf("  Tags API check failed: %v", tagErr)
	}
	defer func() { _ = tagResp.Body.Close() }()

	var tags []struct {
		Name string `json:"name"`
	}
	if decErr := json.NewDecoder(tagResp.Body).Decode(&tags); decErr != nil {
		t.Fatalf("  Decode tags: %v", decErr)
	}
	foundTag := false
	for i := range tags {
		if tags[i].Name == "benchmark-base" {
			foundTag = true
			break
		}
	}
	if !foundTag {
		t.Error("  benchmark-base tag not found")
	} else {
		t.Log("  benchmark-base tag exists")
	}

	// Step 3: WriteForgeState — write forge_state.json.
	t.Log("Step 3: WriteForgeState")
	projectDir := t.TempDir()
	maestroDir := filepath.Join(projectDir, ".maestro")
	if mkErr := os.MkdirAll(maestroDir, 0755); mkErr != nil {
		t.Fatalf("  Create .maestro dir: %v", mkErr)
	}

	if fsErr := g.WriteForgeState(projectDir, instanceID); fsErr != nil {
		t.Fatalf("  WriteForgeState: %v", fsErr)
	}

	statePath := filepath.Join(maestroDir, "forge_state.json")
	stateData, readErr := os.ReadFile(statePath)
	if readErr != nil {
		t.Fatalf("  Read forge_state.json: %v", readErr)
	}
	t.Logf("  forge_state.json written (%d bytes)", len(stateData))

	var stateMap map[string]any
	if jsonErr := json.Unmarshal(stateData, &stateMap); jsonErr != nil {
		t.Fatalf("  Parse forge_state.json: %v", jsonErr)
	}
	if provider, ok := stateMap["provider"].(string); !ok || provider != config.ForgeProviderGitea {
		t.Errorf("  forge_state provider = %v, want gitea", stateMap["provider"])
	} else {
		t.Log("  forge_state.json has provider=gitea")
	}

	// Step 4: DeleteRepo — cleanup.
	t.Log("Step 4: DeleteRepo")
	if delErr := g.DeleteRepo(ctx, instanceID); delErr != nil {
		t.Fatalf("  DeleteRepo: %v", delErr)
	}

	// Verify: repo is gone.
	verifyReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, http.NoBody)
	verifyReq.Header.Set("Authorization", "token "+g.token)
	verifyResp, verifyErr := http.DefaultClient.Do(verifyReq)
	if verifyErr != nil {
		t.Fatalf("  Verify deletion: %v", verifyErr)
	}
	defer func() { _ = verifyResp.Body.Close() }()

	if verifyResp.StatusCode != http.StatusNotFound {
		t.Errorf("  Repo still exists after delete (status=%d)", verifyResp.StatusCode)
	} else {
		t.Log("  Repo deleted successfully (404)")
	}

	t.Log("All Gitea lifecycle steps passed")
}
