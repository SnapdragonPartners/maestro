+++
title = "ADR 0027: Concurrency Safety for Shared Local Infrastructure"
edit_date = "2026-07-21"
status = "proposed"
summary = "Maestro runs many agent lifecycles concurrently against shared local state — git mirrors, workspace directories, dispatcher leases, supervisor restarts. Any operation that mutates state reachable from more than one lifecycle MUST be concurrency-safe: serialized by a key that matches the shared resource (path, agent ID, story ID) or made idempotent, and destructive recovery MUST never delete another actor's in-progress work. Bare last-writer-wins on shared infrastructure is a defect, the same class as a single-arch cross-arch artifact."
+++

# 0027. Concurrency Safety for Shared Local Infrastructure

Status: Proposed (DR-suggested 2026-07-21, pending Codex + DR acceptance). Motivated by three same-class races patched on the Phase 1 benchmark path: the supervisor double-restart (P-6), the agent-type recovery race (P-2), and the git-mirror clone race (P-11). See [patches_v1.md](../v2/phase_1/patches_v1.md).

## Context

Maestro is a local multi-agent factory: a supervisor, dispatcher, PM, architect, and one or more coders (plus transient hotfix agents) run **concurrently in the same process**, and each has its own lifecycle — start, SUSPEND/resume, watchdog kill, restart. The runtime kernel ([ADR 0019](0019-orchestrator-boundary.md)) owns the shared local infrastructure these lifecycles touch: SQLite, the dispatcher and its leases, git mirrors under `.mirrors/`, and per-agent workspace directories.

The recurring defect is structural, not incidental: **an operation on shared state is invoked from more than one lifecycle at nearly the same instant, and the operation was written as if it were the only caller.** The second caller then clobbers, deletes, or duplicates the first caller's in-flight work. Because the agents are event-driven and the windows are sub-millisecond to ~1s, these races are invisible in normal interactive use and surface only under load, on slower hardware, or against larger inputs — exactly the conditions the v2 benchmark creates.

It has now recurred three times in three subsystems, each root-caused from run traces:

- **Supervisor double-restart (P-6).** An agent death fires two independent supervisor paths — the ERROR state-notification and the `Run()`-exit handler — within ~1ms. Both restarted the agent, registering two live instances; `Dispatcher.Attach` is last-writer-wins on the reply channel, so every subsequent approval response went to the idle duplicate while the story-owning instance timed out → double-requeue → attempt-budget burn until abandonment. Fixed with an atomic per-agent in-flight claim and `Dispatcher.TakeLease` (atomic get+clear) so exactly one path requeues a dead agent's story.
- **Agent-type recovery race (P-2).** The unexpected-exit cleanup deleted the `AgentTypes` entry before the restart path read it, so a failed coder was never restarted and its story stranded the factory in an idle hang. Fixed by recovering the type from the ID convention when the entry is already gone.
- **Git-mirror clone race (P-11).** `mirror.EnsureMirror` was written single-caller: two callers (PM bootstrap and workspace setup), holding **throwaway `Manager` instances against the same on-disk path**, race — one caller's in-progress `git clone --mirror` presents as a corrupt mirror ("invalid HEAD / No default references") to the other's integrity check, which `os.RemoveAll`s it mid-clone, crashing the first with `fetch-pack: invalid index-pack output`. It hit large-repo fixtures every time because their clone is slow enough to reliably lose the race. Fixed with a process-wide lock **keyed by the canonicalized mirror path** (not by `Manager`, since instances are ephemeral), exported as `mirror.LockPath` and held by *every* writer: `EnsureMirror`, `RefreshFromForge`, `SwitchUpstream`, and `pkg/coder`'s `CloneManager.ensureMirrorClone` — which previously performed its initial bare clone with no lock at all and guarded only its update path with a private `flock`, a second, mutually-blind lock regime. The first fix attempt covered only `EnsureMirror` and was rejected in review for exactly the reason this ADR exists: a per-resource rule that one writer ignores is not a rule.

Three recurrences of one failure mode across three subsystems is the signal that this belongs in a durable, referenceable standard — the same reasoning ADR [0026](0026-multi-architecture-artifacts.md) applied to cross-arch artifacts.

## Decision

Any operation that mutates state reachable from **more than one agent lifecycle** — process-global variables, dispatcher leases/registrations, git mirrors, workspace directories, and other shared kernel infrastructure — MUST be concurrency-safe. Specifically:

- **Serialize by the shared resource's identity, not the caller's.** The lock/lease key MUST match the thing being protected — the mirror **path**, the **agent ID**, the **story ID** — because callers routinely hold distinct, throwaway handles (`Manager`, driver, tool) to the *same* underlying resource. A mutex on an ephemeral instance protects nothing.
- **Prefer idempotency; fall back to serialization.** An operation that can be made a safe no-op when the work is already done (the waited-on caller finds a complete, valid mirror and skips re-cloning) is preferred. Where that is impractical, serialize the critical section end-to-end.
- **Destructive recovery MUST NOT destroy in-progress work.** "Looks corrupt" is not "is corrupt" while another actor may be mid-write. Recovery that deletes/recreates shared state (`os.RemoveAll` + reclone, delete+recreate a bind-mounted dir — see the bind-mount inode hazard in CLAUDE.md) MUST run under the resource's lock, so it cannot observe or truncate a concurrent writer's partial state.
- **State-changing dispatch handoffs MUST be atomic and single-winner.** Get-then-clear on a lease, claim-then-restart on an agent: use an atomic primitive (`TakeLease`, an in-flight claim), never a read followed by a separate write that a second path can interleave. Last-writer-wins on a reply channel or registration is a defect, not a tolerable default.

## Consequences

- **Reviewers flag bare last-writer-wins on shared infrastructure as a defect** (P1), the same standing as a mutable-tag pin or a single-arch cross-arch artifact. A new operation on `.mirrors/`, workspace dirs, dispatcher registrations, or process-global maps carries the burden of showing it is serialized-by-resource or idempotent.
- Process-wide lock registries (a `map[key]*sync.Mutex` guarded by its own mutex, or `sync.Map`) are an accepted, expected pattern for keying a lock to a resource identity; the `//nolint:gochecknoglobals` on such a registry is justified by this ADR (precedent: `pkg/preflight/validate.go`, `pkg/mirror/manager.go`).
- **The lock must be exported when writers span packages.** `mirror.LockPath` is public precisely because `pkg/coder` writes to the same directories; a package-private lock would have silently readmitted the race. Keys must be canonicalized (absolute + cleaned) so callers that spell a path differently still contend, and the registry's non-reentrancy must be documented at the exported boundary.
- **A per-resource lock needs a regression test that fails without it.** `pkg/mirror/lock_test.go` asserts mutual exclusion on one path, canonicalization across spellings, and non-contention across distinct paths — deterministic by construction (overlap is recorded, not raced for), so the guarantee cannot silently rot.
- Cost is a small amount of serialization at lifecycle-transition points (startup, restart, merge), never on the hot path of LLM calls — the contended operations are I/O setup, not per-token work.
- This standard is **v1-patch-shaped today** (the three fixes live as benchmark-target patches, never backported to `v1-freeze`) but is written arch-neutrally for v2: the v2 orchestrator inherits the same shared-infrastructure surface and the same obligation.
- **Not covered:** cross-*process* concurrency (multiple `maestro` instances against one project dir) and cross-*host* coordination. Those are out of scope for the current single-process, single-user local model ([ADR 0019](0019-orchestrator-boundary.md)); revisit if v2 moves infrastructure out of process.

## Related Documents

- [ADR 0019: Orchestrator Boundary](0019-orchestrator-boundary.md) — the kernel owns shared local infrastructure; this ADR constrains how that infrastructure is mutated concurrently.
- [ADR 0026: Multi-Architecture Distributable Artifacts](0026-multi-architecture-artifacts.md) — sibling "two-plus recurrences → durable standard" ADR; the reviewer-flags-a-defect posture is modeled on it.
- [patches_v1.md](../v2/phase_1/patches_v1.md) — P-2, P-6, P-11: the three races that motivated this ADR, each with its discovering run.
- `CLAUDE.md` — Bind Mount Inode Preservation (a shared-state destructive-recovery hazard in the same family).
