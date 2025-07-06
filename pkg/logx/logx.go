package logx

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Logger struct {
	agentID string
	logger  *log.Logger
}

type Level string

const (
	LevelDebug Level = "DEBUG"
	LevelInfo  Level = "INFO"
	LevelWarn  Level = "WARN"
	LevelError Level = "ERROR"
)

// DebugConfig controls debug logging behavior
type DebugConfig struct {
	Enabled     bool
	FileLogging bool
	LogDir      string
	Domains     map[string]bool // Which domains to enable debug for (nil = all)
}

// Global debug configuration
var (
	debugConfig = &DebugConfig{
		Enabled:     false,
		FileLogging: false,
		LogDir:      "", // Will be set to project root + "/logs" in init()
		Domains:     nil,
	}
	debugMutex sync.RWMutex
)

// getProjectRoot finds the project root directory by looking for go.mod
func getProjectRoot() string {
	// Start from current working directory
	dir, err := os.Getwd()
	if err != nil {
		return "." // Fallback to current directory
	}
	
	// Walk up the directory tree looking for go.mod
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding go.mod
			break
		}
		dir = parent
	}
	
	// If no go.mod found, return current working directory
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "." // Ultimate fallback
}

// getDefaultLogDir returns the default log directory in the project root
func getDefaultLogDir() string {
	projectRoot := getProjectRoot()
	return filepath.Join(projectRoot, "logs")
}

// Initialize debug configuration from environment variables
func init() {
	initDebugFromEnv()
}

// initDebugFromEnv initializes debug configuration from environment variables
func initDebugFromEnv() {
	debugMutex.Lock()
	defer debugMutex.Unlock()
	
	// Set default log directory to project root + "/logs"
	if debugConfig.LogDir == "" {
		debugConfig.LogDir = getDefaultLogDir()
	}
	
	// Check if debug is enabled via DEBUG=1 or DEBUG=true
	if debug := os.Getenv("DEBUG"); debug == "1" || strings.ToLower(debug) == "true" {
		debugConfig.Enabled = true
	}
	
	// Check for file logging via DEBUG_FILE=1 or DEBUG_FILE=true
	if debugFile := os.Getenv("DEBUG_FILE"); debugFile == "1" || strings.ToLower(debugFile) == "true" {
		debugConfig.FileLogging = true
	}
	
	// Set log directory from DEBUG_LOG_DIR or DEBUG_DIR (overrides default)
	if debugLogDir := os.Getenv("DEBUG_LOG_DIR"); debugLogDir != "" {
		debugConfig.LogDir = debugLogDir
	} else if debugDir := os.Getenv("DEBUG_DIR"); debugDir != "" {
		debugConfig.LogDir = debugDir
	}
	
	// Parse domain filtering from DEBUG_DOMAINS=coder,architect,dispatch
	if domains := os.Getenv("DEBUG_DOMAINS"); domains != "" {
		debugConfig.Domains = make(map[string]bool)
		for _, domain := range strings.Split(domains, ",") {
			debugConfig.Domains[strings.TrimSpace(domain)] = true
		}
	}
}

func NewLogger(agentID string) *Logger {
	return &Logger{
		agentID: agentID,
		logger:  log.New(os.Stderr, "", 0), // Log to stderr for CLI compatibility
	}
}

// SetDebugConfig configures global debug logging settings
func SetDebugConfig(enabled, fileLogging bool, logDir string) {
	debugMutex.Lock()
	defer debugMutex.Unlock()
	
	debugConfig.Enabled = enabled
	debugConfig.FileLogging = fileLogging
	
	// If no logDir specified, use default
	if logDir == "" {
		debugConfig.LogDir = getDefaultLogDir()
	} else {
		debugConfig.LogDir = logDir
	}
	
	// Create log directory if needed
	if fileLogging && debugConfig.LogDir != "" {
		os.MkdirAll(debugConfig.LogDir, 0755)
	}
}

// SetDebugDomains configures which domains should have debug logging enabled
func SetDebugDomains(domains []string) {
	debugMutex.Lock()
	defer debugMutex.Unlock()
	
	if len(domains) == 0 {
		debugConfig.Domains = nil // Enable all domains
	} else {
		debugConfig.Domains = make(map[string]bool)
		for _, domain := range domains {
			debugConfig.Domains[strings.TrimSpace(domain)] = true
		}
	}
}

// IsDebugEnabled returns whether debug logging is enabled
func IsDebugEnabled() bool {
	debugMutex.RLock()
	defer debugMutex.RUnlock()
	return debugConfig.Enabled
}

// IsDebugEnabledForDomain returns whether debug logging is enabled for a specific domain
func IsDebugEnabledForDomain(domain string) bool {
	debugMutex.RLock()
	defer debugMutex.RUnlock()
	
	if !debugConfig.Enabled {
		return false
	}
	
	// If no domain filtering is configured, enable all domains
	if debugConfig.Domains == nil {
		return true
	}
	
	// Check if this specific domain is enabled
	return debugConfig.Domains[domain]
}

func (l *Logger) log(level Level, format string, args ...any) {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	message := fmt.Sprintf(format, args...)
	logLine := fmt.Sprintf("[%s] [%s] %s: %s", timestamp, l.agentID, level, message)
	l.logger.Println(logLine)
}

func (l *Logger) Debug(format string, args ...any) {
	// Check if debug logging is enabled
	debugMutex.RLock()
	enabled := debugConfig.Enabled
	debugMutex.RUnlock()
	
	if !enabled {
		return
	}
	
	l.log(LevelDebug, format, args...)
}

// Debug logs a debug message with context and domain filtering
// This is the new primary debug function recommended for all new code
//
// Usage examples:
//   logx.Debug(ctx, "coder", "Task started: %s", taskID)
//   logx.Debug(ctx, "architect", "Story validation: %d requirements", count)
//   logx.Debug(ctx, "dispatch", "Routing %s -> %s", from, to)
//
// Environment variable control:
//   DEBUG=1                           # Enable debug for all domains
//   DEBUG=1 DEBUG_DOMAINS=coder       # Enable debug only for coder domain
//   DEBUG=1 DEBUG_DOMAINS=coder,arch  # Enable debug for multiple domains
//   DEBUG=1 DEBUG_FILE=1              # Enable file logging
//   DEBUG=1 DEBUG_LOG_DIR=/tmp/logs   # Set log directory (default: {project_root}/logs)
func Debug(ctx context.Context, domain, format string, args ...any) {
	if !IsDebugEnabledForDomain(domain) {
		return
	}
	
	// Get agent ID from context if available
	agentID := "unknown"
	if ctx != nil {
		if id := ctx.Value("agent_id"); id != nil {
			if idStr, ok := id.(string); ok {
				agentID = idStr
			}
		}
	}
	
	// Create temporary logger for this debug call
	logger := NewLogger(agentID)
	message := fmt.Sprintf("[%s] %s", domain, fmt.Sprintf(format, args...))
	logger.log(LevelDebug, "%s", message)
}

func (l *Logger) Info(format string, args ...any) {
	l.log(LevelInfo, format, args...)
}

func (l *Logger) Warn(format string, args ...any) {
	l.log(LevelWarn, format, args...)
}

func (l *Logger) Error(format string, args ...any) {
	l.log(LevelError, format, args...)
}

// DebugToFile writes debug information to a specific file (replaces scattered WriteFile calls)
func (l *Logger) DebugToFile(filename, format string, args ...any) {
	debugMutex.RLock()
	enabled := debugConfig.Enabled
	fileLogging := debugConfig.FileLogging
	logDir := debugConfig.LogDir
	debugMutex.RUnlock()
	
	if !enabled {
		return
	}
	
	// Always log to console debug
	l.Debug(format, args...)
	
	// Optionally log to file
	if fileLogging {
		timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		message := fmt.Sprintf(format, args...)
		debugMsg := fmt.Sprintf("[%s] [%s] DEBUG: %s\n", timestamp, l.agentID, message)
		
		// Ensure log directory exists
		if err := os.MkdirAll(logDir, 0755); err != nil {
			// If we can't create the directory, just skip file logging
			return
		}
		
		filePath := filepath.Join(logDir, filename)
		os.WriteFile(filePath, []byte(debugMsg), 0644)
	}
}

// DebugToFile logs a debug message with context, domain, and optional file output
// This replaces scattered fmt.Printf + os.WriteFile patterns in the codebase
func DebugToFile(ctx context.Context, domain, filename, format string, args ...any) {
	if !IsDebugEnabledForDomain(domain) {
		return
	}
	
	// Always do console debug logging
	Debug(ctx, domain, format, args...)
	
	// Optionally write to file if file logging is enabled
	debugMutex.RLock()
	fileLogging := debugConfig.FileLogging
	logDir := debugConfig.LogDir
	debugMutex.RUnlock()
	
	if fileLogging && filename != "" {
		// Get agent ID from context if available
		agentID := "unknown"
		if ctx != nil {
			if id := ctx.Value("agent_id"); id != nil {
				if idStr, ok := id.(string); ok {
					agentID = idStr
				}
			}
		}
		
		timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		message := fmt.Sprintf(format, args...)
		debugMsg := fmt.Sprintf("[%s] [%s] [%s] DEBUG: %s\n", timestamp, agentID, domain, message)
		
		// Ensure log directory exists
		if err := os.MkdirAll(logDir, 0755); err != nil {
			// If we can't create the directory, just skip file logging
			return
		}
		
		filePath := filepath.Join(logDir, filename)
		os.WriteFile(filePath, []byte(debugMsg), 0644)
	}
}

// DebugState logs state transition information (common pattern in codebase)
func (l *Logger) DebugState(action, state string, extra ...string) {
	extraInfo := ""
	if len(extra) > 0 {
		extraInfo = fmt.Sprintf(" - %s", extra[0])
	}
	l.Debug("State %s: %s%s", action, state, extraInfo)
}

// DebugMessage logs message processing information (common pattern)
func (l *Logger) DebugMessage(messageType, details string) {
	l.Debug("Message %s: %s", messageType, details)
}

// Convenient global functions for common debug patterns

// DebugState logs state transition information with context and domain
func DebugState(ctx context.Context, domain, action, state string, extra ...string) {
	extraInfo := ""
	if len(extra) > 0 {
		extraInfo = fmt.Sprintf(" - %s", extra[0])
	}
	Debug(ctx, domain, "State %s: %s%s", action, state, extraInfo)
}

// DebugMessage logs message processing information with context and domain
func DebugMessage(ctx context.Context, domain, messageType, details string) {
	Debug(ctx, domain, "Message %s: %s", messageType, details)
}

// DebugFlow logs workflow step information with context and domain
func DebugFlow(ctx context.Context, domain, step, status string, extra ...string) {
	extraInfo := ""
	if len(extra) > 0 {
		extraInfo = fmt.Sprintf(" - %s", extra[0])
	}
	Debug(ctx, domain, "Flow %s: %s%s", step, status, extraInfo)
}

func (l *Logger) GetAgentID() string {
	return l.agentID
}

func (l *Logger) WithAgentID(agentID string) *Logger {
	return &Logger{
		agentID: agentID,
		logger:  l.logger,
	}
}
