-- Binary attachments and retention pins.
--
-- Binaries NEVER live in relational rows (ADR 0022): this table holds
-- content-addressed digest references into object storage. The cross-store
-- commit-order invariant is the seam's -- object first, pin recorded, row
-- last -- and these tables are the "pin recorded" and "row last" halves.
BEGIN;

CREATE TABLE binary_attachments (
    attachment_id   uuid        PRIMARY KEY,
    organization_id uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,

    -- The digest IS the object's address. Same 64-hex form as every other
    -- digest here, so one CHECK shape covers the whole schema.
    object_digest   text        NOT NULL,
    media_type      text        NOT NULL,
    size_bytes      bigint      NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT binary_attachments_digest_check
        CHECK (object_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT binary_attachments_size_check
        CHECK (size_bytes >= 0)
);

CREATE INDEX binary_attachments_digest_idx ON binary_attachments (object_digest);

-- Retention pins: while an evidence package is authoritative, every Audit
-- artifact and binary attachment it references is pinned, and retention or
-- compaction may prune only UNPINNED records (ADR 0021).
--
-- The pin names what is pinned and what pins it. Both halves matter: the
-- pinned reference is what retention consults, and the pinning artifact is
-- what makes a pin releasable when its evidence package stops being
-- authoritative.
CREATE TABLE retention_pins (
    retention_pin_id  uuid        PRIMARY KEY,
    organization_id   uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,

    -- What holds the pin: a Management artifact, typically an evidence
    -- package.
    pinned_by_artifact_id uuid    NOT NULL REFERENCES management_artifacts (artifact_id) ON DELETE RESTRICT,

    -- What is pinned. Exactly one, by the same exclusive-arc reasoning the
    -- artifact scope uses: a polymorphic target column could not be a
    -- foreign key, and a pin pointing at a pruned row is precisely the
    -- failure pins exist to prevent.
    pinned_audit_artifact_id uuid REFERENCES audit_artifacts     (artifact_id)   ON DELETE RESTRICT,
    pinned_attachment_id     uuid REFERENCES binary_attachments  (attachment_id) ON DELETE RESTRICT,

    -- Digest binding: ADR 0021 requires that even a retention bug be
    -- DETECTABLE -- a dangling or altered reference must fail verification
    -- rather than silently weakening the proof.
    pinned_digest     text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT retention_pins_one_target_check
        CHECK (num_nonnulls(pinned_audit_artifact_id, pinned_attachment_id) = 1),
    CONSTRAINT retention_pins_digest_check
        CHECK (pinned_digest ~ '^[0-9a-f]{64}$')
);

CREATE INDEX retention_pins_by_artifact_idx  ON retention_pins (pinned_by_artifact_id);
CREATE INDEX retention_pins_audit_target_idx ON retention_pins (pinned_audit_artifact_id);
CREATE INDEX retention_pins_attachment_idx   ON retention_pins (pinned_attachment_id);

COMMIT;
