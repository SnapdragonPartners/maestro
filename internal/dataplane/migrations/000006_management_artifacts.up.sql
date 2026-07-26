-- Management artifacts: the review-bearing inputs (ADR 0021).
BEGIN;

CREATE TABLE management_artifacts (
    artifact_id       uuid        PRIMARY KEY,
    organization_id   uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    -- The accountable HUMAN, not derivable from author_instance_id: an
    -- agent-authored artifact has an agent author and a human on whose
    -- behalf the work is done. NOT NULL because ADR 0021's accountability
    -- rule guarantees one always exists.
    user_id           uuid        NOT NULL,

    artifact_type     text        NOT NULL,
    artifact_category text        NOT NULL DEFAULT 'management',
    status            text        NOT NULL DEFAULT 'draft',
    scope_type        text        NOT NULL,

    -- Exclusive arc: one real FK per scope type. A polymorphic scope_id
    -- could not be a foreign key, and a supertable pointed the wrong way --
    -- deleting an entity would leave its scope row behind with the artifact
    -- still resolving to it. ON DELETE RESTRICT is what makes "you cannot
    -- delete a scoped entity that has artifacts" true.
    scope_organization_id uuid REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    scope_product_id      uuid,
    scope_feature_id      uuid,
    scope_epic_id         uuid,
    scope_story_id        uuid,
    -- Item 9 adds scope_benchmark_run_id with the benchmark runs table.
    -- Until then scope_type = 'benchmark' cannot satisfy the exactly-one
    -- check below, so the schema refuses it with no seam rule to remember.

    -- Deliberately NOT declared NOT NULL, unlike lineage_key. It would be
    -- true -- the exactly-one-scope check guarantees a non-null COALESCE --
    -- but it buys nothing and costs a diagnosis: uuid maps to pgtype.UUID
    -- either way, so the generated type is unchanged, while the NOT NULL
    -- fires BEFORE one_scope_check and reports "null value in scope_id"
    -- instead of naming the rule the row actually broke.
    scope_id uuid GENERATED ALWAYS AS (
        COALESCE(scope_organization_id, scope_product_id,
                 scope_feature_id, scope_epic_id, scope_story_id)
    ) STORED,

    -- Denormalised lineage, as far up as the scope implies.
    product_id uuid,
    feature_id uuid,
    epic_id    uuid,
    story_id   uuid,

    author_instance_id   uuid NOT NULL,
    reviewer_instance_id uuid,

    -- Which tool call produced this artifact. ADR 0022's guardrail -- state
    -- changes pass through a tool/action record -- is only auditable if the
    -- link exists. Nullable because human-authored artifacts have no tool
    -- call, which is a real absence rather than a gap.
    produced_by_tool_call_id uuid,

    -- Lifecycle links are organization-aware (composite FKs below). A
    -- plain reference to artifact_id would let an artifact in one
    -- organization amend or supersede one in another, quietly joining two
    -- tenants' histories.
    amends_artifact_id     uuid,
    supersedes_artifact_id uuid,
    replaces_artifact_id   uuid,

    -- Assigned on acceptance and RETAINED thereafter. Without a stored
    -- sequence the effective view ("original plus accepted amendments in
    -- sequence order") has no total order and is undefined.
    amendment_sequence int,
    accepted_at        timestamptz,

    schema_version int         NOT NULL,
    summary        text        NOT NULL,
    payload        jsonb       NOT NULL,
    payload_digest text        NOT NULL,
    review_digest  text        NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),

    -- The amendment chain is FLAT (ADR 0021): amendments target the
    -- original only. Enforced by construction -- these generated columns
    -- plus the self-referencing composite key below make "an amendment of
    -- an amendment" unrepresentable, where a CHECK could not express it and
    -- a comment would only ask people not to.
    -- NOT NULL, unlike scope_id: an IS NOT NULL test always yields true or
    -- false, so this expression cannot produce null for ANY row, valid or
    -- not. The declaration is therefore unreachable and shadows no other
    -- constraint's message -- which is exactly why the same change on
    -- scope_id was wrong there, since its COALESCE genuinely is null for a
    -- row with no scope set. It also makes the UNIQUE constraints below
    -- mean what they say: Postgres treats nulls as distinct in a unique
    -- index, so a nullable column weakens the contract even when no null
    -- can occur.
    is_amendment boolean GENERATED ALWAYS AS (amends_artifact_id IS NOT NULL) STORED NOT NULL,

    -- Deliberately NULLABLE, and load-bearing: the CASE has no ELSE, so
    -- this is null whenever the row amends nothing, which is what makes the
    -- flat-chain foreign key below skip under MATCH SIMPLE instead of
    -- demanding a target that does not exist.
    amends_target_is_amendment boolean GENERATED ALWAYS AS (
        CASE WHEN amends_artifact_id IS NOT NULL THEN false END
    ) STORED,

    CONSTRAINT management_artifacts_category_check
        CHECK (artifact_category = 'management'),

    CONSTRAINT management_artifacts_status_check
        CHECK (status IN ('draft', 'invalidated', 'accepted', 'superseded', 'archived')),

    CONSTRAINT management_artifacts_scope_type_check
        CHECK (scope_type IN ('organization', 'product', 'feature', 'epic', 'story', 'benchmark')),

    CONSTRAINT management_artifacts_one_scope_check
        CHECK (num_nonnulls(scope_organization_id, scope_product_id,
                            scope_feature_id, scope_epic_id, scope_story_id) = 1),
    CONSTRAINT management_artifacts_scope_agrees_check
        CHECK ( (scope_type = 'organization') = (scope_organization_id IS NOT NULL)
            AND (scope_type = 'product')      = (scope_product_id      IS NOT NULL)
            AND (scope_type = 'feature')      = (scope_feature_id      IS NOT NULL)
            AND (scope_type = 'epic')         = (scope_epic_id         IS NOT NULL)
            AND (scope_type = 'story')        = (scope_story_id        IS NOT NULL) ),

    -- The scope column and the lineage column must name the SAME entity.
    -- Independent foreign keys would happily accept a scope pointing at one
    -- Story while story_id named another.
    CONSTRAINT management_artifacts_scope_matches_lineage_check
        CHECK ( (scope_story_id   IS NULL OR scope_story_id   = story_id)
            AND (scope_epic_id    IS NULL OR scope_epic_id    = epic_id)
            AND (scope_feature_id IS NULL OR scope_feature_id = feature_id)
            AND (scope_product_id IS NULL OR scope_product_id = product_id)
            AND (scope_organization_id IS NULL OR scope_organization_id = organization_id) ),

    -- Lineage is scope-conditional (ADR 0018): non-null at every level the
    -- scope covers. A story-scoped artifact with a null epic_id is
    -- unqueryable by the joins the model promises, and no test that inserts
    -- only well-formed rows would ever notice.
    CONSTRAINT management_artifacts_lineage_check
        CHECK (
            CASE scope_type
                WHEN 'story'   THEN story_id IS NOT NULL AND epic_id IS NOT NULL
                                AND feature_id IS NOT NULL AND product_id IS NOT NULL
                WHEN 'epic'    THEN story_id IS NULL AND epic_id IS NOT NULL
                                AND feature_id IS NOT NULL AND product_id IS NOT NULL
                WHEN 'feature' THEN story_id IS NULL AND epic_id IS NULL
                                AND feature_id IS NOT NULL AND product_id IS NOT NULL
                WHEN 'product' THEN story_id IS NULL AND epic_id IS NULL
                                AND feature_id IS NULL AND product_id IS NOT NULL
                -- Organization- and benchmark-scoped artifacts belong to no
                -- Epic, so they carry no work-hierarchy lineage at all.
                ELSE story_id IS NULL AND epic_id IS NULL
                     AND feature_id IS NULL AND product_id IS NULL
            END
        ),

    CONSTRAINT management_artifacts_one_link_check
        CHECK (num_nonnulls(amends_artifact_id, supersedes_artifact_id, replaces_artifact_id) <= 1),

    -- accepted_at is set on acceptance and SURVIVES the terminal states: an
    -- artifact that was accepted and later superseded was still accepted,
    -- and erasing that would erase the audit trail. Only draft and
    -- invalidated artifacts have never been accepted.
    CONSTRAINT management_artifacts_accepted_at_check
        CHECK ((accepted_at IS NOT NULL) = (status IN ('accepted', 'superseded', 'archived'))),

    -- Likewise the amendment sequence, which the effective view depends on
    -- long after the amendment itself has been superseded.
    CONSTRAINT management_artifacts_amendment_sequence_check
        CHECK ((amendment_sequence IS NOT NULL) =
               (amends_artifact_id IS NOT NULL AND status IN ('accepted', 'superseded', 'archived'))),

    CONSTRAINT management_artifacts_payload_digest_check
        CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT management_artifacts_review_digest_check
        CHECK (review_digest ~ '^[0-9a-f]{64}$'),

    CONSTRAINT management_artifacts_schema_version_check
        CHECK (schema_version >= 1),

    -- Composite references: nothing may cross an organization boundary.
    CONSTRAINT management_artifacts_user_fkey
        FOREIGN KEY (user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT management_artifacts_author_fkey
        FOREIGN KEY (author_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT management_artifacts_reviewer_fkey
        FOREIGN KEY (reviewer_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT management_artifacts_tool_call_fkey
        FOREIGN KEY (produced_by_tool_call_id, organization_id)
        REFERENCES tool_calls (tool_call_id, organization_id) ON DELETE RESTRICT,

    -- Whole-tuple lineage references. Unchecked when any column is null
    -- (MATCH SIMPLE), which is exactly right: an epic-scoped artifact has a
    -- null story_id and is checked by the epic tuple instead.
    CONSTRAINT management_artifacts_story_lineage_fkey
        FOREIGN KEY (story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT management_artifacts_epic_lineage_fkey
        FOREIGN KEY (epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT management_artifacts_feature_lineage_fkey
        FOREIGN KEY (feature_id, product_id, organization_id)
        REFERENCES features (feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT management_artifacts_product_lineage_fkey
        FOREIGN KEY (product_id, organization_id)
        REFERENCES products (product_id, organization_id) ON DELETE RESTRICT,

    -- Scope foreign keys, so a scope column names a row that exists.
    CONSTRAINT management_artifacts_scope_story_fkey
        FOREIGN KEY (scope_story_id) REFERENCES stories (story_id) ON DELETE RESTRICT,
    CONSTRAINT management_artifacts_scope_epic_fkey
        FOREIGN KEY (scope_epic_id) REFERENCES epics (epic_id) ON DELETE RESTRICT,
    CONSTRAINT management_artifacts_scope_feature_fkey
        FOREIGN KEY (scope_feature_id) REFERENCES features (feature_id) ON DELETE RESTRICT,
    CONSTRAINT management_artifacts_scope_product_fkey
        FOREIGN KEY (scope_product_id) REFERENCES products (product_id) ON DELETE RESTRICT,

    CONSTRAINT management_artifacts_supersedes_fkey
        FOREIGN KEY (supersedes_artifact_id, organization_id)
        REFERENCES management_artifacts (artifact_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT management_artifacts_replaces_fkey
        FOREIGN KEY (replaces_artifact_id, organization_id)
        REFERENCES management_artifacts (artifact_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT management_artifacts_id_kind_key     UNIQUE (artifact_id, is_amendment),
    CONSTRAINT management_artifacts_id_org_key      UNIQUE (artifact_id, organization_id),
    CONSTRAINT management_artifacts_id_kind_org_key UNIQUE (artifact_id, is_amendment, organization_id)
);

-- The flat-chain constraint: an amendment may only target a NON-amendment.
-- Self-amendment falls out too -- a row amending itself would need its own
-- is_amendment to be both true and false.
ALTER TABLE management_artifacts
    ADD CONSTRAINT management_artifacts_amends_original_fkey
    FOREIGN KEY (amends_artifact_id, amends_target_is_amendment, organization_id)
    REFERENCES management_artifacts (artifact_id, is_amendment, organization_id) ON DELETE RESTRICT;

-- The amendment order is total by construction, not by convention.
CREATE UNIQUE INDEX management_artifacts_amendment_sequence_key
    ON management_artifacts (amends_artifact_id, amendment_sequence)
    WHERE amends_artifact_id IS NOT NULL AND amendment_sequence IS NOT NULL;

CREATE INDEX management_artifacts_scope_idx       ON management_artifacts (scope_type, scope_id);
CREATE INDEX management_artifacts_epic_id_idx     ON management_artifacts (epic_id);
CREATE INDEX management_artifacts_story_id_idx    ON management_artifacts (story_id);
CREATE INDEX management_artifacts_type_status_idx ON management_artifacts (artifact_type, status);
CREATE INDEX management_artifacts_amends_idx      ON management_artifacts (amends_artifact_id);
CREATE INDEX management_artifacts_author_idx      ON management_artifacts (author_instance_id);
CREATE INDEX management_artifacts_tool_call_idx   ON management_artifacts (produced_by_tool_call_id);

COMMIT;
