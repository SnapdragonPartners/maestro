+++
title = "Maestro Wishlist For maestro-cms"
edit_date = "2026-07-13"
status = "live"
type = "requirements"
summary = "Maestro's running feature-request wishlist to the maestro-cms team, with generality arguments and a response field per item — the sibling of the maestro-llms wishlist, originating from the Phase 0 cms spike."
+++

# Maestro Wishlist For maestro-cms

Status: live (approved by Codex and DR, 2026-07-13) — awaiting maestro-cms team responses.

`maestro-cms` is a shared, open-source, general-purpose package; Maestro is a first-class consumer among several. Same rules as the [maestro-llms wishlist](requirements_maestro-llms-wishlist.md): DRY, upstream-first, non-breaking preferred, never fork; each item carries a generality argument and a **Response** field. Items originate from the Phase 0 cms spike ([spike report](phase_0/spike_cms.md)).

## 1. Digest-addressed keying for `store.ObjectStore`

- **What**: either a documented convention (keys are content digests; the consumer guarantees it) or — better — a key-returning API with explicit streaming semantics, e.g. `PutDigest(ctx context.Context, s store.ObjectStore, r io.Reader) (key string, err error)` as a **package-level helper, wrapper type, or new optional interface — never a method added to `ObjectStore` itself**, which would break every existing adapter and fake. Note the streaming problem honestly: `Put` takes the key *before* consuming its reader, while `SHA256HexReader` consumes the stream, so a thin wrapper cannot hash-then-write a non-seekable reader without buffering, spooling to temp, or write-then-verify-with-rollback. The API should state which it does.
- **Why (Maestro)**: our data plane requires content-addressed binaries (retention pinning and reference verification hang off digests). Today cms keys are explicitly opaque, so content-addressing is a discipline each consumer must impose — and implement the streaming mechanics for — alone.
- **Generality**: dedup, integrity verification, and cache-friendly addressing benefit any consumer; an optional API preserves opaque keys for consumers that want them.
- **Breaking-ness**: non-breaking only in the helper/wrapper/optional-interface form; widening `ObjectStore` would be interface-breaking and is not requested.
- **Response**: _pending_

## 2. Contribution offer: the first `index/*` adapter

- **What**: cms's README defers indexed retrieval to optional `index/*` subpackages, currently unbuilt. Maestro will build a Postgres retrieval layer (FTS + vector) for its knowledge work in Phase 6 and offers to shape it to cms's intended `index/*` contract and contribute it once proven in use.
- **Why (Maestro)**: we need it regardless; building it to the package's shape costs little and ends the "cms hands back vectors, everyone builds retrieval alone" gap.
- **Generality**: retrieval over embedded chunks is the obvious next need for every cms consumer.
- **Breaking-ness**: new optional subpackage; non-breaking. Requires the cms team to define (or bless) the `index/*` contract first.
- **Response**: _pending_

## 3. Planned contribution: the generic graph primitive

- **What**: cms ADR 0005 and its v2 notes assign the generic directed graph — caller-supplied schema validation, traversal, subgraph extraction — to cms, citing Maestro's v1 knowledge graph as a design source. Maestro's Phase 6 graph work will be **built as a contribution to cms** against that assignment and consumed back; Maestro keeps only its ontology, persistence composition, population, and workflow policy. We ask the cms team to confirm the intended contract (or sketch it) before Phase 6 so the contribution lands where it belongs on the first try.
- **Why (Maestro)**: cms already claimed this ground; building a Maestro-owned graph "for later upstreaming" would create exactly the divergence the assignment exists to prevent.
- **Generality**: by cms's own scoping, this is core package territory.
- **Breaking-ness**: new package in cms per its own v2 plan; non-breaking.
- **Response**: _pending_

## 4. Image/OCR extraction — later

- **What**: an `extract` entry for images (OCR), and XLSX per cms ADR 0009's deferral, when priorities allow.
- **Why (Maestro)**: our intake accepts uploads including images and diagrams (roadmap pillar 13); today those would bypass the extract pipeline entirely.
- **Generality**: any document-ingesting consumer eventually meets images; the registry design already anticipates new extractors.
- **Breaking-ness**: additive extractors; non-breaking. Explicitly not urgent for Maestro before Phase 6.
- **Response**: _pending_

## Non-requests

Stays Maestro-side by our own analysis: knowledge-pack assembly and citation formatting/verification (Management artifacts under Maestro's review conventions), retrieval policy and ranking, the persistence of `embed.Record`s and provenance trees (our data plane, behind our persistence seam), and the `ObjectStore` adapter over our digest-addressed object module (consumer-side by design).
