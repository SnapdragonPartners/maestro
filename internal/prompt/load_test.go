package prompt

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

// The loader (design D2). A pack on a file system, read into the shape the
// seam installs. The fixtures here are the layout the production embed uses,
// so what these tests prove about fstest.MapFS is what holds of embed.FS.

const fixtureManifest = `{
  "display_name": "fixture",
  "maestro_version": {"min": "v2.0.0-phase.3.0.0", "max": "v2.0.0-phase.4.0.0"},
  "roles": ["coder"]
}`

// fixturePackFS is the positive control: a two-slot pack declaring one role.
func fixturePackFS() fstest.MapFS {
	return fstest.MapFS{
		ManifestFile:                {Data: []byte(fixtureManifest)},
		"entries/coder.system.tmpl": {Data: []byte("You work in {{.Workspace}}.\n")},
		"entries/" + string(slotCoderPlan) + EntryExtension: {Data: []byte("Plan {{.Story}}.")},
	}
}

func TestLoadReadsTheLayout(t *testing.T) {
	loaded, err := Load(fixturePackFS())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	switch {
	case loaded.DisplayName != "fixture":
		t.Errorf("display name %q", loaded.DisplayName)
	case loaded.MinMaestroVersion != "v2.0.0-phase.3.0.0" || loaded.MaxMaestroVersion != "v2.0.0-phase.4.0.0":
		t.Errorf("range [%s, %s)", loaded.MinMaestroVersion, loaded.MaxMaestroVersion)
	case strings.Join(loaded.Roles, ",") != "coder":
		t.Errorf("roles %v", loaded.Roles)
	case len(loaded.Entries) != 2:
		t.Errorf("entries %v", loaded.Entries)
	case loaded.Entries[string(slotCoderSystem)] != "You work in {{.Workspace}}.\n":
		t.Errorf("coder.system = %q: the file's bytes are the entry, unaltered", loaded.Entries[string(slotCoderSystem)])
	case loaded.Entries[string(slotCoderPlan)] != "Plan {{.Story}}.":
		t.Errorf("coder.plan = %q", loaded.Entries[string(slotCoderPlan)])
	}

	// The loaded pack passes the gate a registry with these slots applies,
	// which is what the seam will do with it -- and the gate is not the
	// loader's, so this is the only place the two are run in sequence here.
	if err := fixtureRegistry(t).ValidatePack(loaded.Entries, loaded.Roles); err != nil {
		t.Fatalf("the loaded fixture fails the gate: %v", err)
	}
}

// TestLoadReadsAnEmptyPack is the built-in's shape at item 4: a manifest, no
// entries directory, no roles. It loads to zero entries -- one identity,
// design D1 -- and passes an empty registry's gate vacuously.
func TestLoadReadsAnEmptyPack(t *testing.T) {
	loaded, err := Load(fstest.MapFS{
		ManifestFile: {Data: []byte(`{"display_name": "built-in", "maestro_version": {"min": "v2.0.0-phase.3.0.0", "max": "v2.0.0-phase.4.0.0"}, "roles": []}`)},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Entries == nil || len(loaded.Entries) != 0 || len(loaded.Roles) != 0 {
		t.Fatalf("empty pack loaded as %+v; entries must be an empty, non-nil map so the digest is {}'s", loaded)
	}
	if err := MustNew(nil).ValidatePack(loaded.Entries, loaded.Roles); err != nil {
		t.Fatalf("the empty pack fails the empty registry's gate: %v", err)
	}
}

func TestLoadRefusesWhatTheLayoutDoesNotName(t *testing.T) {
	tests := []struct {
		name    string
		alter   func(fsys fstest.MapFS)
		want    error
		wantMsg string
	}{
		{"no manifest", func(f fstest.MapFS) { delete(f, ManifestFile) }, ErrLayout, "read pack.json"},
		{"manifest is not JSON", func(f fstest.MapFS) { f[ManifestFile] = &fstest.MapFile{Data: []byte("display_name: x")} }, ErrLayout, "decode pack.json"},
		{"unknown manifest field", func(f fstest.MapFS) {
			f[ManifestFile] = &fstest.MapFile{Data: []byte(`{"display_name":"x","maestro_version":{"min":"v1","max":"v2"},"roles":[],"name":"x"}`)}
		}, ErrLayout, `unknown field "name"`},
		{"trailing content", func(f fstest.MapFS) {
			f[ManifestFile] = &fstest.MapFile{Data: []byte(fixtureManifest + "\n{}")}
		}, ErrLayout, "trailing content"},
		// Unmatched closers: Decoder.More answers false at these, so a check
		// built on it admits them (review round 6, P2).
		{"trailing unmatched brace", func(f fstest.MapFS) {
			f[ManifestFile] = &fstest.MapFile{Data: []byte(fixtureManifest + "}")}
		}, ErrLayout, "trailing content"},
		{"trailing unmatched bracket and text", func(f fstest.MapFS) {
			f[ManifestFile] = &fstest.MapFile{Data: []byte(fixtureManifest + "] ignored text")}
		}, ErrLayout, "trailing content"},
		{"blank display name", func(f fstest.MapFS) {
			f[ManifestFile] = &fstest.MapFile{Data: []byte(`{"display_name":" ","maestro_version":{"min":"v1","max":"v2"},"roles":[]}`)}
		}, ErrLayout, "no display_name"},
		{"missing range", func(f fstest.MapFS) {
			f[ManifestFile] = &fstest.MapFile{Data: []byte(`{"display_name":"x","maestro_version":{"min":"v1"},"roles":[]}`)}
		}, ErrLayout, "maestro_version.min and maestro_version.max"},
		{"non-canonical role", func(f fstest.MapFS) {
			f[ManifestFile] = &fstest.MapFile{Data: []byte(`{"display_name":"x","maestro_version":{"min":"v1","max":"v2"},"roles":["Coder"]}`)}
		}, ErrLayout, `role "Coder" is not a canonical role name`},
		{"repeated role", func(f fstest.MapFS) {
			f[ManifestFile] = &fstest.MapFile{Data: []byte(`{"display_name":"x","maestro_version":{"min":"v1","max":"v2"},"roles":["coder","coder"]}`)}
		}, ErrLayout, `role "coder" is listed twice`},
		{"entry without the extension", func(f fstest.MapFS) {
			f["entries/coder.system"] = &fstest.MapFile{Data: []byte("x")}
		}, ErrLayout, "entries/coder.system is not <slot>.tmpl"},
		{"entry for a non-canonical slot", func(f fstest.MapFS) {
			f["entries/Coder.System.tmpl"] = &fstest.MapFile{Data: []byte("x")}
		}, ErrLayout, "entries/Coder.System.tmpl is not <slot>.tmpl"},
		{"nested directory under entries", func(f fstest.MapFS) {
			f["entries/coder/system.tmpl"] = &fstest.MapFile{Data: []byte("x")}
		}, ErrLayout, "entries/coder is a directory"},
		{"blank entry", func(f fstest.MapFS) {
			f["entries/coder.system.tmpl"] = &fstest.MapFile{Data: []byte("  \n")}
		}, ErrInvalidEntry, `slot "coder.system" is blank`},
		{"invalid UTF-8 entry", func(f fstest.MapFS) {
			f["entries/coder.system.tmpl"] = &fstest.MapFile{Data: []byte("abc\xff")}
		}, ErrInvalidEntry, "not valid UTF-8"},
		{"NUL in entry", func(f fstest.MapFS) {
			f["entries/coder.system.tmpl"] = &fstest.MapFile{Data: []byte("abc\x00")}
		}, ErrInvalidEntry, "NUL"},
		{"stray file at the root", func(f fstest.MapFS) {
			f["README.md"] = &fstest.MapFile{Data: []byte("x")}
		}, ErrLayout, `"README.md" is not part of a pack`},
		{"stray directory at the root", func(f fstest.MapFS) {
			f["slots/coder.system.tmpl"] = &fstest.MapFile{Data: []byte("x")}
		}, ErrLayout, `"slots" is not part of a pack`},
		{"manifest as a directory", func(f fstest.MapFS) {
			delete(f, ManifestFile)
			f[ManifestFile+"/x"] = &fstest.MapFile{Data: []byte("x")}
		}, ErrLayout, "read pack.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fixturePackFS()
			tt.alter(fsys)
			_, err := Load(fsys)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("err = %q, want it to mention %q", err, tt.wantMsg)
			}
		})
	}
	// The positive control, so a refusal above is not an artefact of the
	// fixture itself.
	if _, err := Load(fixturePackFS()); err != nil {
		t.Fatalf("the unaltered fixture failed to load: %v", err)
	}
}

func TestLoadRefusesANilFileSystem(t *testing.T) {
	if _, err := Load(nil); !errors.Is(err, ErrLayout) {
		t.Fatalf("err = %v", err)
	}
}
