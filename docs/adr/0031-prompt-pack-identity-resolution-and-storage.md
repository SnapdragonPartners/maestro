+++
title = "ADR 0031: Prompt Pack Identity, Resolution, And Storage"
edit_date = "2026-08-12"
status = "draft"
summary = "Fixes the minimal prompt-pack contract the MPH signature's P component has been carrying informally since Phase 1. A pack version is whole rather than an overlay, immutable, and identified by a content digest over a normalized projection of its entries; the pack name is a label and never a comparison key. P identifies the pack, not the prompt -- tool documentation, the seeding set, and repository facts also shape prompt text, so every contribution must be attributable to a recorded MPH component and one attributable to none makes P unclosed. Resolution happens once and deterministically at dispatch from an explicit override or scoped configuration, and the resolved identity is carried in the invocation and reused verbatim on agent restart, because re-resolving would let one Story span two P values invisibly; a pack change takes effect at the next dispatch, which is the amendment path rather than a silent swap. Packs are a data-plane family and not artifacts, the built-in default is imported at bootstrap with no run-time fallback to the binary because a silent fallback makes the signature a lie, and coverage of the slot set the code requires is validated at dispatch and fails closed, with the declared Maestro version range kept as advisory metadata since coverage is the fact the range proxies for. The debt on principal_instances.prompt_pack_id is three roles in one nullable text column, and its only writer today records a foreign pack the plane will never own, so the plane-record reference is a separate and optional field rather than a foreign key forced onto every row."
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

**Identified by content.** The identity of a pack version is a **digest over a
normalized projection of its entries** — canonical ordering, slot keys, and
contents — computed with the same RFC 8785 JCS discipline
[ADR 0028](0028-artifact-envelopes-and-payload-schemas.md) applies to payloads.
Normalization exists so that cosmetic differences do not mint a new identity and
semantically equal packs digest equal.

**The name is a label, not a key.** `default` is a handle for humans and for
configuration; it is not what two runs are compared on, and it is not unique
across organizations. Every MPH comparison keys on the digest. This is the rule
Phase 1 already follows in practice — `PromptRef.Pack` is documented as a label —
stated so that a later query does not group by name and quietly average two
organizations' different packs together.

Structurally, that means a pack version is unique on its content digest within an
organization, and a `(name, version)` label pair may never be reused for different
content. Making immutability a constraint rather than a convention is the Phase 2
pattern (`configuration_records`' scope constraints, `benchmark_reports`' claim),
and the reason is the same: read-then-write is two statements.

### 2. Three roles, three fields, and the reference is the optional one

The `prompt_pack_id` debt resolves into three distinct facts about a run's P, all
recorded on the principal instance:

| Fact | Always present for an agent principal? | Purpose |
| --- | --- | --- |
| **Pack name** — the declared label | Yes | Human handle, and what configuration selects by |
| **Content digest** | Yes | The identity. The key every MPH comparison groups on, and the only one of the three that means the same thing for a pack the plane owns and one it does not |
| **Pack version reference** — a pointer to the plane's own record | **No** | Resolvability: the pack's entries, metadata, and history |

**The reference is optional by design, not by omission.** A principal instance may
record a pack the plane does not own — an imported benchmark run against a target
Maestro did not configure is exactly that case, and it is not an error. Forcing a
foreign key onto every row would require fabricating records for prompts the plane
never held, which is the same class of dishonesty as reporting a missing metric as
zero (ADR 0025's four-state semantics). A **foreign pack** is recorded by name and
digest, with no reference, and says so.

Which columns Phase 3's migration keeps, adds, or retypes is the migration's
business; what this ADR fixes is that the three facts are three fields and that
the reference is nullable.

### 3. P identifies the pack, not the prompt

The text a model actually receives is not the pack. It also contains the seeding
artifacts, generated tool documentation, and repository facts. A reader who takes
`prompt_hash` to mean *what the model saw* is wrong twice over: it is not derivable
from the pack, and it would change on every call.

**P identifies the pack.** The rest of the prompt is covered by other components of
the same signature — tool documentation is generated from the tool surface, which
is **H** by the roadmap's own definition of the harness lever; the seeding set is
recorded separately as the instance's input artifact digests
([ADR 0021](0021-artifacts-and-principal-instances.md)). The signature is complete
only when the three are read together, and no single component was ever supposed
to carry the whole prompt.

**The closure rule.** Anything that shapes prompt text must be attributable to a
recorded MPH component. A contribution attributable to none makes **P unclosed**,
and an unclosed P is recorded as unclosed rather than digested as though it were
complete — the same discipline
[ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §4 applies to a
spec projection whose provider cannot enumerate its closure, and the same one v1's
prompt manifest applies by hand today when it refuses an entry that matches
nothing.

**This rule is a review obligation, not an enforced one**, and the difference is
worth stating where the rule is rather than only in the consequences. The schema
cannot tell that a fourth source of prompt text has appeared; v1 pays for the same
gap with a hand-maintained manifest and a reviewed allowlist. What a pack buys is
that the boundary is explicit enough for a reviewer to apply the rule — not that
anything applies it automatically.

### 4. Resolution happens once, at dispatch

**Which pack a run uses is decided once, deterministically, at dispatch.** The
resolved name, digest, and reference are written into the invocation and are
immutable for the life of the execution.

Resolution is Orchestrator machinery: it reads an explicit selection or falls back
through scoped configuration, and neither step requires judgment.

**Inputs, in precedence order:**

1. An **explicit selection carried on the dispatch** — how a benchmark
   configuration, a Workbench session, or a Work Group receives a non-default pack.
2. **Scoped configuration**, most-specific-wins over the
   organization/product/repository lineage, through the Phase 2 configuration
   records and their key registry (`internal/dataplane/configkeys`). Pack selection
   registers a key there; it does not invent a second selection mechanism.
3. Failing both, **no pack resolves and dispatch fails** — see §6.

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

### 5. Coverage is validated at dispatch, and fails closed

A pack declares the slots it covers. Resolution checks that declaration against the
slot set the code requires for the roles the execution will run, and **refuses to
dispatch** on a gap.

At dispatch rather than at render, because a pack missing a slot discovered at
render time has already cost a lease, a provisioned resource, and tokens, and it
surfaces as a mid-run failure instead of a configuration error.

Roadmap pillar 10's *supported Maestro version* stays as recorded metadata and is
**advisory**, not the gate. A declared version range is a proxy for the property
that actually matters, and the property is directly checkable: a pack declaring
compatibility while missing a slot is still broken, and a pack outside its declared
range that covers every slot still runs. Record the claim; gate on the fact.

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

- **`principal_instances.prompt_pack_id` stops being a placeholder** and the
  benchmark importer keeps working unchanged, because the plane reference it never
  had is the field that stays optional. Phase 3's migration creates the family; no
  Phase 2 record needs rewriting.
- **MPH comparison becomes a real query.** Grouping on the pack digest answers
  "which prompt content produced these costs" across runs, targets, and the
  file-to-plane storage transition, which is what ADR 0025 promised and what a
  name-keyed column could not deliver.
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
- **Dispatch gains two failure modes that did not exist**: no pack resolves, and a
  resolved pack does not cover the required slots. Both are configuration errors
  surfaced before any resource is leased, which is where they cost least.
- **The MPH signature is only as honest as §3's closure rule is applied.** Nothing
  in the schema prevents a Phase 3 change that injects prompt text from a fourth
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
  [ADR backlog](../v2/notes_adr-backlog.md) candidates 5 (this ADR) and 9
  (registry expansion).
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
