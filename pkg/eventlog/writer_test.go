package eventlog

import (
	"os"
	"path/filepath"
	"testing"

	"orchestrator/pkg/proto"
)

func TestNewWriter(t *testing.T) {
	tmpDir := t.TempDir()

	writer, err := NewWriter(tmpDir, 24)
	if err != nil {
		t.Fatalf("Failed to create writer: %v", err)
	}
	defer writer.Close()

	// Check that log directory was created.
	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		t.Error("Log directory was not created")
	}

	// Check that current log file exists.
	currentFile := writer.GetCurrentLogFile()
	if currentFile == "" {
		t.Error("No current log file set")
	}

	if _, err := os.Stat(currentFile); os.IsNotExist(err) {
		t.Error("Current log file does not exist")
	}
}

func TestWriteMessage(t *testing.T) {
	tmpDir := t.TempDir()

	writer, err := NewWriter(tmpDir, 24)
	if err != nil {
		t.Fatalf("Failed to create writer: %v", err)
	}
	defer writer.Close()

	// Create test message.
	msg := proto.NewAgentMsg(proto.MsgTypeSTORY, "architect", "claude")
	msg.SetPayload("story_id", "001")
	msg.SetPayload("content", "Implement health endpoint")
	msg.SetMetadata("priority", "high")

	// Write message.
	err = writer.WriteMessage(msg)
	if err != nil {
		t.Fatalf("Failed to write message: %v", err)
	}

	// Verify file was written.
	currentFile := writer.GetCurrentLogFile()
	data, err := os.ReadFile(currentFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if len(data) == 0 {
		t.Error("Log file is empty")
	}

	// Verify it's valid JSON with newline.
	if data[len(data)-1] != '\n' {
		t.Error("Log line should end with newline")
	}
}

func TestWriteMultipleMessages(t *testing.T) {
	tmpDir := t.TempDir()

	writer, err := NewWriter(tmpDir, 24)
	if err != nil {
		t.Fatalf("Failed to create writer: %v", err)
	}
	defer writer.Close()

	// Write multiple messages.
	messages := []*proto.AgentMsg{
		proto.NewAgentMsg(proto.MsgTypeSTORY, "architect", "claude"),
		proto.NewAgentMsg(proto.MsgTypeRESULT, "claude", "architect"),
		proto.NewAgentMsg(proto.MsgTypeERROR, "claude", "architect"),
	}

	for i, msg := range messages {
		msg.SetPayload("sequence", i)
		err = writer.WriteMessage(msg)
		if err != nil {
			t.Fatalf("Failed to write message %d: %v", i, err)
		}
	}

	// Read back and verify.
	currentFile := writer.GetCurrentLogFile()
	readMessages, err := ReadMessages(currentFile)
	if err != nil {
		t.Fatalf("Failed to read messages: %v", err)
	}

	if len(readMessages) != len(messages) {
		t.Errorf("Expected %d messages, got %d", len(messages), len(readMessages))
	}

	// Verify message content.
	for i, readMsg := range readMessages {
		originalSeq, _ := messages[i].GetPayload("sequence")
		readSeq, _ := readMsg.GetPayload("sequence")

		// Convert to float64 for comparison (JSON unmarshaling converts numbers to float64)
		origSeqFloat, origOk := originalSeq.(int)
		readSeqFloat, readOk := readSeq.(float64)

		if !origOk || !readOk || float64(origSeqFloat) != readSeqFloat {
			t.Errorf("Message %d sequence mismatch: expected %v (%T), got %v (%T)", i, originalSeq, originalSeq, readSeq, readSeq)
		}

		if readMsg.Type != messages[i].Type {
			t.Errorf("Message %d type mismatch: expected %s, got %s", i, messages[i].Type, readMsg.Type)
		}
	}
}

func TestDailyRotation(t *testing.T) {
	tmpDir := t.TempDir()

	writer, err := NewWriter(tmpDir, 24)
	if err != nil {
		t.Fatalf("Failed to create writer: %v", err)
	}
	defer writer.Close()

	// Write a message to the initial file.
	msg1 := proto.NewAgentMsg(proto.MsgTypeSTORY, "architect", "claude")
	msg1.SetPayload("day", "today")

	err = writer.WriteMessage(msg1)
	if err != nil {
		t.Fatalf("Failed to write first message: %v", err)
	}

	// Get initial file after write.
	initialFile := writer.GetCurrentLogFile()

	// Manually rotate to a different date.
	writer.mu.Lock()
	err = writer.rotate("2025-12-25") // Christmas day
	writer.mu.Unlock()

	if err != nil {
		t.Fatalf("Failed to manually rotate: %v", err)
	}

	// Write another message directly to test rotation behavior.
	msg2 := proto.NewAgentMsg(proto.MsgTypeRESULT, "claude", "architect")
	msg2.SetPayload("day", "christmas")

	// Write directly without going through WriteMessage to avoid auto-rotation.
	writer.mu.Lock()
	jsonData, err := msg2.ToJSON()
	if err != nil {
		writer.mu.Unlock()
		t.Fatalf("Failed to serialize message: %v", err)
	}

	_, err = writer.currentFile.Write(jsonData)
	if err != nil {
		writer.mu.Unlock()
		t.Fatalf("Failed to write message: %v", err)
	}

	_, err = writer.currentFile.WriteString("\n")
	if err != nil {
		writer.mu.Unlock()
		t.Fatalf("Failed to write newline: %v", err)
	}

	err = writer.currentFile.Sync()
	writer.mu.Unlock()
	if err != nil {
		t.Fatalf("Failed to sync file: %v", err)
	}

	// Check that file rotated.
	newFile := writer.GetCurrentLogFile()
	if initialFile == newFile {
		t.Errorf("Expected file to rotate from %s, but still using same file", initialFile)
	}

	// Verify original file still exists and has first message.
	originalMessages, err := ReadMessages(initialFile)
	if err != nil {
		t.Fatalf("Failed to read original file: %v", err)
	}

	if len(originalMessages) != 1 {
		t.Errorf("Expected 1 message in original file, got %d", len(originalMessages))
	}

	originalDay, _ := originalMessages[0].GetPayload("day")
	if originalDay != "today" {
		t.Errorf("Expected 'today' in original file, got %v", originalDay)
	}

	// Verify new file has second message.
	newMessages, err := ReadMessages(newFile)
	if err != nil {
		t.Fatalf("Failed to read new file: %v", err)
	}

	if len(newMessages) != 1 {
		t.Errorf("Expected 1 message in new file, got %d", len(newMessages))
	}

	newDay, _ := newMessages[0].GetPayload("day")
	if newDay != "christmas" {
		t.Errorf("Expected 'christmas' in new file, got %v", newDay)
	}
}

func TestReadMessages(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test log file manually.
	logFile := filepath.Join(tmpDir, "test-events.jsonl")

	// Create test messages.
	msg1 := proto.NewAgentMsg(proto.MsgTypeSTORY, "architect", "claude")
	msg1.SetPayload("task", "test1")

	msg2 := proto.NewAgentMsg(proto.MsgTypeRESULT, "claude", "architect")
	msg2.SetPayload("result", "success")

	// Write manually to file.
	file, err := os.Create(logFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	json1, _ := msg1.ToJSON()
	json2, _ := msg2.ToJSON()

	file.Write(json1)
	file.WriteString("\n")
	file.Write(json2)
	file.WriteString("\n")
	file.Close()

	// Read back.
	messages, err := ReadMessages(logFile)
	if err != nil {
		t.Fatalf("Failed to read messages: %v", err)
	}

	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}

	// Verify first message.
	task, exists := messages[0].GetPayload("task")
	if !exists || task != "test1" {
		t.Errorf("Expected task 'test1', got %v", task)
	}

	// Verify second message.
	result, exists := messages[1].GetPayload("result")
	if !exists || result != "success" {
		t.Errorf("Expected result 'success', got %v", result)
	}
}

func TestReadEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "empty.jsonl")

	// Create empty file.
	file, err := os.Create(logFile)
	if err != nil {
		t.Fatalf("Failed to create empty file: %v", err)
	}
	file.Close()

	messages, err := ReadMessages(logFile)
	if err != nil {
		t.Fatalf("Failed to read empty file: %v", err)
	}

	if len(messages) != 0 {
		t.Errorf("Expected 0 messages from empty file, got %d", len(messages))
	}
}

func TestListLogFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create some test log files.
	testFiles := []string{
		"events-2025-01-01.jsonl",
		"events-2025-01-02.jsonl",
		"events-2025-01-03.jsonl",
		"other-file.txt", // Should be ignored
	}

	for _, filename := range testFiles {
		filePath := filepath.Join(tmpDir, filename)
		file, err := os.Create(filePath)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", filename, err)
		}
		file.Close()
	}

	// List log files.
	logFiles, err := ListLogFiles(tmpDir)
	if err != nil {
		t.Fatalf("Failed to list log files: %v", err)
	}

	// Should find 3 event log files (not the .txt file)
	if len(logFiles) != 3 {
		t.Errorf("Expected 3 log files, got %d", len(logFiles))
	}

	// Verify all files match the pattern.
	for _, file := range logFiles {
		filename := filepath.Base(file)
		matched, err := filepath.Match("events-*.jsonl", filename)
		if err != nil {
			t.Fatalf("Failed to match pattern: %v", err)
		}
		if !matched {
			t.Errorf("File %s doesn't match expected pattern", filename)
		}
	}
}

func TestWriterClose(t *testing.T) {
	tmpDir := t.TempDir()

	writer, err := NewWriter(tmpDir, 24)
	if err != nil {
		t.Fatalf("Failed to create writer: %v", err)
	}

	// Write a message.
	msg := proto.NewAgentMsg(proto.MsgTypeSTORY, "test", "test")
	err = writer.WriteMessage(msg)
	if err != nil {
		t.Fatalf("Failed to write message: %v", err)
	}

	// Close writer.
	err = writer.Close()
	if err != nil {
		t.Fatalf("Failed to close writer: %v", err)
	}

	// Verify writer is closed.
	if writer.currentFile != nil {
		t.Error("Expected current file to be nil after close")
	}

	// Try to write after close (should work because it creates a new file)
	err = writer.WriteMessage(msg)
	if err != nil {
		t.Fatalf("Writing after close should work by creating new file, but got error: %v", err)
	}
}

func TestConcurrentWrites(t *testing.T) {
	tmpDir := t.TempDir()

	writer, err := NewWriter(tmpDir, 24)
	if err != nil {
		t.Fatalf("Failed to create writer: %v", err)
	}
	defer writer.Close()

	// Write messages concurrently.
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			msg := proto.NewAgentMsg(proto.MsgTypeSTORY, "test", "test")
			msg.SetPayload("id", id)

			writeErr := writer.WriteMessage(msg)
			if writeErr != nil {
				t.Errorf("Failed to write message %d: %v", id, writeErr)
			}

			done <- true
		}(i)
	}

	// Wait for all writes to complete.
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all messages were written.
	currentFile := writer.GetCurrentLogFile()
	messages, err := ReadMessages(currentFile)
	if err != nil {
		t.Fatalf("Failed to read messages: %v", err)
	}

	if len(messages) != 10 {
		t.Errorf("Expected 10 messages, got %d", len(messages))
	}
}
