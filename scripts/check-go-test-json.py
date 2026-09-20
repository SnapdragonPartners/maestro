#!/usr/bin/env python3
"""Judge a `go test -json` stream: fail on a failure, on a SKIP, or on nothing having run.

Why this exists (issue #356). The v2 data-plane integration tests SKIP when they
cannot reach a plane, a root key or an object store -- a dozen t.Skipf sites
across planetest, migrations and objects. That is right for a developer who has
not run `make dataplane-up`. In CI it is a false green: `go test` exits 0, every
package prints `ok`, and nothing ran. So CI does not trust the exit status alone.

Three verdicts, each of which fails the job:
  * any test or package FAILED;
  * any test SKIPPED that is not explicitly allowed;
  * FEWER tests passed than --min-passed. An empty or truncated stream agrees
    with itself perfectly -- no failures, no skips -- so the degenerate value is
    guarded explicitly rather than left to look like success.

usage: check-go-test-json.py RESULTS.json --min-passed N [--allow-skip REGEX ...]
"""
import argparse
import json
import re
import sys

# How much of a failing test's output the verdict repeats.
TAIL = 40


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("results")
    ap.add_argument("--min-passed", type=int, required=True)
    ap.add_argument("--allow-skip", action="append", default=[],
                    help="regex over 'package.TestName'; a matching skip is reported, not fatal")
    args = ap.parse_args()
    allowed = [re.compile(p) for p in args.allow_skip]

    passed, failed, skipped, bad_lines = [], [], [], 0
    output = {}
    with open(args.results, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                ev = json.loads(line)
            except json.JSONDecodeError:
                # A build failure prints plain text into the stream. It is not a
                # reason to stop reading, and it IS a reason to fail: see below.
                bad_lines += 1
                continue
            pkg, test, action = ev.get("Package", ""), ev.get("Test"), ev.get("Action")
            name = f"{pkg}.{test}" if test else pkg
            if action == "output" and test:
                output.setdefault(name, []).append(ev.get("Output", ""))
            elif action == "pass" and test:
                passed.append(name)
            elif action == "skip" and test:
                skipped.append(name)
            elif action == "fail":
                failed.append(name)

    fatal_skips = [s for s in skipped if not any(p.search(s) for p in allowed)]

    print(f"passed: {len(passed)}   failed: {len(failed)}   skipped: {len(skipped)} "
          f"({len(skipped) - len(fatal_skips)} allowed)")
    ok = True
    if failed:
        ok = False
        print("\nFAILED:")
        for name in failed:
            print(f"  {name}")
        # The stream is a file on the runner that nobody can open afterwards,
        # so the verdict carries each failing test's own output. Packages are
        # skipped here: their output is the sum of their tests'.
        for name in failed:
            lines = "".join(output.get(name, [])).rstrip().splitlines()
            if not lines:
                continue
            print(f"\n--- output of {name} (last {min(len(lines), TAIL)} of {len(lines)} lines)")
            for out_line in lines[-TAIL:]:
                print(f"    {out_line}")
    if fatal_skips:
        ok = False
        print("\nSKIPPED -- in CI a skip means the test did not run, which is not a pass:")
        for name in fatal_skips:
            reason = "".join(output.get(name, [])).strip().splitlines()
            why = next((r.strip() for r in reason if "skip" in r.lower() or ".go:" in r), "")
            print(f"  {name}\n      {why}")
    if bad_lines:
        ok = False
        print(f"\n{bad_lines} line(s) were not JSON: usually a build failure printed into the stream.")
    if len(passed) < args.min_passed:
        ok = False
        print(f"\nonly {len(passed)} test(s) passed; at least {args.min_passed} are expected. "
              "An empty result has no failures and no skips either, so it is refused by count.")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
