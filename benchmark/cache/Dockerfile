# Union dependency-cache image for the golden Go fixtures (item 5.1, #268).
#
# It bakes every fixture's Go module cache into an immutable layer so v1
# coder runs skip the cold-download tax (discovery-011 lost ~40% of its wall
# clock to a first-run `go mod download`). The cache lives in GOMODCACHE
# (/go/pkg/mod); the image carries no fixture source — runs mount that
# separately. build.sh stages each fixture's go.mod/go.sum (at its pinned
# commit) under context/ before this builds.
#
# The base is pinned by digest to the same golang:1.26 the configs use, so
# the toolchain is identical to a non-cached run. GOTOOLCHAIN=local forbids
# an on-the-fly toolchain download: if a fixture requires a newer Go than the
# base ships, the build fails loudly here rather than silently reaching for
# the network at run time.
FROM golang@sha256:ae5a2316d12f3e78fd99177dad452e6ad4f240af2d71d57b480c3477f250fec6

ENV GOTOOLCHAIN=local

COPY context/ /cache/

RUN set -eu; \
    for dir in /cache/*/; do \
        echo "warming ${dir}"; \
        (cd "${dir}" && go mod download); \
    done
