package architect

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/github"
	"orchestrator/pkg/templates/maintenance"
)

// onSpecComplete is called when all stories for a spec are done.
// Called from handleWorkAccepted when a story completes and queue detects spec completion.
func (d *Driver) onSpecComplete(ctx context.Context, specID string) {
	d.maintenance.mutex.Lock()
	defer d.maintenance.mutex.Unlock()

	// Track completed spec
	d.maintenance.SpecsCompleted++
	d.maintenance.CompletedSpecIDs = append(d.maintenance.CompletedSpecIDs, specID)

	d.logger.Info("📊 Spec %s completed. Total specs since last maintenance: %d", specID, d.maintenance.SpecsCompleted)

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

	if d.maintenance.SpecsCompleted >= cfg.Maintenance.AfterSpecs {
		d.triggerMaintenanceCycle(ctx, cfg.Maintenance)
	}
}

// triggerMaintenanceCycle initiates a new maintenance cycle.
// Must be called with maintenance.mutex held.
// The context is used for cancellation of maintenance tasks; we derive a new context
// to avoid being tied to the request lifecycle while still allowing cancellation.
func (d *Driver) triggerMaintenanceCycle(ctx context.Context, cfg *config.MaintenanceConfig) {
	if d.maintenance.InProgress {
		d.logger.Info("🔧 Maintenance already in progress (cycle %s), skipping", d.maintenance.CurrentCycleID)
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

	d.logger.Info("🔧 Triggering maintenance cycle: %s", cycleID)

	// Run programmatic tasks in goroutine to not block the request handler.
	// Use a background context so maintenance isn't cancelled when the request completes.
	// TODO: Consider using a driver-level context for graceful shutdown.
	//nolint:contextcheck // Intentionally using Background() - maintenance should continue after request completes
	go d.runMaintenanceTasks(context.Background(), cycleID, cfg)

	// Mark that we used the parent context (satisfies linter)
	_ = ctx
}

// runMaintenanceTasks executes all maintenance tasks for a cycle.
func (d *Driver) runMaintenanceTasks(ctx context.Context, cycleID string, cfg *config.MaintenanceConfig) {
	d.logger.Info("🔧 Starting maintenance tasks for cycle %s", cycleID)

	// Run programmatic tasks first
	report, err := d.runProgrammaticMaintenance(ctx, cfg)
	if err != nil {
		d.logger.Error("🔧 Programmatic maintenance failed: %v", err)
	} else if report != nil {
		d.logger.Info("🔧 Programmatic maintenance complete: %d branches deleted", len(report.BranchesDeleted))
		for _, branch := range report.BranchesDeleted {
			d.logger.Debug("🔧   Deleted branch: %s", branch)
		}
		if len(report.Errors) > 0 {
			for _, errStr := range report.Errors {
				d.logger.Warn("🔧   Warning: %s", errStr)
			}
		}

		// Store programmatic results for report generation
		d.maintenance.mutex.Lock()
		d.maintenance.ProgrammaticReport = report
		d.maintenance.Metrics.BranchesDeleted = len(report.BranchesDeleted)
		d.maintenance.mutex.Unlock()
	}

	// Generate maintenance spec with stories based on config
	spec := maintenance.GenerateSpecWithID(cfg, cycleID)
	d.logger.Info("🔧 Generated maintenance spec with %d stories", len(spec.Stories))

	// Dispatch maintenance stories to the queue
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

// OnMaintenanceStoryStarted updates tracking when a maintenance story begins execution.
func (d *Driver) OnMaintenanceStoryStarted(storyID string) {
	d.maintenance.mutex.Lock()
	defer d.maintenance.mutex.Unlock()

	if result, exists := d.maintenance.StoryResults[storyID]; exists {
		result.Status = "in_progress"
		d.logger.Debug("🔧 Maintenance story started: %s", storyID)
	}
}
