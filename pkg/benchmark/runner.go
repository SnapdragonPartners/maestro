package benchmark

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"orchestrator/pkg/logx"
)

// RunOptions configures a single benchmark instance run.
type RunOptions struct {
	// BaseDir is the root directory for per-instance project dirs.
	BaseDir string

	// MaestroBin is the path to the maestro binary.
	MaestroBin string

	// ContainerImage is the default Docker image (used when inst.EvalImage is empty).
	ContainerImage string

	// ArchiveDir is where artifacts are archived after each run.
	ArchiveDir string

	// Timeout is the per-instance wall-clock timeout.
	Timeout time.Duration

	// PollInterval is the DB polling interval (0 = default 10s).
	PollInterval time.Duration
}

// processState tracks a Maestro subprocess lifecycle.
// Multiple goroutines can observe the exit without consuming a channel value.
type processState struct {
	done chan struct{} // closed when process exits
	err  error         // set before done is closed
	once sync.Once
}

func newProcessState() *processState {
	return &processState{done: make(chan struct{})}
}

func (ps *processState) finish(err error) {
	ps.once.Do(func() {
		ps.err = err
		close(ps.done)
	})
}

// exited returns true if the process has already exited.
func (ps *processState) exited() bool {
	select {
	case <-ps.done:
		return true
	default:
		return false
	}
}

// RunInstance orchestrates a single SWE-EVO benchmark instance from start to finish.
func RunInstance(ctx context.Context, inst *Instance, giteaMgr *BenchGitea, opts *RunOptions) Result {
	logger := logx.NewLogger("bench-run")
	start := time.Now()

	result := Result{
		InstanceID: inst.InstanceID,
		Outcome:    OutcomeProcessError,
	}

	image := resolveImage(inst, opts.ContainerImage)
	if image == "" {
		logger.Error("No container image for instance %s", inst.InstanceID)
		return result
	}

	// Always clean up the Gitea repo when done (including partial setup failures).
	defer func() {
		if delErr := giteaMgr.DeleteRepo(ctx, inst.InstanceID); delErr != nil {
			logger.Warn("[%s] Delete repo: %v", inst.InstanceID, delErr)
		}
	}()

	projectDir, setupErr := setupProjectDir(ctx, inst, giteaMgr, image, opts)
	if setupErr != nil {
		logger.Error("[%s] Setup: %v", inst.InstanceID, setupErr)
		return result
	}

	// Pre-pull Docker image.
	logger.Info("[%s] Pre-pulling image %s", inst.InstanceID, image)
	pullCmd := exec.CommandContext(ctx, "docker", "pull", image)
	if pullOut, pullErr := pullCmd.CombinedOutput(); pullErr != nil {
		logger.Warn("[%s] Docker pull failed (continuing): %v\n%s", inst.InstanceID, pullErr, string(pullOut))
	}

	// Launch Maestro and poll for completion.
	pollOutcome := launchAndPoll(ctx, inst, projectDir, opts, logger)

	result.Outcome = pollOutcome
	result.ElapsedSecs = time.Since(start).Seconds()

	// Collect patch (always attempt).
	cloneURL := fmt.Sprintf("%s/%s/%s.git", giteaMgr.baseURL, "maestro", sanitizeRepoName(inst.InstanceID))
	logger.Info("[%s] Collecting patch", inst.InstanceID)
	patch, patchErr := CollectPatch(cloneURL, projectDir)
	if patchErr != nil {
		logger.Warn("[%s] Collect patch: %v", inst.InstanceID, patchErr)
	} else {
		result.Patch = patch
	}

	// Archive artifacts.
	if opts.ArchiveDir != "" {
		if archiveErr := ArchiveArtifacts(projectDir, opts.ArchiveDir, inst.InstanceID); archiveErr != nil {
			logger.Warn("[%s] Archive artifacts: %v", inst.InstanceID, archiveErr)
		}
		result.ArtifactsDir = filepath.Join(opts.ArchiveDir, inst.InstanceID)
	}

	logger.Info("[%s] Complete: outcome=%s elapsed=%.1fs patch=%d bytes",
		inst.InstanceID, result.Outcome, result.ElapsedSecs, len(result.Patch))
	return result
}

func resolveImage(inst *Instance, defaultImage string) string {
	if inst.EvalImage != "" {
		return inst.EvalImage
	}
	return defaultImage
}

// setupProjectDir creates the project directory, seeds the Gitea repo, and writes
// all config files needed for a Maestro run. Returns an absolute project directory path.
func setupProjectDir(ctx context.Context, inst *Instance, giteaMgr *BenchGitea, image string, opts *RunOptions) (string, error) {
	relDir := filepath.Join(opts.BaseDir, sanitizeRepoName(inst.InstanceID))

	// Convert to absolute path so child processes resolve paths correctly.
	projectDir, absErr := filepath.Abs(relDir)
	if absErr != nil {
		return "", fmt.Errorf("resolve absolute path for %s: %w", relDir, absErr)
	}

	if rmErr := os.RemoveAll(projectDir); rmErr != nil {
		return "", fmt.Errorf("clean project dir: %w", rmErr)
	}
	maestroDir := filepath.Join(projectDir, ".maestro")
	if mkErr := os.MkdirAll(maestroDir, 0755); mkErr != nil {
		return "", fmt.Errorf("create project dir: %w", mkErr)
	}

	// Seed Gitea repo.
	cloneURL, seedErr := giteaMgr.CreateAndSeedRepo(ctx, inst.InstanceID, inst.Repo, inst.BaseCommit)
	if seedErr != nil {
		return "", fmt.Errorf("seed repo: %w", seedErr)
	}

	// Write forge_state.json.
	if fsErr := giteaMgr.WriteForgeState(projectDir, inst.InstanceID); fsErr != nil {
		return "", fmt.Errorf("write forge state: %w", fsErr)
	}

	// Generate and write Maestro config.
	cfgData, cfgErr := GenerateConfig(inst, cloneURL, image)
	if cfgErr != nil {
		return "", fmt.Errorf("generate config: %w", cfgErr)
	}
	configPath := filepath.Join(projectDir, "benchmark-config.json")
	if writeErr := os.WriteFile(configPath, cfgData, 0644); writeErr != nil {
		return "", fmt.Errorf("write config: %w", writeErr)
	}

	// Write problem statement as spec file.
	specPath := filepath.Join(projectDir, "problem_statement.md")
	if writeErr := os.WriteFile(specPath, []byte(inst.ProblemStatement), 0644); writeErr != nil {
		return "", fmt.Errorf("write spec: %w", writeErr)
	}

	return projectDir, nil
}

// launchAndPoll starts Maestro as a subprocess, polls the DB for completion,
// then signals Maestro to stop.
func launchAndPoll(ctx context.Context, inst *Instance, projectDir string, opts *RunOptions, logger *logx.Logger) Outcome {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 60 * time.Minute
	}
	maestroCtx, maestroCancel := context.WithTimeout(ctx, timeout)
	defer maestroCancel()

	maestroBin := opts.MaestroBin
	if maestroBin == "" {
		maestroBin = "maestro"
	}

	configPath := filepath.Join(projectDir, "benchmark-config.json")
	specPath := filepath.Join(projectDir, "problem_statement.md")

	maestroCmd := exec.CommandContext(maestroCtx, maestroBin,
		"--config", configPath,
		"--spec-file", specPath,
		"--projectdir", projectDir,
		"--nowebui",
	)
	maestroCmd.Dir = projectDir

	// Log output to file.
	logsDir := filepath.Join(projectDir, "logs")
	if mkErr := os.MkdirAll(logsDir, 0755); mkErr != nil {
		logger.Error("[%s] Create logs dir: %v", inst.InstanceID, mkErr)
		return OutcomeProcessError
	}
	logFile, logErr := os.Create(filepath.Join(logsDir, "maestro-stdout.log"))
	if logErr != nil {
		logger.Error("[%s] Create log file: %v", inst.InstanceID, logErr)
		return OutcomeProcessError
	}
	maestroCmd.Stdout = logFile
	maestroCmd.Stderr = logFile

	logger.Info("[%s] Launching Maestro", inst.InstanceID)
	if startErr := maestroCmd.Start(); startErr != nil {
		_ = logFile.Close()
		logger.Error("[%s] Start maestro: %v", inst.InstanceID, startErr)
		return OutcomeProcessError
	}

	// Track process lifecycle via closeable channel (safe for multiple observers).
	ps := newProcessState()
	go func() {
		ps.finish(maestroCmd.Wait())
		_ = logFile.Close()
	}()

	// Poll DB for completion.
	dbPath := filepath.Join(projectDir, ".maestro", "maestro.db")
	pollCfg := PollConfig{
		DBPath:     dbPath,
		Timeout:    timeout,
		Interval:   opts.PollInterval,
		StallGrace: 5 * time.Minute,
	}

	pollOutcome := pollForCompletionWithRetry(maestroCtx, pollCfg, dbPath, ps, logger, inst.InstanceID)

	// Signal Maestro to stop.
	signalMaestroStop(maestroCmd, ps, logger, inst.InstanceID)

	return pollOutcome
}

// pollForCompletionWithRetry polls the DB once it exists, retrying the open
// until the DB file appears or the process exits.
func pollForCompletionWithRetry(ctx context.Context, cfg PollConfig, dbPath string, ps *processState, logger *logx.Logger, instanceID string) Outcome {
	// Phase 1: Wait for DB to appear and discover spec/session IDs.
	// Process may exit during this phase before any stories are created.
	cfg, discovered := discoverWithRetry(ctx, cfg, dbPath, ps, logger, instanceID)
	if !discovered {
		// Context cancelled, or process exited before stories appeared.
		if ctx.Err() != nil {
			return OutcomeTimeout
		}
		if ps.err != nil {
			logger.Warn("[%s] Maestro exited with error: %v", instanceID, ps.err)
		} else {
			logger.Warn("[%s] Maestro exited before stories were created", instanceID)
		}
		return OutcomeProcessError
	}

	// Phase 2: Poll for story completion (blocks until terminal).
	logger.Info("[%s] Polling DB (spec=%s, session=%s)", instanceID, cfg.SpecID, cfg.SessionID)
	outcome, pollErr := PollForCompletion(ctx, cfg)
	if pollErr != nil {
		logger.Warn("[%s] Poll error: %v", instanceID, pollErr)
	}
	if outcome != "" {
		return outcome
	}
	return OutcomeProcessError
}

// discoverWithRetry waits for the DB file to appear and discovers the spec/session IDs.
// Returns the updated config and true if discovery succeeded.
func discoverWithRetry(ctx context.Context, cfg PollConfig, dbPath string, ps *processState, _ *logx.Logger, _ string) (PollConfig, bool) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return cfg, false
		case <-ps.done:
			return cfg, false
		case <-ticker.C:
			if _, statErr := os.Stat(dbPath); os.IsNotExist(statErr) {
				continue
			}

			specID, discoverErr := discoverSpecID(dbPath)
			if discoverErr != nil || specID == "" {
				continue
			}

			sessionID, sessErr := discoverSessionID(dbPath)
			if sessErr != nil || sessionID == "" {
				continue
			}

			cfg.SpecID = specID
			cfg.SessionID = sessionID
			cfg.DBPath = dbPath
			return cfg, true
		}
	}
}

// discoverSpecID reads the first spec_id from the stories table.
func discoverSpecID(dbPath string) (string, error) {
	db, err := openReadOnly(dbPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()

	var specID string
	row := db.QueryRow(`SELECT spec_id FROM stories LIMIT 1`)
	if scanErr := row.Scan(&specID); scanErr != nil {
		return "", fmt.Errorf("scan spec_id: %w", scanErr)
	}
	return specID, nil
}

// discoverSessionID reads the session_id from the stories table.
func discoverSessionID(dbPath string) (string, error) {
	db, err := openReadOnly(dbPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()

	var sessionID string
	row := db.QueryRow(`SELECT session_id FROM stories LIMIT 1`)
	if scanErr := row.Scan(&sessionID); scanErr != nil {
		return "", fmt.Errorf("scan session_id: %w", scanErr)
	}
	return sessionID, nil
}

// signalMaestroStop sends SIGTERM, waits briefly, then SIGKILL if needed.
func signalMaestroStop(cmd *exec.Cmd, ps *processState, logger *logx.Logger, instanceID string) {
	if cmd.Process == nil {
		return
	}

	// Check if already exited.
	if ps.exited() {
		return
	}

	logger.Info("[%s] Sending SIGTERM to Maestro (pid=%d)", instanceID, cmd.Process.Pid)
	_ = cmd.Process.Signal(syscall.SIGTERM)

	// Wait up to 30 seconds for graceful shutdown.
	select {
	case <-ps.done:
		return
	case <-time.After(30 * time.Second):
		logger.Warn("[%s] Maestro didn't exit after SIGTERM, sending SIGKILL", instanceID)
		_ = cmd.Process.Kill()
		<-ps.done
	}
}
