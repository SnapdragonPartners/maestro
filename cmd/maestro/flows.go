package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"orchestrator/internal/kernel"
	"orchestrator/internal/orch"
	"orchestrator/internal/supervisor"
	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/preflight"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/telemetry"
	"orchestrator/pkg/utils"
)

// URL protocol constants.
const (
	protocolHTTP  = "http"
	protocolHTTPS = "https"
)

// FlowRunner interface defines the common behavior for orchestrator flows.
type FlowRunner interface {
	Run(ctx context.Context, k *kernel.Kernel) error
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
//
//nolint:cyclop // Flow orchestrates agent creation and registration - complexity is acceptable for a flow function
func (f *OrchestratorFlow) Run(ctx context.Context, k *kernel.Kernel) error {
	k.Logger.Info("Starting main flow")

	// Retry unsent telemetry from previous sessions in the background
	// so startup isn't blocked by network latency.
	go func() { //nolint:contextcheck // intentionally detached from parent context
		retryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		retryUnsentTelemetry(retryCtx, k)
	}()

	// Start web UI if requested
	if f.webUI {
		if err := k.StartWebUI(); err != nil {
			return fmt.Errorf("failed to start web UI: %w", err)
		}
		k.Logger.Info("🌐 Web UI started successfully")

		// Display WebUI URL for user access
		protocol := protocolHTTP
		if k.Config.WebUI.SSL {
			protocol = protocolHTTPS
		}
		fmt.Printf("🌐 WebUI: %s://%s:%d\n", protocol, k.Config.WebUI.Host, k.Config.WebUI.Port)
	}

	// Wait for API keys to be configured (if WebUI is active, shows setup page)
	if f.webUI {
		if err := k.WebServer.WaitForSetup(ctx, k.Config); err != nil {
			return fmt.Errorf("setup cancelled: %w", err)
		}
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

		// Wire PM as the demo availability checker for WebUI
		// PM is the sole authority on demo availability (based on bootstrap status)
		if checker, ok := pmAgent.(interface {
			IsDemoAvailable() bool
			EnsureBootstrapChecked(ctx context.Context) error
		}); ok {
			k.WebServer.SetDemoAvailabilityChecker(checker)
			k.Logger.Info("✅ PM wired as demo availability checker")
		}
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

	// Create and register dedicated hotfix coder
	hotfixCoder, hotfixErr := supervisor.GetFactory().NewAgent(ctx, "hotfix-001", string(agent.TypeCoder))
	if hotfixErr != nil {
		return fmt.Errorf("failed to create hotfix coder: %w", hotfixErr)
	}
	supervisor.RegisterAgent(ctx, "hotfix-001", string(agent.TypeCoder), hotfixCoder)

	k.Logger.Info("✅ Created and registered architect, %d coders, and hotfix-001", numCoders)

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

	// === GRACEFUL SHUTDOWN FLOW ===
	performGracefulShutdown(k, supervisor)

	return nil
}

// ResumeFlow handles resuming from a previous shutdown session.
// It restores agent state from the database and continues execution.
type ResumeFlow struct {
	sessionID    string
	webUI        bool
	restoreState bool // true for shutdown sessions (full restore), false for crashed (checkpoint only)
}

// NewResumeFlow creates a new resume flow. restoreState controls whether coder state is restored:
// - true (shutdown): Full restoration including coders
// - false (crashed): Only architect/PM restored from checkpoint, coders start fresh.
func NewResumeFlow(sessionID string, webUI, restoreState bool) *ResumeFlow {
	return &ResumeFlow{
		sessionID:    sessionID,
		webUI:        webUI,
		restoreState: restoreState,
	}
}

// Run executes the resume flow.
//
//nolint:cyclop // This function orchestrates agent creation/restoration - complexity is acceptable for a flow function
func (f *ResumeFlow) Run(ctx context.Context, k *kernel.Kernel) error {
	k.Logger.Info("Starting resume flow for session %s", f.sessionID)

	// Retry unsent telemetry from previous sessions in the background
	// so startup isn't blocked by network latency.
	go func() { //nolint:contextcheck // intentionally detached from parent context
		retryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		retryUnsentTelemetry(retryCtx, k)
	}()

	// Start web UI if requested
	if f.webUI {
		if err := k.StartWebUI(); err != nil {
			return fmt.Errorf("failed to start web UI: %w", err)
		}
		k.Logger.Info("🌐 Web UI started successfully")

		// Display WebUI URL for user access
		protocol := protocolHTTP
		if k.Config.WebUI.SSL {
			protocol = protocolHTTPS
		}
		fmt.Printf("🌐 WebUI: %s://%s:%d\n", protocol, k.Config.WebUI.Host, k.Config.WebUI.Port)
	}

	// Wait for API keys to be configured (if WebUI is active, shows setup page)
	if f.webUI {
		if err := k.WebServer.WaitForSetup(ctx, k.Config); err != nil {
			return fmt.Errorf("setup cancelled: %w", err)
		}
	}

	// Create supervisor for agent lifecycle management (creates its own factory)
	supervisor := supervisor.NewSupervisor(k)

	// Start supervisor's state change processor
	supervisor.Start(ctx)

	// Create and register architect agent, then restore state
	architect, err := supervisor.GetFactory().NewAgent(ctx, "architect-001", string(agent.TypeArchitect))
	if err != nil {
		return fmt.Errorf("failed to create architect: %w", err)
	}
	// Restore architect state BEFORE registering (which starts the Run loop)
	if restorer, ok := architect.(StateRestorer); ok {
		if restoreErr := restorer.RestoreState(ctx, k.Database, f.sessionID); restoreErr != nil {
			k.Logger.Warn("⚠️ Failed to restore architect state: %v", restoreErr)
		} else {
			k.Logger.Info("✅ Restored architect state from session")
		}
	}
	supervisor.RegisterAgent(ctx, "architect-001", string(agent.TypeArchitect), architect)

	// Create and register PM agent (if enabled), then restore state
	if k.Config.PM != nil && k.Config.PM.Enabled {
		pmAgent, pmErr := supervisor.GetFactory().NewAgent(ctx, "pm-001", string(agent.TypePM))
		if pmErr != nil {
			return fmt.Errorf("failed to create PM: %w", pmErr)
		}
		// Restore PM state
		if restorer, ok := pmAgent.(StateRestorer); ok {
			if restoreErr := restorer.RestoreState(ctx, k.Database, f.sessionID); restoreErr != nil {
				k.Logger.Warn("⚠️ Failed to restore PM state: %v", restoreErr)
			} else {
				k.Logger.Info("✅ Restored PM state from session")
			}
		}
		supervisor.RegisterAgent(ctx, "pm-001", string(agent.TypePM), pmAgent)

		// Wire PM as the demo availability checker for WebUI
		// PM is the sole authority on demo availability (based on bootstrap status)
		if checker, ok := pmAgent.(interface {
			IsDemoAvailable() bool
			EnsureBootstrapChecked(ctx context.Context) error
		}); ok {
			k.WebServer.SetDemoAvailabilityChecker(checker)
			k.Logger.Info("✅ PM wired as demo availability checker")
		}
		k.Logger.Info("✅ Created and registered PM agent with restored state")
	}

	// Create and register coder agents based on config
	// Only restore state for shutdown sessions (f.restoreState=true)
	// For crashed sessions, coders start fresh (stories already reset to 'new')
	numCoders := k.Config.Agents.MaxCoders
	for i := 0; i < numCoders; i++ {
		coderID := fmt.Sprintf("coder-%03d", i+1)
		coderAgent, coderErr := supervisor.GetFactory().NewAgent(ctx, coderID, string(agent.TypeCoder))
		if coderErr != nil {
			return fmt.Errorf("failed to create coder %s: %w", coderID, coderErr)
		}
		// Only restore coder state for shutdown sessions
		if f.restoreState {
			if restorer, ok := coderAgent.(StateRestorer); ok {
				if restoreErr := restorer.RestoreState(ctx, k.Database, f.sessionID); restoreErr != nil {
					k.Logger.Warn("⚠️ Failed to restore state for %s: %v", coderID, restoreErr)
				} else {
					k.Logger.Info("✅ Restored state for %s from session", coderID)
				}
			}
		}
		supervisor.RegisterAgent(ctx, coderID, string(agent.TypeCoder), coderAgent)
	}

	// Create and register dedicated hotfix coder
	hotfixCoder, hotfixErr := supervisor.GetFactory().NewAgent(ctx, "hotfix-001", string(agent.TypeCoder))
	if hotfixErr != nil {
		return fmt.Errorf("failed to create hotfix coder: %w", hotfixErr)
	}
	// Only restore hotfix coder state for shutdown sessions
	if f.restoreState {
		if restorer, ok := hotfixCoder.(StateRestorer); ok {
			if restoreErr := restorer.RestoreState(ctx, k.Database, f.sessionID); restoreErr != nil {
				k.Logger.Warn("⚠️ Failed to restore state for hotfix-001: %v", restoreErr)
			} else {
				k.Logger.Info("✅ Restored state for hotfix-001 from session")
			}
		}
	}
	supervisor.RegisterAgent(ctx, "hotfix-001", string(agent.TypeCoder), hotfixCoder)

	if f.restoreState {
		k.Logger.Info("✅ Created and registered architect, %d coders, and hotfix-001 with restored state", numCoders)
	} else {
		k.Logger.Info("✅ Created and registered architect, %d coders, and hotfix-001 (coders starting fresh after crash)", numCoders)
	}

	// Enter main event loop
	k.Logger.Info("🚀 Resumed orchestrator running - agents continuing from saved state")

	// Wait for context cancellation (Ctrl+C, etc.)
	<-ctx.Done()
	k.Logger.Info("📴 Resume flow shutting down due to context cancellation")

	// === GRACEFUL SHUTDOWN FLOW ===
	performGracefulShutdown(k, supervisor)

	return nil
}

// StateRestorer interface for agents that can restore their state from a previous session.
type StateRestorer interface {
	RestoreState(ctx context.Context, db *sql.DB, sessionID string) error
}

// StateSerializer interface for agents that can serialize their state for resume.
type StateSerializer interface {
	SerializeState(ctx context.Context, db *sql.DB, sessionID string) error
}

// performGracefulShutdown executes the graceful shutdown sequence for resumable sessions.
// This is called when the context is cancelled (Ctrl+C) to save agent state for later resumption.
//
//nolint:contextcheck // We intentionally use context.Background() here because the main context is already cancelled
func performGracefulShutdown(k *kernel.Kernel, sup *supervisor.Supervisor) {
	shutdownTimeout := 30 * time.Second

	// 1. Wait for all agents to complete their current work
	if err := sup.WaitForAgentsShutdown(shutdownTimeout); err != nil {
		k.Logger.Warn("⚠️ Some agents did not complete gracefully: %v", err)
	}

	// 2. Serialize state for all agents that support it.
	// SerializeState sends to the persistence queue, so FIFO ordering ensures
	// this is processed after any pending checkpoints.
	serializeCtx := context.Background()
	sessionID := k.Config.SessionID
	agents, _ := sup.GetAgents()
	for agentID, agentInstance := range agents {
		if serializer, ok := utils.SafeAssert[StateSerializer](agentInstance); ok {
			k.Logger.Info("💾 Serializing state for %s", agentID)
			if err := serializer.SerializeState(serializeCtx, k.Database, sessionID); err != nil {
				k.Logger.Warn("⚠️ Failed to serialize state for %s: %v", agentID, err)
			}
		}
	}

	// 3. Drain the persistence queue to process all pending requests including serialization.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	if err := k.DrainPersistenceQueue(drainCtx); err != nil {
		k.Logger.Warn("⚠️ Persistence queue drain incomplete: %v", err)
	}
	drainCancel()

	// 4. Update session status to 'shutdown' (makes it resumable)
	if err := persistence.UpdateSessionStatus(k.Database, k.Config.SessionID, persistence.SessionStatusShutdown); err != nil {
		k.Logger.Error("❌ Failed to update session status: %v", err)
	} else {
		k.Logger.Info("✅ Session marked as 'shutdown' - resumable")
	}

	// 4.5 Send failure telemetry if opted in (after session status is final)
	sendFailureTelemetry(context.Background(), k, k.Config.SessionID)

	// 5. Log resume instructions for the user
	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    📴 Graceful Shutdown Complete                   ║")
	fmt.Println("╠════════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Session ID: %-54s ║\n", k.Config.SessionID)
	fmt.Println("║                                                                    ║")
	fmt.Println("║  To resume this session, run:                                      ║")
	fmt.Println("║    maestro -continue                                               ║")
	fmt.Println("║                                                                    ║")
	fmt.Println("║  Agent states have been saved and can be restored.                 ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}

// sendFailureTelemetry sends failure telemetry for a session if opted in.
// Fire-and-forget: errors are logged but never block the caller.
func sendFailureTelemetry(ctx context.Context, k *kernel.Kernel, sessionID string) {
	if !k.Config.TelemetryEnabled {
		return
	}
	if k.Config.InstallationID == "" {
		return
	}

	records, err := persistence.QueryFailuresBySessionForDB(k.Database, sessionID)
	if err != nil {
		k.Logger.Warn("⚠️ Failed to query failures for telemetry: %v", err)
		return
	}
	if len(records) == 0 {
		// No failures to report, but mark the session so startup retry
		// doesn't keep reprocessing it.
		markTelemetrySent(k, sessionID)
		return
	}

	summary, err := persistence.QuerySessionSummary(k.Database, sessionID)
	if err != nil {
		k.Logger.Warn("⚠️ Failed to query session summary for telemetry: %v", err)
		return
	}

	report := telemetry.BuildReport(k.Config.InstallationID, sessionID, summary, records)

	k.Logger.Info("📊 Sending failure telemetry (%d failures) for session %s", len(report.Failures), sessionID)
	if err := telemetry.SendReport(ctx, report); err != nil {
		k.Logger.Warn("⚠️ Failed to send telemetry: %v", err)
		return
	}

	// Mark session as telemetry-sent via a marker file
	markTelemetrySent(k, sessionID)
	k.Logger.Info("✅ Telemetry sent successfully")
}

// retryUnsentTelemetry checks for terminal sessions that never sent telemetry
// and sends it now. Called on startup to capture crash-path failures.
func retryUnsentTelemetry(ctx context.Context, k *kernel.Kernel) {
	if !k.Config.TelemetryEnabled {
		return
	}

	// Query terminal sessions (shutdown/crashed) that might have unsent telemetry
	rows, err := k.Database.Query(`
		SELECT session_id FROM sessions
		WHERE status IN (?, ?, ?)
		ORDER BY started_at DESC
		LIMIT 5
	`, persistence.SessionStatusShutdown, persistence.SessionStatusCrashed, persistence.SessionStatusCompleted)
	if err != nil {
		k.Logger.Debug("Could not query sessions for telemetry retry: %v", err)
		return
	}
	defer func() { _ = rows.Close() }()

	var sessionIDs []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			k.Logger.Debug("Could not scan session for telemetry retry: %v", err)
			continue
		}
		sessionIDs = append(sessionIDs, sid)
	}
	if err := rows.Err(); err != nil {
		k.Logger.Debug("Could not iterate sessions for telemetry retry: %v", err)
		return
	}

	for _, sid := range sessionIDs {
		if sid == k.Config.SessionID {
			continue // Skip current session
		}
		if isTelemetrySent(k, sid) {
			continue
		}
		sendFailureTelemetry(ctx, k, sid)
	}
}

// markTelemetrySent creates a marker file indicating telemetry was sent for a session.
func markTelemetrySent(k *kernel.Kernel, sessionID string) {
	markerDir := fmt.Sprintf("%s/.maestro/telemetry-sent", k.ProjectDir())
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		k.Logger.Debug("Could not create telemetry marker directory: %v", err)
		return
	}
	markerPath := fmt.Sprintf("%s/%s", markerDir, sessionID)
	if err := os.WriteFile(markerPath, []byte(time.Now().UTC().Format(time.RFC3339)), 0o644); err != nil {
		k.Logger.Debug("Could not write telemetry marker for session %s: %v", sessionID, err)
	}
}

// isTelemetrySent checks if telemetry was already sent for a session.
func isTelemetrySent(k *kernel.Kernel, sessionID string) bool {
	markerPath := fmt.Sprintf("%s/.maestro/telemetry-sent/%s", k.ProjectDir(), sessionID)
	_, err := os.Stat(markerPath)
	return err == nil
}

// runStartupOrchestration executes the startup rebuild + reconcile/rollback from Story 3.
// In airplane mode, it also prepares local services (Gitea, Ollama).
func (f *OrchestratorFlow) runStartupOrchestration(ctx context.Context, k *kernel.Kernel) error {
	k.Logger.Info("🔧 Starting startup orchestration")

	// Get project directory from kernel
	projectDir := k.ProjectDir()

	// If airplane mode, prepare local services first
	if config.IsAirplaneMode() {
		k.Logger.Info("✈️  Airplane mode detected - preparing local services")
		airplaneOrch := orch.NewAirplaneOrchestrator(projectDir)
		if err := airplaneOrch.PrepareAirplaneMode(ctx); err != nil {
			return fmt.Errorf("airplane mode preparation failed: %w", err)
		}
	} else {
		// Standard mode: run preflight checks to validate credentials/tools
		k.Logger.Info("🔍 Running preflight checks for standard mode")
		cfg, err := config.GetConfig()
		if err != nil {
			return fmt.Errorf("failed to get config for preflight: %w", err)
		}
		results, err := preflight.Run(ctx, &cfg)
		if err != nil {
			return fmt.Errorf("preflight checks failed: %w", err)
		}
		if !results.Passed {
			// Print guidance for failed checks
			for i := range results.Checks {
				check := &results.Checks[i]
				if !check.Passed {
					k.Logger.Error("❌ %s: %s", check.Provider, check.Message)
					if guidance := preflight.GetGuidance(check.Provider); guidance != "" {
						k.Logger.Info("   💡 %s", guidance)
					}
				}
			}
			return fmt.Errorf("preflight checks failed - see errors above")
		}
		k.Logger.Info("✅ Preflight checks passed")
	}

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

// ensureWebUIPassword establishes the project password using the following precedence:
//  1. MAESTRO_PASSWORD env var (power users).
//  2. Password verifier file exists (password established in prior session, recovered via WebUI login).
//  3. Orphaned secrets file without verifier (legacy/migration, hard-fail requiring env var).
//  4. First run: generate password, create verifier, display banner.
func ensureWebUIPassword(projectDir string) {
	logger := config.LogInfo

	// 1. Password already available (MAESTRO_PASSWORD env var or previously decrypted secrets)
	if envPw := os.Getenv("MAESTRO_PASSWORD"); envPw != "" {
		config.SetProjectPassword(envPw)
		logger("🔐 WebUI password loaded from MAESTRO_PASSWORD environment variable")
	} else if config.GetProjectPassword() != "" {
		logger("🔐 WebUI password loaded from project secrets")
	}

	if config.GetWebUIPassword() != "" {
		// Ensure verifier matches the active password for future runs without env var
		if err := config.SavePasswordVerifier(projectDir, config.GetProjectPassword()); err != nil {
			logger("⚠️  Failed to save password verifier: %v", err)
		}

		// Check SSL status and warn if disabled
		cfg, err := config.GetConfig()
		if err == nil && cfg.WebUI != nil && !cfg.WebUI.SSL {
			logger("⚠️  WARNING: SSL is disabled! Password will be transmitted in plain text.")
			logger("💡 Enable SSL in config.json or use SSH port forwarding for secure access.")
		}
		return
	}

	// 2. Verifier file exists (password established in a prior session)
	if config.PasswordVerifierExists(projectDir) {
		// Do NOT generate a new password — it will be recovered via WebUI Basic Auth login
		fmt.Println()
		fmt.Println("🔐 Maestro password required.")
		fmt.Println("   Log in to the WebUI with your Maestro password to continue.")
		fmt.Println()
		fmt.Println("   Lost your password? Delete .maestro/.password-verifier.json")
		fmt.Println("   and .maestro/secrets.json.enc, then restart to generate a new one.")
		fmt.Println()
		return
	}

	// 3. Orphaned secrets file without verifier (legacy/migration case)
	if config.SecretsFileExists(projectDir) {
		fmt.Fprintf(os.Stderr, "❌ Secrets file exists at %s/.maestro/secrets.json.enc but no password verifier found.\n", projectDir)
		fmt.Fprintln(os.Stderr, "   Set the MAESTRO_PASSWORD environment variable to decrypt.")
		os.Exit(1)
	}

	// 4. First run: generate password, create verifier
	password, err := generateSecurePassword(16)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to generate password: %v\n", err)
		fmt.Fprintln(os.Stderr, "   Please set MAESTRO_PASSWORD environment variable manually.")
		os.Exit(1)
	}

	config.SetProjectPassword(password)

	if verifierErr := config.SavePasswordVerifier(projectDir, password); verifierErr != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to save password verifier: %v\n", verifierErr)
		os.Exit(1)
	}

	// Check SSL status for warning
	cfg, cfgErr := config.GetConfig()
	sslEnabled := cfgErr == nil && cfg.WebUI != nil && cfg.WebUI.SSL

	// Display the generated password to the user
	fmt.Println("╔════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                   🔐 Project Password Generated                     ║")
	fmt.Println("╠════════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Username: maestro                                                 ║\n")
	fmt.Printf("║  Password: %-52s ║\n", password)
	fmt.Println("╠════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  RECORD THIS PASSWORD — it will not be shown again.               ║")
	fmt.Println("║  This password is used for WebUI login AND secrets encryption.    ║")
	fmt.Println("║  If lost, stored secrets cannot be recovered.                     ║")
	fmt.Println("║                                                                   ║")
	fmt.Println("║  💡 Set MAESTRO_PASSWORD env var to use your own password.        ║")
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

// handleSecretsDecryptionIfReady decrypts the secrets file if the password is already
// in memory (from MAESTRO_PASSWORD env var or first-run generation). If the password is
// not yet available (pending recovery via WebUI login), this skips gracefully — secrets
// will be decrypted lazily when the user logs in via tryRecoverPassword in the auth middleware.
func handleSecretsDecryptionIfReady(projectDir string) {
	// If no password in memory, secrets will be decrypted after WebUI login
	password := config.GetProjectPassword()
	if password == "" {
		if config.SecretsFileExists(projectDir) {
			config.LogInfo("🔐 Secrets file found. Will decrypt after password is provided via WebUI.")
		}
		return
	}

	// No secrets file — nothing to decrypt
	if !config.SecretsFileExists(projectDir) {
		return
	}

	config.LogInfo("📋 Loading project from %s", projectDir)

	secrets, err := config.DecryptSecretsFile(projectDir, password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to decrypt secrets file (check password): %v\n", err)
		os.Exit(1)
	}

	config.SetDecryptedSecrets(secrets)
	config.LogInfo("✅ Credentials decrypted successfully")

	displayWebUIInfo()
}

// displayWebUIInfo displays WebUI access information if enabled.
func displayWebUIInfo() {
	cfg, err := config.GetConfig()
	if err == nil && cfg.WebUI != nil && cfg.WebUI.Enabled {
		protocol := protocolHTTP
		if cfg.WebUI.SSL {
			protocol = protocolHTTPS
		}
		fmt.Printf("🌐 WebUI: %s://%s:%d (username: maestro, use same password)\n",
			protocol, cfg.WebUI.Host, cfg.WebUI.Port)
	}
}
