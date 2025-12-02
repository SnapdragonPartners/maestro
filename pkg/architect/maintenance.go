package architect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/maintenance"
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
	d.maintenance.SpecsCompleted = 0
	d.maintenance.CompletedSpecIDs = nil

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
	// Add maintenance stories to the queue
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
		d.logger.Info("🔧 Queued maintenance story: %s", mStory.Title)
	}
}

// runProgrammaticMaintenance executes non-LLM maintenance tasks.
// Context is used for cancellation of long-running GitHub API calls.
//
//nolint:revive,unparam // ctx reserved for future GitHub API cancellation support
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

	// Import github package and call CleanupMergedBranches
	// This is deferred to the maintenance package for cleaner separation
	d.logger.Info("🔧 Branch cleanup would run here (implementation in pkg/maintenance/)")

	return report, nil
}

// completeMaintenanceCycle marks a maintenance cycle as complete.
func (d *Driver) completeMaintenanceCycle(cycleID string) {
	d.maintenance.mutex.Lock()
	defer d.maintenance.mutex.Unlock()

	d.maintenance.InProgress = false
	d.maintenance.LastMaintenance = time.Now()
	d.maintenance.CurrentCycleID = ""

	d.logger.Info("🔧 Maintenance cycle %s complete", cycleID)
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
}
