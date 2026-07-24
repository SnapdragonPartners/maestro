package main

import (
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

// filterStories decides whether a tier runs as ONE correctly-accounted suite:
// golden-minimal passes a comma-separated list precisely so both stories go
// through a single RunSuite. Separate invocations sharing a suite ID would
// each rewrite the manifest and restart budget accounting, so the selection
// contract is load-bearing and gets tested rather than assumed.
func loaded(ids ...string) []*story.Loaded {
	out := make([]*story.Loaded, 0, len(ids))
	for _, id := range ids {
		out = append(out, &story.Loaded{Definition: &story.Definition{ID: id}})
	}
	return out
}

func names(in []*story.Loaded) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, s.Definition.ID)
	}
	return out
}

func TestFilterStories(t *testing.T) {
	all := loaded("smoke-comment", "dep-bump-xnet", "bugfix-openai-stopreason")

	tests := []struct {
		name    string
		ids     string
		want    []string
		wantErr string
	}{
		{name: "empty selects everything", ids: "",
			want: []string{"smoke-comment", "dep-bump-xnet", "bugfix-openai-stopreason"}},
		{name: "single id", ids: "dep-bump-xnet", want: []string{"dep-bump-xnet"}},
		{name: "comma list — the golden-minimal case", ids: "smoke-comment,dep-bump-xnet",
			want: []string{"smoke-comment", "dep-bump-xnet"}},
		{name: "whitespace around ids is tolerated", ids: " smoke-comment , dep-bump-xnet ",
			want: []string{"smoke-comment", "dep-bump-xnet"}},
		{name: "empty entries from a trailing comma are ignored", ids: "smoke-comment,,",
			want: []string{"smoke-comment"}},
		{name: "selection order follows the loaded order, not the flag",
			ids:  "bugfix-openai-stopreason,smoke-comment",
			want: []string{"smoke-comment", "bugfix-openai-stopreason"}},
		{name: "unknown id is an error, never a silently shorter suite",
			ids: "smoke-comment,not-a-story", wantErr: "not-a-story"},
		{name: "several unknown ids are reported together, sorted for determinism",
			ids: "zzz,aaa", wantErr: "aaa, zzz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := filterStories(all, tt.ids)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error mentioning %q, got none (selected %v)", tt.wantErr, names(got))
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not mention %q", err, tt.wantErr)
				}
				if got != nil {
					t.Errorf("stories returned alongside an error: %v", names(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Join(names(got), ",") != strings.Join(tt.want, ",") {
				t.Errorf("selected %v, want %v", names(got), tt.want)
			}
		})
	}
}

// A silently short suite is the failure mode worth guarding: it would run,
// report, and record fewer stories than the tier claims, with no error.
func TestFilterStoriesNeverSilentlyShortens(t *testing.T) {
	all := loaded("a", "b")
	got, err := filterStories(all, "a,b,c")
	if err == nil {
		t.Fatalf("missing id must error; got %v", names(got))
	}
	if !strings.Contains(err.Error(), "c") {
		t.Errorf("error should name the missing id: %v", err)
	}
}
