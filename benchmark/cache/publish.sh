#!/usr/bin/env bash
# Maintainer/release step ONLY (item 5.1, #268): build the union cache image
# for BOTH linux/amd64 and linux/arm64 and push the multi-arch manifest to
# GHCR, then print the immutable manifest-list digest to pin into
# benchmark/configs/*.toml. The digest is arch-independent — each host (arm64
# dev, amd64 CI) pulls its matching arch. This is the sole path that rewrites
# the tracked digest; PR CI never runs it.
#
# Requires: GHCR push credentials (docker login ghcr.io, or the release
# workflow's GITHUB_TOKEN with packages: write) and a buildx builder with the
# docker-container driver + QEMU for cross-arch (the workflow sets these up;
# locally, `docker buildx create --use` once).
#
# CACHE_IMAGE / CACHE_TAG as in build.sh; CACHE_PLATFORMS overrides the arch set.
set -euo pipefail
cd "$(dirname "$0")"

IMAGE="${CACHE_IMAGE:-ghcr.io/snapdragonpartners/golden-cache}"
TAG="${CACHE_TAG:-latest}"
PLATFORMS="${CACHE_PLATFORMS:-linux/amd64,linux/arm64}"

./stage.sh
docker buildx build --platform "${PLATFORMS}" --push -t "${IMAGE}:${TAG}" .
digest="$(docker buildx imagetools inspect "${IMAGE}:${TAG}" --format '{{.Manifest.Digest}}')"

echo ""
echo "Published ${IMAGE}:${TAG} for ${PLATFORMS}"
echo "Pin this immutable manifest digest into benchmark/configs/*.toml (container_image):"
echo "  ${IMAGE}@${digest}"
