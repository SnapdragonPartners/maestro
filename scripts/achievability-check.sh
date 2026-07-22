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
# contract the agent gets is the contract the file declares.
# NB: no f-strings below — a literal {} inside one parses as an empty
# replacement field and is a SyntaxError.
eval "$(python3 - "$STORY_ABS" <<'PY'
import tomllib, sys, shlex
d = tomllib.load(open(sys.argv[1], 'rb'))
fx = d.get('fixture') or {}
ex = d.get('expectations') or {}
pr = d.get('prompt') or {}
fields = {
    'STORY_ID': str(d.get('id', '')),
    'REPO': str(fx.get('repo', '')),
    'COMMIT': str(fx.get('commit', '')),
    'PROMPT': str(pr.get('text', '')),
    'ALLOWED': ' '.join(ex.get('allowed_paths') or []),
}
for key, val in fields.items():
    print(key + '=' + shlex.quote(val))
PY
)"

[ -n "$REPO" ] && [ -n "$COMMIT" ] && [ -n "$PROMPT" ] || {
    echo "❌ story missing fixture.repo / fixture.commit / prompt.text" >&2; exit 2; }

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
BASE="$(git -C "$WORK/repo" rev-parse HEAD)"

echo "  running headless agent (no cost accounting — this is a control, not a measurement)"
( cd "$WORK/repo" && claude -p "$PROMPT" --permission-mode acceptEdits ) \
    > "$WORK/agent.log" 2>&1
agent_rc=$?
[ $agent_rc -eq 0 ] || echo "  ⚠️  agent exited $agent_rc (continuing — the verdict is the story's checks, not the agent's exit code)"

# Commit the agent's work, as the pipeline's coder would. Checks are free to
# compare against git history — smoke-comment asserts
# `git diff <pin>..HEAD --numstat` — and against an uncommitted tree HEAD is
# still the pin, so such a check sees an empty diff and fails through no fault
# of the agent. Leaving the work uncommitted silently mis-verdicts every
# git-comparing story while grep-based ones pass, which is worse than failing
# outright.
git -C "$WORK/repo" add -A >/dev/null 2>&1
git -C "$WORK/repo" -c user.name="achievability" -c user.email="achievability@local" \
    commit -q -m "achievability check: agent solution" >/dev/null 2>&1 || true

# ---- evaluate against the story's own contract ----
cd "$WORK/repo" || exit 1
fail=0

# Validators and checks, run exactly as declared.
#
# ORDER MATTERS: validators run BEFORE the diff is computed, because a
# validator can itself change the tree — `go build` with no -o drops an
# executable named after the directory. The real engine diffs a COMMITTED
# solution branch, so any artifact the coder built and committed shows up in
# its diff; computing the diff first here made this script blind to exactly
# that class of failure. It was: a build artifact committed into the chat
# fixture made diff-confinement unsatisfiable for three stories, and this
# check passed all three beforehand because it looked too early.
# Commands are base64-encoded across this boundary: engine-owned oracles are
# MULTI-LINE shell (heredoc-injected Go tests), and a line-oriented read would
# truncate them at the first line — creating the oracle file, never running it,
# never cleaning up, and reporting a pass. The real engine hands the whole
# string to `sh -c`, so anything less here is not the same contract.
while IFS=$'\t' read -r kind name b64; do
    [ -n "$b64" ] || continue
    cmd="$(printf '%s' "$b64" | base64 -d)"
    if sh -c "$cmd" >/dev/null 2>&1; then
        echo "  ✓ $kind $name"
    else
        echo "  ✗ $kind $name"; fail=1
    fi
done < <(python3 - "$STORY_ABS" <<'PY'
import tomllib, sys, base64
def emit(kind, name, cmd):
    print(kind + "\t" + name + "\t" + base64.b64encode(cmd.encode()).decode())
d = tomllib.load(open(sys.argv[1], 'rb'))
for v in d.get('validators', []):
    emit('validator', v.get('name', '?'), v.get('command', ''))
for c in d.get('checks', []):
    t = c.get('type')
    if t == 'command':
        emit('check', c.get('name', '?'), c.get('command', ''))
    elif t == 'file_contains':
        emit('check', c.get('name', '?'),
             "grep -qF -- %r %r" % (c.get('contains', ''), c.get('path', '')))
    # files_changed_within is enforced by the diff comparison below.
PY
)

# Diff computed AFTER validators, so build artifacts they produce are visible
# (see the ORDER MATTERS note above).
changed="$(git diff --name-only "$BASE" HEAD -- . ; git diff --name-only -- . ; git ls-files --others --exclude-standard)"
changed="$(printf '%s\n' "$changed" | sed '/^$/d' | sort -u)"
if [ -z "$changed" ]; then
    echo "  ✗ no files changed"; fail=1
else
    echo "  changed: $(printf '%s' "$changed" | tr '\n' ' ')"
fi

# Mirrors engine/checks.go pathAllowed: an entry matches a path exactly, OR a
# directory entry matches anything beneath it. Exact-match-only silently
# rejected every story using directory scopes — bugfix-openai-stopreason
# allows "llms/providers/openai/" and "docs/", so every changed file under
# them would have been reported outside its own allowed paths.
path_allowed() {
    _p="$1"
    for _a in $ALLOWED; do
        [ "$_p" = "$_a" ] && return 0
        _prefix="${_a%/}/"
        case "$_p" in "$_prefix"*) return 0 ;; esac
    done
    return 1
}

if [ -n "$ALLOWED" ]; then
    for c in $changed; do
        ok=0
        path_allowed "$c" && ok=1
        if [ $ok -ne 1 ]; then
            echo "  ✗ $c outside allowed_paths ($ALLOWED)"
            case "$c" in
                *.go|*.md|*.toml|*.json|*.yaml|*.yml) ;;
                *) echo "      hint: looks like a build artifact — is it committed to the fixture, or missing from .gitignore?" ;;
            esac
            fail=1
        fi
    done
fi

echo
if [ $fail -eq 0 ]; then
    echo "✅ $STORY_ID: proven-achievable"
    echo "   A red rung for this story means pipeline distance-to-capability."
    exit 0
fi
echo "⚠️  $STORY_ID: not-proven-achievable"
echo "   This bounds our knowledge, NOT the story — a single agent may simply be"
echo "   the weaker executor. Do not record it as 'unachievable'."
echo "   Agent transcript: $WORK/agent.log (re-run with --keep to retain)"
exit 1
