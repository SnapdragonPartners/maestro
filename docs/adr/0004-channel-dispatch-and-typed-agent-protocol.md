# ADR 0004: Channel Dispatch and Typed Agent Protocol

- Status: Proposed
- Date: 2026-07-06

## Context

The system coordinates autonomous agents that must exchange tasks, questions,
reviews, status updates, requeue requests, cancel requests, and PM interview
events. Earlier designs mention several queue and API approaches. Current code
uses a dispatcher with typed `AgentMsg` payloads and dedicated channels.

## Decision

Use `pkg/dispatch.Dispatcher` as the inter-agent routing boundary. Agents should
communicate through typed protocol messages and dispatcher-owned channels rather
than calling each other directly.

The protocol should preserve the high-level message families:

- TASK for work assignment.
- QUESTION/ANSWER for clarification.
- REQUEST/RESPONSE for reviews, spec approval, merge approval, and budget review.
- ERROR/SHUTDOWN for control and failure cases.

Typed payloads in `pkg/proto` should be preferred over ad hoc map payloads for new
work. Legacy map extraction may remain where migration cost is higher than risk,
but new protocol fields should have explicit structs, validation, and tests.

## Current Implementation

- `pkg/dispatch/dispatcher.go` owns shared story channels, hotfix story channels,
  architect request channels, PM request channels, status update channels, requeue
  channels, cancel request channels, state change notifications, leases, and
  per-agent reply channels.
- Agents implementing `ChannelReceiver` receive their relevant channels at attach
  time.
- `pkg/proto` defines `AgentMsg`, typed payload wrappers, validation, failures,
  incidents, and unified protocol helpers.
- `cmd/maestro/flows.go` uses `InjectSpec` to submit CLI specs through the same
  REQUEST protocol used by PM spec submissions.

## Consequences

- Inter-agent behavior remains observable and persistable through protocol messages.
- Per-agent reply channels keep review/question responses targeted.
- Leases let the supervisor and architect avoid requeueing or cancelling stale work.
- Direct method calls should be limited to local UI-to-PM control surfaces or other
  explicitly documented exceptions.

## Related Documents

- `docs/MAESTRO_CHAT_SPEC.md`
- `docs/UNIFIED_CODER_TOOLS_SPEC.md`
- `docs/PAYLOAD_REFACTOR.md`
- `docs/FAILURE_TAXONOMY_SPEC.md`
- `docs/DURABLE_ASKS_AND_INCIDENTS.md`

