// Package kernel provides shared infrastructure management for the orchestrator.
// It consolidates dispatcher, persistence, build services, and other core components
// that were previously duplicated between bootstrap and main orchestrator flows.
package kernel

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // SQLite driver

	"orchestrator/internal/state"
	"orchestrator/pkg/agent"
	"orchestrator/pkg/build"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/webui"
)

// Kernel manages shared infrastructure components used by both bootstrap and main flows.
// It provides a single source of truth for infrastructure lifecycle and eliminates
// duplication between different orchestrator modes.
type Kernel struct {
	// Context is embedded rather than a field to avoid containedctx lint error
	ctx    context.Context //nolint:containedctx // Required for kernel lifecycle management
	cancel context.CancelFunc

	// Configuration and logging
	Config *config.Config
	Logger *logx.Logger

	// Core infrastructure services (concrete types, no over-abstraction)
	Dispatcher            *dispatch.Dispatcher
	Database              *sql.DB
	PersistenceChannel    chan *persistence.Request
	persistenceWorkerDone chan struct{} // Signals when persistence worker has finished draining
	BuildService          *build.Service
	ChatService           *chat.Service
	WebServer             *webui.Server
	LLMFactory            *agent.LLMClientFactory // Shared LLM client factory for all agents
	ComposeRegistry       *state.ComposeRegistry  // Registry for active Docker Compose stacks

	// Runtime state
	projectDir string
	running    bool
}

// NewKernel creates a new kernel with shared infrastructure components.
// This consolidates the setup logic that was previously duplicated between
// bootstrap.go and main.go.
func NewKernel(parent context.Context, cfg *config.Config, projectDir string) (*Kernel, error) {
	ctx, cancel := context.WithCancel(parent)

	k := &Kernel{
		ctx:        ctx,
		cancel:     cancel,
		Config:     cfg,
		Logger:     logx.NewLogger("kernel"),
		projectDir: projectDir,
		running:    false,
	}

	// Initialize core services
	// Note: initializeServices calls NewLLMClientFactory which uses context.Background() internally
	if err := k.initializeServices(); err != nil { //nolint:contextcheck // LLM factory uses background context for lifecycle
		cancel()
		return nil, fmt.Errorf("failed to initialize kernel services: %w", err)
	}

	return k, nil
}

// initializeServices sets up all the core infrastructure services.
func (k *Kernel) initializeServices() error {
	// Create dispatcher
	var err error
	k.Dispatcher, err = dispatch.NewDispatcher(k.Config)
	if err != nil {
		return fmt.Errorf("failed to create dispatcher: %w", err)
	}

	// Initialize database
	err = k.initializeDatabase()
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	// Create build service
	k.BuildService = build.NewBuildService()

	// Create compose registry for tracking active Docker Compose stacks
	k.ComposeRegistry = state.NewComposeRegistry()

	// Create chat service
	dbOps := persistence.NewDatabaseOperations(k.Database, k.Config.SessionID)
	k.ChatService = chat.NewService(dbOps, k.Config.Chat)

	// Create shared LLM client factory (used by all agents)
	// Note: NewLLMClientFactory uses context.Background() internally for rate limiter lifecycle
	k.LLMFactory, err = agent.NewLLMClientFactory(k.Config) //nolint:contextcheck // Factory uses background context internally
	if err != nil {
		return fmt.Errorf("failed to create LLM client factory: %w", err)
	}

	// Create web server (will be started conditionally)
	k.WebServer = webui.NewServer(k.Dispatcher, k.projectDir, k.ChatService, k.LLMFactory)

	k.Logger.Info("Kernel services initialized successfully")
	return nil
}

// initializeDatabase sets up the database connection and persistence channel.
func (k *Kernel) initializeDatabase() error {
	// Create maestro directory if it doesn't exist
	maestroDir := filepath.Join(k.projectDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return fmt.Errorf("failed to create maestro directory: %w", err)
	}

	// Database path
	dbPath := filepath.Join(maestroDir, "maestro.db")

	// Initialize database with schema using persistence package
	var err error
	k.Database, err = persistence.InitializeDatabase(dbPath)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	// Create persistence channel
	k.PersistenceChannel = make(chan *persistence.Request, 100)

	k.Logger.Info("Database initialized with schema: %s", dbPath)
	return nil
}

// Start begins all kernel services in the correct order.
// This replaces the scattered startup logic from the old orchestrator files.
func (k *Kernel) Start() error {
	if k.running {
		return fmt.Errorf("kernel already running")
	}

	k.Logger.Info("Starting kernel services...")

	// Start dispatcher first
	if err := k.Dispatcher.Start(k.ctx); err != nil {
		return fmt.Errorf("failed to start dispatcher: %w", err)
	}

	// Start persistence worker (after dispatcher)
	k.startPersistenceWorker()

	// Create session record in database (required for resume mode)
	// This must happen after database is initialized but before agents start
	if err := k.createSessionRecord(); err != nil {
		return fmt.Errorf("failed to create session record: %w", err)
	}

	k.running = true
	k.Logger.Info("Kernel services started successfully")
	return nil
}

// StartWebUI conditionally starts the web UI server using configuration settings.
func (k *Kernel) StartWebUI() error {
	if k.WebServer == nil {
		return fmt.Errorf("web server not initialized")
	}

	if k.Config.WebUI == nil {
		return fmt.Errorf("webui config not found")
	}

	cfg := k.Config.WebUI
	k.Logger.Info("Starting web UI on %s:%d (SSL: %v)", cfg.Host, cfg.Port, cfg.SSL)
	if err := k.WebServer.StartServer(k.ctx, cfg.Host, cfg.Port, cfg.SSL, cfg.Cert, cfg.Key); err != nil {
		return fmt.Errorf("failed to start web server: %w", err)
	}
	return nil
}

// Stop gracefully shuts down all kernel services.
func (k *Kernel) Stop() error {
	if !k.running {
		return nil
	}

	k.Logger.Info("Stopping kernel services...")

	// Cleanup compose stacks before cancelling context.
	// Use a timeout context for cleanup since main context will be cancelled.
	if k.ComposeRegistry != nil && k.ComposeRegistry.Count() > 0 {
		k.Logger.Info("🐳 Cleaning up %d Docker Compose stacks...", k.ComposeRegistry.Count())
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := k.ComposeRegistry.Cleanup(cleanupCtx); err != nil {
			k.Logger.Warn("⚠️ Error during compose cleanup: %v", err)
		}
		k.Logger.Info("✅ Compose cleanup complete")
	}

	// Cancel context FIRST to stop all producers from sending to persistence channel.
	// This prevents "send on closed channel" panics when we drain the queue.
	k.cancel()

	// Stop dispatcher (it will notice context cancellation).
	if k.Dispatcher != nil {
		// Use a fresh context since k.ctx is now cancelled.
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := k.Dispatcher.Stop(stopCtx); err != nil {
			k.Logger.Error("Error stopping dispatcher: %v", err)
		}
		stopCancel()
	}

	// Stop web server (it notices context cancellation).
	if k.WebServer != nil {
		k.Logger.Info("Web server stopping via context cancellation")
	}

	// Stop LLM factory (stops rate limiter refill timers).
	if k.LLMFactory != nil {
		k.LLMFactory.Stop()
		k.Logger.Info("LLM factory stopped (rate limiter refill timers terminated)")
	}

	// Now that producers are stopped, drain persistence queue BEFORE closing database.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := k.DrainPersistenceQueue(drainCtx); err != nil {
		k.Logger.Warn("Persistence queue drain issue: %v", err)
	}
	drainCancel()

	// Close database AFTER persistence queue is drained.
	if k.Database != nil {
		if err := k.Database.Close(); err != nil {
			k.Logger.Error("Error closing database: %v", err)
		}
	}

	k.running = false
	k.Logger.Info("Kernel services stopped")
	return nil
}

// ProjectDir returns the project directory path.
func (k *Kernel) ProjectDir() string {
	return k.projectDir
}

// DrainPersistenceQueue closes the persistence channel and waits for pending writes to complete.
// This should be called during graceful shutdown to ensure all state is persisted.
// Returns an error if the drain times out.
func (k *Kernel) DrainPersistenceQueue(ctx context.Context) error {
	if k.PersistenceChannel == nil {
		return nil
	}

	k.Logger.Info("Draining persistence queue...")
	close(k.PersistenceChannel)
	k.PersistenceChannel = nil // Prevent double-close in Stop()

	if k.persistenceWorkerDone == nil {
		return nil
	}

	select {
	case <-k.persistenceWorkerDone:
		k.Logger.Info("Persistence queue drained successfully")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for persistence queue to drain: %w", ctx.Err())
	}
}

// createSessionRecord creates a session record in the database for the current run.
// This is required for resume mode to work - without a session record, shutdown status
// updates will fail and resume will never find any sessions.
func (k *Kernel) createSessionRecord() error {
	// First, mark any stale sessions (active sessions from previous crashed runs) as crashed
	staleCount, err := persistence.MarkStaleSessions(k.Database)
	if err != nil {
		k.Logger.Warn("Failed to mark stale sessions: %v", err)
		// Continue anyway - this is not fatal
	} else if staleCount > 0 {
		k.Logger.Info("Marked %d stale session(s) as crashed", staleCount)
	}

	// Create config snapshot for the session record
	configJSON, err := persistence.ConfigSnapshotToJSON(k.Config)
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	// Create the session record
	if err := persistence.CreateSession(k.Database, k.Config.SessionID, configJSON); err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	k.Logger.Info("Created session record: %s", k.Config.SessionID)
	return nil
}

// startPersistenceWorker begins the database persistence worker goroutine.
// This consolidates the persistence logic that was duplicated between bootstrap and main.
// The worker drains all pending requests before signaling completion via persistenceWorkerDone.
func (k *Kernel) startPersistenceWorker() {
	k.persistenceWorkerDone = make(chan struct{})

	go func() {
		defer close(k.persistenceWorkerDone)
		k.Logger.Debug("Starting persistence worker")

		// Create database operations handler with session isolation.
		// The session ID is passed to ensure all database operations are scoped to the current orchestrator run.
		ops := persistence.NewDatabaseOperations(k.Database, k.Config.SessionID)

		for req := range k.PersistenceChannel {
			if req != nil {
				k.processPersistenceRequest(req, ops)
			}
		}

		k.Logger.Info("Persistence worker finished draining queue")
	}()
}

// processPersistenceRequest handles individual persistence operations.
// This preserves the exact logic from bootstrap.go's processPersistenceRequest.
//
//nolint:cyclop // Simple switch statement for database operations
func (k *Kernel) processPersistenceRequest(req *persistence.Request, ops *persistence.DatabaseOperations) {
	switch req.Operation {
	case persistence.OpUpsertSpec:
		if spec, ok := req.Data.(*persistence.Spec); ok {
			if err := ops.UpsertSpec(spec); err != nil {
				k.Logger.Error("Failed to upsert spec: %v", err)
			} else {
				k.Logger.Info("Successfully upserted spec: %s", spec.ID)
			}
		}

	case persistence.OpUpsertStory:
		if story, ok := req.Data.(*persistence.Story); ok {
			if err := ops.UpsertStory(story); err != nil {
				k.Logger.Error("Failed to upsert story: %v", err)
			} else {
				k.Logger.Info("Successfully upserted story: %s", story.ID)
			}
		}

	case persistence.OpUpdateStoryStatus:
		if statusReq, ok := req.Data.(*persistence.UpdateStoryStatusRequest); ok {
			if err := ops.UpdateStoryStatus(statusReq); err != nil {
				k.Logger.Error("Failed to update story status for %s: %v", statusReq.StoryID, err)
			} else {
				k.Logger.Info("Successfully updated story status: %s -> %s", statusReq.StoryID, statusReq.Status)
			}
		}

	case persistence.OpAddStoryDependency:
		if deps, ok := req.Data.(*persistence.StoryDependency); ok {
			if err := ops.AddStoryDependency(deps.StoryID, deps.DependsOn); err != nil {
				k.Logger.Error("Failed to add story dependency %s -> %s: %v", deps.StoryID, deps.DependsOn, err)
			} else {
				k.Logger.Info("Successfully added story dependency: %s -> %s", deps.StoryID, deps.DependsOn)
			}
		}
	case persistence.OpBatchUpsertStoriesWithDependencies:
		if batchReq, ok := req.Data.(*persistence.BatchUpsertStoriesWithDependenciesRequest); ok {
			if err := ops.BatchUpsertStoriesWithDependencies(batchReq); err != nil {
				k.Logger.Error("Failed to batch upsert stories with dependencies: %v", err)
			} else {
				k.Logger.Info("Successfully batch upserted %d stories with %d dependencies", len(batchReq.Stories), len(batchReq.Dependencies))
			}
		}

	case persistence.OpGetAllStories:
		if req.Response != nil {
			stories, err := ops.GetAllStories()
			if err != nil {
				k.Logger.Error("Failed to get all stories: %v", err)
				req.Response <- err
			} else {
				k.Logger.Info("Successfully retrieved %d stories", len(stories))
				req.Response <- stories
			}
		}

	case persistence.OpUpsertAgentRequest:
		if agentRequest, ok := req.Data.(*persistence.AgentRequest); ok {
			if err := ops.UpsertAgentRequest(agentRequest); err != nil {
				k.Logger.Error("Failed to upsert agent request: %v", err)
			} else {
				k.Logger.Info("Successfully upserted agent request: %s", agentRequest.ID)
			}
		}

	case persistence.OpUpsertAgentResponse:
		if agentResponse, ok := req.Data.(*persistence.AgentResponse); ok {
			if err := ops.UpsertAgentResponse(agentResponse); err != nil {
				k.Logger.Error("Failed to upsert agent response: %v", err)
			} else {
				k.Logger.Info("Successfully upserted agent response: %s", agentResponse.ID)
			}
		}

	case persistence.OpUpsertAgentPlan:
		if agentPlan, ok := req.Data.(*persistence.AgentPlan); ok {
			if err := ops.UpsertAgentPlan(agentPlan); err != nil {
				k.Logger.Error("Failed to upsert agent plan: %v", err)
			} else {
				k.Logger.Info("Successfully upserted agent plan: %s", agentPlan.ID)
			}
		}

	case persistence.OpInsertToolExecution:
		if toolExec, ok := req.Data.(*persistence.ToolExecution); ok {
			if err := ops.InsertToolExecution(toolExec); err != nil {
				k.Logger.Error("Failed to insert tool execution: %v", err)
			} else {
				k.Logger.Debug("Successfully logged tool execution: %s for agent %s", toolExec.ToolName, toolExec.AgentID)
			}
		}

	case persistence.OpStoreKnowledgePack:
		if packReq, ok := req.Data.(*persistence.StoreKnowledgePackRequest); ok {
			if err := ops.StoreKnowledgePack(packReq); err != nil {
				k.Logger.Error("Failed to store knowledge pack: %v", err)
			} else {
				k.Logger.Debug("Successfully stored knowledge pack for story %s", packReq.StoryID)
			}
		}

	case persistence.OpRetrieveKnowledgePack:
		if retrieveReq, ok := req.Data.(*persistence.RetrieveKnowledgePackRequest); ok {
			result, err := ops.RetrieveKnowledgePack(retrieveReq)
			if req.Response != nil {
				if err != nil {
					k.Logger.Error("Failed to retrieve knowledge pack: %v", err)
					req.Response <- err
				} else {
					k.Logger.Debug("Successfully retrieved knowledge pack (%d nodes)", result.Count)
					req.Response <- result
				}
			}
		}

	case persistence.OpCheckKnowledgeModified:
		if checkReq, ok := req.Data.(*persistence.CheckKnowledgeModifiedRequest); ok {
			modified, err := ops.CheckKnowledgeModified(checkReq)
			if req.Response != nil {
				if err != nil {
					k.Logger.Error("Failed to check knowledge modification: %v", err)
					req.Response <- err
				} else {
					k.Logger.Debug("Knowledge modification check: modified=%v", modified)
					req.Response <- modified
				}
			}
		}

	case persistence.OpRebuildKnowledgeIndex:
		if rebuildReq, ok := req.Data.(*persistence.RebuildKnowledgeIndexRequest); ok {
			if err := ops.RebuildKnowledgeIndex(rebuildReq); err != nil {
				k.Logger.Error("Failed to rebuild knowledge index: %v", err)
			} else {
				k.Logger.Info("Successfully rebuilt knowledge index for session %s", rebuildReq.SessionID)
			}
		}

	default:
		k.Logger.Error("Unknown persistence operation: %v", req.Operation)
		if req.Response != nil {
			req.Response <- fmt.Errorf("unknown operation: %v", req.Operation)
		}
	}
}
