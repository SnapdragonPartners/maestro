// Package workspace provides workspace verification and validation functionality.
package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/config"
)

// ValidationState represents the outcome of container validation.
type ValidationState int

// Container validation states.
const (
	ValidationPass           ValidationState = iota // Container is ready to use
	ValidationNeedBootstrap                         // Bootstrap required (detect/dockerfile modes)
	ValidationConfigError                           // Configuration error (user needs to fix)
	ValidationTransientError                        // Transient error (network/registry issues)
)

// ContainerValidationResult contains the results of container validation.
//
//nolint:govet // Logical field grouping preferred over memory optimization
type ContainerValidationResult struct {
	State                ValidationState             `json:"state"`
	Reason               string                      `json:"reason"`
	Details              error                       `json:"details,omitempty"`
	ImageValidation      *ImageValidationResult      `json:"image_validation,omitempty"`
	DockerfileValidation *DockerfileValidationResult `json:"dockerfile_validation,omitempty"`
	DockerDaemonOK       bool                        `json:"docker_daemon_ok"`
	ValidationMethod     string                      `json:"validation_method"` // "image" or "dockerfile"
}

// ImageValidationResult contains results of Docker image validation.
//
//nolint:govet // Logical field grouping preferred over memory optimization
type ImageValidationResult struct {
	ImageName     string `json:"image_name"`
	ErrorMessage  string `json:"error_message,omitempty"`
	PullSucceeded bool   `json:"pull_succeeded"`
	RunSucceeded  bool   `json:"run_succeeded"`
}

// DockerfileValidationResult contains results of Dockerfile validation.
//
//nolint:govet // Logical field grouping preferred over memory optimization
type DockerfileValidationResult struct {
	DockerfilePath string `json:"dockerfile_path"`
	ImageTag       string `json:"image_tag"`
	ErrorMessage   string `json:"error_message,omitempty"`
	BuildSucceeded bool   `json:"build_succeeded"`
	RunSucceeded   bool   `json:"run_succeeded"`
}

// ValidateContainer performs container validation based on project configuration.
// Returns tri-state validation result: PASS, NEED_BOOTSTRAP, CONFIG_ERROR, or TRANSIENT_ERROR.
func ValidateContainer(ctx context.Context, timeout time.Duration) (*ContainerValidationResult, error) {
	result := &ContainerValidationResult{}

	// Get config (should already be loaded)
	cfg, err := config.GetConfig()
	if err != nil {
		result.State = ValidationConfigError
		result.Reason = fmt.Sprintf("config not loaded: %v", err)
		return result, nil
	}

	// First, check if Docker daemon is running
	result.DockerDaemonOK = checkDockerDaemon(ctx, timeout)
	if !result.DockerDaemonOK {
		result.State = ValidationConfigError
		result.Reason = "Docker daemon is not running or accessible"
		return result, nil
	}

	name := ""
	if cfg.Container != nil {
		name = cfg.Container.Name
	}
	if name == "" {
		// Check if dockerfile is specified - this is expected for dockerfile mode
		if cfg.Container != nil && cfg.Container.Dockerfile != "" {
			result.State = ValidationNeedBootstrap
			result.Reason = "container.name empty and dockerfile specified. Container must be built."
			return result, nil
		}
		result.State = ValidationConfigError
		result.Reason = "container.name must be set"
		return result, nil
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch {
	// 1. Bootstrap cases
	case name == "detect":
		result.State = ValidationNeedBootstrap
		result.Reason = `"detect" sentinel value`
		return result, nil

	case cfg.Container != nil && cfg.Container.Dockerfile != "":
		// Dockerfile mode - check if dockerfile exists
		dockerfilePath := cfg.Container.Dockerfile
		if !fileExists(dockerfilePath) {
			result.State = ValidationConfigError
			result.Reason = fmt.Sprintf("dockerfile %s not found", dockerfilePath)
			return result, nil
		}
		result.State = ValidationNeedBootstrap
		result.Reason = "dockerfile provided"
		return result, nil

	// 2. Image validation - check if it exists and can run
	default:
		return validateImageExists(timeoutCtx, name, result), nil
	}
}

// checkDockerDaemon verifies that Docker daemon is running and accessible.
func checkDockerDaemon(ctx context.Context, timeout time.Duration) bool {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "docker", "version", "--format", "{{.Server.Version}}")
	err := cmd.Run()
	return err == nil
}

// fileExists checks if a file exists at the given path.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// validateImageExists checks if an image exists locally or can be pulled and run.
func validateImageExists(ctx context.Context, imageName string, result *ContainerValidationResult) *ContainerValidationResult {
	// 1. Check if image exists locally
	if inspectLocalImage(ctx, imageName) == nil {
		if runImageSmokeTest(ctx, imageName) == nil {
			result.State = ValidationPass
			result.Reason = "image available locally and runnable"
			result.ValidationMethod = "local"
			return result
		}
		result.State = ValidationConfigError
		result.Reason = "image present locally but failed health check"
		return result
	}

	// 2. Try pulling from registry
	if err := dockerPull(ctx, imageName); err != nil {
		if isAuthError(err) {
			result.State = ValidationConfigError
			result.Reason = "private registry requires credentials"
			result.Details = err
			return result
		}
		if isNotFoundError(err) {
			result.State = ValidationConfigError
			result.Reason = "image not found in registry"
			result.Details = err
			return result
		}
		result.State = ValidationTransientError
		result.Reason = "registry/network failure"
		result.Details = err
		return result
	}

	// 3. Run smoke test on pulled image
	if err := runImageSmokeTest(ctx, imageName); err != nil {
		result.State = ValidationConfigError
		result.Reason = "pulled image failed health check"
		result.Details = err
		return result
	}

	result.State = ValidationPass
	result.Reason = "image pulled successfully and runnable"
	result.ValidationMethod = "pull"
	return result
}

// inspectLocalImage checks if image exists locally.
func inspectLocalImage(ctx context.Context, imageName string) error {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", imageName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker image inspect failed: %w", err)
	}
	return nil
}

// dockerPull pulls an image from registry.
func dockerPull(ctx context.Context, imageName string) error {
	cmd := exec.CommandContext(ctx, "docker", "pull", "--platform", "linux/amd64", imageName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker pull failed: %w (output: %s)", err, string(output))
	}
	return nil
}

// runImageSmokeTest runs a simple test to verify image works.
func runImageSmokeTest(ctx context.Context, imageName string) error {
	cmd := exec.CommandContext(ctx, "docker", "run",
		"--rm",
		"--platform", "linux/amd64",
		"--network=none",
		imageName,
		"echo", "ok")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("smoke test failed: %w (output: %s)", err, string(output))
	}
	if !strings.Contains(string(output), "ok") {
		return fmt.Errorf("smoke test did not produce expected output: %s", string(output))
	}
	return nil
}

// isAuthError checks if error is due to authentication.
func isAuthError(err error) bool {
	errStr := err.Error()
	return strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "authentication required") ||
		strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "403")
}

// isNotFoundError checks if error is due to image not found.
func isNotFoundError(err error) bool {
	errStr := err.Error()
	return strings.Contains(errStr, "not found") ||
		strings.Contains(errStr, "404") ||
		strings.Contains(errStr, "pull access denied")
}

// GenerateContainerValidationReport generates a human-readable report of container validation results.
func GenerateContainerValidationReport(result *ContainerValidationResult) string {
	var report strings.Builder

	report.WriteString("# Container Validation Report\n\n")

	// Docker daemon status
	if result.DockerDaemonOK {
		report.WriteString("✅ **Docker Daemon:** Running and accessible\n\n")
	} else {
		report.WriteString("❌ **Docker Daemon:** Not running or not accessible\n\n")
		return report.String() // No point in continuing if Docker is not available
	}

	// Validation method
	report.WriteString(fmt.Sprintf("**Validation Method:** %s\n\n", result.ValidationMethod))

	// Image validation results
	if result.ImageValidation != nil {
		img := result.ImageValidation
		report.WriteString("## Image Validation\n\n")
		report.WriteString(fmt.Sprintf("**Image:** `%s`\n\n", img.ImageName))

		if img.PullSucceeded {
			report.WriteString("✅ **Pull:** Succeeded\n")
		} else {
			report.WriteString("❌ **Pull:** Failed\n")
		}

		if img.RunSucceeded {
			report.WriteString("✅ **Test Run:** Succeeded\n")
		} else {
			report.WriteString("❌ **Test Run:** Failed\n")
		}

		if img.ErrorMessage != "" {
			report.WriteString(fmt.Sprintf("\n**Error Details:** %s\n", img.ErrorMessage))
		}
	}

	// Dockerfile validation results
	if result.DockerfileValidation != nil {
		df := result.DockerfileValidation
		report.WriteString("## Dockerfile Validation\n\n")
		report.WriteString(fmt.Sprintf("**Dockerfile:** `%s`\n", df.DockerfilePath))
		report.WriteString(fmt.Sprintf("**Image Tag:** `%s`\n\n", df.ImageTag))

		if df.BuildSucceeded {
			report.WriteString("✅ **Build:** Succeeded\n")
		} else {
			report.WriteString("❌ **Build:** Failed\n")
		}

		if df.RunSucceeded {
			report.WriteString("✅ **Test Run:** Succeeded\n")
		} else {
			report.WriteString("❌ **Test Run:** Failed\n")
		}

		if df.ErrorMessage != "" {
			report.WriteString(fmt.Sprintf("\n**Error Details:** %s\n", df.ErrorMessage))
		}
	}

	report.WriteString("\n")
	return report.String()
}

// IsValid returns true if container validation passed completely.
func (r *ContainerValidationResult) IsValid() bool {
	return r.State == ValidationPass
}

// NeedsBootstrap returns true if bootstrap is required.
func (r *ContainerValidationResult) NeedsBootstrap() bool {
	return r.State == ValidationNeedBootstrap
}

// IsConfigError returns true if there's a configuration error.
func (r *ContainerValidationResult) IsConfigError() bool {
	return r.State == ValidationConfigError
}

// IsTransientError returns true if there's a transient error.
func (r *ContainerValidationResult) IsTransientError() bool {
	return r.State == ValidationTransientError
}

// GetFailureReason returns a human-readable reason for validation failure.
func (r *ContainerValidationResult) GetFailureReason() string {
	if r.Reason != "" {
		return r.Reason
	}
	return "Unknown container validation failure"
}
