package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/scrypt"
)

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
	decryptedSecrets    map[string]string
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
func SetDecryptedSecrets(secrets map[string]string) {
	decryptedSecretsMux.Lock()
	defer decryptedSecretsMux.Unlock()
	decryptedSecrets = secrets
}

// GetSecret returns a secret value by name using standard precedence:
// 1. Decrypted secrets file (in memory)
// 2. Environment variables.
func GetSecret(name string) (string, error) {
	// Check decrypted secrets first
	decryptedSecretsMux.RLock()
	if decryptedSecrets != nil {
		if value, exists := decryptedSecrets[name]; exists && value != "" {
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

// GetDecryptedSecretNames returns a list of secret names (not values).
func GetDecryptedSecretNames() []string {
	decryptedSecretsMux.RLock()
	defer decryptedSecretsMux.RUnlock()

	if decryptedSecrets == nil {
		return []string{}
	}

	names := make([]string, 0, len(decryptedSecrets))
	for name := range decryptedSecrets {
		names = append(names, name)
	}
	return names
}

// SetSecret sets a secret value in memory.
func SetSecret(name, value string) error {
	decryptedSecretsMux.Lock()
	defer decryptedSecretsMux.Unlock()

	if decryptedSecrets == nil {
		decryptedSecrets = make(map[string]string)
	}
	decryptedSecrets[name] = value
	return nil
}

// DeleteSecret removes a secret from memory.
func DeleteSecret(name string) error {
	decryptedSecretsMux.Lock()
	defer decryptedSecretsMux.Unlock()

	if decryptedSecrets == nil {
		return nil
	}
	delete(decryptedSecrets, name)
	return nil
}

// SaveSecretsToFile saves the current in-memory secrets to the encrypted file.
func SaveSecretsToFile(projectDir, password string) error {
	decryptedSecretsMux.RLock()
	secretsCopy := make(map[string]string, len(decryptedSecrets))
	for k, v := range decryptedSecrets {
		secretsCopy[k] = v
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
func EncryptSecretsFile(projectDir, password string, secrets map[string]string) error {
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
func DecryptSecretsFile(projectDir, password string) (map[string]string, error) {
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

	// Unmarshal JSON
	var secrets map[string]string
	if err := json.Unmarshal(plaintext, &secrets); err != nil {
		return nil, fmt.Errorf("failed to parse secrets: %w", err)
	}

	return secrets, nil
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
