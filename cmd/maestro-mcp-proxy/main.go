// maestro-mcp-proxy is a tiny stdio proxy that forwards MCP traffic
// between Claude Code and the Maestro MCP server via TCP.
//
// Usage: maestro-mcp-proxy <host:port>
//
// The proxy connects to the TCP address, authenticates using the MCP_AUTH_TOKEN
// environment variable, then bidirectionally forwards stdin/stdout traffic.
// This allows Claude Code running inside a container to communicate with
// the Maestro MCP server running on the host.
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
)

// authTokenEnvVar is the environment variable containing the auth token.
const authTokenEnvVar = "MCP_AUTH_TOKEN"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: maestro-mcp-proxy <host:port>")
		os.Exit(1)
	}

	tcpAddr := os.Args[1]

	// Get auth token from environment
	authToken := os.Getenv(authTokenEnvVar)
	if authToken == "" {
		fmt.Fprintf(os.Stderr, "error: %s environment variable not set\n", authTokenEnvVar)
		os.Exit(1)
	}

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
