//go:build integration
// +build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/coder/claude/mcpserver"
	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

// TestMCPTCPIntegration tests the MCP TCP communication between host and container.
// This verifies that:
// 1. MCP server starts on host and listens on TCP port
// 2. Container can connect to host via host.docker.internal
// 3. Tool calls from container execute on host and return results
//
// NOTE: This test requires:
// - Docker available
// - maestro-bootstrap image rebuilt with maestro-mcp-proxy binary
func TestMCPTCPIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping MCP TCP integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "mcp_test")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Create tool provider with shell tool
	logger := logx.NewLogger("mcp-test")
	agentCtx := &tools.AgentContext{
		Executor: exec.NewLocalExec(),
		WorkDir:  workspaceDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"shell"})

	// Start MCP server on host (binds to dynamic port)
	server := mcpserver.NewServer(provider, logger)
	go func() {
		if err := server.Start(ctx); err != nil && err != context.Canceled {
			t.Logf("MCP server error: %v", err)
		}
	}()
	defer server.Stop()

	// Wait for server to start and bind to port
	time.Sleep(200 * time.Millisecond)

	port := server.Port()
	if port == 0 {
		t.Fatal("Server failed to bind to port")
	}
	token := server.Token()
	t.Logf("MCP server started on port %d", port)

	// Start container using LongRunningDockerExec
	containerName := "mcp-tcp-test-container"
	executor := exec.NewLongRunningDockerExec(config.BootstrapContainerTag, "mcp-tcp-test")

	opts := &exec.Opts{
		WorkDir: workspaceDir,
		User:    "0:0",
	}

	startedContainer, err := executor.StartContainer(ctx, containerName, opts)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		executor.StopContainer(cleanupCtx, startedContainer)
	}()
	t.Logf("Container started: %s", startedContainer)

	// Wait for container to be ready
	time.Sleep(1 * time.Second)

	// Verify maestro-mcp-proxy exists in container
	proxyCheck := []string{"which", "maestro-mcp-proxy"}
	result, err := executor.Run(ctx, proxyCheck, &exec.Opts{})
	if err != nil || result.ExitCode != 0 {
		t.Skipf("maestro-mcp-proxy not found in bootstrap container. "+
			"Run maestro to auto-build, or see pkg/dockerfiles/bootstrap.dockerfile for manual build instructions. "+
			"err=%v, exit=%d, stderr=%s", err, result.ExitCode, result.Stderr)
	}
	t.Logf("Proxy binary found: %s", strings.TrimSpace(result.Stdout))

	// TCP address from container perspective
	tcpAddr := fmt.Sprintf("host.docker.internal:%d", port)

	// Test 1: Initialize handshake via proxy over TCP
	t.Run("initialize_handshake", func(t *testing.T) {
		initRequest := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n"

		// Run proxy with stdin input (MCP_AUTH_TOKEN passed via environment)
		cmd := []string{"sh", "-c", "echo '" + strings.TrimSpace(initRequest) + "' | maestro-mcp-proxy " + tcpAddr}
		result, err := executor.Run(ctx, cmd, &exec.Opts{
			Timeout: 5 * time.Second,
			Env:     []string{"MCP_AUTH_TOKEN=" + token},
		})
		if err != nil {
			t.Fatalf("Proxy execution failed: %v", err)
		}

		// Parse response
		scanner := bufio.NewScanner(strings.NewReader(result.Stdout))
		if !scanner.Scan() {
			t.Fatalf("No response from proxy. stdout=%s, stderr=%s", result.Stdout, result.Stderr)
		}

		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatalf("Failed to parse response: %v, raw=%s", err, scanner.Text())
		}

		// Verify response
		if response["error"] != nil {
			t.Fatalf("Got error response: %v", response["error"])
		}

		resultMap, ok := response["result"].(map[string]any)
		if !ok {
			t.Fatalf("Invalid result type: %T", response["result"])
		}

		if resultMap["protocolVersion"] != "2024-11-05" {
			t.Errorf("Unexpected protocol version: %v", resultMap["protocolVersion"])
		}

		t.Logf("Initialize handshake successful")
	})

	// Test 2: List tools via proxy
	t.Run("tools_list", func(t *testing.T) {
		listRequest := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

		cmd := []string{"sh", "-c", "echo '" + strings.TrimSpace(listRequest) + "' | maestro-mcp-proxy " + tcpAddr}
		result, err := executor.Run(ctx, cmd, &exec.Opts{
			Timeout: 5 * time.Second,
			Env:     []string{"MCP_AUTH_TOKEN=" + token},
		})
		if err != nil {
			t.Fatalf("Proxy execution failed: %v", err)
		}

		scanner := bufio.NewScanner(strings.NewReader(result.Stdout))
		if !scanner.Scan() {
			t.Fatalf("No response from proxy. stdout=%s, stderr=%s", result.Stdout, result.Stderr)
		}

		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if response["error"] != nil {
			t.Fatalf("Got error response: %v", response["error"])
		}

		resultMap, ok := response["result"].(map[string]any)
		if !ok {
			t.Fatalf("Invalid result type: %T", response["result"])
		}

		toolsList, ok := resultMap["tools"].([]any)
		if !ok || len(toolsList) == 0 {
			t.Fatalf("Expected tools list, got: %v", resultMap["tools"])
		}

		// Verify shell tool is present
		found := false
		for _, tool := range toolsList {
			toolMap, ok := tool.(map[string]any)
			if ok && toolMap["name"] == "shell" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Shell tool not found in tools list")
		}

		t.Logf("Tools list returned %d tools", len(toolsList))
	})

	// Test 3: Execute shell tool via proxy
	t.Run("tools_call_shell", func(t *testing.T) {
		// JSON-RPC request to call shell tool
		callRequest := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"shell","arguments":{"cmd":"echo MCP_TCP_SUCCESS"}}}` + "\n"

		cmd := []string{"sh", "-c", "echo '" + strings.TrimSpace(callRequest) + "' | maestro-mcp-proxy " + tcpAddr}
		result, err := executor.Run(ctx, cmd, &exec.Opts{
			Timeout: 10 * time.Second,
			Env:     []string{"MCP_AUTH_TOKEN=" + token},
		})
		if err != nil {
			t.Fatalf("Proxy execution failed: %v", err)
		}

		scanner := bufio.NewScanner(strings.NewReader(result.Stdout))
		if !scanner.Scan() {
			t.Fatalf("No response from proxy. stdout=%s, stderr=%s", result.Stdout, result.Stderr)
		}

		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatalf("Failed to parse response: %v, raw=%s", err, scanner.Text())
		}

		if response["error"] != nil {
			t.Fatalf("Got error response: %v", response["error"])
		}

		resultMap, ok := response["result"].(map[string]any)
		if !ok {
			t.Fatalf("Invalid result type: %T", response["result"])
		}

		// Get content array
		content, ok := resultMap["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("Expected content array, got: %v", resultMap["content"])
		}

		// Get text from first content item
		contentItem, ok := content[0].(map[string]any)
		if !ok {
			t.Fatalf("Invalid content item type: %T", content[0])
		}

		text, ok := contentItem["text"].(string)
		if !ok {
			t.Fatalf("Expected text field, got: %v", contentItem)
		}

		// Parse the shell tool result (it's JSON inside the text)
		var shellResult map[string]any
		if err := json.Unmarshal([]byte(text), &shellResult); err != nil {
			t.Fatalf("Failed to parse shell result: %v, text=%s", err, text)
		}

		stdout, ok := shellResult["stdout"].(string)
		if !ok || !strings.Contains(stdout, "MCP_TCP_SUCCESS") {
			t.Fatalf("Expected stdout to contain MCP_TCP_SUCCESS, got: %v", shellResult)
		}

		t.Logf("Shell tool executed successfully via MCP TCP")
	})
}

// TestMCPTCPConcurrentAgents tests that multiple agents can use separate TCP ports.
func TestMCPTCPConcurrentAgents(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping concurrent agents test")
	}

	// Pre-check: verify maestro-mcp-proxy exists in bootstrap container
	// This avoids starting multiple containers just to have them all fail
	preCheckCtx, preCheckCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer preCheckCancel()
	preCheckExecutor := exec.NewLongRunningDockerExec(config.BootstrapContainerTag, "proxy-precheck")
	preCheckContainer, err := preCheckExecutor.StartContainer(preCheckCtx, "proxy-precheck", &exec.Opts{User: "0:0"})
	if err != nil {
		t.Fatalf("Failed to start pre-check container: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	proxyCheck, proxyErr := preCheckExecutor.Run(preCheckCtx, []string{"which", "maestro-mcp-proxy"}, &exec.Opts{})
	preCheckExecutor.StopContainer(preCheckCtx, preCheckContainer)
	if proxyErr != nil || proxyCheck.ExitCode != 0 {
		t.Skipf("maestro-mcp-proxy not found in bootstrap container. " +
			"Run maestro to auto-build, or: go build -o maestro-mcp-proxy ./cmd/maestro-mcp-proxy && " +
			"docker build -t maestro-bootstrap -f pkg/dockerfiles/bootstrap.dockerfile .")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	tempDir := t.TempDir()

	numAgents := 3
	type agentSetup struct {
		containerName string
		port          int
		token         string
		server        *mcpserver.Server
		executor      *exec.LongRunningDockerExec
	}

	agents := make([]*agentSetup, numAgents)
	logger := logx.NewLogger("mcp-concurrent-test")

	// Start agents
	for i := 0; i < numAgents; i++ {
		workspaceDir := filepath.Join(tempDir, "agent", string(rune('0'+i)))
		if err := os.MkdirAll(workspaceDir, 0755); err != nil {
			t.Fatalf("Failed to create workspace: %v", err)
		}

		containerName := "mcp-concurrent-" + string(rune('0'+i))

		agentCtx := &tools.AgentContext{
			Executor: exec.NewLocalExec(),
			WorkDir:  workspaceDir,
		}
		provider := tools.NewProvider(agentCtx, []string{"shell"})

		server := mcpserver.NewServer(provider, logger)
		go server.Start(ctx)

		// Wait for server to bind
		time.Sleep(100 * time.Millisecond)

		executor := exec.NewLongRunningDockerExec(config.BootstrapContainerTag, "concurrent-"+string(rune('0'+i)))
		opts := &exec.Opts{WorkDir: workspaceDir, User: "0:0"}
		fullContainerName, err := executor.StartContainer(ctx, containerName, opts)
		if err != nil {
			t.Fatalf("Failed to start container %d: %v", i, err)
		}

		agents[i] = &agentSetup{
			containerName: fullContainerName, // Use full Docker name for cleanup
			port:          server.Port(),
			token:         server.Token(),
			server:        server,
			executor:      executor,
		}
	}

	// Cleanup — use a fresh context since test ctx may be expired
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		for _, agent := range agents {
			if agent != nil {
				agent.server.Stop()
				agent.executor.StopContainer(cleanupCtx, agent.containerName)
			}
		}
	}()

	time.Sleep(1 * time.Second)

	// Test each agent can make independent calls
	for i, agent := range agents {
		t.Run("agent_"+string(rune('0'+i)), func(t *testing.T) {
			tcpAddr := fmt.Sprintf("host.docker.internal:%d", agent.port)
			marker := "AGENT_" + string(rune('0'+i)) + "_SUCCESS"
			callRequest := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"shell","arguments":{"cmd":"echo ` + marker + `"}}}` + "\n"

			cmd := []string{"sh", "-c", "echo '" + strings.TrimSpace(callRequest) + "' | maestro-mcp-proxy " + tcpAddr}
			result, err := agent.executor.Run(ctx, cmd, &exec.Opts{
				Timeout: 10 * time.Second,
				Env:     []string{"MCP_AUTH_TOKEN=" + agent.token},
			})
			if err != nil {
				t.Fatalf("Agent %d proxy failed: %v", i, err)
			}

			if !strings.Contains(result.Stdout, marker) {
				t.Fatalf("Agent %d: expected marker %s in output, got: %s", i, marker, result.Stdout)
			}

			t.Logf("Agent %d completed successfully", i)
		})
	}
}

// TestMCPTCPDirectConnection tests direct TCP connection without Docker (for faster local testing).
func TestMCPTCPDirectConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tempDir := t.TempDir()
	logger := logx.NewLogger("mcp-test")

	agentCtx := &tools.AgentContext{
		Executor: exec.NewLocalExec(),
		WorkDir:  tempDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"shell"})

	// Start server
	server := mcpserver.NewServer(provider, logger)
	go func() {
		if err := server.Start(ctx); err != nil && err != context.Canceled {
			t.Logf("Server error: %v", err)
		}
	}()
	defer server.Stop()

	time.Sleep(100 * time.Millisecond)

	port := server.Port()
	if port == 0 {
		t.Fatal("Server failed to bind to port")
	}
	token := server.Token()

	// Connect directly via TCP (simulating what proxy does)
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	// Authenticate first (same as proxy does)
	authMsg := `{"auth":"` + token + `"}` + "\n"
	if _, err := conn.Write([]byte(authMsg)); err != nil {
		t.Fatalf("Failed to send auth message: %v", err)
	}

	// Read auth response
	reader := bufio.NewReader(conn)
	authLine, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("Failed to read auth response: %v", err)
	}

	var authResponse map[string]any
	if err := json.Unmarshal(authLine, &authResponse); err != nil {
		t.Fatalf("Failed to parse auth response: %v", err)
	}

	if authResponse["authenticated"] != true {
		t.Fatalf("Authentication failed: %v", authResponse)
	}

	// Send initialize request
	initRequest := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n"
	if _, err := conn.Write([]byte(initRequest)); err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	// Read response
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response["error"] != nil {
		t.Fatalf("Got error: %v", response["error"])
	}

	resultMap, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("Invalid result type: %T", response["result"])
	}

	if resultMap["protocolVersion"] != "2024-11-05" {
		t.Errorf("Unexpected protocol version: %v", resultMap["protocolVersion"])
	}

	t.Logf("Direct TCP connection test passed on port %d", port)
}
