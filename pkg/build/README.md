# Build System Architecture

The build system provides a unified interface for executing build, test, lint, and run operations across different project types. This eliminates the need for hardcoded `make` commands and provides automatic language detection.

## Architecture Overview

### Core Components

1. **`BuildBackend` Interface** - Defines the contract for all build backends
2. **`Registry`** - Manages backend registration and project detection
3. **Backend Implementations** - Language-specific build logic
4. **Priority System** - Ensures correct backend selection when multiple backends match

### Current MVP Backends

- **GoBackend** - Go projects with `go.mod` files
- **PythonBackend** - Python projects using `uv` package manager
- **NodeBackend** - Node.js/JavaScript projects with `package.json`
- **MakeBackend** - Generic projects with Makefiles
- **NullBackend** - Empty repositories (fallback)

## BuildBackend Interface

All backends must implement the `BuildBackend` interface:

```go
type BuildBackend interface {
    // Name returns the backend name for identification
    Name() string
    
    // Detect determines if this backend applies to the given project root
    Detect(root string) bool
    
    // Build executes the build process for the project
    Build(ctx context.Context, root string, stream io.Writer) error
    
    // Test executes the test suite for the project
    Test(ctx context.Context, root string, stream io.Writer) error
    
    // Lint executes linting checks for the project
    Lint(ctx context.Context, root string, stream io.Writer) error
    
    // Run executes the application with provided arguments
    Run(ctx context.Context, root string, args []string, stream io.Writer) error
}
```

## Adding a New Backend

### Step 1: Create Backend Implementation

Create a new file `{language}_backend.go` in the `pkg/build/` directory:

```go
package build

import (
    "context"
    "fmt"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
)

// RustBackend handles Rust projects
type RustBackend struct{}

// NewRustBackend creates a new Rust backend
func NewRustBackend() *RustBackend {
    return &RustBackend{}
}

// Name returns the backend name
func (r *RustBackend) Name() string {
    return "rust"
}

// Detect checks if this is a Rust project
func (r *RustBackend) Detect(root string) bool {
    // Check for Cargo.toml
    if _, err := os.Stat(filepath.Join(root, "Cargo.toml")); err == nil {
        return true
    }
    
    // Check for Rust source files
    return r.containsRustFiles(root)
}

// Build executes cargo build
func (r *RustBackend) Build(ctx context.Context, root string, stream io.Writer) error {
    fmt.Fprintf(stream, "🦀 Building Rust project...\n")
    
    if err := r.runCommand(ctx, root, stream, "cargo", "build"); err != nil {
        return fmt.Errorf("build failed: %w", err)
    }
    
    fmt.Fprintf(stream, "✅ Rust build completed successfully\n")
    return nil
}

// Test executes cargo test
func (r *RustBackend) Test(ctx context.Context, root string, stream io.Writer) error {
    fmt.Fprintf(stream, "🧪 Running Rust tests...\n")
    
    if err := r.runCommand(ctx, root, stream, "cargo", "test"); err != nil {
        return fmt.Errorf("tests failed: %w", err)
    }
    
    fmt.Fprintf(stream, "✅ Rust tests completed successfully\n")
    return nil
}

// Lint executes cargo clippy
func (r *RustBackend) Lint(ctx context.Context, root string, stream io.Writer) error {
    fmt.Fprintf(stream, "🔍 Running Rust linting...\n")
    
    if err := r.runCommand(ctx, root, stream, "cargo", "clippy"); err != nil {
        return fmt.Errorf("linting failed: %w", err)
    }
    
    fmt.Fprintf(stream, "✅ Rust linting completed successfully\n")
    return nil
}

// Run executes cargo run
func (r *RustBackend) Run(ctx context.Context, root string, args []string, stream io.Writer) error {
    fmt.Fprintf(stream, "🚀 Running Rust application...\n")
    
    cargoArgs := append([]string{"run"}, args...)
    if err := r.runCommand(ctx, root, stream, "cargo", cargoArgs...); err != nil {
        return fmt.Errorf("run failed: %w", err)
    }
    
    fmt.Fprintf(stream, "✅ Rust application completed successfully\n")
    return nil
}

// Helper methods...
```

### Step 2: Register Backend in Registry

Update `registry.go` to include your new backend:

```go
func NewRegistry() *Registry {
    r := &Registry{}
    
    // Register MVP backends in priority order
    r.Register(NewGoBackend(), PriorityHigh)
    r.Register(NewPythonBackend(), PriorityHigh)
    r.Register(NewNodeBackend(), PriorityHigh)
    r.Register(NewRustBackend(), PriorityHigh)  // Add your backend
    r.Register(NewMakeBackend(), PriorityMedium)
    r.Register(NewNullBackend(), PriorityLow)
    
    return r
}
```

### Step 3: Add Tests

Create tests in `{language}_backend_test.go`:

```go
package build

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"
)

func TestRustBackend(t *testing.T) {
    // Create temporary directory with Cargo.toml
    tempDir, err := os.MkdirTemp("", "rust-backend-test")
    if err != nil {
        t.Fatalf("Failed to create temp dir: %v", err)
    }
    defer os.RemoveAll(tempDir)

    // Create Cargo.toml
    cargoToml := `[package]
name = "test"
version = "0.1.0"
edition = "2021"
`
    if err := os.WriteFile(filepath.Join(tempDir, "Cargo.toml"), []byte(cargoToml), 0644); err != nil {
        t.Fatalf("Failed to create Cargo.toml: %v", err)
    }

    backend := NewRustBackend()
    
    // Test detection
    if !backend.Detect(tempDir) {
        t.Error("RustBackend should detect directories with Cargo.toml")
    }
    
    // Test name
    if backend.Name() != "rust" {
        t.Errorf("Expected name 'rust', got '%s'", backend.Name())
    }
    
    // Test build (may fail without rust toolchain, but should not panic)
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    var buf strings.Builder
    backend.Build(ctx, tempDir, &buf)
    
    // At minimum, output should contain the build command
    if !strings.Contains(buf.String(), "cargo build") {
        t.Error("Expected 'cargo build' in output")
    }
}
```

## Backend Development Guidelines

### Detection Logic

1. **Primary Detection**: Check for language-specific project files
   - Go: `go.mod`, `go.sum`
   - Python: `pyproject.toml`, `requirements.txt`, `setup.py`
   - Node.js: `package.json`, `package-lock.json`
   - Rust: `Cargo.toml`, `Cargo.lock`

2. **Secondary Detection**: Check for source files if no project files exist
   - Look in common source directories: `src/`, `lib/`, `app/`
   - Check for language-specific file extensions

3. **Avoid False Positives**: Be specific to avoid conflicting with other backends

### Command Execution

1. **Use Context**: Always respect the provided context for cancellation
2. **Stream Output**: Write all output to the provided `io.Writer`
3. **Error Handling**: Wrap errors with context about what failed
4. **Command Logging**: Log the actual commands being executed

### Tool Detection

1. **Check Tool Availability**: Use `exec.LookPath()` to verify tools exist
2. **Provide Fallbacks**: Support multiple tools when possible
3. **Graceful Degradation**: Handle missing tools gracefully

### Example Helper Methods

```go
// commandExists checks if a command is available in PATH
func (b *MyBackend) commandExists(cmd string) bool {
    _, err := exec.LookPath(cmd)
    return err == nil
}

// runCommand executes a command and streams output
func (b *MyBackend) runCommand(ctx context.Context, root string, stream io.Writer, name string, args ...string) error {
    cmd := exec.CommandContext(ctx, name, args...)
    cmd.Dir = root
    cmd.Stdout = stream
    cmd.Stderr = stream
    
    fmt.Fprintf(stream, "$ %s %s\n", name, strings.Join(args, " "))
    
    if err := cmd.Run(); err != nil {
        if exitError, ok := err.(*exec.ExitError); ok {
            return fmt.Errorf("%s %s failed with exit code %d", name, strings.Join(args, " "), exitError.ExitCode())
        }
        return fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
    }
    
    return nil
}

// hasFiles checks if directory contains files matching patterns
func (b *MyBackend) hasFiles(root string, patterns ...string) bool {
    entries, err := os.ReadDir(root)
    if err != nil {
        return false
    }
    
    for _, entry := range entries {
        if entry.IsDir() {
            continue
        }
        
        for _, pattern := range patterns {
            if matched, _ := filepath.Match(pattern, entry.Name()); matched {
                return true
            }
        }
    }
    
    return false
}
```

## Priority System

Backends are registered with priorities to ensure correct selection:

- **PriorityHigh (100)**: Language-specific backends (Go, Python, Node.js, Rust)
- **PriorityMedium (50)**: Generic build systems (Make, CMake)
- **PriorityLow (10)**: Fallback backends (Null)

Higher priority backends are checked first. If multiple backends match, the first one registered wins.

## Integration Points

### Coder Agent Integration

The build system is integrated into the coder agent's testing workflow:

1. **Backend Detection**: Automatically detects project type in `TESTING` state
2. **Context Storage**: Stores backend name in agent state for debugging
3. **Execution**: Uses detected backend for test execution
4. **Error Handling**: Proper error propagation and state transitions

### Template Integration

Coder templates can access backend information through the task payload:

```go
// Backend information is available in TASK messages
backendName := taskPayload["build_backend"].(string)
```

## Common Patterns

### Multiple Tool Support

```go
// Try tools in order of preference
tools := []string{"preferred-tool", "backup-tool", "fallback-tool"}
for _, tool := range tools {
    if b.commandExists(tool) {
        return b.runCommand(ctx, root, stream, tool, args...)
    }
}
return fmt.Errorf("no suitable tool found")
```

### Script Detection

```go
// Check for build scripts in package.json, pyproject.toml, etc.
func (b *MyBackend) hasScript(root, script string) bool {
    configFile := filepath.Join(root, "package.json")
    data, err := os.ReadFile(configFile)
    if err != nil {
        return false
    }
    
    // Simple string search (or use JSON parsing for accuracy)
    return strings.Contains(string(data), fmt.Sprintf("\"%s\":", script))
}
```

### Conditional Operations

```go
// Only run operations if they're configured
if b.hasTestFiles(root) {
    return b.runTests(ctx, root, stream)
}

fmt.Fprintf(stream, "✅ No test files found, skipping tests\n")
return nil
```

## Testing Strategy

1. **Unit Tests**: Test backend detection and basic functionality
2. **Integration Tests**: Test with real project structures
3. **Error Handling**: Test missing tools and malformed projects
4. **Performance Tests**: Test with large projects and timeouts

## Troubleshooting

### Common Issues

1. **Backend Not Detected**: Check detection logic and file patterns
2. **Command Not Found**: Verify tool installation and PATH
3. **Permission Errors**: Check file permissions and execution rights
4. **Timeout Issues**: Adjust context timeouts for slow operations

### Debug Output

Enable debug output by examining the stream content in tests:

```go
var buf strings.Builder
err := backend.Build(ctx, root, &buf)
t.Logf("Build output: %s", buf.String())
```

## Future Enhancements

Potential areas for expansion:

1. **Polyglot Project Support**: Multiple backends per project
2. **Custom Backend Plugins**: External backend registration
3. **Build Caching**: Intelligent caching of build artifacts
4. **Parallel Execution**: Concurrent build operations
5. **Remote Build Support**: Distributed build systems
6. **IDE Integration**: Language server protocol support

## Contributing

When adding new backends:

1. Follow the established patterns and conventions
2. Add comprehensive tests for all functionality
3. Update this documentation with your backend
4. Consider tool availability and graceful degradation
5. Test with real projects of the target language

The build system is designed to be extensible and maintainable. Each backend should be self-contained and follow the same patterns for consistency.