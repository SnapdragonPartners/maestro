package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptSecretsRoundTrip(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()

	password := "test-password-12345"
	secrets := map[string]string{
		"GITHUB_TOKEN":      "ghp_test123456789",
		"ANTHROPIC_API_KEY": "sk-ant-test123",
		"OPENAI_API_KEY":    "sk-test-openai",
		"SSL_KEY_PEM":       "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----",
	}

	// Test encryption
	err := EncryptSecretsFile(tmpDir, password, secrets)
	if err != nil {
		t.Fatalf("Failed to encrypt secrets: %v", err)
	}

	// Verify file exists
	secretsPath := filepath.Join(tmpDir, ".maestro", secretsFileName)
	if _, statErr := os.Stat(secretsPath); os.IsNotExist(statErr) {
		t.Fatalf("Secrets file was not created")
	}

	// Verify file permissions
	info, err := os.Stat(secretsPath)
	if err != nil {
		t.Fatalf("Failed to stat secrets file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("Expected file permissions 0600, got %04o", info.Mode().Perm())
	}

	// Test decryption
	decrypted, err := DecryptSecretsFile(tmpDir, password)
	if err != nil {
		t.Fatalf("Failed to decrypt secrets: %v", err)
	}

	// Verify all secrets match
	if len(decrypted) != len(secrets) {
		t.Errorf("Expected %d secrets, got %d", len(secrets), len(decrypted))
	}

	for key, expectedValue := range secrets {
		if actualValue, exists := decrypted[key]; !exists {
			t.Errorf("Secret %s not found in decrypted data", key)
		} else if actualValue != expectedValue {
			t.Errorf("Secret %s: expected %q, got %q", key, expectedValue, actualValue)
		}
	}
}

func TestDecryptWithWrongPassword(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()

	password := "correct-password"
	wrongPassword := "wrong-password"
	secrets := map[string]string{
		"GITHUB_TOKEN": "ghp_test123456789",
	}

	// Encrypt with correct password
	err := EncryptSecretsFile(tmpDir, password, secrets)
	if err != nil {
		t.Fatalf("Failed to encrypt secrets: %v", err)
	}

	// Try to decrypt with wrong password
	_, err = DecryptSecretsFile(tmpDir, wrongPassword)
	if err == nil {
		t.Fatal("Expected decryption to fail with wrong password, but it succeeded")
	}

	// Error should mention wrong password or decryption failure
	if err.Error() != "decryption failed (wrong password or corrupted file)" {
		t.Errorf("Expected specific error message, got: %v", err)
	}
}

func TestSecretsFileExists(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()

	// Should return false when file doesn't exist
	if SecretsFileExists(tmpDir) {
		t.Error("Expected SecretsFileExists to return false when file doesn't exist")
	}

	// Create secrets file
	password := "test-password"
	secrets := map[string]string{"GITHUB_TOKEN": "ghp_test"}
	err := EncryptSecretsFile(tmpDir, password, secrets)
	if err != nil {
		t.Fatalf("Failed to encrypt secrets: %v", err)
	}

	// Should return true when file exists
	if !SecretsFileExists(tmpDir) {
		t.Error("Expected SecretsFileExists to return true when file exists")
	}
}

func TestGetSecretPrecedence(t *testing.T) {
	// Test 1: Secret from decrypted secrets (in memory)
	SetDecryptedSecrets(map[string]string{
		"TEST_SECRET": "from-secrets-file",
	})
	defer func() {
		SetDecryptedSecrets(nil) // Clean up
	}()

	// Set environment variable with different value
	os.Setenv("TEST_SECRET", "from-env-var")
	defer os.Unsetenv("TEST_SECRET")

	secret, err := GetSecret("TEST_SECRET")
	if err != nil {
		t.Fatalf("Expected to get secret, got error: %v", err)
	}
	if secret != "from-secrets-file" {
		t.Errorf("Expected secret from secrets file (precedence), got: %q", secret)
	}

	// Test 2: Secret from environment when not in secrets file
	SetDecryptedSecrets(map[string]string{
		"OTHER_SECRET": "other-value",
	})

	secret, err = GetSecret("TEST_SECRET")
	if err != nil {
		t.Fatalf("Expected to get secret from env var, got error: %v", err)
	}
	if secret != "from-env-var" {
		t.Errorf("Expected secret from env var, got: %q", secret)
	}

	// Test 3: Secret not found anywhere
	SetDecryptedSecrets(nil)
	os.Unsetenv("TEST_SECRET")

	_, err = GetSecret("TEST_SECRET")
	if err == nil {
		t.Error("Expected error when secret not found, got nil")
	}
}

func TestProjectPasswordMemory(t *testing.T) {
	// Clear any existing password
	ClearProjectPassword()

	// Test initial state
	if pwd := GetProjectPassword(); pwd != "" {
		t.Errorf("Expected empty password initially, got: %q", pwd)
	}

	// Test setting password
	testPassword := "test-pwd-123"
	SetProjectPassword(testPassword)

	if pwd := GetProjectPassword(); pwd != testPassword {
		t.Errorf("Expected %q, got: %q", testPassword, pwd)
	}

	// Test clearing password
	ClearProjectPassword()
	if pwd := GetProjectPassword(); pwd != "" {
		t.Errorf("Expected empty password after clear, got: %q", pwd)
	}
}

func TestGetWebUIPasswordPrecedence(t *testing.T) {
	// Clear state
	ClearProjectPassword()
	SetDecryptedSecrets(nil)
	os.Unsetenv("MAESTRO_PASSWORD")
	defer func() {
		ClearProjectPassword()
		SetDecryptedSecrets(nil)
		os.Unsetenv("MAESTRO_PASSWORD")
	}()

	// Test 1: Empty when nothing set
	if pwd := GetWebUIPassword(); pwd != "" {
		t.Errorf("Expected empty password when nothing set, got: %q", pwd)
	}

	// Test 2: From MAESTRO_PASSWORD env var
	os.Setenv("MAESTRO_PASSWORD", "env-password")
	if pwd := GetWebUIPassword(); pwd != "env-password" {
		t.Errorf("Expected password from env var, got: %q", pwd)
	}

	// Test 3: From project password (higher precedence)
	SetProjectPassword("project-password")
	if pwd := GetWebUIPassword(); pwd != "project-password" {
		t.Errorf("Expected password from project password, got: %q", pwd)
	}

	// Clean up
	ClearProjectPassword()
	os.Unsetenv("MAESTRO_PASSWORD")
}

func TestCorruptedSecretsFile(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()

	// Create corrupted file (too small)
	maestroDir := filepath.Join(tmpDir, ".maestro")
	err := os.MkdirAll(maestroDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create .maestro directory: %v", err)
	}

	secretsPath := filepath.Join(maestroDir, secretsFileName)
	err = os.WriteFile(secretsPath, []byte("corrupted"), 0600)
	if err != nil {
		t.Fatalf("Failed to write corrupted file: %v", err)
	}

	// Try to decrypt corrupted file
	_, err = DecryptSecretsFile(tmpDir, "any-password")
	if err == nil {
		t.Error("Expected error when decrypting corrupted file, got nil")
	}

	// Should mention corruption or invalid format
	if err.Error() != "secrets file is corrupted or invalid format (too small)" {
		t.Logf("Error message: %v", err)
	}
}

func TestEmptySecrets(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()

	password := "test-password"
	secrets := map[string]string{} // Empty secrets

	// Should be able to encrypt/decrypt empty secrets
	err := EncryptSecretsFile(tmpDir, password, secrets)
	if err != nil {
		t.Fatalf("Failed to encrypt empty secrets: %v", err)
	}

	decrypted, err := DecryptSecretsFile(tmpDir, password)
	if err != nil {
		t.Fatalf("Failed to decrypt empty secrets: %v", err)
	}

	if len(decrypted) != 0 {
		t.Errorf("Expected 0 secrets, got %d", len(decrypted))
	}
}
