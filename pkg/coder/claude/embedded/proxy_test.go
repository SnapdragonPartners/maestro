package embedded

import (
	"testing"
)

func TestGetProxyBinary_ARM64(t *testing.T) {
	// Test ARM64 architectures
	for _, arch := range []string{"aarch64", "arm64"} {
		binary, err := GetProxyBinary(arch)
		if err != nil {
			t.Errorf("GetProxyBinary(%q) error: %v", arch, err)
			continue
		}
		if len(binary) == 0 {
			t.Errorf("GetProxyBinary(%q) returned empty binary", arch)
		}
		// Verify it's an ELF binary (Linux executable)
		if len(binary) > 4 && string(binary[:4]) != "\x7fELF" {
			t.Errorf("GetProxyBinary(%q) returned non-ELF binary (magic: %x)", arch, binary[:4])
		}
	}
}

func TestGetProxyBinary_AMD64(t *testing.T) {
	// Test AMD64 architectures
	for _, arch := range []string{"x86_64", "amd64"} {
		binary, err := GetProxyBinary(arch)
		if err != nil {
			t.Errorf("GetProxyBinary(%q) error: %v", arch, err)
			continue
		}
		if len(binary) == 0 {
			t.Errorf("GetProxyBinary(%q) returned empty binary", arch)
		}
		// Verify it's an ELF binary (Linux executable)
		if len(binary) > 4 && string(binary[:4]) != "\x7fELF" {
			t.Errorf("GetProxyBinary(%q) returned non-ELF binary (magic: %x)", arch, binary[:4])
		}
	}
}

func TestGetProxyBinary_Unsupported(t *testing.T) {
	unsupported := []string{"i386", "ppc64", "mips", "riscv64", ""}
	for _, arch := range unsupported {
		_, err := GetProxyBinary(arch)
		if err == nil {
			t.Errorf("GetProxyBinary(%q) should return error for unsupported arch", arch)
		}
	}
}

func TestHasEmbeddedBinaries(t *testing.T) {
	// After running make build-mcp-proxy, this should be true
	if !HasEmbeddedBinaries() {
		t.Skip("Embedded binaries not available (run 'make build-mcp-proxy' first)")
	}

	// If binaries are embedded, verify both architectures work
	arm64, err := GetProxyBinary("arm64")
	if err != nil {
		t.Errorf("ARM64 binary not available: %v", err)
	}
	amd64, err := GetProxyBinary("amd64")
	if err != nil {
		t.Errorf("AMD64 binary not available: %v", err)
	}

	// Both should be non-empty
	if len(arm64) == 0 || len(amd64) == 0 {
		t.Error("Expected non-empty binaries for both architectures")
	}

	// They should be different (different architectures)
	if len(arm64) == len(amd64) {
		// Same size doesn't guarantee they're the same, but let's check
		t.Logf("ARM64 and AMD64 binaries have same size: %d bytes", len(arm64))
	}
}
