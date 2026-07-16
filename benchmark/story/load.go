package story

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/SnapdragonPartners/maestro/benchmark/internal/contenthash"
)

// Loaded pairs a validated definition with its content identity and origin.
type Loaded struct {
	Definition *Definition
	Path       string
	// Hash is the "sha256:" identity of the canonical serialization of the
	// validated definition — formatting and comments are not identity.
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
	hash, hashErr := contenthash.CanonicalJSON(&def)
	if hashErr != nil {
		return nil, fmt.Errorf("story %s: %w", path, hashErr)
	}
	return &Loaded{Definition: &def, Path: path, Hash: hash}, nil
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
