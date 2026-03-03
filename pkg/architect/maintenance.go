package architect

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/github"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/templates/maintenance"
	"orchestrator/pkg/tools"
)

// onSpecComplete is called when all stories for a spec are done.
// Called from checkSpecCompletion when a story completes and queue detects spec completion.
// Runs synchronously so maintenance stories are in the queue before the state machine continues.
func (d *Driver) onSpecComplete(ctx context.Context, specID string) {
	// Lock only for counter/tracking updates, then unlock before calling triggerMaintenanceCycle
	// (which needs to acquire the lock itself). Go mutexes are not reentrant.
	d.maintenance.mutex.Lock()
	d.maintenance.SpecsCompleted++
	d.maintenance.CompletedSpecIDs = append(d.maintenance.CompletedSpecIDs, specID)
	specsCompleted := d.maintenance.SpecsCompleted
	d.maintenance.mutex.Unlock()

	d.logger.Info("📊 Spec %s completed. Total specs since last maintenance: %d", specID, specsCompleted)

	// Check if maintenance should be triggered
	cfg, err := config.GetConfig()
	if err != nil {
		d.logger.Warn("🔧 Failed to get config for maintenance check: %v", err)
		return
	}

	if cfg.Maintenance == nil || !cfg.Maintenance.Enabled {
		d.logger.Debug("🔧 Maintenance mode disabled in config")
		return
	}

	// Heuristic: only trigger maintenance for significant specs or after enough small ones accumulate
	specPoints := d.queue.GetSpecTotalPoints(specID)
	meetsPointsThreshold := specPoints >= cfg.Maintenance.MinSpecPoints
	meetsSpecCountBackstop := specsCompleted >= cfg.Maintenance.MaxSpecsWithoutMaintenance

	if meetsPointsThreshold {
		d.logger.Info("🔧 Spec %s has %d estimated points (threshold: %d) — triggering maintenance",
			specID, specPoints, cfg.Maintenance.MinSpecPoints)
		d.triggerMaintenanceCycle(ctx, cfg.Maintenance)
	} else if meetsSpecCountBackstop {
		d.logger.Info("🔧 %d specs completed without maintenance (backstop: %d) — triggering maintenance",
			specsCompleted, cfg.Maintenance.MaxSpecsWithoutMaintenance)
		d.triggerMaintenanceCycle(ctx, cfg.Maintenance)
	} else {
		d.logger.Info("🔧 Spec %s has %d estimated points (threshold: %d), %d/%d specs since last maintenance — skipping maintenance",
			specID, specPoints, cfg.Maintenance.MinSpecPoints, specsCompleted, cfg.Maintenance.MaxSpecsWithoutMaintenance)
	}
}

// triggerMaintenanceCycle initiates a new maintenance cycle.
// Runs synchronously so maintenance stories are in the queue before the state machine
// continues to DISPATCHING. Branch cleanup (GitHub API) runs as a background goroutine
// since it's a nice-to-have that shouldn't block story dispatch.
func (d *Driver) triggerMaintenanceCycle(ctx context.Context, cfg *config.MaintenanceConfig) {
	d.maintenance.mutex.Lock()

	if d.maintenance.InProgress {
		d.logger.Info("🔧 Maintenance already in progress (cycle %s), skipping", d.maintenance.CurrentCycleID)
		d.maintenance.mutex.Unlock()
		return
	}

	// Generate unique cycle ID
	cycleID := fmt.Sprintf("maintenance-%s", time.Now().Format("2006-01-02-150405"))

	d.maintenance.InProgress = true
	d.maintenance.CurrentCycleID = cycleID
	d.maintenance.CycleStartedAt = time.Now()
	d.maintenance.SpecsCompleted = 0
	d.maintenance.CompletedSpecIDs = nil
	d.maintenance.StoryResults = make(map[string]*MaintenanceStoryResult)
	d.maintenance.ProgrammaticReport = nil
	d.maintenance.Metrics = MaintenanceMetrics{}

	// Snapshot and reset container upgrade flag while we hold the lock
	needsUpgrade := d.maintenance.NeedsContainerUpgrade
	upgradeReason := d.maintenance.ContainerUpgradeReason
	if needsUpgrade {
		if upgradeReason == "" {
			upgradeReason = "unknown"
		}
		d.maintenance.NeedsContainerUpgrade = false
		d.maintenance.ContainerUpgradeReason = ""
	}

	// Unlock before runMaintenanceTasks (which calls dispatchMaintenanceSpec, also locks)
	d.maintenance.mutex.Unlock()

	d.logger.Info("🔧 Triggering maintenance cycle: %s", cycleID)

	// Run maintenance tasks synchronously so stories are queued before state machine continues
	d.runMaintenanceTasks(ctx, cycleID, cfg, needsUpgrade, upgradeReason)
}

// runMaintenanceTasks executes all maintenance tasks for a cycle.
// Branch cleanup (GitHub API) runs as a background goroutine since it's a nice-to-have.
// Story generation and dispatch run synchronously so stories are queued immediately.
// needsUpgrade/upgradeReason are snapshotted by triggerMaintenanceCycle under lock.
func (d *Driver) runMaintenanceTasks(ctx context.Context, cycleID string, cfg *config.MaintenanceConfig, needsUpgrade bool, upgradeReason string) {
	d.logger.Info("🔧 Starting maintenance tasks for cycle %s", cycleID)

	// Run branch cleanup in background — it's a GitHub API call that shouldn't block story dispatch.
	// Use driver-level context so it survives the request lifecycle but cancels on shutdown.
	//nolint:contextcheck // Intentionally using driver context, not request context
	branchCtx := d.shutdownCtx
	if branchCtx == nil {
		branchCtx = ctx
	}
	go func() {
		report, err := d.runProgrammaticMaintenance(branchCtx, cfg)
		if err != nil {
			d.logger.Error("🔧 Programmatic maintenance failed: %v", err)
			return
		}
		if report == nil {
			return
		}
		d.logger.Info("🔧 Programmatic maintenance complete: %d branches deleted", len(report.BranchesDeleted))
		for _, branch := range report.BranchesDeleted {
			d.logger.Debug("🔧   Deleted branch: %s", branch)
		}
		for _, errStr := range report.Errors {
			d.logger.Warn("🔧   Warning: %s", errStr)
		}

		d.maintenance.mutex.Lock()
		d.maintenance.ProgrammaticReport = report
		d.maintenance.Metrics.BranchesDeleted = len(report.BranchesDeleted)
		d.maintenance.mutex.Unlock()
	}()

	// Snapshot and clear logged maintenance items before generating stories.
	// Items logged during this generation will roll into the next cycle (correct by design).
	loggedItems := d.snapshotAndClearItems()
	if len(loggedItems) > 0 {
		d.logger.Info("🔧 Snapshotted %d logged maintenance items for story generation", len(loggedItems))
	}

	// Generate maintenance spec with stories based on config (synchronous, in-memory)
	spec := maintenance.GenerateSpecWithID(cfg, cycleID)

	if needsUpgrade {
		spec.Stories = append(spec.Stories, maintenance.ContainerUpgradeStory(upgradeReason))
		d.logger.Info("🔧 Added container upgrade story (reason: %s)", upgradeReason)
	}

	// Generate stories from logged maintenance items via LLM (non-fatal)
	if len(loggedItems) > 0 {
		llmStories, err := d.generateStoriesFromItems(ctx, loggedItems)
		if err != nil {
			d.logger.Warn("🔧 Failed to generate stories from maintenance items: %v (continuing with hardcoded stories only)", err)
		} else if len(llmStories) > 0 {
			spec.Stories = append(spec.Stories, llmStories...)
			d.logger.Info("🔧 Added %d LLM-generated maintenance stories", len(llmStories))
		}
	}

	d.logger.Info("🔧 Generated maintenance spec with %d stories", len(spec.Stories))

	// Dispatch maintenance stories to the queue (synchronous)
	if len(spec.Stories) > 0 {
		d.dispatchMaintenanceSpec(spec)
		d.logger.Info("🔧 Dispatched %d maintenance stories", len(spec.Stories))
		// Cycle will be completed when all maintenance stories are done
		// (tracked via IsMaintenance flag on stories)
	} else {
		d.logger.Info("🔧 No maintenance stories to dispatch")
		d.completeMaintenanceCycle(cycleID)
	}
}

// dispatchMaintenanceSpec converts maintenance stories to queued stories and dispatches them.
func (d *Driver) dispatchMaintenanceSpec(spec *maintenance.Spec) {
	d.maintenance.mutex.Lock()
	defer d.maintenance.mutex.Unlock()

	// Add maintenance stories to the queue and track them
	for i := range spec.Stories {
		mStory := &spec.Stories[i]
		storyID := fmt.Sprintf("%s-%s", spec.ID, mStory.ID)
		d.queue.AddMaintenanceStory(
			storyID,
			spec.ID,
			mStory.Title,
			mStory.Content,
			mStory.Express,
			true, // IsMaintenance
		)

		// Initialize story tracking
		d.maintenance.StoryResults[storyID] = &MaintenanceStoryResult{
			StoryID: storyID,
			Title:   mStory.Title,
			Status:  "pending",
		}
		d.maintenance.Metrics.StoriesTotal++

		d.logger.Info("🔧 Queued maintenance story: %s", mStory.Title)
	}
}

// runProgrammaticMaintenance executes non-LLM maintenance tasks.
// Context is used for cancellation of long-running GitHub API calls.
func (d *Driver) runProgrammaticMaintenance(ctx context.Context, cfg *config.MaintenanceConfig) (*ProgrammaticReport, error) {
	report := &ProgrammaticReport{}

	// Skip if branch cleanup disabled
	if !cfg.Tasks.BranchCleanup {
		d.logger.Debug("🔧 Branch cleanup disabled in config")
		return report, nil
	}

	// Get GitHub client from global config
	globalCfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	if globalCfg.Git == nil || globalCfg.Git.RepoURL == "" {
		d.logger.Debug("🔧 No git repo configured, skipping branch cleanup")
		return report, nil
	}

	// Create GitHub client from repo URL
	ghClient, err := github.NewClientFromRemote(globalCfg.Git.RepoURL)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("failed to create GitHub client: %v", err))
		return report, nil // Return partial report, don't fail whole maintenance
	}

	// Determine target branch (default to main)
	targetBranch := "main"
	if globalCfg.Git.TargetBranch != "" {
		targetBranch = globalCfg.Git.TargetBranch
	}

	// Get protected patterns from config
	protectedPatterns := cfg.BranchCleanup.ProtectedPatterns

	// Run branch cleanup
	d.logger.Info("🔧 Running branch cleanup (target: %s, protected: %v)", targetBranch, protectedPatterns)
	deleted, cleanupErr := ghClient.CleanupMergedBranches(ctx, targetBranch, protectedPatterns)
	if cleanupErr != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("branch cleanup failed: %v", cleanupErr))
	} else {
		report.BranchesDeleted = deleted
	}

	return report, nil
}

// completeMaintenanceCycle marks a maintenance cycle as complete.
// It generates a report, saves it to file, and posts it to chat.
//
//nolint:contextcheck // Called from background goroutine - uses its own context for chat post
func (d *Driver) completeMaintenanceCycle(cycleID string) {
	d.maintenance.mutex.Lock()

	// Generate cycle report from tracking data
	report := d.generateCycleReportUnsafe()

	// Mark cycle as complete
	d.maintenance.InProgress = false
	d.maintenance.LastMaintenance = time.Now()
	d.maintenance.CurrentCycleID = ""

	d.maintenance.mutex.Unlock()

	d.logger.Info("🔧 Maintenance cycle %s complete", cycleID)

	// Save report to file
	reportsDir := filepath.Join(d.workDir, ".maestro", "maintenance-reports")
	savedPath, err := report.SaveToFile(reportsDir)
	if err != nil {
		d.logger.Error("🔧 Failed to save maintenance report: %v", err)
	} else {
		d.logger.Info("🔧 Maintenance report saved to: %s", savedPath)
	}

	// Post report summary to chat
	d.postMaintenanceReport(report)
}

// generateCycleReportUnsafe creates a CycleReport from current tracking data.
// Must be called with mutex held.
func (d *Driver) generateCycleReportUnsafe() *maintenance.CycleReport {
	// Convert story results to report format
	stories := make([]*maintenance.StoryResult, 0, len(d.maintenance.StoryResults))
	for _, result := range d.maintenance.StoryResults {
		stories = append(stories, &maintenance.StoryResult{
			StoryID:     result.StoryID,
			Title:       result.Title,
			Status:      result.Status,
			PRNumber:    result.PRNumber,
			PRMerged:    result.PRMerged,
			CompletedAt: result.CompletedAt,
			Summary:     result.Summary,
		})
	}

	// Get branch cleanup data
	var branchesDeleted []string
	var cleanupErrors []string
	if d.maintenance.ProgrammaticReport != nil {
		branchesDeleted = d.maintenance.ProgrammaticReport.BranchesDeleted
		cleanupErrors = d.maintenance.ProgrammaticReport.Errors
	}

	// Convert metrics
	metrics := maintenance.CycleMetrics{
		StoriesTotal:     d.maintenance.Metrics.StoriesTotal,
		StoriesCompleted: d.maintenance.Metrics.StoriesCompleted,
		StoriesFailed:    d.maintenance.Metrics.StoriesFailed,
		PRsMerged:        d.maintenance.Metrics.PRsMerged,
		BranchesDeleted:  d.maintenance.Metrics.BranchesDeleted,
	}

	return maintenance.NewCycleReport(
		d.maintenance.CurrentCycleID,
		d.maintenance.CycleStartedAt,
		branchesDeleted,
		cleanupErrors,
		stories,
		metrics,
	)
}

// postMaintenanceReport posts the maintenance report summary to chat.
func (d *Driver) postMaintenanceReport(report *maintenance.CycleReport) {
	if d.chatService == nil {
		d.logger.Debug("🔧 Chat service not available, skipping report post")
		return
	}

	// Generate markdown report
	markdown, err := report.ToMarkdown()
	if err != nil {
		d.logger.Error("🔧 Failed to generate markdown report: %v", err)
		return
	}

	// Post to chat
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = d.chatService.Post(ctx, &ChatPostRequest{
		Author:   d.GetAgentID(),
		Text:     markdown,
		Channel:  "maintenance",
		PostType: "maintenance_report",
	})
	if err != nil {
		d.logger.Error("🔧 Failed to post maintenance report to chat: %v", err)
	} else {
		d.logger.Info("🔧 Maintenance report posted to chat")
	}
}

// ProgrammaticReport holds results of programmatic maintenance tasks.
type ProgrammaticReport struct {
	BranchesDeleted []string
	Errors          []string
}

// GetMaintenanceStatus returns the current maintenance cycle status.
func (d *Driver) GetMaintenanceStatus() MaintenanceStatus {
	d.maintenance.mutex.Lock()
	defer d.maintenance.mutex.Unlock()

	return MaintenanceStatus{
		InProgress:      d.maintenance.InProgress,
		CurrentCycleID:  d.maintenance.CurrentCycleID,
		SpecsCompleted:  d.maintenance.SpecsCompleted,
		LastMaintenance: d.maintenance.LastMaintenance,
		CycleStartedAt:  d.maintenance.CycleStartedAt,
		Metrics:         d.maintenance.Metrics,
	}
}

// MaintenanceStatus provides read-only view of maintenance state.
//
//nolint:govet // Field order optimized for readability, not memory alignment
type MaintenanceStatus struct {
	InProgress      bool
	CurrentCycleID  string
	SpecsCompleted  int
	LastMaintenance time.Time
	CycleStartedAt  time.Time
	Metrics         MaintenanceMetrics
}

// OnMaintenanceStoryComplete updates tracking when a maintenance story finishes.
// If all stories are complete, this triggers cycle completion (report generation and posting).
func (d *Driver) OnMaintenanceStoryComplete(storyID string, success bool, prNumber int, prMerged bool, summary string) {
	d.maintenance.mutex.Lock()

	// Update story result
	if result, exists := d.maintenance.StoryResults[storyID]; exists {
		if success {
			result.Status = "completed"
			d.maintenance.Metrics.StoriesCompleted++
		} else {
			result.Status = "failed"
			d.maintenance.Metrics.StoriesFailed++
		}
		result.PRNumber = prNumber
		result.PRMerged = prMerged
		result.CompletedAt = time.Now()
		result.Summary = summary

		if prMerged {
			d.maintenance.Metrics.PRsMerged++
		}

		d.logger.Info("🔧 Maintenance story %s: %s (PR: %d, merged: %v)",
			storyID, result.Status, prNumber, prMerged)
	} else {
		d.logger.Warn("🔧 Unknown maintenance story completed: %s", storyID)
	}

	// Check if all stories are complete
	cycleComplete := d.isMaintenanceCycleCompleteUnsafe()
	cycleID := d.maintenance.CurrentCycleID

	// Release mutex before calling completeMaintenanceCycle (which also locks)
	d.maintenance.mutex.Unlock()

	// Complete the cycle if all stories are done
	if cycleComplete && cycleID != "" {
		d.completeMaintenanceCycle(cycleID)
	}
}

// isMaintenanceCycleCompleteUnsafe checks if all maintenance stories are done.
// Must be called with mutex held.
func (d *Driver) isMaintenanceCycleCompleteUnsafe() bool {
	for _, result := range d.maintenance.StoryResults {
		if result.Status == "pending" || result.Status == "in_progress" {
			return false
		}
	}
	return len(d.maintenance.StoryResults) > 0
}

// snapshotAndClearItems atomically copies and clears the maintenance items list.
// Returns the snapshot of items that were accumulated since the last cycle.
// Must be called outside the maintenance mutex (it acquires it internally).
func (d *Driver) snapshotAndClearItems() []tools.MaintenanceItem {
	d.maintenance.mutex.Lock()
	defer d.maintenance.mutex.Unlock()

	if len(d.maintenance.Items) == 0 {
		return nil
	}

	// Copy slice and nil the original
	items := d.maintenance.Items
	d.maintenance.Items = nil
	return items
}

// generateStoriesFromItems runs a single-turn LLM toolloop to convert logged maintenance items
// into structured stories. Returns maintenance.Story structs ready to be appended to a spec.
// Non-fatal: callers should log warnings and continue if this fails.
func (d *Driver) generateStoriesFromItems(ctx context.Context, items []tools.MaintenanceItem) ([]maintenance.Story, error) {
	if d.toolLoop == nil || d.LLMClient == nil {
		return nil, fmt.Errorf("LLM not available for maintenance story generation")
	}

	if d.renderer == nil {
		return nil, fmt.Errorf("template renderer not available")
	}

	// Serialize items into a text document for the LLM
	var sb strings.Builder
	for i := range items {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n   Source: %s | Logged: %s\n\n",
			i+1, items[i].Priority, items[i].Description, items[i].Source, items[i].AddedAt.Format(time.RFC3339)))
	}

	// Render the maintenance story generation template
	templateData := &templates.TemplateData{
		TaskContent: sb.String(),
	}

	prompt, err := d.renderer.Render(templates.MaintenanceStoryGenTemplate, templateData)
	if err != nil {
		return nil, fmt.Errorf("failed to render maintenance story gen template: %w", err)
	}

	// Create a fresh context manager for this one-shot generation
	modelName := config.GetEffectiveArchitectModel()
	cm := contextmgr.NewContextManagerWithModel(modelName)
	cm.AddMessage("user", prompt)

	// Get submit_stories tool
	submitStoriesTool := tools.NewSubmitStoriesTool()

	// Run single-turn toolloop with submit_stories as terminal tool
	storiesOut := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[SubmitStoriesResult]{
		ContextManager:     cm,
		GeneralTools:       nil, // No general tools — just generate and submit
		TerminalTool:       submitStoriesTool,
		MaxIterations:      5,
		SingleTurn:         true,
		MaxTokens:          agent.ArchitectMaxTokens,
		Temperature:        config.GetTemperature(config.TempRoleArchitect),
		AgentID:            d.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		PersistenceChannel: d.persistenceChannel,
		OnLLMError:         d.makeOnLLMErrorCallback("maintenance_story_gen"),
	})

	if storiesOut.Kind != toolloop.OutcomeProcessEffect {
		return nil, fmt.Errorf("maintenance story generation failed: %w", storiesOut.Err)
	}

	if storiesOut.Signal != tools.SignalStoriesSubmitted {
		return nil, fmt.Errorf("expected STORIES_SUBMITTED signal, got: %s", storiesOut.Signal)
	}

	// Extract requirements from effect data
	effectData, ok := storiesOut.EffectData.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("STORIES_SUBMITTED effect data is not map[string]any: %T", storiesOut.EffectData)
	}

	requirements, err := d.convertToolResultToRequirements(effectData)
	if err != nil {
		return nil, fmt.Errorf("failed to convert tool result to requirements: %w", err)
	}

	// Cap at 5 stories maximum to prevent queue flooding
	const maxMaintenanceStories = 5
	if len(requirements) > maxMaintenanceStories {
		d.logger.Info("🔧 Capping maintenance stories from %d to %d (prioritizing highest priority)", len(requirements), maxMaintenanceStories)
		requirements = requirements[:maxMaintenanceStories]
	}

	// Convert requirements to maintenance.Story structs
	stories := make([]maintenance.Story, 0, len(requirements))
	for i := range requirements {
		req := &requirements[i]
		// Build story content from description + acceptance criteria
		var content strings.Builder
		content.WriteString(req.Description)
		if len(req.AcceptanceCriteria) > 0 {
			content.WriteString("\n\n## Acceptance Criteria\n")
			for _, ac := range req.AcceptanceCriteria {
				content.WriteString(fmt.Sprintf("- %s\n", ac))
			}
		}

		// Small maintenance stories skip planning
		express := req.EstimatedPoints <= 1

		stories = append(stories, maintenance.Story{
			ID:      req.ID,
			Title:   req.Title,
			Content: content.String(),
			Express: express,
		})
	}

	return stories, nil
}

// OnMaintenanceStoryStarted updates tracking when a maintenance story begins execution.
func (d *Driver) OnMaintenanceStoryStarted(storyID string) {
	d.maintenance.mutex.Lock()
	defer d.maintenance.mutex.Unlock()

	if result, exists := d.maintenance.StoryResults[storyID]; exists {
		result.Status = "in_progress"
		d.logger.Debug("🔧 Maintenance story started: %s", storyID)
	}
}
