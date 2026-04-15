# Running SWE-EVO Benchmarks

How to run the SWE-EVO benchmark runner against Maestro. This covers prerequisites, dataset preparation, running instances, and interpreting results.

For background on what the benchmark measures and why, see `docs/BENCHMARKS.md`. For the design spec, see `docs/specs/SWE_EVO_PLAN.md`.

## Prerequisites

1. **Docker** running locally (Gitea runs as a container)
2. **Maestro binary** built with `make maestro`
3. **Benchmark binary** built with `make benchmark`
4. **API keys** set in environment (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.) for the models Maestro will use
5. Sufficient disk space (~2GB per upstream repo bare clone, plus ~500MB per instance run)

## Preparing the Dataset

### 1. Create a bare clone cache

The runner seeds ephemeral Gitea repos from local bare clones of the upstream repositories. Create a directory containing one bare clone per repo:

```bash
mkdir -p /path/to/bare-repos/psf
git clone --bare https://github.com/psf/requests.git /path/to/bare-repos/psf/requests.git
```

The directory layout must match the `repo` field in the instance JSON. For instance, a `"repo": "psf/requests"` entry expects the bare clone at either `<repos-dir>/psf/requests.git` or `<repos-dir>/psf/requests`.

The full SWE-EVO dataset uses 7 repos:

```bash
for repo in psf/requests conan-io/conan dask/dask iterative/dvc modin-project/modin pydantic/pydantic scikit-learn/scikit-learn; do
  org=$(dirname "$repo")
  mkdir -p /path/to/bare-repos/$org
  git clone --bare "https://github.com/$repo.git" "/path/to/bare-repos/$org/$(basename $repo).git"
done
```

Some repos (pandas, scikit-learn, dask) are large. Budget 10-30 minutes for the initial clones.

### 2. Convert the SWE-EVO dataset

Use the conversion script to produce the instance JSON from the HuggingFace dataset:

```bash
pip install datasets
python scripts/convert-swe-evo.py \
    --dataset Fsoft-AIC/SWE-EVO \
    --output instances.json
```

For a pilot run, limit to a single instance or specific repos:

```bash
# Single instance
python scripts/convert-swe-evo.py \
    --dataset Fsoft-AIC/SWE-EVO \
    --limit 1 \
    --output test-instance.json

# Specific repos only
python scripts/convert-swe-evo.py \
    --dataset Fsoft-AIC/SWE-EVO \
    --repos psf/requests,pallets/flask \
    --output instances.json
```

The script maps SWE-bench fields to the runner's format, derives eval image names (`xingyaoww/sweb.eval.x86_64.<project>:<tag>`), includes `hints_text` in the problem statement when available, and extracts `test_cmd` from the `FAIL_TO_PASS` field.

You can also prepare the instance JSON manually if needed.

### 3. Instance JSON format

The instance JSON is an array of instance objects:

```json
[
  {
    "instance_id": "psf__requests-1234",
    "repo": "psf/requests",
    "base_commit": "111d2b77790bf49943c0dfa09b365371c24aec7e",
    "problem_statement": "The full problem description text...",
    "test_cmd": "pytest tests/test_requests.py -x -q",
    "eval_image": "swe-eval:requests"
  }
]
```

| Field | Required | Description |
|-------|----------|-------------|
| `instance_id` | Yes | Unique identifier (typically `org__repo-number`) |
| `repo` | Yes | Upstream repo in `org/name` format |
| `base_commit` | Yes | Full git SHA to check out before running |
| `problem_statement` | Yes | Raw problem text passed to Maestro as the spec |
| `test_cmd` | No | Test command override (defaults to `pytest`) |
| `eval_image` | No | Per-instance Docker image (falls back to `-container` flag) |

### 4. Ensure a container image is available

Maestro builds its own dev container from the configured language pack (e.g., `primary_platform: "python"`). The `-container` flag and `eval_image` field specify a **base image** for the container build, not a pre-built dev environment.

- **Per-instance images** (`eval_image` field): Derived automatically by the conversion script from the SWE-EVO dataset (`xingyaoww/sweb.eval.x86_64.*`). These are SWE-bench scoring images — Maestro uses them as base images for its own container.
- **Shared default image** (`-container` flag): A generic Python image (e.g., `python:3.11`). Simpler to set up.

If neither `eval_image` nor `-container` is provided, the run will fail at config generation.

## Running

### Basic invocation

```bash
./bin/benchmark \
  -dataset instances.json \
  -repos-dir /path/to/bare-repos \
  -container python:3.11 \
  -maestro-bin ./bin/maestro \
  -timeout 60m
```

### All flags

| Flag | Default | Description |
|------|---------|-------------|
| `-dataset` | (required) | Path to instance JSON file |
| `-repos-dir` | (required) | Directory of bare repo clones |
| `-container` | (none) | Default Docker image when `eval_image` is empty |
| `-maestro-bin` | `maestro` | Path to the Maestro binary |
| `-timeout` | `60m` | Per-instance wall-clock timeout |
| `-instances` | (all) | Comma-separated instance IDs to run (subset filter) |
| `-output` | `preds.json` | SWE-bench-compatible predictions file |
| `-results` | `results.json` | Detailed results with outcomes and timing |
| `-archive-dir` | `archives` | Artifact archive directory |
| `-base-dir` | `runs` | Per-instance project directory root |

### Running a subset

To run specific instances (useful for pilot testing):

```bash
./bin/benchmark \
  -dataset instances.json \
  -repos-dir /path/to/bare-repos \
  -container python:3.11 \
  -maestro-bin ./bin/maestro \
  -instances "psf__requests-1234,psf__requests-5678" \
  -timeout 15m
```

## What happens during a run

For each instance, the runner:

1. **Creates an ephemeral Gitea repo** seeded from the bare clone at `base_commit`, tagged `benchmark-base`
2. **Writes config files** into a fresh project directory: `benchmark-config.json`, `problem_statement.md`, `forge_state.json`
3. **Pre-pulls the Docker image** (if not already cached locally)
4. **Launches Maestro** as a subprocess with `--config`, `--spec-file`, `--projectdir`, and `--nowebui`
5. **Polls the Maestro database** (`.maestro/maestro.db`) for story completion
6. **Detects a terminal state** and sends `SIGTERM` to Maestro (with a 30-second grace before `SIGKILL`)
7. **Collects a patch** via `git diff benchmark-base..origin/main` from the Gitea repo
8. **Archives artifacts** (DB, config, forge state, logs)
9. **Deletes the ephemeral Gitea repo**

A Gitea container is started automatically before the first instance and stays running for the entire batch. Instances run serially.

## Outcomes

Each instance gets one of five outcomes:

| Outcome | Meaning |
|---------|---------|
| `success` | All stories reached `done` status |
| `terminal_failure` | All stories are terminal, at least one `failed` |
| `stalled` | All remaining stories stuck `on_hold` for >5 minutes |
| `timeout` | No terminal state reached before the per-instance timeout |
| `process_error` | Maestro subprocess crashed or failed to start |

## Output files

### `preds.json` (SWE-bench compatible)

```json
{
  "psf__requests-1234": {
    "model_patch": "diff --git a/..."
  }
}
```

Feed this directly to the SWE-bench evaluation harness.

### `results.json` (detailed)

```json
[
  {
    "instance_id": "psf__requests-1234",
    "outcome": "success",
    "model_patch": "diff --git a/...",
    "elapsed_seconds": 342.5,
    "artifacts_dir": "archives/psf__requests-1234"
  }
]
```

### Archived artifacts (`archives/<instance_id>/`)

Each instance archives:
- `maestro.db` — Full Maestro database (stories, messages, agent state)
- `config.json` — Merged Maestro config as actually used
- `forge_state.json` — Gitea connection details
- `logs/` — Maestro stdout/stderr log (if captured)

## Troubleshooting

### `config validation failed: git repo_url must start with 'git@' or 'https://'`

The Maestro binary doesn't include the `http://` URL fix for Gitea. Rebuild with `make maestro`.

### `process_error` on every instance

Check the Maestro log at `<base-dir>/<instance-id>/logs/maestro-stdout.log`. Common causes:
- Missing API keys in environment
- Container image not found or not pullable
- Config validation errors

### Gitea auth failures on re-run

If a previous run was interrupted, the Gitea container may have stale state. Clean up and retry:

```bash
docker rm -f maestro-gitea-benchmark
docker volume rm maestro-gitea-benchmark-data
```

### Bare clone not found

Verify the directory structure matches the `repo` field. For `"repo": "psf/requests"`, the runner looks for `<repos-dir>/psf/requests.git` then `<repos-dir>/psf/requests`.

## Running integration tests

The benchmark package includes an integration test that validates the Gitea lifecycle without launching Maestro:

```bash
# Requires: Docker, bare clone at /tmp/benchmark-test/bare-repos/psf/requests.git
go test -tags=integration -run TestIntegration_BenchGitea_Lifecycle -v ./pkg/benchmark/
```

Override the bare repos path with `BENCH_REPOS_DIR`:

```bash
BENCH_REPOS_DIR=/path/to/bare-repos go test -tags=integration -run TestIntegration_BenchGitea -v ./pkg/benchmark/
```

Unit tests (no Docker required):

```bash
go test ./pkg/benchmark/...
```
