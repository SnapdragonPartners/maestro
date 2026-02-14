package maintenance

import (
	"strings"
	"testing"

	"orchestrator/pkg/config"
)

func TestGenerateSpec(t *testing.T) {
	cfg := &config.MaintenanceConfig{
		Enabled:    true,
		AfterSpecs: 1,
		Tasks: config.MaintenanceTasksConfig{
			BranchCleanup:    true,
			KnowledgeSync:    true,
			DocsVerification: true,
			TodoScan:         true,
			DeferredReview:   true,
			TestCoverage:     true,
		},
		TodoScan: config.TodoScanConfig{
			Markers: []string{"TODO", "FIXME"},
		},
	}

	spec := GenerateSpec(cfg)

	// Verify spec properties
	if spec.Type != SpecTypeMaintenance {
		t.Errorf("expected type %s, got %s", SpecTypeMaintenance, spec.Type)
	}
	if !spec.AutoMerge {
		t.Error("expected AutoMerge to be true")
	}
	if !spec.SkipUAT {
		t.Error("expected SkipUAT to be true")
	}
	if !spec.IsMaintenance {
		t.Error("expected IsMaintenance to be true")
	}
	if spec.Title != "Automated Maintenance Cycle" {
		t.Errorf("unexpected title: %s", spec.Title)
	}
	if !strings.HasPrefix(spec.ID, "maintenance-") {
		t.Errorf("expected ID to start with 'maintenance-', got %s", spec.ID)
	}

	// Should have 5 stories (all tasks enabled)
	if len(spec.Stories) != 5 {
		t.Errorf("expected 5 stories, got %d", len(spec.Stories))
	}

	// Verify all stories have required fields
	for i, story := range spec.Stories {
		if story.ID == "" {
			t.Errorf("story %d has empty ID", i)
		}
		if story.Title == "" {
			t.Errorf("story %d has empty Title", i)
		}
		if story.Content == "" {
			t.Errorf("story %d has empty Content", i)
		}
		if !story.IsMaintenance {
			t.Errorf("story %d should have IsMaintenance=true", i)
		}
	}
}

func TestGenerateSpecWithDisabledTasks(t *testing.T) {
	cfg := &config.MaintenanceConfig{
		Enabled:    true,
		AfterSpecs: 1,
		Tasks: config.MaintenanceTasksConfig{
			BranchCleanup:    true,  // Programmatic, not a story
			KnowledgeSync:    true,  // Story 1
			DocsVerification: false, // Disabled
			TodoScan:         true,  // Story 2
			DeferredReview:   false, // Disabled
			TestCoverage:     true,  // Story 3
		},
	}

	spec := GenerateSpec(cfg)

	// Should have 3 stories (2 disabled)
	if len(spec.Stories) != 3 {
		t.Errorf("expected 3 stories, got %d", len(spec.Stories))
	}

	// Verify the correct stories are present
	storyIDs := make(map[string]bool)
	for _, s := range spec.Stories {
		storyIDs[s.ID] = true
	}

	if !storyIDs["maint-knowledge-sync"] {
		t.Error("missing knowledge-sync story")
	}
	if !storyIDs["maint-todo-scan"] {
		t.Error("missing todo-scan story")
	}
	if !storyIDs["maint-test-coverage"] {
		t.Error("missing test-coverage story")
	}
	if storyIDs["maint-docs-verification"] {
		t.Error("docs-verification story should be disabled")
	}
	if storyIDs["maint-deferred-review"] {
		t.Error("deferred-review story should be disabled")
	}
}

func TestGenerateSpecWithCustomID(t *testing.T) {
	cfg := &config.MaintenanceConfig{
		Enabled:    true,
		AfterSpecs: 1,
		Tasks: config.MaintenanceTasksConfig{
			KnowledgeSync: true,
		},
	}

	customID := "test-maintenance-123"
	spec := GenerateSpecWithID(cfg, customID)

	if spec.ID != customID {
		t.Errorf("expected ID %s, got %s", customID, spec.ID)
	}
}

func TestTodoScanStoryMarkers(t *testing.T) {
	markers := []string{"TODO", "FIXME", "HACK"}
	story := TodoScanStory(markers)

	// Verify markers are included in content
	for _, marker := range markers {
		if !strings.Contains(story.Content, marker) {
			t.Errorf("expected content to contain marker %s", marker)
		}
	}

	// Todo scan should be express (skip planning)
	if !story.Express {
		t.Error("expected TodoScan story to have Express=true")
	}
}

func TestStoryTemplates(t *testing.T) {
	tests := []struct {
		name          string
		story         Story
		expectExpress bool
	}{
		{
			name:          "KnowledgeSync",
			story:         KnowledgeSyncStory(),
			expectExpress: false,
		},
		{
			name:          "DocsVerification",
			story:         DocsVerificationStory(),
			expectExpress: false,
		},
		{
			name:          "TodoScan",
			story:         TodoScanStory([]string{"TODO"}),
			expectExpress: true, // Read-only scan
		},
		{
			name:          "DeferredReview",
			story:         DeferredReviewStory(),
			expectExpress: false,
		},
		{
			name:          "TestCoverage",
			story:         TestCoverageStory(),
			expectExpress: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.story.ID == "" {
				t.Error("story has empty ID")
			}
			if tc.story.Title == "" {
				t.Error("story has empty Title")
			}
			if tc.story.Content == "" {
				t.Error("story has empty Content")
			}
			if !tc.story.IsMaintenance {
				t.Error("story should have IsMaintenance=true")
			}
			if tc.story.Express != tc.expectExpress {
				t.Errorf("expected Express=%v, got %v", tc.expectExpress, tc.story.Express)
			}

			// Verify content has Acceptance Criteria section
			if !strings.Contains(tc.story.Content, "Acceptance Criteria") {
				t.Error("story content should have Acceptance Criteria section")
			}
			// Verify content has Constraints section
			if !strings.Contains(tc.story.Content, "Constraints") {
				t.Error("story content should have Constraints section")
			}
		})
	}
}

func TestContainerUpgradeStory(t *testing.T) {
	story := ContainerUpgradeStory("claude_code")

	if story.ID != "maint-container-upgrade" {
		t.Errorf("expected ID 'maint-container-upgrade', got %s", story.ID)
	}
	if !story.Express {
		t.Error("expected ContainerUpgrade story to have Express=true")
	}
	if !story.IsMaintenance {
		t.Error("expected ContainerUpgrade story to have IsMaintenance=true")
	}

	// Should reference the reason
	if !strings.Contains(story.Content, "claude_code") {
		t.Error("expected content to include the upgrade reason")
	}

	// Should reference minimum version
	if !strings.Contains(story.Content, config.MinClaudeCodeVersion) {
		t.Errorf("expected content to reference minimum version %s", config.MinClaudeCodeVersion)
	}

	// Should have acceptance criteria
	if !strings.Contains(story.Content, "Acceptance Criteria") {
		t.Error("expected content to have Acceptance Criteria section")
	}
}

func TestDefaultMarkersUsed(t *testing.T) {
	cfg := &config.MaintenanceConfig{
		Enabled:    true,
		AfterSpecs: 1,
		Tasks: config.MaintenanceTasksConfig{
			TodoScan: true,
		},
		TodoScan: config.TodoScanConfig{
			Markers: nil, // No markers configured
		},
	}

	spec := GenerateSpec(cfg)

	if len(spec.Stories) != 1 {
		t.Fatalf("expected 1 story, got %d", len(spec.Stories))
	}

	story := spec.Stories[0]
	// Should use default markers
	defaultMarkers := []string{"TODO", "FIXME", "HACK", "XXX", "deprecated"}
	for _, marker := range defaultMarkers {
		if !strings.Contains(story.Content, marker) {
			t.Errorf("expected content to contain default marker %s", marker)
		}
	}
}
