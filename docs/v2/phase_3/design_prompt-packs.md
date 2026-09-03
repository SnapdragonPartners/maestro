+++
title = "Design: Prompt Pack Identity, Storage, And Resolution (Item 4)"
edit_date = "2026-09-03"
status = "draft"
summary = "Mini-plan for Phase 3 item 4: the prompt-pack family built whole — immutable content records under a scheme-qualified digest beside mutable installation records carrying a monotonic revision, the import gate reached through a consumer-owned contract so the seam validates every pack write without the plane importing a renderer, a selector configuration key that is the key registry's first live reader, resolution once at dispatch persisted beside the basis, and organization provisioning that imports the built-in pack and seeds its selector in one transaction. The built-in pack ships EMPTY and declares no role coverage, because item 4 has no model caller and neither candidate slot survived inspection: v1 has exactly one system prompt, the Architect's, and it is bound to v1's workspace and tool contracts. Resolvable but not executable is the honest state, so the loader takes an fs.FS and the non-vacuous proof comes from fixtures travelling the identical path. Carries the principal_instances three-roles-in-one-column split, the importer's legacy-scheme backfill, and four amendments assigning the slot vocabulary and the roles-coverage check to the items that acquire their subjects."
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
(optimistic concurrency on the installation revision), and
[ADR 0019](../../adr/0019-orchestrator-boundary.md) (selecting a pack from
configuration is rules, not judgment, so resolution is Orchestrator machinery).

The plan's item 4 line requires ADR 0031's storage and resolution, *"organization
provisioning seeding the scoped selector — which completes item 3's provisioning
rather than amending it later"*, the `principal_instances.prompt_pack_id`
three-roles-in-one-column correction, and the `"v1-embedded"` foreign-pack case.
Item 3's amendment 3 assigns configuration's **first live consumer** here.

## Scope

**In.** The pack content and installation records and their constraints; both
identity schemes; the import validation gate and the harness-side slot registry
it runs; the built-in pack loader; the selector configuration key and its
registration by the Orchestrator; resolution at dispatch with its typed
refusals; the resolution snapshot persisted beside the dispatch basis;
organization provisioning of content, installation and selector in one
transaction; the `principal_instances` column split with its legacy-scheme
backfill; and the importer change that sets the legacy scheme on what it writes.

**Out.** Everything [ADR 0031's Deferred section](../../adr/0031-prompt-pack-identity-resolution-and-storage.md)
defers, and three things this design assigns explicitly rather than leaving
unowned (D11): the production slot vocabulary, the first executable built-in
pack version, and the dispatch check that an execution's roles fall within the
pack's declared coverage. All three want a subject item 4 does not have.

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
is a deliberate operator act. *"This pack declares no roles"* and *"no pack
resolved"* have different remedies — select a newer built-in version versus seed
a selector — so they are different typed refusals from the start (D8).

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
drift apart.

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

Both closure guards are **exact sets** rather than deny-lists
(`internal/dataplane/store/closure_test.go`, `internal/orchestrator/closure_test.go`),
so each addition is a deliberate edit to a named list. Under this shape `store`'s
set is unchanged and the Orchestrator's gains `internal/prompt`. **The resulting
counts are re-derived from the built API rather than asserted here**: what
`internal/prompt` itself imports decides the number, and a count written into a
design before the package exists is a prediction, not a measurement.

### D4. Two schemes, in their own column, and a rendered form that is not the storage form

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

**No comparison, grouping, or equality claim crosses schemes.** The projection
contains the entries and nothing else — no name, no declared range, no
organization — so identical prompt content computes to one identity wherever it
is installed, while the rows stay organization-scoped and unique on
`(organization, scheme, digest)`.

**JCS canonicalizes the container; entry text is hashed byte for byte.** A
template's leading spaces and line breaks are part of what a model receives.

### D5. The `principal_instances` split, and why only imports may omit the references

`prompt_pack_id` is a nullable `text` with no table behind it
(`migrations/000004_principal_instances.up.sql:29`), doing three jobs: naming a
pack, identifying its content, and standing in for a reference to a record the
plane will hold. Migration `000023` separates them:

| Column | Null? | Meaning |
| --- | --- | --- |
| `prompt_pack_name` | agent-only | The installation's label as it stood at resolution. Human handle; never a selector, never a comparison key |
| `prompt_hash` | agent-only | Unchanged bytes. ADR 0025's run-record contract is untouched |
| `prompt_pack_scheme` | agent-only | The scheme that produced the digest. **Backfilled to `v1-manifest-sha256`** on rows already imported |
| `prompt_pack_content_id` | **nullable** | The plane's content record |
| `prompt_pack_installation_id` | **nullable** | The governing installation |
| `prompt_pack_installation_revision` | **nullable** | Which revision governed |
| `prompt_pack_metadata_snapshot` | **nullable** | Every metadata value that affected the decision |

The backfill is an `UPDATE` inside the same migration that creates the family,
not a second migration, and **digest bytes are never rewritten**.

**The four nullable columns are all-or-none**, enforced by a check constraint. A
row carrying an installation id and no revision would record which mutable
record governed without recording *what it said*, which is the defect §1 closes
one level down. A **foreign pack** — the `"v1-embedded"` case the importer
writes (`benchmarkimport/import.go:700`) — carries name, digest and legacy
scheme with all four absent, and says so.

**Nullable-because-imports is not nullable-in-general**, and the schema cannot
tell the two apart because it cannot see who is writing. So the seam enforces
it: the dispatch-resolution writer refuses an agent principal for a live
dispatch without all four, while the importer's path admits their absence. This
is the same division item 3 drew for provisioning — *"the importer resolves with
the reader and never provisions"* — and it is stated here rather than left to
the reader of a nullable column.

The importer additionally **sets the legacy scheme** on what it writes. ADR 0031
is explicit that the importer stops being unchanged, and this design does not
claim otherwise.

### D6. Immutable content, a mutable installation, and the cardinality that keeps a selector unambiguous

Two tables, because the record has two lifetimes:

- **`prompt_pack_contents`** — immutable, unique on `(organization_id, scheme, digest)`,
  holding the entries. Nothing in it can be corrected because there is nothing
  in it that is not its content. Making immutability a constraint rather than a
  convention is the Phase 2 pattern, for the Phase 2 reason: read-then-write is
  two statements.
- **`prompt_pack_installations`** — references a content row and carries
  everything mutable: display name, declared Maestro version range, declared
  role coverage, who installed it and when, and a **monotonic revision**.

**At most one installation per `(organization_id, content_id)`**, as a unique
constraint. Without it a selector naming a digest becomes ambiguous the moment
two installations of the same content disagree about their declared coverage —
the defect ADR 0031 closed for names, reappearing one level down.

**Updates use optimistic concurrency on the revision** and return a typed
conflict, following `ErrConfigurationConflict`'s precedent: a rowcount carries no
reason, and a caller re-reads on a conflict while giving up on a missing row.

**The validators run on update, not only on creation.** Changing a declared role
coverage or version range can make an installation unusable exactly as creating
a bad one can.

**No version label.** ADR 0031 §1 removed it: a label without a defined ordering
cannot be selected on and cannot be compared, so its only effect is to look
actionable. Version labels and their ordering arrive with the registry
semantics.

### D7. The selector is a configuration key, and it is the registry's first live reader

`orchestrator.Keys()` stops returning an empty registry and registers one key.

| Property | Value | Why |
| --- | --- | --- |
| Key | `prompt.pack` | Matches `configkeys`' canonical pattern: lowercase dotted segments, each starting with a letter |
| Permitted scopes | organization, product, repository | §4's precedence is most-specific-wins over the whole lineage; a key permitted at fewer levels would make that rule partly unexpressible |
| Sensitive | no | A selector is not a credential |
| Schema | a selector object | Validated below |

**The value is a selector, never a name.** §1 makes the name a non-unique label
and makes every edit a new version, so a name cannot deterministically identify
one version. The registered schema therefore admits exactly two shapes — a
content record reference, or a scheme-qualified digest — and **refuses a bare
name explicitly**, with an error saying that a name is a label, so the failure
teaches the rule rather than reporting a parse error.

This is the live reader item 3's amendment 3 assigned here, and it satisfies the
plan's *"configuration and secrets acquire their first consumer"* for the
configuration half. It is a real reader: D8 resolves through
`ResolveConfiguration` on the ordinary path, not through a fixture write.

### D8. Resolution happens once at dispatch, and three of five refusals have a producer today

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

#### The typed refusals

`DispatchReason` gains constants for the refusals that have a producer, and only
those. A reason no code can emit is the same guess about a future caller that
D1's slot rule refuses.

| Refusal | Producer in item 4? | Disposition |
| --- | --- | --- |
| No selector: neither the dispatch nor any scope supplies one | **Yes** | Reachable and tested |
| Unresolved selector: names no plane-owned version in this organization | **Yes** | Reachable and tested — a digest from another organization, or a content id that does not exist |
| Declared range incompatible with the running Maestro version | **Yes** | Reachable and tested: the built-in installation carries a real range, so this is the one §5 gate that is non-vacuous over an empty pack |
| Execution's roles outside the declared coverage | **No — no subject** | Item 6's (D11). `store.Execution` carries identity and authority only; resolved configuration is items 5/6's |
| Harness-contract re-run fails after a version move | Structurally, yes | Runs at dispatch; **vacuous over the empty built-in**, so its non-vacuous proof comes from D2's fixture packs through the same path |

**The declared range can refuse and never authorize.** A pack declaring itself
compatible has made a claim, not passed a check.

#### Where the resolution is persisted

ADR 0031 §4 writes the resolved values "into the invocation", but the invocation
schema is ADR backlog candidate 13 and does not exist; item 2 deliberately gave
executions **identity and authority only**, with resolved configuration demoted
to items 5/6. So the resolution lands beside the thing that decided it: a
`dispatch_prompt_resolutions` row, unique on the dispatch, carrying the resolved
name, scheme, digest, content id, installation id, revision, and the metadata
snapshot.

It is written **in the same transaction as the dispatch**, and the seam refuses
to create a dispatch without it. That a 1:1 invariant of this shape is enforced
by the seam rather than by the schema is the existing pattern, not a new
weakening: `AcceptDispatch` already flips the disposition and creates the
execution in one transaction because *"an accepted dispatch has at least one
execution, which is the seam's half of item 2's invariant."*

A separate table rather than columns on `story_dispatches`, because NOT NULL
columns would need a backfill value for dispatches created before packs existed
and there is no honest one — no pack was resolved for them. A nullable column
set would re-open the nullable-in-general hole D5 closes.

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
   under `pack-jcs-sha256-v1`.
2. Create its installation record.
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
not import a new built-in version until someone selects it. An organization
provisioned after an upgrade seeds at the version the binary then carries. Two
organizations in one deployment can therefore default to different packs by age
alone — the price of never moving a lever silently, and something item 6's first
executable version will make concrete rather than hypothetical.

### D10. The import gate, and what it can and cannot prove at item 4

Three checks at import, against the harness version that will run
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
variable the slot does not supply — travelling the identical path.

### D11. Four downstream obligations, assigned to items rather than described

*"The first item that needs X"* is how an obligation becomes nobody's, which
this repository has paid for. Each is named:

| Obligation | Owner | Why there |
| --- | --- | --- |
| The first native slots and the **first executable built-in version** | **Item 6** (`agent-core`) | It is where the first native model call site exists, so it is the first item that can register a slot under D1's rule |
| The dispatch check that an execution's roles fall **within the declared coverage** | **Item 6** | It has the subject: before it, an execution carries no roles |
| Standalone-reviewer and Claude Code adapter slots, for prompt material Maestro actually supplies | **Item 8** (`external-consumers`) | `pkg/templates/claude/*` is that adapter's content, and item 8 is where the adapter lands |
| Epic-planning and Architect slots | **Item 10** (`work-group-lifecycle`) | The Epic-level plan workflow is item 10's |

**Each later version is a complete replacement, never an overlay.** A pack
version supplies every slot it declares and inherits nothing (§1), so item 8's
built-in version contains item 6's slots plus its own, and item 10's contains
both plus its own. Overlays and inheritance are ADR backlog candidate 9.

This also gives the `pkg/templates` re-cut a home. The
[port inventory](../phase_0/inventory_v1-port.md) lists it as rework —
*"templates re-cut for v2 states"* — without naming an item; the three rows
above are that assignment, split by which item acquires each call site.

## Amendments To The Phase Plan

Four, all requiring Codex and DR acceptance with this design.

1. **Item 4's built-in pack is empty.** The plan's item 4 line does not say how
   much content the built-in pack carries. This design settles it at none, for
   D1's reasons, and states the consequence: Checkpoint 1's *"resolvable
   prompt-pack selector"* is demonstrated in full, and executability is not
   claimed.
2. **Item 6 gains the first executable built-in version and the first native
   slots** (D11). The plan's item 6 line covers the agent core and ADR 0032's
   demoted mechanisms; it does not mention prompt content.
3. **Item 6 gains the roles-within-declared-coverage dispatch check** (D8, D11).
   ADR 0031 §5 makes the check dispatch-time; item 4 cannot host it because
   executions carry no roles until items 5/6 give them a resolved configuration.
4. **Items 8 and 10 gain their slot registrations** (D11), which is also the
   `pkg/templates` re-cut assignment the port inventory left unowned.

None settles a question an Accepted ADR already answers; each assigns an
obligation ADR 0031 creates to the item that acquires its subject.

## Implementation And Review Sequence

Reviewed as a sequence of local commits, in this order, because each step's
verification depends on the one before it.

1. **`internal/prompt`**: slot registry, per-slot variable contract, parser,
   renderer, and the entry projection that feeds the digest. No plane, no store.
2. **`store.PromptContract`** and the `plane.Composition` field (D3), with both
   closure guards re-derived and updated.
3. **Migration `000023`**: the two pack tables, the `dispatch_prompt_resolutions`
   table, the `principal_instances` column split, and the legacy-scheme
   backfill. sqlc regeneration.
4. **The pack family on the seam**: content and installation reads and writes,
   the import gate reached through the contract, the revision's optimistic
   concurrency.
5. **The built-in loader** over `fs.FS` (D2), with the fixture path proven
   first and the empty production embed second — in that order, so the path is
   known to carry content before it is asked to carry none.
6. **The selector key** and `orchestrator.Keys()` (D7).
7. **Resolution in `CreateDispatch`** with its three typed refusals and the
   resolution row (D8).
8. **`ProvisionOrganizationPromptPack`** (D9), and the `dataplanectl` verb.
9. **The importer's legacy scheme** (D5).

## Testing And Verification

Per [Defect-Shaped Verification](../process_build.md#defect-shaped-verification),
every guard below is proven by restoring the exact defect it claims to catch and
showing the named test fails at the intended assertion. Counts are not the
report; the protected defect is.

**Mutants planned, with the defect each protects against:**

| Mutation | Must fail |
| --- | --- |
| Drop the scheme from the digest comparison so equality is on hex alone | A `v1-manifest-sha256` identity groups with a `pack-jcs-sha256-v1` pack — arithmetic on unrelated hashes |
| Include the pack name in the digest projection | Two organizations holding identical content compute different identities; correcting a typo mints a new pack |
| Make the four `principal_instances` reference columns independently nullable | A row records which installation governed without recording what it said |
| Let the seam create a dispatch without the resolution row | A dispatch exists whose P is unrecoverable |
| Make provisioning's three writes three transactions | An organization with content and installation but no selector: unresolvable, reached by an ordinary retry |
| Accept a bare name as a selector value | A selector that cannot deterministically identify one version |
| Skip the contract on installation **update** | An installation valid when written is unusable after an edit |
| Have resolution re-resolve on restart | One Story spans two P values |
| Route the fixture packs around the production loader | The binary-carries-it-and-provisioning-imports-it path is never exercised over content |

That last row is the mutation this design most wants run, because it is the one
D1 creates: it is the check that D2 actually holds, and if it passes with the
fixtures diverted then every other green in this item is over an empty set.

**Positive controls.** Each refusal test is paired with a valid pack that
resolves through the same path, so a refusal caused by an unrelated validation
rule is distinguishable from the one under test.

**Integration.** The provisioning-to-resolution path runs against a real plane.
`internal/dataplane/stack` integration needs `-timeout=40m`; the default
ten-minute alarm reads as a failure with no failing test.

## Open Questions

1. **Is the seam-enforced 1:1 on `dispatch_prompt_resolutions` acceptable, or
   should the resolution be columns on `story_dispatches`?** D8 argues for the
   table on backfill-honesty grounds and cites `AcceptDispatch`'s precedent, but
   this is the decision in this design most likely to be wrong.
2. **Does the built-in pack's installation declare a Maestro version range at
   all, and which?** D8 counts the range check as the one non-vacuous gate at
   item 4, which is true only if the range is real. An unbounded range would
   make it vacuous too.
3. **Should `prompt.pack` be permitted at all three scopes now**, when only the
   organization scope has a writer in item 4? D7 argues yes because §4's
   precedence rule is otherwise partly unexpressible, but it is the same
   register-without-a-reader shape D1 refuses elsewhere, one level down.

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
