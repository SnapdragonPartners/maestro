# CLAUDE.md

Operating instructions for Claude Code in this repository. `AGENTS.md` is a
symbolic link to this file; edit `CLAUDE.md` only so both entry points remain in
sync.

## Project And Authority

Maestro is a Go application for coordinating AI agents to turn product intent
into reviewed repository changes. Active development is the breaking v2
redesign. v1 remains the current implementation until replaced, but is frozen:
do not fix v1 defects unless they block v2 work.

Use the source appropriate to the question.

For current runtime behavior, precedence is:

1. Code and tests.
2. Canonical FSM docs: `pkg/pm/STATES.md`, `pkg/architect/STATES.md`, and
   `pkg/coder/STATES.md`.
3. Current implementation summaries in this file and `README.md`.
4. `deprecated` v1 docs, as unverified orientation only.

For v2 design intent, precedence is:

1. Accepted ADRs (`status = "live"`) in `docs/adr/`.
2. Live phase artifacts in `docs/v2/phase_x/`.
3. The roadmap and other cross-phase documents in `docs/v2/`.
4. Historical ADRs 0001–0016, as non-binding context only.

Documents with `status = "archive"` carry no authority. When code and a v2 ADR
differ, the code describes today and the ADR describes the accepted direction.
Start with `docs/README.md`, `docs/adr/README.md`, and the applicable phase
`README.md`; do not treat a referenced deprecated document as authoritative.

## Binding V2 Workflow

`docs/v2/process_build.md` is the binding working agreement for building v2.
It binds Claude, Codex, and DR and wins over this file if they disagree. The
current phase scope and plan at `docs/v2/phase_x/plan_scope.md` binds work in
that phase. `docs/v2/plan_roadmap.md` defines the phase sequence and cross-phase
decisions.

Roles:

- Claude authors docs, ADRs, scopes, plans, specs, and code.
- Codex reviews.
- DR orchestrates, resolves contention, and accepts.
- An artifact is Accepted only after both Codex and DR approve it.
- Claude/Codex communication routes through DR. Escalate contention that does
  not converge to DR.

### Branch And Review Workflow

1. Work on a feature branch from `main`: `v2/phase_x/<slug>` for phase work or
   `v2/fix/<slug>` for a bug fix. Never work directly on `main`.
2. Never reuse an existing leaf branch name as a namespace prefix; Git refs
   cannot be both a leaf and a directory.
3. Make and test changes, then commit locally. Never bypass hooks with
   `--no-verify`; fix failures.
4. Produce branch notes for Codex and iterate on the local commits until every
   review point is resolved or DR explicitly overrides it.
5. Push only after Codex and DR approve. Push is a gate, not a routine step.
6. Open the PR to `main`; reference the phase plan and applicable ADRs/specs.
7. Address every CI review thread with a fix or reasoned reply, push the
   resolution, mark the thread resolved, and check again for new feedback.
8. DR gives final approval and merges. Claude never merges.

Keep at most one feature/development branch open at a time. Parallel branches
are allowed only for bug fixes. Review checkpoints for large work come from the
phase plan; smaller work receives one end-of-work review.

This human build workflow is distinct from the product's v2 Epic/Story branch
model in ADR 0023. Use ADR 0023 when implementing Maestro's Orchestrator-managed
branch behavior; use the workflow above when contributing to this repository.

### Spikes And Deferred Work

- Commit all open document work before starting a spike.
- Spike code never merges into `pkg/`, `internal/`, or `cmd/`.
- Put reports in the phase directory. Preserve useful scripts only under
  `spikes/phase_x/`, a standalone module excluded from main build, test, and
  lint walkers; preserved spike scripts are unmaintained.
- Put planned work in the roadmap, deferred work discovered during development
  in GitHub Issues on `SnapdragonPartners/maestro`, and design ideas in
  `docs/v2/notes_parking-lot.md`.
- Spikes are not version candidates. Tags such as
  `spike/phase-2-auth` are allowed but do not enter the v2 SemVer ladder.

### Version Tags

Phase releases are prereleases of `v2.0.0`:

```text
v2.0.0-phase.2.0.0      phase 2
v2.0.0-phase.2.1.0      phase 2a
v2.0.0-phase.2.1.1      phase 2a, second cut
v2.0.0-phase.9.0.0      phase 9
v2.0.0-rc.1
v2.0.0
```

The phase identifier is `<phase>.<subphase>.<iteration>`, always three numeric
positions. `0` is the phase proper, `1` is subphase a, and `2` is subphase b.
Never use mixed identifiers such as `phase.2a`. The ladder begins at
`v2.0.0-phase.2.0.0`; do not backfill Phase 1 or 1a tags.

Configure Git's version sorting once per clone:

```bash
git config --add versionsort.suffix "-alpha."
git config --add versionsort.suffix "-phase."
git config --add versionsort.suffix "-rc."
git config --add versionsort.suffix ""
```

## Documentation Rules

Follow ADR 0017.

- New or substantively edited Markdown under `docs/` uses TOML front matter
  with `title`, `edit_date`, `status`, and `summary`; documents following the
  naming convention also include `type`.
- Status is one of `draft`, `live`, `deprecated`, or `archive`. Live status is
  earned through review. Do not infer authority from a link alone.
- Name new docs `type_slug.md`: lowercase single-word type, underscore,
  kebab-case slug. ADRs and `README.md` retain their established names.
- Prefer existing types: `plan`, `spike`, `inventory`, `manifest`,
  `requirements`, `design`, `process`, `research`, and `notes`.
- Put phase artifacts in `docs/v2/phase_x/`, cross-phase docs in `docs/v2/`,
  and Accepted decisions in `docs/adr/`.
- Add each new or relocated draft/live/deprecated doc to its directory's
  `README.md`, quoting its front-matter `summary` verbatim.
- Do not hand-maintain `edit_date`; the pre-commit hook stamps it. Expect the
  stamp to change a staged doc during commit.
- `docs/archive/` is exempt from per-file indexing and archived docs may omit
  `summary` and `type`. Superseded ADRs and closed phase artifacts archive in
  place and keep full front matter.

ADRs use the lifecycle Proposed → Accepted → Superseded or Rejected. Acceptance
requires Codex and DR approval; update status only in the final reviewed commit.
Use the ADR template described by `docs/adr/README.md`.

## Development And Verification

The `Makefile` is the source of truth for commands and dependencies:

```bash
make build             # build, generated assets, and lint prerequisites
make test              # repository unit tests, including benchmark tests
make lint              # repository linters
make test-integration  # integration suite; may require service credentials
make run               # build and run Maestro
```

Build targets install hooks and may include lint prerequisites. Do not copy
testing policy from `docs/TESTING_STRATEGY.md`; it is a deprecated v1 reference.
Use the current phase plan, tests adjacent to the code, build-tagged suites, and
accepted ADRs—especially ADR 0025 for golden stories—to determine required
coverage.

Golden runs use paid model APIs. Never run `golden-minimal`, `golden-all`, or
another paid golden configuration without DR's explicit approval for that
specific run. Phase, plan, or previous-run approval is not reusable. Record each
approved phase-end run in `docs/v2/notes_conformance-log.md` with date, target
identity, per-story verdict, and cost/token totals until the accepted data-plane
design retires that log.

### Verification Discipline

- Verify dependency claims against the pinned version's source, official docs,
  or a focused reproducer before putting them in code, docs, or commit messages.
- For nontrivial regression tests and CI guards, temporarily break the protected
  behavior or fixture to prove the check fails; restore it before committing.
- For fixes, reproduce the original failure, test new boundary behavior, and
  search adjacent call sites for the same pattern.
- Before implementing schemas, parsers, validators, or policy checks, enumerate
  required invariants and rejected cases.
- State important untestable guarantees beside the code. Do not imply they are
  covered merely because neighboring tests pass.
- Use shared mocks for nondeterministic or external boundaries and real cheap,
  deterministic in-process components where that gives stronger confidence.
  Confirm the established pattern in nearby current tests rather than relying
  on deprecated testing documentation.

### Durable Engineering Invariants

These rules are easy to violate and costly to rediscover:

- Shared local infrastructure: operations mutating state reachable from more
  than one agent lifecycle must be idempotent or serialized using a key matching
  the shared resource. Bare last-writer-wins is a defect. Destructive recovery
  must never remove another actor's in-progress work. See ADR 0027.
- Bind-mounted workspace roots: preserve the directory inode. Never call
  `os.RemoveAll` on a workspace root; use
  `utils.CleanDirectoryContents` from `pkg/utils/fs.go`. Delete-and-recreate is
  acceptable only for directories not mounted or shared across actors.
- Cross-architecture artifacts: embedded binaries and published images must
  support at least `linux/amd64` and `linux/arm64` and be verified on each.
  Publish images as multi-arch manifests pinned by the architecture-independent
  manifest digest; cross-compile packaged binaries and select by runtime
  architecture. See ADR 0026.
- Product branch diffs: when implementing or reviewing the v1 path, use the
  repository's merge-base-aware diff machinery and refresh the diff on every
  review round. Do not trust a stale or payload-supplied raw diff.
- v2 Orchestrator boundary: the Orchestrator owns deterministic machinery—agent
  lifecycle, tools, routing, forge, persistence, and scheduling. If a decision
  requires inference or judgment, it belongs to an agent or human. See ADR 0019.
- v2 artifacts: agent handoffs are artifacts; every Management artifact is
  reviewed by a non-author. Preserve immutable accepted history and enforce
  transitions at the Orchestrator seam. See ADRs 0020, 0021, and 0028.
- v2 persistence: use Postgres, sqlc, and golang-migrate through the
  Orchestrator's persistence seam. Do not extend v1 SQLite conventions into v2.
  See ADR 0022 and the current phase plan.

## Architecture Routing

Do not maintain package inventories, field lists, configuration examples, or
state-flow narratives here; those become stale as implementation moves. Route
questions to the smallest authoritative source:

| Question | Read first |
| --- | --- |
| Current PM/Architect/Coder behavior | `pkg/*/STATES.md`, then code and tests |
| Current package or configuration behavior | Owning package, tests, CLI help, and `README.md` |
| Current LLM adapter behavior | `pkg/agent/internal/llmadapter`, tests; deprecated migration doc only as history |
| v2 work hierarchy and Workbench | ADR 0018 |
| Orchestrator ownership | ADR 0019 |
| Review invariant | ADR 0020 |
| Artifact and principal lifecycle | ADR 0021 |
| v2 persistence and data boundaries | ADR 0022 |
| Product Epic/Story branching | ADR 0023 |
| Intake outputs and dispatch seam | ADR 0024 |
| Golden runner and benchmark contract | ADR 0025 |
| Cross-architecture distribution | ADR 0026 |
| Shared-state concurrency | ADR 0027 |
| Artifact encoding and schemas | ADR 0028 |
| Current phase scope and acceptance | `docs/v2/phase_x/plan_scope.md` |

Search code before assuming a path named in a historical document still exists.
When adding a durable design rule, prefer an ADR or focused live doc and link it
from the relevant index; keep this file to the rule and routing pointer.

## Code And Review Standards

Prioritize correctness, robustness, readability, and maintainability. Prefer
simple, idiomatic Go over cleverness or speculative abstraction. Follow the
repository's Go version and established package patterns.

For review findings:

- P0: likely data loss/corruption, crashes in normal use, critical security
  vulnerabilities, or violations that make the requested result unusable.
- P1: concrete correctness, robustness, architectural, or maintainability
  defects that should block acceptance.
- Treat lesser improvements as suggestions or questions. Explain impact and
  cite the exact code or accepted rule; do not block on personal preference.

### Go And Maintainability

- Use `any` instead of `interface{}` in new code. Use generics only when they
  reduce duplication or improve type safety without obscuring behavior.
- Handle errors explicitly and add operational context. Do not silently discard
  errors at meaningful boundaries.
- Prefer the repository's `SafeAssert` helper over bare type assertions when it
  improves failure handling or clarity. A constrained, proven assertion may
  remain; do not mechanically rewrite assertions without benefit.
- Name repeated or non-obvious literals—keys, paths, environment variables,
  timeouts, limits, endpoints, and protocol values. Leave obvious single-use
  literals inline.
- Consolidate duplicated behavior when a shared helper creates a stable seam.
  Do not force DRY across concepts that merely look alike or create coupling to
  avoid a few lines.
- Reject abstractions without a present consumer, boundary, testability gain,
  or established multi-backend need. Interfaces with one implementation require
  a concrete reason.
- Treat comments as contract. Remove stale comments. Link consequential TODO,
  FIXME, and deprecation notes to a plan or issue; critical untracked TODOs are
  acceptance blockers.
- Remove unreachable and orphaned code unless a documented build tag, feature
  gate, or phase plan intentionally retains it.

### Security And Tests

Maestro currently runs locally, but local does not mean trusted. Block obvious
command injection, unsafe user-controlled filesystem paths, and committed
secrets. Apply security work proportionately; do not impose unrelated
enterprise controls without a requirement.

Add tests where they materially reduce risk, especially for branching logic,
parsing and validation, reusable helpers, concurrency, persistence invariants,
and regressions. Use integration tests for boundaries that unit tests cannot
exercise faithfully. A test that cannot fail for the protected defect is not a
regression test.

Keep review feedback constructive and specific. When a local convention
conflicts with accepted ADRs, the phase plan, Go idiom, or a pinned library's
contract, follow the higher-authority source and identify the conflict.
