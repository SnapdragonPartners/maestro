# Maestro Testing Strategy

This document describes the testing approach for the Maestro codebase, including when to use mocks vs real services and where shared test infrastructure lives.

## Testing Philosophy

We use a **hybrid approach** that balances test speed, reliability, and real-world validation:

| Test Type | Services | When to Run | Purpose |
|-----------|----------|-------------|---------|
| Unit tests | Mocks | Every CI run (`make test`) | Logic, state machines, error paths |
| Integration tests | Real APIs | Pre-merge, nightly (`make test-integration`) | End-to-end validation |

### When to Use Mocks

- **State machine transitions** - Test FSM logic without external dependencies
- **Error handling paths** - Simulate failures, timeouts, rate limits
- **Edge cases** - Test boundary conditions precisely
- **Fast feedback** - Run on every commit without API costs

### When to Use Real Services

- **Happy path validation** - Verify real API behavior
- **Integration points** - Catch API drift or contract changes
- **End-to-end flows** - Validate complete workflows
- **Pre-merge confidence** - Final check before merging

## Shared Mock Infrastructure

Shared mocks live in `internal/mocks/` and can be imported by any package's tests.

```
internal/mocks/
├── llm_client.go         # Mock LLMClient for testing agent flows
├── github_client.go      # Mock GitHub client for PR/merge tests
├── chat_service.go       # Mock chat service for escalation tests
├── git_runner.go         # Mock GitRunner for git operation tests
└── container_manager.go  # Mock ContainerManager for Docker tests
```

### Mocks vs Real Services

Not every dependency needs a mock. Consider these factors:

| Factor | Prefer Mock | Prefer Real Service |
|--------|-------------|---------------------|
| Speed | Slow operations (network, disk) | Fast in-memory operations |
| Determinism | Non-deterministic (LLM responses) | Deterministic behavior |
| Availability | Not always available (external APIs) | Always available (local code) |
| Complexity | Simple behavior to emulate | Complex behavior hard to mock accurately |
| Side effects | Creates real resources | No persistent side effects |

**Services we mock:**
- **LLMClient** - Non-deterministic, costly, slow
- **GitHubClient** - External API, rate limits, requires auth
- **ChatService** - Timing-dependent, external service
- **GitRunner** - Filesystem/network operations, slow for clone/fetch
- **ContainerManager** - Requires Docker daemon, slow

**Services we use real:**
- **Dispatcher** - In-memory channels/maps, fast, deterministic

**When real services are advisable:**
For GitRunner and ContainerManager, real services may be preferable when:
- Testing complex multi-step workflows where mock fidelity is a concern
- Integration tests that validate real system behavior
- The test environment reliably has Docker and git available
- Mocking would require implementing significant git/Docker semantics

### Usage Pattern

```go
import (
    "context"
    "testing"
    "orchestrator/internal/mocks"
    "orchestrator/pkg/agent/llm"
)

func TestSomething(t *testing.T) {
    mockLLM := mocks.NewMockLLMClient()
    mockLLM.OnComplete(func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
        // Return controlled response
        return llm.CompletionResponse{
            Content:    "test response",
            StopReason: "end_turn",
        }, nil
    })

    // Use mockLLM in test...
}
```

### Concrete Mock Examples

#### MockLLMClient - Testing Tool Calls

```go
func TestToolCallParsing(t *testing.T) {
    mockLLM := mocks.NewMockLLMClient()

    // Configure mock to return a tool call
    mockLLM.RespondWithToolCall("submit_plan", map[string]any{
        "plan": "Implementation plan details...",
        "confidence": 0.85,
    })

    // Run your test
    // ...

    // Verify the LLM was called correctly
    assert.Equal(t, 1, mockLLM.GetCompleteCallCount())
    assert.True(t, mockLLM.AssertCompleteCalledWith("expected prompt content"))
}
```

#### MockLLMClient - Testing Response Sequences

```go
func TestIterativeConversation(t *testing.T) {
    mockLLM := mocks.NewMockLLMClient()

    // Configure mock to return different responses for each call
    mockLLM.RespondWithSequence([]llm.CompletionResponse{
        {Content: "First response", StopReason: "end_turn"},
        {Content: "Second response", StopReason: "end_turn"},
        {ToolCalls: []llm.ToolCall{{Name: "done"}}, StopReason: "tool_use"},
    })

    // Test iterative conversation flow
    // First call returns "First response"
    // Second call returns "Second response"
    // Third call returns tool call to signal completion
}
```

#### MockChatService - Testing Escalation

```go
func TestEscalationFlow(t *testing.T) {
    mockChat := mocks.NewMockChatService()

    // Configure mock to return a human reply after a delay
    mockChat.ReplyWith("Here's guidance for the escalation...")

    // Run escalation handler
    // ...

    // Verify escalation message was posted
    assert.Equal(t, 1, mockChat.GetPostCallCount())
    lastPost := mockChat.LastPostCall()
    assert.Equal(t, "escalate", lastPost.PostType)
    assert.Contains(t, lastPost.Text, "iteration limit")
}
```

#### MockChatService - Testing Timeout

```go
func TestEscalationTimeout(t *testing.T) {
    mockChat := mocks.NewMockChatService()

    // Configure mock to never reply (simulates human not responding)
    mockChat.NeverReply()

    // Create context with short timeout
    ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
    defer cancel()

    // Run escalation handler - should timeout
    err := handleEscalation(ctx, mockChat, ...)

    assert.ErrorIs(t, err, context.DeadlineExceeded)
}
```

#### MockGitHubClient - Testing Merge Conflicts

```go
func TestMergeWithConflicts(t *testing.T) {
    mockGH := mocks.NewMockGitHubClient()

    // Configure mock to return merge conflict
    mockGH.ReturnMergeConflict("CONFLICT in src/main.go: content differs")

    // Run merge handler
    result, err := handleMergeRequest(ctx, mockGH, prURL, branchName, storyID)

    // Verify conflict handling
    assert.NoError(t, err)
    assert.Equal(t, "NEEDS_CHANGES", result.Status)
    assert.Contains(t, result.Feedback, "conflict")
}
```

#### MockGitHubClient - Testing PR Creation Flow

```go
func TestMergeWithNoPR(t *testing.T) {
    mockGH := mocks.NewMockGitHubClient()

    // Configure mock: no existing PR, then successful merge
    mockGH.OnListPRsForBranch(func(ctx context.Context, branch string) ([]github.PullRequest, error) {
        return []github.PullRequest{}, nil // No existing PR
    })
    mockGH.ReturnMergeSuccess("abc123def")

    // Run merge handler with branch name only
    result, err := handleMergeRequest(ctx, mockGH, "", "feature-branch", storyID)

    // Verify PR was created
    assert.True(t, mockGH.WasCreatePRCalled())
    assert.True(t, mockGH.WasMergeCalled())
}
```

## Service-Specific Guidelines

### LLM Client (`pkg/agent.LLMClient`)

| Scenario | Approach |
|----------|----------|
| Tool call parsing | Unit test with mock - deterministic |
| State transitions | Unit test with mock - test FSM logic |
| Prompt rendering | Unit test - no LLM needed |
| Full conversation flow | Integration test - validate real behavior |
| Token counting | Unit test with mock - verify limits |

**Mock location:** `internal/mocks/llm_client.go`

**Interface:** Already defined in `pkg/agent/llm.go`

### GitHub Client (`pkg/github.Client`)

| Scenario | Approach |
|----------|----------|
| PR merge success | Integration test against test repo |
| PR merge conflicts | Integration test - create real conflict |
| API error handling | Unit test with mock - simulate errors |
| Rate limiting | Unit test with mock - hard to trigger in real API |
| Branch operations | Integration test against test repo |

**Mock location:** `internal/mocks/github_client.go`

**Test repository:** `github.com/anthropics/maestro-test-sandbox` (reset periodically)

**Interface to define:**
```go
// pkg/github/client.go
type GitHubClientInterface interface {
    MergePRWithResult(ctx context.Context, prRef string, opts PRMergeOptions) (*MergeResult, error)
    ListPRsForBranch(ctx context.Context, branch string) ([]*PullRequest, error)
    CreatePR(ctx context.Context, opts PRCreateOptions) (*PullRequest, error)
    CleanupMergedBranches(ctx context.Context, targetBranch string, protected []string) ([]string, error)
}
```

### Chat Service (`pkg/architect.ChatServiceInterface`)

| Scenario | Approach |
|----------|----------|
| Post message | Unit test with mock |
| Poll for replies | Unit test with mock - control timing |
| Timeout handling | Unit test with mock - simulate delays |
| Message formatting | Unit test - no service needed |

**Mock location:** `internal/mocks/chat_service.go`

**Interface:** Already defined in `pkg/architect/driver.go`

### Dispatcher (`pkg/dispatch.Dispatcher`)

| Scenario | Approach |
|----------|----------|
| Message routing | Unit test with **real dispatcher** |
| Channel operations | Unit test with **real dispatcher** |
| Status updates | Unit test with **real dispatcher** |

**Mock:** None - use real dispatcher. It's in-memory (channels/maps), fast, and deterministic.

**Rationale:** The dispatcher is lightweight Go code with no external dependencies. Using the real implementation provides better integration coverage without sacrificing speed.

### GitRunner (`pkg/coder.GitRunner`)

| Scenario | Approach |
|----------|----------|
| Clone operations | Unit test with mock (fast) or integration test with real repo |
| Branch operations | Unit test with mock - simulate branch list output |
| Merge conflicts | Unit test with mock - simulate conflict errors |
| Full clone workflow | Integration test with real test repository |

**Mock location:** `internal/mocks/git_runner.go`

**Interface:** Defined in `pkg/coder/interfaces.go`

**When to use real:** For integration tests where you need to validate actual git behavior, or when mock fidelity is a concern (complex multi-step git workflows).

### ContainerManager (`pkg/coder.ContainerManager`)

| Scenario | Approach |
|----------|----------|
| Container stop | Unit test with mock |
| Shutdown cleanup | Unit test with mock |
| Full container lifecycle | Integration test with real Docker |

**Mock location:** `internal/mocks/container_manager.go`

**Interface:** Defined in `pkg/coder/interfaces.go`

**When to use real:** For integration tests that need to validate actual Docker behavior, or when testing container-specific edge cases that are difficult to mock accurately.

## Integration Test Configuration

Integration tests require API credentials and are skipped if not available:

```bash
# Required environment variables
ANTHROPIC_API_KEY=...    # For LLM integration tests
OPENAI_API_KEY=...       # For O3 integration tests
GITHUB_TOKEN=...         # For GitHub integration tests

# Run integration tests
make test-integration

# Skip integration tests (CI without credentials)
make test
```

### Test Repository Setup

For GitHub integration tests, use a dedicated test repository:

1. Repository: `maestro-test-sandbox` (or configured via `TEST_GITHUB_REPO`)
2. Contains fixture branches for merge conflict testing
3. Reset script: `scripts/reset-test-repo.sh` (run periodically)
4. Protected branches: `main`, `develop`

## File Organization

```
pkg/architect/
├── driver.go
├── driver_test.go           # Unit tests with mocks
├── request.go
├── request_test.go          # Unit tests with mocks
├── merge_integration_test.go # Integration tests (build tag: integration)
└── ...

internal/mocks/
├── llm_client.go
├── github_client.go
├── chat_service.go
├── git_runner.go
└── container_manager.go
```

### Build Tags for Integration Tests

Integration tests use build tags to separate them from unit tests:

```go
//go:build integration

package architect_test

func TestMergeRequest_RealGitHub(t *testing.T) {
    // Requires GITHUB_TOKEN
}
```

Run with: `go test -tags=integration ./...`

## Adding New Mocks

When adding a new external dependency:

1. **Define an interface** in the package that uses it
2. **Create mock** in `internal/mocks/` implementing that interface
3. **Document** the mock's capabilities and usage in this file
4. **Add examples** showing common test patterns

### Mock Implementation Pattern

```go
// internal/mocks/example_client.go
package mocks

type MockExampleClient struct {
    DoSomethingFunc func(ctx context.Context, input string) (string, error)
}

func NewMockExampleClient() *MockExampleClient {
    return &MockExampleClient{
        DoSomethingFunc: func(ctx context.Context, input string) (string, error) {
            return "default response", nil
        },
    }
}

func (m *MockExampleClient) DoSomething(ctx context.Context, input string) (string, error) {
    return m.DoSomethingFunc(ctx, input)
}

// Helper for common test scenarios
func (m *MockExampleClient) OnDoSomething(fn func(context.Context, string) (string, error)) {
    m.DoSomethingFunc = fn
}

func (m *MockExampleClient) FailWith(err error) {
    m.DoSomethingFunc = func(context.Context, string) (string, error) {
        return "", err
    }
}
```

## Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2024-12-13 | Adopt hybrid mock/integration strategy | Balance speed with real-world validation |
| 2024-12-13 | Place shared mocks in `internal/mocks/` | Enable reuse across packages |
| 2024-12-13 | Use build tags for integration tests | Allow CI to run without credentials |
| 2024-12-14 | Use real Dispatcher instead of mock | In-memory, fast, deterministic - no benefit to mocking |
| 2024-12-14 | Add MockGitRunner with real service option | Mocks for unit tests; real git for integration tests |
| 2024-12-14 | Add MockContainerManager with real service option | Mocks for unit tests; real Docker for integration tests |
