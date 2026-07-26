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
    -- User lineage. NOT derivable from the principal: only human principals
    -- carry a user_id, so an agent's calls would be unattributable in team
    -- mode -- and these tables are exactly where cost and action
    -- attribution are reconstructed. Nullable because Orchestrator-initiated
    -- work genuinely has no user behind it; queries must handle that rather
    -- than assume one.
    user_id               uuid,
    principal_instance_id uuid        NOT NULL,
    -- Whole-tuple work lineage, like the artifact tables. Independent
    -- single-column story/epic foreign keys accepted a call whose Story
    -- belonged to a different Epic, or to another organization entirely --
    -- and cost analysis groups by exactly these columns, so an inconsistent
    -- one silently misattributes spend.
    product_id            uuid,
    feature_id            uuid,
    epic_id               uuid,
    story_id              uuid,


    -- An always-non-null encoding of the work tuple. A composite foreign
    -- key over the nullable lineage columns would be SKIPPED whenever any
    -- of them is null (MATCH SIMPLE) -- which is the common case -- so the
    -- provenance check below would silently not apply. Encoding the tuple
    -- into one non-null value makes it always enforced.
    -- user_id is part of the key, not just the work tuple: without it an
    -- LLM call and the tool call claiming it could name DIFFERENT
    -- accountable users while passing the provenance check, which is
    -- precisely the attribution the link exists to make trustworthy.
    lineage_key text GENERATED ALWAYS AS (
        coalesce(user_id::text,    '') || '/' ||
        coalesce(product_id::text, '') || '/' ||
        coalesce(feature_id::text, '') || '/' ||
        coalesce(epic_id::text,    '') || '/' ||
        coalesce(story_id::text,   '')
    ) STORED,

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


    -- Lineage fills top-down; without this a partially-filled tuple slips
    -- past the foreign keys below, which are unchecked when any column is
    -- null (MATCH SIMPLE).
    CONSTRAINT llm_calls_lineage_shape_check
        CHECK (
            (story_id   IS NULL OR epic_id    IS NOT NULL) AND
            (epic_id    IS NULL OR feature_id IS NOT NULL) AND
            (feature_id IS NULL OR product_id IS NOT NULL)
        ),

    CONSTRAINT llm_calls_story_lineage_fkey
        FOREIGN KEY (story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT llm_calls_epic_lineage_fkey
        FOREIGN KEY (epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT llm_calls_feature_lineage_fkey
        FOREIGN KEY (feature_id, product_id, organization_id)
        REFERENCES features (feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT llm_calls_product_lineage_fkey
        FOREIGN KEY (product_id, organization_id)
        REFERENCES products (product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT llm_calls_user_fkey
        FOREIGN KEY (user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT llm_calls_principal_fkey
        FOREIGN KEY (principal_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT llm_calls_tokens_nonnegative_check
        CHECK (input_tokens >= 0 AND output_tokens >= 0
               AND reasoning_tokens >= 0 AND cached_tokens >= 0),
    CONSTRAINT llm_calls_cost_nonnegative_check
        CHECK (cost_usd IS NULL OR cost_usd >= 0),

    CONSTRAINT llm_calls_id_org_key UNIQUE (llm_call_id, organization_id),

    -- The target of tool_calls' provenance foreign key: a tool call may
    -- only claim an LLM call made by the SAME principal for the SAME work.
    CONSTRAINT llm_calls_provenance_key
        UNIQUE (llm_call_id, principal_instance_id, lineage_key, organization_id)
);

CREATE INDEX llm_calls_principal_idx  ON llm_calls (principal_instance_id);
CREATE INDEX llm_calls_story_id_idx   ON llm_calls (story_id);
CREATE INDEX llm_calls_started_at_idx ON llm_calls (started_at);
CREATE INDEX llm_calls_model_idx      ON llm_calls (model);

CREATE TABLE tool_calls (
    tool_call_id          uuid        PRIMARY KEY,
    organization_id       uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    user_id               uuid,
    principal_instance_id uuid        NOT NULL,

    -- The originating LLM call, when there was one. Nullable because the
    -- Orchestrator also invokes tools on its own account (ADR 0019: rules,
    -- not inference), and recording a null is honest where inventing a
    -- parent would not be.
    llm_call_id           uuid,

    -- Whole-tuple work lineage, like the artifact tables. Independent
    -- single-column story/epic foreign keys accepted a call whose Story
    -- belonged to a different Epic, or to another organization entirely --
    -- and cost analysis groups by exactly these columns, so an inconsistent
    -- one silently misattributes spend.
    product_id            uuid,
    feature_id            uuid,
    epic_id               uuid,
    story_id              uuid,


    -- An always-non-null encoding of the work tuple. A composite foreign
    -- key over the nullable lineage columns would be SKIPPED whenever any
    -- of them is null (MATCH SIMPLE) -- which is the common case -- so the
    -- provenance check below would silently not apply. Encoding the tuple
    -- into one non-null value makes it always enforced.
    -- user_id is part of the key, not just the work tuple: without it an
    -- LLM call and the tool call claiming it could name DIFFERENT
    -- accountable users while passing the provenance check, which is
    -- precisely the attribution the link exists to make trustworthy.
    lineage_key text GENERATED ALWAYS AS (
        coalesce(user_id::text,    '') || '/' ||
        coalesce(product_id::text, '') || '/' ||
        coalesce(feature_id::text, '') || '/' ||
        coalesce(epic_id::text,    '') || '/' ||
        coalesce(story_id::text,   '')
    ) STORED,

    tool_name             text        NOT NULL,
    arguments             jsonb       NOT NULL,
    result                jsonb,
    -- Null while in flight; set on completion. An unfinished call is a real
    -- state (the process died), not a missing field.
    succeeded             boolean,
    error_message         text,

    started_at            timestamptz NOT NULL DEFAULT now(),
    finished_at           timestamptz,


    -- Lineage fills top-down; without this a partially-filled tuple slips
    -- past the foreign keys below, which are unchecked when any column is
    -- null (MATCH SIMPLE).
    CONSTRAINT tool_calls_lineage_shape_check
        CHECK (
            (story_id   IS NULL OR epic_id    IS NOT NULL) AND
            (epic_id    IS NULL OR feature_id IS NOT NULL) AND
            (feature_id IS NULL OR product_id IS NOT NULL)
        ),

    CONSTRAINT tool_calls_story_lineage_fkey
        FOREIGN KEY (story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT tool_calls_epic_lineage_fkey
        FOREIGN KEY (epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT tool_calls_feature_lineage_fkey
        FOREIGN KEY (feature_id, product_id, organization_id)
        REFERENCES features (feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT tool_calls_product_lineage_fkey
        FOREIGN KEY (product_id, organization_id)
        REFERENCES products (product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT tool_calls_user_fkey
        FOREIGN KEY (user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT tool_calls_principal_fkey
        FOREIGN KEY (principal_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT,

    -- Provenance is only meaningful if the claimed parent is actually this
    -- call's parent. Matching on id and organization alone would let a tool
    -- call attribute itself to another principal's LLM call, or to the same
    -- principal's work on a different Story -- and provenance that can name
    -- the wrong parent is worse than none, because it reads as evidence.
    CONSTRAINT tool_calls_llm_call_fkey
        FOREIGN KEY (llm_call_id, principal_instance_id, lineage_key, organization_id)
        REFERENCES llm_calls (llm_call_id, principal_instance_id, lineage_key, organization_id) ON DELETE RESTRICT,

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
    user_id               uuid,
    principal_instance_id uuid,
    -- Whole-tuple work lineage, like the artifact tables. Independent
    -- single-column story/epic foreign keys accepted a call whose Story
    -- belonged to a different Epic, or to another organization entirely --
    -- and cost analysis groups by exactly these columns, so an inconsistent
    -- one silently misattributes spend.
    product_id            uuid,
    feature_id            uuid,
    epic_id               uuid,
    story_id              uuid,

    metric_name           text             NOT NULL,
    value                 double precision NOT NULL,
    labels                jsonb            NOT NULL DEFAULT '{}'::jsonb,
    recorded_at           timestamptz      NOT NULL DEFAULT now(),


    -- Lineage fills top-down; without this a partially-filled tuple slips
    -- past the foreign keys below, which are unchecked when any column is
    -- null (MATCH SIMPLE).
    CONSTRAINT metric_events_lineage_shape_check
        CHECK (
            (story_id   IS NULL OR epic_id    IS NOT NULL) AND
            (epic_id    IS NULL OR feature_id IS NOT NULL) AND
            (feature_id IS NULL OR product_id IS NOT NULL)
        ),

    CONSTRAINT metric_events_story_lineage_fkey
        FOREIGN KEY (story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT metric_events_epic_lineage_fkey
        FOREIGN KEY (epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT metric_events_feature_lineage_fkey
        FOREIGN KEY (feature_id, product_id, organization_id)
        REFERENCES features (feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT metric_events_product_lineage_fkey
        FOREIGN KEY (product_id, organization_id)
        REFERENCES products (product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT metric_events_user_fkey
        FOREIGN KEY (user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT metric_events_principal_fkey
        FOREIGN KEY (principal_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT
);

CREATE INDEX metric_events_name_time_idx ON metric_events (metric_name, recorded_at);
CREATE INDEX metric_events_story_id_idx  ON metric_events (story_id);

CREATE TABLE audit_events (
    audit_event_id        uuid        PRIMARY KEY,
    organization_id       uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    user_id               uuid,
    principal_instance_id uuid,

    event_type            text        NOT NULL,
    detail                jsonb       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at           timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT audit_events_user_fkey
        FOREIGN KEY (user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT audit_events_principal_fkey
        FOREIGN KEY (principal_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT
);

CREATE INDEX audit_events_type_time_idx ON audit_events (event_type, occurred_at);

COMMIT;
