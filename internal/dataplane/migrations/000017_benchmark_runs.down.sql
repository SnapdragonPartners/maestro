-- Reverse the benchmark scope and its tables.
--
-- Order matters: the scope columns must go before the tables they reference,
-- and scope_id must stop naming a column that is about to be dropped.
BEGIN;

-- Restore the five-way expression FIRST. A generated column depending on
-- scope_benchmark_run_id would block the drop below.
ALTER TABLE management_artifacts
    ALTER COLUMN scope_id SET EXPRESSION AS (
        COALESCE(scope_organization_id, scope_product_id,
                 scope_feature_id, scope_epic_id, scope_story_id)
    );

ALTER TABLE audit_artifacts
    ALTER COLUMN scope_id SET EXPRESSION AS (
        COALESCE(scope_organization_id, scope_product_id,
                 scope_feature_id, scope_epic_id, scope_story_id)
    );

ALTER TABLE management_artifacts
    DROP CONSTRAINT management_artifacts_one_scope_check,
    DROP CONSTRAINT management_artifacts_scope_agrees_check;

ALTER TABLE management_artifacts
    ADD CONSTRAINT management_artifacts_one_scope_check
        CHECK (num_nonnulls(scope_organization_id, scope_product_id,
                            scope_feature_id, scope_epic_id, scope_story_id) = 1),
    ADD CONSTRAINT management_artifacts_scope_agrees_check
        CHECK ( (scope_type = 'organization') = (scope_organization_id IS NOT NULL)
            AND (scope_type = 'product')      = (scope_product_id      IS NOT NULL)
            AND (scope_type = 'feature')      = (scope_feature_id      IS NOT NULL)
            AND (scope_type = 'epic')         = (scope_epic_id         IS NOT NULL)
            AND (scope_type = 'story')        = (scope_story_id        IS NOT NULL) );

ALTER TABLE audit_artifacts
    DROP CONSTRAINT audit_artifacts_one_scope_check,
    DROP CONSTRAINT audit_artifacts_scope_agrees_check;

ALTER TABLE audit_artifacts
    ADD CONSTRAINT audit_artifacts_one_scope_check
        CHECK (num_nonnulls(scope_organization_id, scope_product_id,
                            scope_feature_id, scope_epic_id, scope_story_id) = 1),
    ADD CONSTRAINT audit_artifacts_scope_agrees_check
        CHECK ( (scope_type = 'organization') = (scope_organization_id IS NOT NULL)
            AND (scope_type = 'product')      = (scope_product_id      IS NOT NULL)
            AND (scope_type = 'feature')      = (scope_feature_id      IS NOT NULL)
            AND (scope_type = 'epic')         = (scope_epic_id         IS NOT NULL)
            AND (scope_type = 'story')        = (scope_story_id        IS NOT NULL) );

ALTER TABLE management_artifacts DROP COLUMN scope_benchmark_run_id;
ALTER TABLE audit_artifacts      DROP COLUMN scope_benchmark_run_id;

DROP TABLE benchmark_attempts;
DROP TABLE benchmark_runs;

COMMIT;
