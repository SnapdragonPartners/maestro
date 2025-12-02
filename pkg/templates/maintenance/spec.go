// Package maintenance provides automated technical debt management
// through periodic maintenance cycles.
package maintenance

import (
	"fmt"
	"time"

	"orchestrator/pkg/config"
)

// SpecTypeMaintenance indicates an auto-generated maintenance spec.
const SpecTypeMaintenance = "maintenance"

// Spec represents a maintenance specification with predefined stories.
type Spec struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Type          string  `json:"type"`
	Stories       []Story `json:"stories"`
	AutoMerge     bool    `json:"auto_merge"`     // PRs auto-merge after CI passes
	SkipUAT       bool    `json:"skip_uat"`       // No UAT for maintenance
	IsMaintenance bool    `json:"is_maintenance"` // Flag for routing
}

// Story represents a maintenance story to be executed.
type Story struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Content       string `json:"content"`
	Express       bool   `json:"express"`        // Skip planning, fast-path to coding
	IsMaintenance bool   `json:"is_maintenance"` // Flag for routing
}

// GenerateSpec creates a maintenance spec with stories based on config.
func GenerateSpec(cfg *config.MaintenanceConfig) *Spec {
	specID := fmt.Sprintf("maintenance-%s", time.Now().Format("2006-01-02-150405"))

	stories := make([]Story, 0, 5)

	if cfg.Tasks.KnowledgeSync {
		stories = append(stories, KnowledgeSyncStory())
	}
	if cfg.Tasks.DocsVerification {
		stories = append(stories, DocsVerificationStory())
	}
	if cfg.Tasks.TodoScan {
		markers := cfg.TodoScan.Markers
		if len(markers) == 0 {
			// Use defaults if not configured
			markers = []string{"TODO", "FIXME", "HACK", "XXX", "deprecated", "DEPRECATED", "@deprecated"}
		}
		stories = append(stories, TodoScanStory(markers))
	}
	if cfg.Tasks.DeferredReview {
		stories = append(stories, DeferredReviewStory())
	}
	if cfg.Tasks.TestCoverage {
		stories = append(stories, TestCoverageStory())
	}

	return &Spec{
		ID:            specID,
		Title:         "Automated Maintenance Cycle",
		Type:          SpecTypeMaintenance,
		Stories:       stories,
		AutoMerge:     true,
		SkipUAT:       true,
		IsMaintenance: true,
	}
}

// GenerateSpecWithID creates a maintenance spec with a custom ID.
// Useful for testing or when the caller needs to control the ID.
func GenerateSpecWithID(cfg *config.MaintenanceConfig, specID string) *Spec {
	spec := GenerateSpec(cfg)
	spec.ID = specID
	return spec
}
