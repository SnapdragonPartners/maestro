package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"orchestrator/pkg/exec"
	"orchestrator/pkg/tools"
)

// authenticateConn sends the auth token and waits for success response.
// Returns the buffered reader for subsequent reads.
func authenticateConn(t *testing.T, conn net.Conn, token string) *bufio.Reader {
	t.Helper()

	// Send auth message
	authMsg := map[string]string{"auth": token}
	data, err := json.Marshal(authMsg)
	if err != nil {
		t.Fatalf("Failed to marshal auth message: %v", err)
	}
	data = append(data, '\n')
	if _, writeErr := conn.Write(data); writeErr != nil {
		t.Fatalf("Failed to send auth message: %v", writeErr)
	}

	// Read auth response
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("Failed to read auth response: %v", err)
	}

	var response struct {
		Authenticated bool   `json:"authenticated"`
		Error         string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("Failed to parse auth response: %v", err)
	}

	if !response.Authenticated {
		t.Fatalf("Authentication failed: %s", response.Error)
	}

	return reader
}

// TestServerStartStop tests that the server starts and stops correctly.
func TestServerStartStop(t *testing.T) {
	// Create temp directory for tool workdir
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create minimal tool provider
	agentCtx := &tools.AgentContext{
		Executor: exec.NewLocalExec(),
		WorkDir:  tmpDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"shell"})

	// Create server
	server := NewServer(provider, nil)

	// Start server in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	// Wait for server to start
	time.Sleep(200 * time.Millisecond)

	// Verify port is assigned
	port := server.Port()
	if port == 0 {
		t.Errorf("Server did not bind to a port")
	}
	t.Logf("Server listening on port %d", port)

	// Stop server
	if err := server.Stop(); err != nil {
		t.Errorf("Failed to stop server: %v", err)
	}

	// Wait for server goroutine to finish
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Server returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("Server did not stop in time")
	}
}

// TestServerInitialize tests the initialize handshake.
func TestServerInitialize(t *testing.T) {
	// Create temp directory for tool workdir
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create minimal tool provider
	agentCtx := &tools.AgentContext{
		Executor: exec.NewLocalExec(),
		WorkDir:  tmpDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"shell"})

	// Create and start server
	server := NewServer(provider, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx) //nolint:errcheck
	time.Sleep(200 * time.Millisecond)
	defer server.Stop() //nolint:errcheck

	// Connect to server via TCP
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", server.Port()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Authenticate first
	reader := authenticateConn(t, conn, server.Token())

	// Send initialize request
	initReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}
	data, _ := json.Marshal(initReq)
	data = append(data, '\n')
	if _, writeErr := conn.Write(data); writeErr != nil {
		t.Fatalf("Failed to send init request: %v", writeErr)
	}

	// Read response
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	var response JSONRPCResponse
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Verify response
	if response.Error != nil {
		t.Errorf("Got error response: %v", response.Error)
	}

	result, ok := response.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("Result is not a map: %T", response.Result)
	}

	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("Expected protocol version 2024-11-05, got %v", result["protocolVersion"])
	}
}

// TestServerToolsList tests the tools/list method.
func TestServerToolsList(t *testing.T) {
	// Create temp directory for tool workdir
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create tool provider with shell tool
	agentCtx := &tools.AgentContext{
		Executor: exec.NewLocalExec(),
		WorkDir:  tmpDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"shell"})

	// Create and start server
	server := NewServer(provider, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx) //nolint:errcheck
	time.Sleep(200 * time.Millisecond)
	defer server.Stop() //nolint:errcheck

	// Connect to server via TCP
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", server.Port()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Authenticate first
	reader := authenticateConn(t, conn, server.Token())

	// Send tools/list request
	listReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}
	data, _ := json.Marshal(listReq)
	data = append(data, '\n')
	if _, writeErr := conn.Write(data); writeErr != nil {
		t.Fatalf("Failed to send list request: %v", writeErr)
	}

	// Read response
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	var response JSONRPCResponse
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Verify response
	if response.Error != nil {
		t.Errorf("Got error response: %v", response.Error)
	}

	result, ok := response.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("Result is not a map: %T", response.Result)
	}

	toolsList, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatalf("tools is not a list: %T", result["tools"])
	}

	if len(toolsList) == 0 {
		t.Error("Expected at least one tool in the list")
	}

	// Verify shell tool is present
	found := false
	for _, tool := range toolsList {
		toolMap, ok := tool.(map[string]interface{})
		if !ok {
			continue
		}
		if toolMap["name"] == "shell" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Shell tool not found in tools list")
	}
}

// TestServerToolsCall tests the tools/call method with shell tool.
func TestServerToolsCall(t *testing.T) {
	// Create temp directory for tool workdir
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create tool provider with shell tool
	agentCtx := &tools.AgentContext{
		Executor: exec.NewLocalExec(),
		WorkDir:  tmpDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"shell"})

	// Create and start server
	server := NewServer(provider, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx) //nolint:errcheck
	time.Sleep(200 * time.Millisecond)
	defer server.Stop() //nolint:errcheck

	// Connect to server via TCP
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", server.Port()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Authenticate first
	reader := authenticateConn(t, conn, server.Token())

	// Send tools/call request for shell
	params := map[string]interface{}{
		"name": "shell",
		"arguments": map[string]interface{}{
			"cmd": "echo hello",
		},
	}
	paramsData, _ := json.Marshal(params)

	callReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  paramsData,
	}
	data, _ := json.Marshal(callReq)
	data = append(data, '\n')
	if _, writeErr := conn.Write(data); writeErr != nil {
		t.Fatalf("Failed to send call request: %v", writeErr)
	}

	// Read response
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	var response JSONRPCResponse
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Verify response
	if response.Error != nil {
		t.Errorf("Got error response: %v", response.Error)
	}

	result, ok := response.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("Result is not a map: %T", response.Result)
	}

	content, ok := result["content"].([]interface{})
	if !ok {
		t.Fatalf("content is not a list: %T", result["content"])
	}

	if len(content) == 0 {
		t.Error("Expected content in response")
	}

	// Verify the output contains the expected text
	contentItem, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatalf("content item is not a map: %T", content[0])
	}
	text, ok := contentItem["text"].(string)
	if !ok {
		t.Fatalf("text field is not a string: %T", contentItem["text"])
	}
	if text == "" {
		t.Error("Expected non-empty text in response")
	}

	t.Logf("Tool response: %s", text)
}

// TestServerConcurrentConnections tests that the server handles multiple connections.
func TestServerConcurrentConnections(t *testing.T) {
	// Create temp directory for tool workdir
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create tool provider
	agentCtx := &tools.AgentContext{
		Executor: exec.NewLocalExec(),
		WorkDir:  tmpDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"shell"})

	// Create and start server
	server := NewServer(provider, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx) //nolint:errcheck
	time.Sleep(200 * time.Millisecond)
	defer server.Stop() //nolint:errcheck

	addr := fmt.Sprintf("127.0.0.1:%d", server.Port())
	token := server.Token()

	// Create multiple connections
	numConns := 5
	errCh := make(chan error, numConns)

	for i := 0; i < numConns; i++ {
		go func(id int) {
			conn, dialErr := net.Dial("tcp", addr)
			if dialErr != nil {
				errCh <- dialErr
				return
			}
			defer conn.Close()

			// Authenticate first
			authMsg := map[string]string{"auth": token}
			authData, _ := json.Marshal(authMsg)
			authData = append(authData, '\n')
			if _, writeErr := conn.Write(authData); writeErr != nil {
				errCh <- writeErr
				return
			}

			reader := bufio.NewReader(conn)
			_, authReadErr := reader.ReadBytes('\n')
			if authReadErr != nil {
				errCh <- authReadErr
				return
			}

			// Send initialize
			initReq := JSONRPCRequest{
				JSONRPC: "2.0",
				ID:      id,
				Method:  "initialize",
			}
			data, _ := json.Marshal(initReq)
			data = append(data, '\n')
			if _, writeErr := conn.Write(data); writeErr != nil {
				errCh <- writeErr
				return
			}

			// Read response
			_, readErr := reader.ReadBytes('\n')
			errCh <- readErr
		}(i)
	}

	// Wait for all connections
	for i := 0; i < numConns; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("Connection %d error: %v", i, err)
		}
	}
}

// TestServerAuthenticationRejection tests that the server rejects invalid tokens.
func TestServerAuthenticationRejection(t *testing.T) {
	// Create temp directory for tool workdir
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create tool provider
	agentCtx := &tools.AgentContext{
		Executor: exec.NewLocalExec(),
		WorkDir:  tmpDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"shell"})

	// Create and start server
	server := NewServer(provider, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx) //nolint:errcheck
	time.Sleep(200 * time.Millisecond)
	defer server.Stop() //nolint:errcheck

	// Connect to server via TCP
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", server.Port()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Send invalid auth token
	authMsg := map[string]string{"auth": "invalid-token"}
	data, err := json.Marshal(authMsg)
	if err != nil {
		t.Fatalf("Failed to marshal auth message: %v", err)
	}
	data = append(data, '\n')
	if _, writeErr := conn.Write(data); writeErr != nil {
		t.Fatalf("Failed to send auth message: %v", writeErr)
	}

	// Read auth response
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("Failed to read auth response: %v", err)
	}

	var response struct {
		Authenticated bool   `json:"authenticated"`
		Error         string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("Failed to parse auth response: %v", err)
	}

	if response.Authenticated {
		t.Error("Expected authentication to fail with invalid token")
	}

	if response.Error == "" {
		t.Error("Expected error message in rejection response")
	}

	t.Logf("Auth rejection: %s", response.Error)
}
