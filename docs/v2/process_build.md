+++
title = "Maestro v2 Build Process (Interim)"
edit_date = "2026-08-31"
status = "live"
summary = "Working agreement for building v2 until Maestro can build Maestro: Claude authors, Codex reviews, DR orchestrates and accepts; review cadence, branching, spikes, testing, and merge rules. Command-level mechanics live in CLAUDE.md."
type = "process"
+++

# Maestro v2 Build Process (Interim)

Status: live — agreed working agreement, 2026-07-11; amended 2026-07-24 (review-before-push made explicit, golden-run cost gate added, command-level mechanics delegated to `CLAUDE.md`); amended 2026-08-06 (defect-shaped verification made explicit); amended 2026-08-31 (*Reachability Claims* added — Accepted by Codex and DR alongside Phase 3 item 1).

This defines how v2 gets built until Maestro can build Maestro (the Phase 9 ramp). It manually implements the generate/review invariant that Maestro v2 automates: one author, one reviewer, human escalation.

## Scope Of This Document

This is the binding agreement, and it binds all three parties. The command-level mechanics that *execute* it — exact branch-name patterns, the step-by-step git workflow, the version-tag ladder, documentation front matter — live in `CLAUDE.md`, which is Claude's operating manual.

The split is by audience: a rule that DR or Codex needs in order to review or enforce the work belongs here; a detail only the author executes belongs in `CLAUDE.md`. Where the two disagree, **this document wins and `CLAUDE.md` is the bug.**

## Roles

- **Author agent: Claude (Claude Code).** Drafts all artifacts — docs, ADRs, phase scopes and plans, specs, code. Roles anchor to the agent, not the underlying model.
- **Reviewer: Codex.** Provides the review function, analogous to what Maestro will automate.
- **Human operator: DR.** Resolves escalation and contention, provides feedback, and accepts. DR is also the effective orchestrator: all communication between Claude and Codex flows through DR.

An artifact is Accepted when both Codex and DR have approved it.

## Phase Workflow

- Each phase begins with a scope and a plan, each reviewed and approved by both Codex and DR.
- The working doc set stays deliberately small — there is no Maestro apparatus yet to manage a large one.
- Each phase gets a branch (or more than one, only if the plan says so).
- Never more than one feature/dev branch open at a time. Parallel branches are acceptable only for bug fixes. This bounds the human operator's review load, not the author's throughput.
- Per-phase working artifacts live in `docs/v2/phase_x/`, mirroring the branch namespace; cross-phase docs stay at the `docs/v2/` root; Accepted decisions land in `docs/adr/`.

## Review Cadence

- **Review happens on local commits.** Claude commits locally, produces branch notes for Codex, and iterates until every point is resolved or explicitly overridden by DR. Both reviewers read the work in place; nothing needs to be pushed to be reviewed.
- **A branch is pushed only after Codex and DR have approved.** Push is a gate, not a step.
- Smaller units of work get a single end-of-work review. Larger ones get review checkpoints defined in the phase plan.

## Testing

- Unit and integration tests gate merges within a phase.
- The golden story suite runs at the end of each phase (once it exists, Phase 1 onward).
- **Golden runs spend real money.** They bill live model APIs — the Phase 1a run cost $26.40. DR must explicitly approve or override each individual run. Approval of a phase, of a plan, or of a previous run is not approval for the next one.

### Defect-Shaped Verification

A nontrivial regression test, structural test, or CI guard is not proven merely
because it passes against the finished code. Temporarily restore the exact defect
the check claims to catch, using the smallest mutation of code, query, or fixture
that reproduces it, and prove the intended check fails for the named reason.

A mutation counts as evidence only when all of these hold:

- it applies to the intended site and the mutated code compiles;
- the selected test actually runs, reaches the protected mechanism, and fails at
  the intended assertion rather than at an earlier or neighbouring guard;
- a positive control proves the fixture can pass through the valid path; and
- the mutation is restored and the clean working tree is verified before any
  later green run is trusted.

A compiler failure, an empty test selector, an unclassified timeout or hang, or
a failure caused by a different validation rule is not a killed mutation and
does not prove the test. Mutation harnesses must bound the test process, refuse
to start on a tree carrying prior mutation residue, and verify restoration even
after a failed or timed-out run.

Checkpoint notes report the protected defect and why the discriminating test
failed, not only a count such as “N/N mutations killed.” This rule is deliberately
scoped to guards and regression tests where a false green would hide a material
contract violation; it does not require mutation testing every routine assertion.

### Reachability Claims

A claim that code is unreachable, dead, vestigial, or safe to delete is a claim
about **every applicable repository configuration across the supported target
matrix**, and it is not proven by an analysis run in one of them. "Applicable"
is the operative word: the scope is the configurations this repository declares
and the platforms it supports — not every configuration the Go toolchain
admits, which no analysis could enumerate. The supported matrix is
ADR 0026's (`linux/amd64` and `linux/arm64` at minimum); a claim is bounded by
that matrix and must say so.

Within that scope the analysis must still be exhaustive. Enabling every build
tag at once is a single configuration: it is not equivalent to the union over
applicable configurations, because a file guarded by a negated or compound
constraint can be excluded from the all-on build while belonging to another
applicable one.

Configurations vary along two axes, and an analysis must account for both.
**Explicit** constraints are the `//go:build` expressions declared in the tree.
**Implicit** constraints select files without any such line: `GOOS`/`GOARCH`
filename suffixes (`_linux.go`, `_arm64.go`), cgo availability, and the
compiler, release and architecture-feature tags Go applies on its own. An
analysis that derives only the explicit axis has surveyed part of the
configuration space and must not claim to have surveyed it all.

A reachability claim counts as evidence only when all of these hold:

- the set of **explicit** build-constraint expressions is **derived at analysis
  time**, not copied from a document or a previous run;
- the **implicit** axis is addressed rather than ignored, across the supported
  matrix: evaluate the platform and cgo dimensions over ADR 0026's targets at
  minimum. Where a dimension selects no files, say so; that absence is itself
  the finding, and it can stop being true;
- the implicit axis is measured by **what the toolchain selects**, not by what
  text resembles. `go list`'s `GoFiles`, `CgoFiles` and `SFiles` under the
  relevant `GOOS`/`GOARCH`/`CGO_ENABLED` settings are evidence; a filename
  search or a `grep` for `import "C"` is not, because it matches occurrences in
  comments and strings. **Any command a document records must actually produce
  the result shown beside it**;
- reachability is computed as the **union over the applicable configurations**
  those two axes imply, and any claimed equivalence between that union and a
  single combined configuration is checked rather than assumed;
- production consumers and test-only consumers are reported **separately**, and
  a package's own in-package tests are not counted as consumers of it; and
- the import-graph result is cross-checked textually **across all file types**,
  not only source files, with any surviving references disposed of explicitly —
  a reference in a `deprecated` or archived document does not block retirement,
  but silence about it is not the same as its absence;
- **a failed measurement cannot be reported as a result.** Any command producing
  evidence propagates the failure of the tool that produced it — `set -o
  pipefail`, a checked exit status, and stderr left visible, never
  `2>/dev/null` piped into something that succeeds regardless. A tool that
  emits nothing on failure yields an empty result whose digest, count or
  comparison agrees with itself perfectly; that is the shape of a false green,
  so guard the degenerate value explicitly; and
- any **digest or ordering-dependent** figure records the environment needed to
  reproduce it — at minimum the toolchain version and the collation (`LC_ALL`),
  because `sort` order is locale-dependent and a digest taken over sorted output
  is not portable without it. A recorded digest that a reviewer cannot reproduce
  is not evidence, and the discrepancy is explained before the figure is
  replaced.

An import graph answers "nothing imports this," which is a proxy for "this is
dead." Reviewers hold the claim to the proxy actually measured: an analysis that
does not state its configurations has not established reachability, and neither
author nor reviewer should treat it as though it had.

Mechanical enforcement of this rule — deriving the constraint set in the build
and failing when an unrecognised one appears — is code, and belongs to its own
scheduled work rather than to any authoring item that happens to need the rule.
It is tracked as
[#342](https://github.com/SnapdragonPartners/maestro/issues/342). Until that
lands, this rule is enforced by review alone, which is exactly the weakness
#342 exists to remove.

## CI Review And Merge

- CI runs automated review agents on every PR. All of their feedback must be resolved before merge: each thread is either fixed or explicitly pushed back on with a reasoned reply, then marked resolved. Resolving CI reviewer feedback is Claude's job.
- Final approval and the merge button are DR's.

## Spikes

- Before a spike begins, all open document work is committed (risk minimization).
- Spike code never merges into app packages (`pkg/`, `internal/`, `cmd/`). Reports land in the phase directory; scripts worth revisiting may be preserved under `spikes/phase_x/`, a standalone module excluded from the main build, test, and lint walkers. Preserved scripts are unmaintained by definition.
- Spikes stay out of the version line — a spike is not a candidate for `v2.0.0`.

## Deferred Work Tracking

All deferred work discovered during v2 development is tracked as GitHub Issues — a durable record that keeps the primary docs and repo clean and does not rely on any one agent's memory. The division of labor: the roadmap holds planned work (phases and spikes), Issues hold deferred work discovered along the way, and the docs/v2 parking lot holds design ideas.

## Escalation

Author/reviewer contention that does not converge goes to DR — the same bounded-contention principle the product applies to agent pairs (roadmap pillar 7).
