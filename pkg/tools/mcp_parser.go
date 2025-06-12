package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ToolCall represents a parsed MCP tool invocation
type ToolCall struct {
	Name    string
	Args    map[string]any
	RawArgs string
}

// MCPParser handles parsing of MCP tool invocation tags
type MCPParser struct {
	toolTagRegex *regexp.Regexp
}

// NewMCPParser creates a new MCP parser instance
func NewMCPParser() *MCPParser {
	// Regex to match <tool name="toolname">...</tool> patterns
	// Use (?s) flag to make . match newlines
	toolTagRegex := regexp.MustCompile(`(?s)<tool\s+name="([^"]+)"[^>]*>(.*?)</tool>`)

	return &MCPParser{
		toolTagRegex: toolTagRegex,
	}
}

// ParseToolCalls extracts tool calls from text containing MCP tags
func (p *MCPParser) ParseToolCalls(text string) ([]ToolCall, error) {
	matches := p.toolTagRegex.FindAllStringSubmatch(text, -1)

	var toolCalls []ToolCall
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		toolName := match[1]
		rawArgs := strings.TrimSpace(match[2])

		// For now, just store the raw args - more sophisticated parsing can be added later
		toolCall := ToolCall{
			Name:    toolName,
			Args:    make(map[string]any),
			RawArgs: rawArgs,
		}

		// Basic argument parsing - try to parse simple key=value pairs
		if err := p.parseBasicArgs(rawArgs, toolCall.Args); err != nil {
			return nil, fmt.Errorf("failed to parse args for tool %s: %w", toolName, err)
		}

		toolCalls = append(toolCalls, toolCall)
	}

	return toolCalls, nil
}

// parseBasicArgs performs JSON or simple key=value argument parsing
func (p *MCPParser) parseBasicArgs(rawArgs string, args map[string]any) error {
	if rawArgs == "" {
		return nil
	}

	// Try to parse as JSON first
	if strings.HasPrefix(strings.TrimSpace(rawArgs), "{") {
		var jsonArgs map[string]any
		if err := json.Unmarshal([]byte(rawArgs), &jsonArgs); err == nil {
			// Successfully parsed as JSON
			for k, v := range jsonArgs {
				args[k] = v
			}
			return nil
		}
	}

	// Fallback: treat the entire raw args as the "cmd" argument for backward compatibility
	args["cmd"] = rawArgs

	return nil
}

// HasToolCalls checks if the text contains any tool invocation tags
func (p *MCPParser) HasToolCalls(text string) bool {
	return p.toolTagRegex.MatchString(text)
}

// ExtractToolNames returns just the tool names found in the text
func (p *MCPParser) ExtractToolNames(text string) []string {
	matches := p.toolTagRegex.FindAllStringSubmatch(text, -1)

	var names []string
	for _, match := range matches {
		if len(match) >= 2 {
			names = append(names, match[1])
		}
	}

	return names
}

// Global parser instance
var globalParser = NewMCPParser()

// ParseToolCalls is a convenience function using the global parser
func ParseToolCalls(text string) ([]ToolCall, error) {
	return globalParser.ParseToolCalls(text)
}

// HasToolCalls is a convenience function using the global parser
func HasToolCalls(text string) bool {
	return globalParser.HasToolCalls(text)
}

// ExtractToolNames is a convenience function using the global parser
func ExtractToolNames(text string) []string {
	return globalParser.ExtractToolNames(text)
}
