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
)

// AgentFactory creates agents with minimal dependencies.
// All configuration is sourced from config.GetConfig() inside agent constructors.
type AgentFactory struct {
	dispatcher         *dispatch.Dispatcher
	persistenceChannel chan<- *persistence.Request
	chatService        *chat.Service
}

// NewAgentFactory creates a new lightweight agent factory.
func NewAgentFactory(dispatcher *dispatch.Dispatcher, persistenceChannel chan<- *persistence.Request, chatService *chat.Service) *AgentFactory {
	return &AgentFactory{
		dispatcher:         dispatcher,
		persistenceChannel: persistenceChannel,
		chatService:        chatService,
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

	// Get architect model
	architectModel, err := getModelByName(&cfg, cfg.Agents.ArchitectModel)
	if err != nil {
		return nil, fmt.Errorf("failed to get architect model: %w", err)
	}

	// Determine work directory from config
	workDir := getWorkDirFromConfig(&cfg)

	// Create architect
	architect, err := architect.NewArchitect(
		ctx,
		agentID,
		architectModel,
		f.dispatcher,
		workDir,
		f.persistenceChannel,
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

	// Get coder model
	coderModel, err := getModelByName(&cfg, cfg.Agents.CoderModel)
	if err != nil {
		return nil, fmt.Errorf("failed to get coder model: %w", err)
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

	// Create coder
	coderAgent, err := coder.NewCoder(
		ctx,
		agentID,
		coderWorkDir, // Individual work directory
		coderModel,
		cloneManager,
		buildService,
		f.chatService, // Chat service for agent collaboration
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create coder %s: %w", agentID, err)
	}

	// Attach to dispatcher
	f.dispatcher.Attach(coderAgent)
	return coderAgent, nil
}

// getWorkDirFromConfig determines the work directory from configuration.
func getWorkDirFromConfig(_ *config.Config) string {
	// TODO: Add WorkDir field to config structure if not present
	// Return absolute current directory to ensure proper host path resolution
	if absDir, err := filepath.Abs("."); err == nil {
		return absDir
	}
	// Fallback to current directory if Abs() fails
	return "."
}

// getModelByName finds a model configuration by name.
func getModelByName(cfg *config.Config, modelName string) (*config.Model, error) {
	for i := range cfg.Orchestrator.Models {
		model := &cfg.Orchestrator.Models[i]
		if model.Name == modelName {
			return model, nil
		}
	}
	return nil, fmt.Errorf("model %s not found in configuration", modelName)
}
