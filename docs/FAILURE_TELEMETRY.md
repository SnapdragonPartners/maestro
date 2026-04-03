# Failure Telemetry

Maestro can optionally report structured failure data to the maestro-issues service at session shutdown. This enables learning about real-world failure patterns, recovery effectiveness, and emerging issues across installations.

## Architecture

```
maestro (client)                    maestro-issues (server)
┌──────────────┐                    ┌───────────────────┐
│ FailureRecord│──build report──▶   │ POST /api/v1/     │
│ (SQLite)     │  sanitize          │   telemetry       │
│              │  cap & sign        │                   │
│ SessionSumm. │──────────────▶     │ Firestore:        │
│              │  JSON POST         │   telemetry/      │
└──────────────┘  10s timeout       │   {instID_sessID} │
                                    │                   │
                                    │ Dashboard:        │
                                    │   /dashboard      │
                                    └───────────────────┘
```

### Flow

1. **Graceful shutdown** (after session status update): Query all failures for the session, build a sanitized report, POST to maestro-issues, mark session as sent via marker file.

2. **Startup retry**: On next startup (fresh or resume), check for terminal sessions (shutdown/crashed) that haven't sent telemetry. Send and mark. This captures crash-path failures that never hit graceful shutdown.

3. **Idempotency**: Firestore document ID is `installationID_sessionID`. Uses `Set` (not `Create`), so resending the same session overwrites rather than duplicating.

## Opt-in

Telemetry is disabled by default. Enable via config:

```json
{
  "telemetry_enabled": true
}
```

## Payload Format

`POST /api/v1/telemetry` with JSON body:

```json
{
  "installation_id": "uuid",
  "signature": "hmac-hex",
  "maestro_version": "v1.2.3",
  "session_id": "uuid",
  "session_summary": {
    "started_at": "2026-04-01T10:00:00Z",
    "ended_at": "2026-04-01T11:30:00Z",
    "session_status": "shutdown",
    "stories_total": 5,
    "stories_completed": 3,
    "stories_failed": 1,
    "stories_held": 1
  },
  "failures": [
    {
      "id": "fail-uuid",
      "kind": "test_failure",
      "source": "auto_classifier",
      "scope_guess": "story",
      "resolved_scope": "story",
      "failed_state": "TESTING",
      "tool_name": "",
      "action": "requeue",
      "resolution_status": "resolved",
      "resolution_outcome": "succeeded",
      "explanation": "Tests failed due to missing import",
      "evidence": [
        {
          "kind": "test_output",
          "summary": "pattern match: missing import",
          "snippet": "Error: cannot find module..."
        }
      ],
      "model": "claude-sonnet-4-20250514",
      "provider": "anthropic"
    }
  ],
  "truncated": false,
  "overflow_counts": {}
}
```

### Size Caps

- **Entry limit**: 100 failures per report. Excess entries are counted in `overflow_counts` by kind.
- **Byte limit**: 256 KB total serialized size. If exceeded, failures are trimmed from the end.
- When either cap triggers, `truncated` is set to `true`.

## Sanitization

All text fields are sanitized before transmission using `pkg/utils/sanitize.go`:

- **Secret patterns**: API keys, tokens, passwords, credentials, AWS keys, provider-prefixed keys are replaced with `[REDACTED]`
- **Path normalization**: `/Users/username/` and `/home/username/` are replaced with `<user>/`
- **Length truncation**: Explanations capped at 2000 chars, evidence snippets at 1000 chars

Sanitization happens at both evidence capture time (belt) and telemetry send time (suspenders).

## Authentication

Reports are signed with HMAC-SHA256 over the `installation_id` using the shared secret compiled into the binary. This is installation-level auth, not payload-integrity auth -- it verifies the sender is a legitimate maestro installation.

## Marker Files

Telemetry sent status is tracked via marker files at `.maestro/telemetry-sent/<sessionID>`. This avoids schema changes and works across crashes.

## Key Files

| File | Purpose |
|------|---------|
| `pkg/config/config.go` | `TelemetryEnabled` config field |
| `pkg/telemetry/sender.go` | Report struct, BuildReport, SendReport |
| `pkg/utils/sanitize.go` | Shared sanitization |
| `pkg/issueservice/client.go` | HMAC signing, base URL resolution |
| `pkg/persistence/failure_ops.go` | QueryFailuresBySession, QuerySessionSummary |
| `cmd/maestro/flows.go` | Shutdown hook, startup retry |

## Related

- maestro-issues: See `docs/TELEMETRY.md` for server-side endpoint spec and dashboard docs
- `docs/FAILURE_TAXONOMY_SPEC.md` for failure kind definitions
- `docs/FAILURE_RECOVERY_V2_SPEC.md` for the recovery system that produces these records
