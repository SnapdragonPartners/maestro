+++
title = "ADR 0015: Agent Chat and Human-in-the-Loop Escalation"
edit_date = "2026-07-15"
status = "deprecated"
summary = "v1 agent chat and human-in-the-loop escalation."
+++

# ADR 0015: Agent Chat and Human-in-the-Loop Escalation

- Status: Proposed
- Date: 2026-07-06

## Context

Agents and humans need a shared, persistent communication channel that is separate
from the typed inter-agent protocol (ADR 0004). Agents post progress and questions;
humans reply through the WebUI; and when an agent exhausts its iteration budget, a
human must be pulled in to unblock it. This channel also carries free-form text that
could accidentally contain secrets, so it needs scanning before persistence.

## Decision

Provide a first-class chat service, persisted in the project SQLite database and
session-isolated, with these properties:

- Agents post via a `chat_post` tool; reading is optional (`chat_read`), because new
  messages are automatically injected into each LLM call.
- Messages are typed by `PostType` (`chat`, `reply`, `escalate`). Escalation messages
  (`escalate`) are surfaced prominently in the WebUI with a reply affordance so a
  human can provide guidance.
- Messages are passed through a secret scanner before storage when scanning is
  enabled. Scanner errors and timeouts are logged and fail open by storing the
  original text.
- Confirmation-related operational messages are excluded from LLM-context injection.

Escalation is the bridge between the toolloop iteration limits (ADR 0006) and human
intervention: hitting a hard limit posts an escalation and waits for a human reply.

## Current Implementation

- `pkg/chat/service.go` defines the chat service, `PostType` constants
  (`PostTypeEscalate = "escalate"`), reply lookup, cursors, and confirmation post
  type filtering.
- `pkg/chat/scanner.go` implements the `SecretScanner` used before persistence.
- `pkg/agent/middleware/chat/injection.go` injects new chat messages into LLM calls
  and filters confirmation post types from injected context.
- Chat is configured under the `chat` section of `config.json`
  (`enabled`, `max_new_messages`, `limits.max_message_chars`, `scanner.*`).
- Toolloop hard-limit escalation posts to chat and waits for human guidance
  (see ADR 0006 and `docs/MAESTRO_CHAT_SPEC.md`).

## Consequences

- New agent-visible collaboration should use the chat channel, not new bespoke
  side channels; the typed protocol (ADR 0004) remains the path for structured
  work routing.
- Any new chat post type must decide whether it should be injected into LLM context
  and whether it needs WebUI prominence.
- Secret scanning is part of the persistence contract; do not bypass it for new
  chat write paths.
- Chat volume affects prompt size because messages are auto-injected;
  `max_new_messages` is a real cost/latency knob, not just a limit.

## Related Documents

- `docs/MAESTRO_CHAT_SPEC.md`
- `CLAUDE.md` (Agent Chat System, Escalation Support)
- ADR 0004 (typed agent protocol)
- ADR 0006 (toolloop iteration limits and escalation)
