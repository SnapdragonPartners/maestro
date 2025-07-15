package coder

import (
	"context"
	"fmt"
	"strings"
)

// MockGitRunner implements GitRunner for testing purposes
type MockGitRunner struct {
	// Commands maps command signatures to their outputs
	Commands map[string][]byte
	// Errors maps command signatures to their errors  
	Errors map[string]error
	// CallLog tracks all commands that were called
	CallLog []GitCall
}

// GitCall represents a logged Git command call
type GitCall struct {
	Dir  string
	Args []string
}

// NewMockGitRunner creates a new mock Git runner
func NewMockGitRunner() *MockGitRunner {
	return &MockGitRunner{
		Commands: make(map[string][]byte),
		Errors:   make(map[string]error),
		CallLog:  make([]GitCall, 0),
	}
}

// Run implements GitRunner interface for testing
func (m *MockGitRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	// Log the call
	m.CallLog = append(m.CallLog, GitCall{
		Dir:  dir,
		Args: append([]string{}, args...), // Copy slice
	})

	// Build command signature for lookup
	sig := m.buildSignature(dir, args...)
	
	// Check for specific error
	if err, exists := m.Errors[sig]; exists {
		return nil, err
	}
	
	// Check for specific output
	if output, exists := m.Commands[sig]; exists {
		return output, nil
	}
	
	// Default successful output
	return []byte("mock output"), nil
}

// SetCommand sets the expected output for a specific command
func (m *MockGitRunner) SetCommand(dir string, output []byte, args ...string) {
	sig := m.buildSignature(dir, args...)
	m.Commands[sig] = output
}

// SetError sets an error for a specific command
func (m *MockGitRunner) SetError(dir string, err error, args ...string) {
	sig := m.buildSignature(dir, args...)
	m.Errors[sig] = err
}

// GetCallCount returns the number of times a command was called
func (m *MockGitRunner) GetCallCount(dir string, args ...string) int {
	sig := m.buildSignature(dir, args...)
	count := 0
	for _, call := range m.CallLog {
		callSig := m.buildSignature(call.Dir, call.Args...)
		if callSig == sig {
			count++
		}
	}
	return count
}

// WasCalled checks if a specific command was called
func (m *MockGitRunner) WasCalled(dir string, args ...string) bool {
	return m.GetCallCount(dir, args...) > 0
}

// Reset clears all recorded calls and expectations
func (m *MockGitRunner) Reset() {
	m.Commands = make(map[string][]byte)
	m.Errors = make(map[string]error)
	m.CallLog = make([]GitCall, 0)
}

// buildSignature creates a unique signature for a command
func (m *MockGitRunner) buildSignature(dir string, args ...string) string {
	return fmt.Sprintf("%s|%s", dir, strings.Join(args, " "))
}