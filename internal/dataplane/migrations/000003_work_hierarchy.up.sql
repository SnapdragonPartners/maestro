-- The work hierarchy: Feature -> Epic -> Story (ADR 0018).
--
-- Lineage is NON-NULL at every level, which is a modelling commitment
-- rather than a convenience: wrapper Features and the default Product
-- guarantee a parent always exists, so no code ever has to handle the
-- "orphan Epic" case that a nullable column would invite.
BEGIN;

CREATE TABLE features (
    feature_id      uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    product_id      uuid        NOT NULL REFERENCES products      (product_id)      ON DELETE RESTRICT,
    title           text        NOT NULL,
    -- Wrapper Features are auto-created by the Orchestrator at degenerate
    -- entry (ADR 0018/0024). Flagged so the UI can collapse them rather
    -- than showing a human a Feature they never asked to create.
    is_wrapper      boolean     NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX features_product_id_idx      ON features (product_id);
CREATE INDEX features_organization_id_idx ON features (organization_id);

-- Epics are repo-scoped (ADR 0018): the Epic is the unit that owns a branch
-- (ADR 0023), and a branch lives in exactly one repository.
CREATE TABLE epics (
    epic_id         uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    product_id      uuid        NOT NULL REFERENCES products      (product_id)      ON DELETE RESTRICT,
    feature_id      uuid        NOT NULL REFERENCES features      (feature_id)      ON DELETE RESTRICT,
    repository_id   uuid        NOT NULL REFERENCES repositories  (repository_id)   ON DELETE RESTRICT,
    title           text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX epics_feature_id_idx    ON epics (feature_id);
CREATE INDEX epics_repository_id_idx ON epics (repository_id);

CREATE TABLE stories (
    story_id        uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    product_id      uuid        NOT NULL REFERENCES products      (product_id)      ON DELETE RESTRICT,
    feature_id      uuid        NOT NULL REFERENCES features      (feature_id)      ON DELETE RESTRICT,
    epic_id         uuid        NOT NULL REFERENCES epics         (epic_id)         ON DELETE RESTRICT,
    title           text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX stories_epic_id_idx ON stories (epic_id);

COMMIT;
