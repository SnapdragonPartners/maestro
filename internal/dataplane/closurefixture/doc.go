// Package closurefixture is the positive control for the two closure guards
// (Phase 3 item 3, design D2; process_build.md's Reachability Claims).
//
// The guards list a package's transitive closure under a crossed matrix of
// build configurations -- tag selection x linux/amd64, linux/arm64 x
// CGO_ENABLED -- by setting GOOS, GOARCH and CGO_ENABLED in `go list`'s
// environment. Every cell of that matrix currently yields the same in-module
// closure for both guarded packages, so a guard that silently stopped passing
// the environment would stay green. This package has a DIFFERENT import set
// per cell, selected the way real code is: by filename suffix for the
// platform, and by the toolchain's cgo constraint. The guards list it beside
// the package under test and assert the selection moved, so the environment
// is proved to reach the toolchain rather than assumed to.
//
// Nothing imports this package. It exists to be listed.
package closurefixture
