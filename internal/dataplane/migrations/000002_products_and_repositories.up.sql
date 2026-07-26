-- Products, repositories, and their many-to-many membership.
--
-- ADR 0022 (as amended): a Product contains one or more repositories, and a
-- repository -- a shared API, say -- may belong to several Products. Each
-- repository designates exactly one PRIMARY Product, which is what the
-- degenerate path's wrapper-Feature inference uses. This amends ADR 0018's
-- one-repo-one-Product MVP rule, which anticipated the revisit.
BEGIN;

CREATE TABLE products (
    product_id      uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    slug            text        NOT NULL,
    display_name    text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT products_org_slug_key UNIQUE (organization_id, slug)
);

CREATE INDEX products_organization_id_idx ON products (organization_id);

-- Repositories are LOGICAL, forge-independent entities (ADR 0022): a repo
-- record may carry several forge bindings -- a local forge in airplane
-- mode, GitHub after sync -- and the binding is an attribute of the record,
-- never its identity. Bindings themselves arrive in Phase 3 with the forge
-- rework; what matters here is that nothing keys a repository by its forge.
CREATE TABLE repositories (
    repository_id   uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    slug            text        NOT NULL,
    display_name    text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT repositories_org_slug_key UNIQUE (organization_id, slug)
);

CREATE INDEX repositories_organization_id_idx ON repositories (organization_id);

CREATE TABLE product_repositories (
    product_id    uuid    NOT NULL REFERENCES products     (product_id)    ON DELETE RESTRICT,
    repository_id uuid    NOT NULL REFERENCES repositories (repository_id) ON DELETE RESTRICT,
    -- Exactly one membership per repository carries is_primary; the partial
    -- unique index below is what makes "exactly one" a fact rather than an
    -- application convention.
    is_primary    boolean NOT NULL DEFAULT false,

    PRIMARY KEY (product_id, repository_id)
);

CREATE UNIQUE INDEX product_repositories_one_primary_per_repo_idx
    ON product_repositories (repository_id)
    WHERE is_primary;

CREATE INDEX product_repositories_repository_id_idx ON product_repositories (repository_id);

COMMIT;
