#!/bin/sh
# fake-maestro: stands in for the v1 binary in hermetic adapter tests.
# Speaks just enough of the CLI surface: -version, and the launch flags
# (--config, --spec-file, --projectdir, --nowebui). Simulates a completed
# run: pushes a solution branch to the forge repo, opens and merges a PR,
# and installs a canned maestro.db (FAKE_DB env) into the project dir.
set -eu

if [ "${1:-}" = "-version" ]; then
    echo "maestro fake-v0.0.0 (golden-runner test binary)"
    echo "  usage-surface: v1"
    exit 0
fi

PROJECT_DIR=""
while [ $# -gt 0 ]; do
    case "$1" in
        --projectdir) PROJECT_DIR="$2"; shift 2 ;;
        *) shift ;;
    esac
done
[ -n "$PROJECT_DIR" ] || { echo "no --projectdir" >&2; exit 1; }

STATE="$PROJECT_DIR/.maestro/forge_state.json"
field() { sed -n "s/^  \"$1\": \"\(.*\)\",\{0,1\}$/\1/p" "$STATE" | head -1; }
URL=$(field url)
TOKEN=$(field token)
OWNER=$(field owner)
REPO=$(field repo_name)

WORK="$PROJECT_DIR/fake-work"
AUTH_URL=$(echo "$URL" | sed "s|http://|http://golden-admin:$TOKEN@|")
git clone --quiet "$AUTH_URL/$OWNER/$REPO.git" "$WORK"
cd "$WORK"
API="$URL/api/v1/repos/$OWNER/$REPO"

# open_and_merge <branch> <content-line>: push a solution branch, open a
# PR, merge it. Gitea processes pushed refs asynchronously, so both PR
# operations retry.
open_and_merge() {
    BRANCH="$1"; LINE="$2"
    git fetch --quiet origin main
    git checkout --quiet -B "$BRANCH" origin/main
    echo "$LINE" >> solution.txt
    git add solution.txt
    git -c user.name=fake -c user.email=fake@invalid commit --quiet -m "$BRANCH solution"
    git push --quiet origin "$BRANCH"

    PR_NUMBER=""
    for _ in 1 2 3 4 5 6 7 8 9 10; do
        PR_NUMBER=$(curl -s -X POST "$API/pulls" \
            -H "Authorization: token $TOKEN" -H "Content-Type: application/json" \
            -d "{\"title\":\"$BRANCH\",\"head\":\"$BRANCH\",\"base\":\"main\"}" | sed -n 's/.*"number":\([0-9]*\).*/\1/p' | head -1)
        [ -n "$PR_NUMBER" ] && break
        sleep 1
    done
    [ -n "$PR_NUMBER" ] || { echo "fake-maestro: PR create failed for $BRANCH" >&2; exit 1; }

    MERGED=""
    for _ in 1 2 3 4 5 6 7 8 9 10; do
        if curl -sf -X POST "$API/pulls/$PR_NUMBER/merge" \
            -H "Authorization: token $TOKEN" -H "Content-Type: application/json" \
            -d '{"Do":"merge"}' > /dev/null; then
            MERGED=yes
            break
        fi
        sleep 1
    done
    [ -n "$MERGED" ] || { echo "fake-maestro: PR merge failed for $BRANCH" >&2; exit 1; }
}

# The canned DB records two stories with PR IDs 1 and 2; every recorded
# story PR must exist, distinctly, and be merged.
open_and_merge story-1 "solution: done"
open_and_merge story-2 "solution: done again"

mkdir -p "$PROJECT_DIR/.maestro/logs"
echo "fake maestro ran" > "$PROJECT_DIR/.maestro/logs/run.log"

# P-1 usage surface: versioned header + one line per (fake) LLM call.
# Totals deliberately match the canned DB story aggregates: 12000 tokens,
# $0.75, across 2 calls. FAKE_NO_USAGE=1 suppresses the log entirely to
# exercise the adapter's usage-surface-required failure path.
if [ "${FAKE_NO_USAGE:-0}" != "1" ]; then
cat > "$PROJECT_DIR/.maestro/usage.jsonl" <<'USAGE'
{"usage_surface_version":1}
{"ts":"2026-07-17T00:00:01Z","story_id":"st-1","agent_id":"coder-001","model":"fake-model","prompt_tokens":5000,"completion_tokens":3000,"cost_usd":0.5,"success":true}
{"ts":"2026-07-17T00:00:02Z","story_id":"st-2","agent_id":"coder-001","model":"fake-model","prompt_tokens":3000,"completion_tokens":1000,"cost_usd":0.25,"success":true}
USAGE
fi

# FAKE_USAGE_ERROR=1 simulates v1 detecting a mid-run usage-log write
# failure: the log is present and valid (header + lines) but the sentinel
# marks streamed usage as undercounting. The adapter must fail the run.
if [ "${FAKE_USAGE_ERROR:-0}" = "1" ]; then
    echo "simulated append failure: disk full" > "$PROJECT_DIR/.maestro/usage.error"
fi

cp "$FAKE_DB" "$PROJECT_DIR/.maestro/maestro.db"

# Stay alive briefly so the adapter's poller (not process death) observes
# the terminal state.
sleep 30
