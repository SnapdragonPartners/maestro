package story

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/SnapdragonPartners/maestro/benchmark/internal/contenthash"
	"github.com/SnapdragonPartners/maestro/benchmark/internal/safe"
)

// oracleDirName is the directory, alongside the story files, holding each
// story's oracle assets: stories/oracles/<story-id>/.
const oracleDirName = "oracles"

// Loaded pairs a validated definition with its content identity and origin.
type Loaded struct {
	Definition *Definition
	// OracleAssets holds the bytes of every referenced oracle asset, keyed by
	// basename, read ONCE at load. Both hashing and (later) materialisation
	// use this retained set and never re-read the file — the bytes hashed are
	// exactly the bytes materialised.
	OracleAssets map[string][]byte
	Path         string
	// Hash is the "sha256:" identity of the canonical serialization of the
	// validated definition — formatting and comments are not identity. For a
	// v2 story with oracle assets it additionally folds in the assets' sorted
	// {path, sha256(content)} digests, so editing an oracle moves the hash.
	Hash string
}

// LoadFile reads, strictly decodes, and validates one story definition.
// Unknown keys are rejected so typos fail at load time.
func LoadFile(path string) (*Loaded, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read story %s: %w", path, err)
	}
	var def Definition
	meta, err := toml.Decode(string(raw), &def)
	if err != nil {
		return nil, fmt.Errorf("decode story %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		return nil, fmt.Errorf("story %s: unknown keys %v", path, keys)
	}
	if validateErr := def.Validate(); validateErr != nil {
		return nil, fmt.Errorf("story %s: %w", path, validateErr)
	}
	// Value-based validation (above) cannot tell an absent key from one present
	// with a zero value: a v1 command check carrying `argv = []` decodes to a
	// nil slice and slips past hasOracleFields. Enforce field ownership on the
	// keys actually PRESENT in the source, so a forbidden key is rejected even
	// when explicitly empty and v1's strict contract cannot be widened.
	if presenceErr := enforceCheckFieldOwnership(string(raw), &def); presenceErr != nil {
		return nil, fmt.Errorf("story %s: %w", path, presenceErr)
	}

	// Read oracle assets once, here, and retain the bytes. Everything
	// downstream — hashing now, materialisation later — uses this set.
	assets, digests, assetErr := loadOracleAssets(path, &def)
	if assetErr != nil {
		return nil, fmt.Errorf("story %s: %w", path, assetErr)
	}

	hash, hashErr := hashWithOracle(&def, digests)
	if hashErr != nil {
		return nil, fmt.Errorf("story %s: %w", path, hashErr)
	}
	return &Loaded{Definition: &def, Path: path, Hash: hash, OracleAssets: assets}, nil
}

// oracleOnlyKeys are valid only on an oracle check; nonOracleKeys are invalid
// on one. Presence of either on the wrong check widens the strict schema.
//
//nolint:gochecknoglobals // fixed key sets, read-only.
var (
	oracleOnlyKeys = []string{"assets", "argv", "package_dir", "scratch"}
	nonOracleKeys  = []string{"command", "path", "contains"}
)

// enforceCheckFieldOwnership rejects a check that declares a key belonging to a
// different check type, judging by PRESENCE in the source rather than value —
// a second lenient decode exposes which keys were written, which the strict
// struct decode cannot. Correlates raw checks with def.Checks by index; TOML
// preserves array-of-table order, so index i is the same check in both.
func enforceCheckFieldOwnership(rawTOML string, def *Definition) error {
	var tree map[string]any
	if _, err := toml.Decode(rawTOML, &tree); err != nil {
		return fmt.Errorf("re-decode for field ownership: %w", err)
	}
	rawChecks, ok := safe.As[[]map[string]any](tree["checks"])
	if !ok || len(rawChecks) != len(def.Checks) {
		// The lenient decode disagreeing with the strict one — different shape,
		// or a different count — would let ownership gating silently no-op.
		// Fail closed rather than skip enforcement.
		return fmt.Errorf("field-ownership decode mismatch: %d raw checks vs %d parsed", len(rawChecks), len(def.Checks))
	}
	for i := range def.Checks {
		present := rawChecks[i]
		forbidden := oracleOnlyKeys
		kind := "oracle-only"
		if def.Checks[i].Type == CheckOracle {
			forbidden = nonOracleKeys
			kind = "not valid on an oracle"
		}
		for _, k := range forbidden {
			if _, has := present[k]; has {
				return fmt.Errorf("check %d (%s): %q is %s key, remove it (present even if empty)", i, def.Checks[i].Name, k, kind)
			}
		}
	}
	return nil
}

// assetDigest is one oracle asset's identity contribution: its basename and
// the content hash of its retained bytes.
type assetDigest struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
}

// loadOracleAssets reads every oracle asset referenced by the definition from
// stories/oracles/<id>/, retaining the bytes and computing content digests.
// It performs the load-time half of path safety: every path component must be
// a regular directory/file, never a symlink (the agent never controls these,
// but a fixture repo or a stray symlink must not redirect a hashed asset).
// Returns nil maps when the story has no oracle checks — the common v1 case,
// which leaves the hash path untouched.
func loadOracleAssets(storyPath string, def *Definition) (map[string][]byte, []assetDigest, error) {
	var names []string
	for i := range def.Checks {
		if def.Checks[i].Type == CheckOracle {
			names = append(names, def.Checks[i].Assets...)
		}
	}
	if len(names) == 0 {
		return nil, nil, nil
	}
	storyDir := filepath.Dir(storyPath)
	// Check EVERY component from the story dir down — not just the final
	// oracles/<id> — so a symlinked `oracles` or `<id>` directory cannot
	// redirect a hashed asset. The story dir itself is caller-provided and out
	// of scope; everything we resolve below it is ours to constrain.
	if err := lstatComponentsAreDirs(storyDir, oracleDirName, def.ID); err != nil {
		return nil, nil, fmt.Errorf("oracle dir: %w", err)
	}
	oracleDir := filepath.Join(storyDir, oracleDirName, def.ID)

	assets := make(map[string][]byte, len(names))
	for _, name := range names {
		if _, dup := assets[name]; dup {
			continue // same asset referenced by two checks; read once
		}
		full := filepath.Join(oracleDir, name)
		if err := lstatRegularFile(full); err != nil {
			return nil, nil, fmt.Errorf("oracle asset %q: %w", name, err)
		}
		content, err := os.ReadFile(full)
		if err != nil {
			return nil, nil, fmt.Errorf("oracle asset %q: %w", name, err)
		}
		assets[name] = content
	}

	digests := make([]assetDigest, 0, len(assets))
	for name, content := range assets {
		sum := sha256.Sum256(content)
		digests = append(digests, assetDigest{Path: name, Sha256: hex.EncodeToString(sum[:])})
	}
	sort.Slice(digests, func(i, j int) bool { return digests[i].Path < digests[j].Path })
	return assets, digests, nil
}

// hashWithOracle computes the story identity. With no oracle digests it is the
// exact v1 path (contenthash.CanonicalJSON(def)), so every existing story's
// hash is byte-identical. With digests it folds a sorted {path, sha256} list
// under "_oracle_assets" into the canonicalised definition map — decoded with
// UseNumber so the budget int64 precision the canonical hasher preserves is
// not lost on this extra pass.
func hashWithOracle(def *Definition, digests []assetDigest) (string, error) {
	if len(digests) == 0 {
		h, err := contenthash.CanonicalJSON(def)
		return h, wrapHash(err)
	}
	raw, err := json.Marshal(def)
	if err != nil {
		return "", fmt.Errorf("marshal for oracle hash: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if decErr := dec.Decode(&m); decErr != nil {
		return "", fmt.Errorf("decode for oracle hash: %w", decErr)
	}
	m["_oracle_assets"] = digests
	h, hErr := contenthash.CanonicalJSON(m)
	return h, wrapHash(hErr)
}

// wrapHash annotates a canonical-hash error at the story-load boundary.
func wrapHash(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("canonical hash: %w", err)
}

// lstatComponentsAreDirs lstat-checks base/c1, base/c1/c2, … requiring each to
// be a real (non-symlink) directory. base is assumed already trusted.
func lstatComponentsAreDirs(base string, components ...string) error {
	p := base
	for _, c := range components {
		p = filepath.Join(p, c)
		info, err := os.Lstat(p)
		if err != nil {
			return fmt.Errorf("lstat %s: %w", p, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", p)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", p)
		}
	}
	return nil
}

// lstatRegularFile requires path to be a regular file, not a symlink or other
// special file.
func lstatRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("lstat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

// LoadDir loads every .toml story definition in dir (non-recursive),
// enforces unique story IDs, and returns them sorted by ID.
func LoadDir(dir string) ([]*Loaded, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read story dir %s: %w", dir, err)
	}
	loaded := make([]*Loaded, 0, len(entries))
	byID := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		one, err := LoadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if prior, dup := byID[one.Definition.ID]; dup {
			return nil, fmt.Errorf("duplicate story id %q in %s and %s", one.Definition.ID, prior, one.Path)
		}
		byID[one.Definition.ID] = one.Path
		loaded = append(loaded, one)
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].Definition.ID < loaded[j].Definition.ID })
	return loaded, nil
}
