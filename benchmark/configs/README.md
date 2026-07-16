+++
title = "MPH Configuration Bundles"
edit_date = "2026-07-16"
status = "live"
summary = "Authored MPH configuration bundles (TOML, one per file), content-hash identified. The paired-agent default lands with item 4 (v1 adapter); the single-agent baseline with item 8."
+++

# MPH Configuration Bundles

Authored benchmark configurations: one MPH (Model/Prompt/Harness) bundle
per TOML file, identified by the content hash of its canonical form — never
by file location (ADR 0025). Schema and loader live in
[`../mph/`](../mph/bundle.go).

The two mandatory configurations arrive with their adapters: the
**paired-agent default** with item 4 (`adapter-v1`) and the **single-agent
happy-path baseline** with item 8 (`baseline-single-agent`).
