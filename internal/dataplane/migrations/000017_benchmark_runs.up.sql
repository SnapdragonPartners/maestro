-- Benchmark runs and attempts: the vertical slice's scope target and its
-- import ledger (docs/v2/phase_2/design_slice_import.md, D2).
--
-- Two tables because a suite run and an attempt are different identities. The
-- suite is what artifacts scope to -- every artifact imported from one suite
-- answers to the same row -- while the attempt is what idempotency is keyed
-- on. One table could not be both without inventing a natural key column on
-- the largest tables in the system for one caller's benefit.
--
-- ADR and consumer, per the plan's reserved-by-name rule: ADR 0022's Phase 2
-- scope (the vertical slice) and ADR 0021 (benchmark-scoped artifacts);
-- consumed by item 9's importer.
BEGIN;

-- One row per SUITE RUN. Created once and never updated: it carries nothing
-- a later import would have to change, which is what keeps re-import a no-op
-- rather than a write.
CREATE TABLE benchmark_runs (
    benchmark_run_id  uuid        PRIMARY KEY,
    organization_id   uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,

    -- The runner's own suite identity, filename-safe by its contract.
    suite_run_id      text        NOT NULL,

    first_imported_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT benchmark_runs_suite_run_id_check
        CHECK (suite_run_id ~ '^[a-z0-9_-]+$'),

    -- Idempotency by stable identity: importing the same suite twice cannot
    -- create a second row, and the uniqueness is the arbiter rather than a
    -- check-then-insert in the importer.
    CONSTRAINT benchmark_runs_org_suite_key UNIQUE (organization_id, suite_run_id),

    -- The target of the organization-aware scope foreign keys below. Not
    -- redundant with the primary key: it is what lets a referencing table
    -- carry (id, organization_id) as a pair, so a scope cannot point at
    -- another tenant's run.
    CONSTRAINT benchmark_runs_id_org_key UNIQUE (benchmark_run_id, organization_id)
);

-- One row per ATTEMPT. This is the import ledger, and the reason idempotency
-- is a property of the database rather than of the importer's control flow.
CREATE TABLE benchmark_attempts (
    benchmark_attempt_id uuid        PRIMARY KEY,
    organization_id      uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    benchmark_run_id     uuid        NOT NULL,

    -- The runner's own attempt identity. Constrained to a single path
    -- component because the importer resolves evidence at
    -- <results>/evidence/<run_id>/ and the engine joins the same value into a
    -- workspace path: a value containing a separator, or `.`/`..`, would
    -- escape the directory it is supposed to name. The shape is enforced here
    -- as well as in the importer because a database that admits the value
    -- leaves the rule to whichever caller remembers it.
    run_id               text        NOT NULL,

    -- The JCS digest of the imported envelope. A re-import comparing equal is
    -- a no-op; comparing different is a conflict and is REJECTED, because run
    -- records are append-only on disk and never rewritten, so a differing
    -- digest is corruption or tampering and overwriting it would erase the
    -- evidence of that.
    record_digest        text        NOT NULL,

    -- The Audit artifact this record became. Written in the SAME transaction
    -- as the artifact: an artifact committed without its ledger row would be
    -- imported again on the next run and silently duplicated.
    audit_artifact_id    uuid        NOT NULL,

    -- Why this attempt contributed no call rows, and EMPTY when they were
    -- read. Recorded with the attempt because it was observed with it: the
    -- reason is a fact about the import, and asking the store again later
    -- answers a different question. An attempt whose calls were read and
    -- whose evidence was afterwards pruned would re-read as "no evidence
    -- directory", so the report would deny the llm_calls rows the plane
    -- holds for it.
    --
    -- NOT NULL and NO DEFAULT, deliberately. A default would let a writer
    -- omit the column and be given an answer it never observed, and the
    -- answer it would be given -- empty, meaning "the calls were read" -- is
    -- the one that fabricates a measurement. A surface-v1 log and a missing
    -- log both import their attempt with zero calls and a reason, so
    -- "unavailable" is an ordinary outcome rather than an edge case.
    calls_unavailable    text        NOT NULL,

    imported_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT benchmark_attempts_run_id_check
        CHECK (run_id ~ '^[a-z0-9][a-z0-9_-]*$'),
    CONSTRAINT benchmark_attempts_record_digest_check
        CHECK (record_digest ~ '^[0-9a-f]{64}$'),

    CONSTRAINT benchmark_attempts_identity_key
        UNIQUE (organization_id, benchmark_run_id, run_id),

    CONSTRAINT benchmark_attempts_run_fkey
        FOREIGN KEY (benchmark_run_id, organization_id)
        REFERENCES benchmark_runs (benchmark_run_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT benchmark_attempts_artifact_fkey
        FOREIGN KEY (audit_artifact_id, organization_id)
        REFERENCES audit_artifacts (artifact_id, organization_id) ON DELETE RESTRICT
);

CREATE INDEX benchmark_attempts_run_idx      ON benchmark_attempts (benchmark_run_id);
CREATE INDEX benchmark_attempts_artifact_idx ON benchmark_attempts (audit_artifact_id);

-- The scope column, on BOTH artifact families: each already admits
-- scope_type = 'benchmark' in its CHECK, and until now nothing could satisfy
-- the exactly-one-scope rule for it.
ALTER TABLE management_artifacts ADD COLUMN scope_benchmark_run_id uuid;
ALTER TABLE audit_artifacts      ADD COLUMN scope_benchmark_run_id uuid;

ALTER TABLE management_artifacts
    ADD CONSTRAINT management_artifacts_scope_benchmark_fkey
    FOREIGN KEY (scope_benchmark_run_id, organization_id)
    REFERENCES benchmark_runs (benchmark_run_id, organization_id) ON DELETE RESTRICT;

ALTER TABLE audit_artifacts
    ADD CONSTRAINT audit_artifacts_scope_benchmark_fkey
    FOREIGN KEY (scope_benchmark_run_id, organization_id)
    REFERENCES benchmark_runs (benchmark_run_id, organization_id) ON DELETE RESTRICT;

-- scope_id is REBUILT to include the new column, not left alone.
--
-- It is not merely an index input: the converter reads it as the domain scope
-- id, and ListManagementArtifactsByScope filters on `scope_id = @scope_id`.
-- Leaving benchmark rows with a null scope_id would surface them as uuid.Nil
-- and make them invisible to the very query the scope exists to serve.
--
-- SET EXPRESSION (PostgreSQL 17+, and the pinned image is 18) rewrites the
-- table so existing rows are recomputed, and PRESERVES the dependent
-- scope index -- which a DROP COLUMN / ADD COLUMN pair would have taken with
-- it, silently.
ALTER TABLE management_artifacts
    ALTER COLUMN scope_id SET EXPRESSION AS (
        COALESCE(scope_organization_id, scope_product_id,
                 scope_feature_id, scope_epic_id, scope_story_id,
                 scope_benchmark_run_id)
    );

ALTER TABLE audit_artifacts
    ALTER COLUMN scope_id SET EXPRESSION AS (
        COALESCE(scope_organization_id, scope_product_id,
                 scope_feature_id, scope_epic_id, scope_story_id,
                 scope_benchmark_run_id)
    );

-- The exactly-one and agreement rules learn the new column. Dropped and
-- recreated rather than edited: migrations are append-only after merge, so
-- this is a new migration and never a change to 000006/000007.
ALTER TABLE management_artifacts
    DROP CONSTRAINT management_artifacts_one_scope_check,
    DROP CONSTRAINT management_artifacts_scope_agrees_check;

ALTER TABLE management_artifacts
    ADD CONSTRAINT management_artifacts_one_scope_check
        CHECK (num_nonnulls(scope_organization_id, scope_product_id,
                            scope_feature_id, scope_epic_id, scope_story_id,
                            scope_benchmark_run_id) = 1),
    ADD CONSTRAINT management_artifacts_scope_agrees_check
        CHECK ( (scope_type = 'organization') = (scope_organization_id   IS NOT NULL)
            AND (scope_type = 'product')      = (scope_product_id        IS NOT NULL)
            AND (scope_type = 'feature')      = (scope_feature_id        IS NOT NULL)
            AND (scope_type = 'epic')         = (scope_epic_id           IS NOT NULL)
            AND (scope_type = 'story')        = (scope_story_id          IS NOT NULL)
            AND (scope_type = 'benchmark')    = (scope_benchmark_run_id  IS NOT NULL) );

ALTER TABLE audit_artifacts
    DROP CONSTRAINT audit_artifacts_one_scope_check,
    DROP CONSTRAINT audit_artifacts_scope_agrees_check;

ALTER TABLE audit_artifacts
    ADD CONSTRAINT audit_artifacts_one_scope_check
        CHECK (num_nonnulls(scope_organization_id, scope_product_id,
                            scope_feature_id, scope_epic_id, scope_story_id,
                            scope_benchmark_run_id) = 1),
    ADD CONSTRAINT audit_artifacts_scope_agrees_check
        CHECK ( (scope_type = 'organization') = (scope_organization_id   IS NOT NULL)
            AND (scope_type = 'product')      = (scope_product_id        IS NOT NULL)
            AND (scope_type = 'feature')      = (scope_feature_id        IS NOT NULL)
            AND (scope_type = 'epic')         = (scope_epic_id           IS NOT NULL)
            AND (scope_type = 'story')        = (scope_story_id          IS NOT NULL)
            AND (scope_type = 'benchmark')    = (scope_benchmark_run_id  IS NOT NULL) );

-- The lineage checks need no change: their ELSE branch already requires every
-- work-hierarchy column to be null, which is exactly what a benchmark-scoped
-- artifact carries. 000006's own comment already named benchmark alongside
-- organization as the scopes that belong to no Epic.

COMMIT;
