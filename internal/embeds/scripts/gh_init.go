// Package scripts contains embedded shell scripts used by the orchestrator.
package scripts

import _ "embed"

// GHInitSh contains the embedded GitHub authentication initialization script.
//
//go:embed gh_init.sh
var GHInitSh []byte
