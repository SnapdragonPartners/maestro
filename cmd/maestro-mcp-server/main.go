// maestro-mcp-server is a standalone MCP server for testing and debugging.
// In production, the MCP server is started by the coder runner directly.
//
// Usage: maestro-mcp-server -port 8765 -tools shell,container_test
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"orchestrator/pkg/coder/claude/mcpserver"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

func main() {
	toolList := flag.String("tools", "shell", "Comma-separated list of tools to expose")
	flag.Parse()

	logger := logx.NewLogger("mcp-server")

	// Parse tool list
	toolNames := strings.Split(*toolList, ",")
	for i := range toolNames {
		toolNames[i] = strings.TrimSpace(toolNames[i])
	}

	// Create tool provider with basic context
	agentCtx := &tools.AgentContext{
		Executor: exec.NewLocalExec(),
		WorkDir:  "/workspace",
	}
	provider := tools.NewProvider(agentCtx, toolNames)

	// Create server (will bind to dynamic port)
	server := mcpserver.NewServer(provider, logger)

	// Handle shutdown signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Info("Shutting down...")
		if err := server.Stop(); err != nil {
			logger.Error("Error stopping server: %v", err)
		}
		cancel()
	}()

	// Start server in goroutine to get the port first
	go func() {
		if err := server.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	}()

	// Wait a moment for server to start, then print the port
	// This is a testing tool, so we can accept a brief delay
	select {
	case <-ctx.Done():
		return
	default:
	}

	// Wait for server to be ready
	for server.Port() == 0 {
		select {
		case <-ctx.Done():
			return
		default:
		}
	}

	logger.Info("MCP server listening on port %d with tools: %v", server.Port(), toolNames)
	fmt.Printf("PORT=%d\n", server.Port())

	// Wait for shutdown
	<-ctx.Done()
}
