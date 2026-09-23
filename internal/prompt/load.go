package prompt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
)

// The built-in pack loader (design D2).
//
// The binary carries the built-in pack and provisioning imports it
// (ADR 0031 section 6). Load is the one reader of a pack laid out on a file
// system, and it takes fs.FS so production's embed.FS and a test's
// fstest.MapFS travel the identical path -- loader, digest, gate and
// persistence. The built-in ships empty at item 4 (design D1), so if the
// fixtures reached the seam through any other entry point, this path would
// only ever have been exercised over zero entries.
//
// # Layout
//
//	pack.json           the manifest: display name, declared range, roles
//	entries/<slot>.tmpl one file per slot; the file name IS the slot key
//
// Everything else is refused. A file under entries/ that is not a slot entry
// is content nothing will render, which is ValidatePack's first rule one
// level up; a directory the layout does not name is a pack whose author
// meant something the loader cannot see.
//
// Load checks the pack's SHAPE and nothing more: the manifest decodes, the
// range and roles are present and canonical, every entry is admissible text
// and is named for a canonical slot. Whether the slots exist, the roles are
// covered and the entries render is the import gate's question, and the gate
// belongs to the seam (design D3): a loader that answered it too would be a
// second copy of the policy, run against a registry the seam might not hold.

const (
	// ManifestFile is the manifest's path within the pack.
	ManifestFile = "pack.json"

	// EntriesDir holds one file per slot.
	EntriesDir = "entries"

	// EntryExtension is the suffix every entry file carries. The rest of the
	// file name is the slot key, so `entries/coder.system.tmpl` fills
	// `coder.system`.
	EntryExtension = ".tmpl"
)

// ErrLayout reports a pack whose files are not the layout Load reads.
var ErrLayout = errors.New("prompt pack layout is not readable")

// Pack is a loaded, unvalidated built-in pack: what the binary carries,
// before the seam's gate has judged it.
//
// Plain types, like the contract's parameters, because the consumer is the
// composition root handing it to the seam, and the seam names nothing of
// this package.
type Pack struct {
	// Entries maps slot key to entry text.
	Entries map[string]string

	DisplayName       string
	MinMaestroVersion string
	MaxMaestroVersion string

	// Roles is the declared coverage: sorted, de-duplicated.
	Roles []string
}

// manifest is pack.json's wire shape.
type manifest struct {
	DisplayName    string   `json:"display_name"`
	MaestroVersion rangeDoc `json:"maestro_version"`
	Roles          []string `json:"roles"`
}

type rangeDoc struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

// Load reads a pack from fsys.
func Load(fsys fs.FS) (*Pack, error) {
	if fsys == nil {
		return nil, fmt.Errorf("%w: no file system was supplied", ErrLayout)
	}
	loaded, err := loadManifest(fsys)
	if err != nil {
		return nil, err
	}
	entries, err := loadEntries(fsys)
	if err != nil {
		return nil, err
	}
	loaded.Entries = entries
	if err := refuseStrays(fsys); err != nil {
		return nil, err
	}
	return loaded, nil
}

func loadManifest(fsys fs.FS) (*Pack, error) {
	raw, err := fs.ReadFile(fsys, ManifestFile)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %w", ErrLayout, ManifestFile, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	// A field the manifest does not define is a declaration the loader would
	// silently drop, and a dropped declaration is worse than a refused one.
	decoder.DisallowUnknownFields()
	var doc manifest
	if decodeErr := decoder.Decode(&doc); decodeErr != nil {
		return nil, fmt.Errorf("%w: decode %s: %w", ErrLayout, ManifestFile, decodeErr)
	}
	if decoder.More() {
		return nil, fmt.Errorf("%w: %s carries trailing content after the manifest object", ErrLayout, ManifestFile)
	}
	switch {
	case strings.TrimSpace(doc.DisplayName) == "":
		return nil, fmt.Errorf("%w: %s declares no display_name", ErrLayout, ManifestFile)
	case doc.MaestroVersion.Min == "" || doc.MaestroVersion.Max == "":
		return nil, fmt.Errorf("%w: %s must declare maestro_version.min and maestro_version.max", ErrLayout, ManifestFile)
	}
	roles, err := canonicalRoles(doc.Roles)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrLayout, ManifestFile, err)
	}
	return &Pack{
		DisplayName:       doc.DisplayName,
		MinMaestroVersion: doc.MaestroVersion.Min,
		MaxMaestroVersion: doc.MaestroVersion.Max,
		Roles:             roles,
	}, nil
}

// canonicalRoles is the manifest's coverage as a set: each role canonical,
// none repeated, sorted. Repetition is refused rather than collapsed --
// the seam would collapse it, but a manifest that lists a role twice was
// edited by hand and one of the two is likely a typo for something else.
func canonicalRoles(declared []string) ([]string, error) {
	seen := make(map[string]bool, len(declared))
	for _, role := range declared {
		if !rolePattern.MatchString(role) {
			return nil, fmt.Errorf("role %q is not a canonical role name", role)
		}
		if seen[role] {
			return nil, fmt.Errorf("role %q is listed twice", role)
		}
		seen[role] = true
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

// loadEntries reads entries/, which may be absent: a pack with no slots has
// no directory to hold them, and an empty directory cannot be embedded.
func loadEntries(fsys fs.FS) (map[string]string, error) {
	files, err := fs.ReadDir(fsys, EntriesDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("%w: read %s/: %w", ErrLayout, EntriesDir, err)
	}
	entries := make(map[string]string, len(files))
	for _, file := range files {
		name := file.Name()
		if file.IsDir() {
			return nil, fmt.Errorf("%w: %s/%s is a directory; entries are one file per slot", ErrLayout, EntriesDir, name)
		}
		slot, isEntry := strings.CutSuffix(name, EntryExtension)
		if !isEntry || !slotPattern.MatchString(slot) {
			return nil, fmt.Errorf("%w: %s/%s is not <slot>%s for a canonical slot key", ErrLayout, EntriesDir, name, EntryExtension)
		}
		raw, err := fs.ReadFile(fsys, path.Join(EntriesDir, name))
		if err != nil {
			return nil, fmt.Errorf("%w: read %s/%s: %w", ErrLayout, EntriesDir, name, err)
		}
		if err := checkEntryText(SlotKey(slot), string(raw)); err != nil {
			return nil, err
		}
		entries[slot] = string(raw)
	}
	return entries, nil
}

// refuseStrays walks the root and refuses anything the layout does not name.
func refuseStrays(fsys fs.FS) error {
	root, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("%w: read the pack root: %w", ErrLayout, err)
	}
	for _, file := range root {
		name := file.Name()
		switch {
		case name == ManifestFile && !file.IsDir():
		case name == EntriesDir && file.IsDir():
		default:
			return fmt.Errorf("%w: %q is not part of a pack (expected %s and %s/)", ErrLayout, name, ManifestFile, EntriesDir)
		}
	}
	return nil
}
