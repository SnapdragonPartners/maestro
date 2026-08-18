package plane

import "log/slog"

// logRelease reports resources that could not be released when a seam closed.
//
// It is a function rather than an inline call so the test can observe that the
// failure was reported at all. A release failure on Close cannot be returned —
// store.Store.Close has no error — so logging is the only channel, and a
// channel nothing verifies is one that can quietly stop working.
// The message names the CONSEQUENCE THIS FUNCTION CAN VOUCH FOR, which is only
// that the named resources may still be live. It previously said lifecycle
// operations may block — true of the local flock, and false of an object
// client whose close failed, which blocks nothing and merely holds
// connections. A generic site cannot know which resource failed, so it reports
// what is common to all of them and lets the names carry the specifics.
func logRelease(err error) {
	slog.Default().Error("could not release resources owned by the data-plane seam; "+
		"the named resources may still be live in this process", "error", err)
}
