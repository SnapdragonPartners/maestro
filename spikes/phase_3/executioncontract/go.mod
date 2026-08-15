// Standalone spike module. Excluded from the main build, test, and lint walkers
// by living outside the root module. Unmaintained: see CLAUDE.md, Spikes And
// Deferred Work.
//
// This is A4's conformance executable for ADR 0032 -- the single bounded
// exception to the pre-Phase-3 rule that a design item produces an Accepted ADR
// and nothing else. Where it lands permanently is a Phase 3 decision.
module maestro-spike/phase3/executioncontract

go 1.26.3
