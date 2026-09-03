//go:build cgo

package closurefixture

import _ "hash/adler32" // selected only when CGO_ENABLED=1

// WithCgo is present only in the cgo selection.
const WithCgo = true
