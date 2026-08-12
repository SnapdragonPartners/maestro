+++
title = "ADR 0031: Prompt Pack Identity, Resolution, And Storage"
edit_date = "2026-08-12"
status = "draft"
summary = "Fixes the minimal prompt-pack contract the MPH signature's P component has been carrying informally since Phase 1. A pack version is whole rather than an overlay, immutable, and identified by a digest over a JCS-canonicalized projection of its entries -- the container is normalized, prompt text never is, since whitespace is content -- carried with the scheme that produced it in a field of its own, because an sha256 prefix names an algorithm and not a scheme; no comparison crosses schemes, so an imported v1 identity stays opaque rather than joining a group with a v2 pack, and the Phase 3 migration backfills the legacy scheme onto rows already imported without rewriting a digest or touching the run-record contract. The name is a label and therefore cannot be a selector either: an MVP selector names exactly one plane-owned version by reference or scheme-qualified digest, a live dispatch must carry that reference while imported foreign runs are the only records permitted to omit it, and no version label exists at all until candidate 9 defines an ordering for it. The pack digest closes over pack entries and nothing else; what accounts for a model input is invocation provenance assembled from four recorded sources -- pack, harness, seeding set, and accumulated turn material -- and since most of that accumulates after an invocation begins, the expected source contract is recorded at the start while the actual status belongs to each model call, with a runtime whose context assembly is internal recorded as permanently unclosed. Both obligations are handed explicitly to the agent execution contract rather than given a column here. Resolution happens once and deterministically at dispatch, and the resolved identity is reused verbatim on agent restart, because re-resolving would let one Story span two P values invisibly. Packs are a data-plane family and not artifacts, the built-in default is imported at bootstrap with no run-time fallback to the binary because a silent fallback makes the signature a lie, and compatibility is validated at import and dispatch as coverage plus parse plus each slot's variable contract against the running harness -- a declared version range can refuse but never authorize. The debt on principal_instances.prompt_pack_id is three roles in one nullable text column, and its only writer today records a foreign pack the plane will never own."
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
inheritance, installed org-level packs, versioning and export, repo-local packs,
and skills remain [candidate 9](../v2/notes_adr-backlog.md).

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

Two things about that, and both were wrong in a first version of this section:

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

Structurally, that means a pack version is unique on `(organization, scheme,
digest)`. Making immutability a constraint rather than a convention is the Phase 2
pattern (`configuration_records`' scope constraints, `benchmark_reports`' claim),
and the reason is the same: read-then-write is two statements.

**MVP records no version label at all**, and a previous version of this section
imposed a `(name, version)` uniqueness constraint over a `version` it never
defined. A label without a defined ordering is a name with extra steps: it cannot
be selected on (§4), it cannot be compared, and its only effect would be to make
two versions of `default` look distinguishable to a reader who then cannot act on
the distinction. Version labels, their ordering, and channels arrive together with
the registry semantics in [candidate 9](../v2/notes_adr-backlog.md). Until then a
pack version is identified by its digest and labelled by its name, and nothing
pretends otherwise.

### 2. Three facts, separately recorded, and only one of them may be absent

The `prompt_pack_id` debt resolves into three distinct facts about a run's P, all
recorded on the principal instance:

| Fact | Always present for an agent principal? | Purpose |
| --- | --- | --- |
| **Pack name** — the declared label | Yes | Human handle only. **Never a selector and never a comparison key** (§1, §4) |
| **Content digest, with its scheme in its own field** | Yes | The identity. The key every MPH comparison groups on, within its scheme (§1). **A selector may name a version by this** |
| **Pack version reference** — a pointer to the plane's own record | **Nullable on the record; required at dispatch** | Resolvability: the pack's entries, metadata, and history. **A selector may name a version by this** |

**The reference is nullable for imports, and only for imports.** A principal
instance may record a pack the plane does not own — an imported benchmark run
against a target Maestro did not configure is exactly that case, and it is not an
error. Forcing a foreign key onto every row would require fabricating records for
prompts the plane never held, which is the same class of dishonesty as reporting a
missing metric as zero (ADR 0025's four-state semantics). A **foreign pack** is
recorded by name and legacy-scheme digest, with no reference, and says so.

**A live Maestro dispatch is not that case.** Its principal instance MUST carry the
reference, and the reference MUST agree with the recorded name, digest, and
organization. Nullable-because-imports and nullable-in-general are different
claims, and only the first is made here — a run Maestro itself configured against
a pack it cannot resolve is a defect, not a foreign pack.

Which columns Phase 3's migration keeps, adds, or retypes is the migration's
business; what this ADR fixes is that the three facts are recorded separately
rather than collapsed into one column, that the digest carries its scheme in a
field of its own, and that the reference is absent only for records the
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
| **H** — the harness | Generated tool documentation, rendering machinery, workflow shape | The harness config hash |
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
  shape prefers — **the actual status of what was assembled.** That is where the
  contribution either is attributable to a recorded source or is not.

**A runtime that cannot emit the necessary events is permanently unclosed, and
must say so.** This is not an edge case: an adapted external runtime assembles
context internally, and under [ADR 0030](0030-tool-execution-policy-hook.md) its
in-resource actions are not mediated and never enter Audit at all. So its
accumulated turn material is unobservable by construction, and its invocations are
unclosed for as long as that holds. Recording them as closed because nothing
contradicted the claim is precisely the failure the status exists to prevent.

**Where the status lives is A4's, not this ADR's.** §2's table is about the pack
and deliberately has no closure field, because a pack is never unclosed — its
entries are its closure. **This ADR hands A4 two obligations explicitly** rather
than inventing columns for them here: the expected source contract on the
invocation, and the per-call status. The invocation schema is
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

1. **Coverage** — every slot the code requires for the roles this execution will
   run is present.
2. **Parse** — every entry parses under the running renderer.
3. **Variable and render contract** — each slot's referenced variables are ones the
   harness supplies for that slot, and nothing it requires is missing.

**Validated at import and again at dispatch.** At import so a broken pack is
rejected when it is introduced rather than when it is first used; at dispatch
because the harness version may have moved since, and dispatch is the binding gate.

**At dispatch rather than at render**, because a fault discovered at render time
has already cost a lease, a provisioned resource, and tokens, and it surfaces as a
mid-run failure instead of a configuration error.

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
source for that content — as the source of a *version*, imported at bootstrap and
verifiable against the binary that supplied it. Once imported, the plane's record is
canonical.

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
  running harness version, and a pack fails the three-part contract check. All are
  configuration errors surfaced before any resource is leased, which is where they
  cost least — and the contract check is the one that will find real breakage,
  since a pack surviving a harness change is the ordinary case that quietly stops
  being true.
- **Phase 3 owes the harness a declared per-slot variable contract.** §5's check is
  only as strong as that declaration, and today the contract is implicit in what
  each template happens to reference: `pkg/templates`' `Render` and
  `RenderWithUserInstructions` both take one `*TemplateData` carrying every field
  any template might want, so nothing states which variables a given template may
  use. Making that per-slot and explicit is new work this ADR creates.
- **A4 inherits two invocation-provenance obligations** — the expected source
  contract at invocation start and the actual status per model call — because §3
  deliberately gives neither a home here. If A4 lands without them the rule has no
  field and reverts to prose.
- **Adapted external runtimes will be recorded as permanently unclosed**, and that
  is the correct result rather than a gap to close later. Their context assembly is
  internal and their in-resource actions are unmediated under ADR 0030, so nothing
  observes it. Anyone comparing an adapted runtime's runs against a native agent's
  is comparing a partially-accounted-for invocation with a fully-accounted-for one,
  and the status is what tells them so.
- **The completeness rule is only as honest as it is applied.** Nothing in the
  schema prevents a Phase 3 change that injects model-input text from a fifth
  place, and no test will fail when one does. It is an untestable guarantee, stated
  beside the decision in §3 as well as here rather than implied to be covered.

### Deferred

Everything in [candidate 9](../v2/notes_adr-backlog.md): registry inheritance and
overlays, installed org-level packs, versioning and export formats, repo-local
packs, and skills. Also deferred: agent-authored packs and their review posture;
per-role packs drawn from different sources within one run; pack evaluation history
and changelog surfaces (pillar 10 metadata beyond what identity needs); prompt
material fetched from outside the plane at run time; and migration of a pack's
content between organizations.

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
