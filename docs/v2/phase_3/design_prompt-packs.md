+++
title = "Design: Prompt Pack Identity, Storage, And Resolution (Item 4)"
edit_date = "2026-09-03"
status = "draft"
summary = "Mini-plan for Phase 3 item 4: the prompt-pack family built whole — immutable content records under a scheme-qualified digest, guarded by the schema's first anti-update trigger, beside mutable installation records carrying a monotonic revision and a governed installer identity; one atomic validated install operation so no content commits uninstalled and no coverage check runs without its declaring installation; the import gate reached through a consumer-owned contract so the seam validates every pack write without the plane importing a renderer; a selector configuration key that is the key registry's first live reader; resolution once at dispatch persisted beside the basis with the harness version it was validated against; a dispatch-bound principal path that copies the persisted resolution so a live principal cannot disagree with its dispatch; and organization provisioning that imports the built-in pack and seeds its selector in one transaction, with the import-and-select operator verb that later built-in versions move through. The built-in pack ships EMPTY and declares no role coverage, because item 4 has no model caller and neither candidate slot survived inspection: v1 has exactly one system prompt, the Architect's, bound to v1's workspace and tool contracts. Resolvable but not executable is the honest state, so the loader takes an fs.FS and the non-vacuous proof comes from fixtures travelling the identical path. Carries the principal_instances three-roles-in-one-column split as a total, lock-first migration whose single shape constraint partitions every row null-safely, whose guard is classified over what the old schema permits rather than what its writers produced, and whose origin is derived from which of three writer verbs was called, the scheme-qualified MPH query, the importer's legacy-scheme backfill, refusal recovery documented and tested in both directions, and five amendments — including the size, which review re-cut from M to L. The harness version is an opaque validated type supplied through the composition, so no root can open a seam with a malformed one, and a reciprocal deferred foreign key makes a dispatch without its resolution — or a resolution later deleted or re-pointed — a refused statement even for a writer that predates the schema."
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

**Six review rounds (Codex, 2026-09-03) found nine, eight, six, four, two and
then three P1s, and this revision carries all thirty-two.** Each is recorded under
[Points Resolved In Review](#points-resolved-in-review) with what was wrong and
where the fix landed. The first draft was M-shaped; the fixes make it L, and
that is amendment 5.

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

**The harness version travels the same way — as an opaque, already-validated
value.** `pkg/version.Version` is the binary's version — `"dev"` unless
goreleaser's ldflags set it — and `pkg/version` imports nothing, so the
composition root reads it. Round 4 had the root pass the *string* and
`orchestrator.Start` validate it, and review showed that check was bypassable:
provisioning and `dataplanectl prompt-pack select-builtin` open the seam
directly and never cross `Start`, so an install could write a version the rule
never saw. The same argument this decision makes about the import gate — a
check the seam does not cross is advisory — applied to the version.

So the version is a **type, not a string**: `harness.Version`, in a new neutral
vocabulary package `internal/dataplane/harness` of the same class as
`configkeys`, constructed only by `harness.Parse`, which admits exactly D8's
two forms and nothing else, and whose zero value is invalid. It is supplied
through **`plane.Composition.Harness`**, beside `Types`, `Keys` and the prompt
contract, and `plane.Open` refuses a zero value the way it refuses a missing
registry. Every composition root must construct it to open a seam at all; the
seam's install, update and resolution paths take it from the composition and
never from a caller. **And there is exactly one of it.** Round 5 also put the
type on `orchestrator.Config`, which review was right to refuse: two valid
values that differ — a composition built from one version and an Orchestrator
configured with another — would drive persistence and restart from different
authorities, and the schema could not tell. So `orchestrator.Config` carries
**no version**. The seam exposes the one it was composed with, `store.Store.Harness()`,
and the Orchestrator reads it there at `Start`; item 6's restart re-check reads
the same accessor. The composition is the authority and the only one. Tests
construct it from a real semver so D8's range check is exercised rather than
skipped, and one test drives an invalid string through the **operator path** —
`select-builtin` with a mis-stamped version — and asserts refusal before the
plane is touched.

`harness` also owns the comparator: `Compare`, `IsDev` and `InRange` over
`golang.org/x/mod/semver`, so the range check has one implementation and the
ladder test lives beside it.

Both closure guards are **exact sets** rather than deny-lists
(`internal/dataplane/store/closure_test.go`, `internal/orchestrator/closure_test.go`),
so each addition is a deliberate edit to a named list. Under this shape `store`'s
set gains `internal/dataplane/harness` — a vocabulary package, the class
`configkeys` already is — and the Orchestrator's gains that and
`internal/prompt`. **The resulting counts are re-derived from the built API
rather than asserted here**: what
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

**One check constraint, three exhaustive shapes, and no NULL can slip
through it.** Round 1 wrote three biconditionals, and review showed they did not
partition the rows: a non-agent could carry *some* prompt fields, because
"not all present" admits "partially present"; and a SQL `CHECK` whose operand
is NULL evaluates to NULL and **passes**, so `origin = 'resolved'` on a row with
a NULL origin refused nothing. The constraint is instead a disjunction of the
three shapes a row may have, each stated positively over every one of the
eight columns, with `num_nulls`/`num_nonnulls` so that no comparison against a
NULL is ever what decides:

| Shape | `kind` | `origin` | name, hash, scheme | the four references |
| --- | --- | --- | --- | --- |
| Non-agent | human or system | NULL | all NULL | all NULL |
| Foreign agent | agent | `foreign` | all NOT NULL | all NULL |
| Resolved agent | agent | `resolved` | all NOT NULL | all NOT NULL |

Anything else — a system principal with a name, a foreign agent with one
reference, an agent with no origin — matches no disjunct and is refused. Today's
schema ties only `agent_type` to the kind (`000004:51`), so an agent principal
may carry partial or absent MPH fields; ADR 0031 §2 says name and digest are
*always present for an agent principal*, and this is where that becomes true.
**The references bind the identity, not only the tenant.** Round 3's references
were composite on `organization_id` alone (000003's whole-tuple rule), which
stops a principal naming another organization's installation and stops nothing
else: content A with digest B, or installation C of some other content, both
satisfied it. ADR 0031 §2 says a live principal's references *"MUST agree with
the recorded name, digest, and organization"*, so the agreement is a key:

- `prompt_pack_contents` gains `UNIQUE (content_id, organization_id, scheme, digest)`
  — redundant with its primary key as a uniqueness fact, present so it can be
  a **foreign-key target**. A resolved principal's
  `(content_id, organization_id, scheme, digest)` references it, so the
  recorded scheme and digest cannot disagree with the content row they name.
- `prompt_pack_installations` gains `UNIQUE (installation_id, organization_id, content_id)`,
  and a resolved principal's `(installation_id, organization_id, content_id)`
  references it, so the installation named is an installation *of that
  content*.
- `dispatch_prompt_resolutions` carries the same two composite references, so
  a resolution is bound the same way and the dispatched-principal verb copies
  from a row that was already bound.

**What stays seam-enforced, and why it must.** Name, revision and the metadata
snapshot are **historical**: they record what the installation said at
resolution, and after a later `UpdatePromptPackInstallation` they legitimately
differ from the installation row. A key that tied them to the current row would
either forbid the update or rewrite history, and ADR 0031 §1 wants neither — a
correction *"cannot make a past run's dispatch look like it was decided on
facts that did not yet exist."* So those three are copied by the seam from the
resolution row, never from the installation, and their agreement with the
dispatch is what `CreateDispatchedPrincipalInstance`'s copy test proves.

#### Three writers, one per shape, and no caller chooses the origin

Round 1 let the general writer accept `origin = 'foreign'` from its caller,
which review was right to refuse: ADR 0031 §2 permits missing references *only
for imports*, and a discriminator a caller can set is a discriminator a caller
can set wrong. So the origin is never an input. It is **derived from which verb
was called**, and the verbs partition the three shapes:

- **`CreatePrincipalInstance`** — humans and system principals only. It
  **refuses `kind = 'agent'`** outright. Every agent principal has a pack, and
  which kind of pack is decided by the path that creates it, so there is no
  general agent path.
- **`RecordForeignAgentPrincipal`** — the import path. It requires a
  `RecordedLifetime` — a closed, historical lifetime, which is what an import
  is and what a live agent is not — and takes a `ForeignPromptPack{Name, Scheme, Digest}`.
  It writes `origin = 'foreign'` itself. The importer moves to this verb
  (`benchmarkimport/import.go:697`, the only production writer of agent
  principals today), setting `scheme = 'v1-manifest-sha256'` on what it writes.
  ADR 0031 is explicit that the importer stops being unchanged.
- **`CreateDispatchedPrincipalInstance`** — the live path. It takes an
  execution identity, **loads the persisted resolution** (D8) for that
  execution's dispatch, and **copies** its name, scheme, digest, content id,
  installation id, revision and snapshot onto the principal, writing
  `origin = 'resolved'` itself. The caller cannot supply those fields; they are
  not in its input. So a live principal cannot disagree with the dispatch it
  runs under, because it never had the chance to.

Item 6 is the first caller of the third verb; item 4 builds it, because it is
the only way the `resolved` shape can be written and the schema's whole point is
that it cannot be written any other way. Its test runs it against a fixture
dispatch and proves the copy.

#### The migration is total, or it refuses

A migration that backfills the legacy scheme on some rows and leaves others in a
state the new constraints forbid does not apply. `000023` therefore follows
`000022.down`'s pattern, which is `000011`'s with the lock it was missing: **lock
every inspected table `ACCESS EXCLUSIVE`, in a fixed order, before any scan**,
then a `DO` block that counts the rows it cannot honestly convert and raises
with the remedy, and only then alter. The lock is not decoration — `000022.down`
says so in its own comment, and its forced-interleaving test proves why. Without
it an old writer can insert between the scan and the `ALTER`: an agent principal
with no hash after the totality guard has approved, or a `story_dispatches` row
after the dispatch guard (D8) has found the table empty, leaving exactly the
row the guard exists to refuse.

**Whom the lock defends against, stated precisely, because round 2 overstated
it.** On the local stack, seam writers *cannot* overlap a migration:
`stack.Migrate` takes the lifecycle lock exclusive (`stack/stack.go:942`) and
`OpenSeam` holds it shared for the seam's lifetime (`stack/seam.go:27`), which
item 3 established. What the lifecycle lock does not cover is everything that
never takes it — a direct database writer through `psql`, an integration test
calling `migrations.Up` on a DSN, and any composer without the local flock,
which is what the cloud composer is. The table lock is the guarantee that holds
regardless of how the database was reached; the lifecycle lock is a property
of one composer. golang-migrate's own advisory lock serializes migrations
against each other and nothing else.

**One lock order for both migration directions — and an honest statement of
what ordering can and cannot buy.** Round 3 claimed a global order would
prevent deadlock, and review showed it cannot: the family's own writers do not
share an access order. `CreateDispatch` reads `configuration_records` before
`prompt_pack_installations`; provisioning writes contents, then installations,
then configuration. No single migration order agrees with both, and a reader
under `REPEATABLE READ` holds `ACCESS SHARE` on every table it has touched, so
a cloud seam mid-dispatch and a migration locking in the opposite order can
each wait on the other. Postgres detects that and aborts one of them.

So the claims are separated:

- **Correctness is the table lock's, unconditionally.** Whoever wins, no row is
  inserted between a scan and its `ALTER`, because the scan does not begin
  until the lock is held. That is the guarantee `000022.down` states and it
  does not depend on order.
- **Freedom from deadlock is a quiescence requirement, not an ordering
  guarantee — and the quiescence has to cover the whole cutover, not the
  migration.** Locally it is *enforced*: `stack.Migrate` holds the lifecycle
  lock exclusive, so no seam is open. In cloud mode nothing in the composer
  can enforce it, so it is an **operational procedure**, and round 4 stated it
  too narrowly as "migrate with no Orchestrator running". The scan-to-`ALTER`
  guarantee says nothing about a seam that was open *before* the migration,
  stayed idle through it, and writes *after* the locks release with code that
  predates the schema. The procedure is therefore: **every old-code seam is
  terminated before the migration starts, and none is opened until the new
  code is the only code running.** It is recorded in the
  [operations runbook](../process_runbook.md#schema-migration-cutover) as
  Policy, in this branch, because a rule that lives only in a design is not
  one an operator will find.
- **If the procedure is violated, two things can happen and both are
  refusals.** A deadlock may abort the migration — a refused migration with
  the same dirty version and the same documented recovery as any other. Or an
  old-code write may reach the new schema afterwards, and D8's reciprocal
  deferred foreign key refuses it — immediately, since the column it omits is
  NOT NULL. Neither is silent and neither corrupts; both
  are a retry after the operator does what the procedure asked.

Within that, both directions still take one fixed order, so that two
*migrations* can never deadlock each other and so the ordering test has
something definite to assert:

```
prompt_pack_contents
prompt_pack_installations
configuration_records
story_dispatches
dispatch_prompt_resolutions
principal_instances
```

Each direction locks every table it scans, alters **or drops**, in that order.
The up locks `principal_instances` and `story_dispatches`. The down locks
`prompt_pack_contents` — round 3 omitted it while dropping it —
`prompt_pack_installations`, `configuration_records`,
`dispatch_prompt_resolutions` and `principal_instances`. The lock-ordering test
asserts the sequence each direction takes against this list.

The guard classifies every row into exactly one of the classes below, and the
classes are exhaustive over what the pre-000023 schema **permits**, not over
what today's writers **produce** — round 2 enumerated the second and review
was right that the schema admits more. Today nothing ties `prompt_pack_id` or
`prompt_hash` to the kind, and `text` admits the empty string, so a system
principal carrying a pack name and an agent carrying a blank hash are both
legal rows the guard has to meet.

- **Converted**: a `kind = 'agent'` row whose `prompt_pack_id` is non-blank
  after trimming and whose `prompt_hash` matches the legacy content-identity
  form `^sha256:[0-9a-f]{64}$` — the importer's own `contentIDPattern`
  (`benchmarkimport/validate.go:15`), which is the only form any writer has
  ever stored. It gets `origin = 'foreign'`, `scheme = 'v1-manifest-sha256'`,
  and `name = prompt_pack_id`. Digest bytes are never rewritten.
- **Refused, agent with a missing or invalid identity**: a `kind = 'agent'` row
  with either field NULL, a blank name, or a hash not in that form. There is no
  honest backfill — the row records a run whose P was never captured, or was
  captured as something no scheme names — and the migration will not invent
  one.
- **Refused, non-agent carrying prompt fields**: a `human` or `system` row with
  any of `prompt_pack_id`, `prompt_hash` non-NULL. Nulling them would be a
  silent conversion of a row that says something the new shape cannot say;
  refusing makes someone look.

The two refusal classes are counted separately and the message reports each
count with its own remedy, so an operator knows which kind of row to find.
Both are reachable only by rows no current writer produces — the importer
validates the hash form and writes no prompt fields on its system principal —
which is the point: a total migration guards against the schema, not against
the writers that happen to exist.

**The legacy scheme's storage form keeps its prefix.** Under
`v1-manifest-sha256` the stored digest is the `sha256:`-prefixed string the
run record carried, because bytes are never rewritten; under
`pack-jcs-sha256-v1` it is the bare 64-hex `canonical.Digest` returns. The
prefix is data *under* the legacy scheme, not a scheme marker — D4's rule that
nothing parses a digest to discover its scheme still holds, because the scheme
column says which form to expect. Round 3 wrote the format checks as
conditionals, and a conditional over a scheme it does not name **passes** on an
unknown scheme, so the constraints are stated as **closed enumerations per
table, each scheme paired with its form, and nothing else admitted**:

| Table | Admitted schemes | Form |
| --- | --- | --- |
| `prompt_pack_contents` | **`pack-jcs-sha256-v1` only** | `^[0-9a-f]{64}$` |
| `principal_instances`, foreign shape | **`v1-manifest-sha256` only** | `^sha256:[0-9a-f]{64}$` |
| `principal_instances`, resolved shape | **`pack-jcs-sha256-v1` only** | `^[0-9a-f]{64}$` — and bound to the content row by the composite key above, so this is belt to that key's braces |
| `dispatch_prompt_resolutions` | `pack-jcs-sha256-v1` only | as resolved |

Contents admit only the plane's own scheme because the legacy scheme is *never
computed by the plane* (D4) and exists only for foreign imports; a legacy-scheme
content row would be a pack the plane claims to own and could not have
digested. The foreign shape admits only the legacy scheme because ADR 0031 §2
defines a foreign pack as *"recorded by name and legacy-scheme digest"*; a
foreign row under the plane's scheme would be a plane-owned identity with no
references, which is the nullable-in-general hole by another door. A row under
any third string matches no enumeration and is refused. A blank or malformed
digest under either scheme is refused at the row, which is also what keeps the
converted class above from being the last time the form is checked.

#### The down migration refuses rather than discards

`000023.down` **refuses** when any plane-owned state exists: a principal with
`origin = 'resolved'`, any `prompt_pack_installations` row, any
`dispatch_prompt_resolutions` row (D8), or any `configuration_records` row for
the `prompt.pack` key (D7). Each is a reference the pre-000023 schema cannot
hold, and a down migration that dropped them would leave either dangling
selectors or principals whose P silently reverted to a name. With none present
it collapses the columns back into `prompt_pack_id`, drops the tables, and
**drops the trigger function** with them — a function left behind is the
reversal's own residue.

#### Refusal recovery, both directions, documented and tested

A refused migration leaves golang-migrate's recorded version **dirty**, and
fixing the offending rows alone does not let it retry: the version has to be
forced back first. `000022.down` documents this in its own refusal message and
`TestRefusalRecoveryNeedsTheVersionForcedBack` proves the instruction is still
true. `000023` does the same in **both** directions:

- **Up refused** — the version is `23` and dirty. Remedy in the message:
  `make dataplane-force-version VERSION=22 FORCE=1`, resolve or delete the
  offending rows (or `make dataplane-reset FORCE=1` on a local plane), then
  `make dataplane-migrate`.
- **Down refused** — the version is `22` and dirty. Remedy:
  `make dataplane-force-version VERSION=23 FORCE=1`, then either remove the
  plane-owned state deliberately or stop reversing.

Each direction has a test that provokes the refusal, asserts the recorded
version and dirty flag the message assumes, proves a bare retry fails, then
forces and retries successfully. If the driver's dirty semantics ever change,
the test — not an operator — is what finds the instruction wrong.

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
(inclusive) and `max` (exclusive) semver strings, declared role coverage,
installer identity, **the Maestro version the gate last validated it against**,
and a revision.

**The revision's invariant, named.** `CHECK (revision >= 1)`, matching
`configuration_records_version_check` (`000015:85`). A new installation starts
at 1. **The next value is seam-derived** — the update statement sets
`revision = revision + 1` under `WHERE revision = $expected` — so no caller
supplies a revision, and a direct write of 0 or a negative is refused by the
row. "Monotonic" is those two facts together, and tests write 0 and −1 directly
to prove the check rather than the seam is what refuses them.

**Declared role coverage is a set, and it is stored as one.** A `text[]` or a
JSON array would let `["architect","coder"]` and `["coder","architect"]` — or
`["coder","coder"]` — compare unequal while declaring the same thing, and D6's
idempotency (*identical metadata returns the existing installation*) and D8's
snapshot would both silently depend on input order. So coverage is a **jsonb
object keyed by role name** with the value `true`: object keys are unique by
construction, jsonb normalizes key order on storage, and jsonb equality is
therefore set equality — **provided every value is the constant `true`**, which
`jsonb_typeof = 'object'` alone does not enforce: `{"coder": true}` and
`{"coder": 1}` have identical keys and compare unequal. So the check is
`jsonb_typeof(declared_roles) = 'object' AND prompt_pack_roles_canonical(declared_roles)`,
where the second is a small `IMMUTABLE` SQL function — a `CHECK` may not
contain a subquery, and it may call an immutable function — that returns true
only when every key is non-empty and every value is the jsonb `true`. It is
dropped by the down beside the trigger function. The seam builds the object
from whatever the caller passes; the constraint is what makes the
representation canonical rather than conventionally so. Tests install with
reordered and duplicated role lists and assert one stored value, one identity,
and `Created = false` on the second call; a direct write of `{"coder": 1}` is
refused by the row.

**The validation version is persisted on every install and update.** Round 2
had dispatch re-running parse and render "when the harness version has moved"
with nothing recording what version the installation had been validated
against, so the claim had no evidence to consult. `InstallPromptPack` and
`UpdatePromptPackInstallation` both write the composition's `harness.Version`
into `validated_maestro_version` beside the result of the gate they ran; D8
reads it.

**The range has a write invariant of its own, independent of any runtime
version.** D8 skips the *comparison* under a development build; round 1 let that
skip reach the *write*, so a malformed or inverted range could be installed on
any local plane and never noticed until a tagged build met it. Install and
metadata update therefore each require, before anything else: `min` and `max`
are valid semver, and `min` sorts strictly below `max`. That is a property of
the declaration, checkable with no harness at all. Whether the running version
falls inside it is a separate evaluation, made at dispatch, and is the only part
D8 declines to make under `"dev"`.

**Idempotent insertion is `ON CONFLICT DO NOTHING`, then a read** — never
`ON CONFLICT DO UPDATE`, which would fire the anti-update trigger on the very
path that is supposed to be a no-op.

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
supplied through `plane.Composition.Harness` (D3) and read back through the
seam, and outside a goreleaser build its value is the string `"dev"`. So:

- **The range is two semver bounds**, `min` inclusive and `max` exclusive,
  stored as strings on the installation, compared under semver precedence
  including prerelease ordering — which the phase ladder needs, since
  `v2.0.0-phase.3.0.0` must sort below `v2.0.0-phase.4.0.0`. No range DSL.
  The comparator is `golang.org/x/mod/semver` at **v0.37.0**, whose prerelease
  ordering Codex verified against that version's source and tests in review
  (2026-09-03); `go.mod` carries no semver dependency today, so this pins a new
  one. An in-tree test asserts the ladder's ordering under it, so the
  verification survives the transcript.
- **The built-in declares the Phase 3 band**: `[v2.0.0-phase.3.0.0, v2.0.0-phase.4.0.0)`.
  Bounded, real, and the range a tagged Phase 3 binary satisfies.
- **Exactly two forms are admitted, and they are checked at construction,
  which every path crosses.** `harness.Parse` (D3) accepts either the **exact
  sentinel `"dev"`** — `pkg/version`'s unset default — or a **valid semver with
  its leading `v`**, which is what `golang.org/x/mod/semver` requires and what
  goreleaser stamps. Anything else — a release built as `2.0.0` without the
  `v`, an empty string, a stray suffix — is **malformed configuration and
  refuses to construct**, typed, naming the value and the two forms; a
  composition without a constructed value cannot open a seam. Round 3 let
  every invalid string fall into the development exception, so a mis-stamped
  release would have bypassed every range in the plane while reporting nothing
  wrong; round 4 moved the check to `Start`, which the operator verbs never
  cross. Checking at construction means dispatch, install and update only ever
  see the two forms, whichever root opened the seam, and none of them
  re-decides the question.
- **The sentinel is not compared.** Under `"dev"` the range check records
  `not-evaluated` on the resolution beside the version string. It does not
  pass and it does not refuse: a pass would assert a comparison that never
  ran, which is [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md)'s
  reported-as-zero mistake, and a refusal would make every local build
  undispatchable.
- **Tests inject a real semver**, so the refuse branch and the pass branch are
  both exercised; a third test proves that `"dev"` records `not-evaluated`
  rather than either; a fourth proves that `"2.0.0"` refuses `harness.Parse`;
  and a fifth drives it through `select-builtin` and asserts the verb refuses
  before opening the plane.

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
| Harness-contract re-run fails after a version move | **Yes**, with the evidence D6 now stores | Re-run when `validated_maestro_version` differs from the running version, **or when either is the `"dev"` sentinel** — a development build cannot claim nothing moved, exactly as D8's restart rule says; no other non-semver value can reach this point, because `harness.Parse` refused it. Under a real semver pair that match, skipped and recorded as such. **Vacuous over the empty built-in**, so its non-vacuous proof comes from D2's fixture packs through the same path |

#### Where the resolution is persisted, and what it records

ADR 0031 §4 writes the resolved values "into the invocation", but the invocation
schema is ADR backlog candidate 13 and does not exist; item 2 deliberately gave
executions **identity and authority only**. So the resolution lands beside the
thing that decided it: a **`dispatch_prompt_resolutions`** row, unique on the
dispatch, carrying the full dispatch lineage tuple as its composite key, the
resolved name, scheme, digest, content id, installation id, revision, the
metadata snapshot, **the Maestro version it was validated against**, and the
range check's result — **`passed` or `not-evaluated`, and nothing else**. Every
reference is composite on `organization_id`.

Round 1 listed `refused` as a third result, and it cannot exist: every
`CreateDispatch` refusal returns before the dispatch row is written
(`postgres/dispatch.go`'s `rejectDispatch` returns an error and the transaction
rolls back), so there is no parent for a refused resolution to hang from. A
refused range check is a `DispatchRejected` returned to the caller with nothing
persisted — which is exactly how item 3's other typed refusals already behave.
Persisting refused *attempts* would be an Audit record with its own design; it
is deferred, and named here so the absence is a decision rather than a gap.

Recording the validated-against version is what makes restart detection
possible without re-resolving P. **The restart rule, which item 6 implements
(D11) and item 4 records for:**

- **A tagged build whose version differs from the recorded one** re-runs the
  **declared range check** *and* parse and the variable contract. Round 1
  omitted the range, and a harness moving out of a pack's declared band is the
  one thing the range exists to catch.
- **A development build revalidates on every restart.** `"dev" == "dev"` says
  nothing — two local binaries built an hour apart both say it — so an
  unversioned harness cannot use the recorded version as evidence that nothing
  moved, and does not. Parse and the variable contract run; the range stays
  `not-evaluated`.
- **Never resolution.** P is fixed at dispatch in every case.

It is written **in the same transaction as the dispatch**, and the seam refuses
to create a dispatch without it. A seam-enforced 1:1 is the existing pattern:
`AcceptDispatch` flips the disposition and creates the execution in one
transaction because *"an accepted dispatch has at least one execution, which is
the seam's half of item 2's invariant."*

**Here the schema holds the other half too, because a seam is not the only
writer — and it holds it in steady state, not only at insert.** Round 5 named
the writer the seam rule cannot reach: an old-code seam, opened before the
migration and idle through it, that writes a dispatch afterwards without a
resolution. Round 5 answered with an insert-time constraint trigger, and round
6 showed that enforced existence at one moment only: delete or re-point the
resolution afterwards and the same orphan exists with nothing to refuse it.

The mechanism that covers both is a **reciprocal deferred foreign key**, and it
is available precisely because the migration refuses existing dispatches
(below) — with no rows to backfill, `story_dispatches` can gain a NOT NULL
column. So:

- `story_dispatches` gains `prompt_resolution_id uuid NOT NULL`, with a
  composite reference `(story_dispatch_id, prompt_resolution_id)` →
  `dispatch_prompt_resolutions (story_dispatch_id, resolution_id)`,
  **`DEFERRABLE INITIALLY DEFERRED`**, so the pair is checked at commit and the
  seam can write the dispatch and its resolution in either order within the
  transaction.
- `dispatch_prompt_resolutions` references its dispatch the other way, with
  the full lineage tuple, `ON DELETE RESTRICT`, and gains
  `UNIQUE (story_dispatch_id, resolution_id)` to be the target above.

What that buys, each a refused statement rather than a convention:

| Attempt | Refused by |
| --- | --- |
| Insert a dispatch with no resolution and commit | The deferred FK at commit |
| Old-code insert with no `prompt_resolution_id` at all | `NOT NULL`, immediately — the column it does not know is the one it cannot omit |
| Delete a resolution that a dispatch names | The FK, `NO ACTION` on the referenced side |
| Re-point a resolution at another dispatch | The composite pair no longer matches the dispatch's own `story_dispatch_id`, so the FK from the dispatch fails on update of its target |

The round 5 constraint trigger is **withdrawn**: the FK does everything it did
and the steady-state half it did not, with no function to maintain, and the
schema goes back to one trigger — the anti-update on contents, which still has
no FK equivalent. The seam still writes both rows itself; the schema is what
makes any other writer's attempt a refused commit rather than a silent success.

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
| The **restart-time compatibility re-check** under D8's rule — a moved tagged version re-runs the range, parse and the variable contract; a development build revalidates on every restart; P is never re-resolved | **Item 6** | It is agent start; item 4 records what it reads (D8) |
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

**One amendment to another live document.** The
[operations runbook](../process_runbook.md#schema-migration-cutover) gains the
schema-migration cutover procedure (D5). The runbook is live and Accepted, so
the section is marked **PROPOSED** in place and carries this design's date; the
acceptance commit flips it to Accepted with the date and parties, following the
plan's own precedent for in-place amendment.

## Implementation And Review Sequence

Reviewed as a sequence of local commits, in this order, because each step's
verification depends on the one before it.

1. **`internal/prompt`**: slot registry, per-slot variable contract, parser,
   renderer, and the entry projection that feeds the digest. No plane, no store.
2. **`store.PromptContract`**, `internal/dataplane/harness`, the
   `plane.Composition` fields and `Store.Harness()` (D3), with both closure
   guards re-derived and updated. The seam also sets `application_name` on its
   connections, so the runbook's cutover check can name Maestro's sessions
   rather than describe them.
3. **Migration `000023`**: the totality guard and the dispatch-rows guard, the
   two pack tables with the anti-update trigger and composite keys, the
   `dispatch_prompt_resolutions` table, the `principal_instances` split with its
   origin discriminator and single shape constraint, the per-scheme format
   checks, the classified guard, the scheme index, and the refusing down. sqlc
   regeneration.
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
8. **The three principal writers** (D5): the general path's refusal of agents,
   `RecordForeignAgentPrincipal` requiring a recorded lifetime, and
   `CreateDispatchedPrincipalInstance` copying the resolution.
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
| Let the general `CreatePrincipalInstance` accept `kind = 'agent'` | An agent principal created by a path that derives no origin — the partition has a fourth door |
| Let `RecordForeignAgentPrincipal` accept a nil `RecordedLifetime` | A live agent recorded as foreign: ADR 0031 §2's *only for imports* violated by the verb that exists to honour it |
| Have `CreateDispatchedPrincipalInstance` take pack fields from its input instead of the resolution | The copy becomes a claim; a live principal can disagree with its dispatch |
| Remove the totality guard; **seed** an agent row with a null `prompt_hash` **before** migrating; assert the guard's distinct count and remedy text | The guard itself. Round 1 had this mutant dying at the later constraint, which proves the constraint and leaves the guard wholly untested — and contradicts lock-first ordering, since nothing can be planted after the lock |
| Relax the shape disjunction; insert a system principal carrying a pack name, a foreign agent carrying one reference, and an agent with a NULL origin | The constraint itself, separately: each stray shape must be refused by the disjunction and not by a neighbouring rule, and the NULL-origin row is the one a biconditional would have passed |
| Remove a per-scheme format check; insert a bare-hex digest under the legacy scheme | A digest whose form its scheme does not admit |
| Insert a content row under `v1-manifest-sha256`, and a foreign principal under `pack-jcs-sha256-v1` | Cross-scheme rows: a pack the plane claims to own and could not have digested; a plane-owned identity with no references |
| Insert any row under a third scheme string | The enumeration, not a conditional: an unknown scheme must be refused, not passed through |
| Relax the composite key; give a resolved principal content A with digest B, then installation C of other content | The identity–reference agreement ADR 0031 §2 requires, which organization-only keys never checked |
| Construct `harness.Parse("2.0.0")` | Fail closed on a malformed version rather than falling into the development exception |
| Run `select-builtin` with a mis-stamped version | The operator path refuses before opening the plane — the check is at a boundary every root crosses, not at `Start` |
| Open a seam with a zero `Composition.Harness` | `plane.Open` refuses a composition with no validated version, as it refuses one with no registry |
| Make `prompt_resolution_id` nullable and drop the deferred FK; insert a `story_dispatches` row through raw pgx with no resolution; commit | An old-code write succeeds after cutover, leaving the row every reader was promised could not exist |
| Drop the `RESTRICT`; delete a valid resolution that a dispatch names | The steady-state orphan the insert-time check could not see |
| Re-point a valid resolution's `story_dispatch_id` at another dispatch | The composite pair diverges from the dispatch that names it |
| Write `{"coder": 1}` directly | The value constraint: identical keys, unequal objects |
| Seed a system principal carrying `prompt_pack_id` before migrating | The guard's second refusal class, with its own count and remedy — not the first class's message |
| Seed an agent whose `prompt_hash` is `''` before migrating | The first refusal class on a non-NULL value: a blank passes an `IS NOT NULL` test and must not pass the guard |
| Write `revision = 0` directly | The check, not the seam |
| Install with `["coder","architect","coder"]` after `["architect","coder"]` | Set semantics: one stored value, `Created = false` |
| Skip writing `validated_maestro_version` on update | Dispatch cannot tell a moved harness from an unmoved one and re-runs nothing |
| Remove the dispatch-rows guard; seed a dispatch before migrating; assert the refusal | A dispatch with no resolution row that every reader must then special-case |
| Remove the up migration's `LOCK TABLE`; interleave an old-shape insert between scan and `ALTER` by forced ordering, on `000022`'s test pattern | The guard approves a table the writer then poisons |
| Same for the down: interleave a `prompt.pack` configuration write after the scan | The down approves deletion of state that commits a moment later |
| Provoke each refusal, retry without forcing, then force and retry | The documented recovery sequence, in both directions; asserted against the recorded version and dirty flag the message names |
| Split `InstallPromptPack` into two transactions and fail between | Committed content with no installation: a row nothing can select |
| Let the seam create a dispatch without the resolution row | A dispatch exists whose P is unrecoverable |
| Make provisioning's three writes three transactions | An organization with content and installation but no selector: unresolvable, reached by an ordinary retry |
| Make `select-builtin`'s configuration update unconditional | Last-writer-wins on the selector — the ADR 0027 defect by name |
| Accept a bare name as a selector value | A selector that cannot deterministically identify one version |
| Skip the contract on installation **update** | An installation valid when written is unusable after an edit |
| Make `"dev"` pass the range check instead of recording `not-evaluated` | A comparison asserted that never ran |
| Skip the range write invariant under `"dev"` and install `min > max` | A malformed declaration on every local plane, discovered by the first tagged build |
| Suppress restart revalidation when both versions read `"dev"` | Two different local binaries treated as one harness |
| Have resolution re-resolve on restart | One Story spans two P values |
| Route the fixture packs around the production loader | The binary-carries-it-and-provisioning-imports-it path is never exercised over content |

The last row is the mutation this design most wants run, because it is the one
D1 creates: it is the check that D2 actually holds, and if it passes with the
fixtures diverted then every other green in this item is over an empty set. The
guard and constraint rows are two mutants, not one, because round 1 had written
them as one and it proved the wrong half: a guard whose removal is only detected
by a later constraint has never been tested at all.

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

**Round 2 — Codex, 2026-09-03. Eight P1s on the round 1 fixes, all accepted
after verification.**

1. *The three origin checks did not partition valid rows*: non-agents could
   carry partial fields, and a SQL biconditional over a NULL passes. → One
   constraint as a disjunction of three exhaustive, null-safe shapes (D5).
2. *`foreign` was caller-selectable.* → The origin is never an input; three
   verbs partition the three shapes and each writes its own. The general path
   refuses agents; `RecordForeignAgentPrincipal` requires a recorded lifetime.
   Verified the importer is the only production writer of agent principals, so
   the move orphans nothing (D5).
3. *The range had no write invariant*, so `"dev"` could install malformed
   bounds. → Valid semver and `min < max` required at install and update,
   independent of the runtime version (D6).
4. *A resolution cannot be `refused`*: refusals return before the dispatch
   exists. → Vocabulary is `passed | not-evaluated`; refused attempts are not
   persisted and their Audit design is named as deferred (D8).
5. *Restart revalidation omitted the range and treated `"dev" == "dev"` as
   unmoved.* → The rule now covers both, and item 6's row says so (D8, D11).
6. *Neither migration direction locked before scanning.* → `000022.down`'s
   lock-first pattern in both directions with a fixed table order, and the
   forced-interleaving test (D5).
7. *Refusal recovery was unspecified.* → Both directions document the
   force-version sequence and test it on `TestRefusalRecoveryNeedsTheVersionForcedBack`'s
   pattern (D5).
8. *The totality mutant proved the constraint, not the guard.* → Two mutants:
   seed before migration and assert the guard's count and remedy; separately
   relax the constraint and assert the disjunction refuses each stray shape.

**Round 3 — Codex, 2026-09-03. Six P1s, all accepted after verification.**

1. *The guard still missed legal old states*: non-agents with prompt fields and
   agents with blank or malformed values are admissible today. → The guard is
   classified over what the schema permits, with two refusal classes counted
   and reported separately; per-scheme digest format checks make the form a
   row constraint (D5). Verified `contentIDPattern` is the only form ever
   stored.
2. *Dispatch could not know whether the harness had moved since install.* →
   `validated_maestro_version` is written on every install and update, and the
   re-run rule reads it, treating any non-semver side as moved (D6, D8).
3. *The lock rationale contradicted item 3*: `Migrate` holds the lifecycle
   lock exclusive and `OpenSeam` shared. → Stated precisely whom the table
   lock defends — direct writers and lock-less composers — and one global
   parent-before-child order for both directions (D5).
4. *Role coverage had set semantics and no canonical form.* → A jsonb object
   keyed by role, so equality is set equality by construction, with reorder
   and duplicate tests (D6).
5. *"Monotonic revision" named no invariant.* → `CHECK (revision >= 1)` on
   `000015`'s precedent, seam-derived `revision + 1`, and direct-write tests
   (D6).
6. *The verification plan targeted superseded mechanisms.* → Every mutant and
   sequence step now names the single disjunction and the three-verb
   partition; new mutants cover each new mechanism above.

**Round 4 — Codex, 2026-09-03. Four P1s and one correction, all accepted.**

1. *The recorded identity was not bound to its references.* → Composite
   identity keys on contents and installations, referenced by resolved
   principals and by resolutions; name, revision and snapshot named as the
   historical fields the seam copies from the resolution (D5).
2. *Scheme constraints were conditionals, so an unknown scheme passed, and
   contents admitted the legacy scheme.* → Closed per-table enumerations:
   contents and the resolved shape admit only the plane's scheme; the foreign
   shape only the legacy one (D5).
3. *The development exception was too broad.* → Exactly `"dev"` or valid
   `v`-prefixed semver, checked once at `Start`; anything else refuses
   startup (D8).
4. *The lock-order claim overreached*: the family's own writers disagree on
   access order, and the down dropped a table it did not lock. → Correctness
   as the lock's, deadlock-freedom as a quiescence requirement enforced
   locally and operational in cloud, an aborted migration as an ordinary
   refusal; `prompt_pack_contents` added to the down's set (D5).
5. *`jsonb_typeof` did not enforce the constant values.* → An immutable
   helper function in the check (D6).

**Round 5 — Codex, 2026-09-03. Two P1s, both accepted.**

1. *Version validation at `Start` was bypassable* by provisioning and
   `select-builtin`, which open the seam directly. → `harness.Version`, an
   opaque type constructed only by `harness.Parse`, supplied through
   `plane.Composition` and refused at zero by `plane.Open`; tested through the
   operator path (D3, D8).
2. *Cloud quiescence covered only scan-to-`ALTER`*: an idle old-code seam can
   write a dispatch without its resolution after the locks release. → The
   procedure covers the whole cutover and is recorded in the operations
   runbook in this branch; a deferred constraint trigger on `story_dispatches`
   makes the 1:1 a refused commit rather than a convention an old writer
   cannot know (D5, D8).

**Round 6 — Codex, 2026-09-03. Three P1s, all accepted; one non-blocking
taken.**

1. *Two authorities for the version*: `Composition.Harness` and
   `orchestrator.Config`, with three passages still prescribing the latter. →
   `Config` carries no version; the seam exposes the composition's through
   `Store.Harness()` and everything reads that (D3, D6, D8, sequence).
2. *The insert-time trigger left the steady state unenforced*: a resolution
   deleted or re-pointed afterwards recreates the orphan. → A reciprocal
   deferred composite FK, possible because the migration refuses existing
   dispatches; the round 5 trigger withdrawn; delete and re-point mutants
   (D8).
3. *The runbook amendment sat inside a live, accepted document as if
   accepted.* → Marked PROPOSED in place, to be flipped with the acceptance
   date and parties in the acceptance commit; listed under the amendments
   below.
4. *(Non-blocking)* "confirm nothing holds a connection" was impractical
   against system sessions. → The runbook names the `pg_stat_activity` check
   filtered to client backends under Maestro's `application_name`, which the
   seam now sets.

Both open questions closed by Codex: the anti-update trigger is acceptable
(idempotent insertion is `ON CONFLICT DO NOTHING`; the down drops the
function), and `golang.org/x/mod/semver` **v0.37.0** is suitable — Codex
verified its source and tests and its prerelease ordering matches the phase
ladder. An in-tree test asserts the ladder's ordering anyway, so the claim does
not live in a review transcript.

## Open Questions

None outstanding after round 6. The two the first draft carried — the trigger
as the schema's first, and the semver comparator — are closed above.

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
