package config

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/scrypt"
)

// Password verifier file: .maestro/.password-verifier.json
// Stores a scrypt-derived hash of the project password so we can verify
// a presented password without storing the password itself on disk.
// Uses the same scrypt parameters as secrets.go for consistency.

const passwordVerifierFile = ".password-verifier.json"

// Scrypt parameters for verifier (same as secrets.go).
const (
	verifierSaltSize = 32
	verifierKeyLen   = 32
	verifierN        = 32768 // 2^15
	verifierR        = 8
	verifierP        = 1
)

type passwordVerifier struct {
	Version int    `json:"version"`
	Salt    string `json:"salt"` // base64-encoded random salt
	Hash    string `json:"hash"` // base64-encoded scrypt output
}

// SavePasswordVerifier creates a scrypt verifier for the given password
// and writes it atomically to .maestro/.password-verifier.json (mode 0600).
func SavePasswordVerifier(projectDir, password string) error {
	// Generate random salt
	salt := make([]byte, verifierSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive key using scrypt
	hash, err := scrypt.Key([]byte(password), salt, verifierN, verifierR, verifierP, verifierKeyLen)
	if err != nil {
		return fmt.Errorf("failed to derive verifier hash: %w", err)
	}

	v := passwordVerifier{
		Version: 1,
		Salt:    base64.StdEncoding.EncodeToString(salt),
		Hash:    base64.StdEncoding.EncodeToString(hash),
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal verifier: %w", err)
	}

	maestroDir := filepath.Join(projectDir, ".maestro")
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		return fmt.Errorf("failed to create .maestro directory: %w", mkdirErr)
	}

	finalPath := filepath.Join(maestroDir, passwordVerifierFile)
	tmpPath := finalPath + ".tmp"

	// Atomic write: temp file -> fsync -> rename
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create temp verifier file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write verifier: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to fsync verifier: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close verifier file: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to atomically install verifier file: %w", err)
	}

	return nil
}

// VerifyPassword checks whether the given password matches the stored verifier.
// Returns (true, nil) on match, (false, nil) on mismatch.
// Returns an error only on I/O or parse failures.
func VerifyPassword(projectDir, password string) (bool, error) {
	path := filepath.Join(projectDir, ".maestro", passwordVerifierFile)

	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("failed to read verifier file: %w", err)
	}

	var v passwordVerifier
	if parseErr := json.Unmarshal(data, &v); parseErr != nil {
		return false, fmt.Errorf("failed to parse verifier file: %w", parseErr)
	}

	if v.Version != 1 {
		return false, fmt.Errorf("unsupported verifier version: %d", v.Version)
	}

	salt, err := base64.StdEncoding.DecodeString(v.Salt)
	if err != nil {
		return false, fmt.Errorf("failed to decode salt: %w", err)
	}

	storedHash, err := base64.StdEncoding.DecodeString(v.Hash)
	if err != nil {
		return false, fmt.Errorf("failed to decode hash: %w", err)
	}

	// Re-derive using the same parameters
	derived, err := scrypt.Key([]byte(password), salt, verifierN, verifierR, verifierP, verifierKeyLen)
	if err != nil {
		return false, fmt.Errorf("failed to derive key for verification: %w", err)
	}

	// Constant-time comparison
	if subtle.ConstantTimeCompare(derived, storedHash) == 1 {
		return true, nil
	}

	return false, nil
}

// PasswordVerifierExists checks whether .maestro/.password-verifier.json exists.
func PasswordVerifierExists(projectDir string) bool {
	path := filepath.Join(projectDir, ".maestro", passwordVerifierFile)
	_, err := os.Stat(path)
	return err == nil
}
