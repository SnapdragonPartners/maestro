package v1target

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/mph"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

// The adapter must satisfy the contract at compile time.
var _ target.Adapter = (*Adapter)(nil)

func testSpec(t *testing.T, settingsMap map[string]string) *target.AttemptSpec {
	t.Helper()
	def := &story.Definition{
		SchemaVersion: story.SchemaVersion,
		ID:            "v1-test",
		Title:         "t",
		Level:         story.LevelStory,
		Fixture:       story.Fixture{Repo: "file:///nowhere", Commit: strings.Repeat("ab", 20), BaseBranch: "main"},
		Prompt:        story.Prompt{Text: "do the thing"},
		Validators:    []story.Validator{{Name: "true", Command: "true"}},
		Checks:        []story.Check{{Name: "always", Type: story.CheckCommand, Command: "true"}},
		Budget:        story.Budget{MaxTokens: 1000, MaxWallClockSeconds: 60, MaxCostUSD: 1},
	}
	bundle := &mph.Bundle{
		SchemaVersion: mph.SchemaVersion,
		Name:          "v1-test-bundle",
		Description:   "t",
		Model:         mph.ModelRouting{Default: "model-x", Roles: map[string]string{"architect": "model-y"}},
		Prompt:        mph.PromptRef{Pack: "v1-embedded"},
		Harness:       mph.HarnessSettings{Adapter: "v1-as-patched", Settings: settingsMap},
		Budget:        mph.DeclaredBudget{ExpectedTokensPerRun: 1, ExpectedCostUSDPerRun: 0.1, MaxCostUSDPerRun: 1, MaxCostUSDPerSuite: 10},
	}
	return &target.AttemptSpec{
		Story:           def,
		Bundle:          bundle,
		Budget:          def.Budget,
		RunID:           "v1-test--bundle--r1--abcd",
		SuiteRunID:      "suite-v1",
		StoryHash:       "sha256:" + strings.Repeat("ab", 32),
		BundleHash:      "sha256:" + strings.Repeat("cd", 32),
		WorkspaceDir:    t.TempDir(),
		EvidenceDir:     t.TempDir(),
		BranchNamespace: "golden/v1-test--bundle--r1--abcd",
	}
}

func TestParseSettings(t *testing.T) {
	spec := testSpec(t, map[string]string{"maestro_bin": "/bin/m", "source_dir": "/src", "poll_interval": "250ms"})
	s, err := parseSettings(spec)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Platform != "go" || s.TestCmd != "go test ./..." || s.PollEvery.String() != "250ms" {
		t.Fatalf("defaults wrong: %+v", s)
	}
	spec = testSpec(t, map[string]string{"source_dir": "/src"})
	if _, err := parseSettings(spec); err == nil {
		t.Fatalf("missing maestro_bin must fail")
	}
}

func TestCanonicalRoutingIsCanonicalJSON(t *testing.T) {
	spec := testSpec(t, nil)
	routing, err := canonicalRouting(spec)
	if err != nil {
		t.Fatalf("routing: %v", err)
	}
	var decoded map[string]string
	if decodeErr := json.Unmarshal([]byte(routing), &decoded); decodeErr != nil {
		t.Fatalf("routing must be valid JSON: %v", decodeErr)
	}
	if decoded["default"] != "model-x" || decoded["architect"] != "model-y" {
		t.Fatalf("routing must carry the complete map: %s", routing)
	}
	// Go's json.Marshal sorts map keys: deterministic representation.
	again, err := canonicalRouting(spec)
	if err != nil || routing != again {
		t.Fatalf("routing must be deterministic: %q vs %q (%v)", routing, again, err)
	}
}

func TestV1ConfigGeneration(t *testing.T) {
	spec := testSpec(t, map[string]string{"maestro_bin": "/bin/m", "source_dir": "/src", "test_cmd": "go test -short ./..."})
	s, err := parseSettings(spec)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	run := &v1Run{spec: spec, settings: s, cloneURL: "http://127.0.0.1:3000/golden/repo.git"}
	raw, err := run.v1Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("config must be JSON: %v", err)
	}
	agents := cfg["agents"].(map[string]any)  //nolint:forcetypeassert // test fixture shape
	git := cfg["git"].(map[string]any)        //nolint:forcetypeassert // test fixture shape
	buildCfg := cfg["build"].(map[string]any) //nolint:forcetypeassert // test fixture shape
	forgeCfg := cfg["forge"].(map[string]any) //nolint:forcetypeassert // test fixture shape
	webui := cfg["webui"].(map[string]any)    //nolint:forcetypeassert // test fixture shape
	if agents["architect_model"] != "model-y" || agents["coder_model"] != "model-x" || agents["pm_model"] != "model-x" {
		t.Fatalf("role routing must map onto v1 agent models: %v", agents)
	}
	if git["repo_url"] != run.cloneURL || git["target_branch"] != "main" {
		t.Fatalf("git config wrong: %v", git)
	}
	if buildCfg["test"] != "go test -short ./..." || forgeCfg["provider"] != "gitea" || webui["enabled"] != false {
		t.Fatalf("config sections wrong: build=%v forge=%v webui=%v", buildCfg, forgeCfg, webui)
	}
}

func TestClassification(t *testing.T) {
	all := classify([]storyState{{Status: "done"}, {Status: "done"}})
	if !all.allDone() || !all.terminal() {
		t.Fatalf("all done must be terminal-done: %+v", all)
	}
	mixed := classify([]storyState{{Status: "done"}, {Status: "failed"}})
	if mixed.allDone() || !mixed.terminal() {
		t.Fatalf("failed rows are terminal but never done: %+v", mixed)
	}
	running := classify([]storyState{{Status: "done"}, {Status: "coding"}})
	if running.terminal() {
		t.Fatalf("in-flight stories are not terminal: %+v", running)
	}
	if classify(nil).terminal() {
		t.Fatalf("zero stories is never terminal")
	}
}

func TestPRsSatisfied(t *testing.T) {
	states := []storyState{{Status: "done", PRID: "1"}}
	if prsSatisfied(states, nil) {
		t.Fatalf("a story claiming a PR requires at least one PR")
	}
	if prsSatisfied(states, []prInfo{{Number: 1, Merged: false}}) {
		t.Fatalf("unmerged PRs must not satisfy")
	}
	if !prsSatisfied(states, []prInfo{{Number: 1, Merged: true}}) {
		t.Fatalf("merged PRs must satisfy")
	}
}

func TestDBNormalizationAgainstCannedDB(t *testing.T) {
	dbPath := writeCannedDB(t)
	db, err := openV1DB(dbPath)
	if err != nil {
		t.Fatalf("open canned db: %v", err)
	}
	defer db.close() //nolint:errcheck // test cleanup
	ctx := context.Background()
	specID, sessionID, err := db.discover(ctx)
	if err != nil || specID != "spec-1" || sessionID != "sess-1" {
		t.Fatalf("discover: %v %s %s", err, specID, sessionID)
	}
	states, err := db.stories(ctx, specID, sessionID)
	if err != nil || len(states) != 2 {
		t.Fatalf("stories: %v %+v", err, states)
	}
	final := classify(states)
	if !final.allDone() {
		t.Fatalf("canned stories are done: %+v", final)
	}
	tokens, cost := final.aggregates()
	if tokens != 12000 || cost != 0.75 {
		t.Fatalf("aggregates: %d %v", tokens, cost)
	}
	count, err := db.toolCallCount(ctx, sessionID)
	if err != nil || count != 3 {
		t.Fatalf("tool calls: %v %d", err, count)
	}
	// WAL-consistent snapshot round-trips.
	snap := filepath.Join(t.TempDir(), "snapshot.db")
	if err := snapshotDB(ctx, dbPath, snap); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := os.Stat(snap); err != nil {
		t.Fatalf("snapshot file: %v", err)
	}
}

func TestMetricsNormalization(t *testing.T) {
	run := &v1Run{spec: testSpec(t, nil)}
	done := finalState{
		classification: classify([]storyState{{Status: "done", Tokens: 100, Cost: 0.5}}),
		toolCalls:      7,
	}
	metrics := run.metrics(&done)
	if err := metrics.Validate(); err != nil {
		t.Fatalf("metrics must be complete: %v", err)
	}
	if err := runrecord.CapabilityCoherence(New().Capabilities().Metrics, metrics); err != nil {
		t.Fatalf("metrics must be capability-coherent: %v", err)
	}
	if v, _ := metrics[runrecord.MetricTokensTotal].Float64(); v != 100 {
		t.Fatalf("tokens: %v", v)
	}
	if metrics[runrecord.MetricLLMCalls].Status != runrecord.StatusUnsupported {
		t.Fatalf("llm_calls is unsupported pre-P-1 (never agent_requests)")
	}
	if metrics[runrecord.MetricHumanInterventions].Status != runrecord.StatusNotApplicable {
		t.Fatalf("human metrics are not applicable unattended")
	}

	aborted := finalState{classification: classify([]storyState{{Status: "coding"}}), toolCalls: -1}
	metrics = run.metrics(&aborted)
	if metrics[runrecord.MetricTokensTotal].Status != runrecord.StatusUnavailable {
		t.Fatalf("pre-acceptance usage is unavailable pre-P-1, got %+v", metrics[runrecord.MetricTokensTotal])
	}
}
