+++
title = "ADR 0031: Prompt Pack Identity, Resolution, And Storage"
edit_date = "2026-08-13"
status = "draft"
summary = "Fixes the minimal prompt-pack contract the MPH signature's P component has been carrying informally since Phase 1. A pack version is whole rather than an overlay, immutable, and identified under the named scheme `pack-jcs-sha256-v1` as SHA-256 over the JCS serialization of a slot-key-to-content map and nothing else -- the container is normalized, prompt text never is, since whitespace is content -- with the scheme in a field of its own, because an sha256 prefix names an algorithm and not a scheme; imported v1 identities keep the opaque scheme `v1-manifest-sha256`, no comparison crosses schemes, and the Phase 3 migration backfills the legacy scheme onto rows already imported without rewriting a digest or touching the run-record contract. Because name, declared version range, and declared role coverage sit outside the digest while the content record is immutable and unique by it, mutable metadata lives on a separate installation record -- at most one per organization and content, carrying a revision that resolution binds and updates check optimistically -- that can be corrected without minting a pack. The identity is global while the row is not: content stays organization-scoped, so two tenants holding one pack hold two rows carrying one identity. The name is a label and therefore cannot be a selector either: an MVP selector names exactly one plane-owned version by reference or scheme-qualified digest, a live dispatch must carry that reference while imported foreign runs are the only records permitted to omit it, and no version label exists at all until candidate 9 defines an ordering for it. The pack digest closes over pack entries and nothing else; what accounts for a model input is invocation provenance assembled from four recorded sources -- pack, harness as Maestro version plus config hash, seeding set, and accumulated turn material -- and since most of that accumulates after an invocation begins, the expected source contract is recorded at the start while each model call binds the exact contributing references and digests and only then draws a status, with unclosed a judgment about whether an adapter can supply trustworthy bindings rather than a category of runtime, and with availability held as a state separate from closure because bound Audit sources are truncatable and a closed call may become unreconstructible. All four obligations are handed explicitly to the agent execution contract rather than given a column here. Resolution happens once and deterministically at dispatch, and the resolved identity is reused verbatim on agent restart, because re-resolving would let one Story span two P values invisibly. Packs are a data-plane family and not artifacts; because importing is not selecting and a deployment bootstrap has no organization to configure, organization provisioning is what idempotently imports the built-in default and seeds the scoped selector that makes it resolvable, upgrades move no existing organization's selector, and two organizations in one deployment can therefore default to different packs according to when each was provisioned. There is no run-time fallback to the binary, because a silent fallback makes the signature a lie. Compatibility splits: import checks parse, each slot's variable contract, and coverage of the roles the installation declares, while dispatch checks that this execution's roles fall within them, with both re-run on installation updates -- and a declared version range can refuse but never authorize. The debt on principal_instances.prompt_pack_id is three roles in one nullable text column, and its only writer today records a foreign pack the plane will never own."
type = "design"
+++

# 0031. Prompt Pack Identity, Resolution, And Storage

Status: **Proposed** (Claude, 2026-08-12). Item A3 of the accepted
[pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md). Drafted concurrently
with, and reviewed as a set alongside,
[ADR 0030](0030-tool-execution-policy-hook.md) (item A2). It has no dependency on
either A1 or A2 and joins the design track at item A4, whose invocation schema
carries pack identity.

This ADR resolves [ADR backlog candidate 5](../v2/notes_adr-backlog.md). Registry
inheritance, versioning and export, repo-local packs, and skills remain
[candidate 9](../v2/notes_adr-backlog.md).

**One line of candidate 9 is drawn differently than it was written, and §2 is why.**
That candidate reads "installed org-level packs" as a single deferred item. It is
two:

- **Minimal installation metadata is required now.** §2 makes a pack's mutable
  facts — display name, declared Maestro version range, declared role coverage —
  a separate record from its immutable content, because they sit outside the
  content digest and otherwise could never be corrected. Phase 3 cannot resolve a
  pack without them.
- **The registry stays deferred**: browsing and installing packs as a user-facing
  act, governance over who may install what, inheritance and overlays,
  versioning and export formats, distribution, and sharing across organizations.

The backlog wording is reconciled to that split in this ADR's acceptance commit.

## Context

### The debt is more specific than "no table behind the column"

`principal_instances.prompt_pack_id` is a nullable `text` column with no table
behind it and no foreign key
(`internal/dataplane/migrations/000004_principal_instances.up.sql:29`), beside
`prompt_hash` (`:30`). The [schema inventory](../v2/phase_2/inventory_schema-tables.md)
lists prompt packs as a deferred family whose creator is Phase 3.

Two facts sharpen that, and both change the answer:

**The column is doing three jobs at once** — naming a pack, identifying its
content, and standing in for a reference to a record the plane will hold. Those
are three different things, and collapsing them is what makes the column look like
a simple missing foreign key when it is not.

**Its only writer today records a pack the plane will never own.** The Phase 2
item 9 benchmark importer writes the *name* string into it
(`internal/dataplane/benchmarkimport/import.go:689`), and for the v1-as-patched
target that string is `"v1-embedded"` — prompts compiled into a v1 binary. Adding
a NOT NULL foreign key to a `prompt_packs` table would leave two options, and both
are wrong: fabricate a pack record for prompt content the plane never held, or
refuse the import and lose the run.

### Phase 1 already settled the shape informally, and it was right

The benchmark MPH bundle carries `PromptRef{Pack, Hash}` — *"a pack label plus
content hash. Hash may be omitted for embedded-prompt targets — the adapter
computes it from actual prompt content and records it in the MPH identity; a
declared hash wins"* (`benchmark/mph/bundle.go:53`). The `paired-default`
configuration declares `[prompt] pack = "v1-embedded"` and no hash.

The adapter's computation is worth reading closely, because it is the honest
version of a problem this ADR has to solve properly.
`benchmark/target/v1target/prompthash.go` hashes a sorted expansion of an
**embedded manifest** of prompt file paths against the target checkout, with a
separate reviewed **allowlist** of files that look like prompts and are not. Every
manifest entry must match something, so a moved prompt file fails loudly rather
than silently dropping out of P.

That manifest exists because v1 has no pack boundary — someone had to decide, by
hand and by review, which files are prompt content. A pack makes that boundary
explicit, and the discipline the manifest encodes carries forward as §3's closure
rule.

### A name collision to settle before Phase 3 folds them together

`pkg/templates/packs` in v1 is **not** this. Those are language and platform
packs — `go.json`, `node.json`, `python.json`, `generic.json` — carrying base
images, Makefile targets, and tooling names for bootstrap scaffolding. They have
no relationship to prompt packs beyond the word, and the
[port inventory](../v2/phase_0/inventory_v1-port.md) lists `pkg/templates`
(with `bootstrap`, `claude`, and `packs`) as rework whose templates are re-cut for
v2 states and whose packs become hash-addressed prompt packs — which reads, at a
glance, as though the technology packs become the prompt packs. They do not.

### What this ADR must satisfy

- [ADR 0021](0021-artifacts-and-principal-instances.md): the MPH signature's **P**
  is *the prompt pack and prompt hash*, recorded on the principal instance, and
  identity is content digests rather than location.
- [ADR 0022](0022-v2-data-plane.md): prompt packs are a data-plane family; all
  access is through the Orchestrator's persistence seam.
- [ADR 0025](0025-golden-stories-and-benchmark-runner.md): a configuration is
  identified by content hash, and *the MPH identity in run records derives from
  content, never location, so results remain comparable across the storage
  transition* — including a transition from file-based Phase 1 configurations to
  data-plane records.
- [ADR 0019](0019-orchestrator-boundary.md): selecting a pack from configuration
  is rules, not judgment, so resolution is Orchestrator machinery.
- Roadmap pillar 10: packs are immutable, hash-addressed, exportable packages,
  with the database canonical for installed org-level packs; and the MVP note that
  *a minimal prompt-pack ID/hash can be enough for early metrics*.

## Decision

### 1. A pack version is whole, immutable, and content-addressed

A **prompt pack** is the complete set of prompt material the factory uses for a
run: role and system prompts, per-state templates, and review prompts, keyed by a
code-resident **slot** vocabulary.

**Whole, not an overlay.** A pack version supplies every slot it declares and
inherits nothing. Overlays and inheritance are candidate 9, and admitting them now
would turn both identity and resolution into a composition problem before there is
a consumer for one.

**Immutable.** A pack version is never edited. An edit produces a new version.

**Identified by content, under a named scheme.** The identity of a pack version is
a **digest over a normalized projection of its entries** — the entry container
canonicalized with the RFC 8785 JCS discipline
[ADR 0028](0028-artifact-envelopes-and-payload-schemas.md) applies to payloads —
carried together with **the scheme that produced it**.

#### The two schemes, named and specified

A scheme identifier is persisted protocol, not implementation detail: it is stored
on every principal instance, and two writers that disagree about what it means
produce identities that compare equal and are not. So both are named and fixed
here, and each carries a version so that a change to what the scheme *means* is a
new scheme rather than a silent reinterpretation of stored rows.

| Scheme | Applies to | Definition |
| --- | --- | --- |
| `pack-jcs-sha256-v1` | Packs the plane owns | SHA-256 over the RFC 8785 JCS serialization of a JSON object mapping **slot key → entry content string**, and nothing else. Slot keys are the code-resident vocabulary of §1. Rendered `pack-jcs-sha256-v1:<lowercase hex>` |
| `v1-manifest-sha256` | Imported v1 identities | Opaque. Whatever `benchmark/target/v1target/prompthash.go` computed — SHA-256 over the sorted manifest expansion of relative path, length, and bytes. **Never rederived by the plane**; recorded as received |

**The v2 projection contains the entries and nothing else** — no name, no declared
version range, no timestamps, no organization. Two consequences follow and both
are intended: identical prompt content computes to the same **identity** wherever
it is installed, and correcting a typo in a pack's metadata does not mint a new
pack (§2 handles what that requires).

**The identity is global; the row is not.** Content records stay
organization-scoped and unique on `(organization, scheme, digest)`, following
Phase 2's tenancy pattern, so two organizations holding the same pack hold two
rows carrying one identity. A previous version of this section said content "is
not organization-scoped," which contradicted that uniqueness key two paragraphs
later. Deduplicating content across tenants is candidate 9's problem and it is not
solved by pretending the rows are already shared.

Two further things were wrong in a first version of this section:

**JCS canonicalizes the container, never the prompt text.** Entry ordering, key
encoding, and structure are normalized so that a cosmetic reordering does not mint
a new identity. **A prompt string is content and is hashed byte for byte**,
whitespace included — a template's leading spaces and line breaks are part of what
the model receives, so "semantically equal packs digest equal" is true of the
container and false of the text.

**Digests are compared only within a scheme.** The identity Phase 1 records for
`v1-embedded` is `sha256:` over an ordered expansion of file paths and bytes
(`benchmark/target/v1target/prompthash.go`); a pack digest is JCS over slot
entries. They are different functions over different domains, and ADR 0028 already
treats a runner-supplied identity as opaque and non-rederivable. So a pack
identity is **scheme-qualified**, and **no comparison, grouping, or equality claim
crosses schemes.** Equal-looking hex under two schemes means nothing.

**The scheme is a separate field, and the legacy rows are backfilled.** An
`sha256:` prefix names an *algorithm*, not a semantic scheme, so a stored
`sha256:<hex>` does not become scheme-qualified by being looked at differently.
Three things cannot all hold — preserve the stored values verbatim, carry the
scheme in the same field, and change neither data nor importer — and a previous
version of this section asserted all three.

The resolution keeps the run-record contract fixed and puts the scheme in the
plane, because the run record is
[ADR 0025](0025-golden-stories-and-benchmark-runner.md)'s and changing its
`prompt_hash` encoding would break a black-box contract for a problem that is not
the runner's:

- **The plane record carries `scheme` beside the digest**, as its own field.
- **Existing rows are backfilled** to the legacy scheme by the Phase 3 migration —
  which is the same migration that creates the family, so this costs an
  `UPDATE`, not a second migration.
- **The importer sets the legacy scheme** on what it writes. It stops being
  unchanged, and the Consequences say so rather than claiming otherwise.
- **Digest bytes are never rewritten.** Preserved verbatim, still opaque, still
  non-rederivable.

**The name is a label, not a key.** `default` is a handle for humans and for
configuration; it is not what two runs are compared on, and it is not unique
across organizations. Every MPH comparison keys on the scheme-qualified digest.
This is the rule Phase 1 already follows in practice — `PromptRef.Pack` is
documented as a label — stated so that a later query does not group by name and
quietly average two organizations' different packs together. §4 draws the
consequence the label rule forces: a name cannot be a selector either.

Structurally, that means a pack **content** record is unique on
`(organization, scheme, digest)`. Making immutability a constraint rather than a
convention is the Phase 2 pattern (`configuration_records`' scope constraints,
`benchmark_reports`' claim), and the reason is the same: read-then-write is two
statements.

#### Immutable content, and a mutable installation beside it

That constraint has a consequence a previous version of this section did not
follow through. The name and the declared Maestro version range are **not** in the
digest (§1's projection), the content record is immutable, and it is unique on its
digest — so **correcting a mistyped name or a wrong declared range is impossible**:
the content has not changed, so a "new version" collides with the row that exists,
and the old row cannot be edited.

The fix is to stop storing two kinds of thing in one record:

- The **pack content record** is immutable, content-addressed, and holds the
  entries. It never changes and is never corrected, because there is nothing in it
  to correct that is not its content.
- The **pack installation record** references it and carries everything mutable:
  the display name, the declared Maestro version range, the **declared role
  coverage** (§5), who installed it and when. It **is** correctable, and
  correcting it is not a new pack.

**Cardinality: at most one installation per `(organization, content)`.** Without
that rule a selector naming a digest becomes ambiguous again the moment two
installations of the same content disagree about their declared coverage — which
is the defect round 2 closed for names, reappearing one level down. An
organization that wants two coverage declarations over the same prompts has two
packs, and since the entries would be identical, that is a signal the coverage
declaration is doing work the content should.

**Installations carry a monotonic revision, and resolution binds it.** The mutable
record governs dispatch — its declared range and coverage are checked in §5, its
name is recorded on the run — so *which revision governed* is part of what
happened, and a content reference cannot supply it. Two consequences:

- **Updates use optimistic concurrency on that revision.** Read-then-write is two
  statements, which is the Phase 2 rule this ADR has already applied twice.
- **Resolution records the installation identity and revision**, and snapshots
  every metadata value that affected the decision, not only the name. A later
  correction then cannot make a past run's dispatch look like it was decided on
  facts that did not yet exist.

**The compatibility validators run on updates, not only on creation** (§5).
Changing a declared role coverage or version range is exactly as capable of
producing an unusable installation as creating one, and an installation that was
valid when written is not thereby valid when edited.

This also puts the name where it belongs. A name is organization-scoped and a
label; a content digest is neither.

**MVP records no version label at all**, and a previous version of this section
imposed a `(name, version)` uniqueness constraint over a `version` it never
defined. A label without a defined ordering is a name with extra steps: it cannot
be selected on (§4), it cannot be compared, and its only effect would be to make
two versions of `default` look distinguishable to a reader who then cannot act on
the distinction. Version labels, their ordering, and channels arrive together with
the registry semantics in [candidate 9](../v2/notes_adr-backlog.md). Until then a
pack version is identified by its digest and labelled by its installation, and
nothing pretends otherwise.

### 2. Four facts, separately recorded, and two of them may be absent

The `prompt_pack_id` debt resolves into distinct facts about a run's P, all
recorded on the principal instance:

| Fact | Always present for an agent principal? | Purpose |
| --- | --- | --- |
| **Pack name** — the installation's label as it stood at resolution | Yes | Human handle only. **Never a selector and never a comparison key** (§1, §4) |
| **Content digest, with its scheme in its own field** | Yes | The identity. The key every MPH comparison groups on, within its scheme (§1). **A selector may name a version by this** |
| **Pack content reference** — a pointer to the plane's own record | **Nullable on the record; required at dispatch** | Resolvability: the pack's entries. **A selector may name a version by this** |
| **Installation identity and revision**, with the decision-affecting metadata snapshotted | **Nullable on the record; required at dispatch** | Which mutable record governed this dispatch, at which revision (§1) |

**The two references are nullable for imports, and only for imports.** A principal
instance may record a pack the plane does not own — an imported benchmark run
against a target Maestro did not configure is exactly that case, and it is not an
error. Forcing foreign keys onto every row would require fabricating records for
prompts the plane never held, which is the same class of dishonesty as reporting a
missing metric as zero (ADR 0025's four-state semantics). A **foreign pack** is
recorded by name and legacy-scheme digest, with neither reference, and says so.

**A live Maestro dispatch is not that case.** Its principal instance MUST carry
both, and they MUST agree with the recorded name, digest, and organization.
Nullable-because-imports and nullable-in-general are different claims, and only
the first is made here — a run Maestro itself configured against a pack it cannot
resolve is a defect, not a foreign pack.

Which columns Phase 3's migration keeps, adds, or retypes is the migration's
business; what this ADR fixes is that these facts are recorded separately rather
than collapsed into one column, that the digest carries its scheme in a field of
its own, that the governing installation is identified by revision and not only by
the content it points at, and that the references are absent only for records the
plane imported rather than produced.

### 3. P identifies the pack, not the prompt

The text a model actually receives is not the pack. It also contains the seeding
artifacts, generated tool documentation, and repository facts. A reader who takes
`prompt_hash` to mean *what the model saw* is wrong twice over: it is not derivable
from the pack, and it would change on every call.

**The pack digest closes over pack entries, and over nothing else.** A first
version of this section pulled everything that shapes a rendered model input into
"P's closure," which contradicted P's own definition one section earlier and would
have made the digest depend on the seeding set and the conversation. It does not
and must not: a pack's identity has to be computable before any run exists.

What that first version was reaching for is a different thing, and it needs its own
name.

**Invocation provenance** is the account of what shaped a model input, and it is
assembled from four recorded sources, of which the pack is only one:

| Source | What it covers | Where it is recorded |
| --- | --- | --- |
| **P** — the pack | Role and system prompts, state templates, review prompts | The pack digest (§1, §2) |
| **H** — the harness | Generated tool documentation, rendering machinery, workflow shape | The **Maestro version and** the harness config hash — [ADR 0021](0021-artifacts-and-principal-instances.md) makes both H, since the app is the harness and the binary's version belongs in it. A previous version of this table named only the hash, which would leave a tool-documentation change shipped in a new binary unattributed |
| **The seeding set** | The artifacts the instance was started with | `principal_instance_inputs` ([ADR 0021](0021-artifacts-and-principal-instances.md)) |
| **Accumulated turn material** | Conversation, tool results, retrieved knowledge, repository facts | Audit records ([ADR 0022](0022-v2-data-plane.md)) |

**The completeness rule attaches to invocation provenance, not to P.** Anything
that shapes a model input must be attributable to one of the four. A contribution
attributable to none makes the **invocation provenance unclosed** — the same
discipline [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §4
applies to a spec projection whose provider cannot enumerate its closure, and the
same one v1's prompt manifest applies by hand today when it refuses an entry that
matches nothing.

**Closure is not knowable once, at the start.** A previous version of this section
implied it was, and an invocation begins before most of what it will contain
exists: the conversation, the tool results, the retrieved knowledge, and the
repository facts all accumulate afterwards. A status computed at invocation start
would be a statement about the *plan* for the run, asserted as though it described
the run.

Two different things, recorded at two different times:

- **At invocation start, the expected source contract** — which of the four
  sources this runtime will report, and by what mechanism. A claim about
  capability, made before there is anything to account for.
- **At each model call** — or on the evolving execution result, whichever A4's
  shape prefers — **the bindings, and then the status.**

**A status without bindings is not provenance.** `closed` asserts that everything
in the call was attributable; it does not say *to what*, so it cannot be checked,
cannot be used to reconstruct a call, and is only as true as whoever wrote it.
Each call therefore binds **the exact contributing source references and digests**
— the pack identity, the H pair, the seeding-set artifact digests, and references
to the specific messages, tool results, and retrievals that entered this call —
and the status is a statement *about that set*. Bindings first; the status is the
conclusion drawn from them.

**Unclosed is a capability judgment, not a runtime category.** A previous version
of this section said an adapted external runtime is *permanently* unclosed, which
is one step wider than the evidence: what makes a runtime unclosed is that **its
adapter cannot supply trustworthy bindings**, not that it is external. Today's
Claude Code path is unclosed on that test — it assembles context internally, and
under [ADR 0030](0030-tool-execution-policy-hook.md) its in-resource actions are
unmediated and never enter Audit, so nothing observes its turn material. An
adapter that emits the events, or a runtime that reports its own assembled
context in a form the plane can bind, is closed on the same test. The distinction
matters because the first framing makes the gap permanent by definition and gives
no adapter author anything to build toward.

Recording a call as closed because nothing contradicted the claim is precisely the
failure the status exists to prevent.

**Closure and availability are separate states, because bindings rot.** The
messages, tool results, and retrievals a call binds are **Audit** data, and
[ADR 0021](0021-artifacts-and-principal-instances.md) makes Audit storage
truncatable by design, subject only to retention pins. So a call can be correctly
marked closed and, months later, have bindings that no longer resolve — and the
reconstruction property this section claims would be quietly false, with the
status still asserting otherwise.

Conflating the two would force a bad choice: either pin every source of every call
forever, which contradicts the retention posture Audit was given, or accept a
`closed` that means less over time. They are different facts:

- **Closure** is a statement about what was true when the call was made. It is
  fixed at that moment and never changes.
- **Availability** is a statement about now, and it degrades as retention runs.

A closed call whose sources have been pruned is *closed and unreconstructible* —
which is an honest and useful thing to be able to say, and is unsayable with one
field. Pinning then becomes a policy choice layered on top for the calls whose
provenance must stay reconstructible, and ADR 0021 already has the mechanism:
evidence packages retention-pin what they cite, so a call cited as evidence keeps
its sources by the existing rule rather than a new one.

**A4 owns the rule**, as it owns the bindings themselves. What this ADR fixes is
that closure must not be read as availability.

**Where the bindings and the states live is A4's, not this ADR's.** §2's table is
about the pack and deliberately has no closure field, because a pack is never
unclosed — its entries are its closure. **This ADR hands A4 four obligations
explicitly** rather than inventing columns for them here: the expected source
contract on the invocation, the per-call source bindings, the per-call closure
status drawn from them, and the retention rule that keeps availability a separate
state from closure. The invocation schema is
[backlog candidate 13](../v2/notes_adr-backlog.md), which already carries
prompt-pack identity.

**And the rule is a review obligation, not an enforced one.** The schema cannot
tell that a fifth source of model-input text has appeared; v1 pays for the same
gap with a hand-maintained manifest and a reviewed allowlist. What a pack buys is
that one of the four boundaries is now explicit enough for a reviewer to apply the
rule — not that anything applies it automatically.

### 4. Resolution happens once, at dispatch

**Which pack a run uses is decided once, deterministically, at dispatch.** The
resolved name, digest, and reference are written into the invocation and are
immutable for the life of the execution.

Resolution is Orchestrator machinery: it reads an explicit selection or falls back
through scoped configuration, and neither step requires judgment.

#### A selector names exactly one version, so it is never a name

§1 makes the name a non-unique label and makes every edit a new version. Both
together mean a name **cannot be a selector**: the moment a second version exists
under one name, "which pack" has no deterministic answer, and a first version of
this section said configuration selects by name anyway.

**An MVP selector identifies exactly one plane-owned pack version**, by record
reference or by scheme-qualified digest. Nothing else is a selector. Resolving a
human-friendly expression — *the newest version of `default`*, a version range, a
channel — needs the version ordering and registry semantics that are
[candidate 9](../v2/notes_adr-backlog.md), and is deferred with them. The name
stays what §1 says it is: a label carried for humans, recorded on the run,
and validated for agreement (§2), never dereferenced.

**Inputs, in precedence order:**

1. An **explicit selector carried on the dispatch** — how a benchmark
   configuration, a Workbench session, or a Work Group receives a non-default pack.
2. **Scoped configuration**, most-specific-wins over the
   organization/product/repository lineage, through the Phase 2 configuration
   records and their key registry (`internal/dataplane/configkeys`). The value it
   holds is a selector in the sense above, not a name. Pack selection registers a
   key there; it does not invent a second selection mechanism.
3. Failing both, **no pack resolves and dispatch fails** — see §6.

This is what makes "the reference is required at dispatch" (§2) reachable rather
than aspirational: resolution produces a version, so the invocation always has one
to carry.

**A named limit, so it is not discovered later.** `configuration_records`' scope arc
is organization/product/repository, which is deliberately *not* the artifact scope
arc and has no Epic or Story level. A Work Group is Epic-grained, so an Epic- or
Story-scoped pack choice is not expressible as configuration today and must arrive
as an explicit selection on the dispatch. That is sufficient for Phase 3 and is
stated rather than implied.

**Agent restart reuses the resolved pack; it does not re-resolve.** ADR 0029 §2
scopes the Incubator to the Story execution rather than to the agent principal, so
a replacement agent resumes the same execution — and if it re-resolved, a
configuration edit between the crash and the restart would change a factory lever
mid-Story with nothing recording that it had happened. One Story would span two P
values and the MPH signature would be honest about neither. This is what makes
single-owner agent restart ([#265](https://github.com/SnapdragonPartners/maestro/issues/265))
a lifecycle question here too.

**A pack change takes effect at the next dispatch.** That is not a limitation to
work around; it is the amendment path. Version-bound dispatch
([ADR 0019](0019-orchestrator-boundary.md) as amended) already re-evaluates and
reissues on amendment, and A5 governs work already executing. A lever that could
change under a running execution would be a silent swap with no version behind it.

### 5. Compatibility is validated against the running harness, and fails closed

**Slot coverage is necessary and is not compatibility.** A first version of this
section gated on coverage alone and then called the declared Maestro version
advisory *because* coverage passes — which does not follow. A pack can supply every
named slot and still be broken against the running harness: a template referencing
a variable the harness no longer supplies, a slot documenting a tool contract that
has changed, or syntax the current renderer rejects.

So the gate is the **pack-to-harness contract**, checked in three parts against the
harness version that will actually run:

1. **Coverage** — every slot the code requires for the roles in question is
   present.
2. **Parse** — every entry parses under the running renderer.
3. **Variable and render contract** — each slot's referenced variables are ones the
   harness supplies for that slot, and nothing it requires is missing.

**Which roles, though?** Coverage is defined against roles, and **at import no
execution exists**, so a previous version of this section asked for a check with
no subject. The answer is a declaration and a split:

**A pack installation declares the roles it covers.** That declaration is the
subject import validates against, and it is metadata on the installation record
(§2) rather than content, so it is correctable.

| Check | At import | At dispatch |
| --- | --- | --- |
| Parse | **Yes**, every entry | Re-run when the harness version has moved |
| Variable and render contract | **Yes**, every entry | Re-run when the harness version has moved |
| Coverage of the **declared** roles | **Yes** — the entries present must satisfy every slot those roles require | — |
| The execution's roles are **within** the declared set | — | **Yes**, and this is the check import cannot make |

Import therefore rejects a pack that is internally inconsistent — it claims to
cover the Architect and has no Architect slots — and dispatch rejects a pack that
is consistent but does not cover *this* execution. Neither check subsumes the
other, and running only the second would let a broken pack sit in the plane until
the first run that needs it.

**At dispatch rather than at render**, because a fault discovered at render time
has already cost a lease, a provisioned resource, and tokens, and it surfaces as a
mid-run failure instead of a configuration error.

**And on installation updates, not only on creation.** Editing a declared role
coverage or version range can make an installation unusable exactly as creating a
bad one can, and an installation validated when written is not thereby valid when
edited (§1).

**The declared Maestro version range can refuse, never authorize.** Pillar 10's
*supported Maestro version* is recorded and is enforced in one direction only: a
pack declaring itself incompatible with the running version is refused without
further work. A pack declaring itself compatible has made a claim, not passed a
check, and the three-part validation above still runs. That is the honest reading
of a self-declared range — it is evidence of intent from the pack's author and
evidence of nothing about the harness it now faces.

### 6. Storage: a data-plane family, seeded by import, with no run-time fallback

**Packs are a first-class data-plane family, not artifacts.** They are a factory
lever — an input to runs, like a model — rather than a work product, and
[ADR 0021](0021-artifacts-and-principal-instances.md)'s Management category carries
the review invariant with it. Making a pack a Management artifact would require
every pack edit to be reviewed by a non-author, which may well be desirable and has
not been decided; it belongs with candidate 9's registry. An agent-authored pack
would be a different case, and it is deferred with the rest.

The pack record and its entries are relational rows. Pack content is text and
small; the object module ([ADR 0022](0022-v2-data-plane.md)) is for binaries.

**The built-in default pack is imported, not embedded-at-run-time.** Phase 3 must
move v1's compiled-in templates into the plane, and the binary is a convenient
source for that content — as the source of a *version*, verifiable against the
binary that supplied it. Once imported, the plane's record is canonical.

**Importing is not selecting, and it happens per organization rather than per
deployment.** A previous version of this section put both at "bootstrap," which is
wrong in two ways that compound. It stopped at the import, which combined with §4
— where resolution fails if neither dispatch nor scoped configuration supplies a
selector — would have left **every ordinary dispatch failing on a freshly
bootstrapped plane**, with a perfectly good default pack sitting unreferenced in
it. And it addressed the omission to a step that has no organization to address:
content rows and configuration records are both organization-scoped (§1, §4), a
deployment bootstrap runs before any organization exists, and a cloud deployment
creates organizations for a long time afterwards.

So the two are split along the boundary that already exists:

- **Deployment bootstrap** does nothing pack-specific. The binary carries the
  built-in pack; nothing is written.
- **Organization provisioning** — creating an organization, or first initializing
  one that predates this work — performs both steps, idempotently:
  1. Import the built-in pack content into that organization, deriving its
     identity under `pack-jcs-sha256-v1` (§1), and create its installation
     record (§2).
  2. **Write the organization-scoped configuration record** whose value is a
     selector naming that content — a reference or scheme-qualified digest, per §4.

That keeps every path through §4 intact: no name-based fallback, no binary
fallback, and one deterministic selector that resolution finds by the ordinary
route.

**Upgrades change nothing by themselves, for either kind of organization.**

- **Existing organizations** keep their selector, and the new built-in version is
  not imported for them until someone selects it — at which point the import
  happens as part of that explicit act. No unreferenced rows accumulate, and no
  lever moves without a decision.
- **Organizations provisioned after the upgrade** seed at the then-current
  built-in version, because that is what the binary carries.

The consequence is worth stating rather than discovering: **two organizations in
one deployment can default to different packs**, according to when each was
provisioned. That is the price of never moving a lever silently, and the honest
reading is that "the default pack" is a property of an organization's history, not
of the installed binary.

**There is no fallback to the binary at run time.** If no pack resolves, dispatch
fails (§4). A silent fallback would produce a run whose P names content the plane
does not hold and whose signature therefore cannot be resolved by anything reading
it later — the signature would be a lie in exactly the way ADR 0025's
`unavailable`-versus-zero rule exists to prevent. It would also leave two
authorities for the same content, which is the failure
[ADR 0017](0017-v2-documentation-authority-and-lifecycle.md) is built to avoid.

This is deliberately *not* the rule Phase 2 item 7 applied to configuration keys.
That registry ships no seed vocabulary because an unwritten key is a guess about a
future caller. A default pack has a present consumer — every Phase 3 run — and the
question here is not whether it exists but which copy is authoritative.

## Consequences

- **`principal_instances.prompt_pack_id` stops being a placeholder**, and the
  benchmark import keeps working — but **not unchanged**, which a previous version
  of this list claimed. The plane reference it never had is the field that stays
  optional, so no import is rejected; the importer must additionally set the legacy
  scheme on what it writes (§1), and the Phase 3 migration backfills the scheme on
  rows already imported. Digest bytes are never rewritten and
  [ADR 0025](0025-golden-stories-and-benchmark-runner.md)'s run-record contract is
  untouched.
- **MPH comparison becomes a real query, within a scheme.** Grouping on the
  scheme-qualified digest answers "which prompt content produced these costs"
  across runs and across the file-to-plane storage transition, which is what
  ADR 0025 promised and what a name-keyed column could not deliver. It does **not**
  put a v1-as-patched target's `v1-embedded` identity in a group with a v2 pack:
  those are different schemes over different domains, and a comparison across them
  would be arithmetic on unrelated hashes. Cross-target comparison of P remains
  something a human asserts with reasons, not something a query concludes.
- **A/B testing prompt versions against golden stories is a configuration change.**
  Two configurations differing only in their explicit pack selection produce two
  identity groups, and nothing else has to move.
- **Phase 3 pays a real conversion cost.** v1's templates carry v1 state names and
  v1 tool documentation; they are re-cut for v2 states in the port inventory
  regardless. Importing them as a pack is the same work with an explicit boundary
  around it, and the boundary is what retires v1's hand-maintained prompt manifest
  and its allowlist.
- **A pack edit cannot affect a running Story**, which is correct and will
  occasionally be inconvenient: fixing a bad prompt means a new dispatch, not a
  hot edit. Stated so it is designed for rather than discovered mid-incident.
- **Dispatch gains failure modes that did not exist**: no pack resolves, a selector
  names no plane-owned version, a pack declares itself incompatible with the
  running harness version, the execution's roles fall outside the pack's declared
  coverage, and a re-run contract check fails after a harness upgrade. All are
  configuration errors surfaced before any resource is leased, which is where they
  cost least — and the last is the one that will find real breakage, since a pack
  surviving a harness change is the ordinary assumption that quietly stops being
  true.
- **Import gains its own gate**, and it is not the same gate: parse and variable
  contract over every entry, plus coverage of the roles the installation declares.
  A pack that is internally inconsistent never reaches the plane; a pack that is
  consistent but wrong for a given execution is caught at dispatch.
- **Organization provisioning gains a step, and deployment bootstrap does not.**
  Provisioning imports the built-in pack into the new organization *and* seeds its
  selector; a deployment bootstrap writes nothing, because it has no organization
  to write for. Anything Phase 3 builds as a first-run path has to run per
  organization, including for organizations that predate this work.
- **Upgrades change no organization's prompts, and organizations drift apart.**
  An existing organization keeps its selector and does not even import the new
  built-in version until someone selects it; an organization provisioned after the
  upgrade seeds at the version the binary then carries. So two organizations in
  one deployment can default to different packs by age alone. That is the price of
  never moving a lever silently, and it means "the default pack" is a fact about
  an organization's history rather than about the installed binary.
- **Phase 3 owes the harness a declared per-slot variable contract.** §5's check is
  only as strong as that declaration, and today the contract is implicit in what
  each template happens to reference: `pkg/templates`' `Render` and
  `RenderWithUserInstructions` both take one `*TemplateData` carrying every field
  any template might want, so nothing states which variables a given template may
  use. Making that per-slot and explicit is new work this ADR creates.
- **A4 inherits four invocation-provenance obligations** — the expected source
  contract at invocation start, the per-call source bindings, the per-call closure
  status drawn from them, and the retention rule that keeps availability separate
  from closure — because §3 deliberately gives none of them a home here. If A4
  lands without them the rule has no field and reverts to prose.
- **Provenance acquires a second, decaying state.** A call's closure is fixed when
  it is made; whether its bound Audit sources still resolve is not, because Audit
  is truncatable by design (ADR 0021). *Closed and unreconstructible* is a real
  and reportable condition, and pinning is a policy laid on top for calls whose
  provenance must survive — which evidence packages already do by the existing
  retention-pin rule rather than a new one.
- **Today's adapted runtimes will be recorded as unclosed, and that is a statement
  about their adapters rather than about them.** Claude Code's context assembly is
  internal and its in-resource actions are unmediated under ADR 0030, so nothing
  observes its turn material and no binding can be produced. Anyone comparing such
  runs against a native agent's is comparing a partially-accounted-for invocation
  with a fully-accounted-for one, and the status is what tells them so — while
  leaving an adapter author a definite thing to build toward, which "external
  runtimes are permanently unclosed" would not.
- **The completeness rule is only as honest as it is applied.** Nothing in the
  schema prevents a Phase 3 change that injects model-input text from a fifth
  place, and no test will fail when one does. It is an untestable guarantee, stated
  beside the decision in §3 as well as here rather than implied to be covered.

### Deferred

[Candidate 9](../v2/notes_adr-backlog.md), as re-cut at the top of this ADR:
registry inheritance and overlays, the **user-facing install and browse
experience** and governance over who may install what, versioning and export
formats, repo-local packs, and skills. **Not** deferred, and stated here so the
line is unmistakable: the minimal installation record §2 requires — display name,
declared version range, declared role coverage, revision — which Phase 3 cannot
resolve a pack without.

Also deferred: agent-authored packs and their review posture; per-role packs drawn
from different sources within one run; pack evaluation history and changelog
surfaces (pillar 10 metadata beyond what identity needs); prompt material fetched
from outside the plane at run time; deduplicating identical content across
organizations; and migration of a pack's content between organizations.

## Related Documents

- [Pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md) item A3;
  [ADR backlog](../v2/notes_adr-backlog.md) candidates 5 (this ADR), 9 (registry
  expansion), and 13 (item A4 — the invocation schema that carries pack identity,
  and which §3 hands the invocation-provenance completeness status).
- [ADR 0021](0021-artifacts-and-principal-instances.md) (the MPH signature, the
  seeding set, Management versus Audit),
  [ADR 0022](0022-v2-data-plane.md) (data-plane families, the persistence seam),
  [ADR 0025](0025-golden-stories-and-benchmark-runner.md) (configurations
  identified by content hash; honest reporting of what is not available),
  [ADR 0028](0028-artifact-envelopes-and-payload-schemas.md) (canonical JSON
  digesting), [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §2 and
  §4 (execution scoped to the Story; declared closures and `unclosed`).
- [ADR 0030](0030-tool-execution-policy-hook.md) (item A2, reviewed as a set with
  this one).
- [Schema inventory](../v2/phase_2/inventory_schema-tables.md) (prompt packs as a
  deferred family, creator Phase 3);
  [port inventory](../v2/phase_0/inventory_v1-port.md) (`pkg/templates` rework);
  [roadmap](../v2/plan_roadmap.md) pillar 10 and D5.
