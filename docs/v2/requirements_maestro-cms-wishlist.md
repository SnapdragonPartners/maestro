+++
title = "Maestro Wishlist For maestro-cms"
edit_date = "2026-07-13"
status = "draft"
type = "requirements"
summary = "Maestro's running feature-request wishlist to the maestro-cms team, with generality arguments and a response field per item — the sibling of the maestro-llms wishlist, originating from the Phase 0 cms spike."
+++

# Maestro Wishlist For maestro-cms

Status: draft — awaiting maestro-cms team responses.

`maestro-cms` is a shared, open-source, general-purpose package; Maestro is a first-class consumer among several. Same rules as the [maestro-llms wishlist](requirements_maestro-llms-wishlist.md): DRY, upstream-first, non-breaking preferred, never fork; each item carries a generality argument and a **Response** field. Items originate from the Phase 0 cms spike ([spike report](phase_0/spike_cms.md)).

## 1. Digest-addressed keying convention for `store.ObjectStore`

- **What**: a documented convention (or thin optional helper) that object keys are content digests — e.g. a `DigestStore` wrapper that computes `content.SHA256HexReader` on `Put` and enforces key = digest.
- **Why (Maestro)**: our data plane requires content-addressed binaries (retention pinning and reference verification hang off digests). Today cms keys are explicitly opaque, so content-addressing is a discipline each consumer must impose alone.
- **Generality**: dedup, integrity verification, and cache-friendly addressing benefit any consumer; keeping it a wrapper preserves opaque keys for consumers that want them.
- **Breaking-ness**: additive helper or documentation; non-breaking.
- **Response**: _pending_

## 2. Contribution offer: the first `index/*` adapter

- **What**: cms's README defers indexed retrieval to optional `index/*` subpackages, currently unbuilt. Maestro will build a Postgres retrieval layer (FTS + vector) for its knowledge work in Phase 6 and offers to shape it to cms's intended `index/*` contract and contribute it once proven in use.
- **Why (Maestro)**: we need it regardless; building it to the package's shape costs little and ends the "cms hands back vectors, everyone builds retrieval alone" gap.
- **Generality**: retrieval over embedded chunks is the obvious next need for every cms consumer.
- **Breaking-ness**: new optional subpackage; non-breaking. Requires the cms team to define (or bless) the `index/*` contract first.
- **Response**: _pending_

## 3. Coordination: the v2 graph primitive

- **What**: not a feature request — a coordination flag. cms's v2 notes plan a graph primitive and cite Maestro's v1 knowledge graph as a design source; Maestro is about to design its v2 graph (Phase 6). We propose designing it against cms's v2 graph notes so it is upstreamable, rather than diverging and reconciling later.
- **Why (Maestro)**: avoids building the same graph twice on both sides of the boundary.
- **Generality**: schema-as-data knowledge graphs are named cms v2 scope already.
- **Breaking-ness**: n/a (design coordination).
- **Response**: _pending_

## 4. Image/OCR extraction — later

- **What**: an `extract` entry for images (OCR), and XLSX per cms ADR 0009's deferral, when priorities allow.
- **Why (Maestro)**: our intake accepts uploads including images and diagrams (roadmap pillar 13); today those would bypass the extract pipeline entirely.
- **Generality**: any document-ingesting consumer eventually meets images; the registry design already anticipates new extractors.
- **Breaking-ness**: additive extractors; non-breaking. Explicitly not urgent for Maestro before Phase 6.
- **Response**: _pending_

## Non-requests

Stays Maestro-side by our own analysis: knowledge-pack assembly and citation formatting/verification (Management artifacts under Maestro's review conventions), retrieval policy and ranking, the persistence of `embed.Record`s and provenance trees (our data plane, behind our persistence seam), and the `ObjectStore` adapter over our digest-addressed object module (consumer-side by design).
