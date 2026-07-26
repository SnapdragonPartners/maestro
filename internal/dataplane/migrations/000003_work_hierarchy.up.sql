-- The work hierarchy: Feature -> Epic -> Story (ADR 0018).
--
-- Lineage is non-null at every level, and -- more than that -- it is
-- CONSISTENT at every level. Independent single-column foreign keys would
-- accept an Epic whose product_id disagrees with its Feature's, or a Story
-- whose epic_id belongs to a different Feature than its own feature_id.
-- Each level therefore references its parent by the WHOLE lineage tuple,
-- so a contradiction is unrepresentable rather than merely discouraged.
BEGIN;

CREATE TABLE features (
    feature_id      uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL,
    user_id         uuid        NOT NULL,
    product_id      uuid        NOT NULL,
    title           text        NOT NULL,
    -- Wrapper Features are auto-created by the Orchestrator at degenerate
    -- entry (ADR 0018/0024). Flagged so the UI can collapse them rather
    -- than showing a human a Feature they never asked to create.
    is_wrapper      boolean     NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT features_user_fkey
        FOREIGN KEY (user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT features_product_fkey
        FOREIGN KEY (product_id, organization_id)
        REFERENCES products (product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT features_lineage_key UNIQUE (feature_id, product_id, organization_id)
);

CREATE INDEX features_product_id_idx      ON features (product_id);
CREATE INDEX features_organization_id_idx ON features (organization_id);

-- Epics are repo-scoped (ADR 0018): the Epic owns a branch (ADR 0023), and
-- a branch lives in exactly one repository.
CREATE TABLE epics (
    epic_id         uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL,
    user_id         uuid        NOT NULL,
    product_id      uuid        NOT NULL,
    feature_id      uuid        NOT NULL,
    repository_id   uuid        NOT NULL,
    title           text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT epics_feature_fkey
        FOREIGN KEY (feature_id, product_id, organization_id)
        REFERENCES features (feature_id, product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT epics_user_fkey
        FOREIGN KEY (user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,

    -- The repository must be a MEMBER of the Epic's Product, not merely in
    -- the same organization. Checking only the organization would let an
    -- Epic own a branch in a repository its Product has nothing to do with,
    -- which every downstream branch and evidence query would then treat as
    -- legitimate. Membership implies the organization, so this replaces the
    -- weaker check rather than joining it.
    CONSTRAINT epics_repository_membership_fkey
        FOREIGN KEY (product_id, repository_id)
        REFERENCES product_repositories (product_id, repository_id) ON DELETE RESTRICT,

    CONSTRAINT epics_lineage_key UNIQUE (epic_id, feature_id, product_id, organization_id)
);

CREATE INDEX epics_feature_id_idx    ON epics (feature_id);
CREATE INDEX epics_repository_id_idx ON epics (repository_id);

CREATE TABLE stories (
    story_id        uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL,
    user_id         uuid        NOT NULL,
    product_id      uuid        NOT NULL,
    feature_id      uuid        NOT NULL,
    epic_id         uuid        NOT NULL,
    title           text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT stories_user_fkey
        FOREIGN KEY (user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT stories_epic_fkey
        FOREIGN KEY (epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT stories_lineage_key UNIQUE (story_id, epic_id, feature_id, product_id, organization_id)
);

CREATE INDEX stories_epic_id_idx ON stories (epic_id);

COMMIT;
