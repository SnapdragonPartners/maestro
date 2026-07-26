-- Principal instances: anything that can produce, author, or review
-- (ADR 0021). One record type, three kinds.
--
--   agent  -- carries model, prompt pack, and harness identity (the MPH
--            signature's M, P and H components)
--   human  -- a user account; model is 'human-<user_id>', so two distinct
--            humans are two distinct models and heterogeneity is uniformly
--            checkable with no nulls or side channels (ADR 0020)
--   system -- an Orchestrator component; model is 'system-<component>'.
--            System principals produce Audit artifacts but can NEVER satisfy
--            the Management review invariant, as author or reviewer: per
--            ADR 0019 they perform no inference, so there is no judgment to
--            review or to review with.
BEGIN;

CREATE TABLE principal_instances (
    principal_instance_id uuid        PRIMARY KEY,
    organization_id       uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    kind                  text        NOT NULL,

    -- The M of MPH. For humans and system components this is the
    -- 'human-<user_id>' / 'system-<component>' form, which is why it is
    -- NOT NULL for every kind.
    model                 text        NOT NULL,

    -- Agent-only identity. Null for human and system principals, which is a
    -- real absence rather than an unfilled field.
    agent_type            text,
    prompt_pack_id        text,
    prompt_hash           text,
    harness_config_hash   text,
    maestro_version       text,

    -- Human principals link to their user; agents and system components do
    -- not. The seam derives 'human-<user_id>' from this rather than trusting
    -- a caller to keep the two in step.
    user_id               uuid REFERENCES users (user_id) ON DELETE RESTRICT,

    -- Scope lineage: nullable, because a principal may be organization-wide
    -- (a scheduler) or scoped to a single Story (a Coder).
    feature_id            uuid REFERENCES features (feature_id) ON DELETE RESTRICT,
    epic_id               uuid REFERENCES epics    (epic_id)    ON DELETE RESTRICT,
    story_id              uuid REFERENCES stories  (story_id)   ON DELETE RESTRICT,

    start_time            timestamptz NOT NULL DEFAULT now(),
    stop_time             timestamptz,
    stop_reason           text,

    CONSTRAINT principal_instances_kind_check
        CHECK (kind IN ('agent', 'human', 'system')),

    -- Agent identity belongs to agents. Enforced rather than assumed,
    -- because an "agent_type" on a human principal would silently corrupt
    -- every MPH comparison that groups by it.
    CONSTRAINT principal_instances_agent_fields_check
        CHECK ((kind = 'agent') = (agent_type IS NOT NULL)),

    -- A human principal is a user; a non-human one is not.
    CONSTRAINT principal_instances_human_user_check
        CHECK ((kind = 'human') = (user_id IS NOT NULL)),

    -- stop_reason accompanies stop_time or neither.
    CONSTRAINT principal_instances_stop_check
        CHECK ((stop_time IS NULL) = (stop_reason IS NULL))
);

CREATE INDEX principal_instances_organization_id_idx ON principal_instances (organization_id);
CREATE INDEX principal_instances_story_id_idx        ON principal_instances (story_id);
CREATE INDEX principal_instances_epic_id_idx         ON principal_instances (epic_id);
-- Cost and MPH analysis joins through principals and groups by model
-- (ADR 0021), which is the query this index exists for.
CREATE INDEX principal_instances_model_idx           ON principal_instances (model);

COMMIT;
