// Package stories_test validates every authored golden story definition in
// this directory: strict schema, unique IDs, and the Phase 1 constraint
// that stories are Story-scoped (ADR 0025).
package stories_test

import (
	"bytes"
	"go/format"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

func TestAuthoredStoriesAreValid(t *testing.T) {
	loaded, err := story.LoadDir(".")
	if err != nil {
		t.Fatalf("stories directory must load cleanly: %v", err)
	}
	if len(loaded) == 0 {
		t.Fatalf("no story definitions found")
	}
	for _, one := range loaded {
		if one.Definition.Level != story.LevelStory {
			t.Errorf("%s: Phase 1 stories are single-repo and Story-scoped, got level %q", one.Definition.ID, one.Definition.Level)
		}
	}
}

// TestOracleAssetsAreWellFormedGo guards the oracle assets that normal Go
// tooling never sees: they live under stories/_oracles/, whose leading
// underscore makes `go build`/`vet`/`test`/lint skip the directory entirely, so
// a syntactically broken or unformatted asset would otherwise ship green. This
// parses every .go asset retained in Loaded.OracleAssets and requires it to be
// gofmt-clean (format.Source succeeds and is a no-op). It is a lightweight
// syntactic guard only; semantic behaviour is proven by `runner verify`.
func TestOracleAssetsAreWellFormedGo(t *testing.T) {
	loaded, err := story.LoadDir(".")
	if err != nil {
		t.Fatalf("stories directory must load cleanly: %v", err)
	}
	for _, one := range loaded {
		for name, content := range one.OracleAssets {
			if !strings.HasSuffix(name, ".go") {
				continue
			}
			formatted, ferr := format.Source(content)
			if ferr != nil {
				t.Errorf("%s: oracle asset %q does not parse: %v", one.Definition.ID, name, ferr)
				continue
			}
			if !bytes.Equal(formatted, content) {
				t.Errorf("%s: oracle asset %q is not gofmt-clean (run gofmt -w)", one.Definition.ID, name)
			}
		}
	}
}
