// Package mocks provides shared mock implementations for testing.
//
// This package contains mock implementations of external service clients
// (LLM, GitHub, Chat) that can be used by any package's tests.
//
// # Usage
//
//	import "orchestrator/internal/mocks"
//
//	func TestSomething(t *testing.T) {
//	    mockLLM := mocks.NewMockLLMClient()
//	    mockLLM.OnComplete(func(msgs []agent.Message) agent.Response {
//	        return agent.Response{Content: "test response"}
//	    })
//	    // Use mockLLM in test...
//	}
//
// # Available Mocks
//
//   - MockLLMClient: Mock for pkg/agent.LLMClient interface
//   - MockGitHubClient: Mock for GitHub API operations
//   - MockChatService: Mock for pkg/architect.ChatServiceInterface
//   - MockDispatcher: Mock for message dispatching
//
// See docs/TESTING_STRATEGY.md for guidelines on when to use mocks
// vs integration tests.
package mocks
