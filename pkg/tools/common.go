// Package tools provides Model Context Protocol (MCP) tool implementations for agent operations.
// It includes file system operations, shell commands, and other tools used by coding agents.
package tools

import (
	"sync"

	"orchestrator/pkg/exec"
)

// once ensures InitCommon is called only once.
var once sync.Once //nolint:gochecknoglobals // Standard sync.Once pattern for initialization

// InitCommon registers common tools that are used across multiple agents.
// This should be called from NewCoder() before agent-specific tool registrations.
// Uses sync.Once to ensure thread-safe single initialization.
func InitCommon() {
	once.Do(func() {
		// Register shell tool with local executor by default.
		// This will be updated later when containers are configured.
		localExecutor := exec.NewLocalExec()
		shellTool := NewShellTool(localExecutor)

		// Register the shell tool - ignore error since we want to continue.
		// even if registration fails (could be already registered in tests)
		_ = Register(shellTool)
	})
}
