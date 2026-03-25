# Secrets Manager Specification

## Overview

The Secrets Manager provides per-project, user-defined secrets that are injected as environment variables into all coding and demo containers. It extends the existing encrypted secrets store to support two categories — **system secrets** (used by Maestro on the host) and **user secrets** (injected into containers for application use) — both manageable through the WebUI.

## Problem

Users need to provide API keys, database URLs, certificates, and other sensitive configuration to their application containers. Currently there is no mechanism to define arbitrary secrets that get bound to containers. The existing encrypted secrets store only supports a predefined set of Maestro operational secrets (LLM API keys, GitHub token, SSL key) with no UI for management.

## Design

### Two Secret Categories

| Category | Purpose | Container Injection | Name Constraints |
|----------|---------|--------------------|--------------------|
| **System** | Maestro operational secrets (LLM API keys, GitHub token, SSL) | NOT injected (exception: Claude Code ANTHROPIC_API_KEY) | Predefined allowlist only |
| **User** | Application secrets for coding/demo containers | ALL injected as env vars | Any valid env var name `^[a-zA-Z_][a-zA-Z0-9_]*$` |

### Precedence

**User > System > Environment.**

When resolving a secret by name:
1. User secrets checked first
2. System secrets checked second
3. Host environment variables checked last

This allows users to override system-level keys for their application (e.g., a different `ANTHROPIC_API_KEY` for their LLM-based app vs. the one Maestro uses internally).

### Storage

Both categories stored in the same AES-256-GCM encrypted file (`.maestro/secrets.json.enc`), using a structured JSON format:

```json
{
  "system": {
    "ANTHROPIC_API_KEY": "sk-ant-...",
    "OPENAI_API_KEY": "sk-..."
  },
  "user": {
    "STRIPE_API_KEY": "sk_live_...",
    "DATABASE_URL": "postgres://..."
  }
}
```

This replaces the previous flat `map[string]string` format. No migration is needed — no existing deployments use the encrypted file yet.

### System Secret Allowlist

Only these names are valid for system secrets:

- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`
- `GOOGLE_GENAI_API_KEY`
- `GITHUB_TOKEN`
- `SSL_KEY_PEM`

Attempting to set a system secret with any other name returns an error.

### Container Injection

User secrets are injected at container **start time** only. Changes take effect on next container start/switch — no mid-stream injection.

**Injection points:**

| Container Type | Mechanism | File |
|----------------|-----------|------|
| Coder (standard) | `opts.Env` → `--env` flags | `pkg/coder/setup.go`, `pkg/coder/driver.go` |
| Coder (Claude Code) | `opts.EnvVars` map | `pkg/coder/claudecode_planning.go`, `pkg/coder/claudecode_coding.go` |
| Demo | `--env` flags in docker run args | `pkg/demo/service.go` |

All injection sites call `config.GetUserSecrets()` which returns a thread-safe copy.

For Claude Code mode, system `ANTHROPIC_API_KEY` is set as a baseline, then user secrets overlay on top (so a user-defined `ANTHROPIC_API_KEY` wins in the container).

## API

Existing `/api/secrets` endpoints extended with a `type` parameter:

### GET /api/secrets?type=user|system

Returns secret names and types (values never returned):

```json
[
  { "name": "STRIPE_API_KEY", "type": "user" },
  { "name": "ANTHROPIC_API_KEY", "type": "system" }
]
```

Optional `type` query param filters results. Omitting returns all.

### POST /api/secrets

```json
{
  "name": "STRIPE_API_KEY",
  "value": "sk_live_...",
  "type": "user"
}
```

- `type` defaults to `"user"` if omitted
- `type=system` validates name against allowlist
- User secret names validated against `^[a-zA-Z_][a-zA-Z0-9_]*$`

### DELETE /api/secrets/:name?type=user

- `type` query param defaults to `"user"`

## WebUI

A "Secrets" button in the header opens a modal with:

- **Tabbed interface**: User Secrets / System Secrets tabs
- **Info banners**: "User secrets are injected as environment variables into all coding and demo containers" / "System secrets are used by Maestro on the host and are NOT injected into containers"
- **Secret list**: Names only with delete buttons (values are write-only, never displayed)
- **Add form**:
  - User tab: Free-text name input + value textarea
  - System tab: Dropdown of known system secret names + value textarea

## Implementation Phases

### Phase 1: Data Model (`pkg/config/secrets.go`)
- `StructuredSecrets` struct with `System` and `User` maps
- `SecretType` constants and `SystemSecretNames` allowlist
- Update all functions: `SetDecryptedSecrets`, `GetSecret`, `SetSecret`, `DeleteSecret`, `GetDecryptedSecretNames`, `EncryptSecretsFile`, `DecryptSecretsFile`, `SaveSecretsToFile`
- New: `GetUserSecrets()`, `ValidateSecretName()`
- Update caller in `cmd/maestro/flows.go`

### Phase 2: API Endpoints (`pkg/webui/secrets_handlers.go`)
- Add type filtering to GET handler
- Add type field to POST handler with system name validation
- Add type query param to DELETE handler

### Phase 3: Coder Container Injection (`pkg/coder/setup.go`, `pkg/coder/driver.go`)
- Append user secrets to `execOpts.Env` before container start/switch

### Phase 4: Demo Container Injection (`pkg/demo/service.go`)
- Add `--env` flags for user secrets in `runContainerWithNetwork()`

### Phase 5: Claude Code Mode Injection (`pkg/coder/claudecode_planning.go`, `claudecode_coding.go`)
- Set system ANTHROPIC_API_KEY baseline, overlay user secrets

### Phase 6: Web UI (`base.html`, `maestro.js`)
- Secrets button in header, modal with tabbed interface
- JS methods for CRUD operations

### Phase 7: Tests
- Update existing tests for new `StructuredSecrets` type
- Add tests for system name validation, type filtering, user-only retrieval

## Files Modified

| File | Change |
|------|--------|
| `pkg/config/secrets.go` | StructuredSecrets type, all function signatures |
| `pkg/config/secrets_test.go` | Update + new tests |
| `cmd/maestro/flows.go` | Update DecryptSecretsFile/SetDecryptedSecrets call |
| `pkg/webui/secrets_handlers.go` | Type param handling, system name validation |
| `pkg/webui/secrets_handlers_test.go` | Update + new tests |
| `pkg/coder/setup.go` | Inject user secrets into execOpts.Env |
| `pkg/coder/driver.go` | Inject user secrets in SwitchContainer |
| `pkg/coder/claudecode_planning.go` | Merge user secrets into EnvVars |
| `pkg/coder/claudecode_coding.go` | Merge user secrets into EnvVars |
| `pkg/demo/service.go` | Add --env flags for user secrets |
| `pkg/webui/web/templates/base.html` | Secrets button + modal HTML |
| `pkg/webui/web/static/js/maestro.js` | Secrets management JS |

## Verification

1. `make build` — Compiles with all signature changes
2. `make test` — All updated + new unit tests pass
3. Manual: Start orchestrator, open WebUI, click Secrets button
4. Manual: Add a user secret via UI, start a coding container, verify env var is present
5. Manual: Add a user secret, start demo, verify env var in demo container
6. Manual: Add a system secret via UI (dropdown), verify stored but NOT injected into containers
