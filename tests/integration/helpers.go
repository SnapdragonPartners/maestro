package integration

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/build"
	"orchestrator/pkg/coder"
	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

// Helper function to get API key for tests.
func getTestAPIKey(t *testing.T) string {
	// Try ANTHROPIC_API_KEY first (standard), then CLAUDE_API_KEY (legacy)
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		return apiKey
	}
	if apiKey := os.Getenv("CLAUDE_API_KEY"); apiKey != "" {
		return apiKey
	}
	t.Skip("Skipping test: ANTHROPIC_API_KEY or CLAUDE_API_KEY environment variable not set")
	return "" // Never reached due to t.Skip()
}

// Test flags for configurable timeouts.
// globalFlags holds all test configuration flags in a single managed structure.
type globalFlags struct {
	planTimeout   *time.Duration
	globalTimeout *time.Duration
	pumpInterval  *time.Duration
	coderStep     *time.Duration
}

// testFlags is the single global instance for test configuration.
var testFlags = globalFlags{ //nolint:gochecknoglobals
	planTimeout:   flag.Duration("timeout-plan", 100*time.Millisecond, "Timeout for plan approval"),
	globalTimeout: flag.Duration("timeout-global", 2*time.Second, "Global test timeout"),
	pumpInterval:  flag.Duration("pump-interval", 10*time.Millisecond, "Message pump interval"),
	coderStep:     flag.Duration("timeout-coder-step", 50*time.Millisecond, "Individual coder step timeout"),
}

// GetTestTimeouts returns timeouts configured via command-line flags.
func GetTestTimeouts() TestTimeouts {
	return TestTimeouts{
		Plan:      *testFlags.planTimeout,
		Global:    *testFlags.globalTimeout,
		Pump:      *testFlags.pumpInterval,
		CoderStep: *testFlags.coderStep,
	}
}

// RequireState asserts that a coder is in the expected state.
func RequireState(t *testing.T, harness *TestHarness, coderID string, want proto.State) {
	t.Helper()

	actual := harness.GetCoderState(coderID)
	if actual != want {
		t.Fatalf("Expected coder %s to be in state %s, but got %s", coderID, want, actual)
	}
}

// ExpectMessage waits for a message on the channel that satisfies the matcher function.
func ExpectMessage(t *testing.T, ch <-chan *proto.AgentMsg, timeout time.Duration, matcher func(*proto.AgentMsg) bool) *proto.AgentMsg {
	t.Helper()

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	for {
		select {
		case msg := <-ch:
			if matcher(msg) {
				return msg
			}
			// Message didn't match, keep waiting.
		case <-timeoutTimer.C:
			t.Fatalf("Timeout waiting for expected message after %v", timeout)
			return nil
		}
	}
}

// CreateTestCoder creates a coder driver for testing.
func CreateTestCoder(t *testing.T, coderID string) *coder.Coder {
	t.Helper()

	// Create temporary directory for this coder.
	tempDir := t.TempDir()

	// Create state store.
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store for coder %s: %v", coderID, err)
	}

	// Create minimal model config.
	modelCfg := &config.ModelCfg{
		MaxContextTokens: 8192,
		MaxReplyTokens:   4096,
	}

	// Create real Claude LLM client for testing.
	apiKey := getTestAPIKey(t)
	llmClient := agent.NewClaudeClient(apiKey)

	// Create BuildService for MCP tools.
	buildService := build.NewBuildService()

	// Create coder driver.
	driver, err := coder.NewCoder(coderID, stateStore, modelCfg, llmClient, tempDir, nil, buildService, nil)
	if err != nil {
		t.Fatalf("Failed to create coder driver %s: %v", coderID, err)
	}

	// Initialize the driver.
	if err := driver.Initialize(context.Background()); err != nil {
		t.Fatalf("Failed to initialize coder driver %s: %v", coderID, err)
	}

	return driver
}

// CreateTestCoderWithAgent creates a coder driver with specific agent configuration for testing.
func CreateTestCoderWithAgent(t *testing.T, coderID string, agentConfig *config.Agent) *coder.Coder {
	t.Helper()

	// Create temporary directory for this coder.
	tempDir := t.TempDir()

	// Create state store.
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store for coder %s: %v", coderID, err)
	}

	// Create minimal model config.
	modelCfg := &config.ModelCfg{
		MaxContextTokens: 8192,
		MaxReplyTokens:   4096,
	}

	// Create real Claude LLM client for testing.
	apiKey := getTestAPIKey(t)
	llmClient := agent.NewClaudeClient(apiKey)

	// Create BuildService for MCP tools.
	buildService := build.NewBuildService()

	// Create coder driver with agent configuration.
	driver, err := coder.NewCoder(coderID, stateStore, modelCfg, llmClient, tempDir, agentConfig, buildService, nil)
	if err != nil {
		t.Fatalf("Failed to create coder driver %s: %v", coderID, err)
	}

	// Initialize the driver.
	err = driver.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Failed to initialize coder driver %s: %v", coderID, err)
	}

	return driver
}

// MessageMatchers contains common message matching functions.
type MessageMatchers struct{}

// MatchRequestType returns a matcher that checks for a specific request type.
func (MessageMatchers) MatchRequestType(requestType proto.RequestType) func(*proto.AgentMsg) bool {
	return func(msg *proto.AgentMsg) bool {
		if msg.Type != proto.MsgTypeREQUEST {
			return false
		}

		reqType, exists := msg.GetPayload(proto.KeyRequestType)
		if !exists {
			return false
		}

		reqTypeStr, ok := reqType.(string)
		if !ok {
			return false
		}

		parsedType, err := proto.ParseRequestType(reqTypeStr)
		if err != nil {
			return false
		}

		return parsedType == requestType
	}
}

// MatchResultWithStatus returns a matcher that checks for a RESULT message with specific status.
func (MessageMatchers) MatchResultWithStatus(status string) func(*proto.AgentMsg) bool {
	return func(msg *proto.AgentMsg) bool {
		if msg.Type != proto.MsgTypeRESULT {
			return false
		}

		msgStatus, exists := msg.GetPayload(proto.KeyStatus)
		if !exists {
			return false
		}

		msgStatusStr, ok := msgStatus.(string)
		if !ok {
			return false
		}

		return msgStatusStr == status
	}
}

// MatchApprovalRequest returns a matcher for approval requests.
func (MessageMatchers) MatchApprovalRequest() func(*proto.AgentMsg) bool {
	return MessageMatchers{}.MatchRequestType(proto.RequestApproval)
}

// Match provides a common message matchers instance.
var Match = MessageMatchers{} //nolint:gochecknoglobals

// SetupTestEnvironment sets up common test environment settings.
func SetupTestEnvironment(t *testing.T) {
	t.Helper()

	// Ensure we're in test mode.
	_ = os.Setenv("GO_TEST", "1")

	// Disable debug logging by default for cleaner test output.
	// Individual tests can re-enable if needed.
	_ = os.Unsetenv("DEBUG")

	// Set a temporary log directory for this test.
	logDir := t.TempDir()
	_ = os.Setenv("DEBUG_LOG_DIR", logDir)
}

// AssertNoChannelMessages verifies that a channel has no pending messages.
func AssertNoChannelMessages(t *testing.T, ch <-chan *proto.AgentMsg, timeout time.Duration) {
	t.Helper()

	select {
	case msg := <-ch:
		t.Fatalf("Expected no messages on channel, but got: %+v", msg)
	case <-time.After(timeout):
		// Good - no messages received.
	}
}

// DrainChannel removes all pending messages from a channel.
func DrainChannel(ch <-chan *proto.AgentMsg) int {
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			return count
		}
	}
}

// WaitForCoderState is a helper that waits for a coder to reach a specific state.
func WaitForCoderState(t *testing.T, harness *TestHarness, coderID string, targetState proto.State, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := harness.Wait(ctx, coderID, targetState); err != nil {
		t.Fatalf("Failed waiting for coder %s to reach %s: %v", coderID, targetState, err)
	}
}

// StartCoderWithTask prepares a coder with a specific task content for TestHarness control.
func StartCoderWithTask(t *testing.T, harness *TestHarness, coderID, taskContent string) {
	t.Helper()

	coderAgent := harness.coders[coderID]
	if coderAgent == nil {
		t.Fatalf("Coder %s not found in harness", coderID)
	}

	// Set up the task data without running the full state machine.
	// The TestHarness will control the stepping.
	coderAgent.Driver.SetStateData("task_content", taskContent)
	coderAgent.Driver.SetStateData("started_at", time.Now().UTC())

	// Initialize the coder if needed.
	if err := coderAgent.Driver.Initialize(context.Background()); err != nil {
		t.Fatalf("Failed to initialize coder %s: %v", coderID, err)
	}

	// Transition to SETUP state first, then the state machine will naturally transition to PLANNING.
	if err := coderAgent.Driver.TransitionTo(context.Background(), coder.StateSetup, nil); err != nil {
		t.Fatalf("Failed to transition coder %s to SETUP: %v", coderID, err)
	}
}
