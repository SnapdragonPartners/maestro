# Phase 3 spike: Docker fencing domains

Reproducer for [ADR 0029](../../../docs/adr/0029-incubator-and-habitat-execution-boundaries.md)
§7. The written-up evidence is
[`docs/v2/phase_3/spike_docker-fencing.md`](../../../docs/v2/phase_3/spike_docker-fencing.md);
this directory is the executable.

```bash
go run .          # all claims, cleans up after itself
go run . -keep    # leave containers for inspection
```

Exits non-zero if any claim is falsified — the program produces evidence, so a
falsified claim is a successful run that changes the ADR.

Needs a reachable Docker daemon. Pulls `alpine:3` if absent. Creates and destroys
roughly 60 containers per run, all carrying a `maestro.spike.run` label so
cleanup is total regardless of which claim failed.

## Status

**Spike code, unmaintained**, per CLAUDE.md's Spikes And Deferred Work. It is a
standalone Go module, so the root module's build, test, and lint walkers skip it
by module boundary rather than by an exclusion rule. It never merges into `pkg/`,
`internal/`, or `cmd/`.
