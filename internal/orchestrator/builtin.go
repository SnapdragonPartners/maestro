package orchestrator

import (
	"embed"
	"fmt"
	"io/fs"

	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/prompt"
)

// The built-in prompt pack (ADR 0031 section 6; item 4 design, D1, D2).
//
// The binary carries it and organization provisioning imports it. It lives
// beside Prompts because it is the same declaration seen from the other
// side: Prompts says which slots exist, and the built-in supplies entries
// for them. At item 4 both are empty, by the rule Prompts states -- a slot
// is registered by the item that first renders it -- so the built-in is a
// manifest declaring the Phase 3 range and no roles, and no entries. That
// pack is resolvable and not executable, and the two are different claims
// (design D1).
//
// builtinDir holds the layout prompt.Load reads. An embed of an empty
// directory is impossible, which is why the loader treats a missing
// entries/ as zero entries rather than as an error.
//
//go:embed builtin
var builtinDir embed.FS

// builtinRoot is the directory embedded above, as the loader's root.
const builtinRoot = "builtin"

// BuiltinPack is the file system the production built-in is loaded from.
// The composition root passes it to LoadBuiltin; tests pass an
// fstest.MapFS to the same function (design D2).
func BuiltinPack() fs.FS {
	sub, err := fs.Sub(builtinDir, builtinRoot)
	if err != nil {
		// The embed directive above names the directory; a failure here is
		// a build that embedded nothing, which is not a runtime condition.
		panic(fmt.Sprintf("orchestrator: the built-in prompt pack is not embedded: %v", err))
	}
	return sub
}

// LoadBuiltin reads a built-in pack from fsys through the one loader and
// shapes it for the seam. This is the ONLY route from a file system to a
// store.BuiltinPromptPack: production supplies BuiltinPack(), fixtures
// supply a MapFS with content, and both cross the same code -- the
// loader, then the seam's gate, digest and install. A fixture that reached
// the seam another way would prove the seam and leave this path exercised
// only over the empty production pack.
func LoadBuiltin(fsys fs.FS) (store.BuiltinPromptPack, error) {
	var none store.BuiltinPromptPack
	loaded, err := prompt.Load(fsys)
	if err != nil {
		return none, fmt.Errorf("load the built-in prompt pack: %w", err)
	}
	return store.BuiltinPromptPack{
		Entries:           loaded.Entries,
		DisplayName:       loaded.DisplayName,
		MinMaestroVersion: loaded.MinMaestroVersion,
		MaxMaestroVersion: loaded.MaxMaestroVersion,
		DeclaredRoles:     loaded.Roles,
	}, nil
}
