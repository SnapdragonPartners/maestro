+++
title = "Maestro v2 Operations Runbook"
edit_date = "2026-08-17"
status = "draft"
summary = "Operational sequences and measured gotchas for running the v2 data plane locally and in the cloud: provider defaults that cost money or silently retain data, commands whose failure modes mislead, and the project-safety rules for cloud work. Command definitions live in the Makefile; design rationale lives in the ADRs."
type = "process"
+++

# Maestro v2 Operations Runbook

Status: draft — started 2026-08-17 during #286 (cloud portability). Not yet
reviewed, so nothing here has the authority of an accepted decision. It records
things that were **measured against real services** and that cost time or money
to discover.

## What belongs here

Operational knowledge with a short half-life in someone's head and a long
half-life in production: provider defaults, command failure modes, the order
operations must happen in.

What does **not** belong here:

- **Command definitions.** The `Makefile` is the source of truth; CLAUDE.md
  routes to it. Duplicating targets here creates a second, drifting copy.
- **Design rationale.** That is the ADRs' job. This file says what to do and
  what bites; it does not re-argue why.
- **Secrets, connection strings, or instance IPs.** Names and connection
  identifiers only.

Each entry says whether it was **measured** or **read in documentation**,
because this repository has repeatedly paid for the difference.

## Current cloud resources

Non-secret coordinates, so nobody has to rediscover them. Credentials never
appear here or anywhere else in the repository.

| Resource | Identifier |
| --- | --- |
| Project | `gen-lang-client-0110204648` |
| Cloud SQL instance | `gen-lang-client-0110204648:us-central1:maestro-286` |
| Object bucket (adapter measurement) | `gs://maestro-objects-286` |

The Cloud SQL root password is generated per session into a `0600` file outside
the repository. Regenerate it rather than reusing one; nothing depends on its
value persisting.

## Project safety for cloud work

**Rule: every `gcloud` invocation carries `CLOUDSDK_CORE_PROJECT=<project>`
inline *and* `--project=<project>`.**

The environment variable wins over the active configuration (measured), so a
forgotten `--project` flag cannot fall through to whatever project happens to
be selected. This is not paranoia about typos — a developer's active project is
frequently unrelated to Maestro, and the failure mode is a billable or
destructive operation against someone else's environment, which no test can
catch and no lint can see.

Do not "fix" the situation by changing the developer's active project or ADC
quota project. Those belong to them and may be load-bearing for other work. Use
explicit scoping instead.

Related: an ADC quota project pointing at an unrelated project does **not**
break GCS bucket access (measured), so it is not worth changing for that
reason alone.

## Google Cloud Storage

### Soft delete is on by default and silently defeats deletion

**Measured.** Every new bucket is created with
`softDeletePolicy.retentionDurationSeconds = 604800` (7 days) whether or not
anybody asked. Under it, a generation-specific delete returns **204** and the
object leaves the versioned listing, while the bytes are retained and **billed**
until their `hardDeleteTime`.

The consequence for Maestro: `objects.Store.DeleteVersion` reclaims **nothing**
on a default bucket. The object sweep issues its deletes, records the storage as
reclaimed, and the bill does not move for a week.

**Provisioning obligations, in order:**

1. **Disable soft delete before the first object write**
   (`softDeletePolicy.retentionDurationSeconds = 0`).
2. **Positively verify the effective policy** and refuse to proceed if it is
   non-zero — the same discipline `EnsureBucket` already applies to versioning,
   for the same reason: an operator, a restored config or a console click can
   turn it back on.
3. **Do not treat a configured policy as an effective one.** Google advises
   waiting **at least** 30 seconds after disabling and gives **no upper bound**.
   Maestro's stabilization policy is 60 seconds against the bucket's `updated`
   timestamp. Waiting it out is a conservative choice above a stated minimum —
   **it is not proof the change has taken effect.**
4. **Treat already soft-deleted objects as unreclaimable residue** until their
   original hard-delete time.

**Order matters more than it looks.** Disabling the policy does not release
existing residue and makes it *unobservable*: a direct read of a known
soft-deleted generation then answers
`400 Soft delete policy is required to request a soft-deleted version` — a
refusal to answer, not a 404 (measured). So a bucket that already accumulated
residue cannot be audited after the policy is switched off.

### Interrupted uploads cannot be enumerated

**Measured.** A resumable upload session that has accepted data and never been
finalized is invisible to every listing surface: live, versioned, and
soft-deleted all report nothing. There is no API that enumerates sessions and
none that aborts one by name; they expire on Google's schedule.

This is why the object seam declares a capability
(`IncompleteWritesProviderReclaimed`) instead of letting an adapter return an
empty slice. An empty result from a reclaimer means "none present", which is a
claim about the bucket; the truth here is "cannot see".

### A bucket is never "empty"

It can only be reported as free of **listable generations**. It may still hold
unenumerable resumable sessions and soft-deleted residue that outlives its own
queryability. Do not write "empty" in a report.

## Cloud SQL

### Creating an instance

```bash
CLOUDSDK_CORE_PROJECT=$P gcloud sql instances create <name> \
  --project=$P \
  --database-version=POSTGRES_18 \
  --edition=ENTERPRISE \
  --tier=db-g1-small \
  --region=us-central1 \
  --storage-size=10GB --storage-type=SSD \
  --availability-type=zonal \
  --deletion-protection \
  --root-password="$(cat <path-outside-the-repo>)" \
  --quiet
```

**`--edition=ENTERPRISE` is required, and omitting it costs money.** PostgreSQL
18 now defaults to **ENTERPRISE_PLUS**, which rejects `db-g1-small` with
`Invalid Tier (db-g1-small) for (ENTERPRISE_PLUS) Edition` and whose own tiers
cost substantially more. The rejection is a hard error rather than a silent
upgrade, which is the only reason this was caught rather than billed.

**`--no-authorized-networks` is not a flag.** Omit it: no authorized networks
is the default, and that is the state you want.

### Creation outruns short command timeouts, and killing the client changes nothing

**Measured: ~7.5 minutes.** A tool or shell timeout that kills `gcloud` does
**not** cancel the server-side operation — the instance continues to
`PENDING_CREATE` and then `RUNNABLE`.

**Do not re-issue `create`.** Check what is actually happening:

```bash
gcloud sql instances list --project=$P
gcloud sql operations list --instance=<name> --project=$P
gcloud sql operations wait <operation-id> --project=$P
```

### Connectivity posture

Reach the instance through the **Cloud SQL Auth Proxy**, not a public
connection:

```bash
cloud-sql-proxy --port 5433 --quota-project $P <project>:<region>:<instance>
```

The instance keeps a public IP but **no authorized networks**, so direct
connections are refused; the proxy authenticates through the Admin API and
always encrypts. Also set `--ssl-mode=ENCRYPTED_ONLY`: it is redundant while
the authorized-network list is empty, and it is what stops a later addition to
that list from being unencrypted.

`psql` and `pg_isready` are at `/opt/homebrew/opt/libpq/bin` on macOS and are
usually not on `PATH`.

### Versions and extensions

**Measured on a live instance, not read from documentation:**

- `POSTGRES_18` reports **PostgreSQL 18.4**, identical to the pinned local
  image, so a cloud-versus-local comparison has no version skew to explain.
- **pgvector 0.8.1** is available and `CREATE EXTENSION vector` succeeds on
  PG 18. Documentation states 0.8.0 for "13 and later"; the instance is a patch
  ahead. This matters for the later knowledge work: choosing PG 18 to match
  local does **not** cost pgvector.

Verify an extension by creating it on the instance. Availability is per-instance
and the documentation generalises across majors.

### Cost posture between runs

**Stop rather than delete.** The acceptance criteria require a re-runnable
workflow and later migrations need re-proving, so a deleted instance costs more
to re-establish than a stopped one costs to keep. Keep deletion protection on.

## Local data plane

Commands are in the `Makefile` (`dataplane-up`, `dataplane-migrate`,
`dataplane-down`, `dataplane-reset FORCE=1`). Two operational notes that are not
visible from the target names:

- **`dataplane-up` is both the first-run and the everyday command** and is
  idempotent. There is no separate bootstrap sequence to remember.
- **A schema change that folds into an existing migration requires
  `dataplane-reset FORCE=1` before `dataplane-up`** on planes created before the
  fold. The plane is not corrupt; its recorded migration state simply predates
  the edit.

## Test-suite gates that need credentials

Suites that need external credentials sit behind their **own build tag**, never
`integration`, because the pre-push gate runs `make test-integration` and a
credential requirement there would either block anyone without them or skip
silently and look green.

`make test-gcs` refuses to run without `MAESTRO_GCS_TEST_BUCKET` rather than
reporting a green skip. A suite that skips on missing configuration and reports
success is worse than one that fails.
