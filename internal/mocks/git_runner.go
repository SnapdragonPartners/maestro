package mocks

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// GitRunCall records the parameters of a Git command call.
type GitRunCall struct {
	Dir  string
	Args []string
}

// MockGitRunner implements coder.GitRunner for testing.
// It provides configurable behavior for Run and RunQuiet operations.
//
// Note: For integration tests requiring realistic git behavior, consider using
// a real GitRunner with a test repository. Mocks are best suited for unit tests
// where speed and determinism are priorities.
type MockGitRunner struct {
	// RunFunc is called when Run is invoked. Override to customize behavior.
	RunFunc func(ctx context.Context, dir string, args ...string) ([]byte, error)

	// RunQuietFunc is called when RunQuiet is invoked. Override to customize behavior.
	RunQuietFunc func(ctx context.Context, dir string, args ...string) ([]byte, error)

	// RunCalls tracks all calls to Run for verification.
	RunCalls []GitRunCall

	// RunQuietCalls tracks all calls to RunQuiet for verification.
	RunQuietCalls []GitRunCall

	// mu protects call tracking slices
	mu sync.Mutex
}

// NewMockGitRunner creates a new mock git runner with default behavior.
// Default behavior: Run and RunQuiet return empty output with no error.
func NewMockGitRunner() *MockGitRunner {
	m := &MockGitRunner{}

	// Default Run behavior: return empty output
	m.RunFunc = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte{}, nil
	}

	// Default RunQuiet behavior: return empty output
	m.RunQuietFunc = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte{}, nil
	}

	return m
}

// Run implements coder.GitRunner.
func (m *MockGitRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	m.mu.Lock()
	m.RunCalls = append(m.RunCalls, GitRunCall{Dir: dir, Args: args})
	m.mu.Unlock()
	return m.RunFunc(ctx, dir, args...)
}

// RunQuiet implements coder.GitRunner.
func (m *MockGitRunner) RunQuiet(ctx context.Context, dir string, args ...string) ([]byte, error) {
	m.mu.Lock()
	m.RunQuietCalls = append(m.RunQuietCalls, GitRunCall{Dir: dir, Args: args})
	m.mu.Unlock()
	return m.RunQuietFunc(ctx, dir, args...)
}

// --- Configuration methods ---

// OnRun sets a custom handler for Run calls.
func (m *MockGitRunner) OnRun(fn func(ctx context.Context, dir string, args ...string) ([]byte, error)) {
	m.RunFunc = fn
}

// OnRunQuiet sets a custom handler for RunQuiet calls.
func (m *MockGitRunner) OnRunQuiet(fn func(ctx context.Context, dir string, args ...string) ([]byte, error)) {
	m.RunQuietFunc = fn
}

// --- Error simulation helpers ---

// FailRunWith configures Run to return the specified error.
func (m *MockGitRunner) FailRunWith(err error) {
	m.RunFunc = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, err
	}
}

// FailRunQuietWith configures RunQuiet to return the specified error.
func (m *MockGitRunner) FailRunQuietWith(err error) {
	m.RunQuietFunc = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, err
	}
}

// FailCommandWith configures Run to fail when a specific command is executed.
// Other commands succeed with empty output.
func (m *MockGitRunner) FailCommandWith(command string, err error) {
	m.RunFunc = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == command {
			return nil, err
		}
		return []byte{}, nil
	}
}

// --- Response helpers ---

// RespondWith configures Run to return the specified output.
func (m *MockGitRunner) RespondWith(output string) {
	m.RunFunc = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(output), nil
	}
}

// RespondToCommand configures Run to return specific output for a specific command.
// Other commands return empty output.
func (m *MockGitRunner) RespondToCommand(command, output string) {
	m.RunFunc = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == command {
			return []byte(output), nil
		}
		return []byte{}, nil
	}
}

// RespondWithMap configures Run to return different outputs for different commands.
// Commands not in the map return empty output.
func (m *MockGitRunner) RespondWithMap(responses map[string]string) {
	m.RunFunc = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 {
			if output, ok := responses[args[0]]; ok {
				return []byte(output), nil
			}
		}
		return []byte{}, nil
	}
}

// --- Scenario helpers ---

// SimulateCloneSuccess configures mock for a successful clone operation.
func (m *MockGitRunner) SimulateCloneSuccess() {
	m.RunFunc = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 0 {
			return []byte{}, nil
		}
		switch args[0] {
		case "clone":
			return []byte("Cloning into 'repo'...\n"), nil
		case "fetch":
			return []byte{}, nil
		case "checkout":
			return []byte("Switched to branch 'main'\n"), nil
		case "branch":
			return []byte("* main\n"), nil
		default:
			return []byte{}, nil
		}
	}
}

// SimulateBranchList configures mock to return a list of branches.
func (m *MockGitRunner) SimulateBranchList(branches []string) {
	m.RunFunc = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "branch" {
			output := ""
			for i, branch := range branches {
				if i == 0 {
					output += "* " + branch + "\n"
				} else {
					output += "  " + branch + "\n"
				}
			}
			return []byte(output), nil
		}
		return []byte{}, nil
	}
}

// SimulateMergeConflict configures mock to return a merge conflict error.
func (m *MockGitRunner) SimulateMergeConflict(conflictingFiles []string) {
	m.RunFunc = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "merge" {
			output := "Auto-merging " + strings.Join(conflictingFiles, "\nAuto-merging ") + "\n"
			output += "CONFLICT (content): Merge conflict in " + conflictingFiles[0] + "\n"
			output += "Automatic merge failed; fix conflicts and then commit the result.\n"
			return []byte(output), fmt.Errorf("exit status 1")
		}
		return []byte{}, nil
	}
}

// --- Verification helpers ---

// Reset clears all recorded calls.
func (m *MockGitRunner) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RunCalls = nil
	m.RunQuietCalls = nil
}

// GetRunCallCount returns the number of times Run was called.
func (m *MockGitRunner) GetRunCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.RunCalls)
}

// GetRunQuietCallCount returns the number of times RunQuiet was called.
func (m *MockGitRunner) GetRunQuietCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.RunQuietCalls)
}

// LastRunCall returns the most recent Run call, or nil if none.
func (m *MockGitRunner) LastRunCall() *GitRunCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.RunCalls) == 0 {
		return nil
	}
	return &m.RunCalls[len(m.RunCalls)-1]
}

// WasCommandCalled returns true if Run was called with the specified command.
func (m *MockGitRunner) WasCommandCalled(command string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, call := range m.RunCalls {
		if len(call.Args) > 0 && call.Args[0] == command {
			return true
		}
	}
	return false
}

// GetCallsForCommand returns all Run calls for a specific command.
func (m *MockGitRunner) GetCallsForCommand(command string) []GitRunCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	var calls []GitRunCall
	for _, call := range m.RunCalls {
		if len(call.Args) > 0 && call.Args[0] == command {
			calls = append(calls, call)
		}
	}
	return calls
}
