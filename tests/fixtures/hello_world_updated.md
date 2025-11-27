# Helloworld Go Web Server - Complete Specification

## Overview

A minimal HTTP server in Go (latest available version) serving as a foundation for future projects. The server provides two endpoints with embedded HTML content, configurable port, graceful shutdown, and comprehensive test coverage.

## Core Requirements

### Go Version
- Use the latest available Go version in the environment
- The go.mod and Dockerfile should use the latest stable Go release available at build time

### Endpoints

#### `/health`
- **Method:** `GET` only (returns 405 Method Not Allowed for other methods)
- **Response Code:** `200 OK`
- **Response Body:** `"OK"`
- **Content-Type:** `text/plain`

#### `/` (Homepage)
- **Method:** `GET` only (returns 405 Method Not Allowed for other methods)
- **Response Code:** `200 OK`
- **Response Body:** HTML rendered from embedded `home.html`
- **Content-Type:** `text/html`
- **Template:** Static HTML5 page embedded at compile time

#### Undefined Routes
- **Response Code:** `404 Not Found`
- **Response Body:** Default 404 message

### Configuration

#### Port Configuration
- **Default:** 8080
- **CLI Flag:** `-port <number>` (e.g., `-port 9000`)
- **Validation:** 
  - Accept any port number 1-65535
  - If invalid or not provided, log warning and use default 8080
  - Log the actual port being used at startup

### HTML Template

#### home.html Requirements
- Basic, complete HTML5 document
- Embedded using Go's `embed` package
- Loaded once at startup and stored in memory
- Minimal content to demonstrate functionality
- Should include:
  - Standard HTML5 doctype
  - Basic meta tags (charset, viewport)
  - Title referencing "helloworld"
  - Simple body content confirming the server is working

Example structure:
```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Helloworld Server</title>
</head>
<body>
    <h1>Helloworld Server</h1>
    <p>Server is running successfully!</p>
</body>
</html>
```

### Server Behavior

#### Startup
1. Parse CLI flags for port configuration
2. Validate port number, fall back to 8080 with warning if invalid
3. Load and parse embedded HTML template
4. Start HTTP server on specified port
5. Log server startup with port number

#### Request Handling
- Use Go's default HTTP server settings (no custom timeouts or connection limits)
- Log each request with method, path, and response code using default `log.Println` format
- Return appropriate status codes:
  - 200 for successful GET requests to valid endpoints
  - 404 for undefined routes
  - 405 for non-GET methods on defined endpoints
  - 500 for internal errors (e.g., template rendering issues)

#### Shutdown
- Handle graceful shutdown on SIGINT/SIGTERM (Ctrl+C)
- Allow 5 seconds for existing connections to complete
- Log shutdown signal received and completion

### Error Handling

1. **Invalid Port:** Log warning and use default 8080
2. **Port Already in Use:** Log error and exit with non-zero status
3. **Non-GET Requests:** Return 405 Method Not Allowed
4. **Undefined Routes:** Return 404 Not Found
5. **Template Rendering Errors:** Return 500 Internal Server Error

### Logging

Use `log.Println` for all output with default timestamp format:
- Server startup with port number
- Each incoming request: `GET /health -> 200` (with default timestamp prefix)
- Warnings for invalid configuration
- Errors during operation
- Shutdown signals and completion

Note: The default `log.Println` timestamp format (e.g., `2024/01/15 10:30:45`) is acceptable and should be used.

## Testing Requirements

### Test Coverage
Maximum reasonable coverage including:

1. **Endpoint Tests:**
   - GET `/health` returns 200 with "OK"
   - GET `/` returns 200 with HTML content
   - POST/PUT/DELETE to endpoints return 405
   - Undefined routes return 404

2. **CLI Flag Tests:**
   - Valid port numbers are accepted
   - Invalid port numbers fall back to default
   - Missing port flag uses default

3. **Error Condition Tests:**
   - Proper error codes and messages for various failure scenarios
   - Note: Missing template file testing is not applicable since `//go:embed` causes compile-time errors

4. **Integration Tests:**
   - Full server startup and shutdown cycle
   - Concurrent request handling

### Test Execution
- Use Go's built-in `testing` and `net/http/httptest`
- Run with flags: `go test -v -cover ./...`
- Tests should be in `main_test.go`

## Build System

### Makefile Targets

```makefile
build:
	go fmt ./...
	go build -o helloworld .

run:
	./helloworld

test:
	go test -v -cover ./...

lint:
	golangci-lint run

clean:
	rm -f helloworld

.PHONY: build run test lint clean
```

### Linting
- Use `golangci-lint` with default configuration
- The lint target runs `golangci-lint run`
- It's acceptable to install golangci-lint as a development tool even though runtime code uses standard library only

## Project Structure

```
helloworld/
├── go.mod          # Module definition (latest Go version)
├── main.go         # Server implementation
├── home.html       # Embedded HTML template
├── main_test.go    # Comprehensive tests
└── Makefile        # Build automation
```

## Implementation Notes

1. **Standard Library for Runtime:** Runtime code uses standard library only (external tools for development/linting are acceptable)
2. **Embed Directive:** Use `//go:embed home.html` to compile template into binary
3. **Single File Implementation:** Keep main server logic in `main.go`
4. **Modular Code:** Despite being single file, organize code into logical functions
5. **Error Messages:** Use clear, descriptive error messages for debugging
6. **Logging Format:** Use default `log.Println` format without custom formatting

## Acceptance Criteria

- [ ] Server starts on port 8080 by default
- [ ] Port can be configured via `-port` flag
- [ ] `/health` endpoint returns "OK" for GET requests
- [ ] `/` endpoint serves embedded HTML page for GET requests
- [ ] Non-GET requests return 405 Method Not Allowed
- [ ] Undefined routes return 404 Not Found
- [ ] Server logs requests with method, path, and status using default log format
- [ ] Graceful shutdown completes within 5 seconds
- [ ] All tests pass with >80% coverage
- [ ] Makefile targets (build, run, test, lint, clean) work as specified
- [ ] Code passes `golangci-lint` with default settings
- [ ] Code is formatted with `go fmt`

## Out of Scope

- Docker/containerization (handled by infrastructure)
- Structured logging frameworks
- Environment variable configuration
- Middleware or routing libraries
- Database connections
- Authentication/authorization
- HTTPS/TLS support
- Static file serving beyond the embedded HTML
- JSON APIs or REST endpoints
- WebSocket support
