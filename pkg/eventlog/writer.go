// Package eventlog provides structured logging and event tracking for the orchestrator system.
package eventlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"orchestrator/pkg/proto"
)

// Writer handles structured logging of agent messages to daily rotated JSON log files.
type Writer struct {
	logDir       string
	currentFile  *os.File
	currentDate  string
	mu           sync.Mutex
	rotationHour int // Hour of day to rotate (0-23)
}

// NewWriter creates a new event log writer with daily rotation in the specified directory.
func NewWriter(logDir string, rotationHours int) (*Writer, error) {
	// Create logs directory if it doesn't exist.
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Default to 24 hours (daily rotation at midnight) if invalid
	if rotationHours <= 0 {
		rotationHours = 24
	}

	writer := &Writer{
		logDir:       logDir,
		rotationHour: rotationHours,
	}

	// Initialize with current log file.
	if err := writer.rotateIfNeeded(); err != nil {
		return nil, fmt.Errorf("failed to initialize log file: %w", err)
	}

	return writer, nil
}

// WriteMessage writes an agent message to the current log file with automatic rotation.
func (w *Writer) WriteMessage(msg *proto.AgentMsg) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Check if we need to rotate.
	if err := w.rotateIfNeeded(); err != nil {
		return fmt.Errorf("failed to rotate log file: %w", err)
	}

	// Convert message to JSON.
	jsonData, err := msg.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize message: %w", err)
	}

	// Write JSON line.
	if _, err := w.currentFile.Write(jsonData); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	// Add newline for JSONL format.
	if _, err := w.currentFile.WriteString("\n"); err != nil {
		return fmt.Errorf("failed to write newline: %w", err)
	}

	// Ensure data is written to disk.
	if err := w.currentFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync file: %w", err)
	}

	return nil
}

func (w *Writer) rotateIfNeeded() error {
	now := time.Now()
	newDate := now.Format("2006-01-02")

	// Check if we need to rotate (new day or no current file)
	if w.currentFile == nil || w.currentDate != newDate {
		return w.rotate(newDate)
	}

	return nil
}

func (w *Writer) rotate(newDate string) error {
	// Close current file if open.
	if w.currentFile != nil {
		if err := w.currentFile.Close(); err != nil {
			return fmt.Errorf("failed to close current log file: %w", err)
		}
	}

	// Create new log file.
	filename := fmt.Sprintf("events-%s.jsonl", newDate)
	filepath := filepath.Join(w.logDir, filename)

	file, err := os.OpenFile(filepath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file %s: %w", filepath, err)
	}

	w.currentFile = file
	w.currentDate = newDate

	return nil
}

// Close closes the current log file and cleans up resources.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentFile != nil {
		err := w.currentFile.Close()
		w.currentFile = nil
		if err != nil {
			return fmt.Errorf("failed to close event log file: %w", err)
		}
	}

	return nil
}

// GetCurrentLogFile returns the path of the currently active log file.
func (w *Writer) GetCurrentLogFile() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentFile == nil {
		return ""
	}

	return filepath.Join(w.logDir, fmt.Sprintf("events-%s.jsonl", w.currentDate))
}

// ReadMessages reads and parses messages from a specific log file.
func ReadMessages(logFilePath string) ([]*proto.AgentMsg, error) {
	data, err := os.ReadFile(logFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read log file: %w", err)
	}

	if len(data) == 0 {
		return []*proto.AgentMsg{}, nil
	}

	// Split by newlines to get individual JSON records.
	lines := []byte{}
	var messages []*proto.AgentMsg

	for _, b := range data {
		if b == '\n' {
			if len(lines) > 0 {
				msg, err := proto.FromJSON(lines)
				if err != nil {
					return nil, fmt.Errorf("failed to parse message: %w", err)
				}
				messages = append(messages, msg)
				lines = []byte{}
			}
		} else {
			lines = append(lines, b)
		}
	}

	// Handle last line if no trailing newline.
	if len(lines) > 0 {
		msg, err := proto.FromJSON(lines)
		if err != nil {
			return nil, fmt.Errorf("failed to parse final message: %w", err)
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

// ListLogFiles returns all event log files in the log directory.
func ListLogFiles(logDir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(logDir, "events-*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("failed to list log files: %w", err)
	}

	return files, nil
}
