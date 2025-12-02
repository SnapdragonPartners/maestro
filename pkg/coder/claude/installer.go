// Package claude provides Claude Code integration for the coder agent.
package claude

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
)

// Installer handles automatic installation of Claude Code and its dependencies.
type Installer struct {
	executor      exec.Executor
	containerName string
	logger        *logx.Logger
}

// NewInstaller creates a new Installer for the given container.
func NewInstaller(executor exec.Executor, containerName string, logger *logx.Logger) *Installer {
	if logger == nil {
		logger = logx.NewLogger("claude-installer")
	}
	return &Installer{
		executor:      executor,
		containerName: containerName,
		logger:        logger,
	}
}

// EnsureClaudeCode ensures Claude Code is installed, installing dependencies as needed (Node.js → npm → Claude Code).
func (i *Installer) EnsureClaudeCode(ctx context.Context) error {
	// Check if Claude Code is already installed
	if installed, _ := i.isClaudeCodeInstalled(ctx); installed {
		i.logger.Info("Claude Code already installed")
		return nil
	}

	// Check and install Node.js if needed
	if installed, _ := i.isNodeInstalled(ctx); !installed {
		i.logger.Info("Node.js not found, installing...")
		if err := i.installNode(ctx); err != nil {
			return logx.Errorf("failed to install Node.js: %w", err)
		}
	}

	// Check and install npm if needed (usually comes with Node.js, but verify)
	if installed, _ := i.isNpmInstalled(ctx); !installed {
		i.logger.Info("npm not found, installing...")
		if err := i.installNpm(ctx); err != nil {
			return logx.Errorf("failed to install npm: %w", err)
		}
	}

	// Install Claude Code
	i.logger.Info("Installing Claude Code...")
	if err := i.installClaudeCode(ctx); err != nil {
		return logx.Errorf("failed to install Claude Code: %w", err)
	}

	// Verify installation
	if installed, version := i.isClaudeCodeInstalled(ctx); !installed {
		return logx.Errorf("Claude Code installation verification failed")
	} else {
		i.logger.Info("Claude Code installed successfully: %s", version)
	}

	return nil
}

// isNodeInstalled checks if Node.js is available.
func (i *Installer) isNodeInstalled(ctx context.Context) (bool, string) {
	result, err := i.runCommand(ctx, []string{"node", "--version"}, 30*time.Second)
	if err != nil {
		return false, ""
	}
	version := strings.TrimSpace(result.Stdout)
	return version != "" && strings.HasPrefix(version, "v"), version
}

// isNpmInstalled checks if npm is available.
func (i *Installer) isNpmInstalled(ctx context.Context) (bool, string) {
	result, err := i.runCommand(ctx, []string{"npm", "--version"}, 30*time.Second)
	if err != nil {
		return false, ""
	}
	version := strings.TrimSpace(result.Stdout)
	return version != "", version
}

// isClaudeCodeInstalled checks if Claude Code is available.
func (i *Installer) isClaudeCodeInstalled(ctx context.Context) (bool, string) {
	result, err := i.runCommand(ctx, []string{"claude", "--version"}, 30*time.Second)
	if err != nil {
		return false, ""
	}
	version := strings.TrimSpace(result.Stdout)
	return version != "", version
}

// installNode installs Node.js using the system package manager.
func (i *Installer) installNode(ctx context.Context) error {
	// Try different package managers based on what's available
	// Priority: apt (Debian/Ubuntu), apk (Alpine), yum (RHEL/CentOS)

	// Try apt-get (Debian/Ubuntu)
	if i.hasCommand(ctx, "apt-get") {
		cmds := [][]string{
			{"apt-get", "update"},
			{"apt-get", "install", "-y", "nodejs", "npm"},
		}
		for _, cmd := range cmds {
			if _, err := i.runCommand(ctx, cmd, 5*time.Minute); err != nil {
				return fmt.Errorf("apt-get command failed: %w", err)
			}
		}
		return nil
	}

	// Try apk (Alpine)
	if i.hasCommand(ctx, "apk") {
		if _, err := i.runCommand(ctx, []string{"apk", "add", "--no-cache", "nodejs", "npm"}, 5*time.Minute); err != nil {
			return fmt.Errorf("apk command failed: %w", err)
		}
		return nil
	}

	// Try yum (RHEL/CentOS)
	if i.hasCommand(ctx, "yum") {
		cmds := [][]string{
			{"yum", "install", "-y", "nodejs", "npm"},
		}
		for _, cmd := range cmds {
			if _, err := i.runCommand(ctx, cmd, 5*time.Minute); err != nil {
				return fmt.Errorf("yum command failed: %w", err)
			}
		}
		return nil
	}

	return fmt.Errorf("no supported package manager found (apt-get, apk, or yum required)")
}

// installNpm installs npm (usually needed only if Node was installed without npm).
func (i *Installer) installNpm(ctx context.Context) error {
	// npm is typically included with Node.js, but if not, try to install it
	if i.hasCommand(ctx, "apt-get") {
		if _, err := i.runCommand(ctx, []string{"apt-get", "install", "-y", "npm"}, 3*time.Minute); err != nil {
			return fmt.Errorf("apt-get npm install failed: %w", err)
		}
		return nil
	}

	if i.hasCommand(ctx, "apk") {
		if _, err := i.runCommand(ctx, []string{"apk", "add", "--no-cache", "npm"}, 3*time.Minute); err != nil {
			return fmt.Errorf("apk npm install failed: %w", err)
		}
		return nil
	}

	if i.hasCommand(ctx, "yum") {
		if _, err := i.runCommand(ctx, []string{"yum", "install", "-y", "npm"}, 3*time.Minute); err != nil {
			return fmt.Errorf("yum npm install failed: %w", err)
		}
		return nil
	}

	return fmt.Errorf("no supported package manager found for npm installation")
}

// installClaudeCode installs Claude Code globally via npm.
func (i *Installer) installClaudeCode(ctx context.Context) error {
	// Install Claude Code globally
	result, err := i.runCommand(ctx, []string{"npm", "install", "-g", "@anthropic-ai/claude-code"}, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("npm install failed: %w (stderr: %s)", err, result.Stderr)
	}
	return nil
}

// hasCommand checks if a command exists in the container.
func (i *Installer) hasCommand(ctx context.Context, cmd string) bool {
	result, err := i.runCommand(ctx, []string{"which", cmd}, 10*time.Second)
	return err == nil && strings.TrimSpace(result.Stdout) != ""
}

// runCommand executes a command in the container.
func (i *Installer) runCommand(ctx context.Context, cmd []string, timeout time.Duration) (exec.Result, error) {
	opts := &exec.Opts{
		Timeout: timeout,
	}
	result, err := i.executor.Run(ctx, cmd, opts)
	if err != nil {
		return result, fmt.Errorf("command %v failed: %w", cmd, err)
	}
	return result, nil
}

// GetClaudeCodeVersion returns the installed Claude Code version.
func (i *Installer) GetClaudeCodeVersion(ctx context.Context) (string, error) {
	installed, version := i.isClaudeCodeInstalled(ctx)
	if !installed {
		return "", fmt.Errorf("claude Code is not installed")
	}
	return version, nil
}
