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

	_ "github.com/mattn/go-sqlite3" // SQLite driver

	"orchestrator/pkg/build"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/limiter"
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
	RateLimiter        *limiter.Limiter
	Dispatcher         *dispatch.Dispatcher
	Database           *sql.DB
	PersistenceChannel chan *persistence.Request
	BuildService       *build.Service
	ChatService        *chat.Service
	WebServer          *webui.Server

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
	if err := k.initializeServices(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize kernel services: %w", err)
	}

	return k, nil
}

// initializeServices sets up all the core infrastructure services.
func (k *Kernel) initializeServices() error {
	// Create rate limiter
	k.RateLimiter = limiter.NewLimiter(k.Config)

	// Create dispatcher
	var err error
	k.Dispatcher, err = dispatch.NewDispatcher(k.Config, k.RateLimiter)
	if err != nil {
		return fmt.Errorf("failed to create dispatcher: %w", err)
	}

	// Initialize database
	if err := k.initializeDatabase(); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	// Create build service
	k.BuildService = build.NewBuildService()

	// Create chat service
	dbOps := persistence.NewDatabaseOperations(k.Database, k.Config.SessionID)
	k.ChatService = chat.NewService(dbOps, k.Config.Chat)

	// Create web server (will be started conditionally)
	k.WebServer = webui.NewServer(k.Dispatcher, k.projectDir, k.ChatService)

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

	// Cancel context to signal shutdown
	k.cancel()

	// Stop dispatcher
	if k.Dispatcher != nil {
		if err := k.Dispatcher.Stop(k.ctx); err != nil {
			k.Logger.Error("Error stopping dispatcher: %v", err)
		}
	}

	// Close database
	if k.Database != nil {
		if err := k.Database.Close(); err != nil {
			k.Logger.Error("Error closing database: %v", err)
		}
	}

	// Close persistence channel
	if k.PersistenceChannel != nil {
		close(k.PersistenceChannel)
	}

	// Stop web server if running
	if k.WebServer != nil {
		// Web server stops via context cancellation
		k.Logger.Info("Web server will stop via context cancellation")
	}

	// Close rate limiter
	if k.RateLimiter != nil {
		k.RateLimiter.Close()
	}

	k.running = false
	k.Logger.Info("Kernel services stopped")
	return nil
}

// ProjectDir returns the project directory path.
func (k *Kernel) ProjectDir() string {
	return k.projectDir
}

// startPersistenceWorker begins the database persistence worker goroutine.
// This consolidates the persistence logic that was duplicated between bootstrap and main.
func (k *Kernel) startPersistenceWorker() {
	go func() {
		k.Logger.Debug("Starting persistence worker")

		// Create database operations handler with session isolation
		// The session ID is passed to ensure all database operations are scoped to the current orchestrator run
		ops := persistence.NewDatabaseOperations(k.Database, k.Config.SessionID)

		for {
			select {
			case <-k.ctx.Done():
				k.Logger.Info("Persistence worker stopping due to context cancellation")
				return

			case req, ok := <-k.PersistenceChannel:
				if !ok {
					k.Logger.Info("Persistence channel closed, stopping worker")
					return
				}

				if req != nil {
					k.processPersistenceRequest(req, ops)
				}
			}
		}
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

	default:
		k.Logger.Error("Unknown persistence operation: %v", req.Operation)
		if req.Response != nil {
			req.Response <- fmt.Errorf("unknown operation: %v", req.Operation)
		}
	}
}
