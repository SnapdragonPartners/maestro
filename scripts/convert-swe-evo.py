#!/usr/bin/env python3
"""Convert SWE-EVO (or any SWE-bench format) dataset to Maestro benchmark JSON.

Usage:
    # From HuggingFace Hub:
    python scripts/convert-swe-evo.py \
        --dataset Fsoft-AIC/SWE-EVO \
        --output instances.json

    # From local Arrow/Parquet files:
    python scripts/convert-swe-evo.py \
        --dataset ./swe-evo-local \
        --output instances.json

    # Filter to specific repos:
    python scripts/convert-swe-evo.py \
        --dataset Fsoft-AIC/SWE-EVO \
        --repos psf/requests,pallets/flask \
        --output instances.json

    # Limit number of instances (useful for pilot runs):
    python scripts/convert-swe-evo.py \
        --dataset Fsoft-AIC/SWE-EVO \
        --limit 5 \
        --output instances.json

Requires: pip install datasets
"""

import argparse
import json
import sys
from pathlib import Path


def load_dataset_rows(dataset_path: str, split: str) -> list[dict]:
    """Load dataset from HuggingFace Hub or local path."""
    try:
        from datasets import load_dataset
    except ImportError:
        print(
            "Error: 'datasets' package required. Install with: pip install datasets",
            file=sys.stderr,
        )
        sys.exit(1)

    local_path = Path(dataset_path)
    if local_path.exists():
        # Local directory or file
        if local_path.is_dir():
            ds = load_dataset("arrow", data_dir=str(local_path), split=split)
        else:
            ds = load_dataset("json", data_files=str(local_path), split=split)
    else:
        # HuggingFace Hub dataset
        ds = load_dataset(dataset_path, split=split)

    return list(ds)


def derive_eval_image(instance_id: str, repo: str, version: str) -> str:
    """Derive the SWE-bench eval Docker image name.

    Convention: xingyaoww/sweb.eval.x86_64.<project>:<instance_id_with_underscores>
    where <project> is the repo name with / replaced by __ (double underscore).

    These images are for SWE-bench evaluation/scoring only -- Maestro builds its
    own dev container from the configured language pack.
    """
    # e.g. "psf/requests" -> "psf__requests"
    project = repo.replace("/", "__")
    # Instance IDs use - but image tags often use _
    tag = instance_id.replace("-", "_")
    return f"xingyaoww/sweb.eval.x86_64.{project}:{tag}"


def convert_row(row: dict) -> dict:
    """Convert a single SWE-bench dataset row to Maestro benchmark format."""
    instance_id = row["instance_id"]
    repo = row["repo"]

    # Build problem statement: main description + hints if available
    problem = row["problem_statement"]
    hints = row.get("hints_text", "")
    if hints and hints.strip():
        problem = f"{problem}\n\n## Hints\n\n{hints}"

    result = {
        "instance_id": instance_id,
        "repo": repo,
        "base_commit": row["base_commit"],
        "problem_statement": problem,
    }

    # Optional: test command from FAIL_TO_PASS
    fail_to_pass = row.get("FAIL_TO_PASS", "")
    if fail_to_pass:
        # FAIL_TO_PASS is a JSON-encoded list of test identifiers
        try:
            tests = json.loads(fail_to_pass) if isinstance(fail_to_pass, str) else fail_to_pass
            if isinstance(tests, list) and tests:
                # Use pytest with the specific failing tests
                result["test_cmd"] = "pytest " + " ".join(tests)
        except (json.JSONDecodeError, TypeError):
            pass

    # Eval image for SWE-bench scoring
    version = row.get("version", "")
    result["eval_image"] = derive_eval_image(instance_id, repo, version)

    return result


def main():
    parser = argparse.ArgumentParser(
        description="Convert SWE-bench format dataset to Maestro benchmark JSON"
    )
    parser.add_argument(
        "--dataset",
        required=True,
        help="HuggingFace dataset name (e.g. Fsoft-AIC/SWE-EVO) or local path",
    )
    parser.add_argument(
        "--split",
        default="test",
        help="Dataset split to use (default: test)",
    )
    parser.add_argument(
        "--output",
        required=True,
        help="Output JSON file path",
    )
    parser.add_argument(
        "--repos",
        help="Comma-separated list of repos to include (e.g. psf/requests,pallets/flask)",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=0,
        help="Max number of instances to output (0 = all)",
    )
    parser.add_argument(
        "--pretty",
        action="store_true",
        default=True,
        help="Pretty-print JSON output (default: true)",
    )
    parser.add_argument(
        "--no-pretty",
        action="store_true",
        help="Compact JSON output",
    )
    args = parser.parse_args()

    print(f"Loading dataset: {args.dataset} (split={args.split})", file=sys.stderr)
    rows = load_dataset_rows(args.dataset, args.split)
    print(f"Loaded {len(rows)} instances", file=sys.stderr)

    # Filter by repo if requested
    repo_filter = set()
    if args.repos:
        repo_filter = {r.strip() for r in args.repos.split(",")}
        rows = [r for r in rows if r["repo"] in repo_filter]
        print(f"Filtered to {len(rows)} instances from repos: {', '.join(sorted(repo_filter))}", file=sys.stderr)

    # Convert
    instances = [convert_row(r) for r in rows]

    # Sort by instance_id for deterministic output
    instances.sort(key=lambda x: x["instance_id"])

    # Apply limit
    if args.limit > 0:
        instances = instances[: args.limit]
        print(f"Limited to {len(instances)} instances", file=sys.stderr)

    # Summarize repos
    repos = sorted({inst["repo"] for inst in instances})
    print(f"Repos ({len(repos)}): {', '.join(repos)}", file=sys.stderr)

    # Write output
    indent = 2 if (args.pretty and not args.no_pretty) else None
    output = json.dumps(instances, indent=indent, ensure_ascii=False)

    output_path = Path(args.output)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(output + "\n")
    print(f"Wrote {len(instances)} instances to {args.output}", file=sys.stderr)


if __name__ == "__main__":
    main()
