+++
title = "Phase 2 Item 7 Design: Configuration Records And The Secrets Vault"
edit_date = "2026-07-30"
status = "draft"
summary = "Design for item 7: configuration records resolved along the org/product/repo lineage with most-specific-wins precedence, and a secrets vault encrypted at rest inside the plane under a per-secret key derived from the external key-file root of trust, with keychain and passphrase backends refused rather than stubbed, a redacting value type that makes a leaked secret a compile-time-shaped mistake, and a locked plane that fails loudly on secrets while every other family keeps working."
type = "design"
+++

# Phase 2 Item 7 Design: Configuration Records And The Secrets Vault

Status: **draft** — awaiting review.

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
  deterministic credential — how the Postgres password and the object-store
  keys are produced without ever being stored (item 2, D1a).
- The four-root path layout, including the rule that the **data-root backup
  excludes the key file** (item 2; the backup operation itself is item 8).

**The motivating defect is v1's, and it is recorded.** The Phase 0
project-folder spike found `forge_state.json` storing a forge API token in
plaintext at 0600 — frozen and WONTFIX in v1, and the explicit thing the v2
design must not reproduce. A vault that merely moves that token into a
database column is the same defect with more infrastructure.

## D1. Configuration records resolve along a lineage, most-specific-wins

A configuration record is a typed key and value scoped to exactly one of
**organization**, **product** or **repository** — the exclusive arc the
schema already uses for artifact scope, and enforced the same way with
`num_nonnulls`. The three levels are the lineage ADR 0018 defines, and the
existing composite keys (`repositories_id_org_key`,
`products (product_id, organization_id)`) make an organization-scoped read
provably unable to reach another tenant's row.

**Reads resolve, they do not merge.** A caller asks for one key at a
repository, and gets the most specific record that exists for it: repository,
else product, else organization, else nothing. The alternative — merging
partial records down the lineage — makes the effective value a function of
what was set where, which nothing can display honestly on a settings screen and
which turns "why is this value what it is?" into an investigation.

Resolution is therefore **one query returning one row plus the level it came
from**, not three queries the caller reconciles. Three reads can disagree
under concurrent writes, and a caller that reconciles them is a second,
untested copy of the precedence rule.

**A repository resolves through its primary Product.** ADR 0018's one-repo-
one-primary-Product rule is what makes the lineage a chain rather than a
graph, and `repositories.primary_product_id` is `NOT NULL`, so the chain
always terminates. Membership in further Products via `product_repositories`
is deliberately **not** consulted: a repository in three Products would
otherwise have three competing parents and no defined precedence between
them.

**Values are JSONB with a declared type, not free text.** The type is
recorded beside the value so a reader can refuse a value that is not what the
key promises, rather than discovering it at the first `strconv` in an
unrelated package. This is the same reasoning ADR 0028 applies to artifact
payloads, at a much smaller scale, and it is why the family is not simply
`text`.

## D2. The vault stores ciphertext; the plane never holds the key

A secret record holds a **name**, a **scope** on the same lineage arc as D1,
and a **ciphertext**. It holds no plaintext, ever, and no key.

**Encryption is AES-256-GCM with a per-secret derived key.** The root key
from the key file is never used directly as an encryption key. Each secret's
key is `HKDF(rootKey, context = "maestro/dataplane/secret/v1/" + secret_id)`,
which gives two properties worth having for one line of code:

- **Nonce reuse across secrets is impossible.** A single vault-wide key with
  random 96-bit nonces is safe only up to a birthday bound; per-secret keys
  remove the question rather than budget for it.
- **A leaked single secret's key compromises exactly that secret.**

The id is a UUIDv7 allocated before the encryption, exactly as item 6
preallocates ids for its cross-store commit order — the key depends on the
id, so the id cannot be assigned by the INSERT.

**The context string is versioned and stored.** `secret.Derive`'s existing
contexts already carry `/v1`; the vault records which context version
encrypted each row, so a future scheme can be introduced without a migration
that must decrypt and re-encrypt every row under a key nobody has yet. Item
6's rule applies: the reader is the compatibility layer, and rows are never
rewritten.

**Key rotation is not implemented and the schema does not pretend it is.**
Rotation needs both keys present at once and a defined half-rotated state,
which is a design in its own right with no Phase 2 consumer. What this item
owes rotation is only that the stored context version makes it *possible*
later — recorded here so a reviewer does not read the version column as a
claim that rotation works.

## D3. The auth module is an interface with one implementation, and the reason is named

`CLAUDE.md` requires a concrete reason for an interface with one
implementation. The reason is not the two future local backends; it is that
**cloud mode replaces the root of trust entirely** with the auth mini-app and
a provider secret manager (ADR 0022). The seam is where that swap happens,
and building the vault against a concrete key file would put a local-only
assumption inside every call site.

The interface answers one question — *give me the root key* — and three
things implement it:

| Backend | State | Behaviour |
| --- | --- | --- |
| Key file | **Implemented** | `paths.EnsureKey`; the default, no ceremony, unattended-safe |
| OS keychain | **Refused** | Returns a typed "not implemented" error naming the backend |
| Passphrase | **Refused** | Likewise |

**Refused, not stubbed, and the distinction is the whole point.** A stub that
returns *something* — an empty key, or a silent fall-through to the key file —
would encrypt real secrets under a key the operator did not choose and
believes they did not use. Both unimplemented backends therefore fail
loudly at construction, and the failure names which backend was asked for.
Selecting one is only possible explicitly; there is no default that can drift
onto them.

## D4. A locked plane fails on secrets and nowhere else

The key file can be missing — restored from a backup that excludes it by
design, or moved to another machine. That state has a name, **locked**, and
a defined blast radius:

- every **secrets** read and write fails with a typed `ErrPlaneLocked`
  naming the key path and what restores it;
- every other family — artifacts, calls, configuration, objects — works
  normally, because none of them is encrypted.

Configuration is deliberately **not** encrypted, which is what makes that
split possible. A configuration record holds a forge URL, a repository slug,
a model name; a secret holds the token. Encrypting both would make an
unreadable key file a total outage rather than a partial one, and would
tempt a caller to put a credential in a config record because config is the
family that still works.

The failure is loud at the **first secrets call**, not at startup. A plane
that refused to start without the key would make the backup's own
two-part restore contract untestable — restore, observe locked, supply the
key, observe unlocked — which is precisely the sequence item 8 has to prove.

## D5. A secret value is a type that cannot be printed

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

## D6. Testing

Behavioural, against the real Postgres, as items 4-6:

| Case | Required outcome |
| --- | --- |
| Resolve a key set at all three levels | The repository's value, with its level reported |
| Resolve a key set only at the organization | The organization's value, through the primary Product |
| Resolve a key set nowhere | Not found, distinguishable from a set-but-empty value |
| A repository in several Products | Resolution follows the **primary** Product only |
| Cross-tenant read of any config or secret | Not found, as everywhere on this seam |
| Round-trip a secret | Plaintext returns; the stored column is neither the plaintext nor a prefix of it |
| Two secrets with the **same plaintext** | Different ciphertexts — per-secret keys and random nonces, asserted rather than assumed |
| Tampered ciphertext | Decryption fails; GCM's authentication is what makes silent corruption impossible |
| Missing key file | `ErrPlaneLocked` from secrets; artifacts, calls and configuration unaffected in the same test |
| Keychain or passphrase backend selected | Typed refusal naming the backend; nothing encrypted, nothing written |
| A `secret.Value` through `%v`, `%s`, `%+v`, `%#v`, `json.Marshal` | `[redacted]` in every one |
| Restore-shaped sequence: lock, fail, supply key, succeed | The two-part restore contract item 8 depends on |

**Mutation-verified**, per this phase's standing lesson: the guards that
matter here — the precedence order, the tenancy scoping, the redaction, the
locked-plane split — are each individually broken to prove their test fails.
The redaction verbs are the likeliest place to write a test that passes for
the wrong reason, since a partially-redacting implementation satisfies any
single verb.

## D7. What this item does not do

- **No key rotation.** D2 leaves it possible; nothing implements it.
- **No keychain or passphrase backend.** Refused, per D3.
- **No consumer.** Both families are built ahead of use, per the plan's
  carve-out. The first real caller is Phase 3's forge binding, which is
  where a forge token stops being a v1 file and becomes a vault row.
- **No backup operation.** Item 8, which depends on this item only for the
  locked-plane behaviour D4 defines.
- **No configuration UI or precedence override.** Resolution is
  most-specific-wins and nothing else; a per-key override policy is a Phase 5
  gate concern.

## Review questions

1. **Is the primary-Product-only lineage right?** It makes resolution a
   chain, at the cost that a repository shared across Products cannot inherit
   from the non-primary ones. The alternative needs a defined precedence
   between sibling Products, which ADR 0018 does not give.
2. **Per-secret derived keys, or one vault key with random nonces?** This
   design takes per-secret because it removes a bound rather than budgeting
   for it; the cost is one HKDF per read.
3. **Should the locked plane fail at startup instead of at first use?** D4
   argues no, on the grounds that item 8's restore contract needs the partial
   state to be observable. A reviewer who wants fail-fast should say so now,
   because item 8 builds on this.
4. **Is `Reveal()` the right escape hatch shape?** It is greppable and
   explicit. The alternative — no escape hatch, with every consumer taking a
   callback — is stricter and harder to use, and no consumer exists yet to
   judge it against.
