// Package tools provides container orchestration tools for atomic container switching.
package tools

import (
	"context"
	"fmt"
	"time"

	"orchestrator/internal/state"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

// Result represents the outcome of a container switch operation.
type Result struct {
	Timestamp     time.Time `json:"timestamp"`       // When the switch occurred
	Status        string    `json:"status"`          // "switched", "noop", or "failed"
	ActiveImageID string    `json:"active_image_id"` // Currently active image ID
	Role          string    `json:"role"`            // Role of the active container
}

// Orchestrator interface defines the operations needed for container switching.
type Orchestrator interface {
	// Container lifecycle
	StartContainer(ctx context.Context, role state.Role, imageID string) (cid, name string, err error)
	StopContainer(ctx context.Context, cid string) error

	// Health and verification
	HealthCheck(ctx context.Context, cid string) error

	// Configuration
	GetRepoURL() string
	GetLastBuiltOrTestedImageID() string

	// State management
	GetRuntimeState() *state.RuntimeState
}

// ContainerSwitch performs atomic container switching with GitHub auth setup and health checks.
// This implements the container promotion algorithm with rollback support.
//
// The algorithm:
// 1. Check for idempotence (already on target image)
// 2. Start candidate container
// 3. Install and run GitHub auth script
// 4. Run health checks
// 5. Atomically commit: stop old, activate new, update pin
// 6. Handle rollback on failures
//
//nolint:cyclop // Complex function handles atomic operations with comprehensive error handling
func ContainerSwitch(ctx context.Context, role state.Role, imageID string, o Orchestrator) (*Result, error) {
	logger := logx.NewLogger("container-switch")
	logger.Info("🔄 Starting container switch: role=%s imageID=%s", string(role), truncateImageID(imageID))

	// Resolve candidate imageID if not provided (for target role)
	if imageID == "" && role == state.RoleTarget {
		imageID = o.GetLastBuiltOrTestedImageID()
		if imageID == "" {
			return nil, fmt.Errorf("no image ID provided and no last built/tested image available")
		}
		logger.Info("🔄 Resolved target image ID: %s", truncateImageID(imageID))
	}

	if imageID == "" {
		return nil, fmt.Errorf("image ID cannot be empty")
	}

	runtimeState := o.GetRuntimeState()
	pinnedImageID := config.GetPinnedImageID()

	// Idempotence check: if already on target image and pinned, return noop
	active := runtimeState.GetActive()
	if active != nil && active.ImageID == imageID && pinnedImageID == imageID {
		logger.Info("🔄 Container switch noop: already using image %s", truncateImageID(imageID))
		return &Result{
			Status:        "noop",
			ActiveImageID: imageID,
			Role:          string(active.Role),
			Timestamp:     time.Now(),
		}, nil
	}

	logger.Info("🔄 Current state: active=%s pinned=%s target=%s",
		formatActiveContainer(active), truncateImageID(pinnedImageID), truncateImageID(imageID))

	// 1) Prepare: start candidate container
	logger.Info("🚀 Starting candidate container for image %s", truncateImageID(imageID))
	cid, name, err := o.StartContainer(ctx, role, imageID)
	if err != nil {
		logger.Error("❌ Failed to start candidate container: %v", err)
		return nil, fmt.Errorf("failed to start candidate container: %w", err)
	}

	logger.Info("✅ Candidate container started: %s (name: %s)", cid, name)

	// Ensure cleanup on any failure after this point
	defer func() {
		if err != nil {
			logger.Info("🧹 Cleaning up candidate container %s due to failure", cid)
			if cleanupErr := o.StopContainer(ctx, cid); cleanupErr != nil {
				logger.Error("⚠️ Failed to cleanup candidate container %s: %v", cid, cleanupErr)
			}
		}
	}()

	// 2) Probe: GitHub auth setup - removed embedded script system
	logger.Info("🔑 GitHub authentication setup skipped (embedded script system removed)")
	logger.Info("✅ Containers should be pre-configured with GitHub authentication")

	// 3) Probe: health checks
	logger.Info("🏥 Running health checks on candidate container")
	if err = o.HealthCheck(ctx, cid); err != nil {
		logger.Error("❌ Health check failed: %v", err)
		return nil, fmt.Errorf("health check failed: %w", err)
	}
	logger.Info("✅ Health checks passed")

	// 4) Commit phase (atomic): stop current, push to history, activate candidate, pin
	logger.Info("📌 Committing container switch (atomic operation)")

	prev := active
	if prev != nil {
		logger.Info("📚 Moving previous container to history: %s", formatActiveContainer(prev))

		// Push to history before stopping
		runtimeState.HistoryPush(&state.HistoryEntry{
			Role:    prev.Role,
			ImageID: prev.ImageID,
			Name:    prev.Name,
			Started: prev.Started,
			Stopped: time.Now(),
		})

		// Stop previous container
		logger.Info("🛑 Stopping previous container: %s", prev.CID)
		if stopErr := o.StopContainer(ctx, prev.CID); stopErr != nil {
			logger.Error("⚠️ Failed to stop previous container %s: %v", prev.CID, stopErr)
			// Continue anyway - the switch can still succeed
		}
	}

	// Activate new container
	newActive := &state.ActiveContainer{
		Role:    role,
		CID:     cid,
		ImageID: imageID,
		Name:    name,
		Started: time.Now(),
	}
	runtimeState.SetActive(newActive)
	logger.Info("✅ Activated new container: %s", formatActiveContainer(newActive))

	// Pin the image atomically
	logger.Info("📌 Pinning image ID: %s", truncateImageID(imageID))
	if err = config.SetPinnedImageID(imageID); err != nil {
		logger.Error("❌ Pin write failed, initiating rollback: %v", err)

		// Critical failure: pin write failed, must rollback
		runtimeState.SetActive(nil)
		if stopErr := o.StopContainer(ctx, cid); stopErr != nil {
			logger.Error("⚠️ Failed to stop new container during rollback: %v", stopErr)
		}

		// Try to restore previous active container
		if prev != nil {
			logger.Info("🔄 Attempting rollback to previous container")
			if rollbackCID, rollbackName, rollbackErr := o.StartContainer(ctx, prev.Role, prev.ImageID); rollbackErr == nil {
				rollbackActive := &state.ActiveContainer{
					Role:    prev.Role,
					CID:     rollbackCID,
					ImageID: prev.ImageID,
					Name:    rollbackName,
					Started: time.Now(),
				}
				runtimeState.SetActive(rollbackActive)
				_ = config.SetPinnedImageID(prev.ImageID) // Best effort
				logger.Info("✅ Rollback successful: %s", formatActiveContainer(rollbackActive))
			} else {
				logger.Error("❌ Rollback failed: %v", rollbackErr)
			}
		}

		return nil, fmt.Errorf("pin write failed: %w", err)
	}

	logger.Info("🎉 Container switch completed successfully")
	logger.Info("📊 Final state: active=%s pinned=%s",
		formatActiveContainer(newActive), truncateImageID(imageID))

	return &Result{
		Status:        "switched",
		ActiveImageID: imageID,
		Role:          string(role),
		Timestamp:     time.Now(),
	}, nil
}

// Helper functions for logging

func truncateImageID(imageID string) string {
	if len(imageID) > 12 {
		return imageID[:12]
	}
	return imageID
}

func formatActiveContainer(active *state.ActiveContainer) string {
	if active == nil {
		return "none"
	}
	return fmt.Sprintf("%s:%s", truncateImageID(active.ImageID), string(active.Role))
}
