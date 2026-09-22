-- Prompt packs (ADR 0031; Phase 3 item 4 design, D4 through D8).
--
-- Four things, in one migration because they constrain each other:
--
--   1. prompt_pack_contents -- immutable, content-addressed pack versions
--      under a scheme-qualified digest, guarded by the schema's FIRST trigger.
--   2. prompt_pack_installations -- the mutable record beside each content
--      row: display name, declared Maestro range, declared role coverage, a
--      governed installer identity, the version the gate last validated it
--      against, and a monotonic revision.
--   3. dispatch_prompt_resolutions -- the pack resolved ONCE at dispatch,
--      persisted beside the dispatch basis, bound to its dispatch by a
--      reciprocal deferred foreign key so a dispatch without a resolution is
--      a refused statement, not a convention.
--   4. The principal_instances split: prompt_pack_id was one nullable text
--      column doing three jobs (design D5). It becomes an origin discriminator
--      plus a name, a scheme, and four references, partitioned by ONE shape
--      constraint that no NULL can slip through.
--
-- THE MIGRATION IS TOTAL, OR IT REFUSES. On 000022.down's pattern, which is
-- 000011's with the lock it was missing: every inspected table is locked
-- ACCESS EXCLUSIVE in a fixed order BEFORE any scan, then a DO block classifies
-- every row and raises with the remedy, and only then does anything alter.
--
-- THE LOCK ORDER, shared with the down migration so two migrations can never
-- deadlock each other:
--
--     prompt_pack_contents
--     prompt_pack_installations
--     configuration_records
--     story_dispatches
--     dispatch_prompt_resolutions
--     principal_instances
--
-- This direction locks only what exists and is scanned or altered here:
-- story_dispatches, then principal_instances. The ordering test reads this
-- file and asserts the sequence against the list above.
--
-- WHOM THE LOCK DEFENDS AGAINST. On the local stack no seam can overlap a
-- migration: stack.Migrate holds the lifecycle lock exclusive. What the
-- lifecycle lock does not cover is a writer that never takes it -- psql, an
-- integration test calling migrations.Up on a DSN, and any composer without
-- the local flock, which is what the cloud composer is. The table lock holds
-- regardless of how the database was reached. Freedom from deadlock against
-- a live seam is NOT something ordering can buy (the family's own writers do
-- not share an access order); it is the cutover procedure in
-- docs/v2/process_runbook.md, and a violated procedure produces a refused
-- migration or a refused write, never a silent success.
--
-- RECOVERING FROM A REFUSAL. golang-migrate marks the version BEFORE running,
-- so a refusal leaves the recorded version at 23 and DIRTY while the rollback
-- leaves the actual schema at 22. Resolving the rows is only half of it:
--
--     make dataplane-force-version VERSION=22 FORCE=1
--
-- then resolve or delete the offending rows -- or, on a local plane,
-- `make dataplane-reset FORCE=1` -- and `make dataplane-migrate`. This is
-- walked end to end in the tests.
BEGIN;

-- ---------------------------------------------------------------------------
-- Step 1: take the tables, THEN refuse.
-- ---------------------------------------------------------------------------
LOCK TABLE story_dispatches    IN ACCESS EXCLUSIVE MODE;
LOCK TABLE principal_instances IN ACCESS EXCLUSIVE MODE;

DO $$
DECLARE
    dispatches   bigint;
    no_identity  bigint;
    stray_fields bigint;
BEGIN
    -- Design D8: a dispatch that predates packs has no honest resolution, and
    -- a reader that must special-case its absence cannot tell a legacy row
    -- from a defect's missing child. So they may not coexist, and this
    -- migration refuses the old ones rather than inventing resolutions.
    SELECT count(*) INTO dispatches FROM story_dispatches;
    IF dispatches > 0 THEN
        RAISE EXCEPTION 'cannot apply 000023: % story dispatch(es) predate prompt-pack resolution '
            'and no honest resolution exists for them. Every dispatch from here on carries the '
            'pack it resolved, and a reader cannot distinguish a legacy dispatch from a missing '
            'resolution. Force the version back (make dataplane-force-version VERSION=22 FORCE=1), '
            'delete these rows -- or on a local plane, make dataplane-reset FORCE=1 -- then '
            'migrate again.', dispatches;
    END IF;

    -- Design D5: two refusal classes, counted separately, each with its own
    -- remedy. They are exhaustive over what the pre-000023 schema PERMITS,
    -- not over what today's writers produce: nothing ties prompt_pack_id or
    -- prompt_hash to the kind, and text admits the empty string.
    --
    -- Class 1: an agent whose identity is missing or is not the legacy
    -- content-identity form. The trim and the pattern are what make a blank
    -- name or a malformed hash refused rather than converted.
    SELECT count(*) INTO no_identity FROM principal_instances
     WHERE kind = 'agent'
       AND (prompt_pack_id IS NULL
            OR btrim(prompt_pack_id, E' \t\r\n') = ''
            OR prompt_hash IS NULL
            OR prompt_hash !~ '^sha256:[0-9a-f]{64}$');

    -- Class 2: a non-agent carrying prompt fields. Nulling them would be a
    -- silent conversion of a row that says something the new shape cannot.
    SELECT count(*) INTO stray_fields FROM principal_instances
     WHERE kind <> 'agent'
       AND (prompt_pack_id IS NOT NULL OR prompt_hash IS NOT NULL);

    IF no_identity > 0 OR stray_fields > 0 THEN
        RAISE EXCEPTION 'cannot apply 000023: % agent principal(s) have a missing or invalid prompt '
            'identity (a NULL or blank prompt_pack_id, or a prompt_hash not of the form '
            'sha256:<64 hex>) -- each records a run whose pack was never captured, and this '
            'migration will not invent one; and % non-agent principal(s) carry prompt fields, '
            'which the new shape cannot hold and this migration will not silently drop. Force the '
            'version back (make dataplane-force-version VERSION=22 FORCE=1), resolve or delete '
            'these rows, then migrate again.', no_identity, stray_fields;
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- Step 2: two small IMMUTABLE functions the row constraints call.
--
-- A CHECK may not contain a subquery, and it may call an immutable function.
-- Both are dropped by the down migration.
-- ---------------------------------------------------------------------------

-- Declared role coverage is a SET stored as a jsonb object keyed by role, and
-- jsonb equality is set equality only when every value is the constant true:
-- {"coder": true} and {"coder": 1} have identical keys and compare unequal.
CREATE FUNCTION prompt_pack_roles_canonical(roles jsonb) RETURNS boolean
    LANGUAGE sql IMMUTABLE STRICT AS $$
    -- CASE, not AND: SQL does not promise evaluation order, and jsonb_each
    -- raises on a non-object, which would refuse with the wrong message.
    SELECT CASE WHEN jsonb_typeof(roles) <> 'object' THEN false
           ELSE NOT EXISTS (
               SELECT 1 FROM jsonb_each(roles) AS entry(key, value)
                WHERE entry.key = '' OR entry.value <> 'true'::jsonb)
           END
$$;

-- Entries are the slot-key -> entry-text object the digest is computed over,
-- and nothing else: string values, non-empty keys.
CREATE FUNCTION prompt_pack_entries_canonical(entries jsonb) RETURNS boolean
    LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT CASE WHEN jsonb_typeof(entries) <> 'object' THEN false
           ELSE NOT EXISTS (
               SELECT 1 FROM jsonb_each(entries) AS entry(key, value)
                WHERE entry.key = '' OR jsonb_typeof(entry.value) <> 'string')
           END
$$;

-- ---------------------------------------------------------------------------
-- Step 3: prompt_pack_contents (design D4, D6).
--
-- The identity is global and the row is not: unique on (organization, scheme,
-- digest), so two organizations holding one pack hold two rows with one
-- identity. The scheme is a CLOSED enumeration of one, because the legacy
-- scheme is never computed by the plane -- a v1-manifest-sha256 content row
-- would be a pack the plane claims to own and could not have digested.
-- ---------------------------------------------------------------------------
CREATE TABLE prompt_pack_contents (
    content_id      uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    scheme          text        NOT NULL,
    digest          text        NOT NULL,
    entries         jsonb       NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT prompt_pack_contents_scheme_check
        CHECK (scheme = 'pack-jcs-sha256-v1'),
    -- The plane's own scheme stores the BARE hex canonical.Digest returns.
    CONSTRAINT prompt_pack_contents_digest_check
        CHECK (digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT prompt_pack_contents_entries_check
        CHECK (prompt_pack_entries_canonical(entries)),

    CONSTRAINT prompt_pack_contents_identity_key
        UNIQUE (organization_id, scheme, digest),
    -- Redundant with the primary key as a uniqueness fact; present so a
    -- reference can bind the identity and not only the tenant (design D5).
    CONSTRAINT prompt_pack_contents_id_identity_key
        UNIQUE (content_id, organization_id, scheme, digest),
    CONSTRAINT prompt_pack_contents_id_org_key
        UNIQUE (content_id, organization_id)
);

-- THE SCHEMA'S FIRST TRIGGER. UNIQUE stops a second write of the same
-- identity; it does not stop an UPDATE rewriting entries and digest together
-- under the original primary key, which is not an edit but a lie. Phase 2's
-- immutability was creation-time uniqueness plus the absence of an update
-- query -- a convention an UPDATE through psql does not respect. Deletion is
-- left to the ON DELETE RESTRICT references from installations, resolutions
-- and principals: unreferenced content may go, referenced content cannot.
--
-- Idempotent insertion is therefore ON CONFLICT DO NOTHING, never DO UPDATE,
-- which would fire this on the path that is supposed to be a no-op.
CREATE FUNCTION prompt_pack_contents_refuse_update() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'prompt_pack_contents is immutable: content % is identified by its content, so '
        'a rewrite under the same id would be a different pack wearing this one''s identity. '
        'Install a new version instead.', OLD.content_id
        USING ERRCODE = 'integrity_constraint_violation';
END $$;

CREATE TRIGGER prompt_pack_contents_immutable
    BEFORE UPDATE ON prompt_pack_contents
    FOR EACH ROW EXECUTE FUNCTION prompt_pack_contents_refuse_update();

-- ---------------------------------------------------------------------------
-- Step 4: prompt_pack_installations (design D6).
--
-- At most one per (organization, content): without that a selector naming a
-- digest is ambiguous the moment two installations of one content disagree
-- about their declared metadata -- the defect ADR 0031 closed for names,
-- reappearing one level down.
-- ---------------------------------------------------------------------------
CREATE TABLE prompt_pack_installations (
    installation_id           uuid        PRIMARY KEY,
    organization_id           uuid        NOT NULL,
    content_id                uuid        NOT NULL,
    display_name              text        NOT NULL,

    -- Declared Maestro range: min inclusive, max exclusive, both full
    -- semantic versions with their leading v. The row checks the FORM; that
    -- min sorts strictly below max is semver precedence, which the seam
    -- checks on every install and update (harness.CheckRange) and which no
    -- regular expression can state.
    min_maestro_version       text        NOT NULL,
    max_maestro_version       text        NOT NULL,

    -- A set, stored as {"<role>": true, ...}.
    declared_roles            jsonb       NOT NULL,

    -- The governed installer identity: the built-in records the binary that
    -- carried it, an operator-supplied pack records the user.
    installed_by_kind         text        NOT NULL,
    installed_by_user_id      uuid,
    builtin_maestro_version   text,

    -- The Maestro version the gate last validated this installation
    -- against, written on every install and update. Dispatch reads it to
    -- decide whether the harness has moved (design D8).
    validated_maestro_version text        NOT NULL,

    -- Starts at 1; the next value is seam-derived (revision = revision + 1
    -- under WHERE revision = expected). Monotonic is those two facts plus
    -- this check, which is what refuses a direct write of 0.
    revision                  integer     NOT NULL,

    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT prompt_pack_installations_display_name_check
        CHECK (btrim(display_name, E' \t\r\n') <> ''),
    CONSTRAINT prompt_pack_installations_min_version_check
        CHECK (min_maestro_version ~ '^v[0-9]+\.[0-9]+\.[0-9]+'),
    CONSTRAINT prompt_pack_installations_max_version_check
        CHECK (max_maestro_version ~ '^v[0-9]+\.[0-9]+\.[0-9]+'),
    CONSTRAINT prompt_pack_installations_declared_roles_check
        CHECK (prompt_pack_roles_canonical(declared_roles)),
    CONSTRAINT prompt_pack_installations_installer_kind_check
        CHECK (installed_by_kind IN ('builtin', 'user')),
    CONSTRAINT prompt_pack_installations_installer_user_check
        CHECK ((installed_by_kind = 'user') = (installed_by_user_id IS NOT NULL)),
    CONSTRAINT prompt_pack_installations_installer_builtin_check
        CHECK ((installed_by_kind = 'builtin') = (builtin_maestro_version IS NOT NULL)),
    CONSTRAINT prompt_pack_installations_validated_version_check
        CHECK (btrim(validated_maestro_version, E' \t\r\n') <> ''),
    CONSTRAINT prompt_pack_installations_revision_check
        CHECK (revision >= 1),

    CONSTRAINT prompt_pack_installations_content_fkey
        FOREIGN KEY (content_id, organization_id)
        REFERENCES prompt_pack_contents (content_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT prompt_pack_installations_user_fkey
        FOREIGN KEY (installed_by_user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT prompt_pack_installations_one_per_content_key
        UNIQUE (organization_id, content_id),
    -- The reference target that binds an installation to ITS content.
    CONSTRAINT prompt_pack_installations_id_content_key
        UNIQUE (installation_id, organization_id, content_id)
);

-- ---------------------------------------------------------------------------
-- Step 5: dispatch_prompt_resolutions (design D8).
--
-- The resolved pack, beside the thing that decided it. Unique on the dispatch;
-- every reference composite on organization_id; identity references bound to
-- the content and installation rows they name, exactly as a resolved
-- principal's are, so the dispatched-principal verb copies from a row that
-- was already bound.
--
-- range_check is `passed` or `not-evaluated` and nothing else. A refused
-- range is a dispatch that was never written, so there is no parent for a
-- `refused` row to hang from.
-- ---------------------------------------------------------------------------

-- The reference target for the resolution's FULL lineage tuple. The existing
-- keys carry either is_accepted or omit story_id.
ALTER TABLE story_dispatches
    ADD CONSTRAINT story_dispatches_full_lineage_key
        UNIQUE (story_dispatch_id, story_id, epic_id, feature_id, product_id, organization_id);

CREATE TABLE dispatch_prompt_resolutions (
    resolution_id             uuid        PRIMARY KEY,
    story_dispatch_id         uuid        NOT NULL,
    organization_id           uuid        NOT NULL,
    product_id                uuid        NOT NULL,
    feature_id                uuid        NOT NULL,
    epic_id                   uuid        NOT NULL,
    story_id                  uuid        NOT NULL,

    resolved_name             text        NOT NULL,
    scheme                    text        NOT NULL,
    digest                    text        NOT NULL,
    content_id                uuid        NOT NULL,
    installation_id           uuid        NOT NULL,
    installation_revision     integer     NOT NULL,
    metadata_snapshot         jsonb       NOT NULL,
    validated_maestro_version text        NOT NULL,
    range_check               text        NOT NULL,
    resolved_at               timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT dispatch_prompt_resolutions_scheme_check
        CHECK (scheme = 'pack-jcs-sha256-v1'),
    CONSTRAINT dispatch_prompt_resolutions_digest_check
        CHECK (digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT dispatch_prompt_resolutions_revision_check
        CHECK (installation_revision >= 1),
    CONSTRAINT dispatch_prompt_resolutions_snapshot_check
        CHECK (jsonb_typeof(metadata_snapshot) = 'object'),
    CONSTRAINT dispatch_prompt_resolutions_validated_version_check
        CHECK (btrim(validated_maestro_version, E' \t\r\n') <> ''),
    CONSTRAINT dispatch_prompt_resolutions_range_check_check
        CHECK (range_check IN ('passed', 'not-evaluated')),

    -- Not deferred: a resolution needs its dispatch present when written,
    -- which is why insertion is parent-first (design D8).
    CONSTRAINT dispatch_prompt_resolutions_dispatch_fkey
        FOREIGN KEY (story_dispatch_id, story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES story_dispatches (story_dispatch_id, story_id, epic_id, feature_id, product_id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT dispatch_prompt_resolutions_content_fkey
        FOREIGN KEY (content_id, organization_id, scheme, digest)
        REFERENCES prompt_pack_contents (content_id, organization_id, scheme, digest) ON DELETE RESTRICT,
    CONSTRAINT dispatch_prompt_resolutions_installation_fkey
        FOREIGN KEY (installation_id, organization_id, content_id)
        REFERENCES prompt_pack_installations (installation_id, organization_id, content_id) ON DELETE RESTRICT,

    CONSTRAINT dispatch_prompt_resolutions_one_per_dispatch_key
        UNIQUE (story_dispatch_id),
    -- The target of the dispatch's reciprocal reference below.
    CONSTRAINT dispatch_prompt_resolutions_dispatch_pair_key
        UNIQUE (story_dispatch_id, resolution_id)
);

CREATE INDEX dispatch_prompt_resolutions_content_idx
    ON dispatch_prompt_resolutions (organization_id, content_id);

-- THE RECIPROCAL DEFERRED FOREIGN KEY. NOT NULL is possible because step 1
-- guaranteed the table is empty. The pair is checked at COMMIT, so the
-- dispatch can be inserted before the resolution it names exists -- and an
-- old-code seam that inserts a dispatch without this column is refused
-- immediately by NOT NULL, while one that supplies it and never writes the
-- resolution is refused at commit. In steady state, deleting a resolution a
-- dispatch names, or changing its identity from under the dispatch, fails on
-- this constraint by name.
ALTER TABLE story_dispatches
    ADD COLUMN prompt_resolution_id uuid NOT NULL,
    ADD CONSTRAINT story_dispatches_prompt_resolution_fkey
        FOREIGN KEY (story_dispatch_id, prompt_resolution_id)
        REFERENCES dispatch_prompt_resolutions (story_dispatch_id, resolution_id)
        DEFERRABLE INITIALLY DEFERRED;

-- ---------------------------------------------------------------------------
-- Step 6: the principal_instances split (design D5).
--
-- Every agent row that survived step 1 is a foreign import with a
-- legacy-scheme identity, so the backfill is total: origin foreign, scheme
-- v1-manifest-sha256, name from the old column. Digest bytes are never
-- rewritten; under the legacy scheme the stored digest keeps its sha256:
-- prefix as DATA, and the scheme column says which form to expect.
-- ---------------------------------------------------------------------------
ALTER TABLE principal_instances
    ADD COLUMN prompt_pack_origin                text,
    ADD COLUMN prompt_pack_name                  text,
    ADD COLUMN prompt_pack_scheme                text,
    ADD COLUMN prompt_pack_content_id            uuid,
    ADD COLUMN prompt_pack_installation_id       uuid,
    ADD COLUMN prompt_pack_installation_revision integer,
    ADD COLUMN prompt_pack_metadata_snapshot     jsonb;

UPDATE principal_instances
   SET prompt_pack_origin = 'foreign',
       prompt_pack_scheme = 'v1-manifest-sha256',
       prompt_pack_name   = prompt_pack_id
 WHERE kind = 'agent';

ALTER TABLE principal_instances DROP COLUMN prompt_pack_id;

ALTER TABLE principal_instances
    ADD CONSTRAINT principal_instances_prompt_pack_origin_check
        CHECK (prompt_pack_origin IS NULL OR prompt_pack_origin IN ('foreign', 'resolved')),

    -- ONE constraint, three exhaustive shapes, and no NULL can slip through.
    --
    -- A CHECK whose operand is NULL evaluates to NULL and PASSES, so a
    -- constraint written as `origin = 'resolved' AND ...` refuses nothing on a
    -- row with a NULL origin. Each disjunct below is therefore stated
    -- positively over ALL EIGHT columns with num_nulls/num_nonnulls, which
    -- are never NULL, and the origin equality is reached only once
    -- num_nonnulls has proved the origin present -- `false AND NULL` is false.
    --
    --   non-agent      : all eight NULL
    --   foreign agent  : origin, name, hash, scheme present; four references NULL
    --   resolved agent : all eight present
    --
    -- A system principal with a name, a foreign agent with one reference, or
    -- an agent with no origin matches no disjunct and is refused.
    ADD CONSTRAINT principal_instances_prompt_pack_shape_check
        CHECK (
            (kind IN ('human', 'system')
             AND num_nulls(prompt_pack_origin, prompt_pack_name, prompt_hash, prompt_pack_scheme,
                           prompt_pack_content_id, prompt_pack_installation_id,
                           prompt_pack_installation_revision, prompt_pack_metadata_snapshot) = 8)
            OR
            (kind = 'agent'
             AND num_nonnulls(prompt_pack_origin, prompt_pack_name, prompt_hash, prompt_pack_scheme) = 4
             AND prompt_pack_origin = 'foreign'
             AND num_nulls(prompt_pack_content_id, prompt_pack_installation_id,
                           prompt_pack_installation_revision, prompt_pack_metadata_snapshot) = 4)
            OR
            (kind = 'agent'
             AND num_nonnulls(prompt_pack_origin, prompt_pack_name, prompt_hash, prompt_pack_scheme,
                              prompt_pack_content_id, prompt_pack_installation_id,
                              prompt_pack_installation_revision, prompt_pack_metadata_snapshot) = 8
             AND prompt_pack_origin = 'resolved')
        ),

    -- Per-scheme format checks as a CLOSED enumeration: each origin is paired
    -- with exactly one scheme and its form, and nothing else is admitted. A
    -- conditional that named only the schemes it knew would PASS an unknown
    -- one. Guarded by IS NOT NULL first, so a NULL origin cannot turn the
    -- disjunction into a passing NULL.
    ADD CONSTRAINT principal_instances_prompt_pack_scheme_check
        CHECK (
            prompt_pack_scheme IS NULL
            OR (prompt_pack_origin IS NOT NULL
                AND prompt_pack_origin = 'foreign'
                AND prompt_pack_scheme = 'v1-manifest-sha256'
                AND prompt_hash ~ '^sha256:[0-9a-f]{64}$')
            OR (prompt_pack_origin IS NOT NULL
                AND prompt_pack_origin = 'resolved'
                AND prompt_pack_scheme = 'pack-jcs-sha256-v1'
                AND prompt_hash ~ '^[0-9a-f]{64}$')
        ),
    ADD CONSTRAINT principal_instances_prompt_pack_name_check
        CHECK (prompt_pack_name IS NULL OR btrim(prompt_pack_name, E' \t\r\n') <> ''),
    ADD CONSTRAINT principal_instances_prompt_pack_revision_check
        CHECK (prompt_pack_installation_revision IS NULL OR prompt_pack_installation_revision >= 1),
    ADD CONSTRAINT principal_instances_prompt_pack_snapshot_check
        CHECK (prompt_pack_metadata_snapshot IS NULL OR jsonb_typeof(prompt_pack_metadata_snapshot) = 'object'),

    -- The resolved shape's references bind the IDENTITY, not only the
    -- tenant: the recorded scheme and digest must agree with the content row
    -- named, and the installation named must be an installation of that
    -- content (ADR 0031 section 2). Unchecked under MATCH SIMPLE when any
    -- column is NULL, which the shape constraint makes exactly the foreign
    -- and non-agent cases.
    ADD CONSTRAINT principal_instances_prompt_pack_content_fkey
        FOREIGN KEY (prompt_pack_content_id, organization_id, prompt_pack_scheme, prompt_hash)
        REFERENCES prompt_pack_contents (content_id, organization_id, scheme, digest) ON DELETE RESTRICT,
    ADD CONSTRAINT principal_instances_prompt_pack_installation_fkey
        FOREIGN KEY (prompt_pack_installation_id, organization_id, prompt_pack_content_id)
        REFERENCES prompt_pack_installations (installation_id, organization_id, content_id) ON DELETE RESTRICT;

-- The MPH query filters on scheme AND digest, never digest alone (design
-- D4); this is its index.
CREATE INDEX principal_instances_prompt_identity_idx
    ON principal_instances (organization_id, prompt_pack_scheme, prompt_hash);

COMMIT;
