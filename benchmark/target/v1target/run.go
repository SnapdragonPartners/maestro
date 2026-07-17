package v1target

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

// v1Run carries one attempt's state through launch, poll, evidence export,
// and solution import.
type v1Run struct {
	adapter  *Adapter
	spec     *target.AttemptSpec
	tail     *usageTail
	cloneURL string
	authURL  string
	settings settings
}

// finalState is everything poll and post-processing learned.
type finalState struct {
	sessionID string
	classification
	toolCalls int64 // -1 = unreadable
	prsMerged bool
}

// execute runs the v1 lifecycle. Evidence export happens before any
// teardown; the caller's Cleanup deletes the Gitea repo afterward.
func (r *v1Run) execute(ctx context.Context) (*target.Observation, error) {
	projectDir := projectDirFor(r.spec)
	if err := r.prepareProject(projectDir); err != nil {
		return nil, err
	}
	launchLog := filepath.Join(r.spec.EvidenceDir, "maestro-launch.log")
	proc, err := r.launch(ctx, projectDir, launchLog)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(projectDir, ".maestro", "maestro.db")
	r.tail = &usageTail{
		path:    filepath.Join(projectDir, ".maestro", "usage.jsonl"),
		errPath: filepath.Join(projectDir, ".maestro", usageErrorFileName),
		report:  r.spec.ReportUsage,
	}
	final, pollErr := r.poll(ctx, dbPath, proc)
	r.adapter.rememberSession(r.spec.RunID, final.sessionID)

	// Stop and fully reap the process BEFORE evidence collection: on a
	// cancelled attempt context the stop is an immediate group kill (no
	// 20s grace against an already-blown deadline), and evidence runs on
	// a fresh bounded context so the DB snapshot cannot fail merely
	// because the attempt was aborted.
	r.stop(proc, ctx.Err() != nil)
	proc.awaitExit()
	if tailErr := r.tail.advance(); tailErr != nil && pollErr == nil {
		pollErr = tailErr
	}
	ectx, cancel := context.WithTimeout(context.Background(), evidenceTimeout)
	defer cancel()

	if pollErr != nil {
		// Still export what exists — failed attempts need their story told;
		// the engine synthesizes metrics for the error path.
		r.exportEvidence(ectx, projectDir, dbPath, nil) //nolint:contextcheck // fresh bounded context by design: the attempt context is already cancelled
		return nil, pollErr
	}

	prs, prErr := r.adapter.gitea.listPRs(ectx, r.spec.RunID) //nolint:contextcheck // fresh bounded evidence context
	final.prsMerged = prErr == nil && prsSatisfied(final.states, prs)
	solutionBranch, importErr := r.importSolution(ectx)         //nolint:contextcheck // fresh bounded evidence context
	evidence := r.exportEvidence(ectx, projectDir, dbPath, prs) //nolint:contextcheck // fresh bounded evidence context
	if importErr != nil {
		// The target claimed terminal work we cannot import; treating that
		// as an in-place solution would validate the untouched fixture.
		return nil, fmt.Errorf("solution import failed: %w", importErr)
	}
	if !r.tail.validated {
		// This adapter declares streamed enforcement; a run that finished
		// without ever producing a validated usage-log header spent tokens
		// with enforcement blind. Refuse success rather than report
		// unavailable usage on an accepted run.
		return nil, fmt.Errorf("usage surface never validated: %s missing or lacked the v%d header", r.tail.path, usageSurfaceVersion)
	}

	return &target.Observation{
		Metrics:              r.metrics(&final),
		Evidence:             evidence,
		SolutionBranch:       solutionBranch,
		TerminalStateReached: final.allDone() && final.prsMerged,
	}, nil
}

// prsSatisfied implements the all-stories PR semantics: every story
// claiming a PR must match a DISTINCT returned PR number, every matched PR
// must be merged, and no PR in the throwaway repo may be unmerged.
func prsSatisfied(states []storyState, prs []prInfo) bool {
	byNumber := make(map[int64]prInfo, len(prs))
	for i := range prs {
		if !prs[i].Merged {
			return false
		}
		byNumber[prs[i].Number] = prs[i]
	}
	used := make(map[int64]bool, len(states))
	for i := range states {
		id := states[i].PRID
		if id == "" {
			// Every story must match a distinct merged PR; a completed
			// story without a PR identity cannot satisfy terminal state.
			return false
		}
		num, ok := prNumber(id)
		if !ok {
			return false
		}
		if _, exists := byNumber[num]; !exists || used[num] {
			return false
		}
		used[num] = true
	}
	return true
}

// prNumber extracts the trailing PR number from v1's pr_id (a bare number
// or a URL-ish reference ending in one).
func prNumber(id string) (int64, bool) {
	end := len(id)
	start := end
	for start > 0 && id[start-1] >= '0' && id[start-1] <= '9' {
		start--
	}
	if start == end {
		return 0, false
	}
	var n int64
	for _, r := range id[start:end] {
		n = n*10 + int64(r-'0')
	}
	return n, true
}

// prepareProject writes the v1 project dir: config.json, forge_state.json,
// and the spec file.
func (r *v1Run) prepareProject(projectDir string) error {
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return fmt.Errorf("project dir: %w", err)
	}
	if err := r.adapter.gitea.writeForgeState(projectDir, r.spec.RunID); err != nil {
		return err
	}
	cfg, err := r.v1Config()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(projectDir, "golden-config.json"), cfg, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	prompt := r.spec.Story.Prompt.Text
	if err := os.WriteFile(filepath.Join(projectDir, "story-spec.md"), []byte(prompt), 0o644); err != nil {
		return fmt.Errorf("write spec: %w", err)
	}
	return nil
}

// v1Config generates the v1 config.json (shape from v1's own benchmark
// harness), mapping the bundle's model routing onto v1's agent models.
func (r *v1Run) v1Config() ([]byte, error) {
	routing := r.spec.Bundle.Model
	roleModel := func(role string) string {
		if m, ok := routing.Roles[role]; ok {
			return m
		}
		return routing.Default
	}
	cfg := map[string]any{
		"project": map[string]any{
			"primary_platform": r.settings.Platform,
			"pack_name":        r.settings.Platform,
		},
		"git": map[string]any{
			"repo_url":      r.cloneURL,
			"target_branch": "main",
		},
		"forge":       map[string]any{"provider": "gitea"},
		"maintenance": map[string]any{"enabled": false},
		"webui":       map[string]any{"enabled": false},
		"agents": map[string]any{
			"max_coders":      1,
			"coder_model":     roleModel("coder"),
			"architect_model": roleModel("architect"),
			"pm_model":        roleModel("pm"),
		},
		"build": map[string]any{
			"build": "true",
			"lint":  "true",
			"run":   "true",
			"test":  r.settings.TestCmd,
		},
	}
	if r.settings.ContainerImage != "" {
		cfg["container"] = map[string]any{"name": r.settings.ContainerImage}
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal v1 config: %w", err)
	}
	return raw, nil
}

// launch starts the maestro subprocess, process-group isolated, output
// teed to the durable launch log.
func (r *v1Run) launch(ctx context.Context, projectDir, launchLog string) (*process, error) {
	logFile, err := os.Create(launchLog)
	if err != nil {
		return nil, fmt.Errorf("launch log: %w", err)
	}
	bin, err := filepath.Abs(r.settings.MaestroBin)
	if err != nil {
		return nil, fmt.Errorf("maestro bin path: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin,
		"--config", filepath.Join(projectDir, "golden-config.json"),
		"--spec-file", filepath.Join(projectDir, "story-spec.md"),
		"--projectdir", projectDir,
		"--nowebui",
	)
	cmd.Dir = projectDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) //nolint:wrapcheck // exec.Cmd.Cancel contract
	}
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		_ = logFile.Close() //nolint:errcheck // launch failed
		return nil, fmt.Errorf("start maestro: %w", err)
	}
	proc := &process{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()      //nolint:errcheck // exit observed via the done channel
		_ = logFile.Close() //nolint:errcheck // best effort
		close(proc.done)
	}()
	return proc, nil
}

// process tracks the maestro subprocess without racing exec.Cmd.Wait:
// ProcessState must never be read concurrently with Wait, so liveness is
// observed through a channel the waiter closes.
type process struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (p *process) dead() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// awaitExit blocks until the waiter goroutine has fully finished (log file
// closed), bounded defensively.
func (p *process) awaitExit() {
	select {
	case <-p.done:
	case <-time.After(30 * time.Second):
	}
}

// pollObserver holds discovery state across poll ticks: the DB handle and
// the spec/session IDs, each acquired once available.
type pollObserver struct {
	db        *v1DB
	dbPath    string
	specID    string
	sessionID string
	stage     string
}

// observe advances discovery and returns the current story states, or an
// error while the target has not produced them yet (stage says how far it
// got).
func (o *pollObserver) observe(ctx context.Context) ([]storyState, error) {
	if o.db == nil {
		o.stage = "its database"
		opened, err := openV1DB(o.dbPath)
		if err != nil {
			return nil, err
		}
		o.db = opened
	}
	if o.specID == "" {
		o.stage = "a spec"
		sid, sess, err := o.db.discover(ctx)
		if err != nil {
			return nil, err
		}
		o.specID, o.sessionID = sid, sess
	}
	o.stage = "stories"
	states, err := o.db.stories(ctx, o.specID, o.sessionID)
	if err != nil {
		return nil, err
	}
	if len(states) == 0 {
		return nil, errors.New("no stories yet")
	}
	return states, nil
}

func (o *pollObserver) close() {
	if o.db != nil {
		_ = o.db.close() //nolint:errcheck // read-only observer
	}
}

// poll watches maestro.db until the story set is terminal, the process
// dies, or the context expires.
func (r *v1Run) poll(ctx context.Context, dbPath string, proc *process) (finalState, error) {
	ticker := time.NewTicker(r.settings.PollEvery)
	defer ticker.Stop()
	obs := &pollObserver{dbPath: dbPath}
	defer obs.close()
	for {
		select {
		case <-ctx.Done():
			return finalState{sessionID: obs.sessionID, toolCalls: -1}, fmt.Errorf("v1 run aborted: %w", context.Cause(ctx))
		case <-ticker.C:
		}
		if tailErr := r.tail.advance(); tailErr != nil {
			// Run half of the P-1 handshake: a bad usage surface is a
			// target-identity error, never a silent downgrade.
			return finalState{sessionID: obs.sessionID, toolCalls: -1}, tailErr
		}
		states, err := obs.observe(ctx)
		if err != nil {
			if proc.dead() {
				return finalState{sessionID: obs.sessionID, toolCalls: -1}, fmt.Errorf("maestro exited before creating %s", obs.stage)
			}
			continue
		}
		final := finalState{classification: classify(states), sessionID: obs.sessionID, toolCalls: -1}
		if final.terminal() {
			if count, countErr := obs.db.toolCallCount(ctx, obs.sessionID); countErr == nil {
				final.toolCalls = count
			}
			return final, nil
		}
		if proc.dead() {
			return final, errors.New("maestro exited before stories reached a terminal state")
		}
	}
}

// stop terminates maestro. Normal stops get a graceful interrupt and a
// bounded grace period; when the attempt context is already cancelled the
// kill is immediate — a blown deadline must not wait out the grace.
func (r *v1Run) stop(proc *process, immediate bool) {
	if proc.cmd.Process == nil || proc.dead() {
		return
	}
	if !immediate {
		_ = syscall.Kill(-proc.cmd.Process.Pid, syscall.SIGINT) //nolint:errcheck // best-effort graceful stop
		select {
		case <-proc.done:
			return
		case <-time.After(stopGrace):
		}
	}
	_ = syscall.Kill(-proc.cmd.Process.Pid, syscall.SIGKILL) //nolint:errcheck // final kill
}

// metrics assembles the final metric set from the streamed P-1 usage log
// (canonical for tokens/cost/llm_calls, present for failed attempts too);
// everything not observable from v1 is unsupported, and usage metrics
// degrade to unavailable only when the log was never validated.
func (r *v1Run) metrics(final *finalState) runrecord.Metrics {
	metrics := make(runrecord.Metrics, len(runrecord.Registry()))
	for _, spec := range runrecord.Registry() {
		metrics[spec.Key] = runrecord.Unsupported()
	}
	metrics[runrecord.MetricHumanInterventions] = runrecord.NotApplicable()
	metrics[runrecord.MetricHumanAttentionSeconds] = runrecord.NotApplicable()
	// The P-1 usage log is the canonical usage source: per-call accrual,
	// present for failed attempts too. Story aggregates remain in the DB
	// snapshot evidence for cross-checking.
	if r.tail != nil && r.tail.validated {
		metrics[runrecord.MetricTokensTotal] = runrecord.Measured(float64(r.tail.tokens))
		metrics[runrecord.MetricCostUSD] = runrecord.Measured(r.tail.costUSD)
		metrics[runrecord.MetricLLMCalls] = runrecord.Measured(float64(r.tail.calls))
	} else {
		reason := "usage log never appeared"
		metrics[runrecord.MetricTokensTotal] = runrecord.Unavailable(reason)
		metrics[runrecord.MetricCostUSD] = runrecord.Unavailable(reason)
		metrics[runrecord.MetricLLMCalls] = runrecord.Unavailable(reason)
	}
	if final.toolCalls >= 0 {
		metrics[runrecord.MetricToolCalls] = runrecord.Measured(float64(final.toolCalls))
	} else {
		metrics[runrecord.MetricToolCalls] = runrecord.Unavailable("tool_executions not readable")
	}
	return metrics
}
