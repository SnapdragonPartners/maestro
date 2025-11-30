package demo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestChangeType_String(t *testing.T) {
	tests := []struct {
		ct       ChangeType
		expected string
	}{
		{NoChange, "no_change"},
		{CodeOnly, "code_only"},
		{DockerfileChanged, "dockerfile_changed"},
		{ComposeChanged, "compose_changed"},
		{ChangeType(999), "unknown"},
	}

	for _, tt := range tests {
		result := tt.ct.String()
		if result != tt.expected {
			t.Errorf("ChangeType(%d).String() = %q, want %q", tt.ct, result, tt.expected)
		}
	}
}

func TestGetChangeRecommendation(t *testing.T) {
	tests := []struct {
		ct       ChangeType
		contains string
	}{
		{NoChange, "up to date"},
		{CodeOnly, "Restart"},
		{DockerfileChanged, "Rebuild"},
		{ComposeChanged, "Rebuild"},
		{ChangeType(999), "Unknown"},
	}

	for _, tt := range tests {
		result := GetChangeRecommendation(tt.ct)
		if result == "" {
			t.Errorf("GetChangeRecommendation(%v) returned empty string", tt.ct)
		}
	}
}

func TestNeedsRebuild(t *testing.T) {
	tests := []struct {
		ct       ChangeType
		expected bool
	}{
		{NoChange, false},
		{CodeOnly, false},
		{DockerfileChanged, true},
		{ComposeChanged, true},
	}

	for _, tt := range tests {
		result := NeedsRebuild(tt.ct)
		if result != tt.expected {
			t.Errorf("NeedsRebuild(%v) = %v, want %v", tt.ct, result, tt.expected)
		}
	}
}

func TestNeedsRestart(t *testing.T) {
	tests := []struct {
		ct       ChangeType
		expected bool
	}{
		{NoChange, false},
		{CodeOnly, true},
		{DockerfileChanged, true},
		{ComposeChanged, true},
	}

	for _, tt := range tests {
		result := NeedsRestart(tt.ct)
		if result != tt.expected {
			t.Errorf("NeedsRestart(%v) = %v, want %v", tt.ct, result, tt.expected)
		}
	}
}

func TestIsComposeFile(t *testing.T) {
	tests := []struct {
		filename string
		expected bool
	}{
		{"compose.yml", true},
		{"compose.yaml", true},
		{"docker-compose.yml", true},
		{"docker-compose.yaml", true},
		{"src/compose.yml", true},
		{"deep/path/to/compose.yaml", true},
		{"Dockerfile", false},
		{"main.go", false},
		{"compose.txt", false},
		{"my-compose.yml", false},
	}

	for _, tt := range tests {
		result := isComposeFile(tt.filename)
		if result != tt.expected {
			t.Errorf("isComposeFile(%q) = %v, want %v", tt.filename, result, tt.expected)
		}
	}
}

func TestIsDockerfile(t *testing.T) {
	tests := []struct {
		filename string
		expected bool
	}{
		{"Dockerfile", true},
		{"Dockerfile.dev", true},
		{"Dockerfile.prod", true},
		{"src/Dockerfile", true},
		{"deep/path/Dockerfile.test", true},
		{"compose.yml", false},
		{"main.go", false},
		{"dockerfile", false}, // Case sensitive
	}

	for _, tt := range tests {
		result := isDockerfile(tt.filename)
		if result != tt.expected {
			t.Errorf("isDockerfile(%q) = %v, want %v", tt.filename, result, tt.expected)
		}
	}
}

func TestGetBaseName(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"file.txt", "file.txt"},
		{"dir/file.txt", "file.txt"},
		{"deep/path/to/file.txt", "file.txt"},
		{"", ""},
	}

	for _, tt := range tests {
		result := getBaseName(tt.path)
		if result != tt.expected {
			t.Errorf("getBaseName(%q) = %q, want %q", tt.path, result, tt.expected)
		}
	}
}

func TestDetectChanges_SameSHA(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	changeType, err := DetectChanges(ctx, tmpDir, "abc123", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changeType != NoChange {
		t.Errorf("expected NoChange for same SHA, got %v", changeType)
	}
}

func TestDetectChanges_EmptySHA(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	_, err := DetectChanges(ctx, tmpDir, "", "abc123")
	if err == nil {
		t.Error("expected error for empty fromSHA")
	}

	_, err = DetectChanges(ctx, tmpDir, "abc123", "")
	if err == nil {
		t.Error("expected error for empty toSHA")
	}
}

// TestDetectChanges_WithGitRepo tests change detection with a real git repo.
func TestDetectChanges_WithGitRepo(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.Command("git", "version").Output(); err != nil {
		t.Skip("git not available")
	}

	ctx := context.Background()
	tmpDir := t.TempDir()

	// Initialize git repo
	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "config", "user.email", "test@test.com")
	runGit(t, tmpDir, "config", "user.name", "Test")

	// Create initial file and commit
	writeFile(t, tmpDir, "main.go", "package main\n")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "initial")
	sha1 := getHead(t, tmpDir)

	// Test 1: Code-only change
	t.Run("code change", func(t *testing.T) {
		writeFile(t, tmpDir, "main.go", "package main\nfunc main() {}\n")
		runGit(t, tmpDir, "add", ".")
		runGit(t, tmpDir, "commit", "-m", "code change")
		sha2 := getHead(t, tmpDir)

		changeType, err := DetectChanges(ctx, tmpDir, sha1, sha2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if changeType != CodeOnly {
			t.Errorf("expected CodeOnly, got %v", changeType)
		}
	})

	sha2 := getHead(t, tmpDir)

	// Test 2: Dockerfile change
	t.Run("dockerfile change", func(t *testing.T) {
		writeFile(t, tmpDir, "Dockerfile", "FROM alpine\n")
		runGit(t, tmpDir, "add", ".")
		runGit(t, tmpDir, "commit", "-m", "add dockerfile")
		sha3 := getHead(t, tmpDir)

		changeType, err := DetectChanges(ctx, tmpDir, sha2, sha3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if changeType != DockerfileChanged {
			t.Errorf("expected DockerfileChanged, got %v", changeType)
		}
	})

	sha3 := getHead(t, tmpDir)

	// Test 3: Compose file change (highest priority)
	t.Run("compose change", func(t *testing.T) {
		writeFile(t, tmpDir, "compose.yml", "version: '3'\nservices:\n  app:\n    image: alpine\n")
		runGit(t, tmpDir, "add", ".")
		runGit(t, tmpDir, "commit", "-m", "add compose")
		sha4 := getHead(t, tmpDir)

		changeType, err := DetectChanges(ctx, tmpDir, sha3, sha4)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if changeType != ComposeChanged {
			t.Errorf("expected ComposeChanged, got %v", changeType)
		}
	})

	sha4 := getHead(t, tmpDir)

	// Test 4: Both Dockerfile and compose change (compose takes priority)
	t.Run("compose priority over dockerfile", func(t *testing.T) {
		writeFile(t, tmpDir, "Dockerfile", "FROM alpine:latest\n")
		writeFile(t, tmpDir, "compose.yml", "version: '3'\nservices:\n  app:\n    build: .\n")
		runGit(t, tmpDir, "add", ".")
		runGit(t, tmpDir, "commit", "-m", "update both")
		sha5 := getHead(t, tmpDir)

		changeType, err := DetectChanges(ctx, tmpDir, sha4, sha5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if changeType != ComposeChanged {
			t.Errorf("expected ComposeChanged (priority), got %v", changeType)
		}
	})
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func getHead(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD failed: %v", err)
	}
	return string(output[:len(output)-1]) // trim newline
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write file %s: %v", path, err)
	}
}
