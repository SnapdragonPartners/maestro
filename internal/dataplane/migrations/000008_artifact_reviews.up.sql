-- Review records (ADR 0021, encoded by ADR 0028).
--
-- Reviewer identity alone is not review completion: a Management artifact
-- persists as draft -- working state with no authority -- until a review
-- record completes. Only then does it become accepted and authoritative.
--
-- The record binds the artifact AND the exact review_digest it reviewed: a
-- digest over the whole reviewable projection, not the payload alone,
-- because re-scoping an artifact or reassigning its author changes what a
-- reviewer agreed to. A review naming only an artifact would silently carry
-- over to content the reviewer never saw.
BEGIN;

CREATE TABLE artifact_reviews (
    review_id       uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    artifact_id     uuid        NOT NULL,
    review_digest   text        NOT NULL,

    -- For an amendment, the base it was reviewed against. ADR 0028: an
    -- amendment's meaning is a function of the effective view it modifies,
    -- so a moved base forces re-review. Null for originals.
    base_digest     text,
    base_sequence   int,

    reviewer_instance_id uuid   NOT NULL,
    decision        text        NOT NULL,
    rationale       text        NOT NULL,
    decided_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT artifact_reviews_decision_check
        CHECK (decision IN ('accepted', 'rejected', 'changes_requested')),

    CONSTRAINT artifact_reviews_review_digest_check
        CHECK (review_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT artifact_reviews_base_digest_check
        CHECK (base_digest IS NULL OR base_digest ~ '^[0-9a-f]{64}$'),

    -- The base is recorded as a pair or not at all.
    CONSTRAINT artifact_reviews_base_pair_check
        CHECK ((base_digest IS NULL) = (base_sequence IS NULL)),

    CONSTRAINT artifact_reviews_artifact_fkey
        FOREIGN KEY (artifact_id, organization_id)
        REFERENCES management_artifacts (artifact_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT artifact_reviews_reviewer_fkey
        FOREIGN KEY (reviewer_instance_id, organization_id)
        REFERENCES principal_instances (principal_instance_id, organization_id) ON DELETE RESTRICT
);

CREATE INDEX artifact_reviews_artifact_id_idx ON artifact_reviews (artifact_id);
CREATE INDEX artifact_reviews_reviewer_idx    ON artifact_reviews (reviewer_instance_id);

-- Acceptance matches on the DIGEST rather than the artifact; this index is
-- what makes that lookup cheap enough to do on every transition.
CREATE INDEX artifact_reviews_accepted_digest_idx
    ON artifact_reviews (artifact_id, review_digest)
    WHERE decision = 'accepted';

COMMIT;
