-- Management artifacts: the review-bearing inputs (ADR 0021).
--
-- Separate from Audit by design, not by a category column: the two have
-- opposite retention postures and Audit volume dwarfs this table. Each
-- table pins its own category so a row cannot land in the wrong family.
BEGIN;

CREATE TABLE management_artifacts (
    artifact_id        uuid        PRIMARY KEY,
    organization_id    uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    -- The accountable HUMAN, which is not derivable from author_instance_id:
    -- an agent-authored artifact has an agent author and a human on whose
    -- behalf the work is done. NOT NULL here because ADR 0021's
    -- accountability rule guarantees one always exists.
    user_id            uuid        NOT NULL REFERENCES users (user_id) ON DELETE RESTRICT,

    artifact_type      text        NOT NULL,
    artifact_category  text        NOT NULL DEFAULT 'management',
    status             text        NOT NULL DEFAULT 'draft',
    scope_type         text        NOT NULL,

    -- Exclusive arc: one real foreign key per scope type. A polymorphic
    -- scope_id column could not be an FK at all, and a supertable pointed
    -- the wrong way -- deleting an entity would leave its scope row behind
    -- with the artifact still resolving to it. ON DELETE RESTRICT is what
    -- makes "you cannot delete a scoped entity that has artifacts" true.
    scope_organization_id uuid REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    scope_product_id      uuid REFERENCES products      (product_id)      ON DELETE RESTRICT,
    scope_feature_id      uuid REFERENCES features      (feature_id)      ON DELETE RESTRICT,
    scope_epic_id         uuid REFERENCES epics         (epic_id)         ON DELETE RESTRICT,
    scope_story_id        uuid REFERENCES stories       (story_id)        ON DELETE RESTRICT,
    -- Item 9 adds scope_benchmark_run_id with the benchmark runs table.
    -- Until then scope_type = 'benchmark' cannot satisfy the exactly-one
    -- check below, so the schema refuses it with no seam rule to remember.

    -- Derived, never written: cannot drift from the typed column.
    scope_id uuid GENERATED ALWAYS AS (
        COALESCE(scope_organization_id, scope_product_id,
                 scope_feature_id, scope_epic_id, scope_story_id)
    ) STORED,

    -- Denormalised lineage for querying, as far up as the scope implies.
    product_id         uuid REFERENCES products (product_id) ON DELETE RESTRICT,
    feature_id         uuid REFERENCES features (feature_id) ON DELETE RESTRICT,
    epic_id            uuid REFERENCES epics    (epic_id)    ON DELETE RESTRICT,
    story_id           uuid REFERENCES stories  (story_id)   ON DELETE RESTRICT,

    author_instance_id   uuid NOT NULL REFERENCES principal_instances (principal_instance_id) ON DELETE RESTRICT,
    reviewer_instance_id uuid          REFERENCES principal_instances (principal_instance_id) ON DELETE RESTRICT,

    -- Lifecycle links; at most one is set (ADR 0021).
    amends_artifact_id     uuid REFERENCES management_artifacts (artifact_id) ON DELETE RESTRICT,
    supersedes_artifact_id uuid REFERENCES management_artifacts (artifact_id) ON DELETE RESTRICT,
    replaces_artifact_id   uuid REFERENCES management_artifacts (artifact_id) ON DELETE RESTRICT,

    -- Assigned on acceptance. Without a stored sequence the effective view
    -- ("original plus accepted amendments IN SEQUENCE ORDER") has no total
    -- order and is undefined, and ADR 0028 binds an amendment's review to a
    -- sequence point that must be a fact rather than an inference.
    amendment_sequence int,
    accepted_at        timestamptz,

    schema_version int         NOT NULL,
    summary        text        NOT NULL,
    payload        jsonb       NOT NULL,
    payload_digest text        NOT NULL,
    review_digest  text        NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT management_artifacts_category_check
        CHECK (artifact_category = 'management'),

    CONSTRAINT management_artifacts_status_check
        CHECK (status IN ('draft', 'invalidated', 'accepted', 'superseded', 'archived')),

    CONSTRAINT management_artifacts_scope_type_check
        CHECK (scope_type IN ('organization', 'product', 'feature', 'epic', 'story', 'benchmark')),

    -- Exactly one scope column, and it agrees with scope_type.
    CONSTRAINT management_artifacts_one_scope_check
        CHECK (num_nonnulls(scope_organization_id, scope_product_id,
                            scope_feature_id, scope_epic_id, scope_story_id) = 1),
    CONSTRAINT management_artifacts_scope_agrees_check
        CHECK ( (scope_type = 'organization') = (scope_organization_id IS NOT NULL)
            AND (scope_type = 'product')      = (scope_product_id      IS NOT NULL)
            AND (scope_type = 'feature')      = (scope_feature_id      IS NOT NULL)
            AND (scope_type = 'epic')         = (scope_epic_id         IS NOT NULL)
            AND (scope_type = 'story')        = (scope_story_id        IS NOT NULL) ),

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

    -- The amendment chain is flat (ADR 0021): amendments target the original
    -- only, and correcting an earlier amendment means a later amendment.
    -- Enforced with the sequence rule below rather than by convention.
    CONSTRAINT management_artifacts_amendment_sequence_check
        CHECK ((amendment_sequence IS NOT NULL) = (amends_artifact_id IS NOT NULL AND status = 'accepted')),

    CONSTRAINT management_artifacts_accepted_at_check
        CHECK ((accepted_at IS NOT NULL) = (status = 'accepted')),

    CONSTRAINT management_artifacts_payload_digest_check
        CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT management_artifacts_review_digest_check
        CHECK (review_digest ~ '^[0-9a-f]{64}$'),

    CONSTRAINT management_artifacts_schema_version_check
        CHECK (schema_version >= 1)
);

-- The amendment order is total by construction, not by convention.
CREATE UNIQUE INDEX management_artifacts_amendment_sequence_key
    ON management_artifacts (amends_artifact_id, amendment_sequence)
    WHERE amends_artifact_id IS NOT NULL AND amendment_sequence IS NOT NULL;

CREATE INDEX management_artifacts_scope_idx        ON management_artifacts (scope_type, scope_id);
CREATE INDEX management_artifacts_epic_id_idx      ON management_artifacts (epic_id);
CREATE INDEX management_artifacts_story_id_idx     ON management_artifacts (story_id);
CREATE INDEX management_artifacts_type_status_idx  ON management_artifacts (artifact_type, status);
CREATE INDEX management_artifacts_amends_idx       ON management_artifacts (amends_artifact_id);
CREATE INDEX management_artifacts_author_idx       ON management_artifacts (author_instance_id);

COMMIT;
