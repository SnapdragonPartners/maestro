# Phase 3 spike: Docker fencing domains

Reproducer for [ADR 0029](../../../docs/adr/0029-incubator-and-habitat-execution-boundaries.md)
§7. The written-up evidence is
[`docs/v2/phase_3/spike_docker-fencing.md`](../../../docs/v2/phase_3/spike_docker-fencing.md);
this directory is the executable.

```bash
go run .          # all claims, cleans up after itself
go run . -keep    # leave containers for inspection
```

**Exit code is 0 only when every claim is `PROVEN`.** Outcomes are three-valued
and two of them exit non-zero, for different reasons:

| Outcome | Exit | Means |
| --- | --- | --- |
| `PROVEN` | 0 | The claim held, and its control held too. |
| `FALSIFIED` | 1 | The claim is false. This is a **successful run that changes the ADR**, not a broken tool — the program produces evidence. |
| `ERROR` | 1 | An observation failed; **nothing is established either way**. |

`ERROR` exits non-zero deliberately: an inconclusive run must never be readable
as a passing one, which is the whole reason the outcome is three-valued rather
than a boolean. Automation should treat a non-zero exit as "read the report",
not as "the claim is false" — the summary table says which of the two it was.

Needs a reachable Docker daemon. Pulls `alpine:3` if absent. Creates and destroys
roughly 60 containers per run, all carrying a `maestro.spike.run` label so
cleanup is total regardless of which claim failed.

## Status

**Spike code, unmaintained**, per CLAUDE.md's Spikes And Deferred Work. It is a
standalone Go module, so the root module's build, test, and lint walkers skip it
by module boundary rather than by an exclusion rule. It never merges into `pkg/`,
`internal/`, or `cmd/`.
