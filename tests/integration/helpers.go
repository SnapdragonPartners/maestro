//go:build integration

package integration

import (
	"context"
	"flag"
	"os"
	osexec "os/exec"
	"testing"
	"time"

	"orchestrator/pkg/coder"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

// mockTestAgent implements tools.Agent interface for testing.
type mockTestAgent struct {
	hostWorkspacePath string
}

func newMockTestAgent(hostWorkspacePath string) *mockTestAgent {
	return &mockTestAgent{hostWorkspacePath: hostWorkspacePath}
}

func (m *mockTestAgent) GetCurrentState() proto.State {
	return proto.State("PLANNING") // Default to read-only state
}

func (m *mockTestAgent) GetHostWorkspacePath() string {
	if m.hostWorkspacePath != "" {
		return m.hostWorkspacePath
	}
	return "/tmp/test-workspace" // Fallback for backwards compatibility
}

var _ tools.Agent = (*mockTestAgent)(nil)

// isDockerAvailable checks if Docker is available by running docker version.
func isDockerAvailable() bool {
	cmd := osexec.Command("docker", "version")
	return cmd.Run() == nil
}

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

// MessageMatchers contains common message matching functions.
type MessageMatchers struct{}

// MatchRequestType returns a matcher that checks for a specific request type.
func (MessageMatchers) MatchRequestType(requestType string) func(*proto.AgentMsg) bool {
	return func(msg *proto.AgentMsg) bool {
		if msg.Type != proto.MsgTypeREQUEST {
			return false
		}

		if msg.Payload == nil {
			return false
		}

		// Match based on payload kind
		switch requestType {
		case "approval":
			return msg.Payload.Kind == proto.PayloadKindApprovalRequest
		case "question":
			return msg.Payload.Kind == proto.PayloadKindQuestionRequest
		default:
			return false
		}
	}
}

// MatchResultWithStatus returns a matcher that checks for a RESULT message with specific status.
func (MessageMatchers) MatchResultWithStatus(status string) func(*proto.AgentMsg) bool {
	return func(msg *proto.AgentMsg) bool {
		if msg.Type != proto.MsgTypeRESPONSE {
			return false
		}

		if msg.Payload == nil {
			return false
		}

		// Extract approval response to check decision/status
		if msg.Payload.Kind == proto.PayloadKindApprovalResponse {
			approvalResult, err := msg.Payload.ExtractApprovalResponse()
			if err != nil {
				return false
			}
			return string(approvalResult.Status) == status
		}

		// For other response types, match against payload kind or generic status
		return false
	}
}

// MatchApprovalRequest returns a matcher for approval requests.
func (MessageMatchers) MatchApprovalRequest() func(*proto.AgentMsg) bool {
	return MessageMatchers{}.MatchRequestType("approval")
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
