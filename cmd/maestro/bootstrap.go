package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/build"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
)

// BootstrapRunner manages the bootstrap flow using agent helpers.
//
//nolint:govet // struct alignment optimization not critical for this type
type BootstrapRunner struct {
	config             *config.Config
	workDir            string
	logger             *logx.Logger
	dispatcher         *dispatch.Dispatcher
	persistenceChannel chan *persistence.Request
	buildService       *build.Service
	database           *sql.DB
}

// NewBootstrapRunner creates a new bootstrap runner.
func NewBootstrapRunner(cfg *config.Config, workDir string) (*BootstrapRunner, error) {
	logger := logx.NewLogger("bootstrap")

	// Load project configuration from .maestro directory
	err := config.LoadConfig(workDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load project config: %w", err)
	}
	logger.Info("📋 Project config loaded into global singleton")

	// Note: Bootstrap container building now happens before this in the init flow

	// Create rate limiter
	rateLimiter := limiter.NewLimiter(cfg)

	// Create dispatcher for agent coordination (no filesystem eventlog)
	dispatcher, err := dispatch.NewDispatcher(cfg, rateLimiter, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create dispatcher: %w", err)
	}

	// Initialize database
	maestroDir := filepath.Join(workDir, ".maestro")
	dbPath := filepath.Join(maestroDir, config.DatabaseFilename)
	database, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	// Create persistence channel
	persistenceChannel := make(chan *persistence.Request, 100)

	// Create build service
	buildService := build.NewBuildService()

	return &BootstrapRunner{
		config:             cfg,
		workDir:            workDir,
		logger:             logger,
		dispatcher:         dispatcher,
		persistenceChannel: persistenceChannel,
		buildService:       buildService,
		database:           database,
	}, nil
}

// RunBootstrap executes the bootstrap flow with direct spec injection.
func (br *BootstrapRunner) RunBootstrap(ctx context.Context, specContent string) error {
	br.logger.Info("Starting bootstrap flow with agent helpers")

	// Start dispatcher
	if err := br.dispatcher.Start(ctx); err != nil {
		return fmt.Errorf("failed to start dispatcher: %w", err)
	}

	// Start persistence worker
	go br.startPersistenceWorker(ctx)

	// Get current config for project info
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	repoURL := ""
	baseBranch := "main"
	if cfg.Project != nil {
		repoURL = cfg.Git.RepoURL
		if cfg.Git.TargetBranch != "" {
			baseBranch = cfg.Git.TargetBranch
		}
	}

	// Create agent configuration from project and orchestrator config
	agentConfig := &AgentConfig{
		NumCoders:       1, // Bootstrap uses one coder agent
		ArchitectModel:  br.getArchitectModel(),
		CoderModel:      br.getCoderModel(),
		WorkDir:         br.workDir,
		RepoURL:         repoURL,
		TargetBranch:    baseBranch,
		MirrorDir:       ".mirrors",
		BranchPattern:   "maestro/story-%s",
		WorktreePattern: "maestro-story-%s",
	}

	// Create agent set using helpers
	agentSet, err := createAgentSet(ctx, agentConfig, br.dispatcher, br.config, br.persistenceChannel, br.buildService)
	if err != nil {
		return fmt.Errorf("failed to create agent set: %w", err)
	}

	br.logger.Info("✅ Created agent set with architect and %d coders", len(agentSet.Coders))

	// Inject spec content directly into architect's spec channel
	if injectErr := br.injectSpecContent(specContent); injectErr != nil {
		return fmt.Errorf("failed to inject spec content: %w", injectErr)
	}

	br.logger.Info("📝 Injected bootstrap spec into architect")

	// Wait for architect completion
	finalState, err := waitForArchitectCompletion(ctx, agentSet.Architect)
	if err != nil {
		return fmt.Errorf("architect completion failed: %w", err)
	}

	br.logger.Info("🏗️ Architect completed with state: %s", finalState)

	// Clean up agents
	if err := cleanupAgents(agentSet); err != nil {
		br.logger.Error("Failed to cleanup agents: %v", err)
	}

	// Bootstrap status is now tracked in database/logs, not config
	if finalState == proto.StateDone {
		br.logger.Info("✅ Bootstrap completed successfully")
	} else {
		br.logger.Error("❌ Bootstrap failed - architect did not reach DONE state")
		return fmt.Errorf("bootstrap failed: architect state %s", finalState)
	}

	return nil
}

// injectSpecContent sends spec content directly to the architect via dispatcher.
func (br *BootstrapRunner) injectSpecContent(specContent string) error {
	// Create SPEC message with content instead of file
	specMsg := proto.NewAgentMsg(proto.MsgTypeSPEC, "bootstrap", string(agent.TypeArchitect))
	specMsg.SetPayload("spec_content", specContent)
	specMsg.SetPayload("type", "spec_content")
	specMsg.SetMetadata("source", "bootstrap")

	// Send via dispatcher
	if err := br.dispatcher.DispatchMessage(specMsg); err != nil {
		return fmt.Errorf("failed to dispatch spec message: %w", err)
	}

	return nil
}

// getArchitectModel extracts architect model configuration.
func (br *BootstrapRunner) getArchitectModel() *config.Model {
	// Find the architect model from the config
	for i := range br.config.Orchestrator.Models {
		if br.config.Orchestrator.Models[i].Name == br.config.Agents.ArchitectModel {
			return &br.config.Orchestrator.Models[i]
		}
	}
	// Return default if not found
	return &config.Model{
		Name:           "o3-mini",
		MaxTPM:         10000,
		DailyBudget:    100.0,
		MaxConnections: 2,
	}
}

// getCoderModel extracts coder model configuration.
func (br *BootstrapRunner) getCoderModel() *config.Model {
	// Find the coder model from the config
	for i := range br.config.Orchestrator.Models {
		if br.config.Orchestrator.Models[i].Name == br.config.Agents.CoderModel {
			return &br.config.Orchestrator.Models[i]
		}
	}
	// Return default if not found
	return &config.Model{
		Name:           config.ModelClaudeSonnetLatest,
		MaxTPM:         50000,
		DailyBudget:    200.0,
		MaxConnections: 4,
	}
}

// startPersistenceWorker runs the database worker goroutine.
func (br *BootstrapRunner) startPersistenceWorker(ctx context.Context) {
	br.logger.Info("Starting bootstrap persistence worker")
	persistenceOps := persistence.NewDatabaseOperations(br.database)

	for {
		select {
		case req := <-br.persistenceChannel:
			if req == nil {
				br.logger.Info("Persistence worker shutting down")
				return
			}
			br.logger.Debug("Processing persistence request: %s", req.Operation)
			br.processPersistenceRequest(req, persistenceOps)
		case <-ctx.Done():
			br.logger.Info("Persistence worker stopping due to context cancellation")
			return
		}
	}
}

// processPersistenceRequest handles persistence requests (simplified version).
//
//nolint:cyclop // Simple switch statement for database operations
func (br *BootstrapRunner) processPersistenceRequest(req *persistence.Request, ops *persistence.DatabaseOperations) {
	switch req.Operation {
	case persistence.OpUpsertSpec:
		if spec, ok := req.Data.(*persistence.Spec); ok {
			if err := ops.UpsertSpec(spec); err != nil {
				br.logger.Error("Failed to upsert spec: %v", err)
			} else {
				br.logger.Info("Successfully upserted spec: %s", spec.ID)
			}
		}
	case persistence.OpUpsertStory:
		if story, ok := req.Data.(*persistence.Story); ok {
			if err := ops.UpsertStory(story); err != nil {
				br.logger.Error("Failed to upsert story: %v", err)
			} else {
				br.logger.Info("Successfully upserted story: %s", story.ID)
			}
		}
	case persistence.OpUpdateStoryStatus:
		if statusReq, ok := req.Data.(*persistence.UpdateStoryStatusRequest); ok {
			if err := ops.UpdateStoryStatus(statusReq); err != nil {
				br.logger.Error("Failed to update story status for %s: %v", statusReq.StoryID, err)
			} else {
				br.logger.Info("Successfully updated story status: %s -> %s", statusReq.StoryID, statusReq.Status)
			}
		}
	case persistence.OpAddStoryDependency:
		if deps, ok := req.Data.(*persistence.StoryDependency); ok {
			if err := ops.AddStoryDependency(deps.StoryID, deps.DependsOn); err != nil {
				br.logger.Error("Failed to add story dependency %s -> %s: %v", deps.StoryID, deps.DependsOn, err)
			} else {
				br.logger.Info("Successfully added story dependency: %s -> %s", deps.StoryID, deps.DependsOn)
			}
		}
	case persistence.OpGetAllStories:
		if req.Response != nil {
			stories, err := ops.GetAllStories()
			if err != nil {
				br.logger.Error("Failed to get all stories: %v", err)
				req.Response <- err
			} else {
				req.Response <- stories
			}
		}
	case persistence.OpQueryPendingStories:
		if req.Response != nil {
			stories, err := ops.QueryPendingStories()
			if err != nil {
				br.logger.Error("Failed to query pending stories: %v", err)
				req.Response <- err
			} else {
				req.Response <- stories
			}
		}
	case persistence.OpGetStoryDependencies:
		if req.Response != nil {
			if storyID, ok := req.Data.(string); ok {
				deps, err := ops.GetStoryDependencies(storyID)
				if err != nil {
					br.logger.Error("Failed to get story dependencies for %s: %v", storyID, err)
					req.Response <- err
				} else {
					req.Response <- deps
				}
			} else {
				br.logger.Error("Invalid data type for get_story_dependencies operation")
				req.Response <- fmt.Errorf("invalid data type")
			}
		}
	case persistence.OpUpsertAgentRequest:
		if agentRequest, ok := req.Data.(*persistence.AgentRequest); ok {
			if err := ops.UpsertAgentRequest(agentRequest); err != nil {
				br.logger.Error("Failed to upsert agent request: %v", err)
			} else {
				br.logger.Info("Successfully upserted agent request: %s", agentRequest.ID)
			}
		}
	case persistence.OpUpsertAgentResponse:
		if agentResponse, ok := req.Data.(*persistence.AgentResponse); ok {
			if err := ops.UpsertAgentResponse(agentResponse); err != nil {
				br.logger.Error("Failed to upsert agent response: %v", err)
			} else {
				br.logger.Info("Successfully upserted agent response: %s", agentResponse.ID)
			}
		}
	case persistence.OpUpsertAgentPlan:
		if agentPlan, ok := req.Data.(*persistence.AgentPlan); ok {
			if err := ops.UpsertAgentPlan(agentPlan); err != nil {
				br.logger.Error("Failed to upsert agent plan: %v", err)
			} else {
				br.logger.Info("Successfully upserted agent plan: %s", agentPlan.ID)
			}
		}
	default:
		br.logger.Warn("Unhandled persistence operation: %s", req.Operation)
	}
}

// Shutdown cleans up resources.
//
//nolint:unparam // error return kept for API consistency
func (br *BootstrapRunner) Shutdown(ctx context.Context) error {
	br.logger.Info("Shutting down bootstrap runner")

	if br.persistenceChannel != nil {
		close(br.persistenceChannel)
	}

	if br.database != nil {
		if err := br.database.Close(); err != nil {
			br.logger.Error("Failed to close database: %v", err)
		}
	}

	if err := br.dispatcher.Stop(ctx); err != nil {
		br.logger.Error("Failed to stop dispatcher: %v", err)
	}

	return nil
}
