+++
title = "Spike: SeaweedFS As The Local Object Provider"
edit_date = "2026-09-24"
status = "draft"
type = "spike"
summary = "The narrow spike behind replacing MinIO (#350): SeaweedFS, ADR 0022's named fallback, started the way the plane starts its object provider and measured against the unchanged objects adapter suite and the five process-model constraints the cold backup and key recovery depend on. 42 of 45 adapter tests pass on a post-4.47 build; the three that fail are pins of MinIO's own divergences from S3, which SeaweedFS does not share. All five constraints hold. One real gap found and already fixed upstream: release 4.47 omits the Initiated date from a multipart-upload listing, which the sweep's grace period reads, so the pin must be a build after seaweedfs#11313. Cold start is ~18s against MinIO's ~1s, which the readiness budgets must absorb."
+++

# Spike: SeaweedFS As The Local Object Provider

Status: **draft** — the measurement behind the provider swap on
`v2/fix/local-object-provider`, tracked in
[#350](https://github.com/SnapdragonPartners/maestro/issues/350). Scripts under
`spikes/phase_3/objectprovider/` (unmaintained, per `CLAUDE.md`).

## Why now

MinIO deleted its community images from Docker Hub around 2026-09-11 and, by
2026-09-24, from quay.io as well; no public registry serves the pinned index
(`sha256:14cea493…`). Every cold-runner CI job that brings the plane up fails,
on `main` and on every PR, and the last community release is unpatched
against five advisories (#350). The stopgap is gone, so the durable answer —
a provider swap behind `objects.Store` — is the unblock.

## What was measured, and against what

An inventory of every reliance on MinIO (recorded on #350) showed the client
surface is small and generic — versioned bucket, put with SHA-256 trailer,
stat/copy/compose by version, list versions with delete markers, delete by
version, list/abort multipart — and that the cold backup never reads the
provider's files as objects. What a replacement must satisfy beyond that is
five process-model constraints. SeaweedFS, [ADR 0022](../../adr/0022-v2-data-plane.md)'s
*named fallback*, was started as the plane starts MinIO (`weed server -dir=/data -s3`,
one bind mount, the invoking uid, credentials by environment, one loopback
port) and:

1. **The unchanged `internal/dataplane/objects` integration suite** was run
   against it by pointing `MAESTRO_MINIO_PORT` at the spike container, with
   the plane's own credential derivation.
2. **The five constraints** were exercised by hand: arbitrary uid, first-start
   files, credential swap over an existing data directory, cold copy of a
   stopped tree, and a host-side liveness probe.

## Results

**Adapter suite: 41/45 on 4.47, 42/45 on a post-4.47 build.**

| Failing test | Cause | Disposition |
| --- | --- | --- |
| `TestListUploadsUnderFindsWhatTheServerPrefixCannot` | SeaweedFS treats the `ListMultipartUploads` prefix as a prefix, as S3 does; MinIO treated it as an exact key (object-module design D1a) | Pin of a MinIO divergence; rewrite as the S3 behaviour. `ListUploadsForKey` must filter by exact key client-side, since the design's abort fence depends on the exact-key answer |
| `TestTheServerNeverTruncatesTheUploadListing` | SeaweedFS honours `max-uploads` and sets `IsTruncated`; MinIO ignored it | Pin of a MinIO divergence; the adapter's two-marker paging is now exercised by a real server |
| `TestPutStagedRejectsCorruptionInTransit` | Both corruptions are still **refused**; the codes are `InternalError` (chunk signature) and `InvalidDigest` (checksum) instead of `SignatureDoesNotMatch` / `XAmzContentChecksumMismatch` | The property holds; the code assertions record MinIO. Assert refusal and absence, and record both servers' codes |
| `TestBothStorageStatesCarryTheirOwnDate` (4.47 only) | **Release 4.47 omits `<Initiated>` from the listing.** A zero date is year 1, so the sweep's `tooFresh` judges the upload *not* young and would condemn a live in-progress upload — the race the grace period exists for. Fixed upstream in [seaweedfs#11313](https://github.com/seaweedfs/seaweedfs/pull/11313) (2026-09-14, the day 4.47 was cut); verified on the `dev` image, where uploads created under 4.47 report their dates too — the time was stored, only the listing omitted it | **The pin must be a build after #11313** — release 4.48 when it lands, a digest-pinned `dev` build until then. The adapter also gains a fail-safe: an upload with no date is judged young, never old |

**Process-model constraints: all five hold.**

| Constraint | Measured |
| --- | --- |
| Runs as an arbitrary host uid over a bind-mounted 0700 directory | `501:20`; ~100 files written on first start (volume `.dat/.idx/.vif`, `filerldb2/`, `m9333/`, `vol_dir.uuid`) — the freshness check has evidence |
| All state in the one directory | Master state, filer metadata (LevelDB) and volumes are all under `/data`; only Unix sockets live in `/tmp` |
| Credentials by environment, swappable over existing data with no re-provisioning | Sentinel written under pair A; container restarted with pair B over the same directory: B reads it, A gets `403 Forbidden`. Identical to item 7's MinIO measurement, so `recover-key` still needs no object-store step |
| A cleanly stopped tree, copied by plain file copy, restarts elsewhere | Second project started from the copy on another port: the sentinel reads back; incomplete uploads and their dates are present |
| Host-side liveness probe | `GET /healthz` on the S3 port: `200`, unauthenticated, plain HTTP; S3 served within ~1s of it. Replaces `/minio/health/live` |

**Two facts the swap must absorb.** Cold start to `/healthz` is **~18s** (MinIO:
~1s); readiness waits in `up` and in backup's restart must budget for it. And
the credential fallback is documented by the binary itself — *"Environment
variables are only used when no S3 configuration file is provided and no
configuration is available from the filer"* — so the plane must never write an
identities file or filer-side S3 configuration, or the environment stops
governing.

**Not measured, deliberately.** Performance; multi-node; anything the plane does
not use. Garage was excluded on paper (no bucket versioning); versitygw and
RustFS were not run, since the ADR-named fallback met every constraint.

## Decision

Replace MinIO with SeaweedFS behind the existing seam, pinned by multi-arch
index digest per ADR 0026 to a build carrying #11313. Rename the provider
surface (compose service, data directory, environment) rather than keep a
`minio` label on a SeaweedFS container. Archives from the MinIO era are not
restorable into the new plane regardless of naming — the bytes are MinIO's —
and existing local planes reset. Recorded as an amendment to ADR 0022 in the
same branch.
