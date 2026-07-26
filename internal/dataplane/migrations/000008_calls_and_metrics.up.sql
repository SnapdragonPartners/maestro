-- Tool calls, LLM calls, metric events, audit events.
--
-- ADR 0022 refines ADR 0021's Audit enumeration: the TOOL CALL is the
-- atomic Audit action unit -- an LLM call that produces no tool call does
-- nothing. LLM call records exist for token/cost accounting (which is
-- metrics) and optional trace debugging.
--
-- The guardrail this encodes: any LLM output that creates artifacts,
-- decisions, or state transitions must pass through a tool/action record.
-- Parsed free-text output can never be a side door. That is v1's
-- terminal-tool discipline promoted to a data-plane rule.
BEGIN;

CREATE TABLE tool_calls (
    tool_call_id       uuid        PRIMARY KEY,
    organization_id    uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    principal_instance_id uuid     NOT NULL REFERENCES principal_instances (principal_instance_id) ON DELETE RESTRICT,
    story_id           uuid        REFERENCES stories (story_id) ON DELETE RESTRICT,
    epic_id            uuid        REFERENCES epics   (epic_id)  ON DELETE RESTRICT,

    tool_name          text        NOT NULL,
    arguments          jsonb       NOT NULL,
    result             jsonb,
    -- Null while in flight; set on completion. An unfinished call is a real
    -- state (the process died), not a missing field.
    succeeded          boolean,
    error_message      text,

    started_at         timestamptz NOT NULL DEFAULT now(),
    finished_at        timestamptz,

    CONSTRAINT tool_calls_finished_check
        CHECK ((finished_at IS NULL) = (succeeded IS NULL))
);

CREATE INDEX tool_calls_principal_idx  ON tool_calls (principal_instance_id);
CREATE INDEX tool_calls_story_id_idx   ON tool_calls (story_id);
CREATE INDEX tool_calls_started_at_idx ON tool_calls (started_at);
CREATE INDEX tool_calls_tool_name_idx  ON tool_calls (tool_name);

CREATE TABLE llm_calls (
    llm_call_id        uuid        PRIMARY KEY,
    organization_id    uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    principal_instance_id uuid     NOT NULL REFERENCES principal_instances (principal_instance_id) ON DELETE RESTRICT,
    story_id           uuid        REFERENCES stories (story_id) ON DELETE RESTRICT,

    provider           text        NOT NULL,
    model              text        NOT NULL,

    input_tokens       bigint      NOT NULL DEFAULT 0,
    output_tokens      bigint      NOT NULL DEFAULT 0,
    reasoning_tokens   bigint      NOT NULL DEFAULT 0,
    cached_tokens      bigint      NOT NULL DEFAULT 0,

    -- Cost is nullable rather than zero: a local model has no modelled
    -- cost, and Phase 1 learned the hard way that pricing an unknown model
    -- at zero is worse than admitting it is unavailable. Four-state metric
    -- semantics live in the runner; here the distinction that matters is
    -- "unknown" versus "free".
    cost_usd           numeric(18, 8),

    started_at         timestamptz NOT NULL DEFAULT now(),
    finished_at        timestamptz,

    CONSTRAINT llm_calls_tokens_nonnegative_check
        CHECK (input_tokens >= 0 AND output_tokens >= 0
               AND reasoning_tokens >= 0 AND cached_tokens >= 0),
    CONSTRAINT llm_calls_cost_nonnegative_check
        CHECK (cost_usd IS NULL OR cost_usd >= 0)
);

CREATE INDEX llm_calls_principal_idx  ON llm_calls (principal_instance_id);
CREATE INDEX llm_calls_story_id_idx   ON llm_calls (story_id);
CREATE INDEX llm_calls_started_at_idx ON llm_calls (started_at);
CREATE INDEX llm_calls_model_idx      ON llm_calls (model);

CREATE TABLE metric_events (
    metric_event_id    uuid        PRIMARY KEY,
    organization_id    uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    principal_instance_id uuid     REFERENCES principal_instances (principal_instance_id) ON DELETE RESTRICT,
    story_id           uuid        REFERENCES stories (story_id) ON DELETE RESTRICT,

    metric_name        text        NOT NULL,
    value              double precision NOT NULL,
    labels             jsonb       NOT NULL DEFAULT '{}'::jsonb,
    recorded_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX metric_events_name_time_idx ON metric_events (metric_name, recorded_at);
CREATE INDEX metric_events_story_id_idx  ON metric_events (story_id);

CREATE TABLE audit_events (
    audit_event_id     uuid        PRIMARY KEY,
    organization_id    uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    principal_instance_id uuid     REFERENCES principal_instances (principal_instance_id) ON DELETE RESTRICT,

    event_type         text        NOT NULL,
    detail             jsonb       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_type_time_idx ON audit_events (event_type, occurred_at);

COMMIT;
