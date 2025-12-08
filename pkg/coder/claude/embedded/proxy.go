// Package embedded provides embedded binary assets for the Claude Code integration.
// The MCP proxy binaries are cross-compiled for Linux (ARM64 and AMD64) at build time
// and embedded in the maestro binary for distribution to containers.
package embedded

import (
	_ "embed"
	"fmt"
)

// Embedded proxy binaries for Linux containers.
// These are built by `make build-mcp-proxy` before the main build.
//
//go:embed proxy-linux-arm64
var proxyLinuxArm64 []byte

//go:embed proxy-linux-amd64
var proxyLinuxAmd64 []byte

// GetProxyBinary returns the MCP proxy binary for the given architecture.
// The arch parameter should be the output of `uname -m` from the container:
//   - "aarch64" or "arm64" -> returns ARM64 binary
//   - "x86_64" or "amd64" -> returns AMD64 binary
//
// Returns an error for unsupported architectures.
func GetProxyBinary(arch string) ([]byte, error) {
	switch arch {
	case "aarch64", "arm64":
		if len(proxyLinuxArm64) == 0 {
			return nil, fmt.Errorf("ARM64 proxy binary not embedded (run 'make build-mcp-proxy' first)")
		}
		return proxyLinuxArm64, nil
	case "x86_64", "amd64":
		if len(proxyLinuxAmd64) == 0 {
			return nil, fmt.Errorf("AMD64 proxy binary not embedded (run 'make build-mcp-proxy' first)")
		}
		return proxyLinuxAmd64, nil
	default:
		return nil, fmt.Errorf("unsupported architecture: %s (supported: aarch64, arm64, x86_64, amd64)", arch)
	}
}

// HasEmbeddedBinaries returns true if the proxy binaries are embedded.
// This can be used to check if the build was done with `make build-mcp-proxy`.
func HasEmbeddedBinaries() bool {
	return len(proxyLinuxArm64) > 0 && len(proxyLinuxAmd64) > 0
}
