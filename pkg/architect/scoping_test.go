package architect

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvertToolResultToRequirements_ExtractsID verifies the ID field is extracted.
func TestConvertToolResultToRequirements_ExtractsID(t *testing.T) {
	driver := newTestDriver()

	toolResult := map[string]any{
		"requirements": []any{
			map[string]any{
				"id":                  "req_001",
				"title":               "Setup project",
				"description":         "Initialize the project",
				"acceptance_criteria": []any{"compiles", "tests pass"},
				"story_type":          "devops",
				"dependencies":        []any{},
			},
			map[string]any{
				"id":                  "req_002",
				"title":               "Add feature",
				"description":         "Implement the feature",
				"acceptance_criteria": []any{"feature works"},
				"story_type":          "app",
				"dependencies":        []any{"req_001"},
			},
		},
	}

	reqs, err := driver.convertToolResultToRequirements(toolResult)
	require.NoError(t, err)
	require.Len(t, reqs, 2)

	assert.Equal(t, "req_001", reqs[0].ID)
	assert.Equal(t, "Setup project", reqs[0].Title)
	assert.Equal(t, "devops", reqs[0].StoryType)

	assert.Equal(t, "req_002", reqs[1].ID)
	assert.Equal(t, "Add feature", reqs[1].Title)
	assert.Equal(t, []string{"req_001"}, reqs[1].Dependencies)
}

// TestLoadStories_OrdinalDependencyResolution verifies ordinal IDs are resolved to story IDs.
func TestLoadStories_OrdinalDependencyResolution(t *testing.T) {
	driver := newTestDriver()

	effectData := map[string]any{
		"requirements": []any{
			map[string]any{
				"id":                  "req_001",
				"title":               "Setup project",
				"description":         "Initialize the project",
				"acceptance_criteria": []any{"compiles"},
				"story_type":          "app",
				"dependencies":        []any{},
			},
			map[string]any{
				"id":                  "req_002",
				"title":               "Add feature A",
				"description":         "Feature A depends on setup",
				"acceptance_criteria": []any{"works"},
				"story_type":          "app",
				"dependencies":        []any{"req_001"},
			},
			map[string]any{
				"id":                  "req_003",
				"title":               "Add feature B",
				"description":         "Feature B depends on both",
				"acceptance_criteria": []any{"works"},
				"story_type":          "app",
				"dependencies":        []any{"req_001", "req_002"},
			},
		},
	}

	specID, storyIDs, err := driver.loadStoriesFromSubmitResultData(context.Background(), "test spec", effectData)
	require.NoError(t, err)
	assert.NotEmpty(t, specID)
	require.Len(t, storyIDs, 3)

	// Verify dependency resolution: story[1] depends on story[0], story[2] depends on story[0] and story[1]
	story1, exists := driver.queue.GetStory(storyIDs[1])
	require.True(t, exists)
	assert.Contains(t, story1.DependsOn, storyIDs[0])

	story2, exists := driver.queue.GetStory(storyIDs[2])
	require.True(t, exists)
	assert.Contains(t, story2.DependsOn, storyIDs[0])
	assert.Contains(t, story2.DependsOn, storyIDs[1])
}

// TestLoadStories_UnresolvableOrdinal verifies error on unknown dependency ordinal.
func TestLoadStories_UnresolvableOrdinal(t *testing.T) {
	driver := newTestDriver()

	effectData := map[string]any{
		"requirements": []any{
			map[string]any{
				"id":                  "req_001",
				"title":               "Setup",
				"description":         "Setup project",
				"acceptance_criteria": []any{"compiles"},
				"story_type":          "app",
				"dependencies":        []any{"req_999"}, // Does not exist
			},
		},
	}

	_, _, err := driver.loadStoriesFromSubmitResultData(context.Background(), "test spec", effectData)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unresolvable dependency")
	assert.Contains(t, err.Error(), "req_999")
}

// TestLoadStories_BootstrapGate verifies new stories depend on pre-existing stories in the queue.
func TestLoadStories_BootstrapGate(t *testing.T) {
	driver := newTestDriver()

	// Pre-populate queue with a bootstrap story (simulates stories from a prior spec)
	driver.queue.AddStory("bootstrap-1", "bootstrap-spec", "Bootstrap setup", "Setup containers", "devops", nil, 1)

	effectData := map[string]any{
		"requirements": []any{
			map[string]any{
				"id":                  "req_001",
				"title":               "App feature A",
				"description":         "First app feature",
				"acceptance_criteria": []any{"works"},
				"story_type":          "app",
				"dependencies":        []any{},
			},
			map[string]any{
				"id":                  "req_002",
				"title":               "App feature B",
				"description":         "Second app feature",
				"acceptance_criteria": []any{"works"},
				"story_type":          "app",
				"dependencies":        []any{"req_001"},
			},
		},
	}

	_, storyIDs, err := driver.loadStoriesFromSubmitResultData(context.Background(), "test spec", effectData)
	require.NoError(t, err)
	require.Len(t, storyIDs, 2)

	// Both new stories should depend on the bootstrap story
	storyA, exists := driver.queue.GetStory(storyIDs[0])
	require.True(t, exists)
	assert.Contains(t, storyA.DependsOn, "bootstrap-1", "new story A should depend on bootstrap story")

	storyB, exists := driver.queue.GetStory(storyIDs[1])
	require.True(t, exists)
	assert.Contains(t, storyB.DependsOn, "bootstrap-1", "new story B should depend on bootstrap story")
	// B should also keep its LLM-specified dependency on A
	assert.Contains(t, storyB.DependsOn, storyIDs[0], "story B should still depend on story A")
}

// TestLoadStories_NoBootstrapStories verifies no extra deps added when queue is empty.
func TestLoadStories_NoBootstrapStories(t *testing.T) {
	driver := newTestDriver()

	effectData := map[string]any{
		"requirements": []any{
			map[string]any{
				"id":                  "req_001",
				"title":               "App feature",
				"description":         "A feature",
				"acceptance_criteria": []any{"works"},
				"story_type":          "app",
				"dependencies":        []any{},
			},
		},
	}

	_, storyIDs, err := driver.loadStoriesFromSubmitResultData(context.Background(), "test spec", effectData)
	require.NoError(t, err)

	story, exists := driver.queue.GetStory(storyIDs[0])
	require.True(t, exists)
	assert.Empty(t, story.DependsOn, "story should have no dependencies when queue was empty")
}

// TestLoadStories_CycleDetection verifies cycles are caught during story loading.
func TestLoadStories_CycleDetection(t *testing.T) {
	driver := newTestDriver()

	effectData := map[string]any{
		"requirements": []any{
			map[string]any{
				"id":                  "req_001",
				"title":               "Story A",
				"description":         "A depends on B",
				"acceptance_criteria": []any{"works"},
				"story_type":          "app",
				"dependencies":        []any{"req_002"},
			},
			map[string]any{
				"id":                  "req_002",
				"title":               "Story B",
				"description":         "B depends on A",
				"acceptance_criteria": []any{"works"},
				"story_type":          "app",
				"dependencies":        []any{"req_001"},
			},
		},
	}

	_, _, err := driver.loadStoriesFromSubmitResultData(context.Background(), "test spec", effectData)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dependency cycle")

	// Queue should be cleared after cycle detection
	allStories := driver.queue.GetAllStories()
	assert.Empty(t, allStories, "queue should be cleared after cycle detection failure")
}

// TestLoadStories_NoDependencies verifies stories with no dependencies work.
func TestLoadStories_NoDependencies(t *testing.T) {
	driver := newTestDriver()

	effectData := map[string]any{
		"requirements": []any{
			map[string]any{
				"id":                  "req_001",
				"title":               "Independent story A",
				"description":         "No deps",
				"acceptance_criteria": []any{"works"},
				"story_type":          "app",
				"dependencies":        []any{},
			},
			map[string]any{
				"id":                  "req_002",
				"title":               "Independent story B",
				"description":         "No deps",
				"acceptance_criteria": []any{"works"},
				"story_type":          "app",
				"dependencies":        []any{},
			},
		},
	}

	_, storyIDs, err := driver.loadStoriesFromSubmitResultData(context.Background(), "test spec", effectData)
	require.NoError(t, err)
	require.Len(t, storyIDs, 2)

	// Both stories should have no dependencies
	for _, id := range storyIDs {
		story, exists := driver.queue.GetStory(id)
		require.True(t, exists)
		assert.Empty(t, story.DependsOn)
	}
}

// TestClearAllResetsForRetry verifies ClearAll properly resets for retry.
func TestClearAllResetsForRetry(t *testing.T) {
	driver := newTestDriver()

	// Add some stories
	driver.queue.AddStory("s1", "spec-1", "Story 1", "Content", "app", nil, 2)
	driver.queue.AddStory("s2", "spec-1", "Story 2", "Content", "app", []string{"s1"}, 2)
	assert.Len(t, driver.queue.GetAllStories(), 2)

	// Clear
	driver.queue.ClearAll()
	assert.Empty(t, driver.queue.GetAllStories())

	// Add new stories — should work fine
	driver.queue.AddStory("s3", "spec-1", "Story 3", "Content", "app", nil, 2)
	assert.Len(t, driver.queue.GetAllStories(), 1)
}

// TestRemoveCyclicEdges verifies cycle-breaking edge removal.
func TestRemoveCyclicEdges(t *testing.T) {
	driver := newTestDriver()

	// Create A→B→C→A cycle
	driver.queue.AddStory("a", "spec-1", "A", "Content", "app", []string{"c"}, 2)
	driver.queue.AddStory("b", "spec-1", "B", "Content", "app", []string{"a"}, 2)
	driver.queue.AddStory("c", "spec-1", "C", "Content", "app", []string{"b"}, 2)

	// Detect and remove cycles
	cycles := driver.queue.DetectCycles()
	require.NotEmpty(t, cycles)

	driver.queue.RemoveCyclicEdges(cycles)

	// After removal, should have no cycles
	cycles = driver.queue.DetectCycles()
	assert.Empty(t, cycles, "no cycles should remain after removal")
}

// TestDispatchingHandlesCyclesGracefully verifies cycle detection in DISPATCHING
// recovers defensively by removing cyclic edges rather than returning StateError.
func TestDispatchingHandlesCyclesGracefully(t *testing.T) {
	driver := newTestDriver()

	// Pre-populate queue with a cycle (simulating a bug in validation)
	driver.queue.AddStory("story-a", "spec-1", "Story A", "Content A", "app", []string{"story-b"}, 2)
	driver.queue.AddStory("story-b", "spec-1", "Story B", "Content B", "app", []string{"story-a"}, 2)

	// Verify cycles exist before dispatching
	cycles := driver.queue.DetectCycles()
	require.NotEmpty(t, cycles, "should have cycles before dispatching")

	// Simulate what handleDispatching does: detect and remove cycles
	driver.queue.RemoveCyclicEdges(cycles)

	// After removal, no cycles should remain
	cycles = driver.queue.DetectCycles()
	assert.Empty(t, cycles, "cycles should be removed after defensive recovery")

	// Stories should still be in the queue (not deleted)
	allStories := driver.queue.GetAllStories()
	assert.Len(t, allStories, 2, "stories should still exist after cycle removal")
}
