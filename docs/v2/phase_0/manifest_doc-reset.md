+++
title = "Manifest: Doc Reset (Phase 0 Item 11)"
edit_date = "2026-07-15"
status = "live"
type = "manifest"
summary = "File-by-file record of the ADR 0017 archive-plan execution: every move to docs/archive/, every deprecated/live front-matter stamp, and every type_slug rename with its reference ripple."
+++

# Manifest: Doc Reset (Phase 0 Item 11)

Status: live (approved by Codex and DR, 2026-07-15). Executes the archive plan fixed in [ADR 0017](../../adr/0017-v2-documentation-authority-and-lifecycle.md): archived documents move to `docs/archive/` preserving filenames (git history preserves original paths; no redirects); the keep list stays in place with `deprecated` front-matter; live docs get naming and front-matter conformance. Files whose disposition was unclear defaulted to archive per the ADR.

**Recorded choice (review round, 2026-07-15):** ADR 0017 is amended alongside this manifest with archive/tooling/asset exemptions — `archive`-status documents carry `title`/`edit_date`/`status` front-matter only (no `summary`: retrieval hooks for no-authority documents are counterproductive); `docs/archive/` maintains a no-authority notice README instead of a per-file index; hidden tooling directories (`.obsidian/`) and asset-only directories (screenshots) maintain no index. Maintained indexes quote each entry's front-matter `summary` verbatim.

## Summary of actions

| Action | Count |
|---|---|
| Archived to `docs/archive/` (from `docs/` root) | 70 |
| Archived to `docs/archive/` (from `docs/specs/`, directory removed) | 44 |
| Screenshots archived to `docs/archive/screenshots/` | 5 |
| Stamped `deprecated` (kept in place, with summaries): ops docs | 14 |
| Stamped `deprecated`: historical ADR notes 0001-0016 | 16 |
| Stamped `deprecated`: `docs/wiki/` | 5 |
| Stamped `archive` front-matter on moved markdown files | 114 |
| Live docs renamed to `type_slug.md` (references updated in 18 files) | 8 |
| Live docs given missing front-matter or `type` field | 8 |
| README indexes created (`docs/`, `docs/wiki/`, `docs/archive/`, `docs/v2/phase_0/`) / regenerated (`docs/v2/`) — entries quote front-matter summaries verbatim | 5 |
| `CLAUDE.md` authority-order entry updated (`docs/specs/` is gone) | 1 |

## Renames (live docs, `type_slug.md` convention)

| From | To |
|---|---|
| `docs/v2/adr-backlog.md` | `docs/v2/notes_adr-backlog.md` |
| `docs/v2/build-process.md` | `docs/v2/process_build.md` |
| `docs/v2/parking-lot.md` | `docs/v2/notes_parking-lot.md` |
| `docs/v2/phase_0/scope-and-plan.md` | `docs/v2/phase_0/plan_scope.md` |
| `docs/v2/provenance-matrix.md` | `docs/v2/notes_provenance-matrix.md` |
| `docs/v2/research-synthesis.md` | `docs/v2/research_synthesis.md` |
| `docs/v2/roadmap.md` | `docs/v2/plan_roadmap.md` |
| `docs/v2/v1-adr-alignment.md` | `docs/v2/notes_v1-adr-alignment.md` |

## Kept `deprecated` at `docs/` root (ADR 0017 keep list)

- `docs/GIT.md`
- `docs/TESTING_STRATEGY.md`
- `docs/MAESTRO_LLMS_MIGRATION.md`
- `docs/ARCHITECT_CONTEXT.md`
- `docs/MAESTRO_CHAT_SPEC.md`
- `docs/HOTFIX_MODE_SPEC.md`
- `docs/MODES.md`
- `docs/AIRPLANE_MODE.md`
- `docs/MAINTENANCE_MODE_SPEC.md`
- `docs/OLLAMA.md`
- `docs/DOC_GRAPH.md`
- `docs/BENCHMARK_HOWTO.md`
- `docs/BENCHMARKS.md`
- `docs/WELCOME_TO_MAESTRO.md`

Plus `docs/adr/0001`-`0016` (historical v1 notes) and `docs/wiki/*` (human-facing set, pending the wiki/docs-site decision) — all stamped `deprecated` with retrieval summaries.

## Archived files

From `docs/` root:

- `AGENTSH_INTEGRATION_SPEC.md`
- `AGENT_LIFECYCLE.md`
- `ARCHITECT_MIGRATION_TODO.md`
- `ARCHITECT_READ_ACCESS_IMPLEMENTATION_SUMMARY.md`
- `ARCHITECT_READ_ACCESS_SPEC.md`
- `ARCHITECT_TOOL_CONFIGURATION.md`
- `BOOTSTRAP_CLEANUP_SPEC.md`
- `BOOTSTRAP_SPEC_REFACTOR.md`
- `BOOTSTRAP_SPEC_ZERO.md`
- `BUDGET_REVIEW_ESCALATION_ISSUE.md`
- `BUDGET_REVIEW_LIMITS_SPEC.md`
- `BUDGET_REVIEW_TEMP.md`
- `BUILD_SERVICE_CONTAINERIZATION_SPEC.md`
- `CODER_CODE_REVIEW.md`
- `CODER_DOCKER_PERMS.md`
- `CODER_EXTERNAL_REFERENCE_ARCHITECTURE_RESEARCH_BRIEF.md`
- `CODER_TOOLLOOP_MIGRATION.md`
- `CONTAINER_SECURITY_SPEC.md`
- `CONTEXT_ISSUE_NOTES.md`
- `CONTEXT_MANAGEMENT.md`
- `DEMO_MODE_SPEC.md`
- `DEVOPS_STORIES.md`
- `DIES.md`
- `DOCKERFILE_PATH_SPEC.md`
- `DURABLE_ASKS_AND_INCIDENTS.md`
- `EMPTY_REPO_INIT.md`
- `FAILURE_RECOVERY_PRODUCTION_TEST_PLAN.md`
- `FAILURE_RECOVERY_V2_SPEC.md`
- `FAILURE_TAXONOMY_SPEC.md`
- `FAILURE_TELEMETRY.md`
- `FIX_TRACKING_RC1.md`
- `GAS_TOWN_COMPARISON.md`
- `GEMINI_INTEGRATION_PLAN.md`
- `KNOWLEDGE_GRAPH_TOOL_SPEC.md`
- `LANGUAGE_PACKS.md`
- `MAESTRO_MACOS_CHANGES.md`
- `MAINTENANCE_LOG_SPEC.md`
- `MERGE_CONFLICT_RESOLUTION_SPEC.md`
- `METRICS_DEMO.md`
- `PASSWORD_VERIFIER_PLAN.md`
- `PAYLOAD_REFACTOR.md`
- `PHI4.md`
- `PM_BOOTSTRAP.md`
- `PM_BOOTSTRAP_TECHNICAL.md`
- `PREVENT_THRASHING.md`
- `PROCESS_EFFECT_POC.md`
- `PROMPT_CACHING_IMPLEMENTATION_PLAN.md`
- `RATE_LIMIT.md`
- `REFACTORING_PLAN.md`
- `REPOSTATE_DESIGN.md`
- `RESILIENCE_IMPROVEMENTS.md`
- `RESUME_MODE_SPEC.md`
- `SECRETS_MANAGER_SPEC.md`
- `SPRINT_NOTES.md`
- `STALLING_FIXES.md`
- `STORIES_COMPLETE_SPEC.md`
- `TODO_FINAL_DECISIONS.md`
- `TODO_LISTS.md`
- `TODO_LISTS_IMPLEMENTATION_STATUS.md`
- `TOOLLOOP_DESIGN.md`
- `TOOLLOOP_REFACTOR_PLAN.md`
- `TOOLS_REFACTOR.md`
- `TOOL_LOOP.md`
- `UNIFIED_CODER_TOOLS_SPEC.md`
- `WEBUI_SETTINGS_SPEC.md`
- `airplane_mode_consolidated_feedback.md`
- `example_app_coding_prompt.md`
- `example_app_coding_prompt_PROPOSED.md`
- `maestro_secrets_spec.md`
- `multi-agent-review-and-toolloop-refactor.md`

From `docs/specs/`:

- `AGENT_TESTING.md`
- `BOOTSTRAP.md`
- `BOOTSTRAP_TODO.md`
- `BOOTSTRAP_UPDATE.md`
- `BUDGET_REVIEW_TODO.md`
- `CLAUDE_CODE.md`
- `CONFIG_REDO.md`
- `CONTEXT_REDO.md`
- `DOCKER_TODO.md`
- `EFFECTS.md`
- `ENHANCED_PLANNING_ARCHITECTURE.md`
- `MAESTRO-REDO.md`
- `MAESTRO_MD_SPEC.md`
- `MERGE_WORKFLOW_IMPLEMENTATION.md`
- `METRICS_POOLING_REQUIREMENTS.md`
- `MIDDLEWARE.md`
- `PHASE2.md`
- `PHASE3.md`
- `PHASE3PRE.md`
- `PHASE4.md`
- `PHASE4OLD.md`
- `PHASE6.md`
- `PLANNING.md`
- `PM.md`
- `PROJECT.md`
- `README_SANDBOX.md`
- `SQLITE.md`
- `STATES.md`
- `STORIES_UPDATE.md`
- `STYLE.md`
- `SWE_EVO_IMPL.md`
- `SWE_EVO_PLAN.md`
- `TESTING.md`
- `TOOLS.md`
- `architect_fsm_stories.md`
- `architect_queue_schema.md`
- `architect_v1_stories.md`
- `auto_checkin_stories.md`
- `channel_refactor_stories_v2.md`
- `coder_agent_improvement_stories.md`
- `coder_stories.md`
- `live_test_spec.md`
- `maestro-ui-v1-stories.md`
- `maestro_container_promotion_plan.md`

## Related Documents

- [ADR 0017](../../adr/0017-v2-documentation-authority-and-lifecycle.md) (the rules and keep list this executes); [Phase 0 plan](plan_scope.md) item 11.
- [docs/archive/README.md](../../archive/README.md) (the no-authority notice).
