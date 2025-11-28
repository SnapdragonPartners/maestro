package main

import (
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
