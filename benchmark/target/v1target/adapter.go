// Package v1target is the v1-as-patched benchmark adapter (Phase 1 item 4,
// design_adapter_v1.md): it drives the frozen-lineage v1 maestro binary
// black-box — per-run Gitea forge isolation, subprocess invocation with DB
// polling, honest post-hoc metric normalization from maestro.db, durable
// evidence export, and MPH identity from audited prompt content.
//
// Budget enforcement is declared post-hoc until item 5 lands the P-1 usage
// surface, which flips this adapter to streamed.
package v1target

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/internal/contenthash"
	"github.com/SnapdragonPartners/maestro/benchmark/internal/gitx"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

// Adapter identity.
const (
	adapterName    = "v1-as-patched"
	adapterVersion = "0.1.0"
	solutionLeaf   = "/solution"
	defaultPoll    = 5 * time.Second
	stopGrace      = 20 * time.Second
)

// settings are the adapter-interpreted harness settings from the bundle.
type settings struct {
	MaestroBin string
	SourceDir  string
	Platform   string
	TestCmd    string
	PollEvery  time.Duration
}

func parseSettings(spec *target.AttemptSpec) (settings, error) {
	raw := spec.Bundle.Harness.Settings
	s := settings{
		MaestroBin: raw["maestro_bin"],
		SourceDir:  raw["source_dir"],
		Platform:   raw["platform"],
		TestCmd:    raw["test_cmd"],
		PollEvery:  defaultPoll,
	}
	if s.MaestroBin == "" || s.SourceDir == "" {
		return s, fmt.Errorf("harness settings maestro_bin and source_dir are required")
	}
	if s.Platform == "" {
		s.Platform = "go"
	}
	if s.TestCmd == "" {
		s.TestCmd = "go test ./..."
	}
	if interval := raw["poll_interval"]; interval != "" {
		parsed, err := time.ParseDuration(interval)
		if err != nil {
			return s, fmt.Errorf("poll_interval: %w", err)
		}
		s.PollEvery = parsed
	}
	return s, nil
}

// Adapter drives the v1-as-patched target.
type Adapter struct {
	gitea *giteaManager
	mu    sync.Mutex
}

// New returns the v1-as-patched adapter.
func New() *Adapter {
	return &Adapter{gitea: newGiteaManager()}
}

// Identity implements target.Adapter.
func (a *Adapter) Identity() target.Identity {
	return target.Identity{Name: adapterName, Version: adapterVersion}
}

// Capabilities implements target.Adapter: what maestro.db can honestly
// yield pre-P-1. llm_calls arrives with the P-1 usage surface (item 5);
// agent_requests is inter-agent messaging, never an LLM-call counter.
func (a *Adapter) Capabilities() target.Capabilities {
	return target.Capabilities{Metrics: []runrecord.MetricKey{
		runrecord.MetricTokensTotal,
		runrecord.MetricCostUSD,
		runrecord.MetricToolCalls,
	}}
}

// Describe implements target.Adapter: immutable binary identity, the
// audited prompt-content hash, canonical model routing, declared post-hoc
// enforcement, and the target commit from the source checkout.
func (a *Adapter) Describe(ctx context.Context, spec *target.AttemptSpec) (runrecord.TargetDescriptor, error) {
	if err := spec.Validate(); err != nil {
		return runrecord.TargetDescriptor{}, fmt.Errorf("v1target describe: %w", err)
	}
	s, err := parseSettings(spec)
	if err != nil {
		return runrecord.TargetDescriptor{}, fmt.Errorf("v1target describe: %w", err)
	}
	binaryID, versionOut, err := binaryIdentity(ctx, s.MaestroBin)
	if err != nil {
		return runrecord.TargetDescriptor{}, err
	}
	commit, err := gitx.Head(ctx, s.SourceDir)
	if err != nil {
		return runrecord.TargetDescriptor{}, fmt.Errorf("target commit from source_dir: %w", err)
	}
	if checkErr := crossCheckCommit(versionOut, commit); checkErr != nil {
		return runrecord.TargetDescriptor{}, checkErr
	}
	pHash, err := promptHash(s.SourceDir)
	if err != nil {
		return runrecord.TargetDescriptor{}, err
	}
	model, err := canonicalRouting(spec)
	if err != nil {
		return runrecord.TargetDescriptor{}, err
	}
	harnessHash, err := contenthash.CanonicalJSON(spec.Bundle.Harness)
	if err != nil {
		return runrecord.TargetDescriptor{}, fmt.Errorf("harness hash: %w", err)
	}
	return runrecord.TargetDescriptor{
		AdapterName:       adapterName,
		AdapterVersion:    adapterVersion,
		CommitHash:        commit,
		BinaryIdentity:    binaryID,
		BudgetEnforcement: runrecord.EnforcementPostHoc,
		MPH: runrecord.MPHIdentity{
			Model:          model,
			PromptPack:     "v1-embedded",
			PromptHash:     pHash,
			HarnessHash:    harnessHash,
			MaestroVersion: strings.TrimSpace(versionOut),
		},
		Capabilities: a.Capabilities().Metrics,
	}, nil
}

// binaryIdentity hashes the executable bytes and captures -version output:
// immutable identity, unlike a path (design round 1).
func binaryIdentity(ctx context.Context, bin string) (identity, versionOut string, err error) {
	raw, err := os.ReadFile(bin)
	if err != nil {
		return "", "", fmt.Errorf("read maestro binary: %w", err)
	}
	sum := sha256.Sum256(raw)
	out, err := exec.CommandContext(ctx, bin, "-version").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("maestro -version: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	versionOut = strings.TrimSpace(string(out))
	return "sha256:" + hex.EncodeToString(sum[:]) + " " + firstLine(versionOut), versionOut, nil
}

// crossCheckCommit verifies the binary's embedded commit against the source
// checkout when the version output exposes one (40-hex token).
func crossCheckCommit(versionOut, commit string) error {
	for _, token := range strings.Fields(versionOut) {
		if len(token) == 40 && isHex(token) && token != commit {
			return fmt.Errorf("binary commit %s does not match source_dir commit %s", token, commit)
		}
	}
	return nil
}

func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

// canonicalRouting serializes the bundle's complete model routing as
// canonical JSON (sorted keys, delimiter-safe) so reviewer heterogeneity is
// never reduced to the default model.
func canonicalRouting(spec *target.AttemptSpec) (string, error) {
	routing := map[string]string{"default": spec.Bundle.Model.Default}
	for role, model := range spec.Bundle.Model.Roles {
		routing[role] = model
	}
	raw, err := json.Marshal(routing)
	if err != nil {
		return "", fmt.Errorf("canonical routing: %w", err)
	}
	return string(raw), nil
}

// Run implements target.Adapter: seed the throwaway forge repo, launch v1,
// poll to terminal, export evidence, and import the solution.
func (a *Adapter) Run(ctx context.Context, spec *target.AttemptSpec) (*target.Observation, error) {
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("v1target run: %w", err)
	}
	s, err := parseSettings(spec)
	if err != nil {
		return nil, fmt.Errorf("v1target run: %w", err)
	}
	if giteaErr := a.gitea.ensureRunning(ctx); giteaErr != nil {
		return nil, fmt.Errorf("v1target gitea: %w", giteaErr)
	}
	cloneURL, authURL, err := a.gitea.createSeededRepo(ctx, spec.RunID, spec.WorkspaceDir, spec.Story.Fixture.Commit)
	if err != nil {
		return nil, fmt.Errorf("v1target seed: %w", err)
	}
	run := &v1Run{adapter: a, spec: spec, settings: s, cloneURL: cloneURL, authURL: authURL}
	return run.execute(ctx)
}

// Cleanup implements target.Adapter: delete the throwaway repo and the v1
// project dir; leftovers surface as errors so the engine records the
// attempt invalid.
func (a *Adapter) Cleanup(ctx context.Context, spec *target.AttemptSpec) error {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("v1target cleanup: %w", err)
	}
	var problems []string
	a.mu.Lock()
	running := a.gitea.running
	a.mu.Unlock()
	if running {
		if err := a.gitea.deleteRepo(ctx, spec.RunID); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if err := os.RemoveAll(projectDirFor(spec)); err != nil {
		problems = append(problems, "project dir: "+err.Error())
	}
	if len(problems) > 0 {
		return fmt.Errorf("v1target cleanup: %s", strings.Join(problems, "; "))
	}
	return nil
}

// Close implements io.Closer: full Docker teardown of the shared Gitea
// container and volume. The runner closes adapters after the suite.
func (a *Adapter) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return a.gitea.teardown(ctx)
}

// projectDirFor places the per-run v1 project dir beside the workspace.
func projectDirFor(spec *target.AttemptSpec) string {
	return filepath.Join(filepath.Dir(spec.WorkspaceDir), spec.RunID+"-v1project")
}
