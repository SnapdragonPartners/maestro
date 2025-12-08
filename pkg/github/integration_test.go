//go:build integration
// +build integration

package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"orchestrator/pkg/config"
)

const (
	// TestOwner and TestRepo are the test repository used for integration tests.
	// This repo is dedicated for testing and can be freely manipulated.
	TestOwner = "SnapdragonPartners"
	TestRepo  = "maestro-test"
)

// testClient creates a client for the test repository.
func testClient() *Client {
	return NewClient(TestOwner, TestRepo)
}

// skipIfNoToken skips the test if GITHUB_TOKEN is not available.
func skipIfNoToken(t *testing.T) {
	t.Helper()
	if !config.HasGitHubToken() {
		t.Skip("GITHUB_TOKEN not available, skipping integration test")
	}
}

// TestIntegration_RepoExists verifies that the test repository exists.
func TestIntegration_RepoExists(t *testing.T) {
	skipIfNoToken(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := testClient()

	if !client.RepoExists(ctx) {
		t.Fatalf("Test repository %s/%s does not exist or is not accessible", TestOwner, TestRepo)
	}
	t.Logf("✅ Test repository %s/%s exists and is accessible", TestOwner, TestRepo)
}

// TestIntegration_GetRepository verifies repository info retrieval.
func TestIntegration_GetRepository(t *testing.T) {
	skipIfNoToken(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := testClient()

	repo, err := client.GetRepository(ctx)
	if err != nil {
		t.Fatalf("GetRepository failed: %v", err)
	}

	if repo.Name != TestRepo {
		t.Errorf("repo.Name = %q, want %q", repo.Name, TestRepo)
	}

	expectedFullName := fmt.Sprintf("%s/%s", TestOwner, TestRepo)
	if repo.FullName != expectedFullName {
		t.Errorf("repo.FullName = %q, want %q", repo.FullName, expectedFullName)
	}

	t.Logf("✅ Repository info: name=%s, default_branch=%s, private=%v",
		repo.Name, repo.DefaultBranch, repo.Private)
}

// TestIntegration_GetSecurityFeatures verifies security feature status retrieval.
func TestIntegration_GetSecurityFeatures(t *testing.T) {
	skipIfNoToken(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := testClient()

	status, err := client.GetSecurityFeatures(ctx)
	if err != nil {
		t.Fatalf("GetSecurityFeatures failed: %v", err)
	}

	t.Logf("✅ Security features: vulnerability_alerts=%v, automated_fixes=%v, auto_merge=%v",
		status.VulnerabilityAlerts, status.AutomatedSecurityFixes, status.AutoMerge)
}

// TestIntegration_ListBranches verifies branch listing.
func TestIntegration_ListBranches(t *testing.T) {
	skipIfNoToken(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := testClient()

	branches, err := client.ListBranches(ctx)
	if err != nil {
		t.Fatalf("ListBranches failed: %v", err)
	}

	// There should be at least one branch (main/master)
	if len(branches) == 0 {
		t.Fatal("Expected at least one branch, got none")
	}

	t.Logf("✅ Found %d branches", len(branches))
	for _, b := range branches {
		t.Logf("  - %s (protected=%v)", b.Name, b.Protected)
	}
}

// TestIntegration_GetDefaultBranch verifies default branch retrieval.
func TestIntegration_GetDefaultBranch(t *testing.T) {
	skipIfNoToken(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := testClient()

	defaultBranch, err := client.GetDefaultBranch(ctx)
	if err != nil {
		t.Fatalf("GetDefaultBranch failed: %v", err)
	}

	if defaultBranch == "" {
		t.Fatal("Expected non-empty default branch name")
	}

	t.Logf("✅ Default branch: %s", defaultBranch)
}

// TestIntegration_BranchLifecycle tests create, check, and delete branch operations.
func TestIntegration_BranchLifecycle(t *testing.T) {
	skipIfNoToken(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := testClient()
	testBranch := fmt.Sprintf("test-branch-%d", time.Now().Unix())

	// Cleanup function to ensure branch is deleted even if test fails
	defer func() {
		_ = client.DeleteBranch(ctx, testBranch)
	}()

	// Step 1: Get default branch to use as base
	defaultBranch, err := client.GetDefaultBranch(ctx)
	if err != nil {
		t.Fatalf("GetDefaultBranch failed: %v", err)
	}

	// Step 2: Get the SHA of the default branch to create a new branch from
	baseBranch, err := client.GetBranch(ctx, defaultBranch)
	if err != nil {
		t.Fatalf("GetBranch(%s) failed: %v", defaultBranch, err)
	}

	// Step 3: Create the test branch using the GitHub API
	endpoint := fmt.Sprintf("/repos/%s/git/refs", client.RepoPath())
	_, err = client.API(ctx, "POST", endpoint, map[string]interface{}{
		"ref": fmt.Sprintf("refs/heads/%s", testBranch),
		"sha": baseBranch.Commit.SHA,
	})
	if err != nil {
		t.Fatalf("Failed to create branch: %v", err)
	}
	t.Logf("✅ Created branch: %s", testBranch)

	// Step 4: Verify branch exists
	exists, err := client.BranchExists(ctx, testBranch)
	if err != nil {
		t.Fatalf("BranchExists failed: %v", err)
	}
	if !exists {
		t.Fatal("Branch should exist after creation")
	}
	t.Logf("✅ Branch exists confirmed")

	// Step 5: Get branch info
	branchInfo, err := client.GetBranch(ctx, testBranch)
	if err != nil {
		t.Fatalf("GetBranch failed: %v", err)
	}
	if branchInfo.Name != testBranch {
		t.Errorf("Branch name = %q, want %q", branchInfo.Name, testBranch)
	}
	t.Logf("✅ Branch info retrieved: SHA=%s", branchInfo.Commit.SHA)

	// Step 6: Check if branch is merged (should be true since it's identical to base)
	merged, err := client.IsBranchMerged(ctx, testBranch, defaultBranch)
	if err != nil {
		t.Fatalf("IsBranchMerged failed: %v", err)
	}
	if !merged {
		t.Error("Expected branch to be considered 'merged' since it has no unique commits")
	}
	t.Logf("✅ IsBranchMerged check passed: merged=%v", merged)

	// Step 7: Delete branch
	err = client.DeleteBranch(ctx, testBranch)
	if err != nil {
		t.Fatalf("DeleteBranch failed: %v", err)
	}
	t.Logf("✅ Branch deleted")

	// Step 8: Verify branch no longer exists
	exists, err = client.BranchExists(ctx, testBranch)
	if err != nil {
		t.Fatalf("BranchExists after delete failed: %v", err)
	}
	if exists {
		t.Fatal("Branch should not exist after deletion")
	}
	t.Logf("✅ Branch deletion confirmed")
}

// TestIntegration_PRLifecycle tests PR create, get, list, and close operations.
func TestIntegration_PRLifecycle(t *testing.T) {
	skipIfNoToken(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client := testClient()
	testBranch := fmt.Sprintf("pr-test-%d", time.Now().Unix())

	// Cleanup function
	defer func() {
		_ = client.DeleteBranch(ctx, testBranch)
	}()

	// Step 1: Get default branch
	defaultBranch, err := client.GetDefaultBranch(ctx)
	if err != nil {
		t.Fatalf("GetDefaultBranch failed: %v", err)
	}

	// Step 2: Create a test branch with a commit
	baseBranch, err := client.GetBranch(ctx, defaultBranch)
	if err != nil {
		t.Fatalf("GetBranch failed: %v", err)
	}

	// Create branch
	endpoint := fmt.Sprintf("/repos/%s/git/refs", client.RepoPath())
	_, err = client.API(ctx, "POST", endpoint, map[string]interface{}{
		"ref": fmt.Sprintf("refs/heads/%s", testBranch),
		"sha": baseBranch.Commit.SHA,
	})
	if err != nil {
		t.Fatalf("Failed to create branch: %v", err)
	}
	t.Logf("✅ Created test branch: %s", testBranch)

	// Create a file on the test branch using Contents API (simpler than Git Data API)
	fileName := fmt.Sprintf("test-%d.txt", time.Now().Unix())
	fileContent := fmt.Sprintf("Test file created at %s for PR integration test", time.Now().Format(time.RFC3339))
	contentsEndpoint := fmt.Sprintf("/repos/%s/contents/%s", client.RepoPath(), fileName)
	_, err = client.API(ctx, "PUT", contentsEndpoint, map[string]interface{}{
		"message": "Test commit for PR integration test",
		"content": base64Encode(fileContent),
		"branch":  testBranch,
	})
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	t.Logf("✅ Created test file on branch")

	// Step 3: Create PR
	pr, err := client.CreatePR(ctx, PRCreateOptions{
		Title: fmt.Sprintf("Test PR %d", time.Now().Unix()),
		Body:  "This is an automated test PR created by integration tests.",
		Head:  testBranch,
		Base:  defaultBranch,
	})
	if err != nil {
		t.Fatalf("CreatePR failed: %v", err)
	}
	t.Logf("✅ Created PR #%d: %s", pr.Number, pr.Title)

	// Step 4: List PRs and verify ours is in the list
	prs, err := client.ListPRsForBranch(ctx, testBranch)
	if err != nil {
		t.Fatalf("ListPRsForBranch failed: %v", err)
	}
	found := false
	for _, p := range prs {
		if p.Number == pr.Number {
			found = true
			break
		}
	}
	if !found {
		t.Error("Created PR not found in ListPRsForBranch results")
	}
	t.Logf("✅ PR found in list")

	// Step 5: Get PR by number
	gotPR, err := client.GetPR(ctx, fmt.Sprintf("%d", pr.Number))
	if err != nil {
		t.Fatalf("GetPR failed: %v", err)
	}
	if gotPR.Number != pr.Number {
		t.Errorf("GetPR returned wrong PR: got #%d, want #%d", gotPR.Number, pr.Number)
	}
	t.Logf("✅ GetPR verified")

	// Step 6: Check PRExists
	exists, err := client.PRExists(ctx, testBranch)
	if err != nil {
		t.Fatalf("PRExists failed: %v", err)
	}
	if !exists {
		t.Error("PRExists should return true for branch with open PR")
	}
	t.Logf("✅ PRExists confirmed")

	// Step 7: Close PR (don't merge - just close)
	err = client.ClosePR(ctx, fmt.Sprintf("%d", pr.Number))
	if err != nil {
		t.Fatalf("ClosePR failed: %v", err)
	}
	t.Logf("✅ PR closed")

	// Verify PR is closed
	closedPR, err := client.GetPR(ctx, fmt.Sprintf("%d", pr.Number))
	if err != nil {
		t.Fatalf("GetPR after close failed: %v", err)
	}
	if !closedPR.Closed {
		t.Errorf("PR closed = %v, want true", closedPR.Closed)
	}
	t.Logf("✅ PR closure confirmed")
}

// TestIntegration_GetPRNodeID verifies the getPRNodeID function works correctly.
func TestIntegration_GetPRNodeID(t *testing.T) {
	skipIfNoToken(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client := testClient()
	testBranch := fmt.Sprintf("nodeid-test-%d", time.Now().Unix())

	// Cleanup function
	defer func() {
		_ = client.DeleteBranch(ctx, testBranch)
	}()

	// Create a test branch and PR (similar setup to PRLifecycle)
	defaultBranch, err := client.GetDefaultBranch(ctx)
	if err != nil {
		t.Fatalf("GetDefaultBranch failed: %v", err)
	}

	baseBranch, err := client.GetBranch(ctx, defaultBranch)
	if err != nil {
		t.Fatalf("GetBranch failed: %v", err)
	}

	// Create branch
	endpoint := fmt.Sprintf("/repos/%s/git/refs", client.RepoPath())
	_, err = client.API(ctx, "POST", endpoint, map[string]interface{}{
		"ref": fmt.Sprintf("refs/heads/%s", testBranch),
		"sha": baseBranch.Commit.SHA,
	})
	if err != nil {
		t.Fatalf("Failed to create branch: %v", err)
	}

	// Create a file on the test branch using Contents API
	fileName := fmt.Sprintf("nodeid-test-%d.txt", time.Now().Unix())
	fileContent := fmt.Sprintf("Test file for node ID test at %s", time.Now().Format(time.RFC3339))
	contentsEndpoint := fmt.Sprintf("/repos/%s/contents/%s", client.RepoPath(), fileName)
	_, err = client.API(ctx, "PUT", contentsEndpoint, map[string]interface{}{
		"message": "Test commit for node ID test",
		"content": base64Encode(fileContent),
		"branch":  testBranch,
	})
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	// Create PR
	pr, err := client.CreatePR(ctx, PRCreateOptions{
		Title: fmt.Sprintf("Node ID Test PR %d", time.Now().Unix()),
		Body:  "Testing getPRNodeID function",
		Head:  testBranch,
		Base:  defaultBranch,
	})
	if err != nil {
		t.Fatalf("CreatePR failed: %v", err)
	}
	t.Logf("✅ Created PR #%d for node ID test", pr.Number)

	// Clean up PR when done
	defer func() {
		_ = client.ClosePR(ctx, fmt.Sprintf("%d", pr.Number))
	}()

	// Test getPRNodeID
	nodeID, err := client.getPRNodeID(ctx, pr.Number)
	if err != nil {
		t.Fatalf("getPRNodeID failed: %v", err)
	}

	// Node ID should be a non-empty string starting with PR_ (GitHub's format)
	if nodeID == "" {
		t.Fatal("Expected non-empty node ID")
	}
	if len(nodeID) < 10 {
		t.Errorf("Node ID seems too short: %q", nodeID)
	}
	t.Logf("✅ Got PR node ID: %s", nodeID)
}

// parseJSON is a helper to unmarshal JSON from API responses.
func parseJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// base64Encode encodes a string to base64.
func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// Note: We don't test EnablePRAutoMerge directly because it requires:
// 1. The repository to have auto-merge enabled in settings
// 2. Branch protection rules with required status checks
// The getPRNodeID test validates the fix for that function's core requirement.
