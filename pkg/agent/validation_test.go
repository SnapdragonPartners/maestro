package agent

import (
	"strings"
	"testing"
)

func TestValidateMessage(t *testing.T) {
	tests := []struct {
		name    string
		msg     CompletionMessage
		wantErr bool
		errType string
	}{
		{
			name:    "valid user message",
			msg:     CompletionMessage{Role: RoleUser, Content: "Hello world"},
			wantErr: false,
		},
		{
			name:    "valid assistant message",
			msg:     CompletionMessage{Role: RoleAssistant, Content: "Hi there!"},
			wantErr: false,
		},
		{
			name:    "valid system message",
			msg:     CompletionMessage{Role: RoleSystem, Content: "You are a helpful assistant"},
			wantErr: false,
		},
		{
			name:    "empty role",
			msg:     CompletionMessage{Role: CompletionRole(""), Content: "Hello"},
			wantErr: true,
			errType: "role",
		},
		{
			name:    "invalid role",
			msg:     CompletionMessage{Role: CompletionRole("invalid"), Content: "Hello"},
			wantErr: true,
			errType: "role",
		},
		{
			name:    "empty content",
			msg:     CompletionMessage{Role: RoleUser, Content: ""},
			wantErr: true,
			errType: "content",
		},
		{
			name:    "whitespace-only content",
			msg:     CompletionMessage{Role: RoleUser, Content: "   \n\t  "},
			wantErr: true,
			errType: "content",
		},
		{
			name:    "malformed bracket prefix",
			msg:     CompletionMessage{Role: RoleUser, Content: "[] some content"},
			wantErr: true,
			errType: "content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMessage(tt.msg)

			if tt.wantErr && err == nil {
				t.Errorf("ValidateMessage() expected error but got none")
				return
			}

			if !tt.wantErr && err != nil {
				t.Errorf("ValidateMessage() unexpected error: %v", err)
				return
			}

			if tt.wantErr && err != nil {
				if !strings.Contains(err.Error(), tt.errType) {
					t.Errorf("ValidateMessage() error type mismatch, want '%s' in error: %v", tt.errType, err)
				}
			}
		})
	}
}

func TestValidateMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []CompletionMessage
		wantErr  bool
	}{
		{
			name: "valid messages",
			messages: []CompletionMessage{
				{Role: RoleUser, Content: "Hello"},
				{Role: RoleAssistant, Content: "Hi there!"},
			},
			wantErr: false,
		},
		{
			name:     "empty message slice",
			messages: []CompletionMessage{},
			wantErr:  true,
		},
		{
			name: "contains invalid message",
			messages: []CompletionMessage{
				{Role: RoleUser, Content: "Hello"},
				{Role: CompletionRole(""), Content: "Invalid"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMessages(tt.messages)

			if tt.wantErr && err == nil {
				t.Errorf("ValidateMessages() expected error but got none")
			}

			if !tt.wantErr && err != nil {
				t.Errorf("ValidateMessages() unexpected error: %v", err)
			}
		})
	}
}

func TestSanitizeMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    CompletionMessage
		expected CompletionMessage
	}{
		{
			name:     "clean message unchanged",
			input:    CompletionMessage{Role: RoleUser, Content: "Hello world"},
			expected: CompletionMessage{Role: RoleUser, Content: "Hello world"},
		},
		{
			name:     "empty role defaults to user",
			input:    CompletionMessage{Role: CompletionRole(""), Content: "Hello"},
			expected: CompletionMessage{Role: RoleUser, Content: "Hello"},
		},
		{
			name:     "invalid role defaults to user",
			input:    CompletionMessage{Role: CompletionRole("invalid"), Content: "Hello"},
			expected: CompletionMessage{Role: RoleUser, Content: "Hello"},
		},
		{
			name:     "empty content gets placeholder",
			input:    CompletionMessage{Role: RoleUser, Content: ""},
			expected: CompletionMessage{Role: RoleUser, Content: "(empty message)"},
		},
		{
			name:     "malformed bracket prefix removed",
			input:    CompletionMessage{Role: RoleUser, Content: "[] some content"},
			expected: CompletionMessage{Role: RoleUser, Content: "some content"},
		},
		{
			name:     "assistant bracket prefix removed",
			input:    CompletionMessage{Role: RoleUser, Content: "[assistant] some content"},
			expected: CompletionMessage{Role: RoleUser, Content: "some content"},
		},
		{
			name:     "whitespace trimmed",
			input:    CompletionMessage{Role: CompletionRole("  user  "), Content: "  hello  "},
			expected: CompletionMessage{Role: RoleUser, Content: "hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeMessage(tt.input)

			if result.Role != tt.expected.Role {
				t.Errorf("SanitizeMessage() role = %v, want %v", result.Role, tt.expected.Role)
			}

			if result.Content != tt.expected.Content {
				t.Errorf("SanitizeMessage() content = %v, want %v", result.Content, tt.expected.Content)
			}
		})
	}
}

func TestValidateAndSanitizeMessages(t *testing.T) {
	tests := []struct {
		name     string
		input    []CompletionMessage
		wantErr  bool
		validate func([]CompletionMessage) bool
	}{
		{
			name: "clean messages pass through",
			input: []CompletionMessage{
				{Role: RoleUser, Content: "Hello"},
				{Role: RoleAssistant, Content: "Hi"},
			},
			wantErr: false,
			validate: func(msgs []CompletionMessage) bool {
				return len(msgs) == 2 &&
					msgs[0].Role == RoleUser && msgs[0].Content == "Hello" &&
					msgs[1].Role == RoleAssistant && msgs[1].Content == "Hi"
			},
		},
		{
			name: "malformed messages get sanitized",
			input: []CompletionMessage{
				{Role: CompletionRole(""), Content: "[] Hello"},
				{Role: CompletionRole("invalid"), Content: "[assistant] Hi"},
			},
			wantErr: false,
			validate: func(msgs []CompletionMessage) bool {
				return len(msgs) == 2 &&
					msgs[0].Role == RoleUser && msgs[0].Content == "Hello" &&
					msgs[1].Role == RoleUser && msgs[1].Content == "Hi"
			},
		},
		{
			name:    "empty slice fails",
			input:   []CompletionMessage{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ValidateAndSanitizeMessages(tt.input)

			if tt.wantErr && err == nil {
				t.Errorf("ValidateAndSanitizeMessages() expected error but got none")
				return
			}

			if !tt.wantErr && err != nil {
				t.Errorf("ValidateAndSanitizeMessages() unexpected error: %v", err)
				return
			}

			if !tt.wantErr && tt.validate != nil {
				if !tt.validate(result) {
					t.Errorf("ValidateAndSanitizeMessages() result validation failed")
				}
			}
		})
	}
}
