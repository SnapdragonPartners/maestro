package architect

import (
	"testing"
	"time"

	"orchestrator/pkg/persistence"
)

func TestQueueLoadStoriesFromDB(t *testing.T) {
	queue := NewQueue(nil)

	// Create some stories
	stories := []*persistence.Story{
		{
			ID:        "story-a",
			SpecID:    "spec-a",
			Title:     "Story A",
			Content:   "Content A",
			Status:    "new",
			Priority:  1,
			StoryType: "app",
			CreatedAt: time.Now(),
		},
		{
			ID:        "story-b",
			SpecID:    "spec-a",
			Title:     "Story B",
			Content:   "Content B",
			Status:    "coding",
			Priority:  2,
			StoryType: "devops",
			CreatedAt: time.Now(),
		},
	}

	// Load stories
	loaded := queue.LoadStoriesFromDB(stories)

	if loaded != 2 {
		t.Errorf("Expected 2 stories loaded, got %d", loaded)
	}

	// Verify stories are in the queue
	allStories := queue.GetAllStories()
	if len(allStories) != 2 {
		t.Errorf("Expected 2 stories in queue, got %d", len(allStories))
	}

	// Verify specific story
	storyA, exists := queue.GetStory("story-a")
	if !exists {
		t.Fatal("Expected story-a to be in queue")
	}
	if storyA.Title != "Story A" {
		t.Errorf("Expected title 'Story A', got '%s'", storyA.Title)
	}
	if storyA.Status != "new" {
		t.Errorf("Expected status 'new', got '%s'", storyA.Status)
	}
}

func TestQueueLoadStoriesFromDB_ClearsExisting(t *testing.T) {
	queue := NewQueue(nil)

	// Add an existing story
	queue.AddStory("existing-story", "spec-x", "Existing Story", "Content", "app", nil, 1)

	// Verify existing story is there
	_, exists := queue.GetStory("existing-story")
	if !exists {
		t.Fatal("Expected existing story to be present before load")
	}

	// Load new stories
	newStories := []*persistence.Story{
		{
			ID:        "new-story",
			SpecID:    "spec-y",
			Title:     "New Story",
			Content:   "Content",
			Status:    "new",
			Priority:  1,
			StoryType: "app",
			CreatedAt: time.Now(),
		},
	}
	loaded := queue.LoadStoriesFromDB(newStories)

	if loaded != 1 {
		t.Errorf("Expected 1 story loaded, got %d", loaded)
	}

	// Existing story should be gone
	_, exists = queue.GetStory("existing-story")
	if exists {
		t.Error("Expected existing story to be cleared")
	}

	// New story should be present
	newStory, exists := queue.GetStory("new-story")
	if !exists {
		t.Fatal("Expected new story to be loaded")
	}
	if newStory.Title != "New Story" {
		t.Errorf("Expected title 'New Story', got '%s'", newStory.Title)
	}
}

func TestQueueLoadStoriesFromDB_Empty(t *testing.T) {
	queue := NewQueue(nil)

	// Add an existing story first
	queue.AddStory("existing-story", "spec-x", "Existing Story", "Content", "app", nil, 1)

	// Load empty stories list
	loaded := queue.LoadStoriesFromDB([]*persistence.Story{})

	if loaded != 0 {
		t.Errorf("Expected 0 stories loaded, got %d", loaded)
	}

	// Queue should be empty
	allStories := queue.GetAllStories()
	if len(allStories) != 0 {
		t.Errorf("Expected 0 stories in queue, got %d", len(allStories))
	}
}

func TestQueueLoadStoriesFromDB_PreservesAllFields(t *testing.T) {
	queue := NewQueue(nil)

	now := time.Now()
	completedAt := now.Add(time.Hour)

	stories := []*persistence.Story{
		{
			ID:              "story-full",
			SpecID:          "spec-full",
			Title:           "Full Story",
			Content:         "Full Content",
			Status:          "review",
			Priority:        5,
			ApprovedPlan:    "The Plan",
			StoryType:       "devops",
			CreatedAt:       now,
			StartedAt:       &now,
			CompletedAt:     &completedAt,
			AssignedAgent:   "coder-001",
			EstimatedPoints: 3,
			DependsOn:       []string{"story-dep-1", "story-dep-2"},
		},
	}

	queue.LoadStoriesFromDB(stories)

	story, exists := queue.GetStory("story-full")
	if !exists {
		t.Fatal("Expected story-full to be in queue")
	}

	// Verify all fields preserved
	if story.ID != "story-full" {
		t.Errorf("Expected ID 'story-full', got '%s'", story.ID)
	}
	if story.SpecID != "spec-full" {
		t.Errorf("Expected SpecID 'spec-full', got '%s'", story.SpecID)
	}
	if story.Title != "Full Story" {
		t.Errorf("Expected Title 'Full Story', got '%s'", story.Title)
	}
	if story.Content != "Full Content" {
		t.Errorf("Expected Content 'Full Content', got '%s'", story.Content)
	}
	if story.Status != "review" {
		t.Errorf("Expected Status 'review', got '%s'", story.Status)
	}
	if story.Priority != 5 {
		t.Errorf("Expected Priority 5, got %d", story.Priority)
	}
	if story.ApprovedPlan != "The Plan" {
		t.Errorf("Expected ApprovedPlan 'The Plan', got '%s'", story.ApprovedPlan)
	}
	if story.StoryType != "devops" {
		t.Errorf("Expected StoryType 'devops', got '%s'", story.StoryType)
	}
	if story.AssignedAgent != "coder-001" {
		t.Errorf("Expected AssignedAgent 'coder-001', got '%s'", story.AssignedAgent)
	}
	if story.EstimatedPoints != 3 {
		t.Errorf("Expected EstimatedPoints 3, got %d", story.EstimatedPoints)
	}
	if len(story.DependsOn) != 2 {
		t.Errorf("Expected 2 dependencies, got %d", len(story.DependsOn))
	}
}
