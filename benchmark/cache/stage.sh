#!/usr/bin/env bash
# Stage each Go fixture's go.mod/go.sum at its pinned commit into context/,
# the Docker build context. Shared by build.sh (local, host arch) and
# publish.sh (multi-arch release). Needs network.
set -euo pipefail
cd "$(dirname "$0")"

RAW="https://raw.githubusercontent.com"

rm -rf context
while read -r name repo commit _rest; do
    # skip blank lines and comments
    [ -z "${name:-}" ] && continue
    case "$name" in \#*) continue ;; esac
    mkdir -p "context/${name}"
    for f in go.mod go.sum; do
        curl -fsSL "${RAW}/${repo}/${commit}/${f}" -o "context/${name}/${f}"
    done
    echo "staged ${name} (${repo}@${commit})"
done < fixtures.txt
