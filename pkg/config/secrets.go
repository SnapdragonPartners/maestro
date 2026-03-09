package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"golang.org/x/crypto/scrypt"
)

// SecretType distinguishes between system (Maestro operational) and user (app-injected) secrets.
type SecretType string

// Secret type constants.
const (
	SecretTypeSystem SecretType = "system"
	SecretTypeUser   SecretType = "user"
)

// systemSecretNames is the allowlist of valid system secret names.
//
//nolint:gochecknoglobals // Intentional package-level lookup table for system secret validation
var systemSecretNames = map[string]bool{
	"ANTHROPIC_API_KEY":    true,
	"OPENAI_API_KEY":       true,
	"GOOGLE_GENAI_API_KEY": true,
	"GITHUB_TOKEN":         true,
	"SSL_KEY_PEM":          true,
}

// StructuredSecrets separates system (Maestro operational) from user (app-injected) secrets.
type StructuredSecrets struct {
	System map[string]string `json:"system"`
	User   map[string]string `json:"user"`
}

// SecretNameEntry represents a secret name with its type for listing.
type SecretNameEntry struct {
	Name string     `json:"name"`
	Type SecretType `json:"type"`
}

// validSecretNameRe validates environment variable names.
//
//nolint:gochecknoglobals // Precompiled regex for secret name validation reused across the package
var validSecretNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ValidateSecretName checks that a name is a valid environment variable name.
func ValidateSecretName(name string) error {
	if name == "" {
		return fmt.Errorf("secret name cannot be empty")
	}
	if !validSecretNameRe.MatchString(name) {
		return fmt.Errorf("secret name %q is not a valid environment variable name (must match ^[a-zA-Z_][a-zA-Z0-9_]*$)", name)
	}
	return nil
}

// Secrets file configuration.
const (
	secretsFileName = "secrets.json.enc"
	saltSize        = 16
	nonceSize       = 12
	scryptN         = 32768 // 2^15
	scryptR         = 8
	scryptP         = 1
	keySize         = 32 // AES-256
)

// Global state for decrypted secrets.
//
//nolint:gochecknoglobals // Intentional global state for in-memory secrets storage
var (
	decryptedSecrets    *StructuredSecrets
	decryptedSecretsMux sync.RWMutex
	projectPassword     string
	projectPasswordMux  sync.RWMutex
)

// SetProjectPassword stores the project password in memory for WebUI auth.
func SetProjectPassword(password string) {
	projectPasswordMux.Lock()
	defer projectPasswordMux.Unlock()
	projectPassword = password
}

// GetProjectPassword retrieves the project password from memory.
func GetProjectPassword() string {
	projectPasswordMux.RLock()
	defer projectPasswordMux.RUnlock()
	return projectPassword
}

// ClearProjectPassword securely clears the project password from memory.
func ClearProjectPassword() {
	projectPasswordMux.Lock()
	defer projectPasswordMux.Unlock()
	if projectPassword != "" {
		// Zero out the password in memory
		passwordBytes := []byte(projectPassword)
		for i := range passwordBytes {
			passwordBytes[i] = 0
		}
		projectPassword = ""
	}
}

// SetDecryptedSecrets stores decrypted secrets in memory.
func SetDecryptedSecrets(secrets *StructuredSecrets) {
	decryptedSecretsMux.Lock()
	defer decryptedSecretsMux.Unlock()
	decryptedSecrets = secrets
}

// GetSecret returns a secret value by name using precedence:
// 1. User secrets (in memory)
// 2. System secrets (in memory)
// 3. Environment variables.
func GetSecret(name string) (string, error) {
	decryptedSecretsMux.RLock()
	if decryptedSecrets != nil {
		// User secrets take precedence over system secrets
		if value, exists := decryptedSecrets.User[name]; exists && value != "" {
			decryptedSecretsMux.RUnlock()
			return value, nil
		}
		if value, exists := decryptedSecrets.System[name]; exists && value != "" {
			decryptedSecretsMux.RUnlock()
			return value, nil
		}
	}
	decryptedSecretsMux.RUnlock()

	// Fall back to environment variable
	if value := os.Getenv(name); value != "" {
		return value, nil
	}

	return "", fmt.Errorf("secret %s not found in secrets file or environment", name)
}

// GetDecryptedSecretNames returns a list of secret names with type info (not values).
func GetDecryptedSecretNames() []SecretNameEntry {
	decryptedSecretsMux.RLock()
	defer decryptedSecretsMux.RUnlock()

	if decryptedSecrets == nil {
		return []SecretNameEntry{}
	}

	entries := make([]SecretNameEntry, 0, len(decryptedSecrets.System)+len(decryptedSecrets.User))
	for name := range decryptedSecrets.System {
		entries = append(entries, SecretNameEntry{Name: name, Type: SecretTypeSystem})
	}
	for name := range decryptedSecrets.User {
		entries = append(entries, SecretNameEntry{Name: name, Type: SecretTypeUser})
	}
	return entries
}

// SetSecret sets a secret value in the specified bucket.
// System secrets are validated against the systemSecretNames allowlist.
func SetSecret(name, value string, secretType SecretType) error {
	if err := ValidateSecretName(name); err != nil {
		return err
	}

	if secretType == SecretTypeSystem {
		if !systemSecretNames[name] {
			return fmt.Errorf("unknown system secret name %q (allowed: ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_GENAI_API_KEY, GITHUB_TOKEN, SSL_KEY_PEM)", name)
		}
	}

	decryptedSecretsMux.Lock()
	defer decryptedSecretsMux.Unlock()

	if decryptedSecrets == nil {
		decryptedSecrets = &StructuredSecrets{
			System: make(map[string]string),
			User:   make(map[string]string),
		}
	}

	switch secretType {
	case SecretTypeSystem:
		if decryptedSecrets.System == nil {
			decryptedSecrets.System = make(map[string]string)
		}
		decryptedSecrets.System[name] = value
		// Sync to environment so code using os.Getenv() directly picks up the change
		_ = os.Setenv(name, value)
	default:
		if decryptedSecrets.User == nil {
			decryptedSecrets.User = make(map[string]string)
		}
		decryptedSecrets.User[name] = value
	}
	return nil
}

// DeleteSecret removes a secret from the specified bucket.
func DeleteSecret(name string, secretType SecretType) error {
	decryptedSecretsMux.Lock()
	defer decryptedSecretsMux.Unlock()

	if decryptedSecrets == nil {
		return nil
	}

	switch secretType {
	case SecretTypeSystem:
		delete(decryptedSecrets.System, name)
		// Remove from environment to stay in sync
		_ = os.Unsetenv(name)
	default:
		delete(decryptedSecrets.User, name)
	}
	return nil
}

// GetUserSecrets returns a copy of user secrets for container injection.
func GetUserSecrets() map[string]string {
	decryptedSecretsMux.RLock()
	defer decryptedSecretsMux.RUnlock()

	if decryptedSecrets == nil || len(decryptedSecrets.User) == 0 {
		return nil
	}

	result := make(map[string]string, len(decryptedSecrets.User))
	for k, v := range decryptedSecrets.User {
		result[k] = v
	}
	return result
}

// SaveSecretsToFile saves the current in-memory secrets to the encrypted file.
func SaveSecretsToFile(projectDir, password string) error {
	decryptedSecretsMux.RLock()
	secretsCopy := &StructuredSecrets{
		System: make(map[string]string),
		User:   make(map[string]string),
	}
	if decryptedSecrets != nil {
		for k, v := range decryptedSecrets.System {
			secretsCopy.System[k] = v
		}
		for k, v := range decryptedSecrets.User {
			secretsCopy.User[k] = v
		}
	}
	decryptedSecretsMux.RUnlock()

	return EncryptSecretsFile(projectDir, password, secretsCopy)
}

// SecretsFileExists checks if secrets.json.enc exists in project directory.
func SecretsFileExists(projectDir string) bool {
	path := filepath.Join(projectDir, ".maestro", secretsFileName)
	_, err := os.Stat(path)
	return err == nil
}

// EncryptSecretsFile encrypts and saves secrets to .maestro/secrets.json.enc.
// Sets file permissions to 0600 for security.
func EncryptSecretsFile(projectDir, password string, secrets *StructuredSecrets) error {
	// Convert password to bytes
	passwordBytes := []byte(password)
	defer func() {
		for i := range passwordBytes {
			passwordBytes[i] = 0
		}
	}()

	// Generate random salt
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive encryption key using scrypt
	key, err := scrypt.Key(passwordBytes, salt, scryptN, scryptR, scryptP, keySize)
	if err != nil {
		return fmt.Errorf("failed to derive encryption key: %w", err)
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()

	// Marshal secrets to JSON
	plaintext, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("failed to marshal secrets: %w", err)
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt plaintext
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Construct final file: [salt][nonce][ciphertext+tag]
	fileData := make([]byte, 0, saltSize+nonceSize+len(ciphertext))
	fileData = append(fileData, salt...)
	fileData = append(fileData, nonce...)
	fileData = append(fileData, ciphertext...)

	// Ensure .maestro directory exists
	maestroDir := filepath.Join(projectDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return fmt.Errorf("failed to create .maestro directory: %w", err)
	}

	// Write to file with secure permissions
	path := filepath.Join(maestroDir, secretsFileName)
	if err := os.WriteFile(path, fileData, 0600); err != nil {
		return fmt.Errorf("failed to write secrets file: %w", err)
	}

	return nil
}

// DecryptSecretsFile decrypts and returns secrets from .maestro/secrets.json.enc.
// Supports both the new structured format and legacy flat map format (auto-migrates).
func DecryptSecretsFile(projectDir, password string) (*StructuredSecrets, error) {
	path := filepath.Join(projectDir, ".maestro", secretsFileName)

	// Check file permissions
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat secrets file: %w", err)
	}

	// Check permissions and fix if needed
	if info.Mode().Perm() != 0600 {
		LogInfo("⚠️  Secrets file has incorrect permissions (found: %04o, expected: 0600).", info.Mode().Perm())
		LogInfo("   This is a security risk. Maestro will fix this automatically.")
		if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
			return nil, fmt.Errorf("failed to fix file permissions: %w", chmodErr)
		}
		LogInfo("✅ File permissions corrected to 0600")
	}

	// Read encrypted file
	fileData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read secrets file: %w", err)
	}

	// Validate file size
	minSize := saltSize + nonceSize + 16 // 16 is GCM tag size
	if len(fileData) < minSize {
		return nil, fmt.Errorf("secrets file is corrupted or invalid format (too small)")
	}

	// Extract salt, nonce, and ciphertext
	salt := fileData[:saltSize]
	nonce := fileData[saltSize : saltSize+nonceSize]
	ciphertext := fileData[saltSize+nonceSize:]

	// Convert password to bytes
	passwordBytes := []byte(password)
	defer func() {
		for i := range passwordBytes {
			passwordBytes[i] = 0
		}
	}()

	// Derive decryption key using scrypt
	key, err := scrypt.Key(passwordBytes, salt, scryptN, scryptR, scryptP, keySize)
	if err != nil {
		return nil, fmt.Errorf("failed to derive decryption key: %w", err)
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Decrypt ciphertext
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (wrong password or corrupted file)")
	}

	// Try structured format first
	var structured StructuredSecrets
	if err := json.Unmarshal(plaintext, &structured); err == nil && (structured.System != nil || structured.User != nil) {
		if structured.System == nil {
			structured.System = make(map[string]string)
		}
		if structured.User == nil {
			structured.User = make(map[string]string)
		}
		return &structured, nil
	}

	// Fall back to legacy flat map format (auto-migrate: all go to system bucket)
	var flatSecrets map[string]string
	if err := json.Unmarshal(plaintext, &flatSecrets); err != nil {
		return nil, fmt.Errorf("failed to parse secrets: %w", err)
	}

	migrated := &StructuredSecrets{
		System: flatSecrets,
		User:   make(map[string]string),
	}
	return migrated, nil
}

// GetSSLCertAndKey returns SSL certificate and key bytes.
// Follows precedence: config paths → secrets file → env vars.
func GetSSLCertAndKey(cfg *Config, projectDir string) (certPEM, keyPEM []byte, err error) {
	if cfg == nil || cfg.WebUI == nil {
		return nil, nil, fmt.Errorf("webui configuration not found")
	}

	if projectDir == "" {
		projectDir = "."
	}

	// 1. Try config paths first
	if cfg.WebUI.Cert != "" {
		certPath := cfg.WebUI.Cert
		if !filepath.IsAbs(certPath) {
			certPath = filepath.Join(projectDir, certPath)
		}
		certData, err := os.ReadFile(certPath)
		if err == nil {
			certPEM = certData
		}
	}

	if cfg.WebUI.Key != "" {
		keyPath := cfg.WebUI.Key
		if !filepath.IsAbs(keyPath) {
			keyPath = filepath.Join(projectDir, keyPath)
		}
		keyData, err := os.ReadFile(keyPath)
		if err == nil {
			keyPEM = keyData
		}
	}

	// 2. Try secrets file
	if certPEM == nil || keyPEM == nil {
		if keyPEM == nil {
			if key, err := GetSecret("SSL_KEY_PEM"); err == nil && key != "" {
				keyPEM = []byte(key)
			}
		}
	}

	// 3. Try environment variables
	if certPEM == nil {
		if cert := os.Getenv("MAESTRO_SSL_CERT"); cert != "" {
			certPEM = []byte(cert)
		}
	}
	if keyPEM == nil {
		if key := os.Getenv("MAESTRO_SSL_KEY"); key != "" {
			keyPEM = []byte(key)
		}
	}

	// Validate we have both
	if certPEM == nil {
		return nil, nil, fmt.Errorf("SSL certificate not found (checked: config paths, env vars)")
	}
	if keyPEM == nil {
		return nil, nil, fmt.Errorf("SSL private key not found (checked: config paths, secrets file, env vars)")
	}

	return certPEM, keyPEM, nil
}
