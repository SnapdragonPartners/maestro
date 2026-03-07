package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndVerifyPassword(t *testing.T) {
	dir := t.TempDir()

	// Create .maestro subdirectory (SavePasswordVerifier creates it, but test structure)
	if err := SavePasswordVerifier(dir, "test-password-123"); err != nil {
		t.Fatalf("SavePasswordVerifier failed: %v", err)
	}

	// Correct password should verify
	ok, err := VerifyPassword(dir, "test-password-123")
	if err != nil {
		t.Fatalf("VerifyPassword returned error: %v", err)
	}
	if !ok {
		t.Fatal("VerifyPassword returned false for correct password")
	}
}

func TestVerifyWrongPassword(t *testing.T) {
	dir := t.TempDir()

	if err := SavePasswordVerifier(dir, "correct-password"); err != nil {
		t.Fatalf("SavePasswordVerifier failed: %v", err)
	}

	ok, err := VerifyPassword(dir, "wrong-password")
	if err != nil {
		t.Fatalf("VerifyPassword returned error: %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword returned true for wrong password")
	}
}

func TestPasswordVerifierExists(t *testing.T) {
	dir := t.TempDir()

	// Should not exist yet
	if PasswordVerifierExists(dir) {
		t.Fatal("PasswordVerifierExists returned true for empty directory")
	}

	// Create verifier
	if err := SavePasswordVerifier(dir, "password"); err != nil {
		t.Fatalf("SavePasswordVerifier failed: %v", err)
	}

	// Should exist now
	if !PasswordVerifierExists(dir) {
		t.Fatal("PasswordVerifierExists returned false after SavePasswordVerifier")
	}
}

func TestVerifierMissingFile(t *testing.T) {
	dir := t.TempDir()

	_, err := VerifyPassword(dir, "password")
	if err == nil {
		t.Fatal("VerifyPassword should return error for missing file")
	}
}

func TestVerifierFilePermissions(t *testing.T) {
	dir := t.TempDir()

	if err := SavePasswordVerifier(dir, "password"); err != nil {
		t.Fatalf("SavePasswordVerifier failed: %v", err)
	}

	path := filepath.Join(dir, ".maestro", passwordVerifierFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Failed to stat verifier file: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Fatalf("Expected permissions 0600, got %04o", perm)
	}
}

func TestVerifierJSONStructure(t *testing.T) {
	dir := t.TempDir()

	if err := SavePasswordVerifier(dir, "password"); err != nil {
		t.Fatalf("SavePasswordVerifier failed: %v", err)
	}

	path := filepath.Join(dir, ".maestro", passwordVerifierFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read verifier file: %v", err)
	}

	var v passwordVerifier
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("Failed to parse verifier JSON: %v", err)
	}

	if v.Version != 1 {
		t.Fatalf("Expected version 1, got %d", v.Version)
	}
	if v.Salt == "" {
		t.Fatal("Salt is empty")
	}
	if v.Hash == "" {
		t.Fatal("Hash is empty")
	}
}

func TestVerifierUniqueSalts(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	if err := SavePasswordVerifier(dir1, "same-password"); err != nil {
		t.Fatalf("SavePasswordVerifier 1 failed: %v", err)
	}
	if err := SavePasswordVerifier(dir2, "same-password"); err != nil {
		t.Fatalf("SavePasswordVerifier 2 failed: %v", err)
	}

	data1, _ := os.ReadFile(filepath.Join(dir1, ".maestro", passwordVerifierFile))
	data2, _ := os.ReadFile(filepath.Join(dir2, ".maestro", passwordVerifierFile))

	var v1, v2 passwordVerifier
	_ = json.Unmarshal(data1, &v1)
	_ = json.Unmarshal(data2, &v2)

	if v1.Salt == v2.Salt {
		t.Fatal("Two saves produced identical salts — salt generation is broken")
	}
}

func TestVerifierCorruptedSaltLength(t *testing.T) {
	dir := t.TempDir()

	if err := SavePasswordVerifier(dir, "password"); err != nil {
		t.Fatalf("SavePasswordVerifier failed: %v", err)
	}

	// Tamper with the verifier file: replace salt with a short value
	path := filepath.Join(dir, ".maestro", passwordVerifierFile)
	data, _ := os.ReadFile(path)
	var v passwordVerifier
	_ = json.Unmarshal(data, &v)
	v.Salt = "c2hvcnQ=" // "short" in base64 (5 bytes, not 32)
	tampered, _ := json.MarshalIndent(v, "", "  ")
	_ = os.WriteFile(path, tampered, 0600)

	_, err := VerifyPassword(dir, "password")
	if err == nil {
		t.Fatal("VerifyPassword should return error for corrupted salt length")
	}
}

func TestVerifierCorruptedHashLength(t *testing.T) {
	dir := t.TempDir()

	if err := SavePasswordVerifier(dir, "password"); err != nil {
		t.Fatalf("SavePasswordVerifier failed: %v", err)
	}

	// Tamper with the verifier file: replace hash with a short value
	path := filepath.Join(dir, ".maestro", passwordVerifierFile)
	data, _ := os.ReadFile(path)
	var v passwordVerifier
	_ = json.Unmarshal(data, &v)
	v.Hash = "c2hvcnQ=" // "short" in base64 (5 bytes, not 32)
	tampered, _ := json.MarshalIndent(v, "", "  ")
	_ = os.WriteFile(path, tampered, 0600)

	_, err := VerifyPassword(dir, "password")
	if err == nil {
		t.Fatal("VerifyPassword should return error for corrupted hash length")
	}
}

func TestVerifierOverwriteOnPasswordChange(t *testing.T) {
	dir := t.TempDir()

	// Save verifier for password A
	if err := SavePasswordVerifier(dir, "password-A"); err != nil {
		t.Fatalf("SavePasswordVerifier failed: %v", err)
	}

	// Overwrite verifier with password B
	if err := SavePasswordVerifier(dir, "password-B"); err != nil {
		t.Fatalf("SavePasswordVerifier (overwrite) failed: %v", err)
	}

	// Password A should no longer verify
	ok, err := VerifyPassword(dir, "password-A")
	if err != nil {
		t.Fatalf("VerifyPassword returned error: %v", err)
	}
	if ok {
		t.Fatal("Old password should not verify after overwrite")
	}

	// Password B should verify
	ok, err = VerifyPassword(dir, "password-B")
	if err != nil {
		t.Fatalf("VerifyPassword returned error: %v", err)
	}
	if !ok {
		t.Fatal("New password should verify after overwrite")
	}
}

func TestVerifierNoTempFileLeftBehind(t *testing.T) {
	dir := t.TempDir()

	if err := SavePasswordVerifier(dir, "password"); err != nil {
		t.Fatalf("SavePasswordVerifier failed: %v", err)
	}

	tmpPath := filepath.Join(dir, ".maestro", passwordVerifierFile+".tmp")
	if _, err := os.Stat(tmpPath); err == nil {
		t.Fatal("Temp file was not cleaned up after atomic write")
	}
}
