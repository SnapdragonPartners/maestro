package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ToolCall represents a parsed MCP tool invocation in Claude API format.
//
//nolint:govet // fieldalignment: JSON parsing struct, logical order preferred
type ToolCall struct {
	ID    string         // Unique identifier for the tool call
	Name  string         // Tool name
	Args  map[string]any // Parsed arguments
	Input map[string]any // Raw input in Claude format
}

// AnthropicToolUse represents a tool_use block from Claude responses.
//
//nolint:govet // fieldalignment: JSON serialization order requirements
type AnthropicToolUse struct {
	Type  string         `json:"type"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ContentBlock represents a single content block in Claude responses.
//
//nolint:govet // fieldalignment: JSON serialization order requirements
type ContentBlock struct {
	Type    string            `json:"type"`
	Text    string            `json:"text,omitempty"`
	ToolUse *AnthropicToolUse `json:"tool_use,omitempty"`
}

// MCPParser handles parsing of Claude API tool use formats.
type MCPParser struct {
	// Regex to match thinking blocks which may contain tool reasoning.
	thinkingRegex *regexp.Regexp
	// Tool blocks can also be represented in JSON format for compatibility.
	jsonToolRegex *regexp.Regexp
	// Regex to match XML-style tool calls.
	xmlToolRegex *regexp.Regexp
}

// NewMCPParser creates a new MCP parser instance.
func NewMCPParser() *MCPParser {
	// Regex to match <thinking>...</thinking> patterns for CoT.
	thinkingRegex := regexp.MustCompile(`(?s)<thinking>(.*?)</thinking>`)

	// Regex to match JSON tool use blocks: {"type":"tool_use",...}
	jsonToolRegex := regexp.MustCompile(`(?s)\{\s*"type"\s*:\s*"tool_use".*?\}`)

	// Regex to match XML-style tool calls: <tool name="shell">command</tool>
	xmlToolRegex := regexp.MustCompile(`(?s)<tool\s+name="([^"]+)"[^>]*>(.*?)</tool>`)

	return &MCPParser{
		thinkingRegex: thinkingRegex,
		jsonToolRegex: jsonToolRegex,
		xmlToolRegex:  xmlToolRegex,
	}
}

// ParseToolCalls extracts tool calls from text containing Claude API tool_use blocks.
func (p *MCPParser) ParseToolCalls(text string) ([]ToolCall, error) {
	var toolCalls []ToolCall

	// Extract JSON tool use blocks.
	jsonMatches := p.jsonToolRegex.FindAllString(text, -1)
	for _, jsonStr := range jsonMatches {
		var contentBlock ContentBlock
		if err := json.Unmarshal([]byte(jsonStr), &contentBlock); err == nil {
			if contentBlock.Type == "tool_use" && contentBlock.ToolUse != nil {
				toolCall := ToolCall{
					ID:    contentBlock.ToolUse.ID,
					Name:  contentBlock.ToolUse.Name,
					Args:  contentBlock.ToolUse.Input, // For compatibility
					Input: contentBlock.ToolUse.Input,
				}
				toolCalls = append(toolCalls, toolCall)
			}
		}
	}

	// If no JSON tool blocks found, try XML format.
	if len(toolCalls) == 0 {
		xmlMatches := p.xmlToolRegex.FindAllStringSubmatch(text, -1)
		for _, match := range xmlMatches {
			if len(match) >= 3 {
				toolName := match[1]
				toolContent := strings.TrimSpace(match[2])

				toolCall := ToolCall{
					ID:    generateToolCallID(),
					Name:  toolName,
					Args:  map[string]any{"cmd": toolContent},
					Input: map[string]any{"cmd": toolContent},
				}
				toolCalls = append(toolCalls, toolCall)
			}
		}
	}

	// If no XML tool blocks found, try a more forgiving approach to extract potential tool calls.
	if len(toolCalls) == 0 {
		// Try to find content blocks in a more liberal format.
		blocks := parseContentBlocks(text)
		for i := range blocks {
			block := &blocks[i]
			if block.Type == "tool_use" && block.ToolUse != nil {
				toolCall := ToolCall{
					ID:    block.ToolUse.ID,
					Name:  block.ToolUse.Name,
					Args:  block.ToolUse.Input,
					Input: block.ToolUse.Input,
				}
				toolCalls = append(toolCalls, toolCall)
			}
		}
	}

	return toolCalls, nil
}

// parseContentBlocks attempts to extract content blocks from text.
// This is a more flexible parser that can handle various formats.
func parseContentBlocks(text string) []ContentBlock {
	var blocks []ContentBlock

	// Try to parse the entire text as a JSON array of content blocks.
	var jsonBlocks []ContentBlock
	if strings.HasPrefix(strings.TrimSpace(text), "[") &&
		strings.HasSuffix(strings.TrimSpace(text), "]") {
		trimmed := strings.TrimSpace(text)
		if err := json.Unmarshal([]byte(trimmed), &jsonBlocks); err == nil {
			return jsonBlocks
		}
	}

	// Look for tool_use sections in a more flexible way.
	toolUseRegex := regexp.MustCompile(`(?s)(tool_use|"type"\s*:\s*"tool_use").*?(\{.*?\})`)
	matches := toolUseRegex.FindAllStringSubmatch(text, -1)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		jsonStr := match[2]
		var toolUse AnthropicToolUse

		// Try to parse as a complete tool_use object.
		if err := json.Unmarshal([]byte(jsonStr), &toolUse); err == nil {
			blocks = append(blocks, ContentBlock{
				Type:    "tool_use",
				ToolUse: &toolUse,
			})
			continue
		}

		// Try to extract just the input part.
		inputRegex := regexp.MustCompile(`"input"\s*:\s*(\{.*?\})`)
		inputMatch := inputRegex.FindStringSubmatch(jsonStr)
		if len(inputMatch) >= 2 {
			var input map[string]any
			if err := json.Unmarshal([]byte(inputMatch[1]), &input); err == nil {
				// Try to extract name.
				nameRegex := regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)
				nameMatch := nameRegex.FindStringSubmatch(jsonStr)
				name := ""
				if len(nameMatch) >= 2 {
					name = nameMatch[1]
				}

				// Try to extract ID.
				idRegex := regexp.MustCompile(`"id"\s*:\s*"([^"]+)"`)
				idMatch := idRegex.FindStringSubmatch(jsonStr)
				id := ""
				if len(idMatch) >= 2 {
					id = idMatch[1]
				}

				blocks = append(blocks, ContentBlock{
					Type: "tool_use",
					ToolUse: &AnthropicToolUse{
						Type:  "tool_use",
						ID:    id,
						Name:  name,
						Input: input,
					},
				})
			}
		}
	}

	return blocks
}

// generateToolCallID generates a unique ID for XML-format tool calls.
func generateToolCallID() string {
	return fmt.Sprintf("xml_tool_%d", time.Now().UnixNano())
}

// HasToolCalls checks if the text contains any tool calls.
func (p *MCPParser) HasToolCalls(text string) bool {
	// Check for JSON tool use blocks.
	if p.jsonToolRegex.MatchString(text) {
		return true
	}

	// Check for XML tool blocks.
	if p.xmlToolRegex.MatchString(text) {
		return true
	}

	// Check for tool_use keyword.
	return strings.Contains(text, "tool_use") ||
		strings.Contains(text, "\"type\": \"tool_use\"") ||
		strings.Contains(text, "\"type\":\"tool_use\"")
}

// ExtractToolNames returns just the tool names found in the text.
func (p *MCPParser) ExtractToolNames(text string) []string {
	toolCalls, err := p.ParseToolCalls(text)
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(toolCalls))
	for i := range toolCalls {
		names = append(names, toolCalls[i].Name)
	}

	return names
}

// FormatToolResult creates a properly formatted tool result for Claude.
func FormatToolResult(toolUseID string, content any) (map[string]any, error) {
	return map[string]any{
		"type":        "tool_result",
		"tool_use_id": toolUseID,
		"content":     content,
	}, nil
}

// Global parser instance.
var globalParser = NewMCPParser() //nolint:gochecknoglobals

// ParseToolCalls is a convenience function using the global parser.
func ParseToolCalls(text string) ([]ToolCall, error) {
	return globalParser.ParseToolCalls(text)
}

// HasToolCalls is a convenience function using the global parser.
func HasToolCalls(text string) bool {
	return globalParser.HasToolCalls(text)
}

// ExtractToolNames is a convenience function using the global parser.
func ExtractToolNames(text string) []string {
	return globalParser.ExtractToolNames(text)
}

// FormatToolResultGlobal is a convenience function to format tool results.
func FormatToolResultGlobal(toolUseID string, content any) (map[string]any, error) {
	return FormatToolResult(toolUseID, content)
}
