-- The work-hierarchy execution families and the dispatch basis
-- (docs/v2/phase_3/design_work-hierarchy.md, Phase 3 item 2).
--
-- ADR and consumer, per the plan's reserved-by-name rule:
--
--   work_groups                  ADR 0018 (the per-Epic unit of execution)
--                                -> item 3, which dispatches into one
--   executions                   ADR 0019 as amended (superseding "the
--                                execution's own authority" needs a durable
--                                referent); ADR 0032 binding items 8, 9, 11
--                                -> item 5 checks authority, item 9 writes it
--   story_dispatches             ADR 0019 as amended (the dispatch, its
--                                disposition, and its basis)
--                                -> item 3 writes, item 9 compares
--   dispatch_basis_dependencies  ADR 0019 test 2 (the basis is a SET)
--                                -> item 9's comparison
--   epic_dependencies            ADR 0024 ("intake persists the full Epic
--                                dependency graph")  -> item 11, item 9
--   story_dependencies           ADR 0024 (the Architect owns the graph
--                                within an Epic); ADR 0019 test 2
--                                -> item 9, item 10
--
-- Two ideas run through the whole file.
--
-- FIRST: every reference travels the WHOLE lineage tuple, as migration
-- 000003 established. Narrower keys are not weaker constraints, they are
-- constraints that admit the wrong parent -- an execution carrying Story B's
-- lineage while naming an accepted dispatch for Story A in the same
-- organization. Migration 000005 already names the cost: provenance that can
-- name the wrong parent is worse than none, because it reads as evidence.
--
-- SECOND: the basis has two sides. These tables store the SNAPSHOT a dispatch
-- was issued under, and the pointers at the bottom of this file store what is
-- CURRENT. Item 9 compares them. Nothing here may constrain the two to agree
-- -- their divergence is the signal, and a key enforcing equality would make
-- the observable condition unrepresentable.
BEGIN;

-- ---------------------------------------------------------------------------
-- Referencable keys on management_artifacts.
--
-- Both are additive supersets of the table's primary key, so no row it
-- previously accepted becomes invalid. They exist so a reference can prove in
-- SQL that the artifact it names is scoped to the work item doing the naming,
-- rather than merely to the same organization. Without them the scope claim
-- would be seam-validated, which is a rule someone must remember rather than
-- one the schema keeps.
--
-- is_amendment is part of both keys because an effective view is keyed by the
-- ORIGINAL's identity (ADR 0021); a reference to an amendment row names a
-- patch, not a view.
-- ---------------------------------------------------------------------------
ALTER TABLE management_artifacts
    ADD CONSTRAINT management_artifacts_story_scope_key
        UNIQUE (artifact_id, is_amendment, scope_story_id, organization_id),
    ADD CONSTRAINT management_artifacts_epic_scope_key
        UNIQUE (artifact_id, is_amendment, scope_epic_id, organization_id);

-- ---------------------------------------------------------------------------
-- work_groups
--
-- ADR 0018 assigns one Work Group to one Epic and defers concurrent Work
-- Groups to post-MVP. The one-per-Epic key is therefore an MVP constraint
-- that post-MVP must deliberately DROP -- recorded so its removal is a
-- decision someone makes rather than a surprise.
--
-- The columns ADR 0018 lists that are absent -- prompt pack, gates, workspace,
-- harness configuration -- arrive with the items that consume them (4, 5-7,
-- Phase 5). What lands now is identity, lineage, and cardinality.
-- ---------------------------------------------------------------------------
CREATE TABLE work_groups (
    work_group_id   uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL,
    product_id      uuid        NOT NULL,
    feature_id      uuid        NOT NULL,
    epic_id         uuid        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT work_groups_epic_fkey
        FOREIGN KEY (epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT work_groups_one_per_epic_key UNIQUE (epic_id, organization_id),

    -- Consumed by story_dispatches: a dispatch names its Work Group through
    -- the Epic lineage, so borrowing another Epic's Work Group is
    -- unrepresentable rather than merely wrong.
    CONSTRAINT work_groups_lineage_key
        UNIQUE (work_group_id, epic_id, feature_id, product_id, organization_id)
);

CREATE INDEX work_groups_epic_id_idx ON work_groups (epic_id);

-- ---------------------------------------------------------------------------
-- story_dispatches
--
-- A dispatch is not just a timestamp. ADR 0019 requires a pending
-- version-bound dispatch to be invalidated when the basis moves and reissued;
-- ADR 0032 requires a handshake failure to be recorded DURABLY while creating
-- no execution. With only dispatched_at, every zero-execution dispatch is
-- ambiguous between "not yet answered", "refused" and "invalidated".
--
-- The dispositions are a lifecycle -- pending -> accepted | failed |
-- invalidated, with every terminal disposition IMMUTABLE. The constraints
-- below express the row's shape, not that lifecycle: nothing here stops a
-- failed row being set back to pending. Enforcement is item 3's named
-- conditional transitions (UPDATE ... WHERE disposition = 'pending', where
-- zero rows affected is a rejected transition). This schema uses no triggers
-- anywhere and ADR 0022 makes the seam the only writer, so there is no
-- production path around them.
--
-- The two version references are test 1's governing version set, which
-- ADR 0019 fixes at EXACTLY two members for this grain -- the Story's
-- effective version and its governing Epic's. Two members is two column
-- groups; a child table would represent a three-member set the ADR forbids.
-- ---------------------------------------------------------------------------
CREATE TABLE story_dispatches (
    story_dispatch_id uuid        PRIMARY KEY,
    organization_id   uuid        NOT NULL,
    product_id        uuid        NOT NULL,
    feature_id        uuid        NOT NULL,
    epic_id           uuid        NOT NULL,
    story_id          uuid        NOT NULL,
    work_group_id     uuid        NOT NULL,
    dispatched_at     timestamptz NOT NULL DEFAULT now(),

    disposition       text        NOT NULL,
    -- Generated rather than a second writable column, so it cannot disagree
    -- with the disposition it summarises. Consumed by executions' composite
    -- foreign key (000006's amends_target_is_amendment idiom).
    is_accepted       boolean     GENERATED ALWAYS AS (disposition = 'accepted') STORED NOT NULL,
    settled_at        timestamptz,
    failure_code      text,
    failure_detail    text,

    -- Test 1, member 1: the Story's effective version at dispatch.
    story_version_artifact_id        uuid    NOT NULL,
    story_version_is_amendment       boolean NOT NULL DEFAULT false,
    story_version_effective_digest   text    NOT NULL,
    story_version_effective_sequence int     NOT NULL,

    -- Test 1, member 2: the governing Epic's effective version at dispatch.
    epic_version_artifact_id         uuid    NOT NULL,
    epic_version_is_amendment        boolean NOT NULL DEFAULT false,
    epic_version_effective_digest    text    NOT NULL,
    epic_version_effective_sequence  int     NOT NULL,

    CONSTRAINT story_dispatches_disposition_check
        CHECK (disposition IN ('pending', 'accepted', 'failed', 'invalidated')),

    -- Every non-pending disposition records when it settled.
    CONSTRAINT story_dispatches_settled_check
        CHECK ((disposition = 'pending') = (settled_at IS NULL)),

    -- A failure carries a STABLE code; the detail is prose and is never the
    -- discriminator a consumer branches on.
    CONSTRAINT story_dispatches_failure_code_check
        CHECK ((disposition = 'failed') = (failure_code IS NOT NULL)),
    CONSTRAINT story_dispatches_failure_detail_check
        CHECK (failure_detail IS NULL OR failure_code IS NOT NULL),

    -- The discriminators are NOT NULL above. A nullable one would let a
    -- non-null artifact id sit beside a null discriminator, which skips the
    -- whole composite foreign key under MATCH SIMPLE and takes the
    -- original-only AND scope claims with it.
    CONSTRAINT story_dispatches_story_version_original_check
        CHECK (NOT story_version_is_amendment),
    CONSTRAINT story_dispatches_epic_version_original_check
        CHECK (NOT epic_version_is_amendment),

    CONSTRAINT story_dispatches_story_version_digest_check
        CHECK (story_version_effective_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT story_dispatches_epic_version_digest_check
        CHECK (epic_version_effective_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT story_dispatches_story_version_sequence_check
        CHECK (story_version_effective_sequence >= 0),
    CONSTRAINT story_dispatches_epic_version_sequence_check
        CHECK (epic_version_effective_sequence >= 0),

    CONSTRAINT story_dispatches_story_fkey
        FOREIGN KEY (story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT story_dispatches_work_group_fkey
        FOREIGN KEY (work_group_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES work_groups (work_group_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,

    -- Each governing reference is bound to its OWN work item through the
    -- scope column. Without scope in the key a Story's version reference
    -- could name another Story's artifact and still satisfy the tenant check.
    CONSTRAINT story_dispatches_story_version_fkey
        FOREIGN KEY (story_version_artifact_id, story_version_is_amendment, story_id, organization_id)
        REFERENCES management_artifacts (artifact_id, is_amendment, scope_story_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT story_dispatches_epic_version_fkey
        FOREIGN KEY (epic_version_artifact_id, epic_version_is_amendment, epic_id, organization_id)
        REFERENCES management_artifacts (artifact_id, is_amendment, scope_epic_id, organization_id) ON DELETE RESTRICT,

    -- Consumed by executions, through the full Story lineage.
    CONSTRAINT story_dispatches_accepted_key
        UNIQUE (story_dispatch_id, is_accepted, story_id, epic_id, feature_id, product_id, organization_id),
    -- Consumed by dispatch_basis_dependencies.
    CONSTRAINT story_dispatches_lineage_key
        UNIQUE (story_dispatch_id, epic_id, feature_id, product_id, organization_id)
);

CREATE INDEX story_dispatches_story_id_idx    ON story_dispatches (story_id);
CREATE INDEX story_dispatches_work_group_idx  ON story_dispatches (work_group_id);
CREATE INDEX story_dispatches_disposition_idx ON story_dispatches (disposition);

-- ---------------------------------------------------------------------------
-- executions
--
-- One row per LOGICAL, Story-scoped execution, which may span several runtime
-- incarnations: ADR 0029 section 2 scopes the Incubator to the Story
-- execution rather than the Agent principal, so agent restart or replacement
-- preserves the work and a replacement agent RESUMES THE SAME EXECUTION.
--
-- What is deliberately absent:
--
--   principal_instance_id -- principals and incarnations are many-to-one over
--   an execution, and which direction the association runs is what the
--   restart lifecycle settles. Item 2 asserts neither.
--
--   the resolved configuration and the per-incarnation bindings (model route,
--   capability set, budgets, epoch, resume token, resource references) --
--   ADR 0032 section 2, under that ADR's own demotion notice. Their
--   persistence shape is settled in items 5/6 against a real consumer.
-- ---------------------------------------------------------------------------
CREATE TABLE executions (
    execution_id         uuid        PRIMARY KEY,
    organization_id      uuid        NOT NULL,
    product_id           uuid        NOT NULL,
    feature_id           uuid        NOT NULL,
    epic_id              uuid        NOT NULL,
    story_id             uuid        NOT NULL,

    story_dispatch_id    uuid        NOT NULL,
    -- Constant. Paired with story_dispatches.is_accepted in the foreign key
    -- below, it makes an execution against a pending, failed or invalidated
    -- dispatch unrepresentable. The column alone is decoration; the exclusion
    -- comes from the key.
    dispatch_is_accepted boolean     NOT NULL DEFAULT true,

    authority_state      text        NOT NULL DEFAULT 'current',
    admission_closed_at  timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT executions_story_fkey
        FOREIGN KEY (story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT executions_dispatch_fkey
        FOREIGN KEY (story_dispatch_id, dispatch_is_accepted,
                     story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES story_dispatches (story_dispatch_id, is_accepted,
                     story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT executions_dispatch_accepted_check CHECK (dispatch_is_accepted),

    CONSTRAINT executions_authority_state_check
        CHECK (authority_state IN ('current', 'superseded')),

    -- ADR 0019's sequence marks authority superseded AND closes admission
    -- together. The converse does NOT hold: a headless block is a forced stop
    -- that closes admission with no dispatch-basis supersession at all
    -- (ADR 0032), so closed admission under current authority is a real
    -- state and only this direction is constrained.
    CONSTRAINT executions_superseded_closes_admission_check
        CHECK (authority_state <> 'superseded' OR admission_closed_at IS NOT NULL),

    -- At most one execution per dispatch. A refused invocation produces no
    -- execution (ADR 0032); restart and principal replacement stay within the
    -- same logical execution; a configuration change is a NEW dispatch. The
    -- other half -- that an accepted dispatch has at LEAST one execution --
    -- is cross-table and is a seam invariant committed with the disposition
    -- flip.
    CONSTRAINT executions_one_per_dispatch_key UNIQUE (story_dispatch_id, organization_id),

    -- Consumed by tool_calls.execution_id in migration 000022, through the
    -- full lineage.
    CONSTRAINT executions_lineage_key
        UNIQUE (execution_id, story_id, epic_id, feature_id, product_id, organization_id)
);

CREATE INDEX executions_story_id_idx        ON executions (story_id);
CREATE INDEX executions_authority_state_idx ON executions (authority_state);

-- ---------------------------------------------------------------------------
-- dispatch_basis_dependencies -- test 2's snapshot
--
-- ADR 0019: the incoming dependency basis is "the work item's own incoming
-- edges: the identities of its predecessors, together with the effective
-- completions that satisfied them". A set of unbounded size, so a child table
-- rather than columns.
--
-- The shared lineage columns are what make a row PROVABLE. A child holding
-- only two UUIDs cannot show that the predecessor belongs to the dispatch's
-- own organization and Epic, nor that the completion is scoped to that
-- predecessor; sharing the lineage across all three foreign keys makes all
-- three true by construction.
--
-- The completion columns are NOT NULL because a Story is dispatched only when
-- dependency-ready, so every predecessor in the basis was satisfied at that
-- moment. (The CURRENT-side pointer on story_dependencies is nullable for the
-- opposite reason: an edge exists before its predecessor completes.)
-- ---------------------------------------------------------------------------
CREATE TABLE dispatch_basis_dependencies (
    story_dispatch_id    uuid NOT NULL,
    organization_id      uuid NOT NULL,
    product_id           uuid NOT NULL,
    feature_id           uuid NOT NULL,
    epic_id              uuid NOT NULL,
    predecessor_story_id uuid NOT NULL,

    completion_artifact_id        uuid    NOT NULL,
    completion_is_amendment       boolean NOT NULL DEFAULT false,
    completion_effective_digest   text    NOT NULL,
    completion_effective_sequence int     NOT NULL,

    PRIMARY KEY (story_dispatch_id, predecessor_story_id),

    CONSTRAINT dispatch_basis_dependencies_dispatch_fkey
        FOREIGN KEY (story_dispatch_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES story_dispatches (story_dispatch_id, epic_id, feature_id, product_id, organization_id)
        ON DELETE RESTRICT,

    CONSTRAINT dispatch_basis_dependencies_predecessor_fkey
        FOREIGN KEY (predecessor_story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,

    CONSTRAINT dispatch_basis_dependencies_completion_fkey
        FOREIGN KEY (completion_artifact_id, completion_is_amendment,
                     predecessor_story_id, organization_id)
        REFERENCES management_artifacts (artifact_id, is_amendment, scope_story_id, organization_id)
        ON DELETE RESTRICT,

    CONSTRAINT dispatch_basis_dependencies_completion_original_check
        CHECK (NOT completion_is_amendment),
    CONSTRAINT dispatch_basis_dependencies_completion_digest_check
        CHECK (completion_effective_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT dispatch_basis_dependencies_completion_sequence_check
        CHECK (completion_effective_sequence >= 0)
);

CREATE INDEX dispatch_basis_dependencies_predecessor_idx
    ON dispatch_basis_dependencies (predecessor_story_id);

-- ---------------------------------------------------------------------------
-- The dependency graphs -- test 2's CURRENT side, and ADR 0024's requirement
--
-- ADR 0024 requires both grains with different owners: intake persists the
-- Epic graph; the Architect owns the Story graph within an Epic. Two typed
-- tables rather than one polymorphic (kind, id) table, because a polymorphic
-- shape would discard the real foreign keys and lineage tuples that make a
-- cross-Epic or cross-Feature edge unrepresentable.
--
-- Sharing the lineage columns between BOTH endpoints is what carries
-- ADR 0024's "within an Epic" -- and "within a Feature" one level up -- in
-- the schema rather than in a rule someone remembers.
--
-- ACYCLICITY IS NOT ENFORCED HERE and cannot be: a CHECK cannot traverse.
-- The self-edge is the one-hop case and is constrained. Everything beyond it
-- is the Orchestrator's, and a bare check-then-insert is a DEFECT -- two
-- concurrent transactions adding A->B and B->A each observe an acyclic graph
-- and both commit. Every graph mutation must be serialized under the same
-- stable parent row (the Feature for the Epic graph, the Epic for the Story
-- graph), with the recursive check and the mutation in that one transaction.
-- ADR 0027, applied here. Item 9 owns it, with a forced-interleaving test.
-- ---------------------------------------------------------------------------
CREATE TABLE epic_dependencies (
    organization_id       uuid        NOT NULL,
    product_id            uuid        NOT NULL,
    feature_id            uuid        NOT NULL,
    successor_epic_id     uuid        NOT NULL,
    predecessor_epic_id   uuid        NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (organization_id, feature_id, successor_epic_id, predecessor_epic_id),

    CONSTRAINT epic_dependencies_no_self_edge_check
        CHECK (successor_epic_id <> predecessor_epic_id),

    CONSTRAINT epic_dependencies_successor_fkey
        FOREIGN KEY (successor_epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT epic_dependencies_predecessor_fkey
        FOREIGN KEY (predecessor_epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT
);

CREATE INDEX epic_dependencies_predecessor_idx ON epic_dependencies (predecessor_epic_id);

CREATE TABLE story_dependencies (
    organization_id        uuid        NOT NULL,
    product_id             uuid        NOT NULL,
    feature_id             uuid        NOT NULL,
    epic_id                uuid        NOT NULL,
    successor_story_id     uuid        NOT NULL,
    predecessor_story_id   uuid        NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),

    -- Which completion CURRENTLY satisfies this edge (design D13). Nullable:
    -- an edge exists before its predecessor completes, which is what "not yet
    -- dependency-ready" IS. The discriminator is NOT NULL regardless, or a
    -- non-null artifact id beside a null discriminator would skip the
    -- composite foreign key under MATCH SIMPLE.
    --
    -- It names the artifact and NOTHING ELSE. No cached digest or sequence:
    -- the effective view is assembled at read time by EffectiveView, so a
    -- stored copy here would be duplicate state that an accepted amendment
    -- moves on one side and not the other. The SNAPSHOT in
    -- dispatch_basis_dependencies does carry a digest and sequence, and the
    -- asymmetry is the point -- a snapshot is a point in time and must not
    -- move, a pointer names something whose view is computed.
    satisfying_completion_artifact_id        uuid,
    satisfying_completion_is_amendment       boolean NOT NULL DEFAULT false,

    PRIMARY KEY (organization_id, epic_id, successor_story_id, predecessor_story_id),

    CONSTRAINT story_dependencies_no_self_edge_check
        CHECK (successor_story_id <> predecessor_story_id),

    CONSTRAINT story_dependencies_successor_fkey
        FOREIGN KEY (successor_story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT story_dependencies_predecessor_fkey
        FOREIGN KEY (predecessor_story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT,

    -- Scoped to the PREDECESSOR: the completion that satisfies this edge
    -- belongs to the Story being depended upon, not to the dependent one.
    CONSTRAINT story_dependencies_completion_fkey
        FOREIGN KEY (satisfying_completion_artifact_id, satisfying_completion_is_amendment,
                     predecessor_story_id, organization_id)
        REFERENCES management_artifacts (artifact_id, is_amendment, scope_story_id, organization_id)
        ON DELETE RESTRICT,

    CONSTRAINT story_dependencies_completion_original_check
        CHECK (NOT satisfying_completion_is_amendment)
);

CREATE INDEX story_dependencies_predecessor_idx ON story_dependencies (predecessor_story_id);

-- ---------------------------------------------------------------------------
-- The current governing versions (design D13)
--
-- Without these, item 9 has nothing to compare the snapshot against. Scope
-- plus type plus `accepted` does not name a governing artifact: several
-- accepted originals of a type can be scoped to one Story over its life, and
-- under ADR 0021 an accepted AMENDMENT leaves the original accepted -- it
-- moves the effective view without moving the original's status.
--
-- A derivation rule ("the latest accepted original of type X") is the
-- alternative and is rejected: it lets accepting a second artifact silently
-- redefine the basis with no record that anything moved, defeating the
-- detection it feeds.
--
-- NOTE the deliberate absence: nothing ties story_dispatches.story_version_*
-- to stories.governing_* and nothing may. Divergence between snapshot and
-- pointer IS test 1's signal, so a constraint forbidding it would make the
-- observable condition unrepresentable and the comparison vacuous.
--
-- These create a reference cycle with management_artifacts, which already
-- references stories. That is fine and is why the pointers are nullable:
-- insert the Story, then its artifact, then point at it.
-- ---------------------------------------------------------------------------
ALTER TABLE stories
    ADD COLUMN governing_artifact_id  uuid,
    ADD COLUMN governing_is_amendment boolean NOT NULL DEFAULT false,

    ADD CONSTRAINT stories_governing_original_check
        CHECK (NOT governing_is_amendment),

    ADD CONSTRAINT stories_governing_fkey
        FOREIGN KEY (governing_artifact_id, governing_is_amendment, story_id, organization_id)
        REFERENCES management_artifacts (artifact_id, is_amendment, scope_story_id, organization_id)
        ON DELETE RESTRICT;

ALTER TABLE epics
    ADD COLUMN governing_artifact_id  uuid,
    ADD COLUMN governing_is_amendment boolean NOT NULL DEFAULT false,

    ADD CONSTRAINT epics_governing_original_check
        CHECK (NOT governing_is_amendment),

    ADD CONSTRAINT epics_governing_fkey
        FOREIGN KEY (governing_artifact_id, governing_is_amendment, epic_id, organization_id)
        REFERENCES management_artifacts (artifact_id, is_amendment, scope_epic_id, organization_id)
        ON DELETE RESTRICT;

COMMIT;
