//go:build integration
// +build integration

// Package integration contains integration tests for the Maestro orchestrator.
package integration

import (
	"os"
	"path/filepath"
	"testing"

	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/persistence"
)

// TestResumeModeSessionLifecycle tests the complete session lifecycle:
// create -> active -> shutdown -> resume -> active
func TestResumeModeSessionLifecycle(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "resume-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("Failed to create .maestro dir: %v", err)
	}

	// Initialize database
	dbPath := filepath.Join(maestroDir, "maestro.db")
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	sessionID := "test-session-001"
	configJSON := `{"session_id": "test-session-001", "agents": {"max_coders": 2}}`

	// Test 1: Create session
	t.Run("create_session", func(t *testing.T) {
		err := persistence.CreateSession(db, sessionID, configJSON)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		// Verify session exists with 'active' status
		session, err := persistence.GetSession(db, sessionID)
		if err != nil {
			t.Fatalf("Failed to get session: %v", err)
		}
		if session.Status != persistence.SessionStatusActive {
			t.Errorf("Expected status 'active', got '%s'", session.Status)
		}
		if session.ConfigJSON != configJSON {
			t.Errorf("Config mismatch")
		}
		t.Logf("✅ Created session with status: %s", session.Status)
	})

	// Test 2: Add an incomplete story (required for resumable session)
	t.Run("add_story", func(t *testing.T) {
		ops := persistence.NewDatabaseOperations(db, sessionID)
		// First create a spec (required for foreign key)
		err := ops.UpsertSpec(&persistence.Spec{
			ID:      "spec-001",
			Content: "Test spec content",
		})
		if err != nil {
			t.Fatalf("Failed to create spec: %v", err)
		}
		// Now create the story
		err = ops.BatchUpsertStoriesWithDependencies(&persistence.BatchUpsertStoriesWithDependenciesRequest{
			Stories: []*persistence.Story{
				{
					ID:        "story-001",
					SpecID:    "spec-001",
					Title:     "Test Story",
					Content:   "Test content",
					Status:    "new",
					StoryType: "app",
				},
			},
		})
		if err != nil {
			t.Fatalf("Failed to create story: %v", err)
		}
		t.Logf("✅ Created incomplete story for session")
	})

	// Test 3: Update to shutdown status (makes it resumable)
	t.Run("shutdown_session", func(t *testing.T) {
		err := persistence.UpdateSessionStatus(db, sessionID, persistence.SessionStatusShutdown)
		if err != nil {
			t.Fatalf("Failed to update session status: %v", err)
		}

		session, err := persistence.GetSession(db, sessionID)
		if err != nil {
			t.Fatalf("Failed to get session: %v", err)
		}
		if session.Status != persistence.SessionStatusShutdown {
			t.Errorf("Expected status 'shutdown', got '%s'", session.Status)
		}
		if session.EndedAt == nil {
			t.Error("Expected ended_at to be set")
		}
		t.Logf("✅ Session marked as shutdown at: %v", session.EndedAt)
	})

	// Test 3: Query for resumable session
	t.Run("find_resumable_session", func(t *testing.T) {
		session, err := persistence.GetMostRecentResumableSession(db)
		if err != nil {
			t.Fatalf("Failed to find resumable session: %v", err)
		}
		if session == nil {
			t.Fatal("Expected to find resumable session, got nil")
		}
		if session.Session.SessionID != sessionID {
			t.Errorf("Expected session ID '%s', got '%s'", sessionID, session.Session.SessionID)
		}
		t.Logf("✅ Found resumable session: %s", session.Session.SessionID)
	})

	// Test 4: Resume (set back to active)
	t.Run("resume_session", func(t *testing.T) {
		err := persistence.UpdateSessionStatus(db, sessionID, persistence.SessionStatusActive)
		if err != nil {
			t.Fatalf("Failed to resume session: %v", err)
		}

		session, err := persistence.GetSession(db, sessionID)
		if err != nil {
			t.Fatalf("Failed to get session: %v", err)
		}
		if session.Status != persistence.SessionStatusActive {
			t.Errorf("Expected status 'active', got '%s'", session.Status)
		}
		t.Logf("✅ Session resumed with status: %s", session.Status)
	})

	// Test 5: No resumable session when completed
	// Note: Active sessions ARE now resumable (treated as crashed)
	// Only 'completed' sessions are not resumable
	t.Run("no_resumable_when_completed", func(t *testing.T) {
		// Mark session as completed (all work done, not resumable)
		err := persistence.UpdateSessionStatus(db, sessionID, persistence.SessionStatusCompleted)
		if err != nil {
			t.Fatalf("Failed to update session to completed: %v", err)
		}

		session, err := persistence.GetMostRecentResumableSession(db)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if session != nil {
			t.Errorf("Expected no resumable session, got: %s", session.Session.SessionID)
		}
		t.Logf("✅ Correctly found no resumable session when session is completed")
	})
}

// TestResumeModeNoSession tests the case when no resumable session exists.
func TestResumeModeNoSession(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "resume-test-no-session-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("Failed to create .maestro dir: %v", err)
	}

	// Initialize database (fresh, no sessions)
	dbPath := filepath.Join(maestroDir, "maestro.db")
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Query for resumable session - should return nil, nil (not an error)
	session, err := persistence.GetMostRecentResumableSession(db)
	if err != nil {
		t.Fatalf("Unexpected error when no session exists: %v", err)
	}
	if session != nil {
		t.Errorf("Expected nil session, got: %s", session.Session.SessionID)
	}
	t.Logf("✅ Correctly returns nil when no resumable session exists")
}

// TestResumeModeStaleSessionDetection tests that stale sessions are marked as crashed.
func TestResumeModeStaleSessionDetection(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "resume-test-stale-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("Failed to create .maestro dir: %v", err)
	}

	// Initialize database
	dbPath := filepath.Join(maestroDir, "maestro.db")
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Create a session that appears to have crashed (status = active but we're "restarting")
	sessionID := "test-stale-session"
	configJSON := `{"session_id": "test-stale-session"}`

	err = persistence.CreateSession(db, sessionID, configJSON)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Simulate startup - mark stale sessions as crashed
	staleCount, err := persistence.MarkStaleSessions(db)
	if err != nil {
		t.Fatalf("Failed to mark stale sessions: %v", err)
	}
	if staleCount != 1 {
		t.Errorf("Expected 1 stale session, got %d", staleCount)
	}

	// Verify session is now crashed
	session, err := persistence.GetSession(db, sessionID)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}
	if session.Status != persistence.SessionStatusCrashed {
		t.Errorf("Expected status 'crashed', got '%s'", session.Status)
	}

	// Crashed session should NOT be resumable
	resumable, err := persistence.GetMostRecentResumableSession(db)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if resumable != nil {
		t.Errorf("Crashed session should not be resumable")
	}

	t.Logf("✅ Stale session correctly marked as crashed and not resumable")
}

// TestResumeModeCoderStatePersistence tests coder state serialization and restoration.
func TestResumeModeCoderStatePersistence(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "resume-test-coder-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("Failed to create .maestro dir: %v", err)
	}

	// Initialize database
	dbPath := filepath.Join(maestroDir, "maestro.db")
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	sessionID := "test-coder-state-session"
	agentID := "coder-001"
	storyID := "story-123"
	planJSON := `{"approach": "test plan", "todos": ["todo1", "todo2"]}`
	todoListJSON := `[{"task": "todo1", "done": true}, {"task": "todo2", "done": false}]`
	knowledgeJSON := `{"files": ["main.go", "util.go"]}`

	// Save coder state
	state := &persistence.CoderState{
		SessionID:         sessionID,
		AgentID:           agentID,
		StoryID:           &storyID,
		State:             "CODING",
		PlanJSON:          &planJSON,
		TodoListJSON:      &todoListJSON,
		CurrentTodoIndex:  1,
		KnowledgePackJSON: &knowledgeJSON,
	}

	err = persistence.SaveCoderState(db, state)
	if err != nil {
		t.Fatalf("Failed to save coder state: %v", err)
	}

	// Restore coder state
	restored, err := persistence.GetCoderState(db, sessionID, agentID)
	if err != nil {
		t.Fatalf("Failed to get coder state: %v", err)
	}

	// Verify all fields
	if restored.State != "CODING" {
		t.Errorf("State mismatch: expected 'CODING', got '%s'", restored.State)
	}
	if restored.StoryID == nil || *restored.StoryID != storyID {
		t.Errorf("StoryID mismatch")
	}
	if restored.CurrentTodoIndex != 1 {
		t.Errorf("CurrentTodoIndex mismatch: expected 1, got %d", restored.CurrentTodoIndex)
	}
	if restored.PlanJSON == nil || *restored.PlanJSON != planJSON {
		t.Errorf("PlanJSON mismatch")
	}
	if restored.TodoListJSON == nil || *restored.TodoListJSON != todoListJSON {
		t.Errorf("TodoListJSON mismatch")
	}
	if restored.KnowledgePackJSON == nil || *restored.KnowledgePackJSON != knowledgeJSON {
		t.Errorf("KnowledgePackJSON mismatch")
	}

	t.Logf("✅ Coder state correctly persisted and restored")
}

// TestResumeModeArchitectStatePersistence tests architect state serialization and restoration.
func TestResumeModeArchitectStatePersistence(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "resume-test-architect-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("Failed to create .maestro dir: %v", err)
	}

	// Initialize database
	dbPath := filepath.Join(maestroDir, "maestro.db")
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	sessionID := "test-architect-state-session"
	escalationJSON := `{"coder-001": 3, "coder-002": 1}`

	// Save architect state
	state := &persistence.ArchitectState{
		SessionID:            sessionID,
		State:                "DISPATCHING",
		EscalationCountsJSON: &escalationJSON,
	}

	err = persistence.SaveArchitectState(db, state)
	if err != nil {
		t.Fatalf("Failed to save architect state: %v", err)
	}

	// Restore architect state
	restored, err := persistence.GetArchitectState(db, sessionID)
	if err != nil {
		t.Fatalf("Failed to get architect state: %v", err)
	}

	// Verify fields
	if restored.State != "DISPATCHING" {
		t.Errorf("State mismatch: expected 'DISPATCHING', got '%s'", restored.State)
	}
	if restored.EscalationCountsJSON == nil || *restored.EscalationCountsJSON != escalationJSON {
		t.Errorf("EscalationCountsJSON mismatch")
	}

	t.Logf("✅ Architect state correctly persisted and restored")
}

// TestResumeModePMStatePersistence tests PM state serialization and restoration.
func TestResumeModePMStatePersistence(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "resume-test-pm-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("Failed to create .maestro dir: %v", err)
	}

	// Initialize database
	dbPath := filepath.Join(maestroDir, "maestro.db")
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	sessionID := "test-pm-state-session"
	specContent := "# Test Spec\n\nThis is a test specification."
	bootstrapJSON := `{"git_url": "https://github.com/test/repo"}`

	// Save PM state
	state := &persistence.PMState{
		SessionID:           sessionID,
		State:               "AWAIT_ARCHITECT",
		SpecContent:         &specContent,
		BootstrapParamsJSON: &bootstrapJSON,
	}

	err = persistence.SavePMState(db, state)
	if err != nil {
		t.Fatalf("Failed to save PM state: %v", err)
	}

	// Restore PM state
	restored, err := persistence.GetPMState(db, sessionID)
	if err != nil {
		t.Fatalf("Failed to get PM state: %v", err)
	}

	// Verify fields
	if restored.State != "AWAIT_ARCHITECT" {
		t.Errorf("State mismatch: expected 'AWAIT_ARCHITECT', got '%s'", restored.State)
	}
	if restored.SpecContent == nil || *restored.SpecContent != specContent {
		t.Errorf("SpecContent mismatch")
	}
	if restored.BootstrapParamsJSON == nil || *restored.BootstrapParamsJSON != bootstrapJSON {
		t.Errorf("BootstrapParamsJSON mismatch")
	}

	t.Logf("✅ PM state correctly persisted and restored")
}

// TestResumeModeContextManagerSerialization tests context manager serialization round-trip.
func TestResumeModeContextManagerSerialization(t *testing.T) {
	// Create a context manager with various message types
	cm := contextmgr.NewContextManager()

	// Set system prompt
	cm.ResetSystemPrompt("You are a helpful coding assistant.")

	// Add messages using the proper API
	cm.AddMessage("user-request", "Please help me write a function.")

	// Serialize
	data, err := cm.Serialize()
	if err != nil {
		t.Fatalf("Failed to serialize context manager: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("Serialized data is empty")
	}

	t.Logf("Serialized %d bytes", len(data))

	// Create new context manager and deserialize
	cm2 := contextmgr.NewContextManager()
	err = cm2.Deserialize(data)
	if err != nil {
		t.Fatalf("Failed to deserialize context manager: %v", err)
	}

	// Verify system prompt was restored
	sysPrompt := cm2.SystemPrompt()
	if sysPrompt == nil || sysPrompt.Content != "You are a helpful coding assistant." {
		t.Errorf("System prompt not restored correctly")
	}

	t.Logf("✅ Context manager serialization round-trip successful")
}

// TestResumeModeAgentContextPersistence tests agent context persistence to database.
func TestResumeModeAgentContextPersistence(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "resume-test-context-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("Failed to create .maestro dir: %v", err)
	}

	// Initialize database
	dbPath := filepath.Join(maestroDir, "maestro.db")
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	sessionID := "test-context-session"
	agentID := "architect-001"
	contextType := "coder-001" // Architect's context for coder-001

	// Create context with messages
	cm := contextmgr.NewContextManager()
	cm.ResetSystemPrompt("You are the architect agent.")
	cm.AddMessage("user-request", "Review this code.")

	// Serialize context
	messagesJSON, err := cm.Serialize()
	if err != nil {
		t.Fatalf("Failed to serialize context: %v", err)
	}

	if len(messagesJSON) == 0 {
		t.Fatal("Serialized context is empty")
	}

	// Save to database
	agentCtx := &persistence.AgentContext{
		SessionID:    sessionID,
		AgentID:      agentID,
		ContextType:  contextType,
		MessagesJSON: string(messagesJSON),
	}

	err = persistence.SaveAgentContext(db, agentCtx)
	if err != nil {
		t.Fatalf("Failed to save agent context: %v", err)
	}

	// Retrieve from database
	contexts, err := persistence.GetAgentContexts(db, sessionID, agentID)
	if err != nil {
		t.Fatalf("Failed to get agent contexts: %v", err)
	}

	if len(contexts) != 1 {
		t.Fatalf("Expected 1 context, got %d", len(contexts))
	}

	// Deserialize and verify
	cm2 := contextmgr.NewContextManager()
	err = cm2.Deserialize([]byte(contexts[0].MessagesJSON))
	if err != nil {
		t.Fatalf("Failed to deserialize context: %v", err)
	}

	// Verify system prompt was restored
	sysPrompt := cm2.SystemPrompt()
	if sysPrompt == nil || sysPrompt.Content != "You are the architect agent." {
		t.Errorf("System prompt not restored correctly")
	}

	t.Logf("✅ Agent context persistence round-trip successful")
}

// TestResumeModeMultipleCoderStates tests handling multiple coders at different states.
func TestResumeModeMultipleCoderStates(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "resume-test-multi-coder-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("Failed to create .maestro dir: %v", err)
	}

	// Initialize database
	dbPath := filepath.Join(maestroDir, "maestro.db")
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	sessionID := "test-multi-coder-session"

	// Create 3 coders at different states
	coderStates := []struct {
		agentID string
		state   string
		todoIdx int
	}{
		{"coder-001", "PLANNING", 0},
		{"coder-002", "CODING", 3},
		{"coder-003", "TESTING", 5},
	}

	// Save all states
	for _, cs := range coderStates {
		state := &persistence.CoderState{
			SessionID:        sessionID,
			AgentID:          cs.agentID,
			State:            cs.state,
			CurrentTodoIndex: cs.todoIdx,
		}
		if err := persistence.SaveCoderState(db, state); err != nil {
			t.Fatalf("Failed to save state for %s: %v", cs.agentID, err)
		}
	}

	// Retrieve all states
	states, err := persistence.GetAllCoderStates(db, sessionID)
	if err != nil {
		t.Fatalf("Failed to get all coder states: %v", err)
	}

	if len(states) != 3 {
		t.Fatalf("Expected 3 states, got %d", len(states))
	}

	// Verify each state
	stateMap := make(map[string]*persistence.CoderState)
	for i := range states {
		stateMap[states[i].AgentID] = &states[i]
	}

	for _, expected := range coderStates {
		got, ok := stateMap[expected.agentID]
		if !ok {
			t.Errorf("Missing state for %s", expected.agentID)
			continue
		}
		if got.State != expected.state {
			t.Errorf("%s: expected state %s, got %s", expected.agentID, expected.state, got.State)
		}
		if got.CurrentTodoIndex != expected.todoIdx {
			t.Errorf("%s: expected todo index %d, got %d", expected.agentID, expected.todoIdx, got.CurrentTodoIndex)
		}
	}

	t.Logf("✅ Multiple coder states correctly persisted and retrieved")
}

// TestResumeModeUpdateSessionStatusRowsAffected tests that UpdateSessionStatus fails when session doesn't exist.
func TestResumeModeUpdateSessionStatusRowsAffected(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "resume-test-rows-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("Failed to create .maestro dir: %v", err)
	}

	// Initialize database
	dbPath := filepath.Join(maestroDir, "maestro.db")
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Try to update a non-existent session
	err = persistence.UpdateSessionStatus(db, "non-existent-session", persistence.SessionStatusShutdown)
	if err == nil {
		t.Error("Expected error when updating non-existent session")
	}
	if err != persistence.ErrSessionNotFound {
		t.Errorf("Expected ErrSessionNotFound, got: %v", err)
	}

	t.Logf("✅ UpdateSessionStatus correctly returns ErrSessionNotFound for missing session")
}

// TestResumeModeCleanupOldSessionData tests that old session data is cleaned up.
func TestResumeModeCleanupOldSessionData(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "resume-test-cleanup-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("Failed to create .maestro dir: %v", err)
	}

	// Initialize database
	dbPath := filepath.Join(maestroDir, "maestro.db")
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Create data for two sessions
	session1 := "old-session"
	session2 := "new-session"

	// Save state for old session
	oldState := &persistence.CoderState{
		SessionID: session1,
		AgentID:   "coder-001",
		State:     "CODING",
	}
	if err := persistence.SaveCoderState(db, oldState); err != nil {
		t.Fatalf("Failed to save old state: %v", err)
	}

	// Save state for new session
	newState := &persistence.CoderState{
		SessionID: session2,
		AgentID:   "coder-001",
		State:     "TESTING",
	}
	if err := persistence.SaveCoderState(db, newState); err != nil {
		t.Fatalf("Failed to save new state: %v", err)
	}

	// Cleanup old session data
	err = persistence.CleanupOldSessionData(db, session2)
	if err != nil {
		t.Fatalf("Failed to cleanup old session data: %v", err)
	}

	// Verify old session data is gone
	_, err = persistence.GetCoderState(db, session1, "coder-001")
	if err != persistence.ErrSessionNotFound {
		t.Errorf("Expected old session data to be deleted, got: %v", err)
	}

	// Verify new session data still exists
	state, err := persistence.GetCoderState(db, session2, "coder-001")
	if err != nil {
		t.Fatalf("New session data should exist: %v", err)
	}
	if state.State != "TESTING" {
		t.Errorf("New session data corrupted")
	}

	t.Logf("✅ Old session data correctly cleaned up")
}
