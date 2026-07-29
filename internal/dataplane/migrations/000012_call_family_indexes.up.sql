-- Indexes for the call family's retained reads (item 5 design, D8).
--
-- One index per retained read shape, chosen against the PREDICATE rather
-- than the projection. Two reads that differ in what they filter on need
-- two indexes even when they return the same columns: a column between the
-- filter and the sort key interrupts the prefix and the ordering cannot be
-- used.
--
-- Every index is organization-leading, because every query is, and every
-- keyset index ends in the primary key, because the cursor is
-- (timestamp, id) and an index without the tie-breaker cannot serve it.
--
-- Shapes with no consumer are deliberately absent: by-type lists on
-- tool_name, metric_name and event_type, and by-Story and by-principal on
-- the event tables. Each returns with the item that first needs it,
-- bringing its own index.
BEGIN;

-- Calls by Story: item 9's import verification.
CREATE INDEX llm_calls_org_story_time_idx
    ON llm_calls (organization_id, story_id, started_at, llm_call_id);
CREATE INDEX tool_calls_org_story_time_idx
    ON tool_calls (organization_id, story_id, started_at, tool_call_id);

-- Calls by principal: ADR 0021's MPH analysis.
CREATE INDEX llm_calls_org_principal_time_idx
    ON llm_calls (organization_id, principal_instance_id, started_at, llm_call_id);
CREATE INDEX tool_calls_org_principal_time_idx
    ON tool_calls (organization_id, principal_instance_id, started_at, tool_call_id);

-- Calls in a time window. NOT served by the cohort index below: that one
-- puts provider and model between the organization and the timestamp, so a
-- predicate naming only the organization cannot use its ordering.
CREATE INDEX llm_calls_org_time_idx
    ON llm_calls (organization_id, started_at, llm_call_id);
CREATE INDEX tool_calls_org_time_idx
    ON tool_calls (organization_id, started_at, tool_call_id);

-- Cost aggregates by cohort within a window: Phase 1B's comparison.
CREATE INDEX llm_calls_org_cohort_time_idx
    ON llm_calls (organization_id, provider, model, started_at);

-- Events in a time window: operations, and truncation's cutoff.
CREATE INDEX metric_events_org_time_idx
    ON metric_events (organization_id, recorded_at, metric_event_id);
CREATE INDEX audit_events_org_time_idx
    ON audit_events (organization_id, occurred_at, audit_event_id);

-- Truncation of COMPLETED calls. The cutoff is finished_at, not started_at:
-- ageing from the start deletes a long-running call the instant it
-- finishes, and the slow calls are the ones worth keeping.
CREATE INDEX llm_calls_org_finished_idx
    ON llm_calls (organization_id, finished_at, llm_call_id);
CREATE INDEX tool_calls_org_finished_idx
    ON tool_calls (organization_id, finished_at, tool_call_id);

-- Old OPEN calls, which truncation counts but never deletes. Partial,
-- because the open set is a vanishing fraction of the largest tables in
-- the system and a full index would be paid for on every write to serve a
-- query about the few.
CREATE INDEX llm_calls_org_open_started_idx
    ON llm_calls (organization_id, started_at)
    WHERE finished_at IS NULL;
CREATE INDEX tool_calls_org_open_started_idx
    ON tool_calls (organization_id, started_at)
    WHERE finished_at IS NULL;

-- Truncation of Audit artifacts, the only pinnable family in scope; binary
-- attachments belong to item 6 with the object module.
CREATE INDEX audit_artifacts_org_created_idx
    ON audit_artifacts (organization_id, created_at, artifact_id);

-- llm_calls (model) from item 3 is deliberately RETAINED. It is not a
-- prefix of the cohort index -- model sits behind provider there -- so the
-- composite cannot serve a model-only lookup, and dropping it is a
-- separate decision needing its own evidence.

COMMIT;
