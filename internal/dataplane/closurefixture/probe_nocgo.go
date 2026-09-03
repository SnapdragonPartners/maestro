//go:build !cgo

package closurefixture

import _ "hash/fnv" // selected only when CGO_ENABLED=0

// WithCgo is absent from the cgo selection.
const WithCgo = false
