// Package mph defines MPH (Model/Prompt/Harness) configuration bundles: the
// authored TOML schema, loader, validation, and content-hash identity
// (ADR 0025).
//
// A bundle is one benchmark configuration. Its identity derives from
// canonical content, never from file location or formatting, so results
// remain comparable when Phase 2 moves bundles into the data plane.
package mph

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/SnapdragonPartners/maestro/benchmark/internal/contenthash"
)

// SchemaVersion is the current bundle schema version. v2 (item 5.1) adds the
// `local` flag and the token-budget dimension for locally-hosted models.
const SchemaVersion = 2

//nolint:gochecknoglobals // Package-level compiled regex for performance.
var kebabPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Bundle is one MPH configuration.
type Bundle struct {
	Model         ModelRouting    `toml:"model" json:"model"`
	Harness       HarnessSettings `toml:"harness" json:"harness"`
	Prompt        PromptRef       `toml:"prompt" json:"prompt"`
	Name          string          `toml:"name" json:"name"`
	Description   string          `toml:"description" json:"description"`
	Budget        DeclaredBudget  `toml:"budget" json:"budget"`
	SchemaVersion int             `toml:"schema_version" json:"schema_version"`
	// Local selects the token budget dimension: the config's models run on a
	// zero-dollar local provider (Ollama), so cost is unmodeled (reported
	// `unavailable`) and the config is budgeted on tokens + wall-clock with
	// no USD reservation. false (default) is the hosted, USD-budgeted path.
	Local bool `toml:"local,omitempty" json:"local,omitempty"`
}

// ModelRouting is the M component: a default model plus per-role overrides.
// Reviewer heterogeneity (ADR 0020) is expressed as a roles entry.
type ModelRouting struct {
	Roles   map[string]string `toml:"roles,omitempty" json:"roles,omitempty"`
	Default string            `toml:"default" json:"default"`
}

// PromptRef is the P component: a pack label plus content hash. Hash may be
// omitted for embedded-prompt targets — the adapter computes it from actual
// prompt content and records it in the MPH identity; a declared hash wins.
type PromptRef struct {
	Pack string `toml:"pack" json:"pack"`
	Hash string `toml:"hash,omitempty" json:"hash,omitempty"`
}

// HarnessSettings is the H component. Adapter selection is a harness lever;
// Settings are adapter-interpreted — the runner never reads them (black-box).
type HarnessSettings struct {
	Settings map[string]string `toml:"settings,omitempty" json:"settings,omitempty"`
	Adapter  string            `toml:"adapter" json:"adapter"`
}

// DeclaredBudget carries the configuration's declared expectations and hard
// caps (the D9 mechanism). Budgets are declared, not discovered.
type DeclaredBudget struct {
	ExpectedTokensPerRun  int64   `toml:"expected_tokens_per_run" json:"expected_tokens_per_run"`
	ExpectedCostUSDPerRun float64 `toml:"expected_cost_usd_per_run" json:"expected_cost_usd_per_run"`
	MaxCostUSDPerRun      float64 `toml:"max_cost_usd_per_run" json:"max_cost_usd_per_run"`
	MaxCostUSDPerSuite    float64 `toml:"max_cost_usd_per_suite" json:"max_cost_usd_per_suite"`
	// Token caps (item 5.1): the budget dimension for local configs. Required
	// and positive when the bundle is local; must be zero otherwise.
	MaxTokensPerRun   int64 `toml:"max_tokens_per_run,omitempty" json:"max_tokens_per_run,omitempty"`
	MaxTokensPerSuite int64 `toml:"max_tokens_per_suite,omitempty" json:"max_tokens_per_suite,omitempty"`
}

// Loaded pairs a validated bundle with its content identity and origin.
type Loaded struct {
	Bundle *Bundle
	Path   string
	// Hash is the "sha256:" identity of the canonical serialization of the
	// validated bundle.
	Hash string
}

// Validate checks the bundle against the schema rules.
func (b *Bundle) Validate() error {
	if b.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version %d: this runner knows only version %d", b.SchemaVersion, SchemaVersion)
	}
	if !kebabPattern.MatchString(b.Name) {
		return fmt.Errorf("name %q must be non-empty kebab-case", b.Name)
	}
	if b.Model.Default == "" {
		return fmt.Errorf("model.default is required")
	}
	for role, model := range b.Model.Roles {
		if role == "" || model == "" {
			return fmt.Errorf("model.roles entries require both role and model")
		}
	}
	if b.Prompt.Pack == "" {
		return fmt.Errorf("prompt.pack is required")
	}
	if b.Prompt.Hash != "" && !contenthash.Valid(b.Prompt.Hash) {
		return fmt.Errorf("prompt.hash must be a complete %q content identity when declared, got %q", contenthash.Prefix, b.Prompt.Hash)
	}
	if b.Harness.Adapter == "" {
		return fmt.Errorf("harness.adapter is required")
	}
	return b.Budget.validate(b.Local)
}

// validate is dimension-keyed (item 5.1): a hosted config is budgeted in USD
// and must declare positive USD caps with zero token caps; a local config is
// budgeted in tokens and must declare positive token caps with zero USD caps
// (a positive USD cap on a local bundle is ambiguous and rejected). Every
// config declares expected_tokens_per_run — for local it is the token
// dimension's estimate, for hosted it is reporting-only.
func (d *DeclaredBudget) validate(local bool) error {
	// TOML admits nan and inf literals, and NaN slides through ordered
	// comparisons — a NaN cap would defeat hard-budget enforcement.
	for _, v := range []float64{d.ExpectedCostUSDPerRun, d.MaxCostUSDPerRun, d.MaxCostUSDPerSuite} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("budget values must be finite, got %v", v)
		}
	}
	if d.ExpectedTokensPerRun <= 0 {
		return fmt.Errorf("expected_tokens_per_run must be positive: budgets are declared, not discovered")
	}
	if local {
		return d.validateTokenDimension()
	}
	return d.validateUSDDimension()
}

func (d *DeclaredBudget) validateTokenDimension() error {
	if d.ExpectedCostUSDPerRun != 0 || d.MaxCostUSDPerRun != 0 || d.MaxCostUSDPerSuite != 0 {
		return fmt.Errorf("local config must not declare USD caps (cost is unmodeled); budget on tokens")
	}
	if d.MaxTokensPerRun <= 0 || d.MaxTokensPerSuite <= 0 {
		return fmt.Errorf("local config must declare positive max_tokens_per_run and max_tokens_per_suite")
	}
	if d.MaxTokensPerRun < d.ExpectedTokensPerRun {
		return fmt.Errorf("max_tokens_per_run %d must be at least expected_tokens_per_run %d", d.MaxTokensPerRun, d.ExpectedTokensPerRun)
	}
	if d.MaxTokensPerSuite < d.MaxTokensPerRun {
		return fmt.Errorf("max_tokens_per_suite %d must be at least max_tokens_per_run %d", d.MaxTokensPerSuite, d.MaxTokensPerRun)
	}
	return nil
}

func (d *DeclaredBudget) validateUSDDimension() error {
	if d.MaxTokensPerRun != 0 || d.MaxTokensPerSuite != 0 {
		return fmt.Errorf("hosted config must not declare token caps; budget on USD (or set local = true)")
	}
	if d.ExpectedCostUSDPerRun <= 0 {
		return fmt.Errorf("expected_cost_usd_per_run must be positive: budgets are declared, not discovered")
	}
	if d.MaxCostUSDPerRun < d.ExpectedCostUSDPerRun {
		return fmt.Errorf("max_cost_usd_per_run %v must be at least expected_cost_usd_per_run %v", d.MaxCostUSDPerRun, d.ExpectedCostUSDPerRun)
	}
	if d.MaxCostUSDPerSuite < d.MaxCostUSDPerRun {
		return fmt.Errorf("max_cost_usd_per_suite %v must be at least max_cost_usd_per_run %v", d.MaxCostUSDPerSuite, d.MaxCostUSDPerRun)
	}
	return nil
}

// LoadFile reads, strictly decodes, and validates one bundle. Unknown keys
// are rejected so typos fail at load time.
func LoadFile(path string) (*Loaded, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bundle %s: %w", path, err)
	}
	var bundle Bundle
	meta, err := toml.Decode(string(raw), &bundle)
	if err != nil {
		return nil, fmt.Errorf("decode bundle %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		return nil, fmt.Errorf("bundle %s: unknown keys %v", path, keys)
	}
	if validateErr := bundle.Validate(); validateErr != nil {
		return nil, fmt.Errorf("bundle %s: %w", path, validateErr)
	}
	hash, hashErr := contenthash.CanonicalJSON(&bundle)
	if hashErr != nil {
		return nil, fmt.Errorf("bundle %s: %w", path, hashErr)
	}
	return &Loaded{Bundle: &bundle, Path: path, Hash: hash}, nil
}

// LoadDir loads every .toml bundle in dir (non-recursive), enforces unique
// bundle names, and returns them sorted by name.
func LoadDir(dir string) ([]*Loaded, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read bundle dir %s: %w", dir, err)
	}
	loaded := make([]*Loaded, 0, len(entries))
	byName := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		one, err := LoadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if prior, dup := byName[one.Bundle.Name]; dup {
			return nil, fmt.Errorf("duplicate bundle name %q in %s and %s", one.Bundle.Name, prior, one.Path)
		}
		byName[one.Bundle.Name] = one.Path
		loaded = append(loaded, one)
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].Bundle.Name < loaded[j].Bundle.Name })
	return loaded, nil
}
