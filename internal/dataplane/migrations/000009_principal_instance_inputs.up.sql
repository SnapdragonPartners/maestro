-- The MPH signature's seeding set (ADR 0021).
--
-- "Every agent is seeded at startup with one or more artifacts paired to
-- its prompt template, and that seed must be sufficient to commence the
-- task... The seeding set is recorded as the instance's input artifact
-- digests in the MPH signature, so 'what was this agent given to start?' is
-- always a query, never a mystery."
--
-- Without this table that sentence is unenforceable and the question is
-- exactly the mystery it promises not to be. The DIGEST is recorded
-- alongside the reference, not just the artifact id: an artifact's
-- effective view can change through amendment, and the seeding set must say
-- what the agent actually received.
BEGIN;

CREATE TABLE principal_instance_inputs (
    principal_instance_id uuid        NOT NULL,
    artifact_id           uuid        NOT NULL,
    organization_id       uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,

    -- The payload digest as seeded. Compare against the artifact's current
    -- digest to detect that the seed has since moved.
    seeded_digest         text        NOT NULL,
    seeded_at             timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (principal_instance_id, artifact_id),

    CONSTRAINT principal_instance_inputs_digest_check
        CHECK (seeded_digest ~ '^[0-9a-f]{64}$'),

    CONSTRAINT principal_instance_inputs_principal_fkey
        FOREIGN KEY (principal_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT principal_instance_inputs_artifact_fkey
        FOREIGN KEY (artifact_id, organization_id)
        REFERENCES management_artifacts (artifact_id, organization_id) ON DELETE RESTRICT
);

CREATE INDEX principal_instance_inputs_artifact_idx ON principal_instance_inputs (artifact_id);

COMMIT;
