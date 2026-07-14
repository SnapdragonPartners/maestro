+++
title = "Spike Report: maestro-cms Boundary And Adoption"
edit_date = "2026-07-13"
status = "live"
type = "spike"
summary = "What the v2 knowledge/document/binary work consumes from maestro-cms versus builds in Maestro. Recommendation: adopt cms for the ingestion pipeline; Maestro owns retrieval, citations, and packs; the generic graph primitive is contributed to cms per its ADR 0005 and consumed back, with Maestro keeping only ontology and policy."
+++

# Spike Report: maestro-cms Boundary And Adoption

Status: live — approved by Codex and DR, 2026-07-13. Phase 0 item 13. Question: what should the v2 knowledge, document, and binary work (Phases 2 and 6) consume from `maestro-cms` versus build in Maestro?

## Method

Code survey of `maestro-cms` (full package inventory, docs and ADRs, dependency and maturity check) and Maestro's `pkg/knowledge` (~1,190 non-test LOC), plus an overlap map against the v2 capability list. Maestro does not currently import maestro-cms anywhere — the modules are fully decoupled today. No code written; no refactor performed (spike rules).

## Findings

**The two codebases are complementary, not competing.** maestro-cms built storage-neutral ingestion primitives and *deferred by its own ADRs* exactly what `pkg/knowledge` built (retrieval indexing, the graph); `pkg/knowledge` lacks everything cms provides. There is no duplication to resolve — only a seam to draw.

- **What cms provides today** (v0.4.0, pre-1.0, near 1:1 test coverage, active): `extract` (PDF/DOCX/PPTX/HTML/Markdown/text via a registry; the shipped PDF preset is `pdftotext`-only; a pure-Go engine exists but only via an explicitly configured registry), `chunk` (pure, token-budgeted, heading-aware), `embed` (batching/bisect orchestration over `maestro-llms`'s `EmbeddingClient` — no provider code of its own, per its ADR 0001), `content` (single-parent provenance trees with caller-assigned IDs and SHA-256 helpers), and `store.ObjectStore` (a four-method, opaque-key byte interface; GCS adapter shipped as an isolated subpackage).
- **What cms deliberately lacks**: indexed/vector retrieval (`index/*` named but unbuilt), any database, a graph primitive (deferred to its v2 — its own notes cite Maestro's knowledge graph as a design source), image/OCR and XLSX extraction, and any citation-formatting layer.
- **Alignment with Accepted v2 doctrine is structurally clean.** cms owns no storage: `StoreHandle{Backend,Key}` backs naturally onto ADR 0022's digest-addressed object module (`SHA256HexReader` produces the key; `testcms.MemoryStore` is a reference adapter), `embed.Record` and provenance trees persist through the persistence seam into Postgres, and cms pins the same `maestro-llms` version Maestro does. One convention gap: cms store keys are *opaque, not digest-enforced* — content-addressing is a discipline Maestro must impose (or upstream, below).
- **`pkg/knowledge` (v1)**: a DOT-graph repository artifact projected into SQLite FTS5, with a hardcoded ontology, raw `*sql.DB` coupling, and exactly two consumers. Worth salvaging as design seeds: the DOT parse/serialize and `Subgraph(depth)`/`Filter` traversal, and the story-scoped knowledge-pack pattern (which the roadmap's knowledge-pack flow keeps). The ontology-as-CHECK-constraints and direct DB coupling are what v2 replaces.
- **Runtime note**: the shipped document preset registers *only* the `pdftotext` engine and returns `ErrEngineUnavailable` when Poppler is absent — the pure-Go engine is an explicitly configured alternative registry, not an automatic fallback. For Maestro: the bootstrap image adds `poppler-utils`, and any non-container path must build a custom registry with the pure-Go engine or fail loudly.
- **Two artifact models, one promotion boundary**: cms `content.Artifact` is a single-parent *ingestion-domain* record — it is not an ADR 0021 Maestro artifact and must not be treated as one. The mapping: cms sources, artifacts, chunks, and embedding records live as ingestion/knowledge data in the data plane, with `DerivedFrom` preserving extraction lineage; anything **promoted into an agent seed** (a knowledge pack) becomes a reviewed Maestro Management artifact carrying scope, principals, MPH signature, lifecycle, and the full input-digest set (ADR 0021). Raw ingestion output never seeds work unreviewed — the promotion boundary applies to ingestion exactly as it applies to message exhaust.

## Governing principle

Same as the toolloop spike: maestro-cms is a shared, open-source package with multiple consumers (closed-source; not referenced here), of which Maestro is first-class. DRY — consume what exists; upstream-first — anything generally useful is at least considered for the package, preferring non-breaking additions; never fork.

## Recommendation

**Adopt maestro-cms as the ingestion pipeline; Maestro owns retrieval, citations, and packs; the generic graph primitive is contributed to cms and consumed back.**

1. **Consume, don't rebuild**: `extract` → `chunk` → `embed.Run` becomes the v2 ingestion pipeline (Phase 6 knowledge; early use for pillar-13 uploads as needed), with `content` provenance trees persisted as the ingestion lineage. Maestro implements `store.ObjectStore` over the ADR 0022 digest-addressed object module (Phase 2's attachment work).
2. **Maestro-side (harness- or convention-specific)**: the retrieval/index layer in Maestro's own Postgres (FTS + pgvector-class), citation formatting and verification (knowledge packs are Management artifacts under Maestro's review conventions), knowledge-pack assembly and the promotion boundary above, and — for the graph — Maestro's *ontology, persistence composition, population, and workflow policy* only.
3. **Upstream-candidacy sort** (wishlist: [requirements_maestro-cms-wishlist.md](../requirements_maestro-cms-wishlist.md)):
   - **Convention/API request**: digest-addressed keying for `ObjectStore` — with honest streaming semantics (see wishlist item 1; a naive wrapper cannot hash and then re-consume a non-seekable reader).
   - **Contribution offer**: Maestro's Phase 6 retrieval layer built to fit cms's deferred `index/*` shape, contributed as the first index adapter once proven.
   - **Planned contribution, per cms's own assignment**: the generic graph primitive. cms ADR 0005 and its v2 notes already assign the generic directed graph, caller-supplied schema validation, traversal, and subgraph extraction to cms — Maestro keeps only its ontology and policy. Phase 6's graph work is therefore *built as a cms contribution and consumed back*, with `pkg/knowledge`'s traversal as a design seed — not a Maestro-owned graph with upstream aspirations.
   - **Later request**: image/OCR extraction (Maestro's pillar-13 uploads include images and diagrams); XLSX per cms's own deferral.
4. **D8 inventory input**: `pkg/knowledge` disposition confirmed as **rewrite**, now with a precise shape — ingestion is consumed from cms; retrieval and packs are new Maestro code, the generic graph primitive is built as a cms contribution (consumed back, Maestro keeping ontology and policy) with the DOT traversal and pack pattern as salvaged design seeds; the SQLite FTS5 indexer and hardcoded ontology are dropped.
5. **No Phase 1 impact.** First adoption point is Phase 2's attachment/object work; the pipeline proper is Phase 6.

No spike scripts were produced (reading spike); `spikes/phase_0/` is not needed for this item.

## Related Documents

- [Phase 0 plan](scope-and-plan.md) item 13; [roadmap](../roadmap.md) pillars 11 and 13, D8; [toolloop spike](spike_toolloop.md) (the shared-package principle and playbook).
- ADRs [0022](../../adr/0022-v2-data-plane.md) (digest-addressed object module, persistence seam), [0021](../../adr/0021-artifacts-and-principal-instances.md) (knowledge packs as Management artifacts).
- Historical note [0012](../../adr/0012-knowledge-graph-as-repository-artifact.md) (the v1 design `pkg/knowledge` implements); maestro-cms docs/ADRs 0001–0010.
