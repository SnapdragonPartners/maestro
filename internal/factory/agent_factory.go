// Package factory provides lightweight agent creation functionality.
package factory

import (
	"context"
	"fmt"
	"path/filepath"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/architect"
	"orchestrator/pkg/build"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/coder"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/pm"
	"orchestrator/pkg/proto"
)

// AgentFactory creates agents with minimal dependencies.
// All configuration is sourced from config.GetConfig() inside agent constructors.
type AgentFactory struct {
	dispatcher         *dispatch.Dispatcher
	persistenceChannel chan<- *persistence.Request
	chatService        *chat.Service
	llmFactory         *agent.LLMClientFactory // Shared LLM factory for rate limiting
}

// NewAgentFactory creates a new lightweight agent factory.
func NewAgentFactory(dispatcher *dispatch.Dispatcher, persistenceChannel chan<- *persistence.Request, chatService *chat.Service, llmFactory *agent.LLMClientFactory) *AgentFactory {
	return &AgentFactory{
		dispatcher:         dispatcher,
		persistenceChannel: persistenceChannel,
		chatService:        chatService,
		llmFactory:         llmFactory,
	}
}

// NewAgent creates a new agent of the specified type.
// All configuration is derived from config.GetConfig() inside the agent constructor.
func (f *AgentFactory) NewAgent(ctx context.Context, agentID, agentType string) (dispatch.Agent, error) {
	switch agentType {
	case string(agent.TypeArchitect):
		return f.createArchitect(ctx, agentID)
	case string(agent.TypeCoder):
		return f.createCoder(ctx, agentID)
	case string(agent.TypePM):
		return f.createPM(ctx, agentID)
	default:
		return nil, fmt.Errorf("unknown agent type: %s", agentType)
	}
}

// createArchitect creates a new architect agent.
func (f *AgentFactory) createArchitect(ctx context.Context, agentID string) (dispatch.Agent, error) {
	// Get current config
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	// Determine work directory from config
	workDir := getWorkDirFromConfig(&cfg)

	// Create architect with shared LLM factory
	architect, err := architect.NewArchitect(
		ctx,
		agentID,
		f.dispatcher,
		workDir,
		f.persistenceChannel,
		f.llmFactory, // Shared factory for rate limiting
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create architect: %w", err)
	}

	// Attach to dispatcher
	f.dispatcher.Attach(architect)
	return architect, nil
}

// createCoder creates a new coder agent.
func (f *AgentFactory) createCoder(ctx context.Context, agentID string) (dispatch.Agent, error) {
	// Get current config
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	// Determine work directory from config
	baseWorkDir := getWorkDirFromConfig(&cfg)
	coderWorkDir := filepath.Join(baseWorkDir, agentID)

	// Extract repository info from config
	repoURL := ""
	targetBranch := "main"
	if cfg.Git != nil {
		repoURL = cfg.Git.RepoURL
		if cfg.Git.TargetBranch != "" {
			targetBranch = cfg.Git.TargetBranch
		}
	}

	// Create clone manager
	gitRunner := coder.NewDefaultGitRunner()
	cloneManager := coder.NewCloneManager(
		gitRunner,
		baseWorkDir, // Clone manager uses base work dir for mirrors
		repoURL,
		targetBranch,
		".mirrors",
		fmt.Sprintf("maestro-story-%s", agentID),
	)

	// Create build service as needed
	buildService := build.NewBuildService()

	// Create coder with shared LLM factory
	coderAgent, err := coder.NewCoder(
		ctx,
		agentID,
		coderWorkDir, // Individual work directory
		cloneManager,
		buildService,
		f.chatService,        // Chat service for agent collaboration
		f.persistenceChannel, // Channel for database operations
		f.llmFactory,         // Shared factory for rate limiting
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create coder %s: %w", agentID, err)
	}

	// Attach to dispatcher
	f.dispatcher.Attach(coderAgent)
	return coderAgent, nil
}

// createPM creates a new PM agent.
func (f *AgentFactory) createPM(ctx context.Context, agentID string) (dispatch.Agent, error) {
	// Get current config
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	// Check if PM is enabled
	if cfg.PM != nil && !cfg.PM.Enabled {
		return nil, fmt.Errorf("PM agent is disabled in configuration")
	}

	// Determine work directory from config
	workDir := getWorkDirFromConfig(&cfg)

	// Create interview request channel
	// TODO: This should be wired to WebUI for receiving interview requests
	// For now, create a buffered channel that will be used when WebUI integration is complete
	interviewRequestCh := make(chan *proto.AgentMsg, 10)

	// Create PM with shared LLM factory
	pmAgent, err := pm.NewPM(
		ctx,
		agentID,
		f.dispatcher,
		workDir,
		f.persistenceChannel,
		f.llmFactory,       // Shared factory for rate limiting
		interviewRequestCh, // Interview requests from WebUI
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create PM: %w", err)
	}

	// Attach to dispatcher
	f.dispatcher.Attach(pmAgent)
	return pmAgent, nil
}

// getWorkDirFromConfig determines the work directory from configuration.
func getWorkDirFromConfig(_ *config.Config) string {
	// Get the project directory from configuration
	// This is where agent working directories should be created
	projectDir, err := config.GetProjectDir()
	if err != nil {
		// Fallback to current directory if GetProjectDir fails
		// This should never happen in normal operation
		return "."
	}
	return projectDir
}
