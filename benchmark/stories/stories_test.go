// Package stories_test validates every authored golden story definition in
// this directory: strict schema, unique IDs, and the Phase 1 constraint
// that stories are Story-scoped (ADR 0025).
package stories_test

import (
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
