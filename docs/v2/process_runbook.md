+++
title = "Maestro v2 Operations Runbook"
edit_date = "2026-09-03"
status = "live"
summary = "Operational sequences and measured gotchas for running the v2 data plane locally and in the cloud: provider defaults that cost money or silently retain data, commands whose failure modes mislead, and the project-safety rules for cloud work. Command definitions live in the Makefile; design rationale lives in the ADRs."
type = "process"
+++

# Maestro v2 Operations Runbook

Status: live — accepted 2026-08-17 by Codex and DR, written during #286 (cloud
portability). It records operational facts that cost time or money to learn,
each tagged with where it came from.

Acceptance is of the document, not a blanket warrant for every sentence in it.
The tags below still carry the evidence: **Measured** claims were observed against
a real service on a date, **Documented** ones were read from the provider and not
checked here, and **Policy** is a choice Maestro made. An untagged claim about
provider behaviour has not been verified, and being live does not make it so.
Provider behaviour also changes — a measurement is a fact about a date.

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

### How claims here are marked

**Material claims about provider behaviour** carry one of three tags, because
this repository has repeatedly paid for the difference between observing
something and being told it. Ordinary prose — what a section is for, why an
ordering matters — is not tagged and does not need to be:

- **Measured** — observed against a real service, with the date. It was true
  then, of that service, and provider behaviour changes.
- **Documented** — taken from primary provider documentation, with a link. Not
  observed here, so it may generalise less than it appears to.
- **Policy** — a choice Maestro makes. No provider guarantees it and no
  measurement supports it; it is a decision, and it can be revisited.

An untagged claim **about what a provider does** has not been checked and should
not be relied on. The rule is scoped to those deliberately: applying it to every
sentence would make the file condemn its own explanatory text, and a rule that
is visibly not followed stops being read as a rule.

## Current cloud resources

Non-secret coordinates, so nobody has to rediscover them. Credentials never
appear here or anywhere else in the repository.

| Resource | Identifier |
| --- | --- |
| Project | `gen-lang-client-0110204648` |
| Cloud SQL instance | `gen-lang-client-0110204648:us-central1:maestro-286` |
| Object bucket (adapter measurement) | `gs://maestro-objects-286` |

**The instance is left stopped between sessions (Policy)**, so start it before
`make test-cloud` and stop it afterwards — see the activation-policy section
below for both commands and for how to confirm which state it is in. Stopped is
cheaper, not free.

### CI identity for the dispatchable proof

The `Cloud data-plane proof (#286)` workflow authenticates by **Workload Identity
Federation** and holds no key. Coordinates, so nobody has to rediscover them:

| Thing | Value |
| --- | --- |
| Service account | `maestro-cloud-proof@gen-lang-client-0110204648.iam.gserviceaccount.com` |
| WIF pool / provider | `github` / `github-actions` (pre-existing, shared) |
| Custom roles | `maestroCloudProofSql` (instance get/update, users update/list), `maestroCloudProofBucket` (bucket get/update), `maestroCloudProofQuota` (`serviceusage.services.use`) |
| CI database user | `maestro_ci` (**not** `postgres`) |
| GitHub environment | `cloud-proof` — reviewer `@SnapdragonPartners/snapdragon`, `main` only, admin bypass disabled |

**Both members of `@SnapdragonPartners/snapdragon` are authorized spend approvers
(Policy, DR 2026-08-17).** #286 requires DR approval for each run; a team
reviewer is deliberately broader than a named individual, and that is recorded
here rather than left to be inferred from the environment's configuration.
Narrowing it to a single reviewer is a one-line change if the team grows.

**Administrator bypass is disabled.** It defaults to enabled, which would let a
repository admin dispatch a paid run without review — the approval requirement
would still appear in the settings while not applying to the people most likely
to trigger it.

**The environment name is a security control, not a label.** The service
account's impersonation binding names the exact OIDC subject
`repo:SnapdragonPartners/maestro:environment:cloud-proof`, so a job that omits
`environment: cloud-proof` receives a token GCP refuses. Binding to the
*repository* instead would let any workflow here assume the identity while
skipping the reviewer — a spend gate that reads as enforced in YAML and is not.
Renaming the environment breaks authentication until the binding is updated to
match, which is the intended failure direction.

The shared provider's condition is `assertion.repository_owner ==
'SnapdragonPartners'`, i.e. **org-wide**. Per-repository restriction therefore
lives in the service-account binding, never in the provider. Do not read the
provider and conclude the identity is repo-scoped.

**`maestro_ci` exists so CI never rotates the operator's password.** The workflow
generates and masks a password for it on every run and leaves it unknowable
afterwards. Rotating `postgres` instead would invalidate the local credential
this runbook's own procedures depend on, breaking somebody's laptop on every CI
run. **Measured 2026-08-17:** `maestro_ci` has `createdb=true`, `super=false`,
and the full cloud suite passes as that user.

**`maestroCloudProofQuota` is not spare.** It holds one permission,
`serviceusage.services.use`, which Google requires of any identity naming a quota
project — and the workflow sets `GOOGLE_CLOUD_QUOTA_PROJECT` because ADC clients
are not scoped by `gcloud`
([Documented](https://docs.cloud.google.com/docs/quotas/set-quota-project)).
Neither Cloud SQL Client nor the other two custom roles provides it. A
single-permission role attached to nothing obvious looks like residue during IAM
reconciliation; removing it breaks the Go client's requests while leaving every
`gcloud` step in the job working, which is a confusing way to fail.

It was also missed by the first round of verification, for a reason worth
keeping: those checks impersonated the service account through `gcloud`, and
`gcloud` does not consume the quota project the way the client libraries do. The
suite passed because the Go client was using an operator's own credentials at the
time. Verifying an identity through a different client than the one that will use
it proves less than it appears to.

**IAM changes take time to propagate.** Fresh bindings returned
`IAM_PERMISSION_DENIED` for roughly 30 seconds after being created and correct
(**measured 2026-08-17**). A denial immediately after granting is not evidence
the grant is wrong; re-read the policy, then retry before changing anything.

### The root password is persisted server-side, so rotation is two steps

The Cloud SQL root password lives on the **instance**, not in the local file.
The local `0600` file outside the repository is only a copy for connecting.

**Regenerating the file alone breaks the next connection.** Rotation is one
fail-fast sequence, not three commands that happen to be adjacent:

```bash
P=<project>; PWFILE=<path-outside-the-repo>
( umask 077 && python3 -c "import secrets,string; print(''.join(secrets.choice(string.ascii_letters+string.digits+'-_.~') for _ in range(40)))" > "$PWFILE" ) \
  && test -s "$PWFILE" \
  && CLOUDSDK_CORE_PROJECT=$P GOOGLE_CLOUD_QUOTA_PROJECT=$P gcloud sql users set-password postgres \
       --instance=<name> --project=$P --password="$(cat "$PWFILE")" --quiet
```

Three details are load-bearing:

- **`umask 077` around the redirection**, not a `chmod` afterwards. Redirection
  creates the file under the ambient umask, so a later `chmod` leaves a window
  in which the secret was world-readable. There is no way to narrow that window
  after the fact; the file has to be created private.
- **`test -s`**, because an empty file is the failure mode that otherwise
  propagates: generation failing silently would set the instance password to the
  empty string, locking out the very account being rotated.
- **`&&` throughout.** As independent statements, a failed generation or a
  failed `chmod` does not stop `set-password` from running — which is precisely
  the ordering that turns a small failure into a locked-out instance.

Known limitation: the password appears in the process arguments of the `gcloud`
call and is therefore visible to other local users via `ps`. Acceptable on a
single-operator workstation, and worth replacing with an interactive
`--prompt-for-password` if this ever runs anywhere shared.
([Documented](https://docs.cloud.google.com/sql/docs/postgres/users).)

**Measured 2026-08-17:** this exact sequence was run against the live instance —
the new credential authenticated and the previous one was rejected, so it
rotates rather than no-ops. Its three failure guards were exercised separately:
a failing generator stops the chain before `set-password`, an empty file is
caught by `test -s`, and the redirection under `umask 077` lands the file
`-rw-------` with no `chmod` involved.

**Rotate before clients open, not during active work.** Nothing depends on the
value persisting across sessions, so rotation is cheap — but it is not "always
safe": an open pool reconnecting after the change will fail with the old
credential, and the Auth Proxy does not shield a password change from clients
that cached one.

### The root key is not rotatable once a vault exists

**Policy, and the most expensive mistake available here.** The Cloud SQL
password and the root-of-trust key look alike — both are operator-supplied
secrets — and they behave in opposite ways.

The **password** authenticates a connection. Rotating it is cheap and reversible;
the procedure above is safe to run at any time before clients open.

The **root key** derives the vault's encryption keys. It does not authenticate
anything, it *decrypts*. Generating a new one for a database that already holds
vault data does not lock you out with an error — it produces a plane that starts
fine and cannot decrypt its own secrets, and there is no recovery except the
original key.

So:

- **Disposable databases** — the per-run databases the cloud suite creates and
  drops — may take a freshly generated key every run. They hold no vault data
  worth keeping, which is the entire reason a fresh key is acceptable there.
- **Any persistent cloud database must retain and reuse its ORIGINAL
  operator-provided key.** Storing it is a prerequisite for creating such a
  database, not a follow-up task: a plane provisioned before its key has a home
  is already unrecoverable, it just does not know yet.

The asymmetry is worth stating because the safe habit for one secret is the
destructive habit for the other. "Rotate freely" is right for the password and
wrong for the root key.

## Project safety for cloud work

**Rule (Policy): every cloud invocation is scoped explicitly, and `gcloud` is
not the only thing that needs it.**

```bash
P=<project>
CLOUDSDK_CORE_PROJECT=$P GOOGLE_CLOUD_QUOTA_PROJECT=$P gcloud ... --project=$P
GOOGLE_CLOUD_QUOTA_PROJECT=$P MAESTRO_GCS_TEST_BUCKET=<bucket> make test-gcs
```

`CLOUDSDK_CORE_PROJECT` wins over the active configuration (**measured
2026-08-17**), so a forgotten `--project` cannot fall through to whatever
project happens to be selected. This is not paranoia about typos — a
developer's active project is frequently unrelated to Maestro, and the failure
mode is a billable or destructive operation against someone else's environment,
which no test catches and no linter sees.

**`gcloud` scoping does not reach Application Default Credentials.** The Go
adapter and any ADC client resolve their quota project separately, so they need
`GOOGLE_CLOUD_QUOTA_PROJECT` — the variable the pinned client libraries
actually read (**measured** in the pinned source of
`cloud.google.com/go/auth` and `google.golang.org/api`, 2026-08-17).

An earlier version of this section said an unrelated ADC quota project was "not
worth changing", on the grounds that GCS access still worked. That conflated two
things: access working says nothing about **billing and quota attribution**,
which is what the quota project determines. Requests can succeed while being
attributed to an unrelated project.
([Documented](https://docs.cloud.google.com/docs/quotas/set-quota-project).)

Do not "fix" any of this by changing the developer's active project or their
ADC quota project. Those belong to them and may be load-bearing for other work.
Scope each invocation instead.

## Google Cloud Storage

### Soft delete is on by default and silently defeats deletion

**Measured 2026-08-17.** A new bucket is created with
`softDeletePolicy.retentionDurationSeconds = 604800` (7 days) without being
asked for. That is the **system default**, which applies absent a tag-based
override at the project or organization level
([Documented](https://docs.cloud.google.com/storage/docs/use-tags-for-soft-delete))
— so do not assume the default is what you will get, in either direction.
Read the policy rather than predicting it.

Under such a policy, a generation-specific delete returns **204** and the object
leaves the versioned listing, while the bytes are retained and **billed** until
their `hardDeleteTime`.

The consequence for Maestro: `objects.Store.DeleteVersion` reclaims **nothing**
on a default bucket. The object sweep issues its deletes, records the storage as
reclaimed, and the bill does not move for a week.

**Provisioning obligations, in order:**

1. **Disable soft delete before the first object write**
   (`softDeletePolicy.retentionDurationSeconds = 0`).
2. **Positively verify the CONFIGURED policy** and refuse to proceed if it is
   non-zero — the same discipline `EnsureBucket` already applies to versioning,
   for the same reason: an operator, a restored config or a console click can
   turn it back on. Note the wording: this reads configuration, which is not
   the same as effectiveness — see the next point.
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
refusal to answer, not a 404 (**measured 2026-08-17**). So a bucket that already
accumulated residue cannot be audited after the policy is switched off.

### Interrupted uploads cannot be enumerated

**Measured 2026-08-17.** A resumable upload session that has accepted data and
never been finalized is invisible to every listing surface: live, versioned, and
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

### Deleting a database orphans its objects beyond the sweep's reach

The object sweep decides what is unreferenced by **walking the database**. So
deleting a database does not leave *unreferenced* objects behind — it leaves
objects nothing can classify at all, because the authority that would have
judged them is gone. They are not reclaimable by any later run; they are only
findable by an operator who already knows the prefix.

This bites wherever the two stores have different lifetimes, which is the normal
shape of cloud work: a disposable per-run database against one long-lived
bucket. Objects outlive the database that referenced them, under an organization
prefix no later run can attribute, billed indefinitely.

**Measured 2026-08-17:** one suite run that dropped its database left two
generations of one object stranded in the bucket. Nothing in the data path can
find them — the rows that named them went with the database.

So anything that creates a disposable cloud database owes a matching cleanup in
the **object** store. The cloud suite purges its own prefixes for the same reason
it drops its database, and it purges **two** per organization: the digest key
under `<organization-uuid>/`, and `staging/<organization-uuid>/`, where a staged
object waits between its upload and its promotion. The seam releases staging on
the paths that succeed, so the second prefix matters for runs that die in
between.

That covers staged objects which were *finalized*. An upload interrupted before
finalizing is a different state and is not reclaimable at all — see
"Interrupted uploads cannot be enumerated" above. Purging a prefix cannot reach
what no listing reports.

A run killed before its cleanup leaves residue that has to be removed by hand.
**Both** prefixes, for the reason above — the organization prefix alone misses
finalized staged objects, which is precisely the residue a run that died between
upload and promotion leaves:

```bash
CLOUDSDK_CORE_PROJECT=$P GOOGLE_CLOUD_QUOTA_PROJECT=$P gcloud storage rm \
  --all-versions \
  "gs://<bucket>/<organization-uuid>/**" \
  "gs://<bucket>/staging/<organization-uuid>/**" --project=$P
```

**`gcloud storage rm` fails the whole command when a URL matches nothing**
(**measured 2026-08-17**: `ERROR: (gcloud.storage.rm) The following URLs matched
no objects or files`). So on a plane with no staging residue — the normal case —
drop that second URL or pass `--continue-on-error`. A cleanly finished run leaves
neither prefix populated, and matching nothing is the expected outcome rather
than a problem to work around.

**Measured 2026-08-17:** the single-prefix form of this command removed both
stranded generations of a promoted object and the bucket returned to reporting no
listable generations. `--all-versions` is in it because the bucket is versioned
and the default deletes only the live generation
([Documented](https://docs.cloud.google.com/sdk/gcloud/reference/storage/rm)),
which would leave the noncurrent ones billed and harder to find than before. The
staging prefix was **not** exercised by that measurement: it was empty, because
the run being cleaned up had promoted successfully.

Deletion only reclaims at all because soft delete is off, which is the
provisioning obligation above. Under the provider default these commands return
success and reclaim nothing for a week.

## Cloud SQL

### Creating an instance

```bash
CLOUDSDK_CORE_PROJECT=$P GOOGLE_CLOUD_QUOTA_PROJECT=$P gcloud sql instances create <name> \
  --project=$P \
  --database-version=POSTGRES_18 \
  --edition=ENTERPRISE \
  --tier=db-g1-small \
  --region=us-central1 \
  --storage-size=10GB --storage-type=SSD \
  --availability-type=zonal \
  --ssl-mode=ENCRYPTED_ONLY \
  --deletion-protection \
  --root-password="$(cat <path-outside-the-repo>)" \
  --quiet
```

**`--edition=ENTERPRISE` is required for this tier.** PostgreSQL 18 defaults to
**ENTERPRISE_PLUS** (**measured 2026-08-17**), which rejects `db-g1-small`
outright: `Invalid Tier (db-g1-small) for (ENTERPRISE_PLUS) Edition`.

Be precise about the risk, because the obvious reading is wrong. With **this**
tier, omitting the flag **fails hard and bills nothing** — the instance is never
created. The expensive path is the plausible reaction to that error: switching
to a tier that *is* valid for Enterprise Plus and creating it successfully, at
Enterprise Plus pricing. So the failure to guard against is not the error
message, it is the fix somebody reaches for next.

**`--no-authorized-networks` is not a flag.** Omit it: no authorized networks
is the default, and that is the state you want.

### Creation outruns short command timeouts, and killing the client changes nothing

**Measured 2026-08-17: ~7.5 minutes.** A tool or shell timeout that kills `gcloud` does
**not** cancel the server-side operation — the instance continues to
`PENDING_CREATE` and then `RUNNABLE`.

**Do not re-issue `create`.** Check what is actually happening:

```bash
CLOUDSDK_CORE_PROJECT=$P GOOGLE_CLOUD_QUOTA_PROJECT=$P gcloud sql instances list --project=$P
CLOUDSDK_CORE_PROJECT=$P GOOGLE_CLOUD_QUOTA_PROJECT=$P gcloud sql operations list --instance=<name> --project=$P
CLOUDSDK_CORE_PROJECT=$P GOOGLE_CLOUD_QUOTA_PROJECT=$P gcloud sql operations wait <operation-id> --project=$P
```

### Connectivity posture

Reach the instance through the **Cloud SQL Auth Proxy**, not a public
connection:

```bash
cloud-sql-proxy --port 5433 --quota-project "$P" "${P}:us-central1:<instance>"
# --quota-project is the proxy's equivalent of GOOGLE_CLOUD_QUOTA_PROJECT; it is
# an ADC client, so it needs the scoping too.
```

**Brace that variable.** `"$P:us-central1:..."` is wrong **in zsh**, which is the
default shell on macOS: `:u` is a history-style modifier meaning *uppercase*, so
zsh reads `$P:u` and leaves `s-central1` behind. **Measured 2026-08-17:**

```text
unbraced: GEN-LANG-CLIENT-0110204648s-central1:maestro-286
braced:   gen-lang-client-0110204648:us-central1:maestro-286
```

Quoting alone does not save you — the modifier applies inside double quotes too.
`${P}` does.

The failure this produces is the expensive part. The proxy **starts, listens, and
accepts connections**, then fails every one of them with
`invalid connection name` in its own log, while clients see
`connection reset by peer`. Nothing points at the shell. A proxy that is
listening is not a proxy that is working: when connections reset, read the
proxy's log before suspecting the instance.

The instance keeps a public IP but **no authorized networks**, so direct
connections are refused; the proxy authenticates through the Admin API and
always encrypts.

`--ssl-mode=ENCRYPTED_ONLY` is in the create command above and belongs there
rather than in a follow-up patch. It is redundant while the authorized-network
list is empty, and it is what stops a later addition to that list from being
unencrypted — so it should never be the thing somebody remembers to add
afterwards. The default is `ALLOW_UNENCRYPTED_AND_ENCRYPTED` (**measured
2026-08-17**). To confirm an existing instance, read it back rather than
assuming the patch landed:

```bash
CLOUDSDK_CORE_PROJECT=$P GOOGLE_CLOUD_QUOTA_PROJECT=$P gcloud sql instances describe <name> \
  --project=$P --format="value(settings.ipConfiguration.sslMode)"
```

`psql` and `pg_isready` are at `/opt/homebrew/opt/libpq/bin` on macOS and are
usually not on `PATH`.

### Versions and extensions

**Measured 2026-08-17 on a live instance, not read from documentation:**

- `POSTGRES_18` reports **PostgreSQL 18.4**, identical to the pinned local
  image, so a cloud-versus-local comparison has no version skew to explain.
- **pgvector 0.8.1** is available and `CREATE EXTENSION vector` succeeds on
  PG 18. Documentation states 0.8.0 for "13 and later"; the instance is a patch
  ahead. This matters for the later knowledge work: choosing PG 18 to match
  local does **not** cost pgvector.

Verify an extension by creating it on the instance. Availability is per-instance
and the documentation generalises across majors.

### Stopping between runs, and how to tell that it worked

**Stop rather than delete (Policy).** The acceptance criteria require a
re-runnable workflow and later migrations need re-proving, so a deleted instance
costs more to re-establish than a stopped one costs to keep. Keep deletion
protection on.

Stopping is an **activation policy**, not a state:

```bash
# Stop
CLOUDSDK_CORE_PROJECT=$P GOOGLE_CLOUD_QUOTA_PROJECT=$P gcloud sql instances patch <name> \
  --project=$P --activation-policy=NEVER --quiet
# Start
CLOUDSDK_CORE_PROJECT=$P GOOGLE_CLOUD_QUOTA_PROJECT=$P gcloud sql instances patch <name> \
  --project=$P --activation-policy=ALWAYS --quiet
```

**Read both fields.** **Measured 2026-08-17** on this instance, across one full
transition:

| | `state` | `settings.activationPolicy` |
| --- | --- | --- |
| Running | `RUNNABLE` | `ALWAYS` |
| Stopped | `STOPPED` | `NEVER` |

```bash
CLOUDSDK_CORE_PROJECT=$P GOOGLE_CLOUD_QUOTA_PROJECT=$P gcloud sql instances describe <name> \
  --project=$P --format="value(state,settings.activationPolicy)"
```

An earlier version of this section said `state` does **not** tell you whether an
instance is stopped, and that `RUNNABLE` is not the discriminator. That was
wrong, and how it got here is worth keeping: the running reading had been
measured, the stopped one had not, and the gap was filled with a cautious guess
that was nonetheless a claim about the provider. `state` does report `STOPPED`,
and it still read `STOPPED` on a second describe.

Prefer the activation policy when the two disagree, because it is the setting
being changed while `state` is derived from it. Whether `state` lags *during* the
transition is **not measured** — both reads here happened after the patch
operation returned.
([Documented](https://docs.cloud.google.com/sql/docs/postgres/start-stop-restart-instance).)

**A stopped instance fails as a TIMEOUT, not as a refusal** (**measured
2026-08-17**). The Auth Proxy goes on listening on loopback and accepts the
connection, so a client waits and then reports
`connection to server at "127.0.0.1", port 5433 failed: timeout expired`. That
reads like a broken proxy or a network fault rather than a backend that is
switched off, so check the activation policy before debugging connectivity.

**Stopping does not stop all charges.** Compute charges stop; **storage and any
reserved IP address continue to bill**
([Documented](https://docs.cloud.google.com/sql/docs/postgres/start-stop-restart-instance)).
So "stopped" is cheaper, not free, and a long-idle instance is still a line on
the bill — which is the number to check before assuming an idle plane costs
nothing.

### Schema migration cutover

**Every old-code seam is terminated before a migration starts, and none is
opened until the new code is the only code running** (**Policy**, Phase 3 item
4). Locally this is enforced: `dataplane-migrate` takes the lifecycle lock
exclusive and an open seam holds it shared, so the command waits. In cloud
mode nothing enforces it, and the failure it prevents is not the one the
migration's own locks cover.

A migration that scans and then alters takes its table locks before scanning,
so no row slips in between — that much holds regardless of what else is
running. What the locks cannot cover is a seam opened **before** the
migration, idle **through** it, that writes **after** the locks release with
code that predates the schema. Such a write satisfies every constraint its
code knows about and violates one it does not: the first case is a
`story_dispatches` row written by pre-000023 code with no
`dispatch_prompt_resolutions` child. The schema refuses that commit by a
deferred constraint trigger, so the outcome is a failed write rather than a
corrupt plane — but a failed write in an Orchestrator that believed it was
current is an incident, and the procedure exists so it is never reached.

The sequence, for a cloud plane:

1. Stop every Orchestrator and every `dataplanectl` session against the
   plane. Confirm nothing holds a connection.
2. Run the migration.
3. Deploy the new code.
4. Start Orchestrators on the new code only.

If a migration deadlocks against a session step 1 missed, Postgres aborts one
side; if that is the migration, the recorded version is dirty and the
migration's own message names the `dataplane-force-version` recovery. Do step 1
properly and retry.

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

There are two, and they are separate because they need different things:

- **`make test-gcs`** — the object adapter alone, against a real bucket. Needs
  `MAESTRO_GCS_TEST_BUCKET` and application default credentials.
- **`make test-cloud`** — the composed data plane, against Cloud SQL and Cloud
  Storage together. Needs `MAESTRO_CLOUD_DSN`, `MAESTRO_GCS_TEST_BUCKET`,
  `MAESTRO_CLOUD_ROOT_KEY` and `GOOGLE_CLOUD_QUOTA_PROJECT`, **plus a started
  instance and a running Auth Proxy** — neither of which an environment variable
  can express, so a run against a stopped instance fails as the timeout above.

Both refuse to run with their configuration missing rather than reporting a green
skip. A suite that skips on missing configuration and reports success is worse
than one that fails.

`MAESTRO_CLOUD_ROOT_KEY` must be **exactly 32 bytes** — the same length a key
file must be, since it protects the same vault. A fresh key each run is correct
*here* and only here: the suite's databases are disposable, which is the whole
reason a new key is safe against them. See the root-key section above for why the
same habit destroys a persistent plane.

Each `make test-cloud` run provisions its own empty database per test and drops
it, and purges its own object prefixes. **Measured 2026-08-17:** consecutive runs
left no databases and no listable object generations behind, which is what makes
the workflow re-runnable rather than merely repeatable. A full run takes about 75
seconds, most of it provisioning and migrating one database per plane test.

**The database drop is deliberately WITHHELD when the object purge fails**, and
the failure message names the retained database and the prefixes to clear. That
is not a bug to tidy up: the retained database's `binary_attachments` rows are
the only remaining record of which digests the run wrote, so dropping it would
turn a reported problem into the unattributable storage described above. Clear
the prefixes with the command in that section, then drop the database by hand.
