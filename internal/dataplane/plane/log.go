package plane

import "log/slog"

// logRelease reports resources that could not be released when a seam closed.
//
// It is a function rather than an inline call so the test can observe that the
// failure was reported at all. A release failure on Close cannot be returned —
// store.Store.Close has no error — so logging is the only channel, and a
// channel nothing verifies is one that can quietly stop working.
func logRelease(err error) {
	slog.Default().Error("could not release resources owned by the data-plane seam; "+
		"lifecycle operations may block until this process exits", "error", err)
}
