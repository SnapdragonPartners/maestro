#!/usr/bin/env bash
# Deterministic acceptance test for the union cache image (item 5.1, #268).
#
# Two guards, both defeating a stale image:
#
#  1. Coverage — every story's [fixture] repo+commit must appear in
#     fixtures.txt. A story re-pinned without updating fixtures.txt is caught
#     here, before any image check. (Whether that re-pin also needs a REPUBLISH
#     depends on whether the image still COVERS it — this script is the gate.
#     It proves coverage, NOT that module inputs are unchanged: a shrinking
#     dependency set verifies against the old image, correctly.)
#
#  2. Offline completeness — for each fixture, mount the CURRENT pinned
#     go.mod/go.sum (freshly staged, NOT the copies baked into the image)
#     and run `go mod download` with the module proxy disabled (GOPROXY=off).
#     Using only the image's baked GOMODCACHE, this can succeed only if the
#     image already caches exactly the modules the current pins require — so
#     a re-pinned fixture whose deps are not in the image fails offline,
#     loudly. (Running against the image's own /cache/<name>/go.mod would be
#     a tautology: it would only prove the image caches its own old inputs.)
#
# Wall-clock timing is secondary evidence only. This is the CI gate.
#
# Usage: verify.sh [image-ref]   (default: $CACHE_IMAGE:$CACHE_TAG)
#   To exercise a specific architecture, pass that arch's per-arch child
#   digest (a single-platform ref) rather than a --platform flag on the
#   multi-arch manifest digest: docker refuses `--platform` against a digest
#   already resolved in the local store ("cannot overwrite digest"), and a
#   child digest runs its own arch directly (foreign arch via the daemon's
#   binfmt/QEMU). The release workflow verifies both child digests this way.
set -euo pipefail
cd "$(dirname "$0")"

IMAGE_REF="${1:-${CACHE_IMAGE:-ghcr.io/snapdragonpartners/golden-cache}:${CACHE_TAG:-latest}}"

# --- Guard 1: every story fixture pin is represented in fixtures.txt --------
extract() { # $1=toml $2=key -> value inside the [fixture] block
    awk -v key="$2" '
        /^\[fixture\]/ { inf=1; next }
        /^\[/          { inf=0 }
        inf && $0 ~ "^[[:space:]]*" key "[[:space:]]*=" {
            gsub(/.*=[[:space:]]*"|".*/, ""); print; exit
        }' "$1"
}
cov_fail=0
for toml in ../stories/*.toml; do
    [ -e "$toml" ] || continue
    repo=$(extract "$toml" repo)
    commit=$(extract "$toml" commit)
    [ -z "$repo" ] && continue
    norm=${repo#https://github.com/}; norm=${norm%.git}
    if ! awk -v r="$norm" -v c="$commit" \
        '!/^#/ && $2==r && $3==c {found=1} END{exit !found}' fixtures.txt; then
        echo "  ✗ $(basename "$toml"): fixture ${norm}@${commit} not in cache/fixtures.txt"
        cov_fail=1
    fi
done
if [ "$cov_fail" -ne 0 ]; then
    echo "❌ story fixtures not covered by cache/fixtures.txt — update it, then re-run this to see whether a republish is needed"
    exit 1
fi
echo "✓ every story fixture pin is represented in fixtures.txt"

# --- Guard 2: image caches the CURRENT pins, proven offline -----------------
./stage.sh >/dev/null   # fetch the current pinned go.mod/go.sum into context/

fail=0
while read -r name repo commit _rest; do
    [ -z "${name:-}" ] && continue
    case "$name" in \#*) continue ;; esac
    echo "verifying ${name} offline against current pin..."
    if ! docker run --rm \
        -e GOPROXY=off -e GOSUMDB=off -e GOTOOLCHAIN=local -e GOFLAGS=-mod=mod \
        -v "${PWD}/context/${name}:/verify:ro" \
        "${IMAGE_REF}" \
        sh -c 'cd /verify && go mod download'; then
        echo "  ✗ ${name}: image does not cache the current pin's deps (offline download failed)"
        fail=1
    fi
done < fixtures.txt

if [ "$fail" -ne 0 ]; then
    echo "❌ cache verification failed — image is stale or incomplete; republish (make cache-publish)"
    exit 1
fi
echo "✅ all fixtures verified offline against ${IMAGE_REF}"
