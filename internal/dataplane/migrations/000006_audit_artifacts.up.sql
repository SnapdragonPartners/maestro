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
-- This table is truncatable by design, subject to retention pins, and will
-- be the largest in the system.
BEGIN;

CREATE TABLE audit_artifacts (
    artifact_id       uuid        PRIMARY KEY,
    organization_id   uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    -- Nullable here, unlike Management: system principals emit exhaust
    -- (startup metrics, scheduler ticks) that genuinely precedes or outlives
    -- any user's action, and forcing a value would mean inventing one.
    user_id           uuid        REFERENCES users (user_id) ON DELETE RESTRICT,

    artifact_type     text        NOT NULL,
    artifact_category text        NOT NULL DEFAULT 'audit',
    scope_type        text        NOT NULL,

    scope_organization_id uuid REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    scope_product_id      uuid REFERENCES products      (product_id)      ON DELETE RESTRICT,
    scope_feature_id      uuid REFERENCES features      (feature_id)      ON DELETE RESTRICT,
    scope_epic_id         uuid REFERENCES epics         (epic_id)         ON DELETE RESTRICT,
    scope_story_id        uuid REFERENCES stories       (story_id)        ON DELETE RESTRICT,

    scope_id uuid GENERATED ALWAYS AS (
        COALESCE(scope_organization_id, scope_product_id,
                 scope_feature_id, scope_epic_id, scope_story_id)
    ) STORED,

    product_id uuid REFERENCES products (product_id) ON DELETE RESTRICT,
    feature_id uuid REFERENCES features (feature_id) ON DELETE RESTRICT,
    epic_id    uuid REFERENCES epics    (epic_id)    ON DELETE RESTRICT,
    story_id   uuid REFERENCES stories  (story_id)   ON DELETE RESTRICT,

    -- Any principal kind, including system (ADR 0021).
    author_instance_id uuid NOT NULL REFERENCES principal_instances (principal_instance_id) ON DELETE RESTRICT,

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
        CHECK (schema_version >= 1)
);

CREATE INDEX audit_artifacts_scope_idx      ON audit_artifacts (scope_type, scope_id);
CREATE INDEX audit_artifacts_story_id_idx   ON audit_artifacts (story_id);
CREATE INDEX audit_artifacts_created_at_idx ON audit_artifacts (created_at);
CREATE INDEX audit_artifacts_type_idx       ON audit_artifacts (artifact_type);

COMMIT;
