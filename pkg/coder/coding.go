package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// handleCoding processes the CODING state with priority-based work handling.
func (c *Coder) handleCoding(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Check for merge conflict (highest priority).
	if conflictData, exists := sm.GetStateValue(KeyMergeConflictDetails); exists {
		c.logger.Info("🧑‍💻 Handling merge conflict in CODING state")
		return c.handleMergeConflictCoding(ctx, sm, conflictData)
	}

	// Check for code review feedback (second priority).
	if reviewData, exists := sm.GetStateValue(KeyCodeReviewRejectionFeedback); exists {
		c.logger.Info("🧑‍💻 Handling code review feedback in CODING state")
		return c.handleCodeReviewCoding(ctx, sm, reviewData)
	}

	// Check for test failures (third priority).
	if testData, exists := sm.GetStateValue(KeyTestFailureOutput); exists {
		c.logger.Info("🧑‍💻 Handling test failures in CODING state")
		return c.handleTestFixCoding(ctx, sm, testData)
	}

	// Default: Continue with initial coding.
	return c.handleInitialCoding(ctx, sm)
}

// handleInitialCoding handles the main coding workflow.
func (c *Coder) handleInitialCoding(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	const maxCodingIterations = 8
	if c.checkLoopBudget(sm, string(stateDataKeyCodingIterations), maxCodingIterations, StateCoding) {
		c.logger.Info("Coding budget exceeded, proceeding to testing")
		return StateTesting, false, nil
	}

	// Continue coding with the main template.
	return c.executeCodingWithTemplate(ctx, sm, map[string]any{
		"scenario": "initial_coding",
		"message":  "Continue with code implementation based on your plan",
	})
}

// handleMergeConflictCoding handles merge conflict resolution during coding.
func (c *Coder) handleMergeConflictCoding(ctx context.Context, sm *agent.BaseStateMachine, conflictData any) (proto.State, bool, error) {
	// Clear merge conflict data after handling.
	sm.SetStateData(KeyMergeConflictDetails, nil)

	// Execute coding with merge conflict context.
	return c.executeCodingWithTemplate(ctx, sm, map[string]any{
		"scenario":      "merge_conflict",
		"conflict_data": conflictData,
		"message":       "Resolve the merge conflicts and continue implementation",
	})
}

// handleCodeReviewCoding handles code review feedback during coding.
func (c *Coder) handleCodeReviewCoding(ctx context.Context, sm *agent.BaseStateMachine, reviewData any) (proto.State, bool, error) {
	// Clear review feedback data after handling.
	sm.SetStateData(KeyCodeReviewRejectionFeedback, nil)

	// Execute coding with review feedback context.
	return c.executeCodingWithTemplate(ctx, sm, map[string]any{
		"scenario":    "code_review_feedback",
		"review_data": reviewData,
		"message":     "Address the code review feedback and continue implementation",
	})
}

// handleTestFixCoding handles test failure fixes during coding.
func (c *Coder) handleTestFixCoding(ctx context.Context, sm *agent.BaseStateMachine, testData any) (proto.State, bool, error) {
	// Clear test failure data after handling.
	sm.SetStateData(KeyTestFailureOutput, nil)

	// Execute coding with test failure context.
	return c.executeCodingWithTemplate(ctx, sm, map[string]any{
		"scenario":  "test_failures",
		"test_data": testData,
		"message":   "Fix the test failures and continue implementation",
	})
}

// executeCodingWithTemplate is the shared implementation for all coding scenarios.
func (c *Coder) executeCodingWithTemplate(ctx context.Context, sm *agent.BaseStateMachine, templateData map[string]any) (proto.State, bool, error) {
	logx.DebugState(ctx, "coder", "enter", string(StateCoding))

	// Get story type for template selection
	storyType := utils.GetStateValueOr[string](sm, proto.KeyStoryType, string(proto.StoryTypeApp))

	// Create ToolProvider for this coding session
	if c.codingToolProvider == nil {
		c.codingToolProvider = c.createCodingToolProvider(storyType)
		c.logger.Debug("Created coding ToolProvider for story type: %s", storyType)
	}

	// Select appropriate coding template based on story type
	var codingTemplate templates.StateTemplate
	if storyType == string(proto.StoryTypeDevOps) {
		codingTemplate = templates.DevOpsCodingTemplate
	} else {
		codingTemplate = templates.AppCodingTemplate
	}

	// Get task content.
	taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")

	// Create enhanced template data with state-specific tool documentation.
	enhancedTemplateData := &templates.TemplateData{
		TaskContent:       taskContent,
		WorkDir:           c.workDir,
		ToolDocumentation: c.codingToolProvider.GenerateToolDocumentation(),
		Extra: map[string]any{
			"story_type": storyType, // Include story type for template logic
		},
	}

	// Merge in additional template data from caller.
	for key, value := range templateData {
		enhancedTemplateData.Extra[key] = value
	}

	// Render enhanced coding template.
	if c.renderer == nil {
		return proto.StateError, false, logx.Errorf("template renderer not available")
	}
	prompt, err := c.renderer.RenderWithUserInstructions(codingTemplate, enhancedTemplateData, c.workDir, "CODER")
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to render coding template")
	}

	// Log the rendered prompt for debugging
	c.logger.Info("🧑‍💻 Starting coding phase for story_type '%s'", storyType)

	// Get LLM response with MCP tool support.
	// Build messages starting with the coding prompt.
	messages := c.buildMessagesWithContext(prompt)

	req := agent.CompletionRequest{
		Messages:  messages,
		MaxTokens: 8192,                     // Increased for comprehensive code generation
		Tools:     c.getCodingToolsForLLM(), // Use state-specific tools
	}

	// Use base agent retry mechanism.
	resp, llmErr := c.llmClient.Complete(ctx, req)
	if llmErr != nil {
		return proto.StateError, false, logx.Wrap(llmErr, "failed to get LLM coding response")
	}

	if resp.Content == "" && len(resp.ToolCalls) == 0 {
		c.logEmptyLLMResponse(prompt, req)
		return proto.StateError, false, logx.Errorf("empty response from Claude")
	}

	// Execute tool calls if any (MCP tools).
	var filesCreated int
	if len(resp.ToolCalls) > 0 {
		filesCreated = c.executeMCPToolCalls(ctx, sm, resp.ToolCalls)
	} else {
		// Fallback: Parse response content for code blocks (legacy approach).
		c.logger.Info("🧑‍💻 No tool calls found, attempting to parse code blocks from response")
		filesCreated = c.parseAndCreateFiles(resp.Content)
	}

	// Add assistant response to context.
	c.contextManager.AddMessage("assistant", resp.Content)

	// Check for implementation completion.
	if c.isImplementationComplete(resp.Content, filesCreated, sm) {
		c.logger.Info("🧑‍💻 Implementation appears complete, proceeding to testing")
		return StateTesting, false, nil
	}

	// Continue in coding state for next iteration.
	c.logger.Info("🧑‍💻 Coding iteration completed, continuing in CODING for more work")
	return StateCoding, false, nil
}

// executeMCPToolCalls executes tool calls using the MCP tool system.
func (c *Coder) executeMCPToolCalls(ctx context.Context, sm *agent.BaseStateMachine, toolCalls []agent.ToolCall) int {
	filesCreated := 0
	c.logger.Info("🧑‍💻 Executing %d MCP tool calls", len(toolCalls))

	for i := range toolCalls {
		toolCall := &toolCalls[i]
		c.logger.Info("Executing MCP tool: %s", toolCall.Name)

		// Handle ask_question tool using Effects pattern.
		if toolCall.Name == tools.ToolAskQuestion {
			// Extract question details from tool arguments.
			question := utils.GetMapFieldOr[string](toolCall.Parameters, "question", "")
			context := utils.GetMapFieldOr[string](toolCall.Parameters, "context", "")
			urgency := utils.GetMapFieldOr[string](toolCall.Parameters, "urgency", "medium")

			if question == "" {
				c.logger.Error("Ask question tool called without question parameter")
				continue
			}

			// Store coding context before asking question.
			c.storeCodingContext(sm)

			// Create question effect
			eff := effect.NewQuestionEffect(question, context, urgency, string(StateCoding))

			c.logger.Info("🧑‍💻 Asking question")

			// Execute the question effect (blocks until answer received)
			result, err := c.ExecuteEffect(ctx, eff)
			if err != nil {
				c.logger.Error("🧑‍💻 Failed to get answer: %v", err)
				// Add error to context for LLM to handle
				c.addComprehensiveToolFailureToContext(*toolCall, err)
				continue
			}

			// Process the answer
			if questionResult, ok := result.(*effect.QuestionResult); ok {
				// Answer received from architect (logged to database only)

				// Add the Q&A to context so the LLM can see it
				qaContent := fmt.Sprintf("Question: %s\nAnswer: %s", question, questionResult.Answer)
				c.contextManager.AddMessage("user", qaContent)

				// Continue with coding using the answer
			} else {
				c.logger.Error("🧑‍💻 Invalid question result type: %T", result)
			}
		}

		// Get tool from ToolProvider and execute.
		tool, err := c.codingToolProvider.Get(toolCall.Name)
		if err != nil {
			c.logger.Error("Tool not found in ToolProvider: %s", toolCall.Name)
			// Add tool failure to context for LLM to react.
			c.addComprehensiveToolFailureToContext(*toolCall, err)
			continue
		}

		result, err := tool.Exec(ctx, toolCall.Parameters)
		if err != nil {
			// Tool execution failures are recoverable - add comprehensive error to context for LLM to react.
			c.logger.Info("Tool execution failed for %s: %v", toolCall.Name, err)
			c.addComprehensiveToolFailureToContext(*toolCall, err)
			continue // Continue processing other tool calls
		}

		// Track file creation for completion detection.
		// Note: Using shell commands or other tools to create files
		filesCreated++

		// Add tool execution results to context so Claude can see them.
		c.addToolResultToContext(*toolCall, result)
		c.logger.Info("MCP tool %s executed successfully", toolCall.Name)
	}

	return filesCreated
}

// isImplementationComplete checks if the current implementation appears complete.
func (c *Coder) isImplementationComplete(responseContent string, filesCreated int, sm *agent.BaseStateMachine) bool {
	// Extract todo from state machine for completion assessment.
	planTodos := utils.GetStateValueOr[[]any](sm, string(stateDataKeyPlanTodos), []any{})

	// Convert to string slice.
	todos := make([]string, 0, len(planTodos))
	for _, todo := range planTodos {
		if todoStr, ok := todo.(string); ok {
			todos = append(todos, todoStr)
		}
	}

	c.logger.Debug("🧑‍💻 Checking completion: %d files created, %d todos planned", filesCreated, len(todos))

	// Check if Claude explicitly indicates completion.
	completionIndicators := []string{
		"implementation is complete",
		"implementation is now complete",
		"all requirements have been implemented",
		"task is complete",
		"story is complete",
		"ready for testing",
		"proceed to testing",
		"implementation finished",
		"all todos completed",
		"all tasks completed",
		"nothing more to implement",
	}

	lowerResponse := strings.ToLower(responseContent)
	for _, indicator := range completionIndicators {
		if strings.Contains(lowerResponse, indicator) {
			c.logger.Info("🧑‍💻 Completion detected via explicit indicator: '%s'", indicator)
			return true
		}
	}

	// Check if sufficient work has been done (heuristic).
	if filesCreated >= 3 && len(todos) > 0 {
		// Check if most todos appear to be addressed in response.
		addressedCount := 0
		for _, todo := range todos {
			// Simple heuristic: check if key terms from todo appear in response.
			todoWords := strings.Fields(strings.ToLower(todo))
			for _, word := range todoWords {
				if len(word) > 3 && strings.Contains(lowerResponse, word) {
					addressedCount++
					break
				}
			}
		}

		completionRatio := float64(addressedCount) / float64(len(todos))
		if completionRatio >= 0.7 { // 70% of todos addressed
			c.logger.Info("🧑‍💻 Completion detected via heuristic: %d/%d todos addressed (%.1f%%), %d files created",
				addressedCount, len(todos), completionRatio*100, filesCreated)
			return true
		}
	}

	return false
}

// File parsing and creation utilities for legacy code block parsing

// isFilenameHeader checks if a line looks like a filename header.
func (c *Coder) isFilenameHeader(line string) bool {
	// Common patterns for filename headers.
	patterns := []string{
		`^#{1,6}\s+(.+\.(go|js|ts|py|java|cpp|h|c|rs|rb|php|swift|kt|scala|cs|dart|yaml|yml|json|xml|html|css|md|txt|sh|sql|Dockerfile|Makefile))`,
		`^File:\s*(.+)`,
		`^Filename:\s*(.+)`,
		`^\*\*(.+\.(go|js|ts|py|java|cpp|h|c|rs|rb|php|swift|kt|scala|cs|dart|yaml|yml|json|xml|html|css|md|txt|sh|sql|Dockerfile|Makefile))\*\*`,
	}

	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(pattern, line); matched {
			return true
		}
	}
	return false
}

// looksLikeCode performs heuristic analysis to determine if text looks like code.
func (c *Coder) looksLikeCode(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}

	// Programming language indicators.
	codeIndicators := []string{
		// Go
		"package ", "func ", "import ", "type ", "var ", "const ", "defer ", "go ", "chan ", "select {",
		// JavaScript/TypeScript
		"function ", "const ", "let ", "var ", "import ", "export ", "class ", "{", "}", "=>",
		// Python
		"def ", "class ", "import ", "from ", "if __name__", "__init__", "self.", "return ",
		// Java
		"public class", "private ", "public ", "static ", "void ", "String ", "int ", "boolean ",
		// C/C++
		"#include", "int main", "printf", "struct ", "#define", "using namespace",
		// Rust
		"fn ", "let ", "mut ", "impl ", "struct ", "enum ", "use ", "mod ",
		// Common patterns
		"{", "}", "(", ")", ";", "//", "/*", "*/", "<!--", "-->",
	}

	for _, indicator := range codeIndicators {
		if strings.Contains(trimmed, indicator) {
			return true
		}
	}

	// Check for indentation patterns common in code.
	if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
		return true
	}

	// Check for assignment operators.
	assignmentOperators := []string{"=", "+=", "-=", "*=", "/=", ":=", "=>", "->"}
	for _, op := range assignmentOperators {
		if strings.Contains(trimmed, op) {
			return true
		}
	}

	return false
}

// guessFilenameFromContent attempts to guess filename from code content.
func (c *Coder) guessFilenameFromContent(line string) string {
	// Look for language-specific patterns.
	patterns := map[string]string{
		`package\s+main`:                      "main.go",
		`package\s+(\w+)`:                     "$1.go",
		`class\s+(\w+)`:                       "$1.java",
		`function\s+(\w+)`:                    "$1.js",
		`def\s+(\w+)`:                         "$1.py",
		`#include\s*<iostream>`:               "main.cpp",
		`#include\s*<stdio.h>`:                "main.c",
		`fn\s+main`:                           "main.rs",
		`impl\s+(\w+)`:                        "$1.rs",
		`struct\s+(\w+)`:                      "$1.h",
		`interface\s+(\w+)`:                   "$1.ts",
		`export\s+(default\s+)?class\s+(\w+)`: "$2.js",
	}

	for pattern, template := range patterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(line); matches != nil {
			if len(matches) > 1 {
				return strings.Replace(template, "$1", matches[1], 1)
			}
			return template
		}
	}

	return ""
}

// guessFilenameFromContext looks at surrounding lines for context clues.
func (c *Coder) guessFilenameFromContext(lines []string, startIdx int) string {
	// Look in a window around the start index for filename clues.
	start := startIdx - 5
	if start < 0 {
		start = 0
	}
	end := startIdx + 5
	if end > len(lines) {
		end = len(lines)
	}

	for i := start; i < end; i++ {
		if filename := c.guessFilenameFromContent(lines[i]); filename != "" {
			return filename
		}
	}

	return "untitled.txt"
}

// parseAndCreateFiles extracts code blocks from LLM response and creates files.
func (c *Coder) parseAndCreateFiles(content string) int {
	lines := strings.Split(content, "\n")
	filesCreated := 0

	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])

		// Skip empty lines.
		if line == "" {
			i++
			continue
		}

		// Check for filename header.
		if c.isFilenameHeader(line) {
			filename := c.extractFilename(line)
			if filename == "" {
				i++
				continue
			}

			// Look for code block start.
			i++
			var codeLines []string
			inCodeBlock := false

			// Skip to code block or start collecting code.
			for i < len(lines) {
				currentLine := lines[i]
				trimmedLine := strings.TrimSpace(currentLine)

				// Check for code block markers.
				if strings.HasPrefix(trimmedLine, "```") {
					if !inCodeBlock {
						inCodeBlock = true
						// Skip the opening marker.
						i++
						continue
					}
					// End of code block.
					break
				}

				// If in code block or line looks like code, collect it.
				if inCodeBlock || c.looksLikeCode(currentLine) {
					codeLines = append(codeLines, currentLine)
				} else if len(codeLines) > 0 {
					// End of code section.
					break
				}

				i++
			}

			// Create file if we have content.
			if len(codeLines) > 0 {
				fileContent := strings.Join(codeLines, "\n")
				if err := c.writeFile(filename, fileContent); err != nil {
					c.logger.Error("Failed to write file %s: %v", filename, err)
				} else {
					c.logger.Info("📝 Created file: %s (%d lines)", filename, len(codeLines))
					filesCreated++
				}
			}
		} else if strings.HasPrefix(line, "```") {
			// Standalone code block without explicit filename.
			filename := c.extractFilenameFromCodeBlock(line)
			if filename == "" {
				filename = c.guessFilenameFromContext(lines, i)
			}

			// Collect code block content.
			i++
			var codeLines []string
			for i < len(lines) {
				currentLine := lines[i]
				if strings.HasPrefix(strings.TrimSpace(currentLine), "```") {
					// End of code block.
					break
				}
				codeLines = append(codeLines, currentLine)
				i++
			}

			// Create file if we have content.
			if len(codeLines) > 0 {
				fileContent := strings.Join(codeLines, "\n")
				if err := c.writeFile(filename, fileContent); err != nil {
					c.logger.Error("Failed to write file %s: %v", filename, err)
				} else {
					c.logger.Info("📝 Created file: %s (%d lines)", filename, len(codeLines))
					filesCreated++
				}
			}
		}

		i++
	}

	return filesCreated
}

// extractFilename extracts filename from a header line.
func (c *Coder) extractFilename(line string) string {
	// Try different patterns to extract filename.
	patterns := []string{
		`^#{1,6}\s+(.+)`,    // Markdown headers
		`^File:\s*(.+)`,     // File: format
		`^Filename:\s*(.+)`, // Filename: format
		`^\*\*(.+)\*\*`,     // **filename**
		`^(.+\.(go|js|ts|py|java|cpp|h|c|rs|rb|php|swift|kt|scala|cs|dart|yaml|yml|json|xml|html|css|md|txt|sh|sql|Dockerfile|Makefile))`, // Direct filename
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(line); len(matches) > 1 {
			filename := strings.TrimSpace(matches[1])
			// Remove any remaining markdown formatting.
			filename = strings.Trim(filename, "*`\"'")
			return filename
		}
	}

	return ""
}

// extractFilenameFromCodeBlock extracts filename from code block marker.
func (c *Coder) extractFilenameFromCodeBlock(line string) string {
	// Look for patterns like ```go:main.go or ```javascript:app.js
	re := regexp.MustCompile(`^\s*` + "`" + `{3,}\s*\w*[:.](\S+)`)
	if matches := re.FindStringSubmatch(line); len(matches) > 1 {
		return matches[1]
	}

	// Look for language hints to guess extension.
	langMap := map[string]string{
		"go":         ".go",
		"javascript": ".js",
		"js":         ".js",
		"typescript": ".ts",
		"ts":         ".ts",
		"python":     ".py",
		"py":         ".py",
		"java":       ".java",
		"cpp":        ".cpp",
		"c":          ".c",
		"rust":       ".rs",
		"ruby":       ".rb",
		"php":        ".php",
		"swift":      ".swift",
		"kotlin":     ".kt",
		"scala":      ".scala",
		"csharp":     ".cs",
		"dart":       ".dart",
		"yaml":       ".yml",
		"json":       ".json",
		"xml":        ".xml",
		"html":       ".html",
		"css":        ".css",
		"markdown":   ".md",
		"shell":      ".sh",
		"bash":       ".sh",
		"sql":        ".sql",
		"dockerfile": "Dockerfile",
		"makefile":   "Makefile",
	}

	re2 := regexp.MustCompile(`^\s*` + "`" + `{3,}\s*(\w+)`)
	if matches := re2.FindStringSubmatch(line); len(matches) > 1 {
		lang := strings.ToLower(matches[1])
		if ext, exists := langMap[lang]; exists {
			if strings.HasPrefix(ext, ".") {
				return "main" + ext
			}
			return ext
		}
	}

	return ""
}

// writeFile writes content to the specified file.
func (c *Coder) writeFile(filename, content string) error {
	// Ensure filename is safe and within workspace.
	if strings.Contains(filename, "..") || filepath.IsAbs(filename) {
		return fmt.Errorf("unsafe filename: %s", filename)
	}

	// Create full path within workspace.
	fullPath := filepath.Join(c.workDir, filename)

	// Create directory if needed.
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write file.
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// handleCodingQuestionTransition - removed, now using single-phase approach via coding_question_pending flag

// storeCodingContext stores the current coding context.
func (c *Coder) storeCodingContext(sm *agent.BaseStateMachine) {
	context := map[string]any{
		"coding_progress": c.getCodingProgress(),
		KeyFilesCreated:   c.getFilesCreated(),
		"current_task":    c.getCurrentTask(),
		"timestamp":       time.Now().UTC(),
	}
	sm.SetStateData(KeyCodingContextSaved, context)
	c.logger.Debug("🧑‍💻 Stored coding context for QUESTION transition")
}

// Placeholder helper methods for coding context management (to be enhanced as needed).
func (c *Coder) getCodingProgress() any { return map[string]any{} }
func (c *Coder) getFilesCreated() any   { return []string{} }
func (c *Coder) getCurrentTask() any    { return map[string]any{} }
