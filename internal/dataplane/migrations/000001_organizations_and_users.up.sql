-- Organizations and users: the multi-user lineage every other table carries.
--
-- ADR 0022 requires this lineage from the start "so team mode never
-- requires a data migration". Local mode uses a default organization and a
-- default user, mirroring the default Product; nothing here enforces
-- authorization, which is post-MVP. These records carry lineage, not policy.
BEGIN;

CREATE TABLE organizations (
    organization_id uuid        PRIMARY KEY,
    slug            text        NOT NULL,
    display_name    text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT organizations_slug_key UNIQUE (slug)
);

CREATE TABLE users (
    user_id         uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    -- Local mode has no authentication; this is an identity, not a credential.
    -- Federated login (Phase 7) adds provider bindings beside it rather than
    -- replacing it, which is why the local identity is not itself an email.
    handle          text        NOT NULL,
    display_name    text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    -- Handles are unique per organization, not globally: two organizations
    -- may legitimately each have a "dan".
    CONSTRAINT users_org_handle_key UNIQUE (organization_id, handle)
);

CREATE INDEX users_organization_id_idx ON users (organization_id);

COMMIT;
