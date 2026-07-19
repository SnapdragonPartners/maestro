#!/usr/bin/env bash
# Build the union dependency-cache image LOCALLY for the host architecture
# and load it, so cache-verify can run against it during development. The
# published image is multi-arch — see publish.sh; a locally built single-arch
# image is for local iteration only, never for pinning.
#
# CACHE_IMAGE / CACHE_TAG override the reference (default:
# ghcr.io/snapdragonpartners/golden-cache:latest).
set -euo pipefail
cd "$(dirname "$0")"

IMAGE="${CACHE_IMAGE:-ghcr.io/snapdragonpartners/golden-cache}"
TAG="${CACHE_TAG:-latest}"

./stage.sh
docker build -t "${IMAGE}:${TAG}" .
echo "built ${IMAGE}:${TAG} (host arch, local only)"
