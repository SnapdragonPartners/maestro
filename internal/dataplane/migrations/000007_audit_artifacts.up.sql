-- Audit artifacts: the exhaust (ADR 0021).
--
-- NOT the Management table with a different category. Audit artifacts are
-- BORN FINAL and have no lifecycle, so this table has no status, no
-- accepted_at, no amendment or supersession links, no review_digest and no
-- reviewer: carrying a Management status vocabulary on rows that can never
-- move through it would only invite readers to interpret a value that means
-- nothing. Retention pinning is a property (see the pins table), not a
-- status.
--
-- Truncatable by design, subject to retention pins, and the largest table
-- in the system.
BEGIN;

CREATE TABLE audit_artifacts (
    artifact_id       uuid        PRIMARY KEY,
    organization_id   uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    -- Nullable here, unlike Management: system principals emit exhaust
    -- (startup metrics, scheduler ticks) that genuinely precedes or outlives
    -- any user's action, and forcing a value would mean inventing one.
    user_id           uuid,

    artifact_type     text        NOT NULL,
    artifact_category text        NOT NULL DEFAULT 'audit',
    scope_type        text        NOT NULL,

    scope_organization_id uuid REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    scope_product_id      uuid,
    scope_feature_id      uuid,
    scope_epic_id         uuid,
    scope_story_id        uuid,

    scope_id uuid GENERATED ALWAYS AS (
        COALESCE(scope_organization_id, scope_product_id,
                 scope_feature_id, scope_epic_id, scope_story_id)
    ) STORED,

    product_id uuid,
    feature_id uuid,
    epic_id    uuid,
    story_id   uuid,

    -- Any principal kind, including system (ADR 0021).
    author_instance_id       uuid NOT NULL,
    produced_by_tool_call_id uuid,

    schema_version int         NOT NULL,
    summary        text        NOT NULL,
    payload        jsonb       NOT NULL,
    payload_digest text        NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT audit_artifacts_category_check
        CHECK (artifact_category = 'audit'),

    CONSTRAINT audit_artifacts_scope_type_check
        CHECK (scope_type IN ('organization', 'product', 'feature', 'epic', 'story', 'benchmark')),

    CONSTRAINT audit_artifacts_one_scope_check
        CHECK (num_nonnulls(scope_organization_id, scope_product_id,
                            scope_feature_id, scope_epic_id, scope_story_id) = 1),
    CONSTRAINT audit_artifacts_scope_agrees_check
        CHECK ( (scope_type = 'organization') = (scope_organization_id IS NOT NULL)
            AND (scope_type = 'product')      = (scope_product_id      IS NOT NULL)
            AND (scope_type = 'feature')      = (scope_feature_id      IS NOT NULL)
            AND (scope_type = 'epic')         = (scope_epic_id         IS NOT NULL)
            AND (scope_type = 'story')        = (scope_story_id        IS NOT NULL) ),

    CONSTRAINT audit_artifacts_scope_matches_lineage_check
        CHECK ( (scope_story_id   IS NULL OR scope_story_id   = story_id)
            AND (scope_epic_id    IS NULL OR scope_epic_id    = epic_id)
            AND (scope_feature_id IS NULL OR scope_feature_id = feature_id)
            AND (scope_product_id IS NULL OR scope_product_id = product_id)
            AND (scope_organization_id IS NULL OR scope_organization_id = organization_id) ),

    CONSTRAINT audit_artifacts_lineage_check
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
                ELSE story_id IS NULL AND epic_id IS NULL
                     AND feature_id IS NULL AND product_id IS NULL
            END
        ),

    CONSTRAINT audit_artifacts_payload_digest_check
        CHECK (payload_digest ~ '^[0-9a-f]{64}$'),

    CONSTRAINT audit_artifacts_schema_version_check
        CHECK (schema_version >= 1),

    CONSTRAINT audit_artifacts_user_fkey
        FOREIGN KEY (user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT audit_artifacts_author_fkey
        FOREIGN KEY (author_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT audit_artifacts_tool_call_fkey
        FOREIGN KEY (produced_by_tool_call_id, organization_id)
        REFERENCES tool_calls (tool_call_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT audit_artifacts_story_lineage_fkey
        FOREIGN KEY (story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT audit_artifacts_epic_lineage_fkey
        FOREIGN KEY (epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT audit_artifacts_feature_lineage_fkey
        FOREIGN KEY (feature_id, product_id, organization_id)
        REFERENCES features (feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT audit_artifacts_product_lineage_fkey
        FOREIGN KEY (product_id, organization_id)
        REFERENCES products (product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT audit_artifacts_scope_story_fkey
        FOREIGN KEY (scope_story_id) REFERENCES stories (story_id) ON DELETE RESTRICT,
    CONSTRAINT audit_artifacts_scope_epic_fkey
        FOREIGN KEY (scope_epic_id) REFERENCES epics (epic_id) ON DELETE RESTRICT,
    CONSTRAINT audit_artifacts_scope_feature_fkey
        FOREIGN KEY (scope_feature_id) REFERENCES features (feature_id) ON DELETE RESTRICT,
    CONSTRAINT audit_artifacts_scope_product_fkey
        FOREIGN KEY (scope_product_id) REFERENCES products (product_id) ON DELETE RESTRICT,

    CONSTRAINT audit_artifacts_id_org_key UNIQUE (artifact_id, organization_id)
);

CREATE INDEX audit_artifacts_scope_idx      ON audit_artifacts (scope_type, scope_id);
CREATE INDEX audit_artifacts_story_id_idx   ON audit_artifacts (story_id);
CREATE INDEX audit_artifacts_created_at_idx ON audit_artifacts (created_at);
CREATE INDEX audit_artifacts_type_idx       ON audit_artifacts (artifact_type);
CREATE INDEX audit_artifacts_tool_call_idx  ON audit_artifacts (produced_by_tool_call_id);

COMMIT;
