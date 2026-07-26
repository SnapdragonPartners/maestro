-- Products, repositories, and their many-to-many membership.
--
-- ADR 0022 (as amended): a Product contains one or more repositories, and a
-- repository -- a shared API, say -- may belong to several Products. Each
-- repository designates EXACTLY ONE primary Product, which is what the
-- degenerate path's wrapper-Feature inference uses.
--
-- "Exactly one" is structural here rather than a constraint over the join
-- table. An earlier draft used a partial unique index on an is_primary
-- flag, which enforces AT MOST one and silently permits zero -- leaving the
-- wrapper-Feature inference with nothing to infer from. A NOT NULL column
-- holds exactly one value, so the count is not something anyone has to check.
BEGIN;

CREATE TABLE products (
    product_id      uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    -- User lineage, carried on every major record (ADR 0022) so team mode
    -- never needs a backfill.
    user_id         uuid        NOT NULL,
    slug            text        NOT NULL,
    display_name    text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT products_user_fkey
        FOREIGN KEY (user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT products_org_slug_key UNIQUE (organization_id, slug),
    CONSTRAINT products_id_org_key   UNIQUE (product_id, organization_id)
);

CREATE INDEX products_organization_id_idx ON products (organization_id);

-- Repositories are LOGICAL, forge-independent entities (ADR 0022): a repo
-- record may carry several forge bindings -- a local forge in airplane
-- mode, GitHub after sync -- and the binding is an attribute of the record,
-- never its identity. Bindings arrive in Phase 3 with the forge rework.
CREATE TABLE repositories (
    repository_id      uuid        PRIMARY KEY,
    organization_id    uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    -- The one primary Product. NOT NULL is the whole enforcement.
    primary_product_id uuid        NOT NULL,
    user_id            uuid        NOT NULL,
    slug               text        NOT NULL,
    display_name       text        NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT repositories_org_slug_key UNIQUE (organization_id, slug),
    CONSTRAINT repositories_id_org_key   UNIQUE (repository_id, organization_id),

    -- Composite, so the primary Product cannot belong to another organization.
    CONSTRAINT repositories_primary_product_fkey
        FOREIGN KEY (primary_product_id, organization_id)
        REFERENCES products (product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT repositories_user_fkey
        FOREIGN KEY (user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT
);

CREATE INDEX repositories_organization_id_idx ON repositories (organization_id);

CREATE TABLE product_repositories (
    product_id      uuid NOT NULL,
    repository_id   uuid NOT NULL,
    organization_id uuid NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,

    PRIMARY KEY (product_id, repository_id),

    CONSTRAINT product_repositories_product_fkey
        FOREIGN KEY (product_id, organization_id)
        REFERENCES products (product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT product_repositories_repository_fkey
        FOREIGN KEY (repository_id, organization_id)
        REFERENCES repositories (repository_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT product_repositories_repo_product_key UNIQUE (repository_id, product_id)
);

CREATE INDEX product_repositories_repository_id_idx ON product_repositories (repository_id);

-- The primary Product must also be an ordinary member, so the membership
-- set can never contradict the designation. DEFERRABLE because the
-- repository row necessarily exists before its membership row: this check
-- belongs at commit, not at statement time.
ALTER TABLE repositories
    ADD CONSTRAINT repositories_primary_is_member_fkey
    FOREIGN KEY (repository_id, primary_product_id)
    REFERENCES product_repositories (repository_id, product_id)
    DEFERRABLE INITIALLY DEFERRED;

COMMIT;
