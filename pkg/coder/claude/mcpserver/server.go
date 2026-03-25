// Package mcpserver implements an MCP server that exposes Maestro tools to Claude Code.
// The server listens on a TCP port and handles JSON-RPC 2.0 requests from the
// stdio proxy running inside containers.
//
// TCP is used instead of Unix sockets because Unix sockets don't work through
// Docker Desktop's file sharing on macOS (gRPC-FUSE limitation).
package mcpserver

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

// Server is an MCP server that exposes Maestro tools via TCP.
type Server struct {
	toolProvider *tools.ToolProvider
	logger       *logx.Logger
	listener     net.Listener
	port         int
	authToken    string
	mu           sync.Mutex
	running      bool
	cancel       context.CancelFunc
	lastEffect   *tools.ProcessEffect // Last ProcessEffect from tool execution
	effectMu     sync.Mutex           // Guards lastEffect
}

// NewServer creates a new MCP server with a randomly generated auth token.
// The server will bind to an available port when Start() is called.
// Use Token() to get the auth token to pass to clients.
func NewServer(toolProvider *tools.ToolProvider, logger *logx.Logger) *Server {
	if logger == nil {
		logger = logx.NewLogger("mcp-server")
	}
	return &Server{
		toolProvider: toolProvider,
		logger:       logger,
		authToken:    generateToken(),
	}
}

// generateToken creates a cryptographically random 32-byte hex token.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a panic - this should never happen in practice
		// as crypto/rand.Read only fails if the system's entropy source is broken
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// Start begins listening for connections on a TCP port.
// This method blocks until Stop() is called or the context is cancelled.
// Use Port() to get the assigned port after Start() returns.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}

	// Create context with cancel for graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	// Listen on TCP with dynamic port assignment (":0" lets OS pick available port)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("failed to listen on TCP: %w", err)
	}

	// Extract the assigned port
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("unexpected listener address type: %T", listener.Addr())
	}
	s.port = addr.Port

	s.listener = listener
	s.running = true
	s.mu.Unlock()

	s.logger.Info("MCP server listening on port %d", s.port)

	// Accept connections until context cancelled
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("server context cancelled: %w", ctx.Err())
		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			// Check if we're shutting down
			select {
			case <-ctx.Done():
				return nil
			default:
				s.logger.Error("Failed to accept connection: %v", err)
				continue
			}
		}

		// Handle connection in goroutine
		go s.handleConnection(ctx, conn)
	}
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	s.running = false

	if s.cancel != nil {
		s.cancel()
	}

	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			return fmt.Errorf("failed to close listener: %w", err)
		}
	}

	s.logger.Info("MCP server stopped")
	return nil
}

// Port returns the TCP port the server is listening on.
// Returns 0 if the server is not running.
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// Token returns the auth token that clients must provide to connect.
func (s *Server) Token() string {
	return s.authToken
}

// ConsumeLastEffect atomically returns and clears the last ProcessEffect.
// Returns nil if no effect has been recorded since the last call.
// This prevents stale effect reuse across tool calls.
func (s *Server) ConsumeLastEffect() *tools.ProcessEffect {
	s.effectMu.Lock()
	defer s.effectMu.Unlock()
	eff := s.lastEffect
	s.lastEffect = nil
	return eff
}

// authMessage is the expected first message from clients.
type authMessage struct {
	Auth string `json:"auth"`
}

// handleConnection processes a single client connection.
// The first message must be an auth message with a valid token.
func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close() //nolint:errcheck // Best-effort close on defer

	s.logger.Debug("New connection from proxy")

	reader := bufio.NewReader(conn)

	// First message must be authentication
	if !s.authenticateConnection(reader, conn) {
		return
	}

	// Process requests after successful auth
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Read line-delimited JSON-RPC messages
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				s.logger.Debug("Connection read error: %v", err)
			}
			return
		}

		// Parse request
		var request JSONRPCRequest
		if err := json.Unmarshal(line, &request); err != nil {
			s.sendError(conn, nil, -32700, "Parse error", err.Error())
			continue
		}

		// Handle request
		s.handleRequest(ctx, conn, &request)
	}
}

// authenticateConnection validates the first message as an auth token.
// Returns true if authentication succeeds.
func (s *Server) authenticateConnection(reader *bufio.Reader, conn net.Conn) bool {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		s.logger.Debug("Failed to read auth message: %v", err)
		return false
	}

	var auth authMessage
	if err := json.Unmarshal(line, &auth); err != nil {
		s.logger.Warn("Invalid auth message format: %v", err)
		s.sendAuthError(conn, "Invalid auth message format")
		return false
	}

	if auth.Auth != s.authToken {
		s.logger.Warn("Invalid auth token from client")
		s.sendAuthError(conn, "Invalid auth token")
		return false
	}

	s.logger.Debug("Client authenticated successfully")

	// Send auth success response
	response := map[string]interface{}{
		"authenticated": true,
	}
	data, _ := json.Marshal(response)
	data = append(data, '\n')
	if _, writeErr := conn.Write(data); writeErr != nil {
		s.logger.Debug("Failed to send auth response: %v", writeErr)
		return false
	}

	return true
}

// sendAuthError sends an authentication error response.
func (s *Server) sendAuthError(conn net.Conn, message string) {
	response := map[string]interface{}{
		"authenticated": false,
		"error":         message,
	}
	data, _ := json.Marshal(response)
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

// handleRequest dispatches a JSON-RPC request to the appropriate handler.
func (s *Server) handleRequest(ctx context.Context, conn net.Conn, req *JSONRPCRequest) {
	switch req.Method {
	case "initialize":
		s.handleInitialize(conn, req)
	case "notifications/initialized":
		// No response needed for notifications
	case "tools/list":
		s.handleToolsList(conn, req)
	case "tools/call":
		s.handleToolsCall(ctx, conn, req)
	default:
		s.sendError(conn, req.ID, -32601, "Method not found", req.Method)
	}
}

// handleInitialize responds to the MCP initialize request.
func (s *Server) handleInitialize(conn net.Conn, req *JSONRPCRequest) {
	result := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "maestro",
			"version": "1.0.0",
		},
	}
	s.sendResult(conn, req.ID, result)
}

// handleToolsList returns the list of available tools.
func (s *Server) handleToolsList(conn net.Conn, req *JSONRPCRequest) {
	// Get tool metadata from provider
	toolMetas := s.toolProvider.List()

	// Convert to MCP format
	mcpTools := make([]map[string]interface{}, 0, len(toolMetas))
	for i := range toolMetas {
		mcpTool := map[string]interface{}{
			"name":        toolMetas[i].Name,
			"description": toolMetas[i].Description,
			"inputSchema": convertInputSchema(toolMetas[i].InputSchema),
		}
		mcpTools = append(mcpTools, mcpTool)
	}

	s.sendResult(conn, req.ID, map[string]interface{}{"tools": mcpTools})
}

// convertInputSchema converts our InputSchema to MCP-compatible format.
func convertInputSchema(schema tools.InputSchema) map[string]interface{} {
	result := map[string]interface{}{
		"type": schema.Type,
	}

	if len(schema.Properties) > 0 {
		props := make(map[string]interface{})
		for name, prop := range schema.Properties { //nolint:gocritic // rangeValCopy acceptable for map iteration
			props[name] = convertProperty(prop)
		}
		result["properties"] = props
	}

	if len(schema.Required) > 0 {
		result["required"] = schema.Required
	}

	return result
}

// convertProperty converts a Property to MCP-compatible format.
func convertProperty(prop tools.Property) map[string]interface{} { //nolint:gocritic // hugeParam acceptable for simple conversion
	result := map[string]interface{}{
		"type": prop.Type,
	}

	if prop.Description != "" {
		result["description"] = prop.Description
	}

	if len(prop.Enum) > 0 {
		result["enum"] = prop.Enum
	}

	if prop.Items != nil {
		result["items"] = convertProperty(*prop.Items)
	}

	if len(prop.Properties) > 0 {
		props := make(map[string]interface{})
		for name, p := range prop.Properties {
			props[name] = convertProperty(*p)
		}
		result["properties"] = props
	}

	if len(prop.Required) > 0 {
		result["required"] = prop.Required
	}

	if prop.MinItems != nil {
		result["minItems"] = *prop.MinItems
	}

	if prop.MaxItems != nil {
		result["maxItems"] = *prop.MaxItems
	}

	return result
}

// handleToolsCall executes a tool and returns the result.
func (s *Server) handleToolsCall(ctx context.Context, conn net.Conn, req *JSONRPCRequest) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(conn, req.ID, -32602, "Invalid params", err.Error())
		return
	}

	s.logger.Info("🔧 MCP tool call: %s", params.Name)

	// Get tool from provider
	tool, err := s.toolProvider.Get(params.Name)
	if err != nil {
		s.logger.Warn("Tool not found: %s - %v", params.Name, err)
		s.sendError(conn, req.ID, -32602, "Tool not found", err.Error())
		return
	}

	// Inject agent ID into context for tools that need it (e.g., chat_read, chat_post)
	// The AgentIDContextKey is defined in pkg/tools/chat_tools.go
	if agentID := s.toolProvider.AgentID(); agentID != "" {
		ctx = context.WithValue(ctx, tools.AgentIDContextKey, agentID)
	}

	// Execute tool
	result, err := tool.Exec(ctx, params.Arguments)
	if err != nil {
		s.logger.Warn("🔧 MCP tool %s failed: %v", params.Name, err)
		// Return error as tool result (not JSON-RPC error) so Claude sees it
		s.sendResult(conn, req.ID, map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("Error: %v", err),
				},
			},
			"isError": true,
		})
		return
	}

	// Store ProcessEffect for signal passthrough (e.g., done tool → SignalStoryComplete)
	if result.ProcessEffect != nil {
		s.effectMu.Lock()
		s.lastEffect = result.ProcessEffect
		s.effectMu.Unlock()
	}

	// Log successful execution with content preview
	contentPreview := result.Content
	if len(contentPreview) > 200 {
		contentPreview = contentPreview[:200] + "..."
	}
	s.logger.Info("🔧 MCP tool %s succeeded: %s", params.Name, contentPreview)

	// Format response
	content := result.Content
	if content == "" {
		content = "Tool executed successfully"
	}

	response := map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": content,
			},
		},
	}

	// Include process effect if present (for state machine signals)
	if result.ProcessEffect != nil {
		response["_maestro_effect"] = map[string]interface{}{
			"signal": result.ProcessEffect.Signal,
			"data":   result.ProcessEffect.Data,
		}
	}

	s.sendResult(conn, req.ID, response)
}

// JSON-RPC message types

// JSONRPCRequest represents a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      interface{}   `json:"id"`
	Result  interface{}   `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC 2.0 error.
type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// sendResult sends a successful JSON-RPC response.
func (s *Server) sendResult(conn net.Conn, id interface{}, result interface{}) { //nolint:gocritic // paramTypeCombine - clarity over brevity
	response := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	s.send(conn, &response)
}

// sendError sends an error JSON-RPC response.
func (s *Server) sendError(conn net.Conn, id interface{}, code int, message, data string) {
	response := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	s.send(conn, &response)
}

// send marshals and writes a response to the connection.
func (s *Server) send(conn net.Conn, response *JSONRPCResponse) {
	data, err := json.Marshal(response)
	if err != nil {
		s.logger.Error("Failed to marshal response: %v", err)
		return
	}

	// Write line-delimited JSON
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		s.logger.Debug("Failed to write response: %v", err)
	}
}
