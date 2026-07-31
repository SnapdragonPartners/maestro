-- Configuration records and the secrets vault (item 7 design, D1 and D5).
--
-- The two families ADR 0022 names for Phase 2 that item 3 left uncreated.
-- Both are built ahead of any consumer, which the phase plan's delegated
-- decision 1 carves out explicitly: the anti-speculation rule says a family
-- is created by the item that first needs it, and the ADR names these for
-- Phase 2 regardless. The ADR wins.
--
-- They are deliberately separate tables rather than one table with an
-- "encrypted" flag. Configuration is unencrypted and secrets are not, the
-- vault carries an entire encryption envelope configuration has no use for,
-- and a single table would make "is this row a secret?" a value to check
-- rather than a table to be in.
BEGIN;

-- --- configuration records -------------------------------------------
--
-- A registered key, a scope on the organization/product/repository lineage,
-- and a JSONB value. The key registry lives in Go (design D1): the schema
-- enforces shape and identity, the seam enforces which keys exist and what
-- their values must look like.
CREATE TABLE configuration_records (
    configuration_record_id uuid        PRIMARY KEY,
    organization_id         uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,

    -- The canonical dotted name. Validated against the code-resident
    -- registry at the seam; unregistered keys never reach here.
    key                     text        NOT NULL,

    scope_type              text        NOT NULL,

    -- Exclusive arc, one real FK per scope type, as management_artifacts
    -- does it. A polymorphic scope_id could not be a foreign key, and
    -- ON DELETE RESTRICT is what makes "you cannot delete a Product that
    -- still has configuration" true rather than aspirational.
    --
    -- The arc is org/product/repository, NOT the artifact scope arc: this
    -- family's lineage is ADR 0018's ownership chain, and a repository is
    -- the leaf it terminates at.
    scope_organization_id   uuid        REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    scope_product_id        uuid,
    scope_repository_id     uuid,

    scope_id uuid GENERATED ALWAYS AS (
        COALESCE(scope_organization_id, scope_product_id, scope_repository_id)
    ) STORED,

    value                   jsonb       NOT NULL,

    -- Optimistic concurrency (design D1). A configuration value is shared
    -- mutable state reachable from more than one agent lifecycle, which
    -- ADR 0027 forbids resolving by last-writer-wins. Every update and
    -- delete names the version it read.
    version                 integer     NOT NULL DEFAULT 1,

    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT configuration_records_scope_type_check
        CHECK (scope_type IN ('organization', 'product', 'repository')),

    CONSTRAINT configuration_records_one_scope_check
        CHECK (num_nonnulls(scope_organization_id, scope_product_id, scope_repository_id) = 1),

    -- The scope columns and scope_type must agree. Without this a row could
    -- claim scope_type = 'product' while carrying a repository id, and every
    -- resolution query that trusts scope_type would read the wrong level.
    CONSTRAINT configuration_records_scope_agrees_check
        CHECK (
            (scope_type = 'organization' AND scope_organization_id IS NOT NULL)
         OR (scope_type = 'product'      AND scope_product_id      IS NOT NULL)
         OR (scope_type = 'repository'   AND scope_repository_id   IS NOT NULL)
        ),

    CONSTRAINT configuration_records_key_check
        CHECK (btrim(key, E' \t\r\n') <> ''),

    CONSTRAINT configuration_records_version_check
        CHECK (version >= 1),

    -- One row per key per scope (design D1). Without it "most-specific-wins"
    -- is undefined the moment two rows exist at one level: the query returns
    -- whichever the planner reached first, and the defect surfaces as an
    -- intermittently wrong value rather than as an error.
    CONSTRAINT configuration_records_key_scope_key
        UNIQUE (organization_id, key, scope_type, scope_id),

    -- Composite, so a scoped entity cannot belong to another organization.
    CONSTRAINT configuration_records_product_fkey
        FOREIGN KEY (scope_product_id, organization_id)
        REFERENCES products (product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT configuration_records_repository_fkey
        FOREIGN KEY (scope_repository_id, organization_id)
        REFERENCES repositories (repository_id, organization_id) ON DELETE RESTRICT
);

-- Resolution reads one key across the lineage at once (design D1), so the
-- index leads with the columns every such read fixes.
CREATE INDEX configuration_records_lookup_idx
    ON configuration_records (organization_id, key, scope_type, scope_id);

-- --- secrets vault ----------------------------------------------------
--
-- Ciphertext only. The plane never holds the key: it is derived per version
-- from the external root of trust, which lives outside the data root and is
-- excluded from the backup by design (item 2).
CREATE TABLE secrets (
    secret_id       uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,

    name            text        NOT NULL,

    -- NULL means shared for the scope; set means an individual credential
    -- readable only by that user (design D5). The seam derives it from the
    -- acting user and never accepts it as an input.
    owner_user_id   uuid,

    scope_type            text  NOT NULL,
    scope_organization_id uuid  REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    scope_product_id      uuid,
    scope_repository_id   uuid,

    scope_id uuid GENERATED ALWAYS AS (
        COALESCE(scope_organization_id, scope_product_id, scope_repository_id)
    ) STORED,

    -- The encryption envelope (design D2). Every part is load-bearing:
    --
    --   scheme     names what to do, as a string rather than an opaque
    --              integer, so a later scheme coexists with this one and the
    --              reader is the compatibility layer;
    --   nonce      is its own column rather than a prefix on the ciphertext,
    --              because a framed blob needs a length convention every
    --              reader must agree on, and a reader taking the wrong
    --              prefix fails as a decryption error -- the least
    --              diagnosable failure available;
    --   version    is part of the key derivation context, which is what
    --              makes nonce reuse across replacements structurally
    --              impossible rather than improbable.
    scheme          text        NOT NULL,
    nonce           bytea       NOT NULL,
    ciphertext      bytea       NOT NULL,
    version         integer     NOT NULL DEFAULT 1,

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT secrets_scope_type_check
        CHECK (scope_type IN ('organization', 'product', 'repository')),

    CONSTRAINT secrets_one_scope_check
        CHECK (num_nonnulls(scope_organization_id, scope_product_id, scope_repository_id) = 1),

    CONSTRAINT secrets_scope_agrees_check
        CHECK (
            (scope_type = 'organization' AND scope_organization_id IS NOT NULL)
         OR (scope_type = 'product'      AND scope_product_id      IS NOT NULL)
         OR (scope_type = 'repository'   AND scope_repository_id   IS NOT NULL)
        ),

    CONSTRAINT secrets_name_check
        CHECK (btrim(name, E' \t\r\n') <> ''),

    CONSTRAINT secrets_scheme_check
        CHECK (btrim(scheme, E' \t\r\n') <> ''),

    CONSTRAINT secrets_version_check
        CHECK (version >= 1),

    -- A zero-length nonce or ciphertext is not an empty secret, it is a
    -- write that lost its payload: GCM always produces at least its
    -- authentication tag, and the nonce is fixed-length by construction.
    CONSTRAINT secrets_nonce_check
        CHECK (octet_length(nonce) = 12),

    CONSTRAINT secrets_ciphertext_check
        CHECK (octet_length(ciphertext) > 0),

    -- Composite, so a secret cannot name a user from another organization
    -- (design D5). The single-column reference would let a cross-tenant id
    -- through, which is the tenancy boundary this whole seam rests on.
    CONSTRAINT secrets_owner_fkey
        FOREIGN KEY (owner_user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT secrets_product_fkey
        FOREIGN KEY (scope_product_id, organization_id)
        REFERENCES products (product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT secrets_repository_fkey
        FOREIGN KEY (scope_repository_id, organization_id)
        REFERENCES repositories (repository_id, organization_id) ON DELETE RESTRICT
);

-- TWO partial unique indexes, not one constraint over the whole tuple
-- (design D5).
--
-- A plain UNIQUE including owner_user_id does not say what it appears to:
-- in Postgres NULL is not equal to itself, so it would permit any number of
-- SHARED secrets with the same name at the same scope -- precisely the
-- duplicates that make resolution non-deterministic, and precisely the case
-- a test that always seeds an owner never reaches.
--
-- The alternative, a sentinel uuid standing in for "nobody", makes one index
-- work at the cost of a magic value every query must remember to exclude and
-- that a real user id could in principle collide with.
CREATE UNIQUE INDEX secrets_individual_key
    ON secrets (organization_id, name, owner_user_id, scope_type, scope_id)
    WHERE owner_user_id IS NOT NULL;

CREATE UNIQUE INDEX secrets_shared_key
    ON secrets (organization_id, name, scope_type, scope_id)
    WHERE owner_user_id IS NULL;

-- Resolution walks the six-step ladder for one name (design D5), fixing the
-- organization and the name and then filtering by scope and ownership.
CREATE INDEX secrets_lookup_idx
    ON secrets (organization_id, name, scope_type, scope_id);

COMMIT;
