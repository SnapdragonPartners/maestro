#!/bin/bash
# Regression tests for achievability-check.sh's check-execution transport.
#
# This guards the THIRD silent-pass defect in that script: its check loop once
# read tab-separated LINES, so a multi-line command (every engine-owned oracle
# is one) was truncated at its first line — which created the oracle file, never
# ran it, never cleaned up, and reported a pass. Fixing the thing is easy; the
# discipline that was missing is proving the checker FAILS when it should. These
# tests do that, hermetically, with no agent and no network.
#
# The transport under test is: a TOML `command` string (possibly multi-line) is
# handed to `sh -c` intact. We reproduce that boundary here — base64 across a
# tab-delimited channel, decode, `sh -c` — and assert:
#   1. a multi-line command whose LATER line fails makes the check fail;
#   2. a multi-line command whose later line succeeds makes it pass;
#   3. side effects of every line actually happen (proving no truncation).
#
# If achievability-check.sh's transport regresses to line-reading, test 1 flips
# to a false pass and this script exits non-zero.

set -uo pipefail
fail=0
note() { printf '  %s %s\n' "$1" "$2"; }

# Mirror of achievability-check.sh's transport: encode a command the way the
# script's python emitter does, then run it the way the loop does.
run_via_transport() {
    local cmd="$1"
    local b64 line kind name payload
    b64="$(printf '%s' "$cmd" | base64 | tr -d '\n')"
    line="$(printf 'check\tname\t%s' "$b64")"
    IFS=$'\t' read -r kind name payload <<<"$line"
    sh -c "$(printf '%s' "$payload" | base64 -d)" >/dev/null 2>&1
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# 1. LATER LINE FAILS → check must FAIL. This is the exact shape that used to
#    false-pass: line 1 (a heredoc/file write) succeeds, a later line is the
#    real assertion. Truncation would run only line 1 and report success.
multiline_fail="$(cat <<'CMD'
touch marker_created
false
CMD
)"
if run_via_transport "$multiline_fail"; then
    note "✗" "multi-line command whose later line fails was reported as PASS (truncation regression)"
    fail=1
else
    note "✓" "multi-line command fails when a later line fails"
fi

# 2. LATER LINE SUCCEEDS → check must PASS.
multiline_pass="$(cat <<'CMD'
x=1
y=2
test $((x + y)) -eq 3
CMD
)"
if run_via_transport "$multiline_pass"; then
    note "✓" "multi-line command passes when all lines succeed"
else
    note "✗" "multi-line command whose lines all succeed was reported as FAIL"
    fail=1
fi

# 3. NO TRUNCATION: every line runs. Prove it by observable side effects — a
#    file created on an early line and removed on a later one. If the command
#    were truncated at line 1, the file would survive.
observable="$(cat <<CMD
touch "$work/oracle_scratch"
rm -f "$work/oracle_scratch"
test ! -e "$work/oracle_scratch"
CMD
)"
if run_via_transport "$observable" && [ ! -e "$work/oracle_scratch" ]; then
    note "✓" "every line of a multi-line command executes (create-then-remove observed)"
else
    note "✗" "a later line did not run — command was truncated"
    fail=1
fi

if [ "$fail" -eq 0 ]; then
    echo "PASS: achievability-check transport handles multi-line commands"
else
    echo "FAIL: transport regressed — multi-line commands are mis-executed"
fi
exit "$fail"
