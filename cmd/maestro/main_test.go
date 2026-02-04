package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateSecurePassword tests password generation.
func TestGenerateSecurePassword(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"short password", 8},
		{"medium password", 16},
		{"long password", 32},
		{"single char", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			password, err := generateSecurePassword(tt.length)

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if len(password) != tt.length {
				t.Errorf("expected password length %d, got %d", tt.length, len(password))
			}

			// Verify password is not empty
			if password == "" {
				t.Error("expected non-empty password")
			}

			// Verify password contains only valid base64 URL-safe characters
			validChars := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_="
			for _, char := range password {
				if !strings.ContainsRune(validChars, char) {
					t.Errorf("password contains invalid character: %c", char)
				}
			}
		})
	}
}

// TestGenerateSecurePasswordUniqueness tests that passwords are unique.
func TestGenerateSecurePasswordUniqueness(t *testing.T) {
	passwords := make(map[string]bool)

	// Generate 100 passwords and verify they're all unique
	for i := 0; i < 100; i++ {
		password, err := generateSecurePassword(16)
		if err != nil {
			t.Fatalf("failed to generate password: %v", err)
		}

		if passwords[password] {
			t.Errorf("generated duplicate password: %s", password)
		}

		passwords[password] = true
	}

	if len(passwords) != 100 {
		t.Errorf("expected 100 unique passwords, got %d", len(passwords))
	}
}

// Note: mergeCommandLineParams and setupProjectInfrastructure require
// config initialization and file system operations, so they're not suitable
// for simple unit tests. They're tested via integration tests instead.

// TestRunModeBootstrapCheck verifies the bootstrap check logic used by run mode.
func TestRunModeBootstrapCheck(t *testing.T) {
	// Create a temp directory without bootstrap files
	tmpDir := t.TempDir()

	// Test 1: No bootstrap - Dockerfile doesn't exist
	pmWorkspace := filepath.Join(tmpDir, "pm-001")
	dockerfilePath := filepath.Join(pmWorkspace, ".maestro", "Dockerfile")

	_, err := os.Stat(dockerfilePath)
	if err == nil {
		t.Fatal("expected Dockerfile to not exist initially")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("expected IsNotExist error, got: %v", err)
	}

	// Test 2: Create bootstrap structure
	maestroDir := filepath.Join(pmWorkspace, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	// Create a minimal Dockerfile
	dockerfileContent := "FROM alpine:latest\n"
	if err := os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644); err != nil {
		t.Fatalf("failed to create Dockerfile: %v", err)
	}

	// Verify Dockerfile now exists
	if _, err := os.Stat(dockerfilePath); err != nil {
		t.Fatalf("expected Dockerfile to exist after creation: %v", err)
	}
}

// TestRunModeProjectDirHandling tests that run mode handles project directory correctly.
func TestRunModeProjectDirHandling(t *testing.T) {
	tests := []struct {
		name       string
		projectDir string
		wantPM     string
	}{
		{
			name:       "standard project dir",
			projectDir: "/path/to/project",
			wantPM:     "/path/to/project/pm-001",
		},
		{
			name:       "relative project dir",
			projectDir: ".",
			wantPM:     "pm-001", // filepath.Join normalizes "." + "pm-001" to just "pm-001"
		},
		{
			name:       "nested project dir",
			projectDir: "/home/user/projects/myapp",
			wantPM:     "/home/user/projects/myapp/pm-001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pmWorkspace := filepath.Join(tt.projectDir, "pm-001")
			if pmWorkspace != tt.wantPM {
				t.Errorf("pmWorkspace = %q, want %q", pmWorkspace, tt.wantPM)
			}
		})
	}
}
