#!/bin/bash
# Single-agent achievability check for a golden story.
#
# Answers exactly one question: can a competent single agent complete this
# story at all? It exists so a red rung is never ambiguous between "the
# pipeline cannot do this yet" (a progress marker, which is useful) and "this
# story is unreasonable" (an authoring defect, which is not).
#
# CONTRACT FIDELITY IS THE POINT. The check runs against the *identical* story
# contract the pipeline is given — same fixture repo and pinned commit, same
# prompt text, same allowed paths, same validators and checks, same isolation
# (a fresh clone from the pin, no inherited state). Proving some *other*,
# looser task achievable proves nothing about the story.
#
# It is cheap in METRICS rigor, not in contract fidelity: no cost aggregation,
# no MPH identity discipline, no repeat sampling. Those belong to the runner.
#
# VERDICTS
#   proven-achievable       a single agent completed it under the real contract
#   not-proven-achievable   it did not — which bounds OUR KNOWLEDGE, not the
#                           story. The agent may simply be the weaker executor.
#                           Never read as "unachievable".
#
# NOT A UNIVERSAL ORACLE. Demonstrated: against smoke-comment this returns
# not-proven-achievable because the agent puts the marker at the TOP of doc.go
# (idiomatic for a doc file) while the story requires it appended at the END —
# yet the pipeline accepts that story 3/3. A RECORDED ACCEPTED RUN IS STRONGER
# EVIDENCE THAN THIS CHECK. Use it for stories that have never passed; do not
# use it to overrule an acceptance.
#
# EXPIRY BY DESIGN: valid only for low rungs. Stories from the decomposition
# rungs upward exist precisely to test multi-Story coordination that a single
# autonomous agent cannot do, so this check is invalid there by construction
# and must be retired rather than left emitting false verdicts.
#
# Usage: scripts/achievability-check.sh benchmark/stories/<story>.toml [--keep]

set -uo pipefail

STORY="${1:-}"
KEEP="${2:-}"
if [ -z "$STORY" ] || [ ! -f "$STORY" ]; then
    echo "usage: $0 <story.toml> [--keep]" >&2
    exit 2
fi

command -v claude >/dev/null || { echo "❌ 'claude' CLI not on PATH" >&2; exit 2; }

root="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 2
STORY_ABS="$(cd "$(dirname "$STORY")" && pwd)/$(basename "$STORY")"

# Read the story with a real TOML parser rather than ad-hoc greps, so the
# contract the agent gets is the contract the file declares. Only the fields
# needed to STAGE the agent are read here — the fixture to clone and the prompt
# to give it. Evaluation (validators, checks, oracles, diff confinement) is NOT
# re-implemented in this script; it is delegated verbatim to `runner verify`,
# which runs the engine's single production check executor. That is the whole
# point of the shared Verify seam: no second copy of check execution to drift.
# NB: no f-strings below — a literal {} inside one parses as an empty
# replacement field and is a SyntaxError.
eval "$(python3 - "$STORY_ABS" <<'PY'
import tomllib, sys, shlex
d = tomllib.load(open(sys.argv[1], 'rb'))
fx = d.get('fixture') or {}
pr = d.get('prompt') or {}
fields = {
    'STORY_ID': str(d.get('id', '')),
    'REPO': str(fx.get('repo', '')),
    'COMMIT': str(fx.get('commit', '')),
    'PROMPT': str(pr.get('text', '')),
}
for key, val in fields.items():
    print(key + '=' + shlex.quote(val))
PY
)"

[ -n "$REPO" ] && [ -n "$COMMIT" ] && [ -n "$PROMPT" ] || {
    echo "❌ story missing fixture.repo / fixture.commit / prompt.text" >&2; exit 2; }

# Build the runner so evaluation runs through the SAME binary the pipeline uses.
echo "▶ building runner"
( cd "$root/benchmark" && go build -o bin/runner ./cmd/runner ) || {
    echo "❌ runner build failed" >&2; exit 2; }
RUNNER="$root/benchmark/bin/runner"

WORK="$(mktemp -d)"
cleanup() { [ "$KEEP" = "--keep" ] || rm -rf "$WORK"; }
trap cleanup EXIT

echo "▶ achievability: $STORY_ID"
echo "  fixture $REPO @ ${COMMIT:0:12}"
echo "  workspace $WORK"

# Isolation: fresh clone, detached at the pin. Same starting state the runner
# gives the pipeline.
git clone -q "$REPO" "$WORK/repo" 2>/dev/null || { echo "❌ clone failed"; exit 1; }
git -C "$WORK/repo" checkout -q "$COMMIT" 2>/dev/null || { echo "❌ pin checkout failed"; exit 1; }

echo "  running headless agent (no cost accounting — this is a control, not a measurement)"
( cd "$WORK/repo" && claude -p "$PROMPT" --permission-mode acceptEdits ) \
    > "$WORK/agent.log" 2>&1
agent_rc=$?
[ $agent_rc -eq 0 ] || echo "  ⚠️  agent exited $agent_rc (continuing — the verdict is the story's checks, not the agent's exit code)"

# Commit the agent's work, as the pipeline's coder would, so the workspace is a
# bound solution: `runner verify` requires the pin to be an ancestor of HEAD and
# the worktree to be clean (matching the engine's committed-solution checkout).
# Leaving the work uncommitted would leave HEAD at the pin — an empty solution —
# and diff-comparing checks would mis-verdict.
git -C "$WORK/repo" add -A >/dev/null 2>&1
git -C "$WORK/repo" -c user.name="achievability" -c user.email="achievability@local" \
    commit -q -m "achievability check: agent solution" >/dev/null 2>&1 || true

# Match the engine's bound checkout: it verifies a CLEAN checkout of the
# solution commit (git clean -fdx), so any untracked/ignored build artifact the
# agent left — this fixture's `go build ./...` emits a git-ignored
# golden-fixture-chat binary at the root — must go, or `runner verify`'s
# clean-worktree binding check (rightly) rejects the workspace as not
# equivalent to what the engine grades.
git -C "$WORK/repo" clean -fdqx >/dev/null 2>&1

# ---- evaluate through the production check executor ----
#
# The script does NOT re-implement validators, checks, oracles, or diff
# confinement. It hands the bound workspace to `runner verify`, which runs the
# engine's single Verify seam — the exact code path the pipeline's own attempt
# flow uses. This is what keeps the control honest: there is no facsimile of
# check execution here to drift from the engine (the base64 shell loop this
# replaces was exactly such a facsimile, and it produced false verdicts —
# truncated multi-line oracles, uncleaned artifacts). Oracle materialisation,
# argv execution, scratch mode, and cleanup are all the engine's.
echo "  verifying via runner (engine's production check executor)"
if "$RUNNER" verify --story "$STORY_ABS" --workspace "$WORK/repo"; then
    verify_rc=0
else
    verify_rc=$?
fi

echo
if [ $verify_rc -eq 0 ]; then
    echo "✅ $STORY_ID: proven-achievable"
    echo "   A red rung for this story means pipeline distance-to-capability."
    exit 0
fi
echo "⚠️  $STORY_ID: not-proven-achievable"
echo "   This bounds our knowledge, NOT the story — a single agent may simply be"
echo "   the weaker executor. Do not record it as 'unachievable'."
echo "   Agent transcript: $WORK/agent.log (re-run with --keep to retain)"
exit 1
