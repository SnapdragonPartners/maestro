// Package utils provides utility functions for container and repository operations.
package utils

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"orchestrator/pkg/coder/claude/embedded"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dockerfiles"
	"orchestrator/pkg/version"
)

const bootstrapHashLabel = "com.maestro.content-hash"

// CreateTempRepoClone creates a temporary clone of the repository for building.
// Returns the temporary directory path and a cleanup function.
func CreateTempRepoClone(ctx context.Context, repoURL, _ string) (string, func(), error) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "maestro-build-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(tempDir) // Best effort cleanup
	}

	// Clone repository to temp directory
	cmd := exec.CommandContext(ctx, "git", "clone", repoURL, tempDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git clone failed: %w\nOutput: %s", err, string(output))
	}

	return tempDir, cleanup, nil
}

// BuildContainerFromDockerfile builds a container from a dockerfile.
// This is a simplified version of the container_build tool for orchestrator use.
func BuildContainerFromDockerfile(ctx context.Context, dockerfilePath, imageName, workDir string) error {
	// Build using docker build with BuildKit enabled
	args := []string{"docker", "build", "-t", imageName, "-f", dockerfilePath, "."}

	// Set timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "docker", args[0])
	cmd.Args = args
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker build failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// IsImageHealthy checks if a Docker image exists and can boot successfully.
func IsImageHealthy(ctx context.Context, imageID string) error {
	// Check if image exists
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", imageID)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("image %s does not exist: %w", imageID, err)
	}

	// Basic boot test
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd = exec.CommandContext(timeoutCtx, "docker", "run", "--rm", imageID, "echo", "health_check")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("image %s failed health check: %w\nOutput: %s", imageID, err, string(output))
	}

	if !strings.Contains(string(output), "health_check") {
		return fmt.Errorf("image %s health check returned unexpected output: %s", imageID, string(output))
	}

	return nil
}

// GetImageID gets the full image ID for a given image name or tag.
func GetImageID(ctx context.Context, imageName string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", "--format={{.Id}}", imageName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get image ID for %s: %w", imageName, err)
	}

	return strings.TrimSpace(string(output)), nil
}

// BootstrapContentHash computes a deterministic hash of all inputs to the bootstrap
// image (Dockerfile, embedded configs, build version). Used to detect when the image
// needs rebuilding after code updates. The build version (set via ldflags by goreleaser)
// ensures rebuilds on new maestro releases without hashing the full proxy binary.
func BootstrapContentHash() string {
	h := sha256.New()
	h.Write([]byte(dockerfiles.GetBootstrapDockerfile()))
	h.Write([]byte(dockerfiles.GetAgentshServerConfig()))
	h.Write([]byte(dockerfiles.GetAgentshEntrypoint()))
	h.Write([]byte(dockerfiles.GetAgentshDefaultPolicy()))
	h.Write([]byte(version.Version))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// IsBootstrapStale checks whether the existing bootstrap image was built from
// different inputs than the current binary. Returns true if the image should be rebuilt.
// Returns false if the image doesn't exist (caller should use IsImageHealthy for that).
func IsBootstrapStale(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect",
		"--format", fmt.Sprintf("{{index .Config.Labels %q}}", bootstrapHashLabel),
		config.BootstrapContainerTag)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false // Image doesn't exist or no label — not "stale", just missing
	}
	imageHash := strings.TrimSpace(string(output))
	return imageHash != BootstrapContentHash()
}

// BuildBootstrapImage builds the maestro-bootstrap container image from the embedded
// Dockerfile and pre-compiled MCP proxy binary. The Dockerfile expects the proxy binary
// to be present in the build context; this function writes the embedded binary there.
func BuildBootstrapImage(ctx context.Context) error {
	// Create temporary build context directory
	buildDir, err := os.MkdirTemp("", "maestro-bootstrap-build-*")
	if err != nil {
		return fmt.Errorf("failed to create temp build directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(buildDir) }()

	// Write the pre-compiled MCP proxy binary for the host architecture.
	// runtime.GOARCH returns "arm64" or "amd64" which maps to the embedded binary names.
	proxyBinary, proxyErr := embedded.GetProxyBinary(runtime.GOARCH)
	if proxyErr != nil {
		return fmt.Errorf("failed to get embedded proxy binary for %s: %w", runtime.GOARCH, proxyErr)
	}

	proxyPath := filepath.Join(buildDir, "maestro-mcp-proxy")
	//nolint:gosec // Proxy binary must be executable
	if writeErr := os.WriteFile(proxyPath, proxyBinary, 0755); writeErr != nil {
		return fmt.Errorf("failed to write proxy binary: %w", writeErr)
	}

	// Write the embedded Dockerfile to the build context
	dockerfilePath := filepath.Join(buildDir, "Dockerfile")
	if writeErr := os.WriteFile(dockerfilePath, []byte(dockerfiles.GetBootstrapDockerfile()), 0644); writeErr != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", writeErr)
	}

	// Write agentsh config and entrypoint to the build context.
	// The Dockerfile COPYs config.yaml into /etc/agentsh/ and entrypoint.sh into /usr/local/bin/.
	// Policies are extracted from the agentsh release tarball at build time (not embedded).
	agentshDir := filepath.Join(buildDir, "agentsh")
	if mkdirErr := os.MkdirAll(agentshDir, 0755); mkdirErr != nil {
		return fmt.Errorf("failed to create agentsh config directory: %w", mkdirErr)
	}
	if writeErr := os.WriteFile(filepath.Join(agentshDir, "config.yaml"), []byte(dockerfiles.GetAgentshServerConfig()), 0644); writeErr != nil {
		return fmt.Errorf("failed to write agentsh server config: %w", writeErr)
	}
	//nolint:gosec // Entrypoint script must be executable
	if writeErr := os.WriteFile(filepath.Join(agentshDir, "entrypoint.sh"), []byte(dockerfiles.GetAgentshEntrypoint()), 0755); writeErr != nil {
		return fmt.Errorf("failed to write agentsh entrypoint: %w", writeErr)
	}
	if writeErr := os.WriteFile(filepath.Join(agentshDir, "maestro-policy.yaml"), []byte(dockerfiles.GetAgentshDefaultPolicy()), 0644); writeErr != nil {
		return fmt.Errorf("failed to write agentsh maestro policy: %w", writeErr)
	}

	// Build the image with a content hash label for staleness detection.
	contentHash := BootstrapContentHash()
	cmd := exec.CommandContext(ctx, "docker", "build",
		"-t", config.BootstrapContainerTag,
		"--label", fmt.Sprintf("%s=%s", bootstrapHashLabel, contentHash),
		buildDir)
	output, buildErr := cmd.CombinedOutput()
	if buildErr != nil {
		return fmt.Errorf("docker build failed: %w\nOutput: %s", buildErr, string(output))
	}

	return nil
}
