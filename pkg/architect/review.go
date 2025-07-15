package architect

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

// ReviewEvaluator manages code review processing for the REVIEWING state
type ReviewEvaluator struct {
	llmClient         LLMClient
	renderer          *templates.Renderer
	queue             *Queue
	workspaceDir      string
	escalationHandler *EscalationHandler
	mergeCh           chan<- string // Channel to signal completed merges

	// Track pending reviews
	pendingReviews map[string]*PendingReview // reviewID -> PendingReview
}

// PendingReview represents a code submission awaiting review
type PendingReview struct {
	ID             string          `json:"id"`
	StoryID        string          `json:"story_id"`
	AgentID        string          `json:"agent_id"`
	CodePath       string          `json:"code_path"`
	CodeContent    string          `json:"code_content"`
	Context        map[string]any  `json:"context"`
	SubmittedAt    time.Time       `json:"submitted_at"`
	Status         string          `json:"status"` // "pending", "approved", "rejected", "needs_fixes", "escalated"
	ReviewNotes    string          `json:"review_notes,omitempty"`
	ReviewedAt     *time.Time      `json:"reviewed_at,omitempty"`
	ChecksRun      []string        `json:"checks_run,omitempty"`
	CheckResults   map[string]bool `json:"check_results,omitempty"`
	RejectionCount int             `json:"rejection_count"`          // Track number of rejections for 3-strikes rule
	ReviewHistory  []ReviewAttempt `json:"review_history,omitempty"` // Track all review attempts
}

// ReviewAttempt represents a single review attempt
type ReviewAttempt struct {
	AttemptNumber int       `json:"attempt_number"`
	ReviewedAt    time.Time `json:"reviewed_at"`
	Result        string    `json:"result"` // "approved", "needs_fixes"
	ReviewNotes   string    `json:"review_notes"`
	ChecksPassed  bool      `json:"checks_passed"`
}

// NewReviewEvaluator creates a new review evaluator
func NewReviewEvaluator(llmClient LLMClient, renderer *templates.Renderer, queue *Queue, workspaceDir string, escalationHandler *EscalationHandler, mergeCh chan<- string) *ReviewEvaluator {
	return &ReviewEvaluator{
		llmClient:         llmClient,
		renderer:          renderer,
		queue:             queue,
		workspaceDir:      workspaceDir,
		escalationHandler: escalationHandler,
		mergeCh:           mergeCh,
		pendingReviews:    make(map[string]*PendingReview),
	}
}

// HandleResult processes a RESULT message from a coding agent (code submission)
func (re *ReviewEvaluator) HandleResult(ctx context.Context, msg *proto.AgentMsg) error {
	// Extract submission details from message
	storyID, _ := msg.Payload["story_id"].(string)
	codePath, _ := msg.Payload["code_path"].(string)
	codeContent, _ := msg.Payload["code_content"].(string)

	if storyID == "" {
		return fmt.Errorf("invalid result message: missing story_id")
	}

	// Create pending review record
	pendingReview := &PendingReview{
		ID:           msg.ID, // Use message ID as review ID
		StoryID:      storyID,
		AgentID:      msg.FromAgent,
		CodePath:     codePath,
		CodeContent:  codeContent,
		Context:      make(map[string]any),
		SubmittedAt:  time.Now().UTC(),
		Status:       "pending",
		ChecksRun:    []string{},
		CheckResults: make(map[string]bool),
	}

	// Copy relevant context from message payload
	for key, value := range msg.Payload {
		if key != "story_id" && key != "code_path" && key != "code_content" {
			pendingReview.Context[key] = value
		}
	}

	// Store pending review
	re.pendingReviews[pendingReview.ID] = pendingReview

	// Start automated review process
	return re.performAutomatedReview(ctx, pendingReview)
}

// performAutomatedReview runs automated checks and LLM review
func (re *ReviewEvaluator) performAutomatedReview(ctx context.Context, pendingReview *PendingReview) error {
	fmt.Printf("🔍 Starting automated review for story %s (agent %s)\n",
		pendingReview.StoryID, pendingReview.AgentID)

	// Step 1: Run automated checks (formatting, linting, tests)
	checksPass, err := re.runAutomatedChecks(ctx, pendingReview)
	if err != nil {
		return fmt.Errorf("automated checks failed: %w", err)
	}

	// Step 2: If checks fail, generate feedback and request fixes
	if !checksPass {
		return re.requestCodeFixes(ctx, pendingReview)
	}

	// Step 3: Run LLM-based code review
	if re.llmClient != nil {
		err = re.performLLMReview(ctx, pendingReview)
		if err != nil {
			return fmt.Errorf("LLM review failed: %w", err)
		}
	} else {
		// Mock mode - approve automatically if checks pass
		return re.approveSubmission(ctx, pendingReview, "Mock approval - automated checks passed")
	}

	return nil
}

// runAutomatedChecks performs formatting, linting, and test checks
func (re *ReviewEvaluator) runAutomatedChecks(ctx context.Context, pendingReview *PendingReview) (bool, error) {
	checks := []string{"format", "lint", "test"}
	allPassed := true

	for _, check := range checks {
		passed, err := re.runSingleCheck(ctx, check, pendingReview)
		if err != nil {
			fmt.Printf("❌ Check %s failed with error: %v\n", check, err)
			pendingReview.CheckResults[check] = false
			allPassed = false
		} else {
			pendingReview.CheckResults[check] = passed
			if passed {
				fmt.Printf("✅ Check %s passed\n", check)
			} else {
				fmt.Printf("❌ Check %s failed\n", check)
				allPassed = false
			}
		}
		pendingReview.ChecksRun = append(pendingReview.ChecksRun, check)
	}

	return allPassed, nil
}

// runSingleCheck runs a specific automated check
func (re *ReviewEvaluator) runSingleCheck(ctx context.Context, checkType string, pendingReview *PendingReview) (bool, error) {
	// Get story context for workspace determination
	story, exists := re.queue.GetStory(pendingReview.StoryID)
	if !exists {
		return false, fmt.Errorf("story %s not found", pendingReview.StoryID)
	}

	// Determine workspace directory - use story-specific workspace if available
	workDir := re.workspaceDir
	if storyWorkspace, ok := pendingReview.Context["workspace_dir"].(string); ok && storyWorkspace != "" {
		workDir = storyWorkspace
	}

	switch checkType {
	case "format":
		return re.runFormatCheck(ctx, workDir, story)
	case "lint":
		return re.runLintCheck(ctx, workDir, story)
	case "test":
		return re.runTestCheck(ctx, workDir, story)
	default:
		return false, fmt.Errorf("unknown check type: %s", checkType)
	}
}

// runFormatCheck checks code formatting using language-agnostic make targets
func (re *ReviewEvaluator) runFormatCheck(ctx context.Context, workDir string, story *QueuedStory) (bool, error) {
	// Use LLM to determine and execute formatting commands
	if re.llmClient != nil {
		return re.runLLMToolInvocation(ctx, workDir, "format", story)
	}

	// Fallback to standard make targets for mock mode
	formatCommands := [][]string{
		{"make", "format"},
		{"make", "fmt"},
	}

	for _, cmdArgs := range formatCommands {
		if re.commandExists("make") && re.makeTargetExists(workDir, cmdArgs[1]) {
			cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
			cmd.Dir = workDir

			output, err := cmd.CombinedOutput()
			if err != nil {
				fmt.Printf("Format check failed: %s\n", string(output))
				return false, fmt.Errorf("format check failed: %s", string(output))
			}

			fmt.Printf("✅ Format check passed using %s\n", strings.Join(cmdArgs, " "))
			return true, nil
		}
	}

	// If no make targets available, warn and assume pass
	fmt.Printf("⚠️ No format make targets available, skipping format check\n")
	return true, nil
}

// runLintCheck runs linting checks using language-agnostic make targets
func (re *ReviewEvaluator) runLintCheck(ctx context.Context, workDir string, story *QueuedStory) (bool, error) {
	// Use LLM to determine and execute linting commands
	if re.llmClient != nil {
		return re.runLLMToolInvocation(ctx, workDir, "lint", story)
	}

	// Fallback to standard make targets for mock mode
	lintCommands := [][]string{
		{"make", "lint"},
		{"make", "check"},
	}

	for _, cmdArgs := range lintCommands {
		if re.commandExists("make") && re.makeTargetExists(workDir, cmdArgs[1]) {
			cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
			cmd.Dir = workDir

			output, err := cmd.CombinedOutput()
			if err != nil {
				fmt.Printf("Lint check failed: %s\n", string(output))
				return false, fmt.Errorf("lint issues found: %s", string(output))
			}

			fmt.Printf("✅ Lint check passed using %s\n", strings.Join(cmdArgs, " "))
			return true, nil
		}
	}

	// If no linting tools available, assume pass
	fmt.Printf("⚠️ No lint make targets available, skipping lint check\n")
	return true, nil
}

// runTestCheck runs tests using language-agnostic make targets
func (re *ReviewEvaluator) runTestCheck(ctx context.Context, workDir string, story *QueuedStory) (bool, error) {
	// Use LLM to determine and execute test commands
	if re.llmClient != nil {
		return re.runLLMToolInvocation(ctx, workDir, "test", story)
	}

	// Fallback to standard make targets for mock mode
	testCommands := [][]string{
		{"make", "test"},
		{"make", "tests"},
	}

	for _, cmdArgs := range testCommands {
		if re.commandExists("make") && re.makeTargetExists(workDir, cmdArgs[1]) {
			cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
			cmd.Dir = workDir

			output, err := cmd.CombinedOutput()
			if err != nil {
				fmt.Printf("Test check failed: %s\n", string(output))
				return false, fmt.Errorf("tests failed: %s", string(output))
			}

			fmt.Printf("✅ Test check passed using %s\n", strings.Join(cmdArgs, " "))
			return true, nil
		}
	}

	// If no test commands available, assume pass
	fmt.Printf("⚠️ No test make targets available, skipping test check\n")
	return true, nil
}

// commandExists checks if a command is available in PATH
func (re *ReviewEvaluator) commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

// makeTargetExists checks if a make target exists in the Makefile
func (re *ReviewEvaluator) makeTargetExists(workDir, target string) bool {
	cmd := exec.Command("make", "-n", target)
	cmd.Dir = workDir
	err := cmd.Run()
	return err == nil
}

// runLLMToolInvocation uses LLM to determine and execute appropriate development tools
func (re *ReviewEvaluator) runLLMToolInvocation(ctx context.Context, workDir, checkType string, story *QueuedStory) (bool, error) {
	// Prepare template data for tool invocation prompt
	templateData := &templates.TemplateData{
		TaskContent: fmt.Sprintf("Execute %s check for story %s", checkType, story.ID),
		Context:     re.formatToolInvocationContext(workDir, checkType, story),
		Extra: map[string]any{
			"check_type":    checkType,
			"workspace_dir": workDir,
			"story_id":      story.ID,
			"story_title":   story.Title,
		},
	}

	// Use code review template for automated checks
	prompt, err := re.renderer.Render(templates.CodeReviewTemplate, templateData)
	if err != nil {
		return false, fmt.Errorf("failed to render code review template: %w", err)
	}

	// Get LLM response with tool commands
	response, err := re.llmClient.GenerateResponse(ctx, prompt)
	if err != nil {
		return false, fmt.Errorf("failed to get LLM response for tool invocation: %w", err)
	}

	// Parse and execute the LLM's tool recommendations
	return re.executeLLMToolResponse(ctx, workDir, checkType, response)
}

// formatToolInvocationContext creates context for LLM tool invocation
func (re *ReviewEvaluator) formatToolInvocationContext(workDir, checkType string, story *QueuedStory) string {
	context := fmt.Sprintf(`Tool Invocation Context:
- Check Type: %s
- Workspace Directory: %s
- Story ID: %s
- Story Title: %s

Available Make Targets:`,
		checkType, workDir, story.ID, story.Title)

	// List available make targets
	targets := re.getAvailableMakeTargets(workDir)
	for _, target := range targets {
		context += fmt.Sprintf("\n- %s", target)
	}

	context += "\n\nProject Structure Detection:"
	// Add basic project structure info
	if re.fileExists(workDir + "/go.mod") {
		context += "\n- Go project detected (go.mod found)"
	}
	if re.fileExists(workDir + "/package.json") {
		context += "\n- Node.js project detected (package.json found)"
	}
	if re.fileExists(workDir+"/requirements.txt") || re.fileExists(workDir+"/pyproject.toml") {
		context += "\n- Python project detected"
	}
	if re.fileExists(workDir + "/Cargo.toml") {
		context += "\n- Rust project detected (Cargo.toml found)"
	}

	return context
}

// getAvailableMakeTargets lists available make targets
func (re *ReviewEvaluator) getAvailableMakeTargets(workDir string) []string {
	cmd := exec.Command("make", "-qp")
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return []string{"No Makefile found"}
	}

	// Parse make output to extract targets (simplified)
	lines := strings.Split(string(output), "\n")
	var targets []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, ":") && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, ".") {
			parts := strings.Split(line, ":")
			if len(parts) > 0 {
				target := strings.TrimSpace(parts[0])
				if target != "" && !strings.Contains(target, " ") {
					targets = append(targets, target)
				}
			}
		}
	}

	if len(targets) == 0 {
		return []string{"No targets found"}
	}

	return targets
}

// fileExists checks if a file exists
func (re *ReviewEvaluator) fileExists(path string) bool {
	_, err := exec.Command("test", "-f", path).Output()
	return err == nil
}

// executeLLMToolResponse parses and executes LLM tool recommendations
func (re *ReviewEvaluator) executeLLMToolResponse(ctx context.Context, workDir, checkType, response string) (bool, error) {
	// For now, simple parsing - in production this would parse structured JSON responses
	// Look for make commands in the response
	lines := strings.Split(response, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "make ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
				cmd.Dir = workDir

				output, err := cmd.CombinedOutput()
				if err != nil {
					fmt.Printf("LLM-recommended command failed: %s\nOutput: %s\n", line, string(output))
					return false, fmt.Errorf("LLM-recommended %s check failed: %s", checkType, string(output))
				}

				fmt.Printf("✅ LLM-recommended command succeeded: %s\n", line)
				return true, nil
			}
		}
	}

	// If no make commands found, fall back to default behavior
	fmt.Printf("⚠️ No executable commands found in LLM response, using fallback\n")
	return true, nil
}

// performLLMReview uses LLM to review the code submission
func (re *ReviewEvaluator) performLLMReview(ctx context.Context, pendingReview *PendingReview) error {
	// Get story context for better review
	story, exists := re.queue.GetStory(pendingReview.StoryID)
	if !exists {
		return fmt.Errorf("story %s not found in queue", pendingReview.StoryID)
	}

	// Prepare template data for code review prompt
	templateData := &templates.TemplateData{
		TaskContent: pendingReview.CodeContent,
		Context:     re.formatReviewContext(pendingReview, story),
		Extra: map[string]any{
			"story_id":           pendingReview.StoryID,
			"story_title":        story.Title,
			"agent_id":           pendingReview.AgentID,
			"review_id":          pendingReview.ID,
			"code_path":          pendingReview.CodePath,
			"checks_run":         pendingReview.ChecksRun,
			"check_results":      pendingReview.CheckResults,
			"submission_context": pendingReview.Context,
		},
	}

	// Render code review prompt template
	prompt, err := re.renderer.Render(templates.CodeReviewTemplate, templateData)
	if err != nil {
		return fmt.Errorf("failed to render code review template: %w", err)
	}

	// Get LLM response
	review, err := re.llmClient.GenerateResponse(ctx, prompt)
	if err != nil {
		return fmt.Errorf("failed to get LLM response for code review: %w", err)
	}

	// Parse LLM review response
	return re.processLLMReviewResponse(ctx, pendingReview, review)
}

// formatReviewContext creates a context string for the LLM review prompt
func (re *ReviewEvaluator) formatReviewContext(pendingReview *PendingReview, story *QueuedStory) string {
	context := fmt.Sprintf(`Code Review Context:
- Story ID: %s
- Story Title: %s
- Agent ID: %s
- Submitted At: %s
- Code Path: %s
- Rejection Count: %d/3 (escalates to human after 3 rejections)

Original Story Details:
- Status: %s
- Estimated Points: %d
- Dependencies: %v
- File Path: %s

Acceptance Requirements:
1. Meets story acceptance criteria as defined in the original story
2. Generally adheres to good coding practices and established patterns
3. Has high levels of test coverage (>80%% unless not feasible)
4. Doesn't change shared interfaces/design patterns without good reason
5. Is deemed "production-ready" with appropriate error handling and documentation

Automated Checks Results:`,
		pendingReview.StoryID,
		story.Title,
		pendingReview.AgentID,
		pendingReview.SubmittedAt.Format(time.RFC3339),
		pendingReview.CodePath,
		pendingReview.RejectionCount,
		story.Status,
		story.EstimatedPoints,
		story.DependsOn,
		story.FilePath,
	)

	// Add check results
	for _, check := range pendingReview.ChecksRun {
		result := "❌ FAILED"
		if passed, exists := pendingReview.CheckResults[check]; exists && passed {
			result = "✅ PASSED"
		}
		context += fmt.Sprintf("\n- %s: %s", check, result)
	}

	// Add review history if this is not the first attempt
	if len(pendingReview.ReviewHistory) > 0 {
		context += "\n\nPrevious Review History:"
		for _, attempt := range pendingReview.ReviewHistory {
			context += fmt.Sprintf("\nAttempt %d (%s): %s - %s",
				attempt.AttemptNumber,
				attempt.ReviewedAt.Format("2006-01-02 15:04:05"),
				attempt.Result,
				truncateString(attempt.ReviewNotes, 100))
		}
	}

	// Add any additional context from the submission
	if len(pendingReview.Context) > 0 {
		context += "\n\nSubmission Context:"
		for key, value := range pendingReview.Context {
			context += fmt.Sprintf("\n- %s: %v", key, value)
		}
	}

	return context
}

// processLLMReviewResponse processes the LLM's review response with 3-strikes rule
func (re *ReviewEvaluator) processLLMReviewResponse(ctx context.Context, pendingReview *PendingReview, review string) error {
	// Parse review response - in production this would parse structured JSON
	reviewLower := strings.ToLower(review)
	now := time.Now().UTC()

	// Create review attempt record
	attemptNumber := len(pendingReview.ReviewHistory) + 1
	checksPassed := re.allChecksPass(pendingReview.CheckResults)

	// Determine review result based on LLM response and acceptance criteria
	var result string
	var reviewNotes string

	if strings.Contains(reviewLower, "approved") || strings.Contains(reviewLower, "looks good") || strings.Contains(reviewLower, "lgtm") {
		result = "approved"
		reviewNotes = review
	} else {
		result = "needs_fixes"
		reviewNotes = review

		// Check 3-strikes rule before rejecting
		if pendingReview.RejectionCount >= 2 { // This would be the 3rd rejection
			return re.escalateToHuman(ctx, pendingReview, review)
		}
	}

	// Record this review attempt
	attempt := ReviewAttempt{
		AttemptNumber: attemptNumber,
		ReviewedAt:    now,
		Result:        result,
		ReviewNotes:   reviewNotes,
		ChecksPassed:  checksPassed,
	}
	pendingReview.ReviewHistory = append(pendingReview.ReviewHistory, attempt)

	// Process based on result
	if result == "approved" {
		return re.approveSubmission(ctx, pendingReview, review)
	} else {
		pendingReview.RejectionCount++
		return re.requestCodeFixes(ctx, pendingReview)
	}
}

// allChecksPass returns true if all automated checks passed
func (re *ReviewEvaluator) allChecksPass(checkResults map[string]bool) bool {
	for _, passed := range checkResults {
		if !passed {
			return false
		}
	}
	return len(checkResults) > 0 // At least one check must have run
}

// escalateToHuman escalates the review to human intervention after 3 strikes
func (re *ReviewEvaluator) escalateToHuman(ctx context.Context, pendingReview *PendingReview, review string) error {
	// Update review record
	now := time.Now().UTC()
	pendingReview.Status = "escalated"
	pendingReview.ReviewNotes = fmt.Sprintf("Escalated to human after 3 rejections. Latest review: %s", review)
	pendingReview.ReviewedAt = &now

	// Use the escalation handler to properly escalate the review failure
	if re.escalationHandler != nil {
		err := re.escalationHandler.EscalateReviewFailure(ctx, pendingReview.StoryID, pendingReview.AgentID, pendingReview.RejectionCount+1, review)
		if err != nil {
			return fmt.Errorf("failed to escalate review failure: %w", err)
		}
	} else {
		// Fallback: mark story as requiring human feedback
		err := re.queue.MarkAwaitHumanFeedback(pendingReview.StoryID)
		if err != nil {
			return fmt.Errorf("failed to mark story %s as awaiting human feedback: %w", pendingReview.StoryID, err)
		}

		fmt.Printf("🚨 Escalated story %s to human intervention after 3 rejections\n",
			pendingReview.StoryID)
	}

	// Send escalation message back to agent
	err := re.sendReviewResult(ctx, pendingReview, "ESCALATED")
	if err != nil {
		return fmt.Errorf("failed to send escalation message: %w", err)
	}

	return nil
}

// approveSubmission marks the code as approved (story completion happens after successful merge)
func (re *ReviewEvaluator) approveSubmission(ctx context.Context, pendingReview *PendingReview, reviewNotes string) error {
	// Update review record
	now := time.Now().UTC()
	pendingReview.Status = "approved"
	pendingReview.ReviewNotes = reviewNotes
	pendingReview.ReviewedAt = &now

	// Story completion is now deferred until after successful PR merge
	// This aligns with the new merge workflow where only successful merges mark stories as complete

	// Signal merge channel for completed story
	if re.mergeCh != nil {
		select {
		case re.mergeCh <- pendingReview.StoryID:
			// Successfully signaled merge
		default:
			// Channel full, log warning but don't fail
			fmt.Printf("⚠️ Warning: merge channel full for story %s\n", pendingReview.StoryID)
		}
	}

	// Send approval message back to agent
	err := re.sendReviewResult(ctx, pendingReview, proto.ApprovalStatusApproved.String())
	if err != nil {
		return fmt.Errorf("failed to send approval message: %w", err)
	}

	fmt.Printf("✅ Approved submission for story %s from agent %s\n",
		pendingReview.StoryID, pendingReview.AgentID)

	return nil
}

// requestCodeFixes sends feedback requesting code changes
func (re *ReviewEvaluator) requestCodeFixes(ctx context.Context, pendingReview *PendingReview) error {
	// Update review record
	now := time.Now().UTC()
	pendingReview.Status = "needs_fixes"
	pendingReview.ReviewedAt = &now

	// Generate feedback based on failed checks
	feedback := re.generateFixFeedback(pendingReview)
	pendingReview.ReviewNotes = feedback

	// Mark story as waiting_review (still needs work)
	err := re.queue.MarkWaitingReview(pendingReview.StoryID)
	if err != nil {
		return fmt.Errorf("failed to mark story %s as waiting review: %w", pendingReview.StoryID, err)
	}

	// Send feedback message back to agent
	err = re.sendReviewResult(ctx, pendingReview, "NEEDS_FIXES")
	if err != nil {
		return fmt.Errorf("failed to send feedback message: %w", err)
	}

	fmt.Printf("🔄 Requested fixes for story %s from agent %s\n",
		pendingReview.StoryID, pendingReview.AgentID)

	return nil
}

// generateFixFeedback creates feedback based on failed checks
func (re *ReviewEvaluator) generateFixFeedback(pendingReview *PendingReview) string {
	feedback := "Code review feedback:\n\n"

	// Add feedback for failed checks
	hasFailures := false
	for check, passed := range pendingReview.CheckResults {
		if !passed {
			hasFailures = true
			switch check {
			case "format":
				feedback += "• Code formatting issues found. Please run 'go fmt' or 'gofmt' to fix formatting.\n"
			case "lint":
				feedback += "• Linting issues found. Please address the warnings from 'golangci-lint' or 'go vet'.\n"
			case "test":
				feedback += "• Tests are failing. Please ensure all tests pass before resubmitting.\n"
			default:
				feedback += fmt.Sprintf("• %s check failed. Please review and fix the issues.\n", check)
			}
		}
	}

	if !hasFailures {
		feedback += "• Automated checks passed, but manual review identified issues that need attention.\n"
	}

	feedback += "\nPlease address these issues and resubmit your code."

	return feedback
}

// sendReviewResult sends the review result back to the agent
func (re *ReviewEvaluator) sendReviewResult(ctx context.Context, pendingReview *PendingReview, result string) error {
	// Create RESULT message
	resultMsg := proto.NewAgentMsg(
		proto.MsgTypeRESULT,
		"architect",           // from
		pendingReview.AgentID, // to
	)

	// Set parent message ID to link back to the original submission
	resultMsg.ParentMsgID = pendingReview.ID

	// Set review result payload
	resultMsg.Payload["review_id"] = pendingReview.ID
	resultMsg.Payload["story_id"] = pendingReview.StoryID
	resultMsg.Payload["review_result"] = result
	resultMsg.Payload["review_notes"] = pendingReview.ReviewNotes
	resultMsg.Payload["reviewed_at"] = pendingReview.ReviewedAt.Format(time.RFC3339)
	resultMsg.Payload["checks_run"] = pendingReview.ChecksRun
	resultMsg.Payload["check_results"] = pendingReview.CheckResults

	// Add metadata
	resultMsg.SetMetadata("review_type", "automated")
	resultMsg.SetMetadata("review_status", strings.ToLower(result))

	// Log the review result for debugging
	fmt.Printf("📋 Review result for story %s: %s\n",
		pendingReview.StoryID, result)

	// In a real implementation, this would be sent via the dispatcher
	// For now, we simulate sending the message
	fmt.Printf("📤 Sending review RESULT message to agent %s for story %s\n",
		pendingReview.AgentID, pendingReview.StoryID)

	return nil
}

// GetPendingReviews returns all pending reviews
func (re *ReviewEvaluator) GetPendingReviews() []*PendingReview {
	reviews := make([]*PendingReview, 0, len(re.pendingReviews))
	for _, review := range re.pendingReviews {
		reviews = append(reviews, review)
	}
	return reviews
}

// GetReviewStatus returns statistics about review processing
func (re *ReviewEvaluator) GetReviewStatus() *ReviewStatus {
	status := &ReviewStatus{
		TotalReviews:      len(re.pendingReviews),
		PendingReviews:    0,
		ApprovedReviews:   0,
		RejectedReviews:   0,
		NeedsFixesReviews: 0,
		Reviews:           make([]*PendingReview, 0, len(re.pendingReviews)),
	}

	for _, review := range re.pendingReviews {
		status.Reviews = append(status.Reviews, review)

		switch review.Status {
		case "pending":
			status.PendingReviews++
		case "approved":
			status.ApprovedReviews++
		case "rejected":
			status.RejectedReviews++
		case "needs_fixes":
			status.NeedsFixesReviews++
		}
	}

	return status
}

// ClearCompletedReviews removes completed reviews from memory (cleanup)
func (re *ReviewEvaluator) ClearCompletedReviews() int {
	cleared := 0
	for id, review := range re.pendingReviews {
		if review.Status == "approved" || review.Status == "rejected" {
			delete(re.pendingReviews, id)
			cleared++
		}
	}
	return cleared
}

// ReviewStatus represents the current state of code review processing
type ReviewStatus struct {
	TotalReviews      int              `json:"total_reviews"`
	PendingReviews    int              `json:"pending_reviews"`
	ApprovedReviews   int              `json:"approved_reviews"`
	RejectedReviews   int              `json:"rejected_reviews"`
	NeedsFixesReviews int              `json:"needs_fixes_reviews"`
	Reviews           []*PendingReview `json:"reviews"`
}
