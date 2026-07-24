package story_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

const validPath = "testdata/valid.toml"

func validTOML(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(raw)
}

// writeStory writes content as a story file in a temp dir and returns its path.
func writeStory(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write story: %v", err)
	}
	return path
}

func TestLoadFileValid(t *testing.T) {
	loaded, err := story.LoadFile(validPath)
	if err != nil {
		t.Fatalf("load valid story: %v", err)
	}
	def := loaded.Definition
	if def.ID != "dep-bump-001" || def.Level != story.LevelStory {
		t.Fatalf("unexpected definition: %+v", def)
	}
	if len(def.Validators) != 2 || len(def.Checks) != 2 || len(def.Rubrics) != 1 {
		t.Fatalf("sections not fully decoded: %+v", def)
	}
	if !strings.HasPrefix(loaded.Hash, "sha256:") {
		t.Fatalf("hash %q must carry the algorithm prefix", loaded.Hash)
	}
}

func TestHashIgnoresFormatting(t *testing.T) {
	base, err := story.LoadFile(validPath)
	if err != nil {
		t.Fatalf("load valid story: %v", err)
	}
	reformatted := "# a leading comment changes bytes, not content\n" +
		strings.Replace(validTOML(t), "max_cost_usd = 5.0", "max_cost_usd = 5.00", 1)
	other, err := story.LoadFile(writeStory(t, "reformatted.toml", reformatted))
	if err != nil {
		t.Fatalf("load reformatted story: %v", err)
	}
	if base.Hash != other.Hash {
		t.Fatalf("formatting must not change identity: %q vs %q", base.Hash, other.Hash)
	}
}

func TestHashTracksContent(t *testing.T) {
	base, err := story.LoadFile(validPath)
	if err != nil {
		t.Fatalf("load valid story: %v", err)
	}
	edited := strings.Replace(validTOML(t), "max_tokens = 200000", "max_tokens = 100000", 1)
	other, err := story.LoadFile(writeStory(t, "edited.toml", edited))
	if err != nil {
		t.Fatalf("load edited story: %v", err)
	}
	if base.Hash == other.Hash {
		t.Fatalf("a content edit must change identity")
	}
}

func TestLoadFileRejections(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(string) string
		wantErr string
	}{
		{"unknown key", func(s string) string { return s + "\nsurprise = true\n" }, "unknown keys"},
		{"future schema version", func(s string) string {
			return strings.Replace(s, "schema_version = 1", "schema_version = 3", 1)
		}, "schema_version"},
		{"uppercase id", func(s string) string {
			return strings.Replace(s, `id = "dep-bump-001"`, `id = "Dep-Bump"`, 1)
		}, "kebab-case"},
		{"bad level", func(s string) string {
			return strings.Replace(s, `level = "story"`, `level = "saga"`, 1)
		}, "level"},
		{"short commit", func(s string) string {
			return strings.Replace(s, `commit = "0123456789abcdef0123456789abcdef01234567"`, `commit = "abc123"`, 1)
		}, "40-hex"},
		{"no validators", func(s string) string {
			block := "[[validators]]\nname = \"build\"\ncommand = \"go build ./...\"\n\n[[validators]]\nname = \"test\"\ncommand = \"go test ./...\"\n"
			return strings.Replace(s, block, "", 1)
		}, "validator"},
		{"zero budget", func(s string) string {
			return strings.Replace(s, "max_tokens = 200000", "max_tokens = 0", 1)
		}, "budget"},
		{"nan budget", func(s string) string {
			return strings.Replace(s, "max_cost_usd = 5.0", "max_cost_usd = nan", 1)
		}, "finite"},
		{"files_changed_within without allowed_paths", func(s string) string {
			return strings.Replace(s, `allowed_paths = ["go.mod", "go.sum"]`, "", 1)
		}, "allowed_paths"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := story.LoadFile(writeStory(t, "story.toml", tc.mutate(validTOML(t))))
			if err == nil {
				t.Fatalf("expected load error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadDirSortsAndRejectsDuplicates(t *testing.T) {
	dir := t.TempDir()
	first := strings.Replace(validTOML(t), `id = "dep-bump-001"`, `id = "zz-last"`, 1)
	second := strings.Replace(validTOML(t), `id = "dep-bump-001"`, `id = "aa-first"`, 1)
	for name, content := range map[string]string{"b.toml": first, "a.toml": second} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	loaded, err := story.LoadDir(dir)
	if err != nil {
		t.Fatalf("load dir: %v", err)
	}
	if len(loaded) != 2 || loaded[0].Definition.ID != "aa-first" || loaded[1].Definition.ID != "zz-last" {
		t.Fatalf("expected sorted IDs, got %+v", loaded)
	}

	if err := os.WriteFile(filepath.Join(dir, "dup.toml"), []byte(first), 0o644); err != nil {
		t.Fatalf("write dup: %v", err)
	}
	if _, err := story.LoadDir(dir); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate IDs must fail, got %v", err)
	}
}
