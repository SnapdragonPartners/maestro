+++
title = "ADR 0010: PM-Led Spec, Bootstrap, Hotfix, and Demo Lifecycle"
edit_date = "2026-07-15"
status = "deprecated"
summary = "v1 PM-led spec, bootstrap, hotfix, and demo lifecycle; superseded conceptually by v2 intake (ADR 0024) and the Workbench."
+++

# ADR 0010: PM-Led Spec, Bootstrap, Hotfix, and Demo Lifecycle

- Status: Proposed
- Date: 2026-07-06

## Context

Maestro no longer treats specs as only static CLI input. The PM agent is the primary
user-facing product surface for requirements gathering, spec preview, bootstrap
coordination, progress updates, and hotfix intake.

The WebUI, demo service, bootstrap detection, and architect spec review all depend
on the PM being a first-class participant.

## Decision

Use PM mode as the default product flow:

1. PM gathers requirements or accepts uploaded spec content.
2. PM generates a previewable spec and waits for user action.
3. User submits the spec for development.
4. PM sends a spec approval REQUEST to Architect.
5. Architect reviews the spec and either returns feedback or submits stories.
6. Architect dispatches stories to coders.
7. PM remains engaged for user questions, incidents, tweaks, and hotfixes.

CLI spec injection should use the same Architect REQUEST path as PM submission.
Bootstrap work should be represented as work the agent system can reason about,
not as a separate hidden one-off path. Demo availability should be determined by
PM/bootstrap state rather than guessed independently by the WebUI.

## Current Implementation

- `cmd/maestro/main.go` logs that PM mode is the default and only main mode.
- `cmd/maestro/flows.go` creates PM when enabled, wires PM as the demo availability
  checker, creates a dedicated `hotfix-001` coder, and injects CLI specs through
  a spec approval REQUEST.
- `pkg/pm/STATES.md` documents WAITING, AWAIT_USER, WORKING, PREVIEW,
  AWAIT_ARCHITECT, DONE, and ERROR semantics.
- `pkg/architect/STATES.md` documents PM spec review in REQUEST state.
- `pkg/tools/spec_submit.go`, `pkg/tools/submit_stories.go`, and PM/architect
  templates implement the spec preview and story submission boundary.

## Consequences

- New spec intake surfaces should route through PM or the shared spec REQUEST
  protocol, not around it.
- Architect should not write application code directly; it reviews specs, plans,
  code, and merges.
- PM owns user asks; Architect owns incidents. Replies should resolve the correct
  durable item type.
- Demo mode should follow bootstrap/project readiness rather than merely checking
  whether a container can run.

## Related Documents

- `README.md`
- `pkg/pm/STATES.md`
- `pkg/architect/STATES.md`
- `docs/PM_BOOTSTRAP.md`
- `docs/PM_BOOTSTRAP_TECHNICAL.md`
- `docs/BOOTSTRAP_SPEC_ZERO.md`
- `docs/HOTFIX_MODE_SPEC.md`
- `docs/DEMO_MODE_SPEC.md`

