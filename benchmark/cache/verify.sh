#!/usr/bin/env bash
# Deterministic acceptance test for the union cache image (item 5.1, #268).
#
# A wall-clock comparison cannot PROVE the cache is warm — model latency,
# compilation, and test variance dominate it. This instead runs each fixture
# against the image with the module proxy disabled (GOPROXY=off) and the
# checksum DB off: `go mod download` can only succeed if every dependency is
# already in the baked GOMODCACHE. A missing dep fails offline, loudly. This
# is the CI gate; before/after timing is secondary evidence only.
#
# Usage: verify.sh [image-ref]  (default: $CACHE_IMAGE:$CACHE_TAG)
set -euo pipefail
cd "$(dirname "$0")"

IMAGE_REF="${1:-${CACHE_IMAGE:-ghcr.io/snapdragonpartners/golden-cache}:${CACHE_TAG:-latest}}"

fail=0
while read -r name repo commit _rest; do
    [ -z "${name:-}" ] && continue
    case "$name" in \#*) continue ;; esac
    echo "verifying ${name} offline (GOPROXY=off)..."
    if ! docker run --rm \
        -e GOPROXY=off -e GOFLAGS=-mod=mod -e GOSUMDB=off -e GOTOOLCHAIN=local \
        "${IMAGE_REF}" \
        sh -c "cd /cache/${name} && go mod download"; then
        echo "  ✗ ${name}: dependencies NOT fully cached (offline download failed)"
        fail=1
    fi
done < fixtures.txt

if [ "$fail" -ne 0 ]; then
    echo "❌ cache verification failed — image is stale or incomplete; republish (make cache-publish)"
    exit 1
fi
echo "✅ all fixtures verified offline against ${IMAGE_REF}"
