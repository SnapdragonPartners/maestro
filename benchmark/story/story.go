// Package story defines golden story definitions: the authored TOML schema,
// its loader, validation, and content identity (ADR 0025).
//
// A golden story is a declarative, versioned fixture referencing a pinned
// external fixture repository. Authored files are TOML; identity and
// everything the runner emits are JSON (Phase 1 plan, ratified decisions).
package story

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"
)

// Story schema versions. Check types and section shapes change only with a
// version bump; older versions keep loading unchanged so their content hashes
// — and any baseline recorded against them — never move.
//
//	v1: the original schema.
//	v2: adds the `oracle` check type (engine-materialised, hashed adjacent
//	    assets). See docs/v2/phase_1/design_oracles.md.
const (
	SchemaV1         = 1
	SchemaV2         = 2
	MaxSchemaVersion = SchemaV2

	// SchemaVersion is retained for callers wanting "the current version".
	SchemaVersion = SchemaV1

	// OracleAssetPrefix is the reserved basename namespace for oracle assets.
	// Requiring it up front makes the source→destination mapping trivial
	// (basename preserved verbatim) and guarantees a materialised asset can
	// never shadow an ordinary solution file.
	OracleAssetPrefix = "zz_oracle_"

	// OracleScratchSolutionCommit is the only recognised `scratch` mode: a
	// clean checkout of the immutable solution commit for mutation helpers.
	OracleScratchSolutionCommit = "solution-commit"
)

//nolint:gochecknoglobals // Package-level compiled regexes for performance.
var (
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	kebabPattern  = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// Level types a story's input prompt (ADR 0025).
type Level string

// Prompt levels. Phase 1 authors only Story-scoped definitions, but the
// schema carries the full taxonomy.
const (
	// LevelFeature is a Feature-scoped prompt.
	LevelFeature Level = "feature"
	// LevelEpic is an Epic-scoped prompt.
	LevelEpic Level = "epic"
	// LevelStory is a Story-scoped (PR-sized) prompt.
	LevelStory Level = "story"
)

// CheckType names one deterministic pass/fail check kind. The set is closed
// per schema version.
type CheckType string

// Check types.
const (
	// CheckCommand runs a command in the workspace; exit 0 passes.
	CheckCommand CheckType = "command"
	// CheckFilesChangedWithin passes when the attempt's diff is confined to
	// the story's expectations.allowed_paths.
	CheckFilesChangedWithin CheckType = "files_changed_within"
	// CheckFileContains passes when the file at Path contains Contains.
	CheckFileContains CheckType = "file_contains"
	// CheckOracle materialises hashed adjacent Go assets into the bound
	// solution (or a scratch checkout) and runs Argv; exit 0 passes. Schema
	// v2 only. See docs/v2/phase_1/design_oracles.md.
	CheckOracle CheckType = "oracle"
)

// Definition is one golden story.
type Definition struct {
	Fixture       Fixture      `toml:"fixture" json:"fixture"`
	Prompt        Prompt       `toml:"prompt" json:"prompt"`
	Expectations  Expectations `toml:"expectations" json:"expectations"`
	ID            string       `toml:"id" json:"id"`
	Title         string       `toml:"title" json:"title"`
	Level         Level        `toml:"level" json:"level"`
	Validators    []Validator  `toml:"validators" json:"validators"`
	Checks        []Check      `toml:"checks" json:"checks"`
	Rubrics       []Rubric     `toml:"rubrics,omitempty" json:"rubrics,omitempty"`
	Budget        Budget       `toml:"budget" json:"budget"`
	SchemaVersion int          `toml:"schema_version" json:"schema_version"`
}

// Fixture pins the external repository a story starts from.
type Fixture struct {
	Repo string `toml:"repo" json:"repo"`
	// Commit is the full 40-hex pinned starting commit.
	Commit     string `toml:"commit" json:"commit"`
	BaseBranch string `toml:"base_branch" json:"base_branch"`
}

// Prompt is the story's input prompt.
type Prompt struct {
	Text string `toml:"text" json:"text"`
}

// Expectations describe the expected footprint and evidence of a solution.
type Expectations struct {
	// AllowedPaths are the files or areas a solution may touch; consumed by
	// the files_changed_within check.
	AllowedPaths []string `toml:"allowed_paths,omitempty" json:"allowed_paths,omitempty"`
	// RequiredArtifacts must be present for benchmark acceptance.
	RequiredArtifacts []string `toml:"required_artifacts,omitempty" json:"required_artifacts,omitempty"`
	// EvidenceShape lists the evidence kinds the attempt must produce.
	EvidenceShape []string `toml:"evidence_shape,omitempty" json:"evidence_shape,omitempty"`
}

// Validator is one engine-executed validation command (build, tests, lint).
// Validators run in the isolated workspace; targets never self-report them.
type Validator struct {
	Name    string `toml:"name" json:"name"`
	Command string `toml:"command" json:"command"`
}

// Check is one deterministic pass/fail check, engine-executed.
//
// Field order is chosen for struct alignment and has no bearing on identity:
// the content hash sorts object keys, so declaration order never affects a
// story's hash. Oracle fields (schema v2) all carry omitempty, so a v1 check's
// JSON — and therefore its hash — is byte-identical to before they existed.
type Check struct {
	Name       string    `toml:"name" json:"name"`
	Type       CheckType `toml:"type" json:"type"`
	Command    string    `toml:"command,omitempty" json:"command,omitempty"`
	Path       string    `toml:"path,omitempty" json:"path,omitempty"`
	Contains   string    `toml:"contains,omitempty" json:"contains,omitempty"`
	PackageDir string    `toml:"package_dir,omitempty" json:"package_dir,omitempty"`
	Scratch    string    `toml:"scratch,omitempty" json:"scratch,omitempty"`
	Assets     []string  `toml:"assets,omitempty" json:"assets,omitempty"`
	Argv       []string  `toml:"argv,omitempty" json:"argv,omitempty"`
}

// Budget declares the story's hard caps. Declared, not discovered.
type Budget struct {
	MaxTokens           int64   `toml:"max_tokens" json:"max_tokens"`
	MaxWallClockSeconds int64   `toml:"max_wall_clock_seconds" json:"max_wall_clock_seconds"`
	MaxCostUSD          float64 `toml:"max_cost_usd" json:"max_cost_usd"`
}

// Rubric is an optional scored rubric — recorded separately, never gating
// pass/fail in Phase 1 (ADR 0025).
type Rubric struct {
	Name     string `toml:"name" json:"name"`
	Criteria string `toml:"criteria" json:"criteria"`
	Version  string `toml:"version" json:"version"`
}

// Validate checks the definition against the schema rules.
func (d *Definition) Validate() error {
	if d.SchemaVersion < SchemaV1 || d.SchemaVersion > MaxSchemaVersion {
		return fmt.Errorf("schema_version %d: this runner knows versions %d–%d", d.SchemaVersion, SchemaV1, MaxSchemaVersion)
	}
	if !kebabCase(d.ID) {
		return fmt.Errorf("id %q must be non-empty kebab-case", d.ID)
	}
	if d.Title == "" {
		return fmt.Errorf("title is required")
	}
	if d.Level != LevelFeature && d.Level != LevelEpic && d.Level != LevelStory {
		return fmt.Errorf("level %q must be one of feature, epic, story", d.Level)
	}
	if err := d.Fixture.validate(); err != nil {
		return fmt.Errorf("fixture: %w", err)
	}
	if d.Prompt.Text == "" {
		return fmt.Errorf("prompt text is required")
	}
	if err := d.validateValidators(); err != nil {
		return err
	}
	if err := d.validateChecks(); err != nil {
		return err
	}
	if err := d.Budget.validate(); err != nil {
		return fmt.Errorf("budget: %w", err)
	}
	return d.validateRubrics()
}

func (d *Definition) validateRubrics() error {
	for i := range d.Rubrics {
		r := &d.Rubrics[i]
		if r.Name == "" || r.Criteria == "" || r.Version == "" {
			return fmt.Errorf("rubric %d: name, criteria, and version are required", i)
		}
	}
	return nil
}

func (d *Definition) validateValidators() error {
	if len(d.Validators) == 0 {
		return fmt.Errorf("at least one validator is required")
	}
	for i := range d.Validators {
		if d.Validators[i].Name == "" || d.Validators[i].Command == "" {
			return fmt.Errorf("validator %d: name and command are required", i)
		}
	}
	return nil
}

func (d *Definition) validateChecks() error {
	if len(d.Checks) == 0 {
		return fmt.Errorf("at least one check is required")
	}
	for i := range d.Checks {
		check := &d.Checks[i]
		if err := check.validate(); err != nil {
			return fmt.Errorf("check %d (%s): %w", i, check.Name, err)
		}
		if check.Type == CheckFilesChangedWithin && len(d.Expectations.AllowedPaths) == 0 {
			return fmt.Errorf("check %d (%s): files_changed_within requires expectations.allowed_paths", i, check.Name)
		}
		if check.Type == CheckOracle && d.SchemaVersion < SchemaV2 {
			return fmt.Errorf("check %d (%s): oracle checks require schema_version >= %d", i, check.Name, SchemaV2)
		}
	}
	return nil
}

func (c *Check) validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	switch c.Type {
	case CheckCommand:
		if c.Command == "" {
			return fmt.Errorf("command checks require a command")
		}
	case CheckFilesChangedWithin:
		// Consumes expectations.allowed_paths; no fields of its own.
	case CheckFileContains:
		if c.Path == "" || c.Contains == "" {
			return fmt.Errorf("file_contains checks require path and contains")
		}
	case CheckOracle:
		return c.validateOracle()
	default:
		return fmt.Errorf("unknown check type %q", c.Type)
	}
	return nil
}

// validateOracle enforces the oracle check's shape and the load-time half of
// its path safety. The materialise-time half (symlink components in the
// agent-controlled solution) lives in the engine. See design_oracles.md.
func (c *Check) validateOracle() error {
	if len(c.Assets) == 0 {
		return fmt.Errorf("oracle checks require at least one asset")
	}
	if len(c.Argv) == 0 {
		return fmt.Errorf("oracle checks require a non-empty argv")
	}
	if c.Command != "" {
		return fmt.Errorf("oracle checks use argv, not command")
	}
	if c.Scratch != "" && c.Scratch != OracleScratchSolutionCommit {
		return fmt.Errorf("oracle scratch %q must be %q or empty", c.Scratch, OracleScratchSolutionCommit)
	}
	if err := safeRelPath(c.PackageDir, true); err != nil {
		return fmt.Errorf("package_dir: %w", err)
	}
	seen := make(map[string]bool, len(c.Assets))
	for _, a := range c.Assets {
		base := filepath.Base(a)
		// Assets are single-component basenames: no directory part, so the
		// source→destination mapping is verbatim and there is no flattening.
		if a != base {
			return fmt.Errorf("oracle asset %q must be a bare basename, not a path", a)
		}
		if err := safeRelPath(a, false); err != nil {
			return fmt.Errorf("oracle asset %q: %w", a, err)
		}
		if !strings.HasPrefix(base, OracleAssetPrefix) {
			return fmt.Errorf("oracle asset %q must be in the %q namespace", a, OracleAssetPrefix)
		}
		// Duplicate destinations: two assets landing on the same basename.
		if seen[base] {
			return fmt.Errorf("oracle asset %q duplicates a destination", a)
		}
		seen[base] = true
	}
	return nil
}

// safeRelPath rejects absolute paths and any component that escapes upward.
// allowEmpty permits "" (used by package_dir to mean the repo root). It is a
// pure lexical check; symlink and existence checks happen where the bytes are
// read (load, for assets) or materialised (engine, for the solution tree).
func safeRelPath(p string, allowEmpty bool) error {
	if p == "" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("path %q must be relative", p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes its directory", p)
	}
	return nil
}

func (f *Fixture) validate() error {
	if f.Repo == "" {
		return fmt.Errorf("repo is required")
	}
	if !commitPattern.MatchString(f.Commit) {
		return fmt.Errorf("commit %q must be a full 40-hex pinned commit", f.Commit)
	}
	if f.BaseBranch == "" {
		return fmt.Errorf("base_branch is required")
	}
	return nil
}

func (b *Budget) validate() error {
	// TOML admits nan and inf literals; a NaN cap slides through ordered
	// comparisons and would defeat hard-budget enforcement.
	if math.IsNaN(b.MaxCostUSD) || math.IsInf(b.MaxCostUSD, 0) {
		return fmt.Errorf("max_cost_usd must be finite, got %v", b.MaxCostUSD)
	}
	if b.MaxTokens <= 0 || b.MaxWallClockSeconds <= 0 || b.MaxCostUSD <= 0 {
		return fmt.Errorf("max_tokens, max_wall_clock_seconds, and max_cost_usd must all be positive: budgets are declared, not discovered")
	}
	return nil
}

// kebabCase reports whether s is non-empty lowercase kebab-case.
func kebabCase(s string) bool {
	return kebabPattern.MatchString(s)
}
