package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/scrypt"
)

func TestEncryptDecryptSecretsRoundTrip(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()

	password := "test-password-12345"
	secrets := &StructuredSecrets{
		System: map[string]string{
			"GITHUB_TOKEN":      "ghp_test123456789",
			"ANTHROPIC_API_KEY": "sk-ant-test123",
			"OPENAI_API_KEY":    "sk-test-openai",
			"SSL_KEY_PEM":       "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----",
		},
		User: map[string]string{
			"DATABASE_URL": "postgres://localhost:5432/mydb",
		},
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

	// Verify system secrets match
	if len(decrypted.System) != len(secrets.System) {
		t.Errorf("Expected %d system secrets, got %d", len(secrets.System), len(decrypted.System))
	}
	for key, expectedValue := range secrets.System {
		if actualValue, exists := decrypted.System[key]; !exists {
			t.Errorf("System secret %s not found in decrypted data", key)
		} else if actualValue != expectedValue {
			t.Errorf("System secret %s: expected %q, got %q", key, expectedValue, actualValue)
		}
	}

	// Verify user secrets match
	if len(decrypted.User) != len(secrets.User) {
		t.Errorf("Expected %d user secrets, got %d", len(secrets.User), len(decrypted.User))
	}
	for key, expectedValue := range secrets.User {
		if actualValue, exists := decrypted.User[key]; !exists {
			t.Errorf("User secret %s not found in decrypted data", key)
		} else if actualValue != expectedValue {
			t.Errorf("User secret %s: expected %q, got %q", key, expectedValue, actualValue)
		}
	}
}

func TestDecryptWithWrongPassword(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()

	password := "correct-password"
	wrongPassword := "wrong-password"
	secrets := &StructuredSecrets{
		System: map[string]string{"GITHUB_TOKEN": "ghp_test123456789"},
		User:   map[string]string{},
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
	secrets := &StructuredSecrets{
		System: map[string]string{"GITHUB_TOKEN": "ghp_test"},
		User:   map[string]string{},
	}
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
	// Test 1: User secret takes precedence over system and env
	SetDecryptedSecrets(&StructuredSecrets{
		System: map[string]string{"TEST_SECRET": "from-system"},
		User:   map[string]string{"TEST_SECRET": "from-user"},
	})
	defer func() {
		SetDecryptedSecrets(nil) // Clean up
	}()

	os.Setenv("TEST_SECRET", "from-env-var")
	defer os.Unsetenv("TEST_SECRET")

	secret, err := GetSecret("TEST_SECRET")
	if err != nil {
		t.Fatalf("Expected to get secret, got error: %v", err)
	}
	if secret != "from-user" {
		t.Errorf("Expected secret from user bucket (highest precedence), got: %q", secret)
	}

	// Test 2: System secret takes precedence over env
	SetDecryptedSecrets(&StructuredSecrets{
		System: map[string]string{"TEST_SECRET": "from-system"},
		User:   map[string]string{},
	})

	secret, err = GetSecret("TEST_SECRET")
	if err != nil {
		t.Fatalf("Expected to get secret from system, got error: %v", err)
	}
	if secret != "from-system" {
		t.Errorf("Expected secret from system bucket, got: %q", secret)
	}

	// Test 3: Fall back to env var
	SetDecryptedSecrets(&StructuredSecrets{
		System: map[string]string{},
		User:   map[string]string{},
	})

	secret, err = GetSecret("TEST_SECRET")
	if err != nil {
		t.Fatalf("Expected to get secret from env var, got error: %v", err)
	}
	if secret != "from-env-var" {
		t.Errorf("Expected secret from env var, got: %q", secret)
	}

	// Test 4: Secret not found anywhere
	SetDecryptedSecrets(nil)
	os.Unsetenv("TEST_SECRET")

	_, err = GetSecret("TEST_SECRET")
	if err == nil {
		t.Error("Expected error when secret not found, got nil")
	}
}

func TestGetSecretMaestroPrefixPrecedence(t *testing.T) {
	defer func() {
		SetDecryptedSecrets(nil)
		os.Unsetenv("TEST_KEY")
		os.Unsetenv("MAESTRO_TEST_KEY")
	}()

	// MAESTRO_ env var beats user secrets, system secrets, and standard env var
	SetDecryptedSecrets(&StructuredSecrets{
		System: map[string]string{"TEST_KEY": "from-system"},
		User:   map[string]string{"TEST_KEY": "from-user"},
	})
	os.Setenv("TEST_KEY", "from-env")
	os.Setenv("MAESTRO_TEST_KEY", "from-maestro-env")

	secret, err := GetSecret("TEST_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != "from-maestro-env" {
		t.Errorf("expected MAESTRO_ env var to win, got: %q", secret)
	}

	// Without MAESTRO_ prefix, falls back to user secret
	os.Unsetenv("MAESTRO_TEST_KEY")
	secret, err = GetSecret("TEST_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != "from-user" {
		t.Errorf("expected user secret without MAESTRO_ prefix, got: %q", secret)
	}
}

func TestGetSystemSecretMaestroPrefixPrecedence(t *testing.T) {
	defer func() {
		SetDecryptedSecrets(nil)
		os.Unsetenv("TEST_KEY")
		os.Unsetenv("MAESTRO_TEST_KEY")
	}()

	// MAESTRO_ env var beats system secrets and standard env var
	SetDecryptedSecrets(&StructuredSecrets{
		System: map[string]string{"TEST_KEY": "from-system"},
	})
	os.Setenv("TEST_KEY", "from-env")
	os.Setenv("MAESTRO_TEST_KEY", "from-maestro-env")

	secret, err := GetSystemSecret("TEST_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != "from-maestro-env" {
		t.Errorf("expected MAESTRO_ env var to win, got: %q", secret)
	}

	// Without MAESTRO_ prefix, falls back to system secret (not env var)
	os.Unsetenv("MAESTRO_TEST_KEY")
	secret, err = GetSystemSecret("TEST_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != "from-system" {
		t.Errorf("expected system secret without MAESTRO_ prefix, got: %q", secret)
	}

	// Without system secret, falls back to standard env var
	SetDecryptedSecrets(&StructuredSecrets{
		System: map[string]string{},
	})
	secret, err = GetSystemSecret("TEST_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != "from-env" {
		t.Errorf("expected standard env var as final fallback, got: %q", secret)
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
	secrets := &StructuredSecrets{
		System: map[string]string{},
		User:   map[string]string{},
	}

	// Should be able to encrypt/decrypt empty secrets
	err := EncryptSecretsFile(tmpDir, password, secrets)
	if err != nil {
		t.Fatalf("Failed to encrypt empty secrets: %v", err)
	}

	decrypted, err := DecryptSecretsFile(tmpDir, password)
	if err != nil {
		t.Fatalf("Failed to decrypt empty secrets: %v", err)
	}

	if len(decrypted.System) != 0 || len(decrypted.User) != 0 {
		t.Errorf("Expected 0 secrets, got %d system + %d user", len(decrypted.System), len(decrypted.User))
	}
}

func TestSetSecretSystemValidation(t *testing.T) {
	SetDecryptedSecrets(nil)
	defer SetDecryptedSecrets(nil)

	// Valid system secret name should succeed
	err := SetSecret("ANTHROPIC_API_KEY", "test-value", SecretTypeSystem)
	if err != nil {
		t.Errorf("Expected system secret to be set, got error: %v", err)
	}

	// Invalid system secret name should fail
	err = SetSecret("UNKNOWN_SYSTEM_SECRET", "test-value", SecretTypeSystem)
	if err == nil {
		t.Error("Expected error for unknown system secret name, got nil")
	}

	// User secrets with any valid name should succeed
	err = SetSecret("MY_CUSTOM_SECRET", "test-value", SecretTypeUser)
	if err == nil {
		// Verify it was stored in user bucket
		secrets := GetUserSecrets()
		if secrets["MY_CUSTOM_SECRET"] != "test-value" {
			t.Errorf("Expected user secret to be stored, got: %v", secrets)
		}
	} else {
		t.Errorf("Expected user secret to be set, got error: %v", err)
	}
}

func TestGetUserSecrets(t *testing.T) {
	// Test nil state
	SetDecryptedSecrets(nil)
	secrets := GetUserSecrets()
	if secrets != nil {
		t.Errorf("Expected nil for no secrets, got: %v", secrets)
	}

	// Test with user secrets
	SetDecryptedSecrets(&StructuredSecrets{
		System: map[string]string{"ANTHROPIC_API_KEY": "sk-ant-test"},
		User:   map[string]string{"DB_URL": "postgres://localhost"},
	})
	defer SetDecryptedSecrets(nil)

	secrets = GetUserSecrets()
	if len(secrets) != 1 {
		t.Errorf("Expected 1 user secret, got %d", len(secrets))
	}
	if secrets["DB_URL"] != "postgres://localhost" {
		t.Errorf("Expected DB_URL value, got: %q", secrets["DB_URL"])
	}
	// Should NOT include system secrets
	if _, exists := secrets["ANTHROPIC_API_KEY"]; exists {
		t.Error("GetUserSecrets should not return system secrets")
	}
}

func TestStructuredSecretsRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	password := "test-password"

	original := &StructuredSecrets{
		System: map[string]string{
			"ANTHROPIC_API_KEY": "sk-ant-test",
			"GITHUB_TOKEN":      "ghp_test",
		},
		User: map[string]string{
			"DATABASE_URL": "postgres://localhost:5432/mydb",
			"REDIS_URL":    "redis://localhost:6379",
		},
	}

	err := EncryptSecretsFile(tmpDir, password, original)
	if err != nil {
		t.Fatalf("Failed to encrypt: %v", err)
	}

	decrypted, err := DecryptSecretsFile(tmpDir, password)
	if err != nil {
		t.Fatalf("Failed to decrypt: %v", err)
	}

	// Verify system secrets
	for k, v := range original.System {
		if decrypted.System[k] != v {
			t.Errorf("System secret %s: expected %q, got %q", k, v, decrypted.System[k])
		}
	}

	// Verify user secrets
	for k, v := range original.User {
		if decrypted.User[k] != v {
			t.Errorf("User secret %s: expected %q, got %q", k, v, decrypted.User[k])
		}
	}
}

func TestLegacyFlatFormatMigration(t *testing.T) {
	tmpDir := t.TempDir()
	password := "test-password"

	// Encrypt a flat map[string]string directly (the old format) using raw crypto,
	// bypassing EncryptSecretsFile which now only accepts *StructuredSecrets.
	legacyFlat := map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-legacy",
		"GITHUB_TOKEN":      "ghp_legacy",
	}
	if err := encryptRawJSON(tmpDir, password, legacyFlat); err != nil {
		t.Fatalf("Failed to encrypt legacy format: %v", err)
	}

	// DecryptSecretsFile should detect the flat format and migrate it
	decrypted, err := DecryptSecretsFile(tmpDir, password)
	if err != nil {
		t.Fatalf("Failed to decrypt: %v", err)
	}

	if decrypted.System["ANTHROPIC_API_KEY"] != "sk-ant-legacy" {
		t.Errorf("Expected ANTHROPIC_API_KEY in system bucket after migration, got: %q", decrypted.System["ANTHROPIC_API_KEY"])
	}
	if decrypted.System["GITHUB_TOKEN"] != "ghp_legacy" {
		t.Errorf("Expected GITHUB_TOKEN in system bucket after migration, got: %q", decrypted.System["GITHUB_TOKEN"])
	}
	if decrypted.User == nil {
		t.Error("Expected User map to be initialized after migration")
	}
	if len(decrypted.User) != 0 {
		t.Errorf("Expected empty User map after migration, got %d entries", len(decrypted.User))
	}
}

// encryptRawJSON encrypts any JSON-serializable value to the secrets file,
// used to create legacy-format test fixtures.
func encryptRawJSON(projectDir, password string, v any) error {
	passwordBytes := []byte(password)
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key, err := scrypt.Key(passwordBytes, salt, scryptN, scryptR, scryptP, keySize)
	if err != nil {
		return err
	}
	plaintext, err := json.Marshal(v)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	fileData := make([]byte, 0, saltSize+nonceSize+len(ciphertext))
	fileData = append(fileData, salt...)
	fileData = append(fileData, nonce...)
	fileData = append(fileData, ciphertext...)

	maestroDir := filepath.Join(projectDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(maestroDir, secretsFileName), fileData, 0600)
}

func TestValidateSecretName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"VALID_NAME", false},
		{"_STARTS_WITH_UNDERSCORE", false},
		{"lowercase", false},
		{"MixedCase123", false},
		{"", true},
		{"invalid-name", true},
		{"has spaces", true},
		{"123_STARTS_WITH_NUMBER", true},
		{"has.dot", true},
	}

	for _, tt := range tests {
		err := ValidateSecretName(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateSecretName(%q) error = %v, wantErr = %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestDeleteSecretWithType(t *testing.T) {
	SetDecryptedSecrets(&StructuredSecrets{
		System: map[string]string{"ANTHROPIC_API_KEY": "sk-ant-test"},
		User:   map[string]string{"MY_SECRET": "my-value"},
	})
	defer SetDecryptedSecrets(nil)

	// Delete user secret
	err := DeleteSecret("MY_SECRET", SecretTypeUser)
	if err != nil {
		t.Fatalf("Failed to delete user secret: %v", err)
	}

	// Verify user secret is gone but system secret remains
	secrets := GetUserSecrets()
	if _, exists := secrets["MY_SECRET"]; exists {
		t.Error("Expected MY_SECRET to be deleted from user secrets")
	}
	val, err := GetSecret("ANTHROPIC_API_KEY")
	if err != nil || val != "sk-ant-test" {
		t.Errorf("Expected system secret to remain, got: %q, %v", val, err)
	}

	// Delete system secret
	err = DeleteSecret("ANTHROPIC_API_KEY", SecretTypeSystem)
	if err != nil {
		t.Fatalf("Failed to delete system secret: %v", err)
	}
	// Temporarily unset env var to test that the secret is truly gone
	origEnv := os.Getenv("ANTHROPIC_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")
	defer func() {
		if origEnv != "" {
			os.Setenv("ANTHROPIC_API_KEY", origEnv)
		}
	}()
	_, err = GetSecret("ANTHROPIC_API_KEY")
	if err == nil {
		t.Error("Expected system secret to be deleted")
	}
}

func TestSetSecretSyncsEnvVar(t *testing.T) {
	SetDecryptedSecrets(nil)
	defer SetDecryptedSecrets(nil)

	// Use a unique env var name to avoid interference
	const envName = "ANTHROPIC_API_KEY"

	// Clear env var first
	origEnv := os.Getenv(envName)
	os.Unsetenv(envName)
	defer func() {
		if origEnv != "" {
			os.Setenv(envName, origEnv)
		} else {
			os.Unsetenv(envName)
		}
	}()

	// SetSecret for a system secret should also set the env var
	if err := SetSecret(envName, "test-sync-value", SecretTypeSystem); err != nil {
		t.Fatalf("SetSecret failed: %v", err)
	}
	if got := os.Getenv(envName); got != "test-sync-value" {
		t.Errorf("Expected env var to be synced, got %q", got)
	}

	// DeleteSecret for a system secret should unset the env var
	if err := DeleteSecret(envName, SecretTypeSystem); err != nil {
		t.Fatalf("DeleteSecret failed: %v", err)
	}
	if got := os.Getenv(envName); got != "" {
		t.Errorf("Expected env var to be cleared after delete, got %q", got)
	}

	// User secrets should NOT sync to env vars
	if err := SetSecret("MY_USER_SECRET", "user-val", SecretTypeUser); err != nil {
		t.Fatalf("SetSecret user failed: %v", err)
	}
	if got := os.Getenv("MY_USER_SECRET"); got != "" {
		t.Errorf("User secrets should not sync to env vars, got %q", got)
	}
}

func TestGetDecryptedSecretNamesWithTypes(t *testing.T) {
	SetDecryptedSecrets(&StructuredSecrets{
		System: map[string]string{"ANTHROPIC_API_KEY": "sk-ant"},
		User:   map[string]string{"MY_SECRET": "value"},
	})
	defer SetDecryptedSecrets(nil)

	entries := GetDecryptedSecretNames()
	if len(entries) != 2 {
		t.Fatalf("Expected 2 entries, got %d", len(entries))
	}

	foundSystem := false
	foundUser := false
	for _, e := range entries {
		if e.Name == "ANTHROPIC_API_KEY" && e.Type == SecretTypeSystem {
			foundSystem = true
		}
		if e.Name == "MY_SECRET" && e.Type == SecretTypeUser {
			foundUser = true
		}
	}
	if !foundSystem {
		t.Error("Expected to find system secret ANTHROPIC_API_KEY")
	}
	if !foundUser {
		t.Error("Expected to find user secret MY_SECRET")
	}
}
