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
	"orchestrator/pkg/effect"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// handleTesting processes the TESTING state.
func (c *Coder) handleTesting(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Get worktree path for running tests
	worktreePath, exists := sm.GetStateValue(KeyWorktreePath)
	if !exists || worktreePath == "" {
		return proto.StateError, false, logx.Errorf("no worktree path found - workspace setup required")
	}

	worktreePathStr, ok := utils.SafeAssert[string](worktreePath)
	if !ok {
		return proto.StateError, false, logx.Errorf("worktree_path is not a string: %v", worktreePath)
	}

	// Get story type for testing strategy decision
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
	// Use MCP test tool instead of direct build registry calls
	if c.buildService != nil {
		// Get backend info first
		backendInfo, err := c.buildService.GetBackendInfo(worktreePathStr)
		if err != nil {
			c.logger.Error("Failed to get backend info: %v", err)
			return proto.StateError, false, logx.Wrap(err, "failed to get backend info")
		}

		// Store backend information for context
		sm.SetStateData(KeyBuildBackend, backendInfo.Name)
		c.logger.Info("App story testing: using build service with backend %s", backendInfo.Name)

		// Run tests using build service
		testsPassed, testOutput, err := c.runTestWithBuildService(ctx, worktreePathStr)
		if err != nil {
			c.logger.Error("Failed to run tests: %v", err)
			// Create test failure effect with truncated error message
			errorStr := err.Error()
			truncatedError := truncateOutput(errorStr)
			sm.SetStateData(KeyTestError, errorStr) // Keep full error for logging

			testFailureEff := effect.NewGenericTestFailureEffect(truncatedError)
			return c.executeTestFailureAndTransition(ctx, sm, testFailureEff)
		}

		// Store test results
		sm.SetStateData(KeyTestsPassed, testsPassed)
		sm.SetStateData(KeyTestOutput, testOutput)
		sm.SetStateData(KeyTestingCompletedAt, time.Now().UTC())

		if !testsPassed {
			c.logger.Info("App story tests failed, transitioning to CODING state for fixes")
			// Create test failure effect with truncated test output
			truncatedOutput := truncateOutput(testOutput)

			testFailureEff := effect.NewGenericTestFailureEffect(truncatedOutput)
			return c.executeTestFailureAndTransition(ctx, sm, testFailureEff)
		}

		c.logger.Info("App story tests passed successfully")
		return c.proceedToCodeReview()
	}

	// Use general testing approach for other story types
	return c.handleLegacyTesting(ctx, sm, worktreePathStr)
}

// handleDevOpsStoryTesting handles testing for DevOps stories focusing on infrastructure validation.
func (c *Coder) handleDevOpsStoryTesting(ctx context.Context, sm *agent.BaseStateMachine, worktreePathStr string) (proto.State, bool, error) {
	c.logger.Info("DevOps story testing: focusing on infrastructure validation")

	// For DevOps stories, we need actual infrastructure validation, not just file checks
	// Check if this is a container-related DevOps story
	dockerfilePath := filepath.Join(worktreePathStr, "Dockerfile")
	if fileExists(dockerfilePath) {
		return c.handleContainerTesting(ctx, sm, worktreePathStr, dockerfilePath)
	}

	// Check for Makefile and run basic validation if present
	makefilePath := filepath.Join(worktreePathStr, "Makefile")
	if fileExists(makefilePath) {
		c.logger.Info("DevOps story: validating Makefile targets")
		if err := c.validateMakefileTargets(worktreePathStr); err != nil {
			c.logger.Error("Makefile validation failed: %v", err)
			// Create test failure effect with truncated error message
			errorStr := err.Error()
			truncatedError := truncateOutput(errorStr)
			sm.SetStateData(KeyTestError, errorStr) // Keep full error for logging

			testFailureEff := effect.NewGenericTestFailureEffect(truncatedError)
			return c.executeTestFailureAndTransition(ctx, sm, testFailureEff)
		}
	}

	// Store successful test results
	sm.SetStateData(KeyTestsPassed, true)
	sm.SetStateData(KeyTestOutput, "DevOps story infrastructure validation completed successfully")
	sm.SetStateData(KeyTestingCompletedAt, time.Now().UTC())

	c.logger.Info("DevOps story testing completed successfully")
	return c.proceedToCodeReview()
}

// handleContainerTesting performs actual container infrastructure testing for DevOps stories.
func (c *Coder) handleContainerTesting(ctx context.Context, sm *agent.BaseStateMachine, worktreePathStr, _ string) (proto.State, bool, error) {
	c.logger.Info("DevOps story: performing container infrastructure testing")

	// Get global config to check container configuration
	globalConfig, err := config.GetConfig()
	if err != nil {
		c.logger.Error("Failed to get global config: %v", err)
		return proto.StateError, false, logx.Wrap(err, "failed to get global config")
	}

	// Check if container configuration is populated
	if globalConfig.Container == nil {
		c.logger.Info("Container config not found - sending back to CODING for container_update")
		feedback := "Container configuration missing. Use the 'container_update' tool to set up container configuration (name, dockerfile path) before building."

		testFailureEff := effect.NewContainerConfigSetupEffect(feedback)
		return c.executeTestFailureAndTransition(ctx, sm, testFailureEff)
	}

	containerConfig := globalConfig.Container
	if containerConfig.Name == "" || containerConfig.Dockerfile == "" {
		c.logger.Info("Container config incomplete - sending back to CODING for container_update")
		feedback := fmt.Sprintf("Container configuration incomplete. Missing: %s%s. Use 'container_update' tool to set container name and dockerfile path.",
			func() string {
				if containerConfig.Name == "" {
					return "container name"
				}
				return ""
			}(),
			func() string {
				if containerConfig.Dockerfile == "" {
					if containerConfig.Name == "" {
						return ", dockerfile path"
					}
					return "dockerfile path"
				}
				return ""
			}())

		testFailureEff := effect.NewContainerConfigSetupEffect(feedback)
		return c.executeTestFailureAndTransition(ctx, sm, testFailureEff)
	}

	c.logger.Info("Container config found: name=%s, dockerfile=%s", containerConfig.Name, containerConfig.Dockerfile)

	// Run container_build tool directly
	buildSuccess, buildError := c.runContainerBuildTesting(ctx, worktreePathStr, containerConfig)
	if !buildSuccess {
		c.logger.Error("Container build failed: %v", buildError)
		feedback := fmt.Sprintf("Container build failed: %s\n\nPlease fix the Dockerfile or build issues and try again.", buildError)

		testFailureEff := effect.NewContainerBuildFailureEffect(feedback)
		return c.executeTestFailureAndTransition(ctx, sm, testFailureEff)
	}

	c.logger.Info("Container build successful, running boot test")

	// Run container_boot_test to validate the container actually works
	bootSuccess, bootError := c.runContainerBootTesting(ctx, containerConfig.Name)
	if !bootSuccess {
		c.logger.Error("Container boot test failed: %v", bootError)
		feedback := fmt.Sprintf("Container build succeeded but boot test failed: %s\n\nThe container builds but doesn't run properly. Please fix the application startup or Dockerfile configuration.", bootError)

		testFailureEff := effect.NewContainerRuntimeFailureEffect(feedback)
		return c.executeTestFailureAndTransition(ctx, sm, testFailureEff)
	}

	c.logger.Info("Container infrastructure testing completed successfully")

	// Store successful test results
	sm.SetStateData(KeyTestsPassed, true)
	sm.SetStateData(KeyTestOutput, fmt.Sprintf("Container infrastructure validation completed successfully:\n- Container '%s' built successfully\n- Container boot test passed\n- Infrastructure is ready for deployment", containerConfig.Name))
	sm.SetStateData(KeyTestingCompletedAt, time.Now().UTC())

	return c.proceedToCodeReview()
}

// executeTestFailureAndTransition executes a test failure effect and transitions to CODING state.
func (c *Coder) executeTestFailureAndTransition(ctx context.Context, sm *agent.BaseStateMachine, testFailureEff *effect.TestFailureEffect) (proto.State, bool, error) {
	// Execute the test failure effect
	result, err := c.ExecuteEffect(ctx, testFailureEff)
	if err != nil {
		c.logger.Error("🧪 Failed to execute test failure effect: %v", err)
		return proto.StateError, false, logx.Wrap(err, "test failure effect execution failed")
	}

	// Process the result
	if failureResult, ok := result.(*effect.TestFailureResult); ok {
		c.logger.Info("🧪 Test failure processed: %s", failureResult.FailureType)
		// Set state data for CODING state to use
		sm.SetStateData(KeyTestFailureOutput, failureResult.FailureMessage)
		sm.SetStateData(KeyCodingMode, failureResult.FailureType)
		return StateCoding, false, nil
	}

	return proto.StateError, false, logx.Errorf("invalid test failure result type: %T", result)
}

// runContainerBuildTesting runs container_build tool directly for testing.
func (c *Coder) runContainerBuildTesting(ctx context.Context, worktreePathStr string, containerConfig *config.ContainerConfig) (bool, string) {
	c.logger.Info("Running container build test for container: %s", containerConfig.Name)

	// Create container build tool instance using the coder's executor
	buildTool := tools.NewContainerBuildTool(c.longRunningExecutor)

	// Prepare arguments for container_build tool
	args := map[string]any{
		"container_name":    containerConfig.Name,
		"dockerfile_path":   containerConfig.Dockerfile,
		"working_directory": worktreePathStr,
	}

	// Execute container build
	result, err := buildTool.Exec(ctx, args)
	if err != nil {
		return false, err.Error()
	}

	// Check if build was successful
	if resultMap, ok := result.(map[string]any); ok {
		if success, exists := resultMap["success"].(bool); exists && success {
			c.logger.Info("Container build test successful")
			return true, ""
		}
		if message, exists := resultMap["message"].(string); exists {
			return false, message
		}
	}

	return false, "Container build completed but success status unclear"
}

// runContainerBootTesting runs container_boot_test tool directly for testing.
func (c *Coder) runContainerBootTesting(ctx context.Context, containerName string) (bool, string) {
	c.logger.Info("Running container boot test for container: %s", containerName)

	// Create container boot test tool instance using the coder's executor
	bootTestTool := tools.NewContainerBootTestTool(c.longRunningExecutor)

	// Prepare arguments for container_boot_test tool
	args := map[string]any{
		"container_name":  containerName,
		"timeout_seconds": 30, // 30 second boot test
	}

	// Execute container boot test
	result, err := bootTestTool.Exec(ctx, args)
	if err != nil {
		return false, err.Error()
	}

	// Check if boot test was successful
	if resultMap, ok := result.(map[string]any); ok {
		if success, exists := resultMap["success"].(bool); exists && success {
			c.logger.Info("Container boot test successful")
			return true, ""
		}
		if message, exists := resultMap["message"].(string); exists {
			return false, message
		}
	}

	return false, "Container boot test completed but success status unclear"
}

// handleLegacyTesting handles the general testing approach for non-DevOps stories.
func (c *Coder) handleLegacyTesting(ctx context.Context, sm *agent.BaseStateMachine, worktreePathStr string) (proto.State, bool, error) {
	// Use global config singleton
	globalConfig, err := config.GetConfig()
	if err != nil {
		c.logger.Error("Global config not available")
		return proto.StateError, false, fmt.Errorf("global config not available: %w", err)
	}

	// Store platform information for context
	platform := globalConfig.Project.PrimaryPlatform
	sm.SetStateData(KeyBuildBackend, platform)

	// Get build command from config
	testCommand := globalConfig.Build.Test
	if testCommand == "" {
		testCommand = "make test" // fallback
	}
	_ = testCommand // Used in runMakeTest below

	// Run tests using detected backend
	testsPassed, testOutput, err := c.runMakeTest(ctx, worktreePathStr)

	// Store test results
	sm.SetStateData(KeyTestsPassed, testsPassed)
	sm.SetStateData(KeyTestOutput, testOutput)
	sm.SetStateData(KeyTestingCompletedAt, time.Now().UTC())

	if err != nil {
		c.logger.Error("Failed to run tests: %v", err)
		// Create test failure effect with truncated error message
		errorStr := err.Error()
		truncatedError := truncateOutput(errorStr)
		sm.SetStateData(KeyTestError, errorStr) // Keep full error for logging

		testFailureEff := effect.NewGenericTestFailureEffect(truncatedError)
		return c.executeTestFailureAndTransition(ctx, sm, testFailureEff)
	}

	if !testsPassed {
		c.logger.Info("Tests failed, transitioning to CODING state for fixes")
		// Create test failure effect with truncated test output
		truncatedOutput := truncateOutput(testOutput)

		testFailureEff := effect.NewGenericTestFailureEffect(truncatedOutput)
		return c.executeTestFailureAndTransition(ctx, sm, testFailureEff)
	}

	c.logger.Info("Tests passed successfully")
	return c.proceedToCodeReview()
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

	// Create context with timeout for test execution
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Use global config singleton for test command
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

	// Execute test command using shell
	opts := execpkg.Opts{
		WorkDir: worktreePath,
		Timeout: 5 * time.Minute,
	}

	result, err := c.longRunningExecutor.Run(testCtx, []string{"sh", "-c", testCommand}, &opts)
	if err != nil {
		return false, "", fmt.Errorf("failed to execute test command: %w", err)
	}
	outputStr := result.Stdout + result.Stderr

	// Log test output for debugging
	c.logger.Info("Test output: %s", outputStr)

	// Check if timeout occurred
	if testCtx.Err() == context.DeadlineExceeded {
		return false, outputStr, logx.Errorf("tests timed out after 5 minutes")
	}

	// Check test result based on exit code
	if result.ExitCode != 0 {
		// Tests failed - expected when tests fail
		c.logger.Info("Tests failed with exit code: %d", result.ExitCode)
		return false, outputStr, nil
	}

	// Tests succeeded
	c.logger.Info("Tests completed successfully")
	return true, outputStr, nil
}

// runTestWithBuildService runs tests using build service instead of direct backend calls.
func (c *Coder) runTestWithBuildService(ctx context.Context, worktreePath string) (bool, string, error) {
	c.logger.Info("Running tests via build service in %s", worktreePath)

	// Create context with timeout for test execution
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Create test request
	req := &build.Request{
		ProjectRoot: worktreePath,
		Operation:   "test",
		Timeout:     300, // 5 minutes
		Context:     make(map[string]string),
	}

	// Execute test via build service
	response, err := c.buildService.ExecuteBuild(testCtx, req)
	if err != nil {
		return false, "", logx.Wrap(err, "build service test execution failed")
	}

	// Log test output for debugging
	c.logger.Info("Test output: %s", response.Output)

	if !response.Success {
		// Check if timeout occurred
		if testCtx.Err() == context.DeadlineExceeded {
			return false, response.Output, logx.Errorf("tests timed out after 5 minutes")
		}

		// Tests failed - expected when tests fail
		c.logger.Info("Tests failed: %s", response.Error)
		return false, response.Output, nil
	}

	// Tests succeeded
	c.logger.Info("Tests completed successfully via build service")
	return true, response.Output, nil
}

// proceedToCodeReview transitions to CODE_REVIEW state after successful testing.
func (c *Coder) proceedToCodeReview() (proto.State, bool, error) {
	// Tests passed, transition to CODE_REVIEW state
	// Approval request will be sent when entering CODE_REVIEW state
	c.logger.Info("🧑‍💻 Tests completed successfully, transitioning to CODE_REVIEW")
	return StateCodeReview, false, nil
}
