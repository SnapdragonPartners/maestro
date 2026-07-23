package story_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

// TestV1HashesArePinned is the regression guard the oracle work is gated on:
// introducing schema v2 and the oracle check type must not move any existing
// v1 story's content identity, or the recorded v1 baseline
// (docs/v2/notes_conformance-log.md) silently stops being comparable. These
// full hashes were captured before the schema-v2 change; if any moves, this
// fails loudly and names the story.
func TestV1HashesArePinned(t *testing.T) {
	pinned := map[string]string{
		"smoke-comment":            "sha256:75495b46c1a24e2340a2deb3c0bc5128fccd2d2e426a386e77b2af08142823f7",
		"dep-bump-xnet":            "sha256:6b5141b820bb2ca6facd016296b2ecd2767c345e6e62610b2e5653df521e1e0a",
		"bugfix-openai-stopreason": "sha256:909bf81ad2ac78ab0d3af44d236362714fa6ee061d51d129f27df485491d9858",
	}
	for id, want := range pinned {
		l, err := story.LoadFile(filepath.Join("..", "stories", id+".toml"))
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if l.Hash != want {
			t.Errorf("%s hash moved:\n  have %s\n  want %s\n(schema-v2 work must not change a v1 identity)", id, l.Hash, want)
		}
	}
}

// writeStory materialises a story TOML plus optional oracle assets in a temp
// dir laid out like stories/ (with an oracles/<id>/ subdir), and returns the
// story path.
func writeOracleStory(t *testing.T, id, toml string, assets map[string]string) string {
	t.Helper()
	root := t.TempDir()
	storyPath := filepath.Join(root, id+".toml")
	if err := os.WriteFile(storyPath, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(assets) > 0 {
		dir := filepath.Join(root, "oracles", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range assets {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return storyPath
}

const v2Base = `schema_version = 2
id = "oracle-fixture"
title = "t"
level = "story"
[fixture]
repo = "https://example.invalid/r"
commit = "0123456789012345678901234567890123456789"
base_branch = "main"
[prompt]
text = "do a thing"
[expectations]
allowed_paths = ["x.go"]
required_artifacts = ["pr"]
evidence_shape = ["diff"]
[[validators]]
name = "build"
command = "go build ./..."
[budget]
max_tokens = 1000000
max_wall_clock_seconds = 600
max_cost_usd = 5.0
`

// A valid v2 oracle check.
const oracleCheck = `
[[checks]]
name = "oracle-x"
type = "oracle"
assets = ["zz_oracle_x_test.go"]
package_dir = ""
argv = ["go", "test", "."]
`

func TestOracleLoadsAndHashesContent(t *testing.T) {
	p1 := writeOracleStory(t, "oracle-fixture", v2Base+oracleCheck, map[string]string{"zz_oracle_x_test.go": "package main\n"})
	l1, err := story.LoadFile(p1)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := string(l1.OracleAssets["zz_oracle_x_test.go"]); got != "package main\n" {
		t.Errorf("retained bytes = %q", got)
	}

	// Same definition, different oracle CONTENT → different hash. Proves the
	// asset bytes are folded into identity, not just the paths.
	p2 := writeOracleStory(t, "oracle-fixture", v2Base+oracleCheck, map[string]string{"zz_oracle_x_test.go": "package main // changed\n"})
	l2, err := story.LoadFile(p2)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if l1.Hash == l2.Hash {
		t.Error("editing an oracle asset must move the story hash")
	}
}

func TestOracleRejections(t *testing.T) {
	tests := []struct {
		name    string
		check   string
		assets  map[string]string
		wantErr string
	}{
		{
			name:    "oracle under schema v1",
			check:   oracleCheck,
			assets:  map[string]string{"zz_oracle_x_test.go": "package main\n"},
			wantErr: "schema_version",
		},
		{
			name:    "asset outside reserved namespace",
			check:   "\n[[checks]]\nname=\"o\"\ntype=\"oracle\"\nassets=[\"plain_test.go\"]\nargv=[\"go\",\"test\"]\n",
			assets:  map[string]string{"plain_test.go": "package main\n"},
			wantErr: "namespace",
		},
		{
			name:    "asset is a path not a basename",
			check:   "\n[[checks]]\nname=\"o\"\ntype=\"oracle\"\nassets=[\"sub/zz_oracle_x.go\"]\nargv=[\"go\",\"test\"]\n",
			wantErr: "basename",
		},
		{
			name:    "empty argv",
			check:   "\n[[checks]]\nname=\"o\"\ntype=\"oracle\"\nassets=[\"zz_oracle_x_test.go\"]\nargv=[]\n",
			assets:  map[string]string{"zz_oracle_x_test.go": "package main\n"},
			wantErr: "argv",
		},
		{
			name:    "command instead of argv",
			check:   "\n[[checks]]\nname=\"o\"\ntype=\"oracle\"\nassets=[\"zz_oracle_x_test.go\"]\ncommand=\"go test\"\nargv=[\"go\",\"test\"]\n",
			assets:  map[string]string{"zz_oracle_x_test.go": "package main\n"},
			wantErr: "argv, not command",
		},
		{
			name:    "package_dir traversal",
			check:   "\n[[checks]]\nname=\"o\"\ntype=\"oracle\"\nassets=[\"zz_oracle_x_test.go\"]\npackage_dir=\"../escape\"\nargv=[\"go\",\"test\"]\n",
			assets:  map[string]string{"zz_oracle_x_test.go": "package main\n"},
			wantErr: "escapes",
		},
		{
			name:    "unknown scratch mode",
			check:   "\n[[checks]]\nname=\"o\"\ntype=\"oracle\"\nassets=[\"zz_oracle_x_test.go\"]\nscratch=\"nonsense\"\nargv=[\"go\",\"test\"]\n",
			assets:  map[string]string{"zz_oracle_x_test.go": "package main\n"},
			wantErr: "scratch",
		},
		{
			name:    "referenced asset missing on disk",
			check:   oracleCheck,
			assets:  nil, // file not written
			wantErr: "oracle",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := v2Base + tt.check
			if tt.name == "oracle under schema v1" {
				body = "schema_version = 1" + body[len("schema_version = 2"):]
			}
			p := writeOracleStory(t, "oracle-fixture", body, tt.assets)
			_, err := story.LoadFile(p)
			if err == nil {
				t.Fatalf("expected error mentioning %q, got none", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestOracleSymlinkAssetRejected covers the load-time symlink guard, which a
// string-only fixture cannot express.
func TestOracleSymlinkAssetRejected(t *testing.T) {
	root := t.TempDir()
	storyPath := filepath.Join(root, "oracle-fixture.toml")
	if err := os.WriteFile(storyPath, []byte(v2Base+oracleCheck), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "oracles", "oracle-fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The asset is a symlink to an in-tree file — its target is regular, but
	// the asset path itself is not, and hashing a symlink target is exactly
	// the redirection the guard forbids.
	target := filepath.Join(root, "real.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "zz_oracle_x_test.go")); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}
	if _, err := story.LoadFile(storyPath); err == nil {
		t.Fatal("expected a symlink asset to be rejected")
	}
}
