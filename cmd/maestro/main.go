package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"orchestrator/internal/kernel"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

func main() {
	// Parse command line flags
	var (
		gitRepo    = flag.String("git-repo", "", "Git repository URL for bootstrap mode")
		specFile   = flag.String("spec-file", "", "Path to specification file")
		webUI      = flag.Bool("webui", false, "Enable web UI for main mode")
		bootstrap  = flag.Bool("bootstrap", false, "Run in bootstrap mode")
		projectDir = flag.String("projectdir", ".", "Project directory")
	)
	flag.Parse()

	// Determine mode
	if *bootstrap {
		if err := runBootstrapMode(*projectDir, *gitRepo, *specFile); err != nil {
			fmt.Fprintf(os.Stderr, "Bootstrap failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := runMainMode(*projectDir, *specFile, *webUI); err != nil {
			fmt.Fprintf(os.Stderr, "Main mode failed: %v\n", err)
			os.Exit(1)
		}
	}
}

func runBootstrapMode(projectDir, gitRepo, specFile string) error {
	logger := logx.NewLogger("maestro-bootstrap")
	logger.Info("Starting Maestro in bootstrap mode")

	// Initialize common kernel infrastructure
	k, ctx, err := initializeKernel(projectDir)
	if err != nil {
		return fmt.Errorf("failed to initialize kernel: %w", err)
	}
	defer func() {
		if stopErr := k.Stop(); stopErr != nil {
			logger.Error("Error stopping kernel: %v", stopErr)
		}
	}()

	// Create and run bootstrap flow
	flow := NewBootstrapFlow(gitRepo, specFile)
	return flow.Run(ctx, k)
}

func runMainMode(projectDir, specFile string, webUI bool) error {
	logger := logx.NewLogger("maestro-main")
	logger.Info("Starting Maestro in main mode")

	// Initialize common kernel infrastructure
	k, ctx, err := initializeKernel(projectDir)
	if err != nil {
		return fmt.Errorf("failed to initialize kernel: %w", err)
	}
	defer func() {
		if stopErr := k.Stop(); stopErr != nil {
			logger.Error("Error stopping kernel: %v", stopErr)
		}
	}()

	// Create and run main flow
	flow := NewMainFlow(specFile, webUI)
	return flow.Run(ctx, k)
}

// initializeKernel consolidates the common kernel initialization logic.
func initializeKernel(projectDir string) (*kernel.Kernel, context.Context, error) {
	// Load configuration
	if err := config.LoadConfig(projectDir); err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}

	cfg, err := config.GetConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get config: %w", err)
	}

	// Create context with signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	_ = cancel // Will be called when context is cancelled

	// Initialize kernel with shared infrastructure
	k, err := kernel.NewKernel(ctx, &cfg, projectDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create kernel: %w", err)
	}

	// Start kernel services
	if err := k.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start kernel: %w", err)
	}

	return k, ctx, nil
}
