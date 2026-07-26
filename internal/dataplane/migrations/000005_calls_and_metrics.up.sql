-- LLM calls, tool calls, metric events, audit events.
--
-- ADR 0022 refines ADR 0021's Audit enumeration: the TOOL CALL is the
-- atomic Audit ACTION unit -- an LLM call that produces no tool call does
-- nothing. LLM call records exist for token/cost accounting (which is
-- metrics) and optional trace debugging.
--
-- The guardrail this encodes: any LLM output that creates artifacts,
-- decisions, or state transitions must pass through a tool/action record,
-- so parsed free-text can never be a side door. That claim is only
-- DEMONSTRABLE if the chain is recorded, which is why a tool call links
-- back to its originating LLM call, and why artifacts (next migration)
-- link back to the tool call that produced them. Without those links the
-- rule is an assertion nobody can audit.
--
-- LLM calls come first because tool calls reference them.
BEGIN;

CREATE TABLE llm_calls (
    llm_call_id           uuid        PRIMARY KEY,
    organization_id       uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    principal_instance_id uuid        NOT NULL,
    story_id              uuid        REFERENCES stories (story_id) ON DELETE RESTRICT,

    provider              text        NOT NULL,
    model                 text        NOT NULL,

    input_tokens          bigint      NOT NULL DEFAULT 0,
    output_tokens         bigint      NOT NULL DEFAULT 0,
    reasoning_tokens      bigint      NOT NULL DEFAULT 0,
    cached_tokens         bigint      NOT NULL DEFAULT 0,

    -- Nullable rather than zero: a local model has no modelled cost, and
    -- Phase 1 learned that pricing an unknown model at zero is worse than
    -- admitting it is unavailable. The distinction here is "unknown" versus
    -- "free", which a zero cannot express.
    cost_usd              numeric(18, 8),

    started_at            timestamptz NOT NULL DEFAULT now(),
    finished_at           timestamptz,

    CONSTRAINT llm_calls_principal_fkey
        FOREIGN KEY (principal_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT llm_calls_tokens_nonnegative_check
        CHECK (input_tokens >= 0 AND output_tokens >= 0
               AND reasoning_tokens >= 0 AND cached_tokens >= 0),
    CONSTRAINT llm_calls_cost_nonnegative_check
        CHECK (cost_usd IS NULL OR cost_usd >= 0),

    CONSTRAINT llm_calls_id_org_key UNIQUE (llm_call_id, organization_id)
);

CREATE INDEX llm_calls_principal_idx  ON llm_calls (principal_instance_id);
CREATE INDEX llm_calls_story_id_idx   ON llm_calls (story_id);
CREATE INDEX llm_calls_started_at_idx ON llm_calls (started_at);
CREATE INDEX llm_calls_model_idx      ON llm_calls (model);

CREATE TABLE tool_calls (
    tool_call_id          uuid        PRIMARY KEY,
    organization_id       uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    principal_instance_id uuid        NOT NULL,

    -- The originating LLM call, when there was one. Nullable because the
    -- Orchestrator also invokes tools on its own account (ADR 0019: rules,
    -- not inference), and recording a null is honest where inventing a
    -- parent would not be.
    llm_call_id           uuid,

    story_id              uuid        REFERENCES stories (story_id) ON DELETE RESTRICT,
    epic_id               uuid        REFERENCES epics   (epic_id)  ON DELETE RESTRICT,

    tool_name             text        NOT NULL,
    arguments             jsonb       NOT NULL,
    result                jsonb,
    -- Null while in flight; set on completion. An unfinished call is a real
    -- state (the process died), not a missing field.
    succeeded             boolean,
    error_message         text,

    started_at            timestamptz NOT NULL DEFAULT now(),
    finished_at           timestamptz,

    CONSTRAINT tool_calls_principal_fkey
        FOREIGN KEY (principal_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT tool_calls_llm_call_fkey
        FOREIGN KEY (llm_call_id, organization_id)
        REFERENCES llm_calls (llm_call_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT tool_calls_finished_check
        CHECK ((finished_at IS NULL) = (succeeded IS NULL)),

    CONSTRAINT tool_calls_id_org_key UNIQUE (tool_call_id, organization_id)
);

CREATE INDEX tool_calls_principal_idx  ON tool_calls (principal_instance_id);
CREATE INDEX tool_calls_llm_call_idx   ON tool_calls (llm_call_id);
CREATE INDEX tool_calls_story_id_idx   ON tool_calls (story_id);
CREATE INDEX tool_calls_started_at_idx ON tool_calls (started_at);
CREATE INDEX tool_calls_tool_name_idx  ON tool_calls (tool_name);

CREATE TABLE metric_events (
    metric_event_id       uuid             PRIMARY KEY,
    organization_id       uuid             NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    principal_instance_id uuid,
    story_id              uuid             REFERENCES stories (story_id) ON DELETE RESTRICT,

    metric_name           text             NOT NULL,
    value                 double precision NOT NULL,
    labels                jsonb            NOT NULL DEFAULT '{}'::jsonb,
    recorded_at           timestamptz      NOT NULL DEFAULT now(),

    CONSTRAINT metric_events_principal_fkey
        FOREIGN KEY (principal_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT
);

CREATE INDEX metric_events_name_time_idx ON metric_events (metric_name, recorded_at);
CREATE INDEX metric_events_story_id_idx  ON metric_events (story_id);

CREATE TABLE audit_events (
    audit_event_id        uuid        PRIMARY KEY,
    organization_id       uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    principal_instance_id uuid,

    event_type            text        NOT NULL,
    detail                jsonb       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at           timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT audit_events_principal_fkey
        FOREIGN KEY (principal_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT
);

CREATE INDEX audit_events_type_time_idx ON audit_events (event_type, occurred_at);

COMMIT;
