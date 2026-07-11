# ADR 0014: Failure Taxonomy, Durable Asks, and Incidents

- Status: Proposed
- Date: 2026-07-06

## Context

Agents fail for very different reasons: a transient provider outage, an
unrecoverable story that is contradictory or impossible, an environment/container
problem, or a missing prerequisite. Treating all failures the same causes bad
recovery decisions — endlessly requeueing an impossible story, or giving up on a
story that only needed a retry after a network blip.

The system also needs durable, human-answerable items that outlive a single agent
turn: user-facing questions the PM owns, and operational incidents the architect
owns. These cannot live only in ephemeral agent memory, because the answering human
may respond minutes or hours later, possibly after a restart.

## Decision

Classify agent failures with a structured `FailureKind` wherever the failure path
has enough context, and carry that classification through the requeue/restart path
so the supervisor and architect can make kind-aware recovery decisions rather than
blind retries. Legacy or unexpected-exit paths may still be unclassified, but new
failure-producing code should prefer structured `FailureInfo`.

Represent long-lived human-answerable work as durable items with explicit
lifecycles, split by owner:

- The PM owns user asks (questions surfaced to the human).
- The architect owns incidents (operational failures needing a decision).

Replies must resolve the correct durable item type. Failure kinds evolve through a
normalization path so older records remain interpretable as the taxonomy changes.

## Current Implementation

- `pkg/proto/failure.go` defines `FailureKind` and its members
  (`transient`, `story_invalid`, environment/prerequisite kinds), the deprecated v1
  `external` umbrella, and `NormalizeFailureKind()` to map legacy kinds forward.
- `pkg/proto/incident.go` defines `Incident`, `IncidentKind`, and `IncidentAction`.
- `internal/supervisor/supervisor.go` extracts `proto.FailureInfo` from state-change
  notification metadata and passes it into `Dispatcher.UpdateStoryRequeue()` on coder
  ERROR, so requeue reason and classification are preserved.
- Telemetry records failures for later analysis (see `docs/FAILURE_TELEMETRY.md`).

## Consequences

- New failure paths should classify with a current `FailureKind` when possible, not
  the deprecated `external` umbrella, and should route through
  `NormalizeFailureKind()` when reading historical records.
- Recovery policy is a function of failure kind; do not add ad hoc retry logic that
  ignores the classification.
- Durable asks and incidents must resolve against the correct owner (PM vs architect)
  — see ADR 0010.
- A watchdog/requeue race can currently produce a spurious `environment` failure
  record for a cleanly-completed story; analytics that depend on environment failure
  records should account for this until it is fixed
  (SnapdragonPartners/maestro#221).

## Related Documents

- `docs/FAILURE_TAXONOMY_SPEC.md`
- `docs/FAILURE_RECOVERY_V2_SPEC.md`
- `docs/DURABLE_ASKS_AND_INCIDENTS.md`
- `docs/FAILURE_TELEMETRY.md`
- ADR 0003 (agent roles and FSMs)
- ADR 0010 (PM/architect ownership of asks and incidents)
