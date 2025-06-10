# Go Code Style Guide

This document defines the coding standards and conventions for the MVP Multi-Agent AI Coding System orchestrator.

## General Principles

- **Simplicity**: Prefer clear, simple solutions over clever ones
- **Consistency**: Follow established patterns throughout the codebase
- **Readability**: Write code that tells a story
- **Testing**: All code should be unit tested where practical

## Go Formatting

### Code Formatting
- Use `go fmt` for all Go code formatting
- Use `staticcheck` for static analysis and linting
- No exceptions to standard Go formatting rules

### Build Commands
```bash
make build    # Build the orchestrator binary
make test     # Run all tests (go test ./...)
make lint     # Run staticcheck and go fmt
make run      # Run the orchestrator with banner output
```

## Package Structure

### Import Organization
```go
import (
    // Standard library first
    "context"
    "fmt"
    "time"
    
    // Third-party packages
    "github.com/external/package"
    
    // Internal packages
    "orchestrator/pkg/config"
    "orchestrator/pkg/proto"
)
```

### Package Naming
- Use single word package names when possible
- Use lowercase package names
- Package names should be nouns (config, dispatch, proto)

## Error Handling

### Error Wrapping
```go
// Preferred
if err := someFunc(); err != nil {
    return fmt.Errorf("failed to process message: %w", err)
}

// Not preferred
if err := someFunc(); err != nil {
    return err
}
```

### Error Messages
- Start with lowercase (unless proper noun)
- No punctuation at the end
- Be specific about what failed
- Include context when helpful

```go
// Good
return fmt.Errorf("failed to read config file %s: %w", path, err)

// Bad
return fmt.Errorf("Error reading file")
```

## Naming Conventions

### Variables
- Use camelCase for variables
- Use descriptive names, avoid abbreviations
- Single letter variables only for short loops (i, j, k)

```go
// Good
messageCount := 0
rateLimiter := limiter.New()

// Bad
msgCnt := 0
rl := limiter.New()
```

### Functions
- Use camelCase starting with lowercase for private functions
- Use PascalCase for public functions
- Function names should be verbs or verb phrases

```go
// Good
func processMessage(ctx context.Context, msg *proto.AgentMsg) error
func (d *Dispatcher) RegisterAgent(agent Agent) error

// Bad
func message(ctx context.Context, msg *proto.AgentMsg) error
func (d *Dispatcher) agent(agent Agent) error
```

### Constants
- Use PascalCase for exported constants
- Use camelCase for unexported constants
- Group related constants

```go
const (
    MsgTypeTASK     = "TASK"
    MsgTypeRESULT   = "RESULT"
    MsgTypeERROR    = "ERROR"
    MsgTypeQUESTION = "QUESTION"
    MsgTypeSHUTDOWN = "SHUTDOWN"
)
```

## Structs and Interfaces

### Struct Definition
```go
// Good - fields grouped logically, documented
type Orchestrator struct {
    // Core components
    config      *config.Config
    dispatcher  *dispatch.Dispatcher
    rateLimiter *limiter.Limiter
    
    // Logging and monitoring
    eventLog *eventlog.Writer
    logger   *logx.Logger
    
    // Agent management
    agents       map[string]StatusAgent
    shutdownTime time.Duration
}
```

### Interface Definition
- Keep interfaces small and focused
- Use descriptive names ending in -er when appropriate

```go
type Agent interface {
    ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error)
    GetID() string
    Shutdown(ctx context.Context) error
}
```

## Concurrency

### Context Usage
- Always accept context as first parameter
- Respect context cancellation
- Pass context down the call chain

```go
func (d *Dispatcher) processMessage(ctx context.Context, msg *proto.AgentMsg) {
    select {
    case <-ctx.Done():
        return // Respect cancellation
    default:
        // Continue processing
    }
}
```

### Mutex Usage
- Use sync.RWMutex when read operations outnumber writes
- Keep critical sections small
- Document what the mutex protects

```go
type Dispatcher struct {
    mu     sync.RWMutex // protects agents map
    agents map[string]Agent
}
```

## Testing

### Test Organization
- One test file per source file (`foo.go` → `foo_test.go`)
- Use table-driven tests for multiple scenarios
- Test error conditions explicitly

```go
func TestConfigLoader(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        want     *Config
        wantErr  bool
    }{
        {
            name:  "valid config",
            input: `{"models": {"claude": {"api_key": "test"}}}`,
            want:  &Config{/* expected config */},
        },
        // More test cases...
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

### Test Naming
- Test functions start with `Test`
- Use descriptive test names
- Include the scenario being tested

```go
func TestDispatcher_ProcessMessage_Success(t *testing.T)
func TestDispatcher_ProcessMessage_RateLimitExceeded(t *testing.T)
func TestDispatcher_ProcessMessage_InvalidAgent(t *testing.T)
```

## Logging

### Log Levels
- `DEBUG`: Detailed information for debugging
- `INFO`: General information about program flow
- `WARN`: Potentially harmful situations
- `ERROR`: Error events that don't stop the program

### Log Messages
```go
// Good - specific, includes context
logger.Info("Starting orchestrator with %d agents", len(agents))
logger.Error("Failed to process message %s: %v", msgID, err)

// Bad - vague, no context
logger.Info("Starting")
logger.Error("Error: %v", err)
```

## Git Commit Messages

### Format
```
<type>: <description>

[optional body]

🤖 Generated with [Claude Code](https://claude.ai/code)

Co-Authored-By: Claude <noreply@anthropic.com>
```

### Types
- `feat`: New feature
- `fix`: Bug fix
- `refactor`: Code refactoring
- `test`: Adding or updating tests
- `docs`: Documentation changes
- `build`: Build system changes
- `chore`: Maintenance tasks

### Examples
```
feat: implement graceful shutdown with STATUS.md generation

Add SIGINT/SIGTERM signal handling that broadcasts SHUTDOWN messages
to all agents and collects status reports before terminating.

🤖 Generated with [Claude Code](https://claude.ai/code)

Co-Authored-By: Claude <noreply@anthropic.com>
```

```
fix: prevent duplicate message logging in dispatcher

Remove duplicate WriteMessage call in processWithRetry to fix
TestE2EMultipleStories expecting 3 results but getting 6.

🤖 Generated with [Claude Code](https://claude.ai/code)

Co-Authored-By: Claude <noreply@anthropic.com>
```

## File Organization

### Directory Structure
```
orchestrator/
├── agents/           # Agent implementations
├── config/          # Configuration files
├── docs/            # Documentation
├── logs/            # Runtime logs (created during execution)
├── pkg/             # Core packages
│   ├── config/      # Configuration loading
│   ├── dispatch/    # Message dispatching
│   ├── eventlog/    # Event logging
│   ├── limiter/     # Rate limiting
│   ├── logx/        # Structured logging
│   └── proto/       # Message protocol
├── status/          # Status reports (created during shutdown)
├── stories/         # Development stories
├── main.go          # Application entry point
├── Makefile         # Build automation
├── go.mod           # Go module definition
└── README.md        # Project overview
```

### File Naming
- Use lowercase with underscores for multi-word files
- Test files end with `_test.go`
- Keep file names concise but descriptive

## Documentation

### Code Comments
- Use complete sentences
- Explain the "why", not just the "what"
- Document all exported functions, types, and constants

```go
// ProcessMessage handles incoming agent messages with retry logic.
// It applies rate limiting before forwarding to the target agent
// and implements exponential backoff for failed attempts.
func (d *Dispatcher) ProcessMessage(msg *proto.AgentMsg) error {
    // Implementation...
}
```

### Package Documentation
- Each package should have a doc.go file or package comment
- Explain the package's purpose and main concepts

## Performance Guidelines

### Memory Management
- Prefer stack allocation over heap when possible
- Reuse buffers and slices where appropriate
- Use sync.Pool for expensive-to-create objects

### Concurrency
- Use channels for communication, mutexes for state protection
- Avoid sharing memory; prefer message passing
- Use worker pools for bounded concurrency

## Security Considerations

### Sensitive Data
- Never log API keys or sensitive configuration
- Use environment variables for secrets
- Validate all external inputs

### Error Information
- Don't expose internal paths or sensitive details in error messages
- Sanitize error messages returned to external callers

---

This style guide should be updated as the codebase evolves and new patterns emerge.