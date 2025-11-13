package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/term"

	"orchestrator/internal/kernel"
	"orchestrator/internal/orch"
	"orchestrator/internal/supervisor"
	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/proto"
)

// FlowRunner interface defines the common behavior for orchestrator flows.
type FlowRunner interface {
	Run(ctx context.Context, k *kernel.Kernel) error
}

// BootstrapFlow handles single-spec execution with termination.
// This consolidates the bootstrap logic from bootstrap.go.
type BootstrapFlow struct {
	gitRepo  string
	specFile string
}

// NewBootstrapFlow creates a new bootstrap flow.
func NewBootstrapFlow(gitRepo, specFile string) *BootstrapFlow {
	return &BootstrapFlow{
		gitRepo:  gitRepo,
		specFile: specFile,
	}
}

// Run executes the bootstrap flow.
func (f *BootstrapFlow) Run(ctx context.Context, k *kernel.Kernel) error {
	k.Logger.Info("Starting bootstrap flow")

	// Load and inject spec content FIRST to complete interactive setup
	k.Logger.Info("📝 Starting interactive bootstrap setup...")
	specContent, err := f.loadSpecContent(ctx)
	if err != nil {
		return fmt.Errorf("failed to load spec content: %w", err)
	}

	// Get updated configuration after interactive setup
	updatedConfig, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get updated config: %w", err)
	}

	// Create supervisor for agent lifecycle management (creates its own factory)
	supervisor := supervisor.NewSupervisor(k)

	// Start supervisor's state change processor
	supervisor.Start(ctx)

	// Create and register architect agent
	architect, err := supervisor.GetFactory().NewAgent(ctx, "architect-001", string(agent.TypeArchitect))
	if err != nil {
		return fmt.Errorf("failed to create architect: %w", err)
	}
	supervisor.RegisterAgent(ctx, "architect-001", string(agent.TypeArchitect), architect)

	// Create and register PM agent (if enabled)
	if updatedConfig.PM != nil && updatedConfig.PM.Enabled {
		pmAgent, pmErr := supervisor.GetFactory().NewAgent(ctx, "pm-001", string(agent.TypePM))
		if pmErr != nil {
			return fmt.Errorf("failed to create PM: %w", pmErr)
		}
		supervisor.RegisterAgent(ctx, "pm-001", string(agent.TypePM), pmAgent)
		k.Logger.Info("✅ Created and registered PM agent")
	}

	// Create and register coder agents based on config
	numCoders := updatedConfig.Agents.MaxCoders
	for i := 0; i < numCoders; i++ {
		coderID := fmt.Sprintf("coder-%03d", i+1)
		coderAgent, coderErr := supervisor.GetFactory().NewAgent(ctx, coderID, string(agent.TypeCoder))
		if coderErr != nil {
			return fmt.Errorf("failed to create coder %s: %w", coderID, coderErr)
		}
		supervisor.RegisterAgent(ctx, coderID, string(agent.TypeCoder), coderAgent)
	}

	k.Logger.Info("✅ Created and registered architect and %d coders", numCoders)

	// Inject spec into architect
	if specErr := InjectSpec(k.Dispatcher, "bootstrap", specContent); specErr != nil {
		return fmt.Errorf("failed to inject spec content: %w", specErr)
	}

	k.Logger.Info("📝 Injected bootstrap spec into architect")

	// Wait for architect completion - use interface
	finalState, err := f.waitForArchitectCompletion(ctx, architect)
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	k.Logger.Info("🎉 Bootstrap completed with architect in %s state", finalState)
	return nil
}

// loadSpecContent loads the specification content for bootstrap.
func (f *BootstrapFlow) loadSpecContent(ctx context.Context) ([]byte, error) {
	if f.specFile != "" {
		// Load from file
		content, err := os.ReadFile(f.specFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read spec file: %w", err)
		}
		return content, nil
	}

	// Run interactive bootstrap setup (implemented in interactive_bootstrap.go)
	return f.runInteractiveBootstrapSetup(ctx)
}

// StateProvider interface for agents that provide state information.
type StateProvider interface {
	GetCurrentState() proto.State
}

// waitForArchitectCompletion waits for the architect to reach a terminal state.
// This preserves the existing waitForArchitectCompletion logic.
func (f *BootstrapFlow) waitForArchitectCompletion(ctx context.Context, architect dispatch.Agent) (proto.State, error) {
	// Cast to StateProvider to get state information
	stateProvider, ok := architect.(StateProvider)
	if !ok {
		return proto.StateError, fmt.Errorf("architect does not implement StateProvider interface")
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return proto.StateError, fmt.Errorf("context cancelled while waiting for architect")

		case <-ticker.C:
			currentState := stateProvider.GetCurrentState()

			// Check for terminal states
			if currentState == proto.StateDone {
				return proto.StateDone, nil
			}

			if currentState == proto.StateError {
				return proto.StateError, fmt.Errorf("architect reached ERROR state")
			}

			// Continue waiting for non-terminal states
		}
	}
}

// OrchestratorFlow handles long-running multi-spec processing.
// This consolidates the main orchestrator logic from main.go.
type OrchestratorFlow struct {
	specFile string
	webUI    bool
}

// NewMainFlow creates a new main flow.
func NewMainFlow(specFile string, webUI bool) *OrchestratorFlow {
	return &OrchestratorFlow{
		specFile: specFile,
		webUI:    webUI,
	}
}

// Run executes the main flow.
func (f *OrchestratorFlow) Run(ctx context.Context, k *kernel.Kernel) error {
	k.Logger.Info("Starting main flow")

	// Start web UI if requested
	if f.webUI {
		// Generate password if not set (before starting WebUI)
		ensureWebUIPassword()

		if err := k.StartWebUI(); err != nil {
			return fmt.Errorf("failed to start web UI: %w", err)
		}
		k.Logger.Info("🌐 Web UI started successfully")

		// Display WebUI URL for user access
		protocol := "http"
		if k.Config.WebUI.SSL {
			protocol = "https"
		}
		fmt.Printf("🌐 WebUI: %s://%s:%d\n", protocol, k.Config.WebUI.Host, k.Config.WebUI.Port)
	}

	// Create supervisor for agent lifecycle management (creates its own factory)
	supervisor := supervisor.NewSupervisor(k)

	// Start supervisor's state change processor
	supervisor.Start(ctx)

	// Create and register architect agent
	architect, err := supervisor.GetFactory().NewAgent(ctx, "architect-001", string(agent.TypeArchitect))
	if err != nil {
		return fmt.Errorf("failed to create architect: %w", err)
	}
	supervisor.RegisterAgent(ctx, "architect-001", string(agent.TypeArchitect), architect)

	// Create and register PM agent (if enabled)
	if k.Config.PM != nil && k.Config.PM.Enabled {
		pmAgent, pmErr := supervisor.GetFactory().NewAgent(ctx, "pm-001", string(agent.TypePM))
		if pmErr != nil {
			return fmt.Errorf("failed to create PM: %w", pmErr)
		}
		supervisor.RegisterAgent(ctx, "pm-001", string(agent.TypePM), pmAgent)
		k.Logger.Info("✅ Created and registered PM agent")
	}

	// Create and register coder agents based on config
	numCoders := k.Config.Agents.MaxCoders
	for i := 0; i < numCoders; i++ {
		coderID := fmt.Sprintf("coder-%03d", i+1)
		coderAgent, coderErr := supervisor.GetFactory().NewAgent(ctx, coderID, string(agent.TypeCoder))
		if coderErr != nil {
			return fmt.Errorf("failed to create coder %s: %w", coderID, coderErr)
		}
		supervisor.RegisterAgent(ctx, coderID, string(agent.TypeCoder), coderAgent)
	}

	k.Logger.Info("✅ Created and registered architect and %d coders", numCoders)

	// Run startup orchestration (Story 3: rebuild + reconcile/rollback)
	if err := f.runStartupOrchestration(ctx, k); err != nil {
		return fmt.Errorf("startup orchestration failed: %w", err)
	}

	// Handle initial spec if provided
	if f.specFile != "" {
		specContent, err := os.ReadFile(f.specFile)
		if err != nil {
			return fmt.Errorf("failed to read spec file: %w", err)
		}

		if err := InjectSpec(k.Dispatcher, "cli", specContent); err != nil {
			return fmt.Errorf("failed to inject initial spec: %w", err)
		}

		k.Logger.Info("📝 Injected initial spec from file: %s", f.specFile)
	}

	// Enter main event loop
	k.Logger.Info("🚀 Main orchestrator running - ready for specs")

	// Wait for context cancellation (Ctrl+C, etc.)
	<-ctx.Done()
	k.Logger.Info("📴 Main flow shutting down due to context cancellation")

	return nil
}

// TODO: Temporarily disabled startup orchestration to debug crash
// runStartupOrchestration executes the startup rebuild + reconcile/rollback from Story 3.
func (f *OrchestratorFlow) runStartupOrchestration(ctx context.Context, k *kernel.Kernel) error {
	k.Logger.Info("🔧 Starting startup orchestration")

	// Get project directory from kernel
	projectDir := k.ProjectDir()

	// Create startup orchestrator (false = not bootstrap mode, this only runs in main mode)
	startupOrch, err := orch.NewStartupOrchestrator(projectDir, false)
	if err != nil {
		return fmt.Errorf("failed to create startup orchestrator: %w", err)
	}

	// Execute startup sequence
	if err := startupOrch.OnStart(ctx); err != nil {
		return fmt.Errorf("startup orchestration failed: %w", err)
	}

	k.Logger.Info("✅ Startup orchestration completed successfully")

	// User-friendly message when system is ready
	fmt.Println("✅ Startup complete.")

	return nil
}

// InjectSpec provides centralized spec injection into the dispatcher.
// Sends spec as REQUEST message (same protocol as PM) so architect handles it in REQUEST state.
func InjectSpec(dispatcher *dispatch.Dispatcher, source string, content []byte) error {
	// Create REQUEST message with spec approval (unified with PM flow)
	msg := proto.NewAgentMsg(proto.MsgTypeREQUEST, source, string(agent.TypeArchitect))

	// Build approval request payload
	approvalPayload := &proto.ApprovalRequestPayload{
		ApprovalType: proto.ApprovalTypeSpec,
		Content:      string(content), // Spec markdown goes in Content field
		Reason:       fmt.Sprintf("Spec submitted via %s", source),
		Metadata:     make(map[string]string),
	}
	approvalPayload.Metadata["source"] = source

	msg.SetTypedPayload(proto.NewApprovalRequestPayload(approvalPayload))
	msg.SetMetadata("approval_id", proto.GenerateApprovalID())
	msg.SetMetadata("source", source)

	// Send via dispatcher
	if err := dispatcher.DispatchMessage(msg); err != nil {
		return fmt.Errorf("failed to dispatch spec request: %w", err)
	}
	return nil
}

// ensureWebUIPassword checks if a WebUI password is available, and generates one if not.
// With unified password: uses project password from secrets or MAESTRO_PASSWORD env var.
// Logs status messages, but displays generated passwords directly to stdout (not logs).
func ensureWebUIPassword() {
	logger := config.LogInfo

	// Check if password is already set (from secrets file or MAESTRO_PASSWORD)
	if config.GetWebUIPassword() != "" {
		// Password already set - check which source
		if config.GetProjectPassword() != "" {
			logger("🔐 WebUI password loaded from project secrets")
		} else {
			logger("🔐 WebUI password loaded from MAESTRO_PASSWORD environment variable")
		}

		// Check SSL status and warn if disabled
		cfg, err := config.GetConfig()
		if err == nil && cfg.WebUI != nil && !cfg.WebUI.SSL {
			logger("⚠️  WARNING: SSL is disabled! Password will be transmitted in plain text.")
			logger("💡 Enable SSL in config.json or use SSH port forwarding for secure access.")
		}
		return
	}

	// Generate a secure random password for this session
	password, err := generateSecurePassword(16)
	if err != nil {
		fmt.Printf("⚠️  Failed to generate WebUI password: %v\n", err)
		fmt.Println("⚠️  Please set MAESTRO_PASSWORD environment variable manually")
		return
	}

	// Store in memory (simulating what would happen with MAESTRO_PASSWORD env var)
	config.SetProjectPassword(password)

	// Check SSL status for warning
	cfg, cfgErr := config.GetConfig()
	sslEnabled := cfgErr == nil && cfg.WebUI != nil && cfg.WebUI.SSL

	// Display the generated password to the user (NOT via logger!)
	fmt.Println("╔════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                   🔐 WebUI Password Generated                      ║")
	fmt.Println("╠════════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Username: maestro                                                 ║\n")
	fmt.Printf("║  Password: %-52s ║\n", password)
	fmt.Println("╠════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  ⚠️  Save this password! It will not be shown again.               ║")
	fmt.Println("║  💡 Set MAESTRO_PASSWORD env var to use a custom password.         ║")
	if !sslEnabled {
		fmt.Println("╠════════════════════════════════════════════════════════════════════╣")
		fmt.Println("║  🔓 WARNING: SSL is disabled! Password sent in plain text.        ║")
		fmt.Println("║  💡 Enable SSL in config.json or use SSH port forwarding.         ║")
	}
	fmt.Println("╚════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}

// generateSecurePassword generates a cryptographically secure random password.
// The password uses base64 encoding for readability while maintaining high entropy.
func generateSecurePassword(length int) (string, error) {
	// Generate random bytes (we need more bytes than length to account for base64 encoding)
	// base64 encoding produces 4 characters for every 3 bytes
	numBytes := (length * 3) / 4
	if numBytes < length {
		numBytes = length
	}

	randomBytes := make([]byte, numBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Encode to base64 URL-safe format (no special chars that need escaping)
	password := base64.URLEncoding.EncodeToString(randomBytes)

	// Trim to desired length
	if len(password) > length {
		password = password[:length]
	}

	return password, nil
}

// handleSecretsDecryption checks for secrets file and decrypts it if present.
// Returns error only on fatal issues. Gracefully handles missing file or wrong password.
//
//nolint:cyclop // Complex logic for user interaction flow - extracting helpers would reduce readability
func handleSecretsDecryption(projectDir string) error {
	// Check if secrets file exists
	if !config.SecretsFileExists(projectDir) {
		// No secrets file - will use environment variables
		return nil
	}

	config.LogInfo("📋 Loading project from %s", projectDir)

	// Try to get password from MAESTRO_PASSWORD env var first
	password := os.Getenv("MAESTRO_PASSWORD")

	maxAttempts := 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// If no env var, prompt user
		if password == "" {
			var err error
			password, err = promptForSecretsPassword()
			if err != nil {
				return fmt.Errorf("failed to read password: %w", err)
			}
		}

		// Try to decrypt
		secrets, err := config.DecryptSecretsFile(projectDir, password)
		if err != nil {
			if attempt < maxAttempts {
				// Wrong password - offer retry or delete
				if promptRetryOrDelete() {
					return deleteSecretsFile(projectDir)
				}

				// Retry - clear password for next prompt
				password = ""
				continue
			}

			// Max attempts reached - delete file
			fmt.Println("⚠️  Maximum password attempts reached.")
			return deleteSecretsFile(projectDir)
		}

		// Success! Store secrets and password in memory
		config.SetDecryptedSecrets(secrets)
		config.SetProjectPassword(password)
		config.LogInfo("✅ Credentials decrypted successfully")

		// Display WebUI info if WebUI is enabled
		displayWebUIInfo()

		return nil
	}

	return nil
}

// promptForSecretsPassword prompts user for secrets password.
func promptForSecretsPassword() (string, error) {
	fmt.Print("Enter Maestro password: ")
	passwordBytes, err := term.ReadPassword(syscall.Stdin)
	fmt.Println() // New line after password input
	if err != nil {
		return "", fmt.Errorf("failed to read password: %w", err)
	}

	password := string(passwordBytes)

	// Clear password bytes from memory
	for i := range passwordBytes {
		passwordBytes[i] = 0
	}

	return password, nil
}

// promptRetryOrDelete asks user if they want to retry or delete secrets file.
func promptRetryOrDelete() (shouldDelete bool) {
	fmt.Println("⚠️  Unable to decrypt secrets file with specified password.")
	fmt.Println()
	fmt.Print("Do you want to (R)etry or (D)elete the secrets file and restart? [R/d]: ")

	var choice string
	if _, err := fmt.Scanln(&choice); err != nil {
		// Treat scan error as "retry" (safer default)
		return false
	}

	return choice == "d" || choice == "D"
}

// deleteSecretsFile removes the secrets file and returns nil.
func deleteSecretsFile(projectDir string) error {
	secretsPath := projectDir + "/.maestro/secrets.json.enc"
	if err := os.Remove(secretsPath); err != nil {
		return fmt.Errorf("failed to delete secrets file: %w", err)
	}
	fmt.Println("⚠️  Deleting .maestro/secrets.json.enc...")
	fmt.Println("✅ Secrets file removed. Attempting to continue with environment variables...")
	return nil
}

// displayWebUIInfo displays WebUI access information if enabled.
func displayWebUIInfo() {
	cfg, err := config.GetConfig()
	if err == nil && cfg.WebUI != nil && cfg.WebUI.Enabled {
		protocol := "http"
		if cfg.WebUI.SSL {
			protocol = "https"
		}
		fmt.Printf("🌐 WebUI: %s://%s:%d (username: maestro, use same password)\n",
			protocol, cfg.WebUI.Host, cfg.WebUI.Port)
	}
}
