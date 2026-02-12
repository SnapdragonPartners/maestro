// maestro-mcp-proxy is a tiny stdio proxy that forwards MCP traffic
// between Claude Code and the Maestro MCP server via TCP.
//
// Usage:
//
//	maestro-mcp-proxy <host:port>          # Normal proxy mode
//	maestro-mcp-proxy --check <host:port>  # Health check mode
//
// In normal mode, the proxy connects to the TCP address, authenticates using
// the MCP_AUTH_TOKEN environment variable, then bidirectionally forwards
// stdin/stdout traffic.
//
// In check mode (--check), the proxy connects, authenticates, and exits
// with code 0 on success or 1 on failure. No stdin/stdout forwarding.
//
// TCP is used instead of Unix sockets because Unix sockets don't work through
// Docker Desktop's file sharing on macOS (gRPC-FUSE limitation).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// authTokenEnvVar is the environment variable containing the auth token.
const authTokenEnvVar = "MCP_AUTH_TOKEN"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: maestro-mcp-proxy [--check] <host:port>")
		os.Exit(1)
	}

	// Parse args: --check mode or normal proxy mode.
	checkMode := false
	tcpAddr := os.Args[1]
	if os.Args[1] == "--check" {
		checkMode = true
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: maestro-mcp-proxy --check <host:port>")
			os.Exit(1)
		}
		tcpAddr = os.Args[2]
	}

	// Get auth token from environment
	authToken := os.Getenv(authTokenEnvVar)
	if authToken == "" {
		fmt.Fprintf(os.Stderr, "error: %s environment variable not set\n", authTokenEnvVar)
		os.Exit(1)
	}

	if checkMode {
		runCheck(tcpAddr, authToken)
	} else {
		runProxy(tcpAddr, authToken)
	}
}

// runCheck connects, authenticates, and exits with status code indicating success/failure.
func runCheck(tcpAddr, authToken string) {
	// Use a short timeout for health checks.
	conn, err := net.DialTimeout("tcp", tcpAddr, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		os.Exit(1)
	}

	if err := authenticate(conn, authToken); err != nil {
		_ = conn.Close()
		fmt.Fprintf(os.Stderr, "auth failed: %v\n", err)
		os.Exit(1)
	}

	_ = conn.Close()
	fmt.Println("ok")
}

// runProxy runs the normal bidirectional proxy mode.
func runProxy(tcpAddr, authToken string) {
	// Connect to TCP server
	conn, err := net.Dial("tcp", tcpAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect to %s: %v\n", tcpAddr, err)
		os.Exit(1)
	}
	defer conn.Close() //nolint:errcheck // Best-effort close on defer

	// Authenticate with the server
	if err := authenticate(conn, authToken); err != nil {
		conn.Close() //nolint:errcheck,gosec // Best-effort close before exit
		fmt.Fprintf(os.Stderr, "authentication failed: %v\n", err)
		os.Exit(1) //nolint:gocritic // exitAfterDefer is intentional - we close conn explicitly above
	}

	// Bidirectional copy between stdin/stdout and TCP connection
	var wg sync.WaitGroup
	wg.Add(2)

	// stdin -> TCP
	go func() {
		defer wg.Done()
		_, _ = io.Copy(conn, os.Stdin)
		// Signal EOF to server by closing write side
		if c, ok := conn.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		}
	}()

	// TCP -> stdout
	go func() {
		defer wg.Done()
		_, _ = io.Copy(os.Stdout, conn)
	}()

	wg.Wait()
}

// authResponse is the expected response from the server.
type authResponse struct { //nolint:govet // fieldalignment acceptable for small struct
	Authenticated bool   `json:"authenticated"`
	Error         string `json:"error,omitempty"`
}

// authenticate sends the auth token and waits for confirmation.
func authenticate(conn net.Conn, token string) error {
	// Send auth message
	authMsg := map[string]string{"auth": token}
	data, err := json.Marshal(authMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal auth message: %w", err)
	}
	data = append(data, '\n')

	if _, writeErr := conn.Write(data); writeErr != nil {
		return fmt.Errorf("failed to send auth message: %w", writeErr)
	}

	// Read auth response
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("failed to read auth response: %w", err)
	}

	var response authResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return fmt.Errorf("failed to parse auth response: %w", err)
	}

	if !response.Authenticated {
		if response.Error != "" {
			return fmt.Errorf("server rejected authentication: %s", response.Error)
		}
		return fmt.Errorf("server rejected authentication")
	}

	return nil
}
