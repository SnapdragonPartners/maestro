+++
title = "Phase 2 Item 7 Design: Configuration Records And The Secrets Vault"
edit_date = "2026-07-31"
status = "live"
summary = "Design for item 7: configuration records under a governed key registry validated before write and resolved most-specific-wins along the org/product/repo lineage, and a secrets vault whose per-version keys and canonically-encoded AAD bind a ciphertext to every field deciding who may read it, with individual credentials resolved over a six-step ladder where specificity outranks ownership, replacement and deletion under optimistic concurrency promising unaddressability rather than erasure, a local root-key provider distinct from the replaceable secrets store, and one create-versus-load rule that every lifecycle operation shares so only a fresh plane may mint a key."
type = "design"
+++

# Phase 2 Item 7 Design: Configuration Records And The Secrets Vault

Status: **live** — Accepted by Codex and DR (2026-07-31) after six review
rounds (five P1s, then five, three, two, two and one; all upheld). Two of
them changed the design's shape rather than its wording: the locked-plane
model was impossible (D4) and the provider boundaries were conflated (D3).
The new-key recovery procedure is **measured against the pinned images**, not
argued (D4); its native-Linux confirmation is an explicit **item 8 gate**,
not an item 7 blocker.

Delivers the two families ADR 0022 names for Phase 2 that item 3 deliberately
did not create: **configuration records** and the **secrets vault**. Both are
built ahead of any consumer, which the phase plan flags as the one place its
own anti-speculation rule and the ADR point in opposite directions
(delegated decision 1, carve-out). The ADR wins; this document records what
that buys and what it costs.

The seam and its conventions are items 4 and 5's; the key file, the path
layout and `secret.Derive` are item 2's. This records only what differs.

## What item 7 owes

The plan's row: *"The configuration records family (org/product/repo lineage)
and the secrets vault: encrypted at rest inside the plane, unlocked by the
external key-file root of trust from item 2; OS-keychain and passphrase
backends stubbed behind the auth-module interface without being implemented.
Typed queries with tests, including the locked-plane failure path."*

Three things already exist and are not rebuilt:

- `paths.EnsureKey` creates and reads the 0600 root-of-trust key file under
  the config root, silently at setup (item 2, D1).
- `secret.Derive` turns that root key plus a context string into a
  deterministic **printable** credential — how the Postgres password and the
  object-store keys are produced without ever being stored (item 2, D1a).
- The four-root path layout, including the rule that the **data-root backup
  excludes the key file** (item 2; the backup operation itself is item 8).

**The motivating defect is v1's, and it is recorded.** The Phase 0
project-folder spike found `forge_state.json` storing a forge API token in
plaintext at 0600 — frozen and WONTFIX in v1, and the explicit thing the v2
design must not reproduce. A vault that merely moves that token into a
database column is the same defect with more infrastructure.

## D1. Configuration keys are governed by a registry, and validated before the write

A configuration record is a **registered key**, a **scope** on the
organization/product/repository lineage, and a **value**.

Round 1 had the value's type declared by the caller and checked when read.
That is not a type system, it is a convention: a writer can declare whatever
type it is about to write, so every value validates against its own claim and
nothing is ever wrong. Two things get in that way — a value no reader can use
lands successfully and fails much later somewhere else, and, worse, **nothing
stops a plaintext credential being written into an unencrypted family** whose
whole distinction from the vault is that it holds no secrets.

So keys are **declared in a code-resident registry**, exactly as ADR 0028
declares artifact payload types, and for the same reason: the schema lives
with the code that understands it, and validation happens at the seam.

Each registration carries:

| Field | Purpose |
| --- | --- |
| Key | The canonical dotted name, the only thing a caller may write |
| Value schema | Validated **before** the write, so an invalid value never lands |
| Permitted scopes | Which lineage levels may set it — some keys are organization-wide by nature |
| Sensitivity | Marks a key as credential-shaped; **refused** in this family, directing the caller to the vault |

An **unregistered key is refused**, which is what makes the registry
governing rather than advisory. The alternative — unknown keys pass through
unvalidated — reintroduces the caller-declared-type hole in a different
place.

**The registry ships with no seed vocabulary**, following the rule item 3
applied to tables and item 4 applied to artifact types: a key is registered
by the item that first writes it. Phase 2 has no consumer, so Phase 2
registers nothing, and the tests register their own fixtures.

### Resolution: most-specific-wins, one query, primary Product only

A caller asks for one key at a repository and gets the most specific record
that exists: repository, else product, else organization, else nothing.

**Reads resolve, they do not merge.** A merged value is a function of what was
set where, which nothing can display honestly on a settings screen and which
turns "why is this value what it is?" into an investigation.

Resolution is **one query returning the value and the level it came from**,
not three queries the caller reconciles. Three reads can disagree under
concurrent writes, and a caller that reconciles them is a second, untested
copy of the precedence rule.

**A repository resolves through its primary Product.** ADR 0018's
one-repo-one-primary-Product rule is what makes the lineage a chain rather
than a graph, and `repositories.primary_product_id` is `NOT NULL`, so the
chain always terminates. Membership in further Products via
`product_repositories` is deliberately **not** consulted: a repository in
three Products would otherwise have three competing parents with no defined
precedence between them.

### Identity and mutation

**One row per (organization, key, scope).** Without that constraint,
"most-specific-wins" is undefined the moment two rows exist at the same level
— the query would return whichever the planner reached first, and the bug
would appear as an intermittently wrong value rather than as an error. The
scope is the exclusive arc, so uniqueness is over `(organization_id, key,
scope_type, scope_id)`.

**Updates are conditional, like the vault's.** A configuration value is
shared mutable state reachable from more than one agent lifecycle, which is
exactly what ADR 0027 forbids resolving by last-writer-wins. Every record
carries a `version`; an update names the version the caller read, and zero
rows affected is a typed conflict rather than a silent overwrite of somebody
else's change.

**Deletion is a first-class operation, not an omission.** A record at a
specific level is an *override*, and the only way to restore inheritance from
the level above is to remove it — so without a delete, an override set once
at a repository can never be undone, only overwritten with a value that
happens to match the parent's and then silently diverges when the parent
changes. That is a worse state than having no override at all, because it
looks intentional.

Deletion carries the **same expected-version guard** as an update, for the
same reason and one more: an operator removing what they believe is a stale
override can otherwise erase a value somebody set a moment earlier, and an
unconditional delete reports success either way. Zero rows affected is the
same typed conflict.

## D2. The encryption envelope, stated completely

Round 1 asserted AES-256-GCM with a per-secret derived key and left the rest
implied. Every implied part is load-bearing, so all of it is stated here.

**Key material.** `secret.Derive` returns raw-URL **base64 text**, because
its outputs are connection-string credentials — measured against the source,
not assumed. An AES-256 key is 32 raw bytes, so item 7 adds
`secret.DeriveKey(rootKey, context) ([]byte, error)` returning the HKDF
output **before** encoding. Passing the base64 string as key material would
silently use a 43-byte value where the cipher wants 32, and reusing
`Derive`'s output at all would put one credential's bytes and the vault's key
material in the same derivation — the property `Derive`'s own doc comment
exists to preserve.

**Per-version keys, not per-secret.** The context is

```
maestro/dataplane/secret/v1/<secret_id>/<version>
```

Deriving per *secret* was round 1's proposal, and it is not enough once
replacement exists (D5): rotating a token rewrites the row under the same
key, and every rewrite draws another nonce from the same 96-bit space.
Binding the version into the context gives every stored ciphertext its own
key, which makes nonce reuse **structurally impossible** rather than
improbable — no birthday budget to reason about, and no counter to trust.

**The envelope stored on the row:**

| Column | Contents |
| --- | --- |
| `scheme` | The named scheme, e.g. `aes-256-gcm/hkdf-sha256/v1`. Not a bare integer: the string names what to do |
| `nonce` | The 12-byte random nonce, stored beside the ciphertext rather than prefixed into it |
| `ciphertext` | GCM output including its authentication tag |
| `version` | The secret's current version, part of the key context and of the AAD |

The nonce is a **separate column, not a prefix**, because a framed blob needs
a length convention every reader must agree on, and a reader that takes the
wrong prefix length fails as a decryption error rather than a parse error —
the least diagnosable failure available.

**Authenticated data binds the ciphertext to everything that decides who may
read it.** Round 2 used `organization_id || secret_id || version || scheme`,
which protects against the wrong attack. Moving a ciphertext to a different
row is already impossible — a different `secret_id` derives a different key,
so it fails before the AAD is consulted, and a test of that proves nothing.

The real exposure is **mutating the metadata on the row the ciphertext
already belongs to**. The key depends only on id and version, so an
`UPDATE secrets SET owner_user_id = <someone else>` leaves the ciphertext
perfectly decryptable and silently hands one person's credential to another.
The same is true of the name and the scope: changing either retargets a
working secret at a resource it was never issued for.

So the AAD covers **every field that determines access**:

`organization_id`, `secret_id`, `version`, `scheme`, `owner_user_id`
(including its absence), `name`, `scope_type`, `scope_id`.

**Encoded canonically, not concatenated.** Joining variable-length fields
end to end is ambiguous — a name of `ab` beside `c` produces the same bytes
as `a` beside `bc` — so equal AADs could describe different rows. The plane
already has an unambiguous encoder for exactly this problem:
`internal/dataplane/canonical`, the JCS/RFC 8785 implementation ADR 0028 uses
for digests. The AAD is the canonical JSON of those fields, which also gives
the null owner a distinct representation from any user id rather than an
empty string that a real value could collide with.

**The scheme is recorded per row, so a later scheme coexists with this one.**
Item 6's rule applies: the reader is the compatibility layer. Key rotation is
**not implemented** and the schema does not pretend it is — rotation needs
both keys live at once and a defined half-rotated state, which is a design in
its own right with no Phase 2 consumer. What this item owes rotation is only
that it stays possible.

## D3. Two boundaries, kept apart

Round 1 justified a single interface with "cloud mode replaces the root of
trust", and that conflated two different replacements. Cloud mode does not
hand Maestro a root key from a provider secret manager — it **replaces the
secrets module entirely**, because the provider stores and returns the
secrets themselves. An interface shaped "give me a root key" cannot express
that, and a cloud implementation forced through it would have to invent a
root key it has no use for.

So there are two seams, at different layers:

**`RootKeyProvider` — local only.** Answers one question: *give me the root
key material*. Three implementations, all local, all interchangeable:

| Backend | State | Behaviour |
| --- | --- | --- |
| Key file | **Implemented** | The 0600 file under the config root; the default, no ceremony, unattended-safe |
| OS keychain | **Refused** | Typed "not implemented" error naming the backend |
| Passphrase | **Refused** | Likewise |

**The secrets store — a seam on the persistence interface.** The local
implementation is this item's vault, which consumes a `RootKeyProvider`. A
cloud implementation talks to a provider secret manager and consumes no root
key at all. That is the seam ADR 0022 means when it says cloud mode replaces
this with the auth mini-app plus a provider secret manager — and the mini-app
is a third thing again, an authentication surface for users rather than a key
supplier.

**The contract is the whole of D5, not "put and get".** Describing it as a
key-value store would let a cloud implementation satisfy the interface while
silently dropping the semantics callers depend on — resolution order,
ownership, and conflict detection are not local implementation details, they
are the behaviour. Every implementation owes:

| Operation | Contract |
| --- | --- |
| Create individual | Owner is the **acting user**, never a parameter (D5) |
| Create shared | A **distinct call**, so "shared" is a deliberate act rather than an omitted field |
| Resolve | The **six-step ladder**, returning the value, the level, and whether the hit was individual or shared |
| Replace | Conditional on the **expected version**; conflict is typed, never last-writer-wins |
| Delete | Likewise conditional and likewise typed |
| Every operation | Carries the **acting user**, and enforces it on reads *and* writes |

A provider that offers only unconditional put and get therefore needs an
adapter that supplies the missing halves — versioning and the ladder — rather
than an interface narrow enough to accept it as-is. Discovering that at the
seam is the point of writing the contract down now, while the only
implementation is one we control.

**Refused, not stubbed, and the distinction is the point.** A stub returning
*something* — an empty key, or a silent fall-through to the key file — would
encrypt real secrets under a key the operator did not choose and believes
they did not use. Both unimplemented backends fail loudly at construction,
naming which backend was asked for. Selecting one is only possible
explicitly; nothing defaults onto them.

## D4. Setup and reopening are different operations

Round 1 claimed a plane could run "locked" — every family working except
secrets — and that is **impossible**. Review established why, and the
mechanism is worth stating exactly, because the failure it produces is
misleading:

The root key derives the **Postgres password and the object-store
credentials** as well as the vault's key material. `stack.Up` calls
`paths.EnsureKey`, which **creates a key if none is present**. So a data root
restored without its original key gets a *new* key, hence a new derived
Postgres password — while the existing cluster still holds the password
`initdb` wrote from the original key. The authenticated healthcheck
(`PGPASSWORD=… psql -tAc 'select 1'`, which item 2 chose over `pg_isready`
precisely because `pg_isready` succeeds with wrong credentials) then fails,
`waitReady` times out, and `up` reports **"data plane did not become ready"**
after three minutes.

That is a correct refusal arrived at by accident, and it diagnoses nothing:
the operator sees a timeout, not a missing key. So key handling splits by
what the data root already contains:

| Data root | Key handling |
| --- | --- |
| **Fresh** — every service data directory empty | `EnsureKey` **may create** the key. This is setup, and creating it silently is item 2's no-ceremony default |
| **Existing** — any service data directory populated | **Load only.** If the key file is absent, fail immediately with `ErrPlaneLocked`, naming the expected path and what restores it |

The check runs **before Compose is invoked**, so the diagnosis arrives in
seconds and names the actual cause instead of following a three-minute
readiness timeout that names a symptom.

**Emptiness is judged across every service data directory, not just
Postgres.** Round 2 named the Postgres directory alone, which is the
directory that proves the *password* problem — but the object-store
credentials derive from the same root key, so a plane whose Postgres
directory is empty while MinIO holds objects is equally an existing plane.
Any populated service directory means a plane that some earlier key already
provisioned.

**The rule is one function, and every lifecycle operation goes through it.**
There are three production call sites for `paths.EnsureKey` today — `up`,
`Migrate` and `ForceVersion` — and a guard placed only in `up` leaves the
other two able to mint a key against an existing plane. `Migrate` in
particular is the operation an operator reaches for *after* a restore, which
is exactly when the key is most likely to be missing. So create-versus-load
is decided in a single helper that all three call, and **only a fresh `up`
passes create**; `Migrate` and `ForceVersion` are always load-only, because
neither is ever the operation that provisions a plane.

**There is no partial-failure mode, and the design no longer claims one.**
The plane opens or it refuses. Configuration remains **unencrypted**
regardless — not to enable a partial mode, which does not exist, but because
a config record holds a forge URL or a model name while a secret holds the
token, and encrypting both would buy nothing while tempting a caller to put a
credential in whichever family was easier.

**Item 8 tests this as a restore sequence**: refuse with `ErrPlaneLocked`,
supply the original key, open successfully. That is exactly the two-part
restore contract the backup's key-exclusion creates, and it is a sequence
with observable states rather than an assertion.

### The other restore path ADR 0022 promises, and what it costs

ADR 0022 says restore on a new machine requires "the backup plus the key
file, **or re-entry of secrets**". Everything above serves the first branch.
The second one is not free, and this design does not deliver it — which is a
gap to close deliberately rather than to leave implied.

Re-entering secrets means accepting a **new** root key over an existing data
root, and the new key changes three things at once. **Both mechanisms below
are measured against the pinned images, not reasoned about** — an earlier
revision stated this table as fact on the strength of neither.

| Derived credential | Effect of a new key | Recovery needed |
| --- | --- | --- |
| Vault key material | Every stored ciphertext becomes undecryptable | Delete every secret row; the operator re-enters them |
| Postgres password | The cluster still holds the password `initdb` wrote from the **old** key | **Restart socket-only under a local-trust `hba_file`, `ALTER USER` over the Unix socket, restart clean** — measured below, under the shipped uid |
| Object-store credentials | `MINIO_ROOT_USER`/`MINIO_ROOT_PASSWORD` are passed as **environment**, not baked into the data directory | None; the store follows the new key — measured below |

**MinIO, measured** (`minio/minio@sha256:14cea49…`): a bucket and object were
written under one root credential pair, the container was replaced with a
second pair over the same data directory, and the object read back
successfully under the **new** credentials while the old ones were rejected
with `The Access Key Id you provided does not exist in our records`. The
object store therefore needs no recovery step at all — it follows the key.

**Postgres: single-user mode does not work here, which measuring under the
shipped identity is what revealed.** The obvious recipe — pipe
`ALTER USER … PASSWORD …` into `postgres --single -D <PGDATA>` — needs no
password and no `pg_hba` edit, and it succeeds when the container runs as the
image's own `postgres` user. But Compose runs Postgres as
`${MAESTRO_UID}:${MAESTRO_GID}` over a host-owned directory (item 2, D2a), and
that uid is not in the image's passwd file. Measured under it, the standalone
backend refuses before doing anything:

```
postgres: could not look up effective user ID 501: user does not exist
```

The normal server entrypoint tolerates an arbitrary uid; the standalone
backend calls `getpwuid` and does not. Running the recovery as `postgres`
instead is not an escape — on native Linux that uid cannot write the 0700
host-owned bind mount, which is the exact asymmetry item 2 found when a
container running as the image user passed on macOS and failed on Linux.

**So recovery runs the ordinary server with an overridden `hba_file` — and it
must never be reachable over the network while it does.** Trust
authentication means *anyone who can open a connection is the database
owner*, so a recovery step that publishes a TCP listener hands the cluster to
whatever else can route to it, during the one operation whose entire purpose
is restoring data somebody cares about. An earlier revision issued the
`ALTER USER` over a Docker network, which is exactly that hole.

The recovery server is therefore **socket-only**:

| Constraint | Why |
| --- | --- |
| `-c listen_addresses=''` | No TCP listener exists at all, so trust cannot be reached from off-host or from another container |
| No published port, no network attachment | Defence in depth behind the same fact |
| HBA carries `local all all trust` **only** | The `host` line is what an earlier revision needed and is now absent entirely |
| `ALTER USER` issued through the container's **Unix socket** | `docker exec … psql`, which needs no listener |
| Networking returns only **after** the clean restart | And only to verify the new credential authenticates |

**Measured under the shipped identity** (`postgres@sha256:3a82e1f5…`, 18.4),
running as `${MAESTRO_UID}:${MAESTRO_GID}` over a 0700 host-owned bind mount:
the recovery container reported **no listener on 5432** and no published
ports, `ALTER USER` succeeded over the socket, and after restarting with no
overrides the **new** password authenticated over the network with the
pre-reset data intact while the **old** password was rejected. Nothing about
the data directory's ownership changes, because the recovery container is the
same identity as the normal one.

**Two traps recorded, because both were walked into while measuring this.**
Verifying the passwords from *inside* the container proves nothing — the
image's `pg_hba` trusts in-container connections, so old and new passwords
both appeared to work, and the first round of results was vacuous. The
measurements above were redone over a Docker network by hostname, for the same
reason item 2's healthcheck connects by service name rather than loopback.
And a recovery step verified as the image's `postgres` user proves nothing
about the runtime that ships, which is the second trap and the one that
produced the wrong mechanism in an earlier revision.

**One limit stated rather than glossed:** all of this was measured on macOS,
where Docker Desktop virtualises bind-mount ownership. The claim that matters
on native Linux is that the recovery container runs as the *same* uid as the
normal one and therefore has exactly the access the normal one has — but item
2's own history is that this is the axis where the two platforms diverge, so
item 8 must exercise the sequence in the **native-Linux CI job** rather than
inheriting a developer-machine result.

New-key recovery is therefore **feasible and bounded, and it belongs to item
8** as an explicit, guarded destructive operation — it deletes every secret
and rewrites a database credential. ADR 0022's "or re-entry of secrets"
stands as written; nothing here needs amending. What item 7 owes item 8 is
the two things it has already built: a refusal that names the state
precisely, and a vault whose rows can be dropped wholesale without disturbing
any other family.

## D5. Secrets belong to users, and are replaced rather than accumulated

Round 1 scoped secrets to the lineage alone. ADR 0022 relies on **individual
credentials** — a forge token is a person's, not an organization's — so
ownership is part of the model, not a later refinement.

**A secret carries an optional owner.** Every secret is organization-scoped
and lineage-scoped as in D1; additionally it may name an owning user.

- `owner_user_id` **set** — an individual credential, readable only by that
  user.
- `owner_user_id` **null** — a shared credential for the scope.

**Resolution is a six-step ladder, and it is written out because "prefer the
individual" does not determine it.** Two orderings are consistent with that
phrase and they disagree: ownership as the outer sort (my org-wide token
beats the team's repo token) or specificity as the outer sort (the team's
repo token beats my org-wide one). The ladder is:

| # | Level | Owner |
| --- | --- | --- |
| 1 | Repository | the caller |
| 2 | Repository | shared |
| 3 | Product | the caller |
| 4 | Product | shared |
| 5 | Organization | the caller |
| 6 | Organization | shared |

**Specificity is the outer sort; ownership breaks ties within a level.** The
reason is what a credential is *for*: scope says which resource it works
against, and a credential for the wrong resource does not function no matter
whose it is, while a shared credential for the right resource does. Preferring
ownership across levels would reach past a repository deploy key for a
personal organization-wide token that may have no access to that repository
at all.

Attribution is preserved by **recording which secret answered**, not by
preferring a credential that may not work — so the read returns the level
**and** whether the hit was individual or shared. A caller that cannot tell
which it got cannot report "you are using the team token" to anybody.

**No user ever resolves another user's secret**, enforced in the query
(`owner_user_id = @user_id OR owner_user_id IS NULL`) rather than by
filtering after the read.

**Ownership constrains writes as well as reads, which round 2 left out
entirely.** A read predicate alone gives an access model where one user
cannot *see* another's credential but can freely replace or delete it — and
the destructive half is the more damaging one, since a caller who cannot read
a secret also cannot tell what they destroyed. So:

- **creation derives the owner from the acting user; it is not a parameter.**
  An owner the caller supplies is an owner the caller can lie about, and the
  damage is not merely a mislabelled row: the partial unique index gives each
  user exactly one slot per name and scope, so a secret created *as* somebody
  else **occupies that slot** — the victim's own creation then fails against a
  row they cannot read, replace or delete. A shared secret is created by
  asking for one explicitly, which is a different call and not an owner value;
- **replace and delete carry the acting-user predicate too**, matching the
  read: a statement affects a row only when it is the caller's own or shared.
  Zero rows affected is indistinguishable from a version conflict at the SQL
  level, which is correct — a caller learns that their write did not apply,
  not whether somebody else's credential exists;
- the owner is bound by a **composite foreign key** to
  `(user_id, organization_id)`, so a secret cannot name a user from another
  organization. The plain single-column reference would let a cross-tenant id
  through, and the composite form is the pattern the schema already uses for
  `repositories.primary_product_id` and for principals.

**One row per (organization, name, owner, scope) — and the nullable owner
needs two indexes, not one.** A plain `UNIQUE (organization_id, name,
owner_user_id, scope_type, scope_id)` does not do what it appears to: in
Postgres `NULL` is not equal to itself, so the constraint permits any number
of **shared** secrets with the same name at the same scope — precisely the
duplicates that make resolution non-deterministic, and precisely the case
that is not exercised if every test seeds an owner. Two partial unique
indexes state the rule honestly:

- `WHERE owner_user_id IS NOT NULL` over the full tuple — one individual
  secret per user, name and scope;
- `WHERE owner_user_id IS NULL` over the tuple without the owner — one shared
  secret per name and scope.

The alternative, a sentinel UUID standing in for "nobody", makes one index
work at the cost of a magic value that every query must remember to exclude
and that a real user id could in principle collide with.

**Replacement rewrites the row; it does not append.** Item 6's "accepted rows
are never rewritten" is an *artifact* rule, and importing it here was a
mistake: an artifact is reviewed history whose immutability is the point,
while a secret is a live credential. Keeping superseded ciphertexts would make
every rotated-away token recoverable forever — turning rotation, whose whole
purpose is to end a credential's usefulness, into an archive of credentials.

So replacement is an **UPDATE in place** that bumps `version`, derives the new
key from the new version, and writes a fresh nonce, ciphertext and AAD.

**What that does and does not promise.** Round 2 said the previous ciphertext
"is gone" and its key "unreachable". Both are false, and stating them would
have been a security claim the system does not honour:

- Postgres `UPDATE` writes a new tuple and leaves the old one dead until
  vacuum; the old value also survives in the WAL and in every backup taken
  before the rotation;
- the old key is **derivable at any time** from the root key, the secret id
  and the previous version, because HKDF is deterministic. Nothing about
  bumping a version makes an earlier context uncomputable.

The honest promise is narrower and still worth having: **a replaced secret is
no longer addressable through the vault.** The seam will not return it, no
resolution reaches it, and the live row holds only the current value. That is
rotation of the *addressable* credential, not cryptographic erasure, and
anyone who needs the latter needs a different design — one this item does not
claim to provide.

**Concurrent replacement is serialized, not last-writer-wins.** ADR 0027
names bare last-writer-wins on shared state as a defect. Replacement is
conditional on the version the caller read (`WHERE version = @expected`);
zero rows updated is a typed `ErrSecretConflict`, and the caller re-reads
rather than overwriting a rotation it never saw.

**Deletion is conditional too, for the same reason.** A plain delete races a
concurrent replacement: an operator removing what they believe is a stale
credential can erase a rotation committed a moment earlier, and the delete
succeeds either way, so nothing reports it. `DELETE … WHERE version =
@expected` makes that collision an `ErrSecretConflict` instead of a silent
loss. Nothing references secrets — configuration is separate and unencrypted
by D4 — so there is no dependency to break.

## D6. A secret value is a type that cannot be printed

The v1 defect was a token in a file. The equivalent v2 defect is a token in a
log line, and it arrives the same way: someone formats a struct with `%+v`.

So plaintext leaves the vault as a `secret.Value`, not a `string`:

- `String()`, `GoString()` and `Format()` render `[redacted]`;
- `MarshalJSON` renders `"[redacted]"`, so a struct serialised into an
  artifact payload or an error body cannot carry it;
- the plaintext is reachable only through an explicit `Reveal()` whose name
  is greppable in review.

This does not make leaking impossible — `Reveal()` exists, and it must. It
makes leaking **deliberate and visible**, which is the difference between a
mistake and a decision. The tests assert every formatting verb, because the
one that is missed is the one that gets used.

## D7. Testing

Behavioural, against the real Postgres, as items 4-6:

| Case | Required outcome |
| --- | --- |
| Resolve a key set at all three levels | The repository's value, with its level reported |
| Resolve a key set only at the organization | The organization's value, through the primary Product |
| Resolve a key set nowhere | Not found, distinguishable from a set-but-empty value |
| A repository in several Products | Resolution follows the **primary** Product only |
| Write an unregistered key | Refused; nothing lands |
| Write a value failing the registered schema | Refused **before** the write, asserted by reading the table afterwards |
| Write to a scope the key does not permit | Refused |
| Write a credential-sensitivity key to configuration | Refused, naming the vault |
| Cross-tenant read of any config or secret | Not found, as everywhere on this seam |
| Round-trip a secret | Plaintext returns; the stored column is neither the plaintext nor a prefix of it |
| Two secrets with the **same plaintext** | Different ciphertexts |
| Replace a secret | New version, new nonce, new ciphertext; the vault no longer returns the old value |
| Two concurrent replacements | One succeeds; the loser gets `ErrSecretConflict`, not a silent overwrite |
| Delete racing a replacement | `ErrSecretConflict`; the rotation is not silently erased |
| Two concurrent configuration updates | One succeeds; the loser gets a typed conflict |
| Delete a repository override | The product or organization value is inherited again — the reason deletion exists |
| Configuration delete racing an update | Typed conflict; neither silently wins |
| One user **replacing or deleting** another's secret | No rows affected, reported as a conflict — asserted for both verbs, since a read-only ownership test passes with the write side wide open |
| The public creation input declares **exactly** its permitted fields | A **structural** test over the seam's type, asserting the field set rather than the absence of one name — see below |
| The **stored** owner equals the authenticated acting user | Read the row back and compare against the caller's identity, not against what was passed |
| A user creating their own secret where another user already has one of the same name and scope | Both succeed and resolve independently — the slot is per user, and this is the case a poisoned slot would break |
| A secret owned by a user of **another organization** | Refused by the composite foreign key |
| Tampered ciphertext | Decryption fails on GCM authentication |
| **`owner_user_id` changed** on a row, id and version untouched | Decryption fails on the AAD — the case that matters, since a moved ciphertext already fails on the key |
| **`name` or scope changed** likewise | Fails the same way; a working secret cannot be retargeted |
| The **full six-step ladder** | Each step asserted by seeding only the levels below it and confirming which row answers |
| Two shared secrets, same name and scope | Refused by the partial unique index — asserted **without** an owner, since a seeded owner cannot reach this case |
| Another user's individual secret | Not found — enforced in the query, asserted with two users |
| Open an **existing** data root with no key file | `ErrPlaneLocked` before Compose runs, naming the path |
| Open a **fresh** data root with no key file | Key created; the plane comes up |
| Keychain or passphrase backend selected | Typed refusal naming the backend; nothing encrypted, nothing written |
| A `secret.Value` through `%v`, `%s`, `%+v`, `%#v`, `json.Marshal` | `[redacted]` in every one |

**Mutation-verified**, per this phase's standing lesson. The guards most
likely to pass for the wrong reason are named in advance, and review round 2
supplied one of them by finding a test that could not have failed:

- the **AAD** — the round-2 case moved a ciphertext to another row, which
  fails on the derived key before authentication is reached, so it would have
  passed with no AAD at all. The replacement mutates metadata *on the same
  id and version*;
- the **partial unique index on shared secrets** — a test that seeds an owner
  never reaches the `NULL`-owner branch, which is the branch that is wrong by
  default;
- the **redaction verbs** — a partially-redacting implementation satisfies
  any single one;
- the **cross-user filter** — invisible to a test with one user;
- **creation's ownership**, which needs a structural test rather than a
  behavioural one. "Create as A, read as B" exercises the read filter and
  nothing else: it passes whether or not the creation API accepts an owner,
  because a test that never supplies one cannot discover that supplying one is
  possible. The guard is the **shape of the input type**, so the test parses
  the seam — the same instrument item 6 used for its one-emptying-protocol
  rule, and for the same reason: the property is about which code can be
  written, not about a value any run produces.

  **It asserts the exact permitted field set, not the absence of a field
  called `owner`.** A deny-list on one name is defeated by `user_id`,
  `principal_id` or `on_behalf_of` — the same defeat the queries structure
  test already documents, where a name-only allow-list was beaten by
  rewriting an approved statement. Adding a field to the creation input is
  then a change that fails the build and has to be argued for, which is the
  reviewable act. The behavioural half is separate and compares the **stored**
  owner against the authenticated caller;
- the **fresh-versus-existing data-root split** — a suite that only ever runs
  against a fresh root proves half the rule, and the half it skips is the one
  that refuses.

## D8. What this item does not do

- **No key rotation.** D2 leaves it possible; nothing implements it.
- **No keychain or passphrase backend.** Refused, per D3.
- **No cloud secrets store.** The seam D3 defines is where it lands; ADR 0022
  places it in cloud mode.
- **No consumer.** Both families are built ahead of use, per the plan's
  carve-out. The first real caller is Phase 3's forge binding, where a forge
  token stops being a v1 file and becomes a vault row.
- **No backup operation.** Item 8, which depends on this item only for the
  refusal behaviour D4 defines.
- **No configuration UI or precedence override.** Resolution is
  most-specific-wins and nothing else; a per-key override policy is a Phase 5
  gate concern.

## Review questions

Round 1's four are **resolved**: the registry stays in this item with an empty
vocabulary; per-version derivation stays; individual-first **within each scope
level** stays, with the full ladder now tabled and tested; `Reveal()` stays.

Both remaining questions are now **resolved**. The first carries an
obligation into item 8; the second is recorded only so the road not taken is
legible.

1. **Resolved: new-key recovery lands in item 8, and ADR 0022 stands.** The
   mechanism is measured and feasible (D4), so the ADR is not overpromising;
   what it costs is that item 8 grows beyond "quiesce, copy, restart" to
   include an explicit, guarded destructive operation — one that deletes every
   secret and rewrites a database credential. Guarded in item 8's own terms:
   it destroys more than `reset` does in the only sense that matters, since
   the vault's contents cannot be recreated from anything in the backup.
   **Item 8 also owes the native-Linux measurement**: everything in D4 was
   measured on macOS, and the bind-mount ownership axis is precisely where
   item 2 found the two platforms disagreeing.
2. **Resolved: specificity stays the outer sort.** The ladder prefers a
   shared repository credential over the caller's own organization-wide one,
   on the grounds that a credential for the wrong resource does not work
   whoever owns it. Recorded here because the alternative is not a small
   variation: making ownership the outer sort would reorder **steps 2 through
   5** — every individual level ahead of every shared one — rather than
   swapping a neighbouring pair, which is how an earlier revision described
   it.
