+++
title = "Design: Prompt Pack Identity, Storage, And Resolution (Item 4)"
edit_date = "2026-09-03"
status = "draft"
summary = "Mini-plan for Phase 3 item 4: the prompt-pack family built whole — immutable content records under a scheme-qualified digest, guarded by the schema's first anti-update trigger, beside mutable installation records carrying a monotonic revision and a governed installer identity; one atomic validated install operation so no content commits uninstalled and no coverage check runs without its declaring installation; the import gate reached through a consumer-owned contract so the seam validates every pack write without the plane importing a renderer; a selector configuration key that is the key registry's first live reader; resolution once at dispatch persisted beside the basis with the harness version it was validated against; a dispatch-bound principal path that copies the persisted resolution so a live principal cannot disagree with its dispatch; and organization provisioning that imports the built-in pack and seeds its selector in one transaction, with the import-and-select operator verb that later built-in versions move through. The built-in pack ships EMPTY and declares no role coverage, because item 4 has no model caller and neither candidate slot survived inspection: v1 has exactly one system prompt, the Architect's, bound to v1's workspace and tool contracts. Resolvable but not executable is the honest state, so the loader takes an fs.FS and the non-vacuous proof comes from fixtures travelling the identical path. Carries the principal_instances three-roles-in-one-column split as a total migration with an origin discriminator, the scheme-qualified MPH query, the importer's legacy-scheme backfill, and five amendments — including the size, which review re-cut from M to L."
type = "design"
+++

# Design: Prompt Pack Identity, Storage, And Resolution (Item 4)

Mini-plan for **Phase 3 item 4** (`prompt-packs`), the last item of block A and
the one that makes [Checkpoint 1](plan_scope.md#block-a--foundations) runnable:
*"a fresh organization is provisioned with a resolvable prompt-pack selector."*

Item 3 left two seats deliberately empty and this item fills both. The
provisioning family stops at repository because *"a default written here is one
item 4 must migrate"* ([item 3 design](design_orchestrator-seam.md), D11), and
`orchestrator.Keys()` returns `configkeys.MustNew(nil)` because *"a key
registered without a reader is a guess about a future caller"* (D7). Neither is
a stub to replace; both are absences to complete.

**Round 1 (Codex, 2026-09-03) found nine P1s and this revision carries all of
them.** Each is recorded under [Points Resolved In Review](#points-resolved-in-review)
with what was wrong and where the fix landed. The first draft was M-shaped; the
fixes make it L, and that is amendment 5.

## What Binds This Item

[ADR 0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md) is the
whole specification and every decision below traces to a section of it. Also
binding: [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) (the
MPH signature's P, recorded on the principal instance),
[ADR 0022](../../adr/0022-v2-data-plane.md) (packs are a data-plane family
reached through the persistence seam),
[ADR 0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md) (the JCS
digesting discipline the pack scheme borrows),
[ADR 0027](../../adr/0027-concurrency-safety-for-shared-local-infrastructure.md)
(optimistic concurrency on the installation revision and on the selector), and
[ADR 0019](../../adr/0019-orchestrator-boundary.md) (selecting a pack from
configuration is rules, not judgment, so resolution is Orchestrator machinery).

The plan's item 4 line requires ADR 0031's storage and resolution, *"organization
provisioning seeding the scoped selector — which completes item 3's provisioning
rather than amending it later"*, the `principal_instances.prompt_pack_id`
three-roles-in-one-column correction, and the `"v1-embedded"` foreign-pack case.
Item 3's amendment 3 assigns configuration's **first live consumer** here.

## Scope

**In.** The pack content and installation records and their constraints,
including the anti-update trigger and the composite tenancy keys; both identity
schemes and the scheme-qualified MPH query; the atomic validated install
operation and the conditional installation update; the harness-side slot
registry and the consumer-owned contract the seam validates through; the
built-in loader over `fs.FS`; the selector configuration key and its
registration by the Orchestrator; resolution at dispatch with its typed refusals
and its version semantics; the resolution row persisted beside the dispatch
basis; the dispatch-bound principal path; organization provisioning of content,
installation and selector in one transaction; the import-and-select operator
verb; the `principal_instances` column split as a total migration with a
refusing down migration; and the importer change that sets the legacy scheme.

**Out.** Everything [ADR 0031's Deferred section](../../adr/0031-prompt-pack-identity-resolution-and-storage.md)
defers, and four things this design assigns explicitly rather than leaving
unowned (D11): the production slot vocabulary, the first executable built-in
version, the dispatch check that an execution's roles fall within declared
coverage, and the restart-time compatibility re-check. All four want a subject
item 4 does not have.

## Decisions

### D1. The built-in pack ships empty, and "resolvable but not executable" is the honest state

The first draft of this design proposed seeding the built-in pack with two
slots, an Architect and a Coder role system prompt, on the reasoning that a role
system prompt is the one slot shape that does not depend on v2's state names.
Review rejected it and inspection of the tree shows review was right on stronger
grounds than the argument it made:

- **`pkg/templates` contains exactly one system prompt**, `architect/system_prompt.tpl.md`,
  and it is bound to v1 throughout — it mounts the coder workspace at
  `/mnt/coders/{{.Extra.AgentID}}`, carries v1's communication protocol, and
  reads its entire context out of the `.Extra` bag. Re-cutting it needs v2's
  workspace and tool contracts, which are
  [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md)'s and
  item 7's.
- **There is no Coder system prompt at all.** `pkg/templates/coder/` holds 28
  state templates and nothing role-level. The only system-prompt-shaped coder
  material is `pkg/templates/claude/{planning,coding}.tpl.md`, which is Claude
  Code adapter content and belongs to item 8.

So one proposed slot was unportable and the other did not exist. Seeding them
would have been inventing a vocabulary, which is what all three candidate scopes
were meant to avoid.

**The rule, stated once and applied throughout: a slot is registered by the item
that first renders it.** This is `configkeys`' rule — *"a registered key with no
writer is a guess about a future caller"* — and `internal/dataplane/work`'s, one
level down. Item 4 has no model caller, so item 4 has no production slot
vocabulary.

**Therefore the built-in pack's content is the empty entry set and its
installation declares no role coverage.** Organization provisioning imports it
and seeds a selector that genuinely resolves. Every path through ADR 0031 §4
stays intact: no name-based fallback, no binary fallback at run time, one
deterministic selector found by the ordinary route. The pack is **resolvable and
not executable**, and the two are different claims.

#### The §6 sentence this has to answer

ADR 0031 §6 says: *"A default pack has a present consumer — every Phase 3 run —
and the question here is not whether it exists but which copy is
authoritative."* An empty default serves no run, so the sentence deserves an
answer rather than a step around it.

It holds, because that passage is about **authority**, not about content volume.
Its subject is the choice between the plane's record and the binary, and its
conclusion is that there is no run-time fallback to the binary. An empty
built-in does not weaken that conclusion; it strengthens it, by leaving nothing
tempting to fall back to. What the passage does establish is that the default
must **exist and be reachable**, which is exactly what provisioning delivers.

#### The false-green this creates, named before it is mitigated

An empty pack means the production import path is only ever exercised over zero
entries. The digest of `{}`, coverage over the empty role set, parse over zero
entries, and the variable contract over zero entries all agree with themselves
perfectly — the shape [the reachability rule](../process_build.md#reachability-claims)
warns about, and the shape this repository has shipped before. D2 is the
mitigation and it is a requirement of this decision, not an implementation
detail beside it.

**An empty pack must also be distinguishable from an absent one.** When item 6
lands the first native call site, every organization provisioned before it holds
a pack that covers no roles, and under §6's no-silent-upgrade rule moving them
is a deliberate operator act — the verb D11 builds *here* so that it exists
before it is needed. *"This pack declares no roles"* and *"no pack resolved"*
have different remedies — select a newer built-in version versus seed a
selector — so they are different typed refusals from the start (D8).

### D2. One loader path, `fs.FS`, and the fixtures travel it

The built-in pack loader takes an `fs.FS`. Production supplies an `embed.FS`;
tests supply a non-empty `fstest.MapFS`. Both pass through the **identical**
loader, digest computation, validation gate, and persistence path.

This is what stops D1's empty built-in from being the only thing that path ever
carries. A test that reached the validators through a separate entry point would
prove the validators and leave the production import untested, which is a
distinction easy to lose and expensive to discover: the mechanism ADR 0031 §6
specifies is *the binary carries the built-in pack; provisioning imports it*,
and if that traversal only ever runs empty then nothing has demonstrated it.

The loader is therefore parameterized at the composition root, not selected by a
build tag or a test hook, so the injected path and the production path cannot
drift apart. The same injection gives D11's import-and-select verb its
non-vacuous proof: a *second* non-empty fixture built-in, selected over the
first, exercises the update path that a real upgrade will take.

### D3. `internal/prompt` sits above the plane, and the seam validates through a consumer-owned contract

The slot registry, the per-slot variable contract, the parser and the renderer
live in a new harness-side package, `internal/prompt`. The persisted pack
records stay in the data plane. The composition root supplies the registry;
tests may register fixture slots.

**The import gate must be unavoidable through the seam.** That is `configkeys`'
lesson: the seam consults the registry before every configuration write, and
that consultation is the entire difference between a governing registry and an
advisory one. A pack written around the gate is a pack the plane holds and
cannot render.

**But the seam must not import the harness package.** `configkeys` is pure
vocabulary — a `Key`, a `Scope`, and a one-method `Validator` the caller
implements — which is why `store` can import it at a cost of nothing.
`internal/prompt` is not that: it carries a template parser and the render
contract, which is harness machinery, and putting it under `store` would drag
the renderer into the plane's closure to buy a direct import.

So the contract is **consumer-owned**: `store.PromptContract` is declared in
`store`, implemented by `internal/prompt.Registry`, and supplied through
`plane.Composition` beside `Types` and `Keys`, with their semantics — *what
slots exist is a property of the caller's job.* Every pack write consults it;
the plane never names the implementation.

**The harness version travels the same way.** `pkg/version.Version` is the
binary's version — `"dev"` unless goreleaser's ldflags set it — and
`pkg/version` imports nothing, so the composition root reads it and supplies it
through `orchestrator.Config.MaestroVersion`. The Orchestrator never imports
`pkg/version`; the value arrives as configuration, which also lets tests inject a
real semver so D8's range check is exercised rather than skipped.

Both closure guards are **exact sets** rather than deny-lists
(`internal/dataplane/store/closure_test.go`, `internal/orchestrator/closure_test.go`),
so each addition is a deliberate edit to a named list. Under this shape `store`'s
set is unchanged and the Orchestrator's gains `internal/prompt`. **The resulting
counts are re-derived from the built API rather than asserted here**: what
`internal/prompt` itself imports decides the number, and a count written into a
design before the package exists is a prediction, not a measurement.

### D4. Two schemes in their own column, a rendered form that is not the storage form, and a query that cannot separate them

`canonical.Digest` returns a bare lowercase 64-hex SHA-256 over the RFC 8785
canonicalization (`internal/dataplane/canonical/canonical.go:63`). That is the
digest; the **scheme is a separate column**, per ADR 0031 §1, because an
`sha256:` prefix names an algorithm and not a semantic scheme.

| Scheme | Applies to | Produced by |
| --- | --- | --- |
| `pack-jcs-sha256-v1` | Packs the plane owns | `canonical.Digest` over the slot-key → entry-content object, and nothing else |
| `v1-manifest-sha256` | Imported v1 identities | Never computed here. Recorded as received, opaque, never rederived |

`pack-jcs-sha256-v1:<hex>` is a **rendered form** used in selectors and in
operator-facing output. It is not how the row stores the pair, and nothing
parses a stored digest to discover its scheme.

**No comparison, grouping, or equality claim crosses schemes — and the seam's
query surface has to make that unexpressible, not merely say it.** Today
`MPHQuery` selects on `PromptHash *string` alone and `ListPrincipalInstancesByPromptHash`
(`queries/principal_instances.sql:83`) filters on `prompt_hash` with no scheme.
Left as-is, D4 is prose. So:

- `MPHQuery.PromptHash` is replaced by `PromptIdentity *store.PromptIdentity`,
  a struct of `Scheme` and `Digest` that cannot be constructed with one half
  missing at the seam.
- The query filters on both columns, and the supporting index is
  `(organization_id, prompt_pack_scheme, prompt_hash)`.
- The importer's integration tests that exercise the query today
  (`import_integration_test.go:892`, `:915`) move to the pair.

The projection contains the entries and nothing else — no name, no declared
range, no organization — so identical prompt content computes to one identity
wherever it is installed, while the rows stay organization-scoped and unique on
`(organization, scheme, digest)`.

**JCS canonicalizes the container; entry text is hashed byte for byte.** A
template's leading spaces and line breaks are part of what a model receives.

### D5. The `principal_instances` split: an origin discriminator, a total migration, and a dispatch-bound principal path

`prompt_pack_id` is a nullable `text` with no table behind it
(`migrations/000004_principal_instances.up.sql:29`), doing three jobs: naming a
pack, identifying its content, and standing in for a reference to a record the
plane will hold. Migration `000023` separates them.

#### The columns and the constraint that makes the two cases a schema fact

The first draft said the seam would keep *nullable-because-imports* from
becoming *nullable-in-general*, and named the wrong seam verb to do it. Review
pointed out that the dispatch-resolution writer never creates principals and
`CreatePrincipalInstance` carries no dispatch or execution identity, so it
cannot tell a live principal from an import. The fix is to stop asking the seam
to remember which case it is in and make the case a column:

| Column | Meaning |
| --- | --- |
| `prompt_pack_origin` | `resolved` or `foreign`. **The discriminator.** |
| `prompt_pack_name` | The installation's label as it stood at resolution. Human handle; never a selector, never a comparison key |
| `prompt_hash` | Unchanged bytes. ADR 0025's run-record contract is untouched |
| `prompt_pack_scheme` | The scheme that produced the digest |
| `prompt_pack_content_id` | The plane's content record |
| `prompt_pack_installation_id` | The governing installation |
| `prompt_pack_installation_revision` | Which revision governed |
| `prompt_pack_metadata_snapshot` | Every metadata value that affected the decision |

Three check constraints:

1. **Agent-ness**: `(kind = 'agent') = (origin, name, hash and scheme are all NOT NULL)`.
   Today's schema ties only `agent_type` to the kind (`000004:51`), so an agent
   principal may carry partial or absent MPH fields; ADR 0031 §2 says name and
   digest are *always present for an agent principal*, and this is where that
   becomes true.
2. **Resolved**: `origin = 'resolved'` ⇔ the four reference columns are all
   NOT NULL.
3. **Foreign**: `origin = 'foreign'` ⇔ the four reference columns are all NULL.

Together they say what the first draft only promised: a row either carries the
whole plane-owned reference set or none of it, and which one is stated on the
row rather than inferred from who wrote it. The references are composite on
`organization_id` (000003's whole-tuple rule), so a resolved principal cannot
name another organization's installation.

#### The two writers, and why they are two

- **`CreatePrincipalInstance`** — the general path — **refuses `origin = 'resolved'`**.
  It writes humans, system principals, and foreign agent principals. That is
  what the importer uses, and it now sets `origin = 'foreign'` and
  `scheme = 'v1-manifest-sha256'` on what it writes (`benchmarkimport/import.go:700`).
  ADR 0031 is explicit that the importer stops being unchanged.
- **`CreateDispatchedPrincipalInstance`** — the dispatch-bound path — takes an
  execution identity, **loads the persisted resolution** (D8) for that
  execution's dispatch, and **copies** its name, scheme, digest, content id,
  installation id, revision and snapshot onto the principal. The caller cannot
  supply those fields; they are not in its input. So a live principal cannot
  disagree with the dispatch it runs under, because it never had the chance to.

Item 6 is the first caller of the second verb; item 4 builds it, because it is
the only way the `resolved` origin can be written and the schema's whole point is
that it cannot be written any other way. Its test runs it against a fixture
dispatch and proves the copy.

#### The migration is total, or it refuses

A migration that backfills the legacy scheme on some rows and leaves others in a
state the new constraints forbid does not apply. `000023` therefore follows
`000011`'s pattern — a `DO` block that counts the rows it cannot honestly
convert and raises with the remedy — and only then alters the table:

- **Backfilled**: every `kind = 'agent'` row with `prompt_pack_id` and
  `prompt_hash` both present gets `origin = 'foreign'`,
  `scheme = 'v1-manifest-sha256'`, and `name = prompt_pack_id`. Digest bytes are
  never rewritten.
- **Refused**: any `kind = 'agent'` row with either absent. There is no honest
  backfill — the row records a run whose P was never captured — and the
  migration will not invent one. This branch is reachable only by rows no
  current writer produces: the importer's validator requires both
  (`benchmarkimport/validate.go:186-189`), and nothing else writes agent
  principals today. It is a guard against a writer that does not exist, which
  is what a total migration has to be.
- **Non-agent rows** must carry none of the fields, which is already the case
  for every writer.

#### The down migration refuses rather than discards

`000023.down` **refuses** when any plane-owned state exists: a principal with
`origin = 'resolved'`, any `prompt_pack_installations` row, any
`dispatch_prompt_resolutions` row (D8), or any `configuration_records` row for
the `prompt.pack` key (D7). Each is a reference the pre-000023 schema cannot
hold, and a down migration that dropped them would leave either dangling
selectors or principals whose P silently reverted to a name. With none present
it collapses the columns back into `prompt_pack_id` and drops the tables.

### D6. Immutable content by trigger, a mutable installation with a governed installer, and one atomic install

Two tables, because the record has two lifetimes.

**`prompt_pack_contents`** — unique on `(organization_id, scheme, digest)`,
holding the entries. `UNIQUE` stops a second write of the same identity; it does
**not** stop an `UPDATE` rewriting entries and digest together in place, which
review was right to call out. So the table carries a `BEFORE UPDATE` trigger
that raises. **This is the first trigger in the schema.** Phase 2's
"immutability as a constraint" was creation-time uniqueness, and
`management_artifacts` is immutable by the absence of an update query — which
is a convention, and one an `UPDATE` through `psql` does not respect. A content
row's identity *is* its content, so a rewrite is not an edit but a lie with the
same primary key, and that is worth the schema's first trigger. Deletion is left
to the composite `ON DELETE RESTRICT` references from installations, resolutions
and principals: unreferenced content may go; referenced content cannot.

**`prompt_pack_installations`** — references a content row and carries
everything mutable: display name, declared Maestro version range as `min`
(inclusive) and `max` (exclusive) semver strings (D8 gives them their
semantics), declared role coverage, installer identity, and a **monotonic
revision**.

**At most one installation per `(organization_id, content_id)`**, as a unique
constraint. Without it a selector naming a digest becomes ambiguous the moment
two installations of the same content disagree about their declared metadata —
the defect ADR 0031 closed for names, reappearing one level down.

#### The installer is a governed source, not always a user

Organization provisioning runs before any user exists — `BootstrapOrganizationInput`
is a slug and a display name — so "who installed it" has no user to name for
the built-in. Three options were considered: require an acting user
(provisioning would then need one it does not have), create a system principal
instance per provisioning act (the importer's precedent, `system-benchmark-importer`,
but a principal instance is a *lifetime*, and an install is not one), or record
a **governed source identity**. The third:

| Column | Constraint |
| --- | --- |
| `installed_by_kind` | `builtin` or `user` |
| `installed_by_user_id` | composite FK to `users`; NOT NULL ⇔ kind is `user` |
| `builtin_maestro_version` | the binary version that carried the content; NOT NULL ⇔ kind is `builtin` |

That answers "where did this come from" for both cases without inventing an
actor for one of them. An operator-supplied pack (a later item's user-facing
install) records the user; the built-in records the binary.

#### One atomic install, because the checks span both tables

Review's fifth finding: a standalone content write cannot validate declared-role
coverage, because the roles are declared on the installation; and two
independent public writes would let content commit with no installation, which
is a row nothing can select. So the write surface is:

- **`InstallPromptPack`** — takes entries **and** installation metadata, runs
  the whole D10 gate through the contract, and inserts content and installation
  in one transaction. Content insertion is **internal** to this verb. Idempotent
  by identity: re-installing identical content with identical metadata returns
  the existing installation with `Created = false`; identical content with
  differing metadata is a `BootstrapConflict`, because the cardinality rule
  above means there is exactly one installation to disagree with.
- **`UpdatePromptPackInstallation`** — metadata only, **conditional on the
  revision**, returning a typed conflict on `ErrConfigurationConflict`'s
  precedent. The validators run here too: changing a declared coverage or range
  can make an installation unusable exactly as creating a bad one can.

There is no public content write and no unconditional installation update.

**No version label.** ADR 0031 §1 removed it: a label without a defined ordering
cannot be selected on and cannot be compared. Version labels and their ordering
arrive with the registry semantics.

### D7. The selector is a configuration key, and it is the registry's first live reader

`orchestrator.Keys()` stops returning an empty registry and registers one key.

| Property | Value | Why |
| --- | --- | --- |
| Key | `prompt.pack` | Matches `configkeys`' canonical pattern: lowercase dotted segments, each starting with a letter |
| Permitted scopes | organization, product, repository | §4's precedence is most-specific-wins over the whole lineage. Review confirmed: `ResolveConfiguration` already reads the whole repository → primary Product → organization chain, and the generic writer already supports each scope, so permitting all three registers nothing that lacks machinery |
| Sensitive | no | A selector is not a credential |
| Schema | a selector object | Validated below |

**The value is a selector, never a name.** §1 makes the name a non-unique label
and makes every edit a new version, so a name cannot deterministically identify
one version. The registered schema therefore admits exactly two shapes — a
content record reference, or a scheme-qualified digest in D4's rendered form —
and **refuses a bare name explicitly**, with an error saying that a name is a
label, so the failure teaches the rule rather than reporting a parse error.

This is the live reader item 3's amendment 3 assigned here, and it satisfies the
plan's *"configuration and secrets acquire their first consumer"* for the
configuration half. It is a real reader: D8 resolves through
`ResolveConfiguration` on the ordinary path, not through a fixture write.

### D8. Resolution happens once at dispatch, with defined version semantics, and pre-000023 dispatches are refused rather than special-cased

Resolution runs inside `CreateDispatch`, in the transaction that derives the
basis, and its inputs are §4's in precedence order:

1. **An explicit selector carried on the dispatch.**
2. **Scoped configuration**, via `ResolveConfiguration(ctx, organizationID, repositoryID, "prompt.pack")`.
   The repository is reachable: `Epic.RepositoryID` is a column
   (`store/work.go`), and a Story's Epic is already read to derive the basis.
3. **Failing both, dispatch fails.**

**The lineage limit is named rather than discovered.** `configuration_records`'
scope arc has no Epic or Story level, so an Epic- or Story-scoped pack choice is
not expressible as configuration and must arrive as an explicit selection on the
dispatch. Sufficient for Phase 3; stated so nobody looks for it.

#### Version semantics, because "dev" is what every local build says

The first draft counted the declared-range check as the one non-vacuous gate at
item 4 and did not say what a version *was*. It is `pkg/version.Version`,
supplied through `Config.MaestroVersion` (D3), and outside a goreleaser build
its value is the string `"dev"`. So:

- **The range is two semver bounds**, `min` inclusive and `max` exclusive,
  stored as strings on the installation, compared under semver precedence
  including prerelease ordering — which the phase ladder needs, since
  `v2.0.0-phase.3.0.0` must sort below `v2.0.0-phase.4.0.0`. No range DSL.
  The comparator is a library whose prerelease behaviour is **verified against
  the pinned version's source before it enters code**, per the verification
  discipline; `golang.org/x/mod/semver` is the candidate, and `go.mod` carries
  no semver dependency today.
- **The built-in declares the Phase 3 band**: `[v2.0.0-phase.3.0.0, v2.0.0-phase.4.0.0)`.
  Bounded, real, and the range a tagged Phase 3 binary satisfies.
- **A development build is not compared.** A version that is not valid semver —
  `"dev"` — makes the range check `not-evaluated`, recorded as such on the
  resolution beside the version string. It does not pass and it does not
  refuse: a pass would assert a comparison that never ran, which is
  [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md)'s
  reported-as-zero mistake, and a refusal would make every local build
  undispatchable.
- **Tests inject a real semver**, so the refuse branch and the pass branch are
  both exercised, and a third test proves that `"dev"` records `not-evaluated`
  rather than either.

**The declared range can refuse and never authorize.** A pack declaring itself
compatible has made a claim, not passed a check.

#### The typed refusals

`DispatchReason` gains constants for the refusals that have a producer, and only
those. A reason no code can emit is the same guess about a future caller that
D1's slot rule refuses.

| Refusal | Producer in item 4? | Disposition |
| --- | --- | --- |
| No selector: neither the dispatch nor any scope supplies one | **Yes** | Reachable and tested |
| Unresolved selector: names no plane-owned version in this organization | **Yes** | Reachable and tested — a digest from another organization, or a content id that does not exist |
| Declared range incompatible with the running Maestro version | **Yes** | Reachable and tested under an injected semver; `not-evaluated` under `"dev"` |
| Execution's roles outside the declared coverage | **No — no subject** | Item 6's (D11). `store.Execution` carries identity and authority only; resolved configuration is items 5/6's |
| Harness-contract re-run fails after a version move | Structurally, yes | Runs at dispatch; **vacuous over the empty built-in**, so its non-vacuous proof comes from D2's fixture packs through the same path |

#### Where the resolution is persisted, and what it records

ADR 0031 §4 writes the resolved values "into the invocation", but the invocation
schema is ADR backlog candidate 13 and does not exist; item 2 deliberately gave
executions **identity and authority only**. So the resolution lands beside the
thing that decided it: a **`dispatch_prompt_resolutions`** row, unique on the
dispatch, carrying the full dispatch lineage tuple as its composite key, the
resolved name, scheme, digest, content id, installation id, revision, the
metadata snapshot, **the Maestro version it was validated against**, and the
range check's result (`passed`, `refused`, `not-evaluated`). Every reference is
composite on `organization_id`.

Recording the validated-against version is what makes restart detection
possible without re-resolving P: a replacement agent compares the running
version with the recorded one and, if they differ, **re-runs parse and the
variable contract** — never resolution. Item 6 owns that re-check (D11); item 4
records what it needs.

It is written **in the same transaction as the dispatch**, and the seam refuses
to create a dispatch without it. A seam-enforced 1:1 is the existing pattern:
`AcceptDispatch` flips the disposition and creates the execution in one
transaction because *"an accepted dispatch has at least one execution, which is
the seam's half of item 2's invariant."*

#### Pre-000023 dispatches are refused by the migration

A separate table leaves a question the first draft did not answer: what does a
reader do with a dispatch that has no resolution row? If such rows can exist
legitimately, every reader must distinguish an honest legacy dispatch from a
future missing child caused by a defect, and the two look identical.

They cannot be told apart, so they are not allowed to coexist. **`000023`
refuses to apply when `story_dispatches` has any row**, on `000011`'s pattern,
with the remedy in the message: these rows predate any pack, no honest
resolution exists for them, and a local plane resets. With that guarantee every
reader that returns a `StoryDispatch` — `GetDispatch`, `ListDispatchesByDisposition`,
and `OpenWork`'s `OpenDispatch` — joins the resolution, and **absence is a typed
integrity error**, never a legacy state.

**Restart reuses the resolution and never re-resolves.** A replacement agent
resumes the same execution, which names the same dispatch, which carries the
resolved pack. If it re-resolved, a configuration edit between the crash and the
restart would move a factory lever mid-Story with nothing recording that it had
happened, and one Story would span two P values.

### D9. Organization provisioning does all three writes, or none

`ProvisionOrganizationPromptPack` joins the provisioning family on its existing
pattern — idempotent by natural key, `Bootstrapped[T]`, a typed
`BootstrapConflict` on differing supplied data — and performs ADR 0031 §6's two
steps as three writes **in one transaction**:

1. Import the built-in content into the organization, deriving its identity
   under `pack-jcs-sha256-v1` — through D6's `InstallPromptPack`, with
   `installed_by_kind = 'builtin'` and the binary version recorded.
2. That same call creates the installation record.
3. Write the organization-scoped `prompt.pack` configuration record naming that
   content.

**One transaction, because a partial success is precisely the failure the
checkpoint tests for.** An organization holding content and installation but no
configuration record has an unresolvable selector, which is indistinguishable at
dispatch from never having been provisioned — and it would be reached by an
ordinary retry, not by an exotic fault.

**It covers organizations that predate this work**, per §6's *"or first
initializing one that predates this work"*: the verb is idempotent, so running
it against an existing organization is the initialization path rather than a
separate migration. Nothing is written at deployment bootstrap, which has no
organization to write for.

**Upgrades move nothing.** An existing organization keeps its selector and does
not import a new built-in version until someone selects it — through D11's verb.
An organization provisioned after an upgrade seeds at the version the binary
then carries. Two organizations in one deployment can therefore default to
different packs by age alone — the price of never moving a lever silently.

### D10. The import gate, and what it can and cannot prove at item 4

Three checks at install, against the harness version that will run
(ADR 0031 §5): **coverage** of the roles the installation declares, **parse** of
every entry, and the **variable and render contract** — each slot's referenced
variables are ones the harness supplies for that slot, and nothing it requires
is missing.

That last check is the per-slot variable contract ADR 0031's Consequences call
*"new work this ADR creates"*, and it is new because v1 has no such thing:
`pkg/templates`' renderer takes one `TemplateData` carrying every field any
template might want, so nothing states which variables a given template may use.
`internal/prompt` declares the contract per slot, which is what makes the check
mean anything.

**Over the empty built-in all three pass vacuously**, and this design says so
rather than reporting three green checks. The non-vacuous proof is D2's:
fixture packs with real entries, real declared coverage, and deliberate faults
— a missing slot for a declared role, an unparseable entry, a reference to a
variable the slot does not supply — travelling the identical path, through
`InstallPromptPack` and through `UpdatePromptPackInstallation` both.

### D11. The import-and-select verb is built here; four obligations are assigned to the items that acquire their subjects

**Item 4 builds `dataplanectl prompt-pack select-builtin`.** Review's eighth
finding: without it, every organization provisioned during item 4 stays
non-executable with no owner for moving it. The verb is machinery, not
vocabulary, so it has a producer now — and its update path is provable now,
through D2's second fixture built-in:

1. Import the running binary's built-in into the organization through
   `InstallPromptPack` — idempotent by identity, so re-running against an
   organization already holding this version is a no-op.
2. **Update** the organization-scoped `prompt.pack` record to name it,
   **conditional on the record's version** (`UpdateConfigurationRecord` with the
   version read), returning `ErrConfigurationConflict` if somebody moved first.

One transaction. Item 6 then gains only the content the verb moves
organizations to, not the verb.

*"The first item that needs X"* is how an obligation becomes nobody's, which
this repository has paid for. Each of the rest is named:

| Obligation | Owner | Why there |
| --- | --- | --- |
| The first native slots and the **first executable built-in version** | **Item 6** (`agent-core`) | It is where the first native model call site exists, so it is the first item that can register a slot under D1's rule |
| The dispatch check that an execution's roles fall **within the declared coverage** | **Item 6** | It has the subject: before it, an execution carries no roles |
| The **restart-time compatibility re-check** — compare the running version with the resolution's recorded one; on a move, re-run parse and the variable contract without re-resolving P | **Item 6** | It is agent start; item 4 records what it reads (D8) |
| Standalone-reviewer and Claude Code adapter slots, for prompt material Maestro actually supplies | **Item 8** (`external-consumers`) | `pkg/templates/claude/*` is that adapter's content, and item 8 is where the adapter lands |
| Epic-planning and Architect slots | **Item 10** (`work-group-lifecycle`) | The Epic-level plan workflow is item 10's |

**Each later version is a complete replacement, never an overlay.** A pack
version supplies every slot it declares and inherits nothing (§1), so item 8's
built-in version contains item 6's slots plus its own, and item 10's contains
both plus its own. Overlays and inheritance are ADR backlog candidate 9.

This also gives the `pkg/templates` re-cut a home. The
[port inventory](../phase_0/inventory_v1-port.md) lists it as rework —
*"templates re-cut for v2 states"* — without naming an item; the rows above are
that assignment, split by which item acquires each call site.

## Amendments To The Phase Plan

Five, all requiring Codex and DR acceptance with this design.

1. **Item 4's built-in pack is empty.** The plan's item 4 line does not say how
   much content the built-in pack carries. This design settles it at none, for
   D1's reasons, and states the consequence: Checkpoint 1's *"resolvable
   prompt-pack selector"* is demonstrated in full, and executability is not
   claimed.
2. **Item 6 gains the first executable built-in version and the first native
   slots** (D11). The plan's item 6 line covers the agent core and ADR 0032's
   demoted mechanisms; it does not mention prompt content.
3. **Item 6 gains two dispatch-and-start checks** (D8, D11): roles within
   declared coverage, and the restart-time compatibility re-check. ADR 0031 §5
   makes both harness-facing; item 4 cannot host either because executions
   carry no roles and no agent starts.
4. **Items 8 and 10 gain their slot registrations** (D11), which is also the
   `pkg/templates` re-cut assignment the port inventory left unowned.
5. **Item 4 is L, not M.** The first draft was M-shaped. Review's nine findings
   added a total migration with a refusing down, an anti-update trigger, a
   compound install verb, a dispatch-bound principal verb, the scheme-qualified
   query rewrite, version semantics with a comparator dependency, and the
   import-and-select verb — each individually small, together the difference
   between a family and a family with its invariants enforced. The plan line's
   size is amended so that the checkpoint's schedule is honest.

None settles a question an Accepted ADR already answers; each assigns an
obligation ADR 0031 creates to the item that acquires its subject, or records a
cost the plan under-estimated.

## Implementation And Review Sequence

Reviewed as a sequence of local commits, in this order, because each step's
verification depends on the one before it.

1. **`internal/prompt`**: slot registry, per-slot variable contract, parser,
   renderer, and the entry projection that feeds the digest. No plane, no store.
2. **`store.PromptContract`**, `Config.MaestroVersion`, and the
   `plane.Composition` field (D3), with both closure guards re-derived and
   updated.
3. **Migration `000023`**: the totality guard and the dispatch-rows guard, the
   two pack tables with the anti-update trigger and composite keys, the
   `dispatch_prompt_resolutions` table, the `principal_instances` split with its
   origin discriminator and three checks, the legacy backfill, the scheme index,
   and the refusing down. sqlc regeneration.
4. **The pack family on the seam**: `InstallPromptPack` with the gate reached
   through the contract, `UpdatePromptPackInstallation` under the revision, the
   reads, and the `MPHQuery` rewrite (D4, D6).
5. **The built-in loader** over `fs.FS` (D2), with the fixture path proven
   first and the empty production embed second — in that order, so the path is
   known to carry content before it is asked to carry none.
6. **The selector key** and `orchestrator.Keys()` (D7).
7. **Resolution in `CreateDispatch`** with the semver comparator, the three
   typed refusals, the `not-evaluated` branch, and the resolution row; the
   dispatch readers joining it (D8).
8. **`CreateDispatchedPrincipalInstance`** and the refusal of `resolved` on the
   general path (D5).
9. **`ProvisionOrganizationPromptPack`** and `select-builtin` (D9, D11), with
   the `dataplanectl` verbs.
10. **The importer's origin and legacy scheme** (D5).

## Testing And Verification

Per [Defect-Shaped Verification](../process_build.md#defect-shaped-verification),
every guard below is proven by restoring the exact defect it claims to catch and
showing the named test fails at the intended assertion. Counts are not the
report; the protected defect is.

**Mutants planned, with the defect each protects against:**

| Mutation | Must fail |
| --- | --- |
| Drop the scheme from the MPH query so it filters on hex alone | A `v1-manifest-sha256` identity groups with a `pack-jcs-sha256-v1` pack — arithmetic on unrelated hashes |
| Include the pack name in the digest projection | Two organizations holding identical content compute different identities; correcting a typo mints a new pack |
| Drop the anti-update trigger, then `UPDATE` a content row's entries and digest through raw pgx | A content record rewritten in place under its original primary key |
| Relax the resolved/foreign check so the four reference columns are independently nullable | A row records which installation governed without recording what it said |
| Let the general `CreatePrincipalInstance` accept `origin = 'resolved'` | A live principal whose pack fields were caller-supplied and never checked against its dispatch |
| Have `CreateDispatchedPrincipalInstance` take pack fields from its input instead of the resolution | Same defect from the other side: the copy becomes a claim |
| Remove the migration's totality guard and plant an agent row with a null `prompt_hash` | The migration applies and the new agent-ness constraint is violated by a row it left behind — **the mutant must die at the constraint, not at the guard**, so the planted row goes in *after* the guard would have run |
| Remove the dispatch-rows guard and plant a pre-migration dispatch | A dispatch with no resolution row that every reader must then special-case |
| Split `InstallPromptPack` into two transactions and fail between | Committed content with no installation: a row nothing can select |
| Let the seam create a dispatch without the resolution row | A dispatch exists whose P is unrecoverable |
| Make provisioning's three writes three transactions | An organization with content and installation but no selector: unresolvable, reached by an ordinary retry |
| Make `select-builtin`'s configuration update unconditional | Last-writer-wins on the selector — the ADR 0027 defect by name |
| Accept a bare name as a selector value | A selector that cannot deterministically identify one version |
| Skip the contract on installation **update** | An installation valid when written is unusable after an edit |
| Make `"dev"` pass the range check instead of recording `not-evaluated` | A comparison asserted that never ran |
| Have resolution re-resolve on restart | One Story spans two P values |
| Route the fixture packs around the production loader | The binary-carries-it-and-provisioning-imports-it path is never exercised over content |

The last row is the mutation this design most wants run, because it is the one
D1 creates: it is the check that D2 actually holds, and if it passes with the
fixtures diverted then every other green in this item is over an empty set. The
totality-guard row is the one most likely to be proven for the wrong reason —
this repository has shipped a mutant that died on a neighbouring check before —
so its note about *where* it must die is part of the plan, not commentary.

**Positive controls.** Each refusal test is paired with a valid pack that
resolves through the same path, so a refusal caused by an unrelated validation
rule is distinguishable from the one under test.

**Integration.** The provisioning-to-resolution path, the trigger, both
migration guards, and the down migration's refusal run against a real plane.
`internal/dataplane/stack` integration needs `-timeout=40m`; the default
ten-minute alarm reads as a failure with no failing test.

## Points Resolved In Review

**Round 1 — Codex, 2026-09-03. Nine P1s, all accepted after verification
against the tree.**

1. *D5 named the wrong enforcement point*: the dispatch-resolution writer never
   creates principals, and `CreatePrincipalInstance` has no dispatch identity to
   distinguish a live principal from an import. → The origin discriminator and
   its three checks; `CreateDispatchedPrincipalInstance` copies the persisted
   resolution; the general path refuses `resolved` (D5).
2. *Scheme-qualified comparison was prose*: `MPHQuery` and its SQL filtered on
   `prompt_hash` alone. → `PromptIdentity{Scheme, Digest}`, the paired query,
   and the composite index (D4).
3. *Migration 000023 was not total*: agent rows with partial MPH fields are
   admissible today, so a backfill alone could not establish the invariants. →
   The totality guard on `000011`'s pattern, the agent-ness constraint, composite
   tenancy keys, and a down that refuses rather than discards (D5). Verified
   that the refusal branch is reachable only by rows no current writer produces.
4. *Content immutability had no mechanism*: `UNIQUE` prevents a second identity,
   not an in-place rewrite. → The anti-update trigger, named as the schema's
   first, with the raw-`UPDATE` mutant (D6).
5. *The write surface needed one compound operation*: standalone content writes
   cannot validate coverage and admit committed uninstalled content. →
   `InstallPromptPack` with content insertion internal; metadata updates
   separate and conditional (D6).
6. *"Who installed it" had no representable actor* for a fresh organization. →
   The governed source identity — `builtin` with the binary version, or `user`
   with a composite FK — and the two alternatives recorded as rejected (D6).
7. *Version compatibility was underspecified*: the runtime version is `"dev"`,
   no range semantics existed, and nothing recorded the validated-against
   version. → Semver bounds with prerelease ordering, the Phase 3 band,
   `not-evaluated` for development builds, the version supplied through
   `Config`, and the resolution recording what it validated against (D3, D8).
8. *Existing organizations had no owner for leaving the empty pack.* → The
   `select-builtin` verb is built in item 4 with a conditional selector update;
   item 6 gains only the content (D11).
9. *Pre-000023 dispatches were undefined*: readers could not tell a legacy
   dispatch from a missing child caused by a defect. → The migration refuses
   existing dispatches, so absence is an integrity error everywhere (D8).

Codex's calls on the first draft's open questions, all taken: keep
`dispatch_prompt_resolutions`; a real bounded Phase 3 range with explicit
development-version behaviour; all three configuration scopes now. The repeated
clause in D6 is gone, and the size is amendment 5.

## Open Questions

1. **Is a trigger acceptable as the immutability mechanism, given it is the
   schema's first?** D6 argues the alternative — convention, as
   `management_artifacts` has today — is what review just rejected. If the
   answer is no, the honest fallback is a seam-only guarantee stated as
   uncovered, not a `UNIQUE` dressed as one.
2. **`golang.org/x/mod/semver` as the comparator**, subject to the source
   verification D8 requires before it enters code. If its prerelease ordering
   does not match the phase ladder, the fallback is a small in-tree comparator
   over the ladder's fixed shape — which is less code than it sounds and more
   code than anyone wants to own.

## Related Documents

- [ADR 0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md) — the
  whole specification.
- [Phase 3 scope and plan](plan_scope.md) — item 4 and Checkpoint 1.
- [Item 3 design](design_orchestrator-seam.md) — D7 (the registry threaded, the
  live reader assigned here), D11 (provisioning shaped for this item), D2/D3
  (the closure and the composition root).
- [Item 2 design](design_work-hierarchy.md) — the dispatch record this
  resolution is written beside.
- [Port inventory](../phase_0/inventory_v1-port.md) — `pkg/templates` as rework,
  assigned by D11.
