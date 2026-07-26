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

    user_id               uuid,

    -- Scope lineage: nullable, because a principal may be organization-wide
    -- (a scheduler) or scoped to a single Story (a Coder). Composite, so a
    -- principal cannot be scoped to another organization's work.
    feature_id            uuid,
    epic_id               uuid,
    story_id              uuid,
    product_id            uuid,

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

    CONSTRAINT principal_instances_user_fkey
        FOREIGN KEY (user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,

    -- A Story-scoped principal carries the whole tuple, so its scope cannot
    -- name a Story from a different Epic.
    CONSTRAINT principal_instances_story_fkey
        FOREIGN KEY (story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT principal_instances_epic_fkey
        FOREIGN KEY (epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT principal_instances_feature_fkey
        FOREIGN KEY (feature_id, product_id, organization_id)
        REFERENCES features (feature_id, product_id, organization_id) ON DELETE RESTRICT,

    -- Scope lineage fills top-down: a Story-scoped principal also names its
    -- Epic and Feature. Without this a partially-filled tuple would slip
    -- past the foreign keys above, which are unchecked when any column is
    -- null (MATCH SIMPLE).
    CONSTRAINT principal_instances_lineage_shape_check
        CHECK (
            (story_id   IS NULL OR epic_id    IS NOT NULL) AND
            (epic_id    IS NULL OR feature_id IS NOT NULL) AND
            (feature_id IS NULL OR product_id IS NOT NULL)
        ),

    CONSTRAINT principal_instances_stop_check
        CHECK ((stop_time IS NULL) = (stop_reason IS NULL)),

    -- Lets artifacts reference an author by (id, organization).
    CONSTRAINT principal_instances_id_org_key UNIQUE (principal_instance_id, organization_id)
);

CREATE INDEX principal_instances_organization_id_idx ON principal_instances (organization_id);
CREATE INDEX principal_instances_story_id_idx        ON principal_instances (story_id);
CREATE INDEX principal_instances_epic_id_idx         ON principal_instances (epic_id);
-- Cost and MPH analysis joins through principals and groups by model
-- (ADR 0021), which is the query this index exists for.
CREATE INDEX principal_instances_model_idx           ON principal_instances (model);

COMMIT;
