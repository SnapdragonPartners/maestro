+++
title = "ADR 0023: v2 Branch Strategy"
edit_date = "2026-07-13"
status = "draft"
summary = "Maps git structure to the work hierarchy: Epic branches off default, Story branches off Epic; automated Story merges on passing evidence, human Accept for Epic-to-default; rebase as a harness function; branch naming for machine and human branches."
+++

# 0023. v2 Branch Strategy

Status: Proposed

## Context

v1 merges every Story directly to the default branch, which makes the Epic-level integration and acceptance unit invisible in git. ADR 0018 makes the mapping explicit — Story is the local implementation/review unit, Epic the integration/acceptance unit, Feature the cross-repo intent unit — and the git model should mirror it one-to-one. An agent fleet also needs branch conventions that are deterministic and collision-free (the `v2/roadmap` leaf-vs-namespace collision during the v2 build demonstrated the failure mode).

## Decision

### The mapping

- **Feature** owns no branch — it may span repositories and is coordinated through artifacts, not git.
- **Epic** owns one branch per Epic, cut from the default branch head of its repository.
- **Story** owns one branch, cut from its Epic branch head; Story branches merge back into the Epic branch.
- The **Epic branch merges into default** only on acceptance (roadmap D4): human Accept by default, waivable per Epic by explicit recorded configuration for low-risk cases.

### Merge policy

- **Story → Epic** merges are automated when the Story's evidence passes review (roadmap pillar 8) — the Architect's review record is the gate, not a human click.
- **Epic → default** is the human acceptance gate (ADR 0020's human-reserved approval). This creates deliberate back-pressure in large Features (D4); the dashboard makes the queue visible.
- Merged Story branches are deleted; Epic branches are deleted after acceptance. Golden-story fixture branches are cleaned per the benchmark ADR (item 7).

### Rebase is a harness function

Keeping Story branches current against the Epic head, and Epic branches current against default, is scheduled harness work — not an incidental git operation left to agent whim. The split follows ADR 0019's boundary rule: a mechanically clean rebase is Orchestrator work; a conflict requiring judgment spawns an agent — dispatched as a conflict-resolution Story or a Workbench session (roadmap pillar 6). The Orchestrator never resolves a conflict by inference, because it can't.

### Diff semantics

v1's phantom-diff prevention carries forward, retargeted to the hierarchy: review diffs use merge-base semantics against the branch one level up — Story diffs against the Epic branch, Epic diffs against default — so a review never contains changes that arrived from elsewhere. Reviewers obtain diffs themselves (fresh `get_diff`-style calls); diffs are never delivered as stale payload.

### Branch naming

Machine-managed branches are namespaced away from human branches:

- `maestro/epic/<epic-id>` and `maestro/story/<epic-id>/<story-id>` for runtime branches. IDs, not titles — deterministic, collision-free, and stable under renames.
- Human development branches keep their own namespaces (during the v2 build: `v2/phase_x/*`, `v2/fix/*` per the build process).
- The leaf-vs-namespace rule is law: a name once used as a leaf branch is never reused as a namespace prefix, and the ID-based scheme above is constructed so the situation cannot arise (`maestro/epic/<id>` is always a leaf; `maestro/story/<epic-id>/` is always a namespace).

### What carries forward

The clone/mirror/forge workflow of historical note [0009](0009-clone-mirror-and-forge-pr-workflow.md) is kept and extended: local bare mirrors, forge-mediated PRs, and branch protection on default remain; only the branch topology above it changes. Forge bindings are repo attributes (ADR 0022), so the strategy is forge-independent — an Epic branch behaves identically on a local airplane-mode forge and on GitHub.

## Consequences

- The git graph mirrors the work hierarchy, so dashboards and golden stories can derive integration state from git itself rather than shadow bookkeeping.
- Long-lived Epic branches make rebase cadence a real harness parameter (an H lever in MPH) — measurable by the golden suite's merge-conflict stories.
- The automated Story→Epic merge concentrates CI attention where it belongs: Story-level checks gate the automated merge; Epic-level integration checks gate the human Accept.
- Phase 4 implements exactly this ADR (branch creation, both merge paths, rebase functions, conflict dispatch); its exit criteria already match.

## Related Documents

- [ADR 0018](0018-v2-work-taxonomy.md) (the hierarchy and its git mapping), [ADR 0019](0019-orchestrator-boundary.md) (mechanical-vs-judgment split), [ADR 0020](0020-review-invariant-reviewer-vs-partner.md) (review gates, human-reserved Accept), [ADR 0022](0022-v2-data-plane.md) (forge-independent repos).
- [Roadmap](../v2/roadmap.md) pillars 6 and 8, D4; [build-process](../v2/build-process.md) (human branch conventions); [ADR backlog](../v2/adr-backlog.md) Branch Strategy entry.
- Historical note [0009](0009-clone-mirror-and-forge-pr-workflow.md) (kept and extended; superseded only in branch topology).
