-- The mediated execution boundary's columns (docs/v2/phase_3/design_execution-boundary.md,
-- Phase 3 item 5, D12). One migration, four tables:
--
--   tool_calls                  the attempt: family, digests, the claim, the
--                               target and mutation keys, the reason code, the
--                               durable operator decision and its consumption,
--                               the drain disposition   -> D5, D7, D11
--   executions                  the resolved configuration (capability set,
--                               headless, acting user), immutable after
--                               insert; the four-axis terminal result
--                                                        -> D4, D6, D7, D11
--   principal_instances         the execution a LIVE agent principal belongs
--                               to                       -> D4 check 1
--   repository_forge_bindings   the child family migration 000002 deferred to
--                               "the forge rework"       -> D13
--
-- TOTAL OR REFUSE. capability_set, headless and acting_user_id are NOT NULL
-- with no default and no backfill: an execution accepted before this
-- migration has no honest configuration, and a default would let a writer
-- omit the set silently -- which is the exact silence D12's immutability
-- trigger exists to prevent. So this migration refuses to run against a
-- populated executions table, and against any resolved-origin agent principal
-- (a live agent exists only under an execution; item 4 design, D5). No plane
-- this phase supports holds either: the local plane is reset per phase and
-- the cloud plane was provisioned empty (#286). 000023 used the same shape.
--
-- LOCK FIRST, THEN SCAN, on 000023's reasoning: a writer that never takes the
-- lifecycle lock -- psql, a test calling migrations.Up on a DSN -- can commit
-- an execution between the scan and the ALTER, and the migration would then
-- die on NOT NULL, for the wrong reason and with no remedy in the message.
--
-- RECOVERING FROM A REFUSAL. golang-migrate marks the version BEFORE running,
-- so a refusal leaves the recorded version at 24 and DIRTY while the schema is
-- really at 23:
--
--     make dataplane-force-version VERSION=23 FORCE=1
--
-- then delete the offending rows -- or, on a local plane, `make dataplane-reset
-- FORCE=1` -- and `make dataplane-migrate`. Walked end to end in the tests.
BEGIN;

-- ---------------------------------------------------------------------------
-- Step 1: take the tables, THEN refuse.
-- ---------------------------------------------------------------------------
LOCK TABLE executions          IN ACCESS EXCLUSIVE MODE;
LOCK TABLE principal_instances IN ACCESS EXCLUSIVE MODE;
LOCK TABLE tool_calls          IN ACCESS EXCLUSIVE MODE;

DO $$
DECLARE
    live_executions bigint;
    live_agents     bigint;
BEGIN
    SELECT count(*) INTO live_executions FROM executions;
    SELECT count(*) INTO live_agents FROM principal_instances
     WHERE kind = 'agent' AND prompt_pack_origin = 'resolved';
    IF live_executions > 0 OR live_agents > 0 THEN
        RAISE EXCEPTION 'cannot apply 000024: % execution(s) and % live agent principal(s) predate the '
            'execution boundary. Every execution from here on carries its resolved capability set, '
            'headless flag and acting user, immutable from insert, and every live agent names its '
            'execution; none of these has an honest value for a row that predates them, and this '
            'migration will not invent one. Force the version back (make dataplane-force-version '
            'VERSION=23 FORCE=1), delete these rows -- or on a local plane, make dataplane-reset '
            'FORCE=1 -- then migrate again.', live_executions, live_agents;
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- Step 2: one IMMUTABLE function the executions row constraint calls.
--
-- The capability set is a CANONICAL JSON array of family identities: strings,
-- non-empty, strictly ascending -- which is sorted AND de-duplicated in one
-- predicate. The seam canonicalises before it writes (D12); this is what
-- makes the stored form the invariant rather than a convention, so a direct
-- writer cannot store ["b","a","a"] and a later comparison by digest cannot
-- disagree with one by set. A CHECK may not contain a subquery, and may call
-- an immutable function; 000023's prompt_pack_roles_canonical is the pattern.
-- ---------------------------------------------------------------------------
CREATE FUNCTION execution_capability_set_canonical(capabilities jsonb) RETURNS boolean
    LANGUAGE sql IMMUTABLE STRICT AS $$
    -- CASE, not AND: SQL does not promise evaluation order, and
    -- jsonb_array_elements raises on a non-array, which would refuse with the
    -- wrong message.
    SELECT CASE WHEN jsonb_typeof(capabilities) <> 'array' THEN false
           ELSE NOT EXISTS (
               SELECT 1
                 FROM jsonb_array_elements(capabilities) WITH ORDINALITY AS entry(value, idx)
                 LEFT JOIN jsonb_array_elements(capabilities) WITH ORDINALITY AS previous(value, idx)
                        ON previous.idx = entry.idx - 1
                WHERE jsonb_typeof(entry.value) <> 'string'
                   OR entry.value = '""'::jsonb
                   OR (previous.value IS NOT NULL AND (previous.value #>> '{}') >= (entry.value #>> '{}')))
           END
$$;

-- ---------------------------------------------------------------------------
-- Step 3: repository_forge_bindings (D12, D13).
--
-- A child family, not columns on the row: ADR 0022's logical repository "may
-- carry several forge bindings" (000002), so a second provider is a second
-- row rather than a schema change. Item 5 admits one provider; a family
-- selects its binding BY provider, and a rule for choosing among several is
-- item 10's, where the promotion path has a consumer for it.
-- ---------------------------------------------------------------------------
CREATE TABLE repository_forge_bindings (
    repository_id   uuid        NOT NULL,
    organization_id uuid        NOT NULL,
    provider        text        NOT NULL,
    base_url        text        NOT NULL,
    owner           text        NOT NULL,
    repo            text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (repository_id, provider),

    CONSTRAINT repository_forge_bindings_repository_fkey
        FOREIGN KEY (repository_id, organization_id)
        REFERENCES repositories (repository_id, organization_id) ON DELETE RESTRICT,

    -- A closed enumeration of one until a second provider has a consumer.
    CONSTRAINT repository_forge_bindings_provider_check
        CHECK (provider IN ('gitea')),
    CONSTRAINT repository_forge_bindings_base_url_check
        CHECK (base_url ~ '^https?://[^[:space:]]+$'),
    CONSTRAINT repository_forge_bindings_owner_check
        CHECK (btrim(owner, E' \t\r\n') <> ''),
    CONSTRAINT repository_forge_bindings_repo_check
        CHECK (btrim(repo, E' \t\r\n') <> '')
);

-- ---------------------------------------------------------------------------
-- Step 4: executions -- the resolved configuration (D12).
--
-- NOT NULL is possible because step 1 guaranteed the table is empty.
-- acting_user_id is the operator who accepted the dispatch: the vault's verbs
-- need an organization MEMBER and an agent principal has no user, so the
-- execution acts for its dispatcher and never for a user a request names
-- (D6). The composite key is 000001's tenant shape: an execution cannot bind
-- another organization's member.
-- ---------------------------------------------------------------------------
ALTER TABLE executions
    ADD COLUMN capability_set jsonb   NOT NULL,
    ADD COLUMN headless       boolean NOT NULL,
    ADD COLUMN acting_user_id uuid    NOT NULL,

    ADD CONSTRAINT executions_capability_set_check
        CHECK (execution_capability_set_canonical(capability_set)),

    ADD CONSTRAINT executions_acting_user_fkey
        FOREIGN KEY (acting_user_id, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,

    -- Consumed by principal_instances.execution_id below: a live agent binds
    -- its execution AND its tenant in one key, so a principal cannot name
    -- another organization's execution (000021's whole-lineage rule, applied
    -- to the one reference that has no lineage of its own).
    ADD CONSTRAINT executions_id_org_key UNIQUE (execution_id, organization_id);

-- ---------------------------------------------------------------------------
-- Step 5: executions -- the four-axis terminal result (D11; ADR 0032 section 5).
--
-- Every axis is a CLOSED vocabulary in SQL and the applicability rule is a
-- set of CHECKs, so a direct writer cannot store what TerminalResult.Validate
-- refuses: "makes invalid combinations unrepresentable" is a property of the
-- schema, not only of one Go path. The eight invalid shapes the design names
-- are each refused by exactly one constraint below, and the tests plant each.
-- ---------------------------------------------------------------------------
ALTER TABLE executions
    ADD COLUMN status                 text,
    ADD COLUMN completion_disposition text,
    ADD COLUMN cancellation_reason    text,
    ADD COLUMN failure_class          text,
    ADD COLUMN blocked_tool_call_id   uuid,
    ADD COLUMN error_message          text,
    ADD COLUMN terminated_at          timestamptz,

    ADD CONSTRAINT executions_status_check
        CHECK (status IS NULL OR status IN ('completed', 'blocked', 'cancelled', 'timed_out', 'failed')),
    ADD CONSTRAINT executions_completion_disposition_check
        CHECK (completion_disposition IS NULL OR completion_disposition IN ('changed', 'already_satisfied')),
    ADD CONSTRAINT executions_cancellation_reason_check
        CHECK (cancellation_reason IS NULL OR cancellation_reason IN ('superseded', 'operator_requested', 'shutdown')),
    ADD CONSTRAINT executions_failure_class_check
        CHECK (failure_class IS NULL OR failure_class IN ('retryable_infrastructure', 'non_retryable_agent')),

    -- The applicability rule: an axis that does not apply must be absent,
    -- and one that applies must be present. Each is an equivalence, so a
    -- present axis on the wrong status is refused as surely as a missing one.
    ADD CONSTRAINT executions_completion_axis_check
        CHECK ((status IS NOT DISTINCT FROM 'completed') = (completion_disposition IS NOT NULL)),
    ADD CONSTRAINT executions_cancellation_axis_check
        CHECK ((status IS NOT DISTINCT FROM 'cancelled') = (cancellation_reason IS NOT NULL)),
    ADD CONSTRAINT executions_failure_axis_check
        CHECK ((status IS NOT DISTINCT FROM 'failed') = (failure_class IS NOT NULL)),
    ADD CONSTRAINT executions_blocked_axis_check
        CHECK ((status IS NOT DISTINCT FROM 'blocked') = (blocked_tool_call_id IS NOT NULL)),
    -- A failure carries its diagnostic -- required, because a synthesized
    -- failure names the rule it stands in for -- and nothing else does.
    ADD CONSTRAINT executions_error_message_check
        CHECK ((status IS NOT DISTINCT FROM 'failed')
               = (error_message IS NOT NULL AND btrim(error_message, E' \t\n\r\f\v') <> '')),
    ADD CONSTRAINT executions_terminated_at_check
        CHECK ((status IS NULL) = (terminated_at IS NULL)),

    -- A terminal result is recorded only after admission has closed (D7's
    -- forced-stop order; D11's verb precondition). The verb refuses first;
    -- the row refuses a writer that is not the verb.
    ADD CONSTRAINT executions_terminal_closes_admission_check
        CHECK (status IS NULL OR admission_closed_at IS NOT NULL);

-- ---------------------------------------------------------------------------
-- Step 6: executions -- immutability (D12, on item 4's anti-update trigger).
--
-- The resolved configuration "must not silently change" (ADR 0032 item 10),
-- and a terminal result is recorded at most once (D11). UNIQUE and CHECK
-- cannot say either; a BEFORE UPDATE trigger can. Nothing legitimate updates
-- these columns after insert: the configuration arrives in AcceptDispatch's
-- INSERT because this trigger leaves no other initialization path, and the
-- terminal columns move exactly once, from all-NULL to a valid shape.
-- ---------------------------------------------------------------------------
CREATE FUNCTION executions_refuse_rewrite() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.capability_set IS DISTINCT FROM OLD.capability_set
       OR NEW.headless       IS DISTINCT FROM OLD.headless
       OR NEW.acting_user_id IS DISTINCT FROM OLD.acting_user_id THEN
        RAISE EXCEPTION 'execution % configuration is immutable: the capability set, headless flag and '
            'acting user were resolved at dispatch, and a change is a new dispatch, not an update',
            OLD.execution_id
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF OLD.status IS NOT NULL
       AND (NEW.status                 IS DISTINCT FROM OLD.status
            OR NEW.completion_disposition IS DISTINCT FROM OLD.completion_disposition
            OR NEW.cancellation_reason    IS DISTINCT FROM OLD.cancellation_reason
            OR NEW.failure_class          IS DISTINCT FROM OLD.failure_class
            OR NEW.blocked_tool_call_id   IS DISTINCT FROM OLD.blocked_tool_call_id
            OR NEW.error_message          IS DISTINCT FROM OLD.error_message
            OR NEW.terminated_at          IS DISTINCT FROM OLD.terminated_at) THEN
        RAISE EXCEPTION 'execution % already has a terminal result (%); a terminal result is recorded '
            'at most once', OLD.execution_id, OLD.status
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER executions_immutable
    BEFORE UPDATE ON executions
    FOR EACH ROW EXECUTE FUNCTION executions_refuse_rewrite();

-- ---------------------------------------------------------------------------
-- Step 7: tool_calls -- the attempt (D5, D7, D11, D12).
--
-- Every column is present exactly on the rows the boundary writes, which are
-- the rows with execution_id set. The plane's one other writer -- the
-- benchmark importer -- writes execution_id NULL and none of these, and its
-- rows, existing and future, are untouched by every presence rule below.
-- ---------------------------------------------------------------------------
ALTER TABLE tool_calls
    -- The Orchestrator-owned family identity (<kind>/<verb>); the correlation
    -- key over the caller-supplied fields; the digest over the substituted
    -- input the hook decided on and gate 3 compares; the provider's tool-call
    -- string, kept for correlation with the LLM turn and never as the key.
    ADD COLUMN family           text,
    ADD COLUMN request_digest   text,
    ADD COLUMN arguments_digest text,
    ADD COLUMN caller_ref       text,
    -- The family's declared target, for correlation and inheritance; and the
    -- shared resource the effect mutates, named family-independently, for
    -- serialization. NULL mutation_key is a family that mutates nothing shared.
    ADD COLUMN target_key       text,
    ADD COLUMN mutation_key     text,
    -- The Orchestrator instance driving an unsettled attempt.
    ADD COLUMN claimed_by       uuid,
    -- D8's T2 record: that the attempt was current when revalidated.
    ADD COLUMN revalidated_at   timestamptz,
    -- ADR 0030 section 8's reason code.
    ADD COLUMN reason_code      text,
    -- ADR 0030 section 4: "the action-scoped decision is still durable, for
    -- crash recovery". The decision stays on the row through settlement; its
    -- consumption is what turns an approval into an execution (D7), once.
    ADD COLUMN operator_decision             text,
    ADD COLUMN operator_decided_by           uuid,
    ADD COLUMN operator_decided_at           timestamptz,
    ADD COLUMN operator_decision_consumed_at timestamptz,
    ADD COLUMN operator_decision_consumed_by uuid,
    -- ADR 0032 section 6's per-attempt disposition, plus the absence of one.
    ADD COLUMN drain_disposition text;

ALTER TABLE tool_calls
    -- A boundary attempt always carries its identity and both digests; no
    -- other row ever does.
    ADD CONSTRAINT tool_calls_boundary_identity_check
        CHECK ((execution_id IS NOT NULL) = (family IS NOT NULL)
               AND (execution_id IS NOT NULL) = (request_digest IS NOT NULL)
               AND (execution_id IS NOT NULL) = (arguments_digest IS NOT NULL)
               AND (execution_id IS NOT NULL) = (target_key IS NOT NULL)),
    ADD CONSTRAINT tool_calls_family_check
        CHECK (family IS NULL OR family ~ '^[a-z][a-z0-9_]*/[a-z][a-z0-9_]*$'),
    ADD CONSTRAINT tool_calls_request_digest_check
        CHECK (request_digest IS NULL OR request_digest ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT tool_calls_arguments_digest_check
        CHECK (arguments_digest IS NULL OR arguments_digest ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT tool_calls_target_key_check
        CHECK (target_key IS NULL OR btrim(target_key, E' \t\r\n') <> ''),
    -- Permitted NULL on an execution-bound row (a non-mutating family), never
    -- present on any other.
    ADD CONSTRAINT tool_calls_mutation_key_check
        CHECK (mutation_key IS NULL
               OR (execution_id IS NOT NULL AND btrim(mutation_key, E' \t\r\n') <> '')),
    ADD CONSTRAINT tool_calls_caller_ref_check
        CHECK (caller_ref IS NULL OR execution_id IS NOT NULL),
    ADD CONSTRAINT tool_calls_revalidated_check
        CHECK (revalidated_at IS NULL OR execution_id IS NOT NULL),

    -- The claim is held while the attempt is unsettled and released at
    -- settle; a settled row nobody drives, an unsettled boundary row somebody
    -- must.
    ADD CONSTRAINT tool_calls_claim_check
        CHECK ((claimed_by IS NOT NULL) = (state <> 'settled' AND execution_id IS NOT NULL)),

    -- The reason code per outcome: REQUIRED for denied, stale and unknown;
    -- OPTIONAL for failed, which keeps error_message as the human text;
    -- FORBIDDEN for succeeded and blocked (blocked carries the requirement
    -- set) and while unsettled. A CASE over the outcome reaches ELSE on NULL,
    -- so an unsettled row is governed by the FORBIDDEN branch and not by a
    -- NULL comparison that passes.
    ADD CONSTRAINT tool_calls_reason_code_check
        CHECK (CASE outcome
                   WHEN 'denied'  THEN reason_code IS NOT NULL
                   WHEN 'stale'   THEN reason_code IS NOT NULL
                   WHEN 'unknown' THEN reason_code IS NOT NULL
                   WHEN 'failed'  THEN true
                   ELSE reason_code IS NULL
               END),
    ADD CONSTRAINT tool_calls_reason_code_format_check
        CHECK (reason_code IS NULL OR reason_code ~ '^[a-z][a-z0-9_]*(/[a-z][a-z0-9_]*)+$'),

    -- The decision: the first three all or none; a closed vocabulary of two.
    ADD CONSTRAINT tool_calls_operator_decision_shape_check
        CHECK (num_nonnulls(operator_decision, operator_decided_by, operator_decided_at) IN (0, 3)),
    ADD CONSTRAINT tool_calls_operator_decision_check
        CHECK (operator_decision IS NULL OR operator_decision IN ('approve_once', 'deny_once')),
    ADD CONSTRAINT tool_calls_operator_decided_by_fkey
        FOREIGN KEY (operator_decided_by, organization_id)
        REFERENCES users (user_id, organization_id) ON DELETE RESTRICT,
    -- Consumption is all-or-none, written in one statement; only a decision
    -- can be consumed, and only an approval.
    ADD CONSTRAINT tool_calls_consumption_shape_check
        CHECK (num_nonnulls(operator_decision_consumed_at, operator_decision_consumed_by) IN (0, 2)),
    ADD CONSTRAINT tool_calls_consumption_requires_approval_check
        CHECK (operator_decision_consumed_at IS NULL OR operator_decision IS NOT DISTINCT FROM 'approve_once'),
    -- The consumer is an attempt: this one, or the inheriting re-request (D5).
    ADD CONSTRAINT tool_calls_consumed_by_fkey
        FOREIGN KEY (operator_decision_consumed_by, organization_id)
        REFERENCES tool_calls (tool_call_id, organization_id) ON DELETE RESTRICT,
    -- A decision is recorded on a row that entered the operator wait, which
    -- is a row that carries a requirement set: a decision with nothing to
    -- decide is unrepresentable.
    ADD CONSTRAINT tool_calls_decision_requires_requirement_check
        CHECK (operator_decision IS NULL OR requirement_set IS NOT NULL),

    -- The disposition: present exactly on SETTLED execution-bound rows,
    -- scoped to the boundary's rows because the importer settles its rows
    -- through CompleteToolCall with no disposition (review round 9).
    ADD CONSTRAINT tool_calls_drain_disposition_check
        CHECK (drain_disposition IS NULL OR drain_disposition IN
               ('stopped_before_commit', 'committed', 'in_fenced_domain', 'unresolved')),
    ADD CONSTRAINT tool_calls_drain_disposition_presence_check
        CHECK ((drain_disposition IS NOT NULL) = (state = 'settled' AND execution_id IS NOT NULL)),

    -- Consumed by executions.blocked_tool_call_id: the referenced attempt is
    -- bound to the execution AND the tenant in one key, so a terminal result
    -- cannot name another execution's attempt.
    ADD CONSTRAINT tool_calls_id_execution_org_key UNIQUE (tool_call_id, execution_id, organization_id);

-- One live attempt per mutated resource, held through unresolved drainage:
-- at most one attempt with a given mutation_key that is unsettled OR settled
-- with its mutation not yet known to have landed or not (D12; ADR 0027 keys
-- serialization by the resource, not the writer). On mutation_key ALONE, so
-- two families touching one resource are serialized as one family's two
-- attempts are. NULLs are distinct under a unique index, so non-mutating
-- attempts never collide.
CREATE UNIQUE INDEX tool_calls_live_mutation_key
    ON tool_calls (mutation_key)
    WHERE state <> 'settled' OR drain_disposition = 'unresolved';

-- The rows recovery enumerates (D5's ListAttemptsForRecovery): unsettled
-- boundary attempts, and settled ones whose drainage is unresolved.
CREATE INDEX tool_calls_recovery_idx
    ON tool_calls (organization_id, execution_id)
    WHERE execution_id IS NOT NULL AND (state <> 'settled' OR drain_disposition = 'unresolved');

-- The Story-scoped guard (D4): is any attempt of this Story operator_waiting?
CREATE INDEX tool_calls_story_wait_idx
    ON tool_calls (organization_id, story_id)
    WHERE state = 'operator_waiting';

-- ---------------------------------------------------------------------------
-- Step 8: tool_calls -- the disposition moves only from unresolved (D11).
--
-- The outcome is settled once; the disposition may move LATER, in one
-- direction, when a later reconciliation obtains attempt-specific evidence.
-- A settled disposition is evidence, and evidence does not change its mind.
-- ---------------------------------------------------------------------------
CREATE FUNCTION tool_calls_refuse_disposition_rewrite() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.drain_disposition IS NOT NULL
       AND OLD.drain_disposition <> 'unresolved'
       AND NEW.drain_disposition IS DISTINCT FROM OLD.drain_disposition THEN
        RAISE EXCEPTION 'attempt % drain disposition is %, which is evidence and does not move; only '
            'an unresolved disposition may be resolved', OLD.tool_call_id, OLD.drain_disposition
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER tool_calls_disposition_monotonic
    BEFORE UPDATE ON tool_calls
    FOR EACH ROW EXECUTE FUNCTION tool_calls_refuse_disposition_rewrite();

-- ---------------------------------------------------------------------------
-- Step 9: executions.blocked_tool_call_id -- the reference and its meaning.
--
-- The composite key binds the attempt to this execution and tenant; the
-- trigger binds it to its MEANING: the referenced row is a settled, blocked
-- attempt, so a blocked execution always references "the pending action and
-- the structured requirement set" (ADR 0032 section 5). The reference is
-- written at most once (step 6), so BEFORE INSERT OR UPDATE covers every
-- path that sets it, and the referenced attempt is settled -- an outcome is
-- immutable -- so the meaning cannot later be lost.
-- ---------------------------------------------------------------------------
ALTER TABLE executions
    ADD CONSTRAINT executions_blocked_tool_call_fkey
        FOREIGN KEY (blocked_tool_call_id, execution_id, organization_id)
        REFERENCES tool_calls (tool_call_id, execution_id, organization_id) ON DELETE RESTRICT;

CREATE FUNCTION executions_require_blocked_attempt() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    attempt_outcome text;
BEGIN
    IF NEW.blocked_tool_call_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT outcome INTO attempt_outcome FROM tool_calls
     WHERE tool_call_id = NEW.blocked_tool_call_id;
    IF attempt_outcome IS DISTINCT FROM 'blocked' THEN
        RAISE EXCEPTION 'execution % names attempt % as its blocking action, but that attempt is %, not '
            'a settled blocked attempt', NEW.execution_id, NEW.blocked_tool_call_id,
            coalesce(attempt_outcome, 'unsettled')
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER executions_blocked_attempt_is_blocked
    BEFORE INSERT OR UPDATE ON executions
    FOR EACH ROW EXECUTE FUNCTION executions_require_blocked_attempt();

-- ---------------------------------------------------------------------------
-- Step 10: principal_instances.execution_id (D4 check 1; ADR 0032 item 2).
--
-- The live-principal insert already derives its lineage FROM an execution
-- and stored no reference to it, so a principal of a prior execution of the
-- same Story was indistinguishable from this one's. Required exactly for a
-- LIVE agent -- origin 'resolved', item 4 design D5: "a live agent exists
-- only under an execution" -- and forbidden for humans, system principals
-- and foreign imports, which live outside any execution. coalesce, so a NULL
-- origin cannot turn the equivalence into a passing NULL (000023's own note).
-- ---------------------------------------------------------------------------
ALTER TABLE principal_instances
    ADD COLUMN execution_id uuid,

    ADD CONSTRAINT principal_instances_execution_fkey
        FOREIGN KEY (execution_id, organization_id)
        REFERENCES executions (execution_id, organization_id) ON DELETE RESTRICT,

    ADD CONSTRAINT principal_instances_live_agent_execution_check
        CHECK ((execution_id IS NOT NULL)
               = (kind = 'agent' AND coalesce(prompt_pack_origin, '') = 'resolved'));

CREATE INDEX principal_instances_execution_idx ON principal_instances (execution_id);

COMMIT;
