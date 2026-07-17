#!/usr/bin/env bash
# Maintainer/release step ONLY (item 5.1, #268): push the union cache image
# and print the immutable digest to pin into benchmark/configs/*.toml. This
# is the sole path that rewrites the tracked digest — PR CI never runs it.
# Requires GHCR push credentials (docker login ghcr.io) or the release
# workflow's GITHUB_TOKEN with packages: write.
#
# Run build.sh (and verify.sh) first. CACHE_IMAGE / CACHE_TAG as in build.sh.
set -euo pipefail
cd "$(dirname "$0")"

IMAGE="${CACHE_IMAGE:-ghcr.io/snapdragonpartners/golden-cache}"
TAG="${CACHE_TAG:-latest}"

docker push "${IMAGE}:${TAG}"
digest="$(docker inspect --format='{{index .RepoDigests 0}}' "${IMAGE}:${TAG}")"

echo ""
echo "Published ${IMAGE}:${TAG}"
echo "Pin this immutable digest into benchmark/configs/*.toml (container_image):"
echo "  ${digest}"
