+++
title = "Phase 2 Item 7 Design: Configuration Records And The Secrets Vault"
edit_date = "2026-07-30"
status = "draft"
summary = "Design for item 7: configuration records under a governed key registry that validates before write and resolves most-specific-wins along the org/product/repo lineage, and a secrets vault whose per-version keys, stored nonces and identity-bound AAD are derived from raw root-key material, with individual credentials owned by users and falling back to shared ones, replacement under optimistic concurrency rather than immutable history, a local root-key provider kept distinct from the replaceable secrets store, and a plane that refuses to open an existing data root without its original key."
type = "design"
+++

# Phase 2 Item 7 Design: Configuration Records And The Secrets Vault

Status: **draft** — awaiting review. Rewritten after review round 1, which
found five P1s; the two that changed the design's shape rather than its
wording were the impossible locked-plane model (D4) and the conflated
provider boundaries (D3).

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

**Authenticated data binds the ciphertext to its identity.** The AAD is
`organization_id || secret_id || version || scheme`. Without it, a ciphertext
copied from one row to another decrypts successfully under its own key, so a
row's value could be swapped for another's by anyone able to write the table.
With it, a moved ciphertext fails authentication — and the per-version key
means it usually cannot even be decrypted, so the AAD is the second of two
independent barriers rather than the only one.

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

**The secrets store — a seam on the persistence interface.** Answers *put and
get a secret by name and scope*. The local implementation is this item's
vault, which consumes a `RootKeyProvider`. A cloud implementation talks to a
provider secret manager and consumes no root key at all. That is the seam ADR
0022 means when it says cloud mode replaces this with the auth mini-app plus
a provider secret manager — and the mini-app is a third thing again, an
authentication surface for users rather than a key supplier.

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
| **Fresh / empty** — no cluster yet | `EnsureKey` **may create** the key. This is setup, and creating it silently is item 2's no-ceremony default |
| **Existing / non-empty** — a cluster is present | **Load only.** If the key file is absent, fail immediately with `ErrPlaneLocked`, naming the expected path and what restores it |

The check runs **before Compose is invoked**, so the diagnosis arrives in
seconds and names the actual cause instead of following a three-minute
readiness timeout that names a symptom.

"Non-empty" is read from the **Postgres service data directory**: `initdb`
populates it, so its contents are the honest signal that a cluster exists
carrying a password derived from some earlier key.

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

## D5. Secrets belong to users, and are replaced rather than accumulated

Round 1 scoped secrets to the lineage alone. ADR 0022 relies on **individual
credentials** — a forge token is a person's, not an organization's — so
ownership is part of the model, not a later refinement.

**A secret carries an optional owner.** Every secret is organization-scoped
and lineage-scoped as in D1; additionally it may name an owning user.

- `owner_user_id` **set** — an individual credential, readable only by that
  user.
- `owner_user_id` **null** — a shared credential for the scope.

**Resolution prefers the individual, then the shared.** For a caller acting
as user *U* seeking name *N* at repository *R*: *U*'s own secret at the most
specific level, else a shared secret at the most specific level, else
nothing. One query, as D1, returning the level **and** whether the hit was
individual or shared — a caller that cannot tell which it got cannot report
"you are using the team token" to anybody.

**No user ever resolves another user's secret**, enforced in the query
(`owner_user_id = @user_id OR owner_user_id IS NULL`) rather than by
filtering after the read.

**Replacement rewrites the row; it does not append.** Item 6's "accepted rows
are never rewritten" is an *artifact* rule, and importing it here was a
mistake: an artifact is reviewed history whose immutability is the point,
while a secret is a live credential. Keeping superseded ciphertexts would make
every rotated-away token recoverable forever — turning rotation, whose whole
purpose is to end a credential's usefulness, into an archive of credentials.

So replacement is an **UPDATE in place** that bumps `version`, derives the new
key from the new version, and writes a fresh nonce, ciphertext and AAD. The
previous ciphertext is gone, and its key is unreachable because the context
that derived it corresponds to no row.

**Concurrent replacement is serialized, not last-writer-wins.** ADR 0027
names bare last-writer-wins on shared state as a defect. Replacement is
conditional on the version the caller read (`WHERE version = @expected`);
zero rows updated is a typed `ErrSecretConflict`, and the caller re-reads
rather than overwriting a rotation it never saw.

**Deletion is a plain delete of the row.** Nothing references secrets — the
configuration family is separate and unencrypted by D4 — so there is no
dependency to break, and a deleted secret's key context corresponds to no
row.

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
| Replace a secret | New version, new nonce, new ciphertext; the old plaintext is unrecoverable from the row |
| Two concurrent replacements | One succeeds; the loser gets `ErrSecretConflict`, not a silent overwrite |
| Tampered ciphertext | Decryption fails on GCM authentication |
| Ciphertext **moved to another row** | Fails: the AAD binds it to its own identity |
| An individual secret beside a shared one at the same scope | The caller's own wins; another user still resolves the shared one |
| Another user's individual secret | Not found — enforced in the query, asserted with two users |
| Open an **existing** data root with no key file | `ErrPlaneLocked` before Compose runs, naming the path |
| Open a **fresh** data root with no key file | Key created; the plane comes up |
| Keychain or passphrase backend selected | Typed refusal naming the backend; nothing encrypted, nothing written |
| A `secret.Value` through `%v`, `%s`, `%+v`, `%#v`, `json.Marshal` | `[redacted]` in every one |

**Mutation-verified**, per this phase's standing lesson. The guards most
likely to pass for the wrong reason are named in advance: the redaction verbs
(a partially-redacting implementation satisfies any single one), the
cross-user query filter (a test with one user cannot see it), the AAD (a test
that never moves a ciphertext cannot), and the fresh-versus-existing
data-root split (a test that only ever runs against a fresh root proves half
the rule).

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

1. **Does the key registry belong in this item, or in the item that first
   registers a key?** Building it now is the same carve-out that builds the
   families themselves, but the registry has strictly less justification: no
   key is registered until Phase 3. The alternative is caller-declared types
   until then, which round 1 showed is not a type system at all.
2. **Is per-version key derivation worth it over a version-scoped nonce
   counter?** It removes the nonce question entirely at the cost of one HKDF
   per read; a counter would be cheaper and would have to be trusted.
3. **Should the individual-versus-shared preference be configurable?** It is
   fixed at individual-first here. A team wanting shared-first has no way to
   say so, which may be right for MVP and is worth stating rather than
   discovering.
4. **Is `Reveal()` the right escape hatch shape?** It is greppable and
   explicit. The alternative — no escape hatch, every consumer taking a
   callback — is stricter and harder to use, and no consumer exists yet to
   judge it against.
