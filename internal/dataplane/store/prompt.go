package store

// PromptContract is the import gate for prompt packs, as the seam sees it
// (ADR 0031 section 5; Phase 3 item 4 design, D3 and D10).
//
// CONSUMER-OWNED, and that is the point of declaring it here. Whether a pack
// is usable is decided by a slot vocabulary, a per-slot variable contract, a
// template parser and a renderer -- harness machinery, which lives in
// internal/prompt. The seam must consult that gate on every pack write, or
// the gate is advisory: a pack written around it is a pack the plane holds
// and nothing can render. But the seam must not import it, or the renderer
// joins the plane's closure to buy a direct call. So the seam declares what
// it needs, internal/prompt.Registry implements it, and the composition root
// supplies it beside Types and Keys, with their semantics: what slots exist
// is a property of the caller's job.
//
// The parameters are plain types for the same reason. Entries are keyed by
// slot; declaredRoles is a set, so order and repetition carry no meaning.
//
// Implementations must be safe for concurrent use and must not retain or
// modify what they are given: one contract is shared by every pack write the
// seam serves.
type PromptContract interface {
	// ValidatePack reports whether these entries, declaring coverage of
	// these roles, are usable by the harness that supplied this contract.
	ValidatePack(entries map[string]string, declaredRoles []string) error
}
