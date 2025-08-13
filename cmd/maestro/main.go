package main

import (
	"bufio"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver

	"orchestrator/pkg/agent"
	"orchestrator/pkg/architect"
	"orchestrator/pkg/build"
	"orchestrator/pkg/coder"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/dockerfiles"
	"orchestrator/pkg/eventlog"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates/bootstrap"
	"orchestrator/pkg/utils"
	"orchestrator/pkg/webui"
	"orchestrator/pkg/workspace"
)

const (
	containerSourceDetect     = "detect"
	containerSourceDockerfile = "dockerfile"
	containerSourceImageName  = "imagename"
)

// Orchestrator manages the multi-agent system.
//
//nolint:govet // Large complex struct, logical grouping preferred over memory optimization
type Orchestrator struct {
	config       *config.Config
	dispatcher   *dispatch.Dispatcher
	rateLimiter  *limiter.Limiter
	eventLog     *eventlog.Writer
	logger       *logx.Logger
	architect    *architect.Driver // Phase 4: Architect driver for spec processing
	webServer    *webui.Server     // Web UI server
	buildService *build.Service    // Build execution service for MCP tools
	agents       map[string]StatusAgent
	agentTypes   map[string]string // Map of agentID -> agent type ("coder" or "architect")
	shutdownTime time.Duration
	projectDir   string // Project directory containing all work directories as well as the config and database (in the subdir .maestro)

	// Persistence layer
	database           *sql.DB                         // SQLite database connection
	persistenceChannel chan *persistence.Request       // Channel for database operations
	persistenceOps     *persistence.DatabaseOperations // Database operations handler

	// Agents handle their own state machines.
}

// StatusAgent interface for agents that can generate status reports.
type StatusAgent interface {
	dispatch.Agent
	GenerateStatus() (string, error)
}

// ArchitectMessageAdapter removed - architect driver is now registered directly with dispatcher.

// checkDependencies verifies that all required external dependencies are available.
func checkDependencies(_ *config.Config) error {
	var errors []string

	// Check for git.
	if _, err := exec.LookPath("git"); err != nil {
		errors = append(errors, "git is not installed or not in PATH")
	}

	// Check for gh (GitHub CLI).
	if _, err := exec.LookPath("gh"); err != nil {
		errors = append(errors, "gh (GitHub CLI) is not installed or not in PATH")
	}

	// Check for Docker (always required since executor is simplified to Docker-only).
	if _, err := exec.LookPath("docker"); err != nil {
		errors = append(errors, "docker is not installed or not in PATH (required for Docker executor)")
	} else {
		// Check if Docker daemon is running.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "docker", "ps", "-q")
		if err := cmd.Run(); err != nil {
			errors = append(errors, "Docker daemon is not running (required for Docker executor)")
		}
	}

	// Check for GITHUB_TOKEN.
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		errors = append(errors, "GITHUB_TOKEN not found in environment variables")
	}

	// Validate GITHUB_TOKEN format if present.
	if githubToken != "" {
		if !strings.HasPrefix(githubToken, "ghp_") && !strings.HasPrefix(githubToken, "github_pat_") {
			errors = append(errors, "GITHUB_TOKEN format appears invalid (should start with 'ghp_' or 'github_pat_')")
		}
		if len(githubToken) < 20 {
			errors = append(errors, "GITHUB_TOKEN appears too short to be valid")
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("dependency check failed:\n  - %s", strings.Join(errors, "\n  - "))
	}

	fmt.Println("✅ All required dependencies are available:")
	fmt.Println("  - git: available")
	fmt.Println("  - gh (GitHub CLI): available")
	fmt.Println("  - docker: available")
	fmt.Println("  - GITHUB_TOKEN: configured")

	return nil
}

//nolint:cyclop // Main function complexity is acceptable for orchestrator startup logic
func main() {
	// Check if this is the init subcommand
	if len(os.Args) >= 2 && os.Args[1] == "init" {
		handleInit(os.Args[2:])
		return
	}

	// Check if this is the bootstrap subcommand
	if len(os.Args) >= 2 && os.Args[1] == "bootstrap" {
		handleBootstrap(os.Args[2:])
		return
	}

	fmt.Println("orchestrator boot")

	var specPath string
	var liveMode bool
	var uiMode bool
	var projectDir string
	var continueMode bool
	flag.StringVar(&specPath, "spec", "", "Path to specification file (markdown)")
	flag.StringVar(&projectDir, "projectdir", "", "Project directory (default: current directory)")
	flag.BoolVar(&liveMode, "live", false, "Use live API calls instead of mock mode")
	flag.BoolVar(&uiMode, "ui", false, "Start web UI server on port 8080")
	flag.BoolVar(&continueMode, "continue", false, "Resume from previous state if interrupted")
	flag.Parse()

	// Require project directory to be explicitly specified.
	if projectDir == "" {
		log.Fatalf("Project directory must be specified with -projectdir flag")
	}

	if loadErr := config.LoadConfig(projectDir); loadErr != nil {
		log.Fatalf("Failed to load config: %v", loadErr)
	}
	cfg, err := config.GetConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get config: %v\n", err)
		os.Exit(1)
	}

	// Check required dependencies before proceeding.
	if depErr := checkDependencies(&cfg); depErr != nil {
		log.Fatalf("Missing required dependencies: %v", depErr)
	}

	// Config loaded successfully (no need to print it).
	_ = cfg

	// Create orchestrator.
	orchestrator, err := NewOrchestrator(&cfg, projectDir)
	if err != nil {
		log.Fatalf("Failed to create orchestrator: %v", err)
	}

	// Validate continue mode if requested.
	if continueMode {
		if err := validateContinueMode(orchestrator); err != nil {
			log.Fatalf("Continue mode validation failed: %v", err)
		}
	}

	// Start orchestrator.
	ctx := context.Background()
	if err := orchestrator.Start(ctx, liveMode); err != nil {
		log.Fatalf("Failed to start orchestrator: %v", err)
	}

	// Start web UI if requested.
	if uiMode {
		orchestrator.StartWebUI(ctx, 8080)

		// Open browser.
		if err := openBrowser("http://localhost:8080"); err != nil {
			log.Printf("Failed to open browser: %v", err)
			fmt.Println("🌐 Web UI available at: http://localhost:8080")
		}
	}

	// If spec file provided, send SPEC message to dispatcher.
	if specPath != "" {
		if err := orchestrator.SendSpecification(ctx, specPath); err != nil {
			log.Fatalf("Failed to send specification: %v", err)
		}
	}

	// Setup signal handling for graceful shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for signal.
	sig := <-sigChan
	orchestrator.logger.Info("Received signal %v, initiating graceful shutdown", sig)

	// Perform graceful shutdown.
	shutdownCtx, cancel := context.WithTimeout(ctx, orchestrator.shutdownTime)

	if err := orchestrator.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error during shutdown: %v", err)
		cancel()
		os.Exit(1)
	}

	orchestrator.logger.Info("Orchestrator shutdown completed successfully")
	cancel()
}

// validateContinueMode checks if there are in-flight stories that can be resumed.
// This function fails fast if there's nothing to continue from.
func validateContinueMode(orchestrator *Orchestrator) error {
	dbPath := filepath.Join(orchestrator.projectDir, config.ProjectConfigDir, config.DatabaseFilename)
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			orchestrator.logger.Error("Failed to close database: %v", closeErr)
		}
	}()

	// Check if there are any specs with open stories
	var openStoryCount int
	err = db.QueryRow(`
		SELECT COUNT(*) 
		FROM stories 
		WHERE status NOT IN ('committed', 'merged', 'duplicate')
	`).Scan(&openStoryCount)
	if err != nil {
		return fmt.Errorf("failed to check for open stories: %w", err)
	}

	if openStoryCount == 0 {
		return fmt.Errorf("no open stories found - nothing to continue from")
	}

	// Check if there are any agent states to restore
	var agentStateCount int
	err = db.QueryRow(`
		SELECT COUNT(*) 
		FROM agent_states
	`).Scan(&agentStateCount)
	if err != nil {
		// If agent_states table doesn't exist yet, it's not an error for continuation
		// The system will work without existing agent state
		orchestrator.logger.Info("No agent states found, will start fresh agents")
		return nil
	}

	orchestrator.logger.Info("Continue mode validated: %d open stories, %d agent states",
		openStoryCount, agentStateCount)
	return nil
}

// NewOrchestrator creates a new orchestrator instance.
//
//nolint:cyclop // Orchestrator initialization complexity is acceptable - contains essential startup logic
func NewOrchestrator(cfg *config.Config, projectDir string) (*Orchestrator, error) {
	logger := logx.NewLogger("orchestrator")

	// Create rate limiter.
	rateLimiter := limiter.NewLimiter(cfg)

	// Create event log.
	eventLog, err := eventlog.NewWriter("logs", 24)
	if err != nil {
		return nil, fmt.Errorf("failed to create event log: %w", err)
	}

	// Create build service.
	buildService := build.NewBuildService()

	// Initialize executor manager and shell tool.
	executorManager := execpkg.NewExecutorManager(nil) // Config is ignored in simplified version
	if initErr := executorManager.Initialize(context.Background()); initErr != nil {
		return nil, fmt.Errorf("failed to initialize executor manager: %w", initErr)
	}

	defaultExecutor, err := execpkg.GetDefault()
	if err != nil {
		return nil, fmt.Errorf("failed to get default executor: %w", err)
	}

	// Tools are now auto-registered via init() functions in the tools package
	// Individual agents will create ToolProviders with appropriate executor configuration

	logger.Info("Initialized executor: %s", defaultExecutor.Name())

	// No global state store - each agent manages its own state.

	// Create dispatcher.
	dispatcher, err := dispatch.NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		return nil, fmt.Errorf("failed to create dispatcher: %w", err)
	}

	// Set global container registry for all executors.
	execpkg.SetGlobalRegistry(dispatcher.GetContainerRegistry())

	// Set default shutdown timeout.
	shutdownTime := 30 * time.Second
	// GracefulShutdownTimeoutSec field was removed, use default timeout

	// Initialize SQLite database for persistence.
	// Database lives in .maestro directory alongside user instruction files.
	maestroDir := filepath.Join(projectDir, ".maestro")
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		return nil, fmt.Errorf("failed to create .maestro directory: %w", mkdirErr)
	}

	dbPath := filepath.Join(maestroDir, config.DatabaseFilename)
	database, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	// Test database connectivity with a simple ping.
	// This ensures the database is working before we proceed.
	// Any database errors after this point will be treated as fatal.
	if err := database.Ping(); err != nil {
		if closeErr := database.Close(); closeErr != nil {
			// Log close error but return the original error
			_ = closeErr
		}
		return nil, fmt.Errorf("database ping failed - persistence layer unavailable: %w", err)
	}

	persistenceOps := persistence.NewDatabaseOperations(database)

	// Create persistence channel for fire-and-forget database operations.
	// Buffer size of 100 prevents blocking during normal operation.
	persistenceChannel := make(chan *persistence.Request, 100)

	logger.Info("Initialized SQLite database at: %s", dbPath)

	return &Orchestrator{
		config:             cfg,
		dispatcher:         dispatcher,
		rateLimiter:        rateLimiter,
		eventLog:           eventLog,
		logger:             logger,
		agents:             make(map[string]StatusAgent),
		agentTypes:         make(map[string]string),
		shutdownTime:       shutdownTime,
		architect:          nil, // Will be set during agent creation
		buildService:       buildService,
		projectDir:         projectDir,
		database:           database,
		persistenceChannel: persistenceChannel,
		persistenceOps:     persistenceOps,
	}, nil
}

// Start initializes and starts the orchestrator.
func (o *Orchestrator) Start(ctx context.Context, liveMode bool) error {
	o.logger.Info("Starting orchestrator")

	// Start dispatcher first (no bootstrap yet - it will be triggered by spec receipt).
	if err := o.dispatcher.Start(ctx); err != nil {
		return fmt.Errorf("failed to start dispatcher: %w", err)
	}

	// Start persistence worker for database operations.
	// This goroutine handles all database writes in a serialized manner to prevent race conditions.
	go o.startPersistenceWorker(ctx)

	// Create and register agents.
	if err := o.createAgents(ctx, liveMode); err != nil {
		return fmt.Errorf("failed to create agents: %w", err)
	}

	// Agents will handle their own polling and state machines.

	// Start agent state change processor.
	go o.startStateChangeProcessor(ctx)

	o.logger.Info("Orchestrator started successfully")
	return nil
}

// startPersistenceWorker runs the database worker goroutine.
// This handles all database operations in a serialized manner to prevent race conditions.
// Fire-and-forget writes are processed without response, queries return results via channels.
func (o *Orchestrator) startPersistenceWorker(ctx context.Context) {
	o.logger.Info("Starting persistence worker")

	for {
		select {
		case req := <-o.persistenceChannel:
			if req == nil {
				// Channel closed, shutdown
				o.logger.Info("Persistence worker shutting down")
				return
			}

			// Process the request
			o.processPersistenceRequest(req)

		case <-ctx.Done():
			o.logger.Info("Persistence worker stopping due to context cancellation")
			return
		}
	}
}

// processPersistenceRequest handles a single persistence request.
func (o *Orchestrator) processPersistenceRequest(req *persistence.Request) {
	switch req.Operation {
	// Write operations (fire-and-forget)
	case persistence.OpUpsertSpec:
		o.handleUpsertSpec(req)
	case persistence.OpUpsertStory:
		o.handleUpsertStory(req)
	case persistence.OpUpdateStoryStatus:
		o.handleUpdateStoryStatus(req)
	case persistence.OpAddStoryDependency:
		o.handleAddStoryDependency(req)
	// Query operations (with response)
	case persistence.OpQueryStoriesByStatus:
		o.handleQueryStoriesByStatus(req)
	case persistence.OpQueryPendingStories:
		o.handleQueryPendingStories(req)
	case persistence.OpGetStoryByID:
		o.handleGetStoryByID(req)
	case persistence.OpGetSpecSummary:
		o.handleGetSpecSummary(req)
	case persistence.OpGetStoryDependencies:
		o.handleGetStoryDependencies(req)
	case persistence.OpGetAllStories:
		o.handleGetAllStories(req)
	default:
		o.logger.Error("Unknown persistence operation: %s", req.Operation)
		if req.Response != nil {
			req.Response <- fmt.Errorf("unknown operation: %s", req.Operation)
		}
	}
}

// handleUpsertSpec handles spec upsert operations.
func (o *Orchestrator) handleUpsertSpec(req *persistence.Request) {
	if spec, ok := req.Data.(*persistence.Spec); ok {
		if err := o.persistenceOps.UpsertSpec(spec); err != nil {
			o.logger.Error("FATAL: Failed to upsert spec %s: %v", spec.ID, err)
			o.logger.Error("Persistence failure is unrecoverable, shutting down")
			os.Exit(1) // Fatal error - cannot continue without persistence
		}
		o.logger.Debug("Upserted spec: %s", spec.ID)
	} else {
		o.logger.Error("Invalid data type for upsert_spec operation")
	}
}

// handleUpsertStory handles story upsert operations.
func (o *Orchestrator) handleUpsertStory(req *persistence.Request) {
	if story, ok := req.Data.(*persistence.Story); ok {
		if err := o.persistenceOps.UpsertStory(story); err != nil {
			o.logger.Error("FATAL: Failed to upsert story %s: %v", story.ID, err)
			o.logger.Error("Persistence failure is unrecoverable, shutting down")
			os.Exit(1) // Fatal error - cannot continue without persistence
		}
		o.logger.Debug("Upserted story: %s", story.ID)
	} else {
		o.logger.Error("Invalid data type for upsert_story operation")
	}
}

// handleUpdateStoryStatus handles story status update operations.
func (o *Orchestrator) handleUpdateStoryStatus(req *persistence.Request) {
	if updateReq, ok := req.Data.(*persistence.UpdateStoryStatusRequest); ok {
		if err := o.persistenceOps.UpdateStoryStatus(updateReq); err != nil {
			o.logger.Error("FATAL: Failed to update story status %s: %v", updateReq.StoryID, err)
			o.logger.Error("Persistence failure is unrecoverable, shutting down")
			os.Exit(1) // Fatal error - cannot continue without persistence
		}
		o.logger.Debug("Updated story status: %s -> %s", updateReq.StoryID, updateReq.Status)
	} else {
		o.logger.Error("Invalid data type for update_story_status operation")
	}
}

// handleAddStoryDependency handles story dependency operations.
func (o *Orchestrator) handleAddStoryDependency(req *persistence.Request) {
	if deps, ok := req.Data.(*persistence.StoryDependency); ok {
		if err := o.persistenceOps.AddStoryDependency(deps.StoryID, deps.DependsOn); err != nil {
			o.logger.Error("FATAL: Failed to add story dependency %s -> %s: %v", deps.StoryID, deps.DependsOn, err)
			o.logger.Error("Persistence failure is unrecoverable, shutting down")
			os.Exit(1) // Fatal error - cannot continue without persistence
		}
		o.logger.Debug("Added story dependency: %s -> %s", deps.StoryID, deps.DependsOn)
	} else {
		o.logger.Error("Invalid data type for add_story_dependency operation")
	}
}

// handleQueryStoriesByStatus handles query operations by status.
func (o *Orchestrator) handleQueryStoriesByStatus(req *persistence.Request) {
	if req.Response != nil {
		if filter, ok := req.Data.(*persistence.StoryFilter); ok {
			stories, err := o.persistenceOps.QueryStoriesByFilter(filter)
			if err != nil {
				o.logger.Error("Failed to query stories by status: %v", err)
				req.Response <- err
			} else {
				req.Response <- stories
			}
		} else {
			o.logger.Error("Invalid data type for query_stories_by_status operation")
			req.Response <- fmt.Errorf("invalid data type")
		}
	}
}

// handleQueryPendingStories handles pending stories query operations.
func (o *Orchestrator) handleQueryPendingStories(req *persistence.Request) {
	if req.Response != nil {
		stories, err := o.persistenceOps.QueryPendingStories()
		if err != nil {
			o.logger.Error("Failed to query pending stories: %v", err)
			req.Response <- err
		} else {
			req.Response <- stories
		}
	}
}

// handleGetStoryByID handles get story by ID operations.
func (o *Orchestrator) handleGetStoryByID(req *persistence.Request) {
	if req.Response != nil {
		if storyID, ok := req.Data.(string); ok {
			story, err := o.persistenceOps.GetStoryByID(storyID)
			if err != nil {
				o.logger.Error("Failed to get story %s: %v", storyID, err)
				req.Response <- err
			} else {
				req.Response <- story
			}
		} else {
			o.logger.Error("Invalid data type for get_story_by_id operation")
			req.Response <- fmt.Errorf("invalid data type")
		}
	}
}

// handleGetSpecSummary handles get spec summary operations.
func (o *Orchestrator) handleGetSpecSummary(req *persistence.Request) {
	if req.Response != nil {
		if specID, ok := req.Data.(string); ok {
			summary, err := o.persistenceOps.GetSpecSummary(specID)
			if err != nil {
				o.logger.Error("Failed to get spec summary %s: %v", specID, err)
				req.Response <- err
			} else {
				req.Response <- summary
			}
		} else {
			o.logger.Error("Invalid data type for get_spec_summary operation")
			req.Response <- fmt.Errorf("invalid data type")
		}
	}
}

// handleGetStoryDependencies handles get story dependencies operations.
func (o *Orchestrator) handleGetStoryDependencies(req *persistence.Request) {
	if req.Response != nil {
		if storyID, ok := req.Data.(string); ok {
			deps, err := o.persistenceOps.GetStoryDependencies(storyID)
			if err != nil {
				o.logger.Error("Failed to get story dependencies %s: %v", storyID, err)
				req.Response <- err
			} else {
				req.Response <- deps
			}
		} else {
			o.logger.Error("Invalid data type for get_story_dependencies operation")
			req.Response <- fmt.Errorf("invalid data type")
		}
	}
}

// handleGetAllStories handles get all stories operations.
func (o *Orchestrator) handleGetAllStories(req *persistence.Request) {
	if req.Response != nil {
		stories, err := o.persistenceOps.GetAllStories()
		if err != nil {
			o.logger.Error("Failed to get all stories: %v", err)
			req.Response <- err
		} else {
			req.Response <- stories
		}
	}
}

// GetPersistenceChannel returns the channel for sending persistence requests.
// This allows agents to send database operations to the orchestrator.
func (o *Orchestrator) GetPersistenceChannel() chan<- *persistence.Request {
	return o.persistenceChannel
}

// Shutdown performs graceful shutdown of the orchestrator.
//
//nolint:unparam // error return kept for API consistency
func (o *Orchestrator) Shutdown(ctx context.Context) error {
	o.logger.Info("Starting graceful shutdown")

	// Step 1: Close persistence channel and database.
	if o.persistenceChannel != nil {
		close(o.persistenceChannel)
		o.logger.Info("Closed persistence channel")
	}

	if o.database != nil {
		if err := o.database.Close(); err != nil {
			o.logger.Error("Failed to close database: %v", err)
		} else {
			o.logger.Info("Closed database connection")
		}
	}

	// Step 2: Agents handle their own shutdown via SHUTDOWN messages.

	// Step 2: Broadcast SHUTDOWN message to all agents.
	if err := o.broadcastShutdown(); err != nil {
		o.logger.Error("Failed to broadcast shutdown: %v", err)
	}

	// Step 3: Wait for agents to complete current work.
	waitTime := o.shutdownTime / 4
	o.logger.Info("Waiting %v for agents to complete work", waitTime)

	// Use context-aware sleep to respect cancellation
	select {
	case <-ctx.Done():
		o.logger.Info("Shutdown wait cancelled by context")
	case <-time.After(waitTime):
		o.logger.Debug("Shutdown wait period completed")
	}

	// Step 4: Generate status files for all agents and orchestrator.
	if err := o.generateStatusFiles(); err != nil {
		o.logger.Error("Failed to generate status files: %v", err)
	}

	// Step 5: Stop all remaining containers via global registry.
	o.logger.Info("Stopping all remaining containers")
	registry := execpkg.GetGlobalRegistry()
	if registry != nil {
		executor := execpkg.NewLongRunningDockerExec("alpine:latest", "") // Empty agentID - for cleanup only
		if err := registry.StopAllContainers(ctx, executor); err != nil {
			o.logger.Error("Failed to stop all containers: %v", err)
		}
	}
	o.logger.Info("Container stop completed")

	// Step 6: Stop dispatcher.
	o.logger.Info("Stopping dispatcher")
	if err := o.dispatcher.Stop(ctx); err != nil {
		o.logger.Error("Failed to stop dispatcher: %v", err)
	} else {
		o.logger.Info("Dispatcher stop completed")
	}

	// Step 7: Close other resources.
	o.rateLimiter.Close()

	if err := o.eventLog.Close(); err != nil {
		o.logger.Error("Failed to close event log: %v", err)
	}

	o.logger.Info("Graceful shutdown completed")
	return nil
}

// SendSpecification sends a SPEC message to the dispatcher for the architect to process.
func (o *Orchestrator) SendSpecification(_ context.Context, specFile string) error {
	o.logger.Info("Sending specification file to architect: %s", specFile)
	fmt.Printf("🚀 Sending specification to architect...\n")

	// Create SPEC message for the architect.
	specMsg := proto.NewAgentMsg(proto.MsgTypeSPEC, "orchestrator", string(agent.TypeArchitect))
	specMsg.SetPayload("spec_file", specFile)
	specMsg.SetPayload("type", "spec_file")
	specMsg.SetMetadata("source", "command_line")

	// Send via dispatcher.
	if err := o.dispatcher.DispatchMessage(specMsg); err != nil {
		return fmt.Errorf("failed to dispatch specification message: %w", err)
	}

	fmt.Printf("✅ Specification sent to architect via dispatcher\n")
	return nil
}

//nolint:cyclop // Complex agent creation logic, acceptable for initialization
func (o *Orchestrator) createAgents(ctx context.Context, _ bool) error {
	// Create architect agent
	architectModel := o.getArchitectModel()
	architectID := "architect-001"
	architectWorkDir := filepath.Join(o.projectDir, strings.ReplaceAll(architectID, ":", "-"))

	// Create architect with LLM integration (same pattern as coder)
	var err error
	o.architect, err = architect.NewArchitect(architectID, architectModel, o.dispatcher, architectWorkDir, o.config, o.persistenceChannel)
	if err != nil {
		return fmt.Errorf("failed to create architect: %w", err)
	}

	// Initialize the architect driver.
	if err := o.architect.Initialize(context.Background()); err != nil {
		return fmt.Errorf("failed to initialize architect: %w", err)
	}

	// Register and start architect
	o.dispatcher.Attach(o.architect)
	go func() {
		if err := o.architect.Run(ctx); err != nil {
			o.logger.Error("Architect state machine error: %v", err)
		}
	}()

	o.agents[architectID] = &StatusAgentWrapper{Agent: o.architect}
	o.agentTypes[architectID] = string(agent.TypeArchitect)
	o.logger.Info("Created architect driver: %s", architectID)

	// Create coder agents
	coderModel := o.getCoderModel()
	maxCoders := o.config.Agents.MaxCoders
	if maxCoders <= 0 {
		maxCoders = 2 // Default
	}

	for i := 0; i < maxCoders; i++ {
		coderID := fmt.Sprintf("coder-%03d", i+1)
		coderWorkDir := filepath.Join(o.projectDir, strings.ReplaceAll(coderID, ":", "-"))

		// Create clone manager for Git clone support.
		var cloneManager *coder.CloneManager
		if o.config.Git != nil && o.config.Git.RepoURL != "" {
			gitRunner := coder.NewDefaultGitRunner()
			cloneManager = coder.NewCloneManager(
				gitRunner,
				o.projectDir, // Use project dir, not agent workdir, for shared mirror
				o.config.Git.RepoURL,
				o.config.Git.TargetBranch,
				o.config.Git.MirrorDir,
				o.config.Git.BranchPattern,
			)
			o.logger.Info("Created clone manager for coder %s with repo %s", coderID, o.config.Git.RepoURL)
		} else {
			return fmt.Errorf("no Git repository URL configured - CloneManager is required for coder agents")
		}

		// Create coder with LLM integration.
		coderAgent, err := coder.NewCoder(coderID, coderID, coderWorkDir, coderModel, os.Getenv("ANTHROPIC_API_KEY"), cloneManager, o.buildService)
		if err != nil {
			return fmt.Errorf("failed to create coder agent %s: %w", coderID, err)
		}

		// Initialize and start the coder's state machine.
		if err := coderAgent.Initialize(context.Background()); err != nil {
			return fmt.Errorf("failed to initialize coder %s: %w", coderID, err)
		}

		// Register and start coder
		o.dispatcher.Attach(coderAgent)
		go func(c *coder.Coder, id string) {
			if err := c.Run(ctx); err != nil {
				o.logger.Error("Coder %s state machine error: %v", id, err)
			}
		}(coderAgent, coderID)

		o.agents[coderID] = &StatusAgentWrapper{Agent: coderAgent}
		o.agentTypes[coderID] = string(agent.TypeCoder)
		o.logger.Info("Created coder agent: %s", coderID)
	}

	o.logger.Info("Created and registered %d agents total", len(o.agents))
	return nil
}

//nolint:unparam // error return kept for future extensibility
func (o *Orchestrator) broadcastShutdown() error {
	o.logger.Info("Broadcasting SHUTDOWN message to all agents")

	for agentID := range o.agents {
		shutdownMsg := proto.NewAgentMsg(proto.MsgTypeSHUTDOWN, "orchestrator", agentID)
		shutdownMsg.SetPayload("reason", "Graceful shutdown requested")
		shutdownMsg.SetPayload("timeout", o.shutdownTime.String())
		shutdownMsg.SetMetadata("shutdown_type", "graceful")

		if err := o.dispatcher.DispatchMessage(shutdownMsg); err != nil {
			o.logger.Error("Failed to send shutdown to agent %s: %v", agentID, err)
			continue
		}

		o.logger.Debug("Sent shutdown message to agent %s", agentID)
	}

	return nil
}

// StatusAgentWrapper wraps regular agents to provide status functionality (placeholder - status generation removed).
type StatusAgentWrapper struct {
	dispatch.Agent
}

// GenerateStatus generates a status report for the wrapped agent.
func (w *StatusAgentWrapper) GenerateStatus() (string, error) {
	agentID := w.Agent.GetID()

	status := "# Agent Status Report\n\n"
	status += fmt.Sprintf("- **Agent ID**: %s\n", agentID)
	status += fmt.Sprintf("- **Timestamp**: %s\n", time.Now().Format(time.RFC3339))

	// Add Agent Information section that tests expect
	status += "\n## Agent Information\n\n"

	// Try to get detailed information if the agent is a Driver
	if driver, ok := w.Agent.(agent.Driver); ok {
		agentType := string(driver.GetAgentType())
		currentState := string(driver.GetCurrentState())

		status += fmt.Sprintf("- **Agent Type**: %s\n", agentType)
		status += fmt.Sprintf("- **Current State**: %s\n", currentState)

		stateData := driver.GetStateData()
		if len(stateData) > 0 {
			status += "\n## State Data\n\n"
			for key, value := range stateData {
				status += fmt.Sprintf("- **%s**: %v\n", key, value)
			}
		}
	} else {
		status += "- **Agent Type**: Unknown (not a Driver)\n"
		status += "- **Current State**: Unknown\n"
	}

	return status, nil
}

// generateStatusFiles creates status files for all agents and the orchestrator.
func (o *Orchestrator) generateStatusFiles() error {
	// Create status directory.
	statusDir := filepath.Join(o.projectDir, "status")
	if err := os.MkdirAll(statusDir, 0755); err != nil {
		return fmt.Errorf("failed to create status directory: %w", err)
	}

	o.logger.Info("Generating status files in: %s", statusDir)

	// Generate status files for all agents.
	for agentID, agent := range o.agents {
		statusContent, err := agent.GenerateStatus()
		if err != nil {
			o.logger.Error("Failed to generate status for agent %s: %v", agentID, err)
			continue
		}

		statusFile := filepath.Join(statusDir, agentID+"-STATUS.md")
		if err := os.WriteFile(statusFile, []byte(statusContent), 0644); err != nil {
			o.logger.Error("Failed to write status file for agent %s: %v", agentID, err)
			continue
		}

		o.logger.Info("Generated status file: %s", statusFile)
	}

	// Generate orchestrator status file.
	orchestratorStatus := o.generateOrchestratorStatus()
	orchestratorFile := filepath.Join(statusDir, "orchestrator-STATUS.md")
	if err := os.WriteFile(orchestratorFile, []byte(orchestratorStatus), 0644); err != nil {
		return fmt.Errorf("failed to write orchestrator status file: %w", err)
	}

	o.logger.Info("Generated orchestrator status file: %s", orchestratorFile)
	return nil
}

// generateOrchestratorStatus generates a status report for the orchestrator.
func (o *Orchestrator) generateOrchestratorStatus() string {
	status := "# Orchestrator Status Report\n\n"
	status += fmt.Sprintf("- **Timestamp**: %s\n", time.Now().Format(time.RFC3339))
	status += fmt.Sprintf("- **Project Directory**: %s\n", o.projectDir)
	status += fmt.Sprintf("- **Agent Count**: %d\n", len(o.agents))
	status += fmt.Sprintf("- **Shutdown Timeout**: %v\n", o.shutdownTime)

	// Add Configuration section that tests expect
	status += "\n## Configuration\n\n"
	status += fmt.Sprintf("- **Config File**: %v\n", "config.json")
	if o.config.Git != nil {
		status += fmt.Sprintf("- **Repository URL**: %s\n", o.config.Git.RepoURL)
		status += fmt.Sprintf("- **Target Branch**: %s\n", o.config.Git.TargetBranch)
	}
	status += "- **Executor Type**: docker (simplified)\n"

	status += "\n## Registered Agents\n\n"
	for agentID, agentType := range o.agentTypes {
		status += fmt.Sprintf("- **%s**: %s\n", agentID, agentType)
	}

	status += "\n## System Information\n\n"
	status += fmt.Sprintf("- **Go Version**: %s\n", runtime.Version())
	status += fmt.Sprintf("- **OS/Arch**: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	status += fmt.Sprintf("- **CPU Count**: %d\n", runtime.NumCPU())

	return status
}

// startStateChangeProcessor starts a goroutine that processes state change notifications.
// and restarts agents when they reach DONE state.
func (o *Orchestrator) startStateChangeProcessor(ctx context.Context) {
	o.logger.Info("Starting state change processor")

	// Get state change notifications from dispatcher.
	stateChangeCh := o.dispatcher.GetStateChangeChannel()

	for {
		select {
		case <-ctx.Done():
			o.logger.Info("State change processor stopping due to context cancellation")
			return
		case notification, ok := <-stateChangeCh:
			if !ok {
				// Channel is closed, stop processing.
				o.logger.Info("State change channel closed, stopping processor")
				return
			}
			if notification != nil {
				o.handleStateChange(ctx, notification)
			}
		}
	}
}

// handleStateChange handles individual state change notifications.
func (o *Orchestrator) handleStateChange(ctx context.Context, notification *proto.StateChangeNotification) {
	o.logger.Info("Agent %s state changed: %s -> %s", notification.AgentID, notification.FromState, notification.ToState)

	// Determine agent type from stored agent configuration.
	agentType := o.getAgentType(notification.AgentID)

	// Restart coder agents when they reach DONE or ERROR state.
	if (notification.ToState == proto.StateDone || notification.ToState == proto.StateError) && agentType == string(agent.TypeCoder) {
		o.logger.Info("Agent %s reached %s state, restarting...", notification.AgentID, notification.ToState)

		// For ERROR state, requeue the story the agent was working on.
		if notification.ToState == proto.StateError {
			if err := o.dispatcher.SendRequeue(notification.AgentID, "agent error"); err != nil {
				o.logger.Error("Failed to requeue story for agent %s: %v", notification.AgentID, err)
			}
		}

		// TODO: Future enhancement - Collect and pool metrics before agent restart
		// See docs/METRICS_POOLING_REQUIREMENTS.md for detailed requirements.
		// This is the ideal integration point for metrics collection before shutdown.

		if err := o.restartAgent(ctx, notification.AgentID); err != nil {
			o.logger.Error("Failed to restart agent %s: %v", notification.AgentID, err)
		}
	}
}

// getAgentType returns the type of an agent (coder or architect).
func (o *Orchestrator) getAgentType(agentID string) string {
	if agentType, exists := o.agentTypes[agentID]; exists {
		return agentType
	}
	return "unknown"
}

// restartAgent shuts down and recreates an agent with the same configuration.
func (o *Orchestrator) restartAgent(ctx context.Context, agentID string) error {
	o.logger.Info("Restarting agent %s", agentID)

	// Get the agent.
	agentWrapper, exists := o.agents[agentID]
	if !exists {
		return fmt.Errorf("agent %s not found", agentID)
	}

	// Extract the underlying agent.
	var oldAgent agent.Driver
	if wrapper, ok := agentWrapper.(*StatusAgentWrapper); ok {
		if driver, ok := wrapper.Agent.(agent.Driver); ok {
			oldAgent = driver
		}
	}

	if oldAgent == nil {
		return fmt.Errorf("agent %s is not a valid driver", agentID)
	}

	// Shutdown the old agent.
	if err := oldAgent.Shutdown(ctx); err != nil {
		o.logger.Error("Failed to shutdown old agent %s: %v", agentID, err)
		// Continue anyway - we'll create a new one.
	}

	// Remove from dispatcher.
	o.dispatcher.Detach(agentID)

	// Clear any story lease for this agent.
	o.dispatcher.ClearLease(agentID)

	// Clean up all resources for this agent.
	if err := o.cleanupAgentResources(ctx, agentID); err != nil {
		o.logger.Error("Failed to cleanup resources for agent %s: %v", agentID, err)
		// Continue anyway.
	}

	// Remove from agents map.
	delete(o.agents, agentID)

	// Recreate the agent with the same configuration.
	if err := o.recreateAgent(ctx, agentID); err != nil {
		return fmt.Errorf("failed to recreate agent %s: %w", agentID, err)
	}

	o.logger.Info("Successfully restarted agent %s", agentID)
	return nil
}

// cleanupAgentResources removes all resources associated with an agent using shared cleanup logic.
func (o *Orchestrator) cleanupAgentResources(ctx context.Context, agentID string) error {
	cleanupCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Convert agent ID to filesystem-safe format using shared helper.
	fsafeID := utils.SanitizeIdentifier(agentID)

	// Stop all containers associated with this agent using global registry.
	registry := execpkg.GetGlobalRegistry()
	if registry != nil {
		containers := registry.GetContainersByAgent(fsafeID)
		if len(containers) > 0 {
			o.logger.Info("Stopping %d containers for agent %s", len(containers), agentID)
			// Create executor to stop containers.
			executor := execpkg.NewLongRunningDockerExec("alpine:latest", "") // Empty agentID - for cleanup only
			for i := range containers {
				containerInfo := &containers[i]
				if err := executor.StopContainer(cleanupCtx, containerInfo.ContainerName); err != nil {
					o.logger.Error("Failed to stop container %s for agent %s: %v", containerInfo.ContainerName, agentID, err)
				} else {
					o.logger.Info("Stopped container %s for agent %s", containerInfo.ContainerName, agentID)
				}
			}
		}
	}

	// Try to extract container name from the agent if possible (legacy fallback).
	var containerName string
	if agentWrapper, exists := o.agents[agentID]; exists {
		if wrapper, ok := agentWrapper.(*StatusAgentWrapper); ok {
			if coderAgent, ok := wrapper.Agent.(*coder.Coder); ok {
				containerName = coderAgent.GetContainerName() // We'll need to add this method
			}
		}
	}

	// Create a temporary clone manager for cleanup with the same configuration.
	if o.config.Git != nil && o.config.Git.RepoURL != "" {
		gitRunner := coder.NewDefaultGitRunner()
		cloneManager := coder.NewCloneManager(
			gitRunner,
			o.projectDir,
			o.config.Git.RepoURL,
			o.config.Git.TargetBranch,
			o.config.Git.MirrorDir,
			o.config.Git.BranchPattern,
		)

		// Set up paths for cleanup.
		agentWorkDir := filepath.Join(o.projectDir, fsafeID)
		agentStateDir := filepath.Join(o.projectDir, "states", fsafeID)

		// Use the shared comprehensive cleanup method.
		if err := cloneManager.CleanupAgentResources(cleanupCtx, agentID, containerName, agentWorkDir, agentStateDir); err != nil {
			wrapped := fmt.Errorf("comprehensive cleanup failed for agent %s: %w", agentID, err)
			o.logger.Error("%v", wrapped)
			return wrapped
		}

		o.logger.Info("Successfully completed comprehensive cleanup for agent %s", agentID)
	} else {
		// Fallback to basic cleanup if no repo URL configured.
		o.logger.Warn("No repo URL configured, using basic cleanup for agent %s", agentID)

		agentWorkDir := filepath.Join(o.projectDir, fsafeID)
		agentStateDir := filepath.Join(o.projectDir, "states", fsafeID)

		if err := os.RemoveAll(agentWorkDir); err != nil {
			o.logger.Error("Failed to remove agent work directory %s: %v", agentWorkDir, err)
		}

		if err := os.RemoveAll(agentStateDir); err != nil {
			o.logger.Error("Failed to remove agent state directory %s: %v", agentStateDir, err)
		}
	}

	return nil
}

// getArchitectModel returns the architect model configuration.
func (o *Orchestrator) getArchitectModel() *config.Model {
	// Find the architect model from the config
	for i := range o.config.Orchestrator.Models {
		if o.config.Orchestrator.Models[i].Name == o.config.Agents.ArchitectModel {
			return &o.config.Orchestrator.Models[i]
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

// getCoderModel returns the coder model configuration.
func (o *Orchestrator) getCoderModel() *config.Model {
	// Find the coder model from the config
	for i := range o.config.Orchestrator.Models {
		if o.config.Orchestrator.Models[i].Name == o.config.Agents.CoderModel {
			return &o.config.Orchestrator.Models[i]
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

// recreateAgent recreates an agent with the same configuration.
func (o *Orchestrator) recreateAgent(ctx context.Context, agentID string) error {
	// Simple agent recreation - determine type from ID pattern
	fsafeID := strings.ReplaceAll(agentID, ":", "-")
	agentWorkDir := filepath.Join(o.projectDir, fsafeID)

	if strings.HasPrefix(agentID, "coder-") {
		// Recreate coder agent
		coderModel := o.getCoderModel()

		// Create clone manager.
		var cloneManager *coder.CloneManager
		if o.config.Git != nil && o.config.Git.RepoURL != "" {
			gitRunner := coder.NewDefaultGitRunner()
			cloneManager = coder.NewCloneManager(
				gitRunner,
				o.projectDir,
				o.config.Git.RepoURL,
				o.config.Git.TargetBranch,
				o.config.Git.MirrorDir,
				o.config.Git.BranchPattern,
			)
			o.logger.Info("Recreated clone manager for coder %s", agentID)
		} else {
			return fmt.Errorf("no Git repository URL configured for coder agent recreation")
		}

		// Create coder agent.
		coderAgent, err := coder.NewCoder(agentID, agentID, agentWorkDir, coderModel, os.Getenv("ANTHROPIC_API_KEY"), cloneManager, o.buildService)
		if err != nil {
			return fmt.Errorf("failed to create coder agent %s: %w", agentID, err)
		}

		// Initialize the agent.
		if err := coderAgent.Initialize(ctx); err != nil {
			return fmt.Errorf("failed to initialize coder %s: %w", agentID, err)
		}

		// Start the agent's state machine.
		go func() {
			if err := coderAgent.Run(ctx); err != nil {
				o.logger.Error("Recreated coder %s state machine error: %v", agentID, err)
			}
		}()

		// Attach to dispatcher.
		o.dispatcher.Attach(coderAgent)

		// Add to agents map.
		o.agents[agentID] = &StatusAgentWrapper{Agent: coderAgent}
		o.logger.Info("Recreated and registered agent: %s", agentID)

		return nil
	}

	return fmt.Errorf("agent recreation not supported for agent ID pattern: %s", agentID)
}

// StartWebUI starts the web UI server.
func (o *Orchestrator) StartWebUI(ctx context.Context, port int) {
	o.logger.Info("Starting web UI server on port %d", port)

	// REMOVED: Filesystem state store - state persistence is now handled by SQLite
	// Web UI will need to be updated to use SQLite for agent state display

	// Create web UI server.
	o.webServer = webui.NewServer(o.dispatcher, nil, o.projectDir)

	// Start server in background.
	go func() {
		if err := o.webServer.StartServer(ctx, port); err != nil {
			o.logger.Error("Web UI server error: %v", err)
		}
	}()

	o.logger.Info("Web UI server started successfully")
}

// openBrowser opens the default browser to the given URL.
func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
		args = []string{url}
	}

	if err := exec.Command(cmd, args...).Start(); err != nil {
		return fmt.Errorf("failed to start command %s: %w", cmd, err)
	}
	return nil
}

// handleBootstrap handles the maestro bootstrap command for automated setup.
func handleBootstrap(args []string) {
	// Parse command line flags
	bootstrapFlags := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	projectDir := bootstrapFlags.String("projectdir", "", "Project directory (required)")
	specContent := bootstrapFlags.String("spec", "", "Bootstrap specification content (optional)")
	if err := bootstrapFlags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse flags: %v\n", err)
		os.Exit(1)
	}

	if *projectDir == "" {
		fmt.Fprintf(os.Stderr, "Project directory must be specified with -projectdir flag\n")
		os.Exit(1)
	}

	fmt.Println("🚀 Maestro Bootstrap Flow")
	fmt.Println("Using agent helpers for automated container and build setup.")
	fmt.Println()

	// Load orchestrator configuration
	if loadErr := config.LoadConfig(*projectDir); loadErr != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", loadErr)
		os.Exit(1)
	}
	cfg, err := config.GetConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get config: %v\n", err)
		os.Exit(1)
	}

	// Create bootstrap runner
	runner, err := NewBootstrapRunner(&cfg, *projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create bootstrap runner: %v\n", err)
		os.Exit(1)
	}

	// Generate spec content if not provided
	var bootstrapSpec string
	if *specContent != "" {
		bootstrapSpec = *specContent
	} else {
		// Generate bootstrap spec from project config and verification
		bootstrapSpec, err = generateBootstrapSpec(*projectDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to generate bootstrap spec: %v\n", err)
			os.Exit(1)
		}
	}

	// Run bootstrap flow with cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Run bootstrap in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- runner.RunBootstrap(ctx, bootstrapSpec)
	}()

	// Wait for completion or signal
	var exitCode int
	select {
	case err := <-errChan:
		if err != nil {
			// Cleanup on failure
			_ = runner.Shutdown(ctx)
			fmt.Fprintf(os.Stderr, "Bootstrap failed: %v\n", err)
			exitCode = 1
		} else {
			// Cleanup on success
			if err := runner.Shutdown(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to shutdown cleanly: %v\n", err)
			}
		}
	case sig := <-sigChan:
		fmt.Printf("\nReceived signal %s, shutting down gracefully...\n", sig)
		cancel() // Cancel context to stop bootstrap

		// Wait for graceful shutdown with timeout
		func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer shutdownCancel()

			if err := runner.Shutdown(shutdownCtx); err != nil {
				fmt.Fprintf(os.Stderr, "Shutdown error: %v\n", err)
			}
		}()
		fmt.Println("Bootstrap shutdown complete")
		exitCode = 0
	}

	// Clean up and exit
	cancel()
	if exitCode != 0 {
		os.Exit(exitCode)
	}

	fmt.Println("✅ Bootstrap completed successfully!")
	fmt.Println("🎯 Project is ready for development with validated container and build system.")
}

// generateBootstrapSpec generates a bootstrap specification from current project state.
func generateBootstrapSpec(projectDir string) (string, error) {
	// Get project configuration (should already be loaded)
	cfg, err := config.GetConfig()
	if err != nil {
		// Try loading if not already loaded
		if loadErr := config.LoadConfig(projectDir); loadErr != nil {
			return "", fmt.Errorf("failed to load project config: %w", loadErr)
		}
		cfg, err = config.GetConfig()
		if err != nil {
			return "", fmt.Errorf("failed to get project config: %w", err)
		}
	}

	// Run workspace verification to identify what needs bootstrapping
	logger := logx.NewLogger("bootstrap-spec")
	ctx := context.Background()
	opts := workspace.VerifyOptions{
		Logger:  logger,
		Timeout: 30 * time.Second,
		Fast:    false,
	}

	report, err := workspace.VerifyWorkspace(ctx, projectDir, opts)
	if err != nil {
		return "", fmt.Errorf("workspace verification failed: %w", err)
	}

	// Generate spec content using template system
	projectName := ""
	platform := ""
	containerImage := ""
	gitRepoURL := ""
	dockerfilePath := ""

	if cfg.Project != nil {
		projectName = cfg.Project.Name
		// Platform field was removed, use generic
		platform = genericPlatform
		if cfg.Git != nil {
			gitRepoURL = cfg.Git.RepoURL
		}
	}
	if projectName == "" {
		projectName = filepath.Base(projectDir)
	}
	if platform == "" {
		platform = genericPlatform
	}

	if cfg.Container != nil {
		containerImage = cfg.Container.Name
		dockerfilePath = cfg.Container.Dockerfile // From was replaced with Dockerfile
	}

	return generateBootstrapSpecContent(projectName, platform, containerImage, gitRepoURL, dockerfilePath, report)
}

// handleInit handles the maestro init command for interactive project setup.
func handleInit(args []string) {
	// Parse command line flags
	initFlags := flag.NewFlagSet("init", flag.ExitOnError)
	gitRepo := initFlags.String("git-repo", "", "Git repository URL (required)")
	if err := initFlags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse flags: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("🚀 Maestro Project Initialization")
	fmt.Println("This will set up a new Maestro project with configuration and bootstrap.")
	fmt.Println()

	// Load orchestrator configuration
	projectDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get current directory: %v\n", err)
		os.Exit(1)
	}

	if loadErr := config.LoadConfig(projectDir); loadErr != nil {
		fmt.Fprintf(os.Stderr, "Failed to load orchestrator config: %v\n", loadErr)
		os.Exit(1)
	}
	orchestratorConfig, err := config.GetConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get orchestrator config: %v\n", err)
		os.Exit(1)
	}

	// Get current working directory
	currentDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get current directory: %v\n", err)
		os.Exit(1)
	}

	// Run new git-first initialization flow
	if err := initializeProject(currentDir, *gitRepo, &orchestratorConfig); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize project: %v\n", err)
		os.Exit(1)
	}
}

const (
	genericPlatform = "generic"
)

// getTargetBranch prompts user for target branch using config default.
func getTargetBranch() string {
	scanner := bufio.NewScanner(os.Stdin)

	// Use current config as default
	currentConfig, err := config.GetConfig()
	if err != nil {
		// Config not loaded yet - that's fine for init
		currentConfig = config.Config{}
	}
	defaultBranch := "main"
	if currentConfig.Git != nil && currentConfig.Git.TargetBranch != "" {
		defaultBranch = currentConfig.Git.TargetBranch // TargetBranch moved to Git config as BaseBranch
	}

	fmt.Printf("🌿 Target branch [%s]: ", defaultBranch)
	targetBranch := defaultBranch
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			targetBranch = input
		}
	}

	// Update config with user choice - TargetBranch moved to Git config
	// Note: Would need to update Git.BaseBranch instead, but this is legacy code
	// if currentConfig.Git != nil {
	//	currentConfig.Git.TargetBranch = targetBranch
	// }

	return targetBranch
}

// handleBootstrapOrComplete handles the final step of the initialization flow.
//
//nolint:unused // Legacy function kept for reference
func handleBootstrapOrComplete(projectDir string, projectConfig, orchestratorConfig *config.Config, report *workspace.VerifyReport, verifyErr error, logger *logx.Logger) error {
	if verifyErr != nil {
		// Workspace verification failed - we need bootstrap
		fmt.Println("⚙️  Bootstrap required - will validate build system, container, and git access")

		// Check dockerfile mode and build bootstrap container if needed (before user confirmation)
		if projectConfig.Container != nil && projectConfig.Container.Dockerfile != "" && projectConfig.Container.Name == "" {
			logger.Info("🐳 Dockerfile mode detected - building bootstrap container as prerequisite")
			if buildErr := buildBootstrapContainerForInit(projectDir, logger); buildErr != nil {
				return fmt.Errorf("failed to build bootstrap container: %w", buildErr)
			}
			logger.Info("✅ Bootstrap container ready")
		}

		// Ask for user confirmation after container building but before starting expensive agents
		if !confirmBootstrap() {
			return fmt.Errorf("bootstrap cancelled by user")
		}

		fmt.Println("🚀 Starting automated bootstrap flow...")

		// Generate bootstrap spec content directly (no file creation)
		projectName := projectConfig.Project.Name
		if projectName == "" {
			projectName = filepath.Base(projectDir)
		}
		// Platform field was removed, use generic
		platform := genericPlatform
		containerImage := ""
		var gitRepoURL string
		var dockerfilePath string
		if projectConfig.Container != nil {
			containerImage = projectConfig.Container.Name
			dockerfilePath = projectConfig.Container.Dockerfile
		}
		if projectConfig.Git != nil {
			gitRepoURL = projectConfig.Git.RepoURL
		}

		bootstrapSpec, specErr := generateBootstrapSpecContent(projectName, platform, containerImage, gitRepoURL, dockerfilePath, report)
		if specErr != nil {
			return fmt.Errorf("failed to generate bootstrap spec content: %w", specErr)
		}

		// Run bootstrap flow directly using the already loaded orchestrator config
		if bootstrapErr := runBootstrapFlow(projectDir, orchestratorConfig, bootstrapSpec); bootstrapErr != nil {
			return fmt.Errorf("bootstrap failed: %w", bootstrapErr)
		}

		fmt.Println("✅ Bootstrap completed successfully!")
	} else {
		fmt.Println("🎉 All requirements already met - bootstrap not needed!")
		// Mark bootstrap as completed since verification passed
		bootstrapInfo := map[string]interface{}{
			"completed": true,
			"last_run":  time.Now(),
		}
		if err := config.UpdateBootstrap(projectDir, bootstrapInfo); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to update bootstrap status: %v\n", err)
		}
	}
	return nil
}

// initializeProject implements the new git-first project initialization flow.
//
//nolint:cyclop // Complex initialization flow requires multiple steps
func initializeProject(projectDir, gitRepoFlag string, orchestratorConfig *config.Config) error {
	logger := logx.NewLogger("init")

	// Step 0: Config should already be loaded by handleInit function
	logger.Info("Using global config singleton")

	// Step 1: Gather required information
	gitRepo, err := getGitRepository(gitRepoFlag)
	if err != nil {
		return fmt.Errorf("failed to get git repository: %w", err)
	}

	// Step 2: Ask for target branch first
	targetBranch := getTargetBranch()

	// Step 3: Create git mirror and temporary worktree
	fmt.Println("🔗 Setting up git repository access...")
	if setupErr := setupGitMirror(projectDir, gitRepo); setupErr != nil {
		return fmt.Errorf("failed to setup git mirror: %w", setupErr)
	}

	// Step 4: Detect platform from actual repository files using target branch and keep worktree
	fmt.Println("🔍 Detecting platform from repository files...")
	platform, confidence, worktreePath, cleanupFn, err := detectPlatformAndCreateWorktree(projectDir, targetBranch)
	if err != nil {
		return fmt.Errorf("failed to detect platform: %w", err)
	}
	fmt.Printf("📋 Detected platform: %s (confidence: %.0f%%)\n", platform, confidence*100)

	// Ensure worktree cleanup happens at the end
	if cleanupFn != nil {
		defer cleanupFn()
	}

	// Step 5: Gather remaining user input for customization
	params, _, err := gatherUserInputNew(platform, confidence, projectDir, gitRepo, targetBranch, worktreePath)
	if err != nil {
		return fmt.Errorf("failed to gather user input: %w", err)
	}

	// Step 5: Update global config with user choices using the atomic update functions
	// Update project info
	projectInfo := &config.ProjectInfo{
		Name:            params.name,
		PrimaryPlatform: params.platform, // Set user-confirmed platform
	}
	if updateErr := config.UpdateProject(projectInfo); updateErr != nil {
		return fmt.Errorf("failed to update project info: %w", updateErr)
	}

	// Update container config based on user selection
	containerConfig := &config.ContainerConfig{
		Name: params.containerName,
		// NeedsRebuild field removed - build state tracked in database
		// From field removed - use direct Dockerfile field
	}

	// Configure container source based on user selection
	switch params.containerSource {
	case containerSourceDockerfile:
		if params.dockerfilePath != "" {
			containerConfig.Dockerfile = params.dockerfilePath
			// Note: Fallback container logic moved to runtime
		}
	case containerSourceImageName:
		if params.containerImage != "" {
			containerConfig.Name = params.containerImage
			// Clear dockerfile since using direct image
			containerConfig.Dockerfile = ""
		}
	default: // "detect" or other
		// Use default Go image
		containerConfig.Name = config.DefaultGoDockerImage
	}

	if updateErr := config.UpdateContainer(containerConfig); updateErr != nil {
		return fmt.Errorf("failed to update container config: %w", updateErr)
	}

	// Update git config with repository URL and target branch
	gitConfig := &config.GitConfig{
		RepoURL:       gitRepo,
		TargetBranch:  targetBranch,
		MirrorDir:     config.DefaultMirrorDir,
		BranchPattern: config.DefaultBranchPattern,
	}
	if updateErr := config.UpdateGit(gitConfig); updateErr != nil {
		return fmt.Errorf("failed to update git config: %w", updateErr)
	}

	// Step 6: Setup project infrastructure
	if setupErr := setupProjectInfrastructureNew(projectDir); setupErr != nil {
		return fmt.Errorf("failed to setup project infrastructure: %w", setupErr)
	}

	// Step 7: Initialize SQLite database with schema
	if dbErr := initializeDatabase(projectDir); dbErr != nil {
		return fmt.Errorf("failed to initialize database: %w", dbErr)
	}

	// Step 8: Verify workspace with new verification system
	fmt.Println("🔍 Verifying workspace setup...")
	report, err := verifyNewWorkspace(projectDir, logger, nil)

	// Handle bootstrap or completion
	if handleErr := handleBootstrapOrCompleteNew(projectDir, orchestratorConfig, report, err, logger); handleErr != nil {
		return handleErr
	}

	fmt.Println("✅ Project initialization completed successfully!")
	printNextStepsNew()
	return nil
}

// setupProjectInfrastructureNew creates necessary directories using the global config system.
func setupProjectInfrastructureNew(projectDir string) error {
	// Create .maestro directory
	maestroDir := filepath.Join(projectDir, config.ProjectConfigDir)
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return fmt.Errorf("failed to create .maestro directory: %w", err)
	}
	// Config is automatically saved by the atomic update functions
	return nil
}

// handleBootstrapOrCompleteNew handles bootstrap or completion using the global config system.
func handleBootstrapOrCompleteNew(projectDir string, orchestratorConfig *config.Config, report *workspace.VerifyReport, verifyErr error, logger *logx.Logger) error {
	if verifyErr != nil {
		logger.Warn("Workspace verification failed: %v", verifyErr)

		// Check if we need to build maestro-bootstrap container
		cfg, err := config.GetConfig()
		if err != nil {
			return fmt.Errorf("failed to get config for bootstrap handling: %w", err)
		}

		// Build maestro-bootstrap container if dockerfile is specified
		if cfg.Container != nil && cfg.Container.Dockerfile != "" {
			fmt.Println("🔨 Building maestro-bootstrap container for dockerfile...")
			if buildErr := buildBootstrapContainerForInit(projectDir, logger); buildErr != nil {
				logger.Error("Failed to build bootstrap container: %v", buildErr)
				return fmt.Errorf("failed to build bootstrap container: %w", buildErr)
			}
		}

		// Ask for user confirmation before starting bootstrap
		if !confirmBootstrap() {
			fmt.Println("❌ Bootstrap cancelled by user")
			return nil
		}

		// Start agent bootstrap cycle
		fmt.Println("🚀 Starting agent bootstrap cycle to fix issues...")
		if bootstrapErr := runAgentBootstrap(projectDir, orchestratorConfig, report, logger); bootstrapErr != nil {
			logger.Error("Agent bootstrap failed: %v", bootstrapErr)
			return fmt.Errorf("agent bootstrap failed: %w", bootstrapErr)
		}

		// Update bootstrap status to indicate completion needed
		bootstrapInfo := map[string]interface{}{
			"completed":         false,
			"last_run":          time.Now(),
			"requirements_met":  make(map[string]bool),
			"validation_errors": []string{verifyErr.Error()},
		}
		if err := config.UpdateBootstrap(projectDir, bootstrapInfo); err != nil {
			return fmt.Errorf("failed to update bootstrap status: %w", err)
		}

		fmt.Println("⚠️  Workspace verification failed. Bootstrap will be required.")
		fmt.Printf("📋 Verification error: %v\n", verifyErr)
		return nil
	}

	// Verification passed - mark bootstrap as completed
	bootstrapInfo := map[string]interface{}{
		"completed":         true,
		"last_run":          time.Now(),
		"requirements_met":  make(map[string]bool),
		"validation_errors": []string{},
	}
	if err := config.UpdateBootstrap(projectDir, bootstrapInfo); err != nil {
		return fmt.Errorf("failed to update bootstrap status: %w", err)
	}

	fmt.Println("✅ Workspace verification passed!")
	if report != nil {
		fmt.Printf("📋 Verification completed successfully\n")
	}
	return nil
}

// confirmBootstrap asks the user for confirmation before starting bootstrap.
func confirmBootstrap() bool {
	fmt.Print("Continue with bootstrap setup? [Yn]: ")
	var input string
	_, _ = fmt.Scanln(&input)
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "n" || input == "no" {
		return false
	}
	return true
}

// detectPlatform analyzes the current directory to detect the platform type.
func detectPlatform(dir string) (string, float64) {
	backends := []build.Backend{
		build.NewGoBackend(),
		build.NewNodeBackend(),
		build.NewPythonBackend(),
	}

	for _, backend := range backends {
		if backend.Detect(dir) {
			confidence := 0.95 // High confidence for specific file detection
			return backend.Name(), confidence
		}
	}

	// Check for Makefile as generic indicator
	if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
		return genericPlatform, 0.7
	}

	// Default to null backend for empty repositories
	return "null", 0.5
}

// getGitRepository gets git repository URL from flag or user input.
func getGitRepository(gitRepoFlag string) (string, error) {
	if gitRepoFlag != "" {
		fmt.Printf("🔗 Git repository URL: %s (from command line)\n", gitRepoFlag)
		return gitRepoFlag, nil
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("🔗 Git repository URL (required): ")
		if !scanner.Scan() {
			return "", fmt.Errorf("failed to read input")
		}

		gitRepo := strings.TrimSpace(scanner.Text())
		if gitRepo != "" {
			return gitRepo, nil
		}
		fmt.Println("❌ Git repository URL is required. Please enter a valid URL.")
	}
}

// setupGitMirror creates the git mirror and .mirrors directory.
func setupGitMirror(projectDir, gitRepo string) error {
	// Ensure project directory exists and is accessible
	if projectDir == "" {
		return fmt.Errorf("project directory is empty")
	}

	// Verify project directory exists
	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		return fmt.Errorf("project directory does not exist: %s", projectDir)
	}

	mirrorsDir := filepath.Join(projectDir, ".mirrors")

	// For init command, always clean up existing mirrors directory for fresh start
	if _, err := os.Stat(mirrorsDir); err == nil {
		fmt.Printf("🧹 Cleaning up existing .mirrors directory for fresh initialization\n")
		if removeErr := os.RemoveAll(mirrorsDir); removeErr != nil {
			return fmt.Errorf("failed to remove existing .mirrors directory: %w", removeErr)
		}
	}

	// Create fresh .mirrors directory
	fmt.Printf("📁 Creating fresh .mirrors directory: %s\n", mirrorsDir)
	if err := os.MkdirAll(mirrorsDir, 0755); err != nil {
		return fmt.Errorf("failed to create .mirrors directory: %w", err)
	}

	// Extract repository name from URL for mirror directory
	repoName := extractRepoName(gitRepo)
	mirrorPath := filepath.Join(mirrorsDir, repoName+".git")

	// Clone as bare repository (mirror)
	cmd := exec.Command("git", "clone", "--mirror", gitRepo, mirrorPath)
	cmd.Dir = projectDir // Set working directory explicitly
	cmd.Env = append(os.Environ(),
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=10",
		"GIT_TERMINAL_PROMPT=0",
	)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w (output: %s)", err, string(output))
	}

	fmt.Printf("📦 Git mirror created: %s\n", mirrorPath)
	return nil
}

// extractRepoName extracts repository name from git URL.
func extractRepoName(gitURL string) string {
	// Handle both SSH and HTTPS URLs
	parts := strings.Split(gitURL, "/")
	if len(parts) == 0 {
		return "repo"
	}

	repoName := parts[len(parts)-1]
	// Remove .git suffix if present
	repoName = strings.TrimSuffix(repoName, ".git")

	return repoName
}

// detectPlatformAndCreateWorktree detects platform and creates a persistent worktree for dockerfile scanning.
func detectPlatformAndCreateWorktree(projectDir, targetBranch string) (string, float64, string, func(), error) {
	// Get mirror path - extract from git repository setup
	mirrorPath := filepath.Join(projectDir, ".mirrors")

	// Find the actual mirror directory (should only be one)
	entries, err := os.ReadDir(mirrorPath)
	if err != nil || len(entries) == 0 {
		return genericPlatform, 0.5, "", nil, fmt.Errorf("git mirror not found")
	}

	actualMirrorPath := filepath.Join(mirrorPath, entries[0].Name())

	// Create persistent worktree for platform detection and dockerfile scanning
	worktreePath := filepath.Join(actualMirrorPath, "dockerfile-scan")

	// Clean up any existing worktree
	_ = exec.Command("git", "-C", actualMirrorPath, "worktree", "remove", "--force", worktreePath).Run()

	// Create worktree using target branch
	cmd := exec.Command("git", "-C", actualMirrorPath, "worktree", "add", "--detach", worktreePath, targetBranch)
	if err := cmd.Run(); err != nil {
		// Fallback to HEAD if target branch doesn't exist
		cmd = exec.Command("git", "-C", actualMirrorPath, "worktree", "add", "--detach", worktreePath, "HEAD")
		if err := cmd.Run(); err != nil {
			return genericPlatform, 0.5, "", nil, fmt.Errorf("failed to create worktree: %w", err)
		}
	}

	// Create cleanup function
	cleanupFn := func() {
		_ = exec.Command("git", "-C", actualMirrorPath, "worktree", "remove", "--force", worktreePath).Run()
	}

	// Use existing platform detection on the worktree
	platform, confidence := detectPlatform(worktreePath)
	return platform, confidence, worktreePath, cleanupFn, nil
}

// getContainerSourceDefaults determines default container source from existing config.
func getContainerSourceDefaults(existingConfig *config.Config) string {
	if existingConfig != nil && existingConfig.Container != nil {
		if existingConfig.Container.Dockerfile != "" {
			return containerSourceDockerfile
		} else if existingConfig.Container.Name != "" {
			return containerSourceImageName
		}
	}
	return containerSourceDetect
}

// getPlatformDefault determines default platform from existing config or detected platform.
func getPlatformDefault(_ *config.Config, detectedPlatform string) string {
	// Platform field was removed from ProjectInfo - always use detected platform
	return detectedPlatform
}

// gatherUserInputNew collects user input for the new flow using existing config as defaults.
//
//nolint:cyclop // Complex user input collection requires multiple conditional branches
func gatherUserInputNew(platform string, confidence float64, projectDir, gitRepo, targetBranch, worktreePath string) (projectParamsNew, float64, error) {
	scanner := bufio.NewScanner(os.Stdin)

	// Get existing config to use as defaults
	existingConfig, err := config.GetConfig()
	if err != nil {
		// Config not loaded yet - use defaults
		existingConfig = config.Config{}
	}

	// Use platform defaults
	defaultPlatform := getPlatformDefault(&existingConfig, platform)

	// Confirm platform with existing config as default
	fmt.Printf("🎯 Confirm platform [%s]: ", defaultPlatform)
	finalPlatform := defaultPlatform
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			finalPlatform = input
			confidence = 1.0 // User override = 100% confidence
		}
	}

	// Container source selection with existing config as default
	defaultContainerSource := getContainerSourceDefaults(&existingConfig)
	fmt.Printf("🐳 Container source (dockerfile/imagename/detect) [%s]: ", defaultContainerSource)
	containerSource := defaultContainerSource
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			containerSource = input
		}
	}

	// Collect container information based on source type
	var containerName, dockerfilePath, containerImage string

	switch containerSource {
	case containerSourceDockerfile:
		selectedDockerfile := selectDockerfile(scanner, worktreePath)
		if selectedDockerfile == "" {
			return projectParamsNew{}, 0, fmt.Errorf("dockerfile selection cancelled")
		}
		dockerfilePath = selectedDockerfile
		// Container name should be blank when using dockerfile - will be set after build
		containerName = ""
	case containerSourceImageName:
		fmt.Print("📦 Enter image name: ")
		if scanner.Scan() {
			imageName := strings.TrimSpace(scanner.Text())
			if imageName == "" {
				return projectParamsNew{}, 0, fmt.Errorf("image name cannot be empty")
			}
			containerImage = imageName
			containerName = imageName
		} else {
			return projectParamsNew{}, 0, fmt.Errorf("failed to read image name")
		}
	case containerSourceDetect:
		containerName = containerSourceDetect
	default:
		return projectParamsNew{}, 0, fmt.Errorf("invalid container source: %s", containerSource)
	}

	// Project name with existing config as default
	defaultProjectName := filepath.Base(projectDir)
	if existingConfig.Project != nil && existingConfig.Project.Name != "" {
		defaultProjectName = existingConfig.Project.Name
	}

	fmt.Printf("📁 Project name [%s]: ", defaultProjectName)
	projectName := defaultProjectName
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			projectName = input
		}
	}

	return projectParamsNew{
		name:            projectName,
		gitRepo:         gitRepo,
		targetBranch:    targetBranch,
		platform:        finalPlatform,
		containerName:   containerName,
		containerSource: containerSource,
		dockerfilePath:  dockerfilePath,
		containerImage:  containerImage,
	}, confidence, nil
}

// projectParamsNew holds project parameters for new flow.
type projectParamsNew struct {
	name            string
	gitRepo         string
	targetBranch    string
	platform        string
	containerName   string
	containerSource string // "dockerfile", "imagename", or "detect"
	dockerfilePath  string // Path to dockerfile if containerSource is "dockerfile"
	containerImage  string // Image name if containerSource is "imagename"
}

// updateProjectConfigWithUserChoices updates existing project config with user choices.
//
//nolint:unused // Legacy function kept for reference
func updateProjectConfigWithUserChoices(projectConfig *config.Config, params *projectParamsNew, _ float64) {
	// now := time.Now() // Unused since time fields were removed

	// Ensure schema version is set
	if projectConfig.SchemaVersion == "" {
		projectConfig.SchemaVersion = config.SchemaVersion
	}

	// Update project info
	projectConfig.Project.Name = params.name
	// GitRepo and TargetBranch fields removed - these belong in GitConfig now
	// projectConfig.Project.GitRepo = params.gitRepo
	// projectConfig.Project.TargetBranch = params.targetBranch
	// Platform and PlatformConfidence fields removed - projects are generic by default
	// projectConfig.Project.Platform = params.platform
	// projectConfig.Project.PlatformConfidence = finalConfidence
	// CreatedAt field removed from ProjectInfo
	// if projectConfig.Project.CreatedAt.IsZero() {
	//	projectConfig.Project.CreatedAt = now
	// }

	// Update container configuration based on user selection
	// NeedsRebuild field removed - build state tracked in database
	// projectConfig.Container.NeedsRebuild = true // Always needs initial build

	if params.containerName == containerSourceDetect {
		// Detect mode: set name to "detect" and use bootstrap container temporarily
		projectConfig.Container.Name = containerSourceDetect
		// From field removed - using direct Dockerfile field
		// projectConfig.Container.From.Container = config.DefaultBootstrapDockerImage
		// projectConfig.Container.From.Dockerfile = "" // Clear any existing dockerfile
	} else if looksLikeDockerfile(params.containerName) {
		// Dockerfile mode: DON'T set Name (container doesn't exist yet, needs bootstrap)
		// Agent will set final name later with update_container tool after building
		projectConfig.Container.Name = "" // Clear name until container is built
		// From field removed - using direct Dockerfile field
		// projectConfig.Container.From.Dockerfile = params.containerName
		// projectConfig.Container.From.Container = config.DefaultBootstrapDockerImage
		projectConfig.Container.Dockerfile = params.containerName
	} else {
		// Image name mode: use the provided image name directly
		projectConfig.Container.Name = params.containerName
		// From field removed - using direct Name field
		// projectConfig.Container.From.Container = params.containerName
		projectConfig.Container.Dockerfile = "" // Clear any existing dockerfile
	}

	// Update build configuration with defaults if not set
	if projectConfig.Build.Build == "" {
		projectConfig.Build.Build = "make build"
	}
	if projectConfig.Build.Test == "" {
		projectConfig.Build.Test = "make test"
	}
	if projectConfig.Build.Lint == "" {
		projectConfig.Build.Lint = "make lint"
	}
	if projectConfig.Build.Run == "" {
		projectConfig.Build.Run = "make run"
	}

	// Initialize bootstrap info if needed
	// Bootstrap field removed from config - tracking moved to database
	// if projectConfig.Bootstrap.RequirementsMet == nil {
	//	projectConfig.Bootstrap.RequirementsMet = make(map[string]bool)
	// }
}

// looksLikeDockerfile checks if a name looks like a dockerfile path.
//
//nolint:unused // Legacy function kept for reference
func looksLikeDockerfile(name string) bool {
	return strings.Contains(name, "Dockerfile") || strings.HasSuffix(name, ".dockerfile")
}

// selectDockerfile scans for dockerfiles and lets user select one.
func selectDockerfile(scanner *bufio.Scanner, worktreePath string) string {
	fmt.Println("🔍 Scanning repository for dockerfiles...")

	dockerfiles := findDockerfiles(worktreePath)
	if len(dockerfiles) == 0 {
		fmt.Println("❌ No dockerfiles found in repository")
		fmt.Print("📝 Enter dockerfile path manually: ")
		if scanner.Scan() {
			return strings.TrimSpace(scanner.Text())
		}
		return ""
	}

	fmt.Println("\nFound dockerfiles:")
	for i, dockerfile := range dockerfiles {
		// Highlight .maestro/Dockerfile if it exists
		if strings.Contains(dockerfile, ".maestro/Dockerfile") {
			fmt.Printf("  %d. %s ⭐ (maestro dockerfile)\n", i+1, dockerfile)
		} else {
			fmt.Printf("  %d. %s\n", i+1, dockerfile)
		}
	}

	fmt.Printf("\nSelect dockerfile [1]: ")
	selection := "1"
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			selection = input
		}
	}

	// Parse selection
	index, err := strconv.Atoi(selection)
	if err != nil || index < 1 || index > len(dockerfiles) {
		fmt.Printf("Invalid selection: %s\n", selection)
		return ""
	}

	selectedDockerfile := dockerfiles[index-1]
	fmt.Printf("✅ Using %s\n", selectedDockerfile)
	return selectedDockerfile
}

// findDockerfiles searches for dockerfile patterns in the worktree.
func findDockerfiles(worktreePath string) []string {
	var dockerfiles []string

	// Priority patterns in order
	patterns := []string{
		".maestro/Dockerfile", // Maestro dockerfile (highest priority)
		"Dockerfile",          // Standard dockerfile
		"Dockerfile.*",        // Dockerfile variants
		"*.dockerfile",        // .dockerfile extension
	}

	// Check priority patterns first
	for _, pattern := range patterns {
		matches := findFilesByPattern(worktreePath, pattern)
		dockerfiles = append(dockerfiles, matches...)
	}

	// Remove duplicates while preserving order
	seen := make(map[string]bool)
	unique := make([]string, 0, len(dockerfiles))
	for _, dockerfile := range dockerfiles {
		if !seen[dockerfile] {
			seen[dockerfile] = true
			unique = append(unique, dockerfile)
		}
	}

	// Limit to reasonable number
	if len(unique) > 20 {
		unique = unique[:20]
	}

	return unique
}

// findFilesByPattern searches for files matching a pattern.
func findFilesByPattern(root, pattern string) []string {
	var matches []string

	// Handle simple cases first
	if !strings.Contains(pattern, "*") {
		fullPath := filepath.Join(root, pattern)
		if _, err := os.Stat(fullPath); err == nil {
			matches = append(matches, pattern)
		}
		return matches
	}

	// Use filepath.Walk for glob patterns
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // Continue walking on errors
		}

		// Skip hidden directories except .maestro
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") && info.Name() != ".maestro" {
			return filepath.SkipDir
		}

		if info.IsDir() {
			return nil
		}

		// Get relative path from root
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return nil //nolint:nilerr // Continue walking on path errors
		}

		// Check if filename matches pattern
		matched, err := filepath.Match(pattern, relPath)
		if err != nil {
			// Try matching just the filename
			matched, _ = filepath.Match(pattern, info.Name())
		}

		if matched {
			matches = append(matches, relPath)
		}

		return nil
	})

	if err != nil {
		// If walk fails, try simple glob
		globPattern := filepath.Join(root, pattern)
		globMatches, _ := filepath.Glob(globPattern)
		for _, match := range globMatches {
			if relPath, err := filepath.Rel(root, match); err == nil {
				matches = append(matches, relPath)
			}
		}
	}

	return matches
}

// setupProjectInfrastructure creates necessary directories and saves configuration.
//
//nolint:unused // Legacy function kept for reference
func setupProjectInfrastructure(projectDir string, _ *config.Config) error {
	// Create .maestro directory
	maestroDir := filepath.Join(projectDir, config.ProjectConfigDir)
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return fmt.Errorf("failed to create .maestro directory: %w", err)
	}

	// Save project configuration
	// Save method removed - using UpdateProject, UpdateContainer, etc. instead
	// if err := projectConfig.Save(projectDir); err != nil {
	//	return fmt.Errorf("failed to save project configuration: %w", err)
	// }

	fmt.Printf("✅ Project configuration saved to %s\n", filepath.Join(maestroDir, config.ProjectConfigFilename))
	return nil
}

// initializeDatabase creates and initializes the SQLite database with proper schema.
func initializeDatabase(projectDir string) error {
	dbPath := filepath.Join(projectDir, config.ProjectConfigDir, config.DatabaseFilename)

	// Create database with proper schema using persistence package
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer func() { _ = db.Close() }()

	fmt.Printf("🗄️  Database initialized: %s\n", dbPath)
	return nil
}

// verifyNewWorkspace runs comprehensive workspace verification.
func verifyNewWorkspace(projectDir string, logger *logx.Logger, _ *config.Config) (*workspace.VerifyReport, error) {
	ctx := context.Background()
	opts := workspace.VerifyOptions{
		Logger:  logger,
		Timeout: 30 * time.Second,
		Fast:    false, // Run full verification
	}

	report, err := workspace.VerifyWorkspace(ctx, projectDir, opts)
	if err != nil {
		return nil, fmt.Errorf("verification failed: %w", err)
	}

	// Display results
	if len(report.Warnings) > 0 {
		fmt.Printf("⚠️  Warnings:\n")
		for _, warning := range report.Warnings {
			fmt.Printf("   • %s\n", warning)
		}
	}

	if len(report.Failures) > 0 {
		fmt.Printf("❌ Failures:\n")
		for _, failure := range report.Failures {
			fmt.Printf("   • %s\n", failure)
		}
		return report, fmt.Errorf("workspace verification failed with %d failures", len(report.Failures))
	}

	fmt.Printf("✅ Workspace verification passed!\n")
	return report, nil
}

// generateBootstrapSpecContent generates bootstrap spec content using the template system.
func generateBootstrapSpecContent(projectName, platform, containerImage, gitRepoURL, dockerfilePath string, report *workspace.VerifyReport) (string, error) {
	// Use the existing bootstrap template renderer function
	spec, err := bootstrap.GenerateBootstrapSpecFromReportEnhanced(projectName, platform, containerImage, gitRepoURL, dockerfilePath, report)
	if err != nil {
		return "", fmt.Errorf("failed to generate bootstrap spec: %w", err)
	}
	return spec, nil
}

// runBootstrapFlow executes the bootstrap flow directly using the bootstrap runner.
//
//nolint:unused // Legacy function kept for reference
func runBootstrapFlow(projectDir string, cfg *config.Config, specContent string) error {
	// Create bootstrap runner
	runner, err := NewBootstrapRunner(cfg, projectDir)
	if err != nil {
		return fmt.Errorf("failed to create bootstrap runner: %w", err)
	}

	// Run bootstrap flow with spec content
	ctx := context.Background()
	if err := runner.RunBootstrap(ctx, specContent); err != nil {
		// Cleanup on failure
		_ = runner.Shutdown(ctx)
		return fmt.Errorf("bootstrap execution failed: %w", err)
	}

	// Cleanup on success
	if err := runner.Shutdown(ctx); err != nil {
		// Log warning but don't fail the whole bootstrap
		fmt.Printf("Warning: Failed to shutdown bootstrap runner cleanly: %v\n", err)
	}

	return nil
}

// printNextStepsNew displays next steps for the new flow.
func printNextStepsNew() {
	fmt.Println()
	fmt.Println("🎯 Next steps:")
	fmt.Println("   1. Review .maestro/config.json configuration")
	fmt.Println("   2. If using custom dockerfile, maestro-bootstrap container will be built automatically")
	fmt.Println("   3. Run: maestro -workdir . -spec <your-spec.md>")
	fmt.Println("   4. Agent bootstrap cycle will start to confirm everything and begin processing")
	fmt.Println("   5. Stories will be stored in SQLite database and managed automatically")
}

// buildBootstrapContainerForInit builds the maestro-bootstrap container during initialization.
func buildBootstrapContainerForInit(workDir string, logger *logx.Logger) error {
	const bootstrapTag = "maestro-bootstrap:latest"

	logger.Info("🔨 Ensuring bootstrap container: %s", bootstrapTag)

	// 1. Get the bootstrap dockerfile content from pkg/dockerfiles
	dockerfileContent := dockerfiles.GetBootstrapDockerfile()
	if dockerfileContent == "" {
		return fmt.Errorf("bootstrap dockerfile content is empty")
	}

	// 2. Write dockerfile to temporary file in .maestro directory
	maestroDir := filepath.Join(workDir, config.ProjectConfigDir)
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return fmt.Errorf("failed to create .maestro directory: %w", err)
	}

	dockerfilePath := filepath.Join(maestroDir, "bootstrap.dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644); err != nil {
		return fmt.Errorf("failed to write bootstrap dockerfile: %w", err)
	}

	// Clean up dockerfile after build
	defer func() {
		if removeErr := os.Remove(dockerfilePath); removeErr != nil {
			logger.Error("Failed to cleanup bootstrap dockerfile: %v", removeErr)
		}
	}()

	// 4. Run docker build with the dockerfile (quietly to reduce verbosity)
	logger.Info("🐳 Running docker build for bootstrap container...")
	buildCmd := exec.Command("docker", "build", "--quiet", "-t", bootstrapTag, "-f", dockerfilePath, maestroDir)
	// Only show output on error to reduce verbosity
	buildCmd.Stdout = nil
	buildCmd.Stderr = os.Stderr

	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}

	logger.Info("✅ Bootstrap container ready: %s", bootstrapTag)
	return nil
}

// runAgentBootstrap starts the agent bootstrap cycle to fix workspace issues.
func runAgentBootstrap(projectDir string, orchestratorConfig *config.Config, report *workspace.VerifyReport, logger *logx.Logger) error {
	logger.Info("🤖 Starting agent bootstrap cycle...")

	// Create bootstrap runner
	bootstrapRunner, err := NewBootstrapRunner(orchestratorConfig, projectDir)
	if err != nil {
		return fmt.Errorf("failed to create bootstrap runner: %w", err)
	}

	// Create bootstrap specification content using existing verification report
	bootstrapSpec, err := generateBootstrapSpecFromReport(projectDir, report)
	if err != nil {
		return fmt.Errorf("failed to generate bootstrap spec: %w", err)
	}

	// Run bootstrap with the generated spec
	ctx := context.Background()
	if err := bootstrapRunner.RunBootstrap(ctx, bootstrapSpec); err != nil {
		return fmt.Errorf("bootstrap execution failed: %w", err)
	}

	logger.Info("✅ Agent bootstrap cycle completed successfully")
	return nil
}

// generateBootstrapSpecFromReport creates bootstrap spec using existing verification report.
func generateBootstrapSpecFromReport(projectDir string, report *workspace.VerifyReport) (string, error) {
	// Get project configuration (should already be loaded)
	cfg, err := config.GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get project config: %w", err)
	}

	// Extract info from config
	projectName := ""
	platform := ""
	containerImage := ""
	gitRepoURL := ""
	dockerfilePath := ""

	if cfg.Project != nil {
		projectName = cfg.Project.Name
		platform = cfg.Project.PrimaryPlatform
		if cfg.Git != nil {
			gitRepoURL = cfg.Git.RepoURL
		}
	}
	if projectName == "" {
		projectName = filepath.Base(projectDir)
	}
	if platform == "" {
		platform = genericPlatform
	}

	if cfg.Container != nil {
		containerImage = cfg.Container.Name
		dockerfilePath = cfg.Container.Dockerfile
	}

	// Use the template system with the existing report
	return generateBootstrapSpecContent(projectName, platform, containerImage, gitRepoURL, dockerfilePath, report)
}
