package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/build"
	"orchestrator/pkg/config"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// handleTesting processes the TESTING state.
func (c *Coder) handleTesting(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Get worktree path for running tests.
	worktreePath, exists := sm.GetStateValue(KeyWorktreePath)
	if !exists || worktreePath == "" {
		return proto.StateError, false, logx.Errorf("no worktree path found - workspace setup required")
	}

	worktreePathStr, ok := utils.SafeAssert[string](worktreePath)
	if !ok {
		return proto.StateError, false, logx.Errorf("worktree_path is not a string: %v", worktreePath)
	}

	// Get story type for testing strategy decision.
	storyType := string(proto.StoryTypeApp) // Default to app
	if storyTypeVal, exists := sm.GetStateValue(proto.KeyStoryType); exists {
		if storyTypeStr, ok := storyTypeVal.(string); ok && proto.IsValidStoryType(storyTypeStr) {
			storyType = storyTypeStr
		}
	}

	c.logger.Info("Testing story type: %s", storyType)

	// Use different testing strategies based on story type
	if storyType == string(proto.StoryTypeDevOps) {
		return c.handleDevOpsStoryTesting(ctx, sm, worktreePathStr)
	}
	return c.handleAppStoryTesting(ctx, sm, worktreePathStr)
}

// handleAppStoryTesting handles testing for application stories using traditional build/test/lint flow.
func (c *Coder) handleAppStoryTesting(ctx context.Context, sm *agent.BaseStateMachine, worktreePathStr string) (proto.State, bool, error) {
	// Use MCP test tool instead of direct build registry calls.
	if c.buildService != nil {
		// Get backend info first.
		backendInfo, err := c.buildService.GetBackendInfo(worktreePathStr)
		if err != nil {
			c.logger.Error("Failed to get backend info: %v", err)
			return proto.StateError, false, logx.Wrap(err, "failed to get backend info")
		}

		// Store backend information for context.
		sm.SetStateData(KeyBuildBackend, backendInfo.Name)
		c.logger.Info("App story testing: using build service with backend %s", backendInfo.Name)

		// Run tests using the build service.
		testsPassed, testOutput, err := c.runTestWithBuildService(ctx, worktreePathStr)
		if err != nil {
			c.logger.Error("Failed to run tests: %v", err)
			// Truncate error output to prevent context bloat.
			errorStr := err.Error()
			truncatedError := truncateOutput(errorStr)
			sm.SetStateData(KeyTestError, errorStr)               // Keep full error for logging
			sm.SetStateData(KeyTestFailureOutput, truncatedError) // Use truncated for context
			sm.SetStateData(KeyCodingMode, "test_fix")
			return StateCoding, false, nil
		}

		// Store test results.
		sm.SetStateData(KeyTestsPassed, testsPassed)
		sm.SetStateData(KeyTestOutput, testOutput)
		sm.SetStateData(KeyTestingCompletedAt, time.Now().UTC())

		if !testsPassed {
			c.logger.Info("App story tests failed, transitioning to CODING state for fixes")
			// Truncate test output to prevent context bloat.
			truncatedOutput := truncateOutput(testOutput)
			sm.SetStateData(KeyTestFailureOutput, truncatedOutput)
			sm.SetStateData(KeyCodingMode, "test_fix")
			return StateCoding, false, nil
		}

		c.logger.Info("App story tests passed successfully")
		return c.proceedToCodeReview(ctx, sm)
	}

	// Fallback to legacy testing approach
	return c.handleLegacyTesting(ctx, sm, worktreePathStr)
}

// handleDevOpsStoryTesting handles testing for DevOps stories focusing on infrastructure validation.
func (c *Coder) handleDevOpsStoryTesting(ctx context.Context, sm *agent.BaseStateMachine, worktreePathStr string) (proto.State, bool, error) {
	c.logger.Info("DevOps story testing: focusing on infrastructure validation")

	// For DevOps stories, we focus on:
	// 1. Container builds (if Dockerfile present)
	// 2. Configuration validation
	// 3. Basic infrastructure checks
	// Skip traditional build/test/lint which may not be relevant

	// Check if this is a container-related DevOps story
	dockerfilePath := filepath.Join(worktreePathStr, "Dockerfile")
	if fileExists(dockerfilePath) {
		c.logger.Info("DevOps story: validating Dockerfile build")
		if err := c.validateDockerfileBuild(ctx, worktreePathStr); err != nil {
			c.logger.Error("Dockerfile validation failed: %v", err)
			errorStr := err.Error()
			truncatedError := truncateOutput(errorStr)
			sm.SetStateData(KeyTestError, errorStr)
			sm.SetStateData(KeyTestFailureOutput, truncatedError)
			sm.SetStateData(KeyCodingMode, "test_fix")
			return StateCoding, false, nil
		}
	}

	// Check for Makefile and run basic validation if present
	makefilePath := filepath.Join(worktreePathStr, "Makefile")
	if fileExists(makefilePath) {
		c.logger.Info("DevOps story: validating Makefile targets")
		if err := c.validateMakefileTargets(worktreePathStr); err != nil {
			c.logger.Error("Makefile validation failed: %v", err)
			errorStr := err.Error()
			truncatedError := truncateOutput(errorStr)
			sm.SetStateData(KeyTestError, errorStr)
			sm.SetStateData(KeyTestFailureOutput, truncatedError)
			sm.SetStateData(KeyCodingMode, "test_fix")
			return StateCoding, false, nil
		}
	}

	// Store successful test results
	sm.SetStateData(KeyTestsPassed, true)
	sm.SetStateData(KeyTestOutput, "DevOps story infrastructure validation completed successfully")
	sm.SetStateData(KeyTestingCompletedAt, time.Now().UTC())

	c.logger.Info("DevOps story testing completed successfully")
	return c.proceedToCodeReview(ctx, sm)
}

// handleLegacyTesting handles the legacy testing approach for backward compatibility.
func (c *Coder) handleLegacyTesting(ctx context.Context, sm *agent.BaseStateMachine, worktreePathStr string) (proto.State, bool, error) {
	// Use global config singleton.
	globalConfig, err := config.GetConfig()
	if err != nil {
		c.logger.Error("Global config not available")
		return proto.StateError, false, fmt.Errorf("global config not available: %w", err)
	}

	// Store platform information for context.
	platform := globalConfig.Project.PrimaryPlatform
	sm.SetStateData(KeyBuildBackend, platform)

	// Get build command from config
	testCommand := globalConfig.Build.Test
	if testCommand == "" {
		testCommand = "make test" // fallback
	}
	_ = testCommand // Used in runMakeTest below

	// Run tests using the detected backend.
	testsPassed, testOutput, err := c.runMakeTest(ctx, worktreePathStr)

	// Store test results.
	sm.SetStateData(KeyTestsPassed, testsPassed)
	sm.SetStateData(KeyTestOutput, testOutput)
	sm.SetStateData(KeyTestingCompletedAt, time.Now().UTC())

	if err != nil {
		c.logger.Error("Failed to run tests: %v", err)
		// Truncate error output to prevent context bloat.
		errorStr := err.Error()
		truncatedError := truncateOutput(errorStr)
		sm.SetStateData(KeyTestError, errorStr)               // Keep full error for logging
		sm.SetStateData(KeyTestFailureOutput, truncatedError) // Use truncated for context
		sm.SetStateData(KeyCodingMode, "test_fix")
		return StateCoding, false, nil
	}

	if !testsPassed {
		c.logger.Info("Tests failed, transitioning to CODING state for fixes")
		// Truncate test output to prevent context bloat.
		truncatedOutput := truncateOutput(testOutput)
		sm.SetStateData(KeyTestFailureOutput, truncatedOutput)
		sm.SetStateData(KeyCodingMode, "test_fix")
		return StateCoding, false, nil
	}

	c.logger.Info("Tests passed successfully")
	return c.proceedToCodeReview(ctx, sm)
}

// validateDockerfileBuild validates that a Dockerfile can be built successfully.
func (c *Coder) validateDockerfileBuild(_ context.Context, worktreePathStr string) error {
	// Simple Docker build validation - could be enhanced with actual build
	dockerfilePath := filepath.Join(worktreePathStr, "Dockerfile")
	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return fmt.Errorf("failed to read Dockerfile: %w", err)
	}

	// Basic Dockerfile validation
	dockerfileContent := string(content)
	if !strings.Contains(dockerfileContent, "FROM") {
		return fmt.Errorf("dockerfile missing required FROM instruction")
	}

	// Could add more sophisticated validation here
	c.logger.Info("Dockerfile validation passed")
	return nil
}

// validateMakefileTargets validates that Makefile has reasonable targets for DevOps.
func (c *Coder) validateMakefileTargets(worktreePathStr string) error {
	makefilePath := filepath.Join(worktreePathStr, "Makefile")
	content, err := os.ReadFile(makefilePath)
	if err != nil {
		return fmt.Errorf("failed to read Makefile: %w", err)
	}

	makefileContent := string(content)
	// For DevOps stories, we're more lenient - just check that it's not empty
	if strings.TrimSpace(makefileContent) == "" {
		return fmt.Errorf("makefile is empty")
	}

	c.logger.Info("Makefile validation passed")
	return nil
}

// fileExists checks if a file exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

const maxOutputLength = 2000

// truncateOutput truncates long output to prevent context bloat.
func truncateOutput(output string) string {
	if len(output) <= maxOutputLength {
		return output
	}

	truncated := output[:maxOutputLength]
	return truncated + "\n\n[... output truncated after " + fmt.Sprintf("%d", maxOutputLength) + " characters for context management ...]"
}

// runMakeTest executes tests using the appropriate build backend - implements AR-103.
func (c *Coder) runMakeTest(ctx context.Context, worktreePath string) (bool, string, error) {
	c.logger.Info("Running tests in %s", worktreePath)

	// Create a context with timeout for the test execution.
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Use global config singleton for test command.
	globalConfig, err := config.GetConfig()
	if err != nil {
		return false, "", fmt.Errorf("global config not available: %w", err)
	}

	platform := globalConfig.Project.PrimaryPlatform
	testCommand := globalConfig.Build.Test
	if testCommand == "" {
		testCommand = "make test" // fallback
	}

	c.logger.Info("Using %s platform for testing with command: %s", platform, testCommand)

	// Execute test command using shell.
	opts := execpkg.Opts{
		WorkDir: worktreePath,
		Timeout: 5 * time.Minute,
	}

	result, err := c.longRunningExecutor.Run(testCtx, []string{"sh", "-c", testCommand}, &opts)
	if err != nil {
		return false, "", fmt.Errorf("failed to execute test command: %w", err)
	}
	outputStr := result.Stdout + result.Stderr

	// Log the test output for debugging.
	c.logger.Info("Test output: %s", outputStr)

	// Check if it's a timeout.
	if testCtx.Err() == context.DeadlineExceeded {
		return false, outputStr, logx.Errorf("tests timed out after 5 minutes")
	}

	// Check test result based on exit code.
	if result.ExitCode != 0 {
		// Tests failed - this is expected when tests fail.
		c.logger.Info("Tests failed with exit code: %d", result.ExitCode)
		return false, outputStr, nil
	}

	// Tests succeeded.
	c.logger.Info("Tests completed successfully")
	return true, outputStr, nil
}

// runTestWithBuildService runs tests using the build service instead of direct backend calls.
func (c *Coder) runTestWithBuildService(ctx context.Context, worktreePath string) (bool, string, error) {
	c.logger.Info("Running tests via build service in %s", worktreePath)

	// Create a context with timeout for the test execution.
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Create test request.
	req := &build.Request{
		ProjectRoot: worktreePath,
		Operation:   "test",
		Timeout:     300, // 5 minutes
		Context:     make(map[string]string),
	}

	// Execute test via build service.
	response, err := c.buildService.ExecuteBuild(testCtx, req)
	if err != nil {
		return false, "", logx.Wrap(err, "build service test execution failed")
	}

	// Log the test output for debugging.
	c.logger.Info("Test output: %s", response.Output)

	if !response.Success {
		// Check if it's a timeout.
		if testCtx.Err() == context.DeadlineExceeded {
			return false, response.Output, logx.Errorf("tests timed out after 5 minutes")
		}

		// Tests failed - this is expected when tests fail.
		c.logger.Info("Tests failed: %s", response.Error)
		return false, response.Output, nil
	}

	// Tests succeeded.
	c.logger.Info("Tests completed successfully via build service")
	return true, response.Output, nil
}

// proceedToCodeReview transitions to CODE_REVIEW state after successful testing.
func (c *Coder) proceedToCodeReview(_ context.Context, _ *agent.BaseStateMachine) (proto.State, bool, error) {
	// Tests passed, transition to CODE_REVIEW state.
	// The approval request will be sent when entering the CODE_REVIEW state.
	c.logger.Info("🧑‍💻 Tests completed successfully, transitioning to CODE_REVIEW")
	return StateCodeReview, false, nil
}
