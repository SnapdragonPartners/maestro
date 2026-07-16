// Package story defines golden story definitions: the authored TOML schema,
// its loader, validation, and content identity (ADR 0025).
//
// A golden story is a declarative, versioned fixture referencing a pinned
// external fixture repository. Authored files are TOML; identity and
// everything the runner emits are JSON (Phase 1 plan, ratified decisions).
package story

import (
	"fmt"
	"regexp"
)

// SchemaVersion is the current story definition schema. Check types and
// section shapes change only with a version bump.
const SchemaVersion = 1

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
type Check struct {
	Name string    `toml:"name" json:"name"`
	Type CheckType `toml:"type" json:"type"`
	// Command is required for type "command".
	Command string `toml:"command,omitempty" json:"command,omitempty"`
	// Path and Contains are required for type "file_contains".
	Path     string `toml:"path,omitempty" json:"path,omitempty"`
	Contains string `toml:"contains,omitempty" json:"contains,omitempty"`
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
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version %d: this runner knows only version %d", d.SchemaVersion, SchemaVersion)
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
	default:
		return fmt.Errorf("unknown check type %q", c.Type)
	}
	return nil
}

func (f *Fixture) validate() error {
	if f.Repo == "" {
		return fmt.Errorf("repo is required")
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(f.Commit) {
		return fmt.Errorf("commit %q must be a full 40-hex pinned commit", f.Commit)
	}
	if f.BaseBranch == "" {
		return fmt.Errorf("base_branch is required")
	}
	return nil
}

func (b *Budget) validate() error {
	if b.MaxTokens <= 0 || b.MaxWallClockSeconds <= 0 || b.MaxCostUSD <= 0 {
		return fmt.Errorf("max_tokens, max_wall_clock_seconds, and max_cost_usd must all be positive: budgets are declared, not discovered")
	}
	return nil
}

// kebabCase reports whether s is non-empty lowercase kebab-case.
func kebabCase(s string) bool {
	return regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(s)
}
