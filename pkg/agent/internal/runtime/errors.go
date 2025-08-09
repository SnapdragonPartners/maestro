package runtime

import "errors"

var (
	// ErrMaxRetriesExceeded indicates the maximum number of retries has been exceeded.
	ErrMaxRetriesExceeded = errors.New("maximum retries exceeded")
)
