package claude

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"orchestrator/pkg/coder/claude/mcpserver"
	"orchestrator/pkg/logx"
)

func TestVerifyMCPConnectivity_HostSide(t *testing.T) {
	// Start a real MCP server with a minimal tool provider.
	logger := logx.NewLogger("test")
	server := mcpserver.NewServer(nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = server.Start(ctx)
	}()

	// Wait for server to bind.
	time.Sleep(200 * time.Millisecond)

	port := server.Port()
	if port == 0 {
		t.Fatal("server failed to bind to port")
	}

	// Test host-side connectivity: connect and authenticate.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	token := server.Token()
	authMsg := fmt.Sprintf("{\"auth\":%q}\n", token)
	if _, writeErr := conn.Write([]byte(authMsg)); writeErr != nil {
		_ = conn.Close()
		t.Fatalf("failed to send auth: %v", writeErr)
	}

	buf := make([]byte, 1024)
	n, readErr := conn.Read(buf)
	_ = conn.Close()

	if readErr != nil {
		t.Fatalf("failed to read auth response: %v", readErr)
	}

	response := string(buf[:n])
	if response == "" {
		t.Fatal("empty auth response")
	}

	// Verify the response contains "authenticated":true.
	if !contains(response, `"authenticated":true`) {
		t.Errorf("expected authenticated:true, got: %s", response)
	}

	_ = server.Stop()
}

func TestVerifyMCPConnectivity_BadToken(t *testing.T) {
	logger := logx.NewLogger("test")
	server := mcpserver.NewServer(nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = server.Start(ctx)
	}()

	time.Sleep(200 * time.Millisecond)

	port := server.Port()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	// Send wrong token.
	authMsg := "{\"auth\":\"wrong-token\"}\n"
	if _, writeErr := conn.Write([]byte(authMsg)); writeErr != nil {
		_ = conn.Close()
		t.Fatalf("failed to send auth: %v", writeErr)
	}

	buf := make([]byte, 1024)
	n, readErr := conn.Read(buf)
	_ = conn.Close()

	if readErr != nil {
		t.Fatalf("failed to read response: %v", readErr)
	}

	response := string(buf[:n])
	if contains(response, `"authenticated":true`) {
		t.Error("expected authentication to fail with wrong token")
	}

	_ = server.Stop()
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
