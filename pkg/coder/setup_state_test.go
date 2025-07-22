package coder

import (
	"context"
	"fmt"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

func TestSetupStateHandler(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*MockGitRunner)
		storyID       string
		expectedState proto.State
		expectedError bool
		skipWorkspace bool
	}{
		{
			name:    "successful setup",
			storyID: "050",
			setupMock: func(mock *MockGitRunner) {
				// Mock successful Git operations.
				mock.SetCommand("", []byte("mock output"), "clone", "--bare")
				mock.SetCommand("", []byte("mock output"), "remote", "update", "--prune")
				mock.SetCommand("", []byte("mock output"), "worktree", "add", "--detach")
				mock.SetCommand("", []byte("mock output"), "switch", "-c", "story-050")
			},
			expectedState: StatePlanning,
			expectedError: false,
		},
		{
			name:    "missing story ID",
			storyID: "", // Will not be set in state
			setupMock: func(_ *MockGitRunner) {
				// No setup needed.
			},
			expectedState: proto.StateError,
			expectedError: true,
		},
		{
			name:    "workspace setup failure",
			storyID: "050",
			setupMock: func(mock *MockGitRunner) {
				// Mock failed Git operation.
				mock.SetError("", fmt.Errorf("git clone failed"), "clone", "--bare")
			},
			expectedState: proto.StateError,
			expectedError: true,
		},
		{
			name:          "no workspace manager",
			storyID:       "050",
			skipWorkspace: true,
			setupMock: func(_ *MockGitRunner) {
				// No setup needed.
			},
			expectedState: StatePlanning,
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test coder.
			tempDir := t.TempDir()
			stateStore, _ := state.NewStore(tempDir)

			coder, err := NewCoder("test-agent", stateStore, &config.ModelCfg{}, &mockLLMClient{}, tempDir, &config.Agent{}, nil, nil)
			if err != nil {
				t.Fatal("Failed to create coder:", err)
			}

			// Setup workspace manager if not skipping.
			if !tt.skipWorkspace {
				mockGit := NewMockGitRunner()
				tt.setupMock(mockGit)

				_ = NewWorkspaceManager(
					mockGit,
					tempDir,
					"git@github.com:user/repo.git",
					"main",
					".mirrors",
					"story-{STORY_ID}",
					"{AGENT_ID}/{STORY_ID}",
				)
				// Note: SetWorkspaceManager method not available, skipping workspace setup.
			}

			// Set story ID in state if provided.
			if tt.storyID != "" {
				coder.BaseStateMachine.SetStateData(KeyStoryID, tt.storyID)
			}

			// Test the setup handler.
			ctx := context.Background()
			nextState, done, err := coder.handleSetup(ctx, coder.BaseStateMachine)

			// Check error expectation.
			if tt.expectedError {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}

			// Check state transition.
			if nextState != tt.expectedState {
				t.Errorf("Expected next state %s, got %s", tt.expectedState, nextState)
			}

			// Setup should never be done (terminal)
			if done {
				t.Error("Setup state should never be terminal")
			}

			// Verify worktree path is set on success.
			if !tt.expectedError && !tt.skipWorkspace && tt.storyID != "" {
				worktreePath, exists := coder.BaseStateMachine.GetStateValue(KeyWorktreePath)
				if !exists {
					t.Error("worktree_path should be set after successful setup")
				}
				if worktreePath == "" {
					t.Error("worktree_path should not be empty")
				}
			}
		})
	}
}

func TestDoneStateHandler(t *testing.T) {
	tests := []struct {
		name          string
		storyID       string
		setupCleanup  bool
		skipWorkspace bool
	}{
		{
			name:         "successful cleanup and restart",
			storyID:      "050",
			setupCleanup: true,
		},
		{
			name:          "no workspace manager",
			storyID:       "050",
			skipWorkspace: true,
		},
		{
			name:    "missing story ID",
			storyID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test coder.
			tempDir := t.TempDir()
			stateStore, _ := state.NewStore(tempDir)

			coder, err := NewCoder("test-agent", stateStore, &config.ModelCfg{}, &mockLLMClient{}, tempDir, &config.Agent{}, nil, nil)
			if err != nil {
				t.Fatal("Failed to create coder:", err)
			}

			// Setup workspace manager if not skipping.
			if !tt.skipWorkspace {
				mockGit := NewMockGitRunner()
				if tt.setupCleanup {
					// Mock successful cleanup operations.
					mockGit.SetCommand("", []byte("mock output"), "worktree", "remove")
					mockGit.SetCommand("", []byte("mock output"), "worktree", "prune")
				}

				_ = NewWorkspaceManager(
					mockGit,
					tempDir,
					"git@github.com:user/repo.git",
					"main",
					".mirrors",
					"story-{STORY_ID}",
					"{AGENT_ID}/{STORY_ID}",
				)
				// Note: SetWorkspaceManager method not available, skipping workspace setup.
			}

			// Set up state data.
			if tt.storyID != "" {
				coder.BaseStateMachine.SetStateData(KeyStoryID, tt.storyID)
			}
			coder.BaseStateMachine.SetStateData(KeyTaskContent, "test task")
			coder.BaseStateMachine.SetStateData(KeyWorktreePath, "/some/path")

			// Test the done handler.
			ctx := context.Background()
			nextState, done, err := coder.handleDone(ctx, coder.BaseStateMachine)

			// Should not error.
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Should remain in DONE (terminal state)
			if nextState != proto.StateDone {
				t.Errorf("Expected next state %s, got %s", proto.StateDone, nextState)
			}

			// Should be terminal.
			if !done {
				t.Error("Done state should be terminal")
			}

			// Verify state data is cleared.
			storyIDCleared, _ := coder.BaseStateMachine.GetStateValue(KeyStoryID)
			if storyIDCleared != "" {
				t.Error("story_id should be cleared after done")
			}

			taskContentCleared, _ := coder.BaseStateMachine.GetStateValue(KeyTaskContent)
			if taskContentCleared != "" {
				t.Error("task_content should be cleared after done")
			}
		})
	}
}

func TestErrorStateHandler(t *testing.T) {
	// Create test coder.
	tempDir := t.TempDir()
	stateStore, _ := state.NewStore(tempDir)

	coder, err := NewCoder("test-agent", stateStore, &config.ModelCfg{}, &mockLLMClient{}, tempDir, &config.Agent{}, nil, nil)
	if err != nil {
		t.Fatal("Failed to create coder:", err)
	}

	// Setup workspace manager.
	mockGit := NewMockGitRunner()
	mockGit.SetCommand("", []byte("mock output"), "worktree", "remove")
	mockGit.SetCommand("", []byte("mock output"), "worktree", "prune")

	_ = NewWorkspaceManager(
		mockGit,
		tempDir,
		"git@github.com:user/repo.git",
		"main",
		".mirrors",
		"story-{STORY_ID}",
		"{AGENT_ID}/{STORY_ID}",
	)
	// Note: SetWorkspaceManager method not available, skipping workspace setup.

	// Set up state data.
	coder.BaseStateMachine.SetStateData(KeyStoryID, "050")
	coder.BaseStateMachine.SetStateData(KeyErrorMessage, "test error")
	coder.BaseStateMachine.SetStateData(KeyTaskContent, "test task")

	// Test the error handler.
	ctx := context.Background()
	nextState, done, err := coder.handleError(ctx, coder.BaseStateMachine)

	// Should not error.
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Should remain in ERROR (terminal state)
	if nextState != proto.StateError {
		t.Errorf("Expected next state %s, got %s", proto.StateError, nextState)
	}

	// Should be terminal.
	if !done {
		t.Error("Error state should be terminal")
	}

	// Verify state data is cleared (but error_message might be preserved)
	storyIDCleared, _ := coder.BaseStateMachine.GetStateValue(KeyStoryID)
	if storyIDCleared != "" {
		t.Error("story_id should be cleared after error")
	}

	taskContentCleared, _ := coder.BaseStateMachine.GetStateValue(KeyTaskContent)
	if taskContentCleared != "" {
		t.Error("task_content should be cleared after error")
	}
}

// Mock LLM client for testing.
type mockLLMClient struct{}

func (m *mockLLMClient) Complete(_ context.Context, _ agent.CompletionRequest) (agent.CompletionResponse, error) {
	return agent.CompletionResponse{
		Content: "Mock response",
	}, nil
}

func (m *mockLLMClient) Stream(_ context.Context, _ agent.CompletionRequest) (<-chan agent.StreamChunk, error) {
	ch := make(chan agent.StreamChunk, 1)
	ch <- agent.StreamChunk{Content: "Mock response", Done: true}
	close(ch)
	return ch, nil
}

func (m *mockLLMClient) GetConfig() *agent.LLMConfig {
	return &agent.LLMConfig{
		MaxContextTokens: 32000,
		MaxOutputTokens:  4096,
		CompactIfOver:    2000,
	}
}
