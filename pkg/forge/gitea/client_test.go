package gitea

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"orchestrator/pkg/forge"
)

// TestNewClient tests client creation.
func TestNewClient(t *testing.T) {
	client := NewClient("http://localhost:3000", "test-token", "maestro", "myrepo")
	if client == nil {
		t.Fatal("NewClient should not return nil")
	}
	if client.Provider() != forge.ProviderGitea {
		t.Errorf("Provider should be gitea, got %s", client.Provider())
	}
	if client.RepoPath() != "maestro/myrepo" {
		t.Errorf("RepoPath should be 'maestro/myrepo', got %s", client.RepoPath())
	}
	if client.CloneURL() != "http://localhost:3000/maestro/myrepo.git" {
		t.Errorf("CloneURL should be 'http://localhost:3000/maestro/myrepo.git', got %s", client.CloneURL())
	}
}

// TestListPRsForBranch tests listing PRs by branch.
func TestListPRsForBranch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/maestro/myrepo/pulls" {
			// Verify auth header
			if r.Header.Get("Authorization") != "token test-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			// Return mock PR list
			prs := []giteaPR{
				{
					Number:  1,
					HTMLURL: "http://localhost:3000/maestro/myrepo/pulls/1",
					Title:   "Test PR",
					State:   "open",
					Head:    giteaRef{Ref: "feature-branch", SHA: "abc123"},
					Base:    giteaRef{Ref: "main", SHA: "def456"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(prs)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token", "maestro", "myrepo")
	ctx := context.Background()

	prs, err := client.ListPRsForBranch(ctx, "feature-branch")
	if err != nil {
		t.Fatalf("ListPRsForBranch failed: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("Expected 1 PR, got %d", len(prs))
	}
	if prs[0].Number != 1 {
		t.Errorf("Expected PR #1, got #%d", prs[0].Number)
	}
	if prs[0].HeadBranch != "feature-branch" {
		t.Errorf("Expected head branch 'feature-branch', got %s", prs[0].HeadBranch)
	}
}

// TestGetPRByNumber tests getting a PR by number.
func TestGetPRByNumber(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/maestro/myrepo/pulls/1" {
			pr := giteaPR{
				Number:    1,
				HTMLURL:   "http://localhost:3000/maestro/myrepo/pulls/1",
				Title:     "Test PR",
				State:     "open",
				Mergeable: true,
				Head:      giteaRef{Ref: "feature-branch", SHA: "abc123"},
				Base:      giteaRef{Ref: "main", SHA: "def456"},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pr)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token", "maestro", "myrepo")
	ctx := context.Background()

	pr, err := client.GetPR(ctx, "1")
	if err != nil {
		t.Fatalf("GetPR failed: %v", err)
	}
	if pr.Number != 1 {
		t.Errorf("Expected PR #1, got #%d", pr.Number)
	}
	if !pr.Mergeable {
		t.Error("PR should be mergeable")
	}
}

// TestGetPRNotFound tests getting a nonexistent PR.
func TestGetPRNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token", "maestro", "myrepo")
	ctx := context.Background()

	_, err := client.GetPR(ctx, "999")
	if err == nil {
		t.Error("GetPR should fail for nonexistent PR")
	}
}

// TestCreatePR tests creating a PR.
func TestCreatePR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/maestro/myrepo/pulls" && r.Method == http.MethodPost {
			// Verify request body
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if body["title"] != "New Feature" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if body["head"] != "feature-branch" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			// Return created PR
			pr := giteaPR{
				Number:  2,
				HTMLURL: "http://localhost:3000/maestro/myrepo/pulls/2",
				Title:   body["title"].(string),
				State:   "open",
				Head:    giteaRef{Ref: body["head"].(string), SHA: "abc123"},
				Base:    giteaRef{Ref: body["base"].(string), SHA: "def456"},
			}
			w.WriteHeader(http.StatusCreated)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pr)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token", "maestro", "myrepo")
	ctx := context.Background()

	opts := forge.PRCreateOptions{
		Title: "New Feature",
		Body:  "This is a new feature",
		Head:  "feature-branch",
		Base:  "main",
	}

	pr, err := client.CreatePR(ctx, opts)
	if err != nil {
		t.Fatalf("CreatePR failed: %v", err)
	}
	if pr.Number != 2 {
		t.Errorf("Expected PR #2, got #%d", pr.Number)
	}
	if pr.Title != "New Feature" {
		t.Errorf("Expected title 'New Feature', got %s", pr.Title)
	}
}

// TestCreatePR_MissingHead tests creating PR without head branch.
func TestCreatePR_MissingHead(t *testing.T) {
	client := NewClient("http://localhost:3000", "test-token", "maestro", "myrepo")
	ctx := context.Background()

	opts := forge.PRCreateOptions{
		Title: "New Feature",
		// Missing Head
	}

	_, err := client.CreatePR(ctx, opts)
	if err == nil {
		t.Error("CreatePR should fail without head branch")
	}
}

// TestCreatePR_MissingTitle tests creating PR without title.
func TestCreatePR_MissingTitle(t *testing.T) {
	client := NewClient("http://localhost:3000", "test-token", "maestro", "myrepo")
	ctx := context.Background()

	opts := forge.PRCreateOptions{
		Head: "feature-branch",
		// Missing Title
	}

	_, err := client.CreatePR(ctx, opts)
	if err == nil {
		t.Error("CreatePR should fail without title")
	}
}

// TestMergePRWithResult tests merging a PR.
func TestMergePRWithResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle get PR
		if r.URL.Path == "/api/v1/repos/maestro/myrepo/pulls/1" && r.Method == http.MethodGet {
			pr := giteaPR{
				Number:    1,
				State:     "open",
				Mergeable: true,
				Head:      giteaRef{Ref: "feature", SHA: "abc123"},
				Base:      giteaRef{Ref: "main", SHA: "def456"},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pr)
			return
		}
		// Handle merge
		if r.URL.Path == "/api/v1/repos/maestro/myrepo/pulls/1/merge" && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token", "maestro", "myrepo")
	ctx := context.Background()

	opts := forge.PRMergeOptions{
		Method:       "squash",
		DeleteBranch: true,
	}

	result, err := client.MergePRWithResult(ctx, "1", opts)
	if err != nil {
		t.Fatalf("MergePRWithResult failed: %v", err)
	}
	if !result.Merged {
		t.Error("PR should be merged")
	}
}

// TestMergePRWithResult_Conflict tests merge with conflicts.
func TestMergePRWithResult_Conflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/maestro/myrepo/pulls/1" && r.Method == http.MethodGet {
			pr := giteaPR{
				Number:    1,
				State:     "open",
				Mergeable: false,
				Head:      giteaRef{Ref: "feature", SHA: "abc123"},
				Base:      giteaRef{Ref: "main", SHA: "def456"},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pr)
			return
		}
		if r.URL.Path == "/api/v1/repos/maestro/myrepo/pulls/1/merge" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"message": "merge conflict"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token", "maestro", "myrepo")
	ctx := context.Background()

	result, err := client.MergePRWithResult(ctx, "1", forge.PRMergeOptions{})
	if err != nil {
		t.Fatalf("MergePRWithResult should not error on conflict: %v", err)
	}
	if result.Merged {
		t.Error("PR should not be merged due to conflict")
	}
	if !result.HasConflicts {
		t.Error("Result should indicate conflicts")
	}
}

// TestClosePR tests closing a PR.
func TestClosePR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/maestro/myrepo/pulls/1" {
			if r.Method == http.MethodGet {
				pr := giteaPR{Number: 1, State: "open"}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(pr)
				return
			}
			if r.Method == http.MethodPatch {
				var body map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["state"] != "closed" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token", "maestro", "myrepo")
	ctx := context.Background()

	err := client.ClosePR(ctx, "1")
	if err != nil {
		t.Fatalf("ClosePR failed: %v", err)
	}
}

// TestGetOrCreatePR_Existing tests getting existing PR.
func TestGetOrCreatePR_Existing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/maestro/myrepo/pulls" && r.Method == http.MethodGet {
			prs := []giteaPR{
				{
					Number: 1,
					Title:  "Existing PR",
					State:  "open",
					Head:   giteaRef{Ref: "feature-branch"},
					Base:   giteaRef{Ref: "main"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(prs)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token", "maestro", "myrepo")
	ctx := context.Background()

	opts := forge.PRCreateOptions{
		Title: "New PR",
		Head:  "feature-branch",
	}

	pr, err := client.GetOrCreatePR(ctx, opts)
	if err != nil {
		t.Fatalf("GetOrCreatePR failed: %v", err)
	}
	// Should return existing PR, not create new one
	if pr.Number != 1 {
		t.Errorf("Expected existing PR #1, got #%d", pr.Number)
	}
	if pr.Title != "Existing PR" {
		t.Errorf("Expected title 'Existing PR', got %s", pr.Title)
	}
}

// TestClientHelpers tests client helper methods.
func TestClientHelpers(t *testing.T) {
	client := NewClient("http://localhost:3000/", "token", "owner", "repo")

	// Test URL normalization (trailing slash removed)
	if client.BaseURL() != "http://localhost:3000" {
		t.Errorf("BaseURL should remove trailing slash, got %s", client.BaseURL())
	}

	if client.Owner() != "owner" {
		t.Errorf("Owner should be 'owner', got %s", client.Owner())
	}

	if client.Repo() != "repo" {
		t.Errorf("Repo should be 'repo', got %s", client.Repo())
	}
}

// TestConvertPR tests PR conversion.
func TestConvertPR(t *testing.T) {
	mergedAt := "2024-01-15T10:30:00Z"
	gpr := &giteaPR{
		Number:    1,
		HTMLURL:   "http://example.com/pr/1",
		Title:     "Test",
		Body:      "Description",
		State:     "closed",
		Merged:    true,
		MergedAt:  &mergedAt,
		Mergeable: false,
		Head:      giteaRef{Ref: "feature", SHA: "abc"},
		Base:      giteaRef{Ref: "main", SHA: "def"},
	}

	pr := convertPR(gpr)

	if pr.Number != 1 {
		t.Errorf("Number should be 1, got %d", pr.Number)
	}
	if pr.URL != "http://example.com/pr/1" {
		t.Errorf("URL mismatch: %s", pr.URL)
	}
	if pr.HeadBranch != "feature" {
		t.Errorf("HeadBranch should be 'feature', got %s", pr.HeadBranch)
	}
	if !pr.Merged {
		t.Error("Merged should be true")
	}
	if pr.MergedAt == nil {
		t.Error("MergedAt should not be nil")
	}
	if !pr.IsMerged() {
		t.Error("IsMerged should return true")
	}
}
