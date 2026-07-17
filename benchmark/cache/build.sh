#!/usr/bin/env bash
# Build the union dependency-cache image (item 5.1, #268). Stages each Go
# fixture's go.mod/go.sum at its pinned commit into context/, then builds the
# image. Network is required (the baked `go mod download` fetches modules);
# the resulting cache is then usable fully offline (see verify.sh).
#
# CACHE_IMAGE / CACHE_TAG override the image reference (default:
# ghcr.io/snapdragonpartners/golden-cache:latest). This script only builds
# locally; publish.sh pushes.
set -euo pipefail
cd "$(dirname "$0")"

IMAGE="${CACHE_IMAGE:-ghcr.io/snapdragonpartners/golden-cache}"
TAG="${CACHE_TAG:-latest}"
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

docker build -t "${IMAGE}:${TAG}" .
echo "built ${IMAGE}:${TAG}"
