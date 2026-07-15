+++
title = "ADR 0011: Configuration, Operating Modes, and Secrets"
edit_date = "2026-07-15"
status = "deprecated"
summary = "v1 configuration, operating modes, and secrets handling; superseded for v2 intent by the project-folder spike and ADR 0022 as amended."
+++

# ADR 0011: Configuration, Operating Modes, and Secrets

- Status: Proposed
- Date: 2026-07-06

## Context

Maestro must load per-project settings, support local WebUI setup, handle standard
and airplane operation, persist selected runtime settings, and protect API keys and
user application secrets. Older docs include several config plans; current code has
a singleton config package with atomic update helpers and an encrypted structured
secrets store.

## Decision

Use `.maestro/config.json` as the project configuration file and keep runtime state
out of config unless it is intentionally persisted user/project preference.

Configuration rules:

- `pkg/config` owns load, validation, defaulting, and atomic subsystem updates.
- `GetConfig()` returns a value copy. Mutations go through update helpers.
- Config schema changes require deliberate versioning and validation updates.
- CLI flags may override selected startup choices, such as airplane mode.

Operating modes:

- Standard mode uses external providers and GitHub.
- Airplane mode uses local Gitea and local/Ollama-compatible models where configured.
- Sync mode pushes airplane-mode output back toward GitHub and exits.
- Run mode starts application dependencies/app execution without the orchestrator.

Secrets rules:

- System secrets are for Maestro host operation.
- User secrets are application secrets injected into coding/demo containers.
- Encrypted secrets live at `.maestro/secrets.json.enc`.
- The project password protects WebUI login and secrets encryption.
- Environment variables remain a supported source, with `MAESTRO_`-prefixed system
  secrets taking precedence for Maestro use.

## Current Implementation

- `pkg/config/config.go` documents singleton config principles and implements
  defaulting, validation, operating mode, forge provider selection, and atomic
  updates.
- `pkg/config/secrets.go` defines `StructuredSecrets`, system/user secret categories,
  encryption/decryption, migration from legacy flat maps, and `GetUserSecrets()`.
- `cmd/maestro/main.go` establishes the project password, handles password verifier
  recovery, decrypts secrets when possible, resolves operating mode, and branches
  to main/resume/sync/run flows.
- `pkg/webui/secrets_handlers.go` exposes WebUI secret management.
- Coder, Claude Code, and demo paths inject user secrets through `config.GetUserSecrets()`.

## Consequences

- Do not add build timestamps, transient health, or agent progress to config; use
  SQLite or runtime state.
- System secret allowlists and user secret validation should stay strict enough to
  prevent obvious accidental leakage without turning this local app into an
  enterprise secrets system.
- Any new mode should define its forge/provider/container implications explicitly.
- Config docs that predate airplane mode, structured secrets, or the password
  verifier need confirmation against code before use.

## Related Documents

- `README.md`
- `docs/WEBUI_SETTINGS_SPEC.md`
- `docs/SECRETS_MANAGER_SPEC.md`
- `docs/maestro_secrets_spec.md`
- `docs/PASSWORD_VERIFIER_PLAN.md`
- `docs/AIRPLANE_MODE.md`
- `docs/OLLAMA.md`
- `docs/specs/CONFIG_REDO.md`

