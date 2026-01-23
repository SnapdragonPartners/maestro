package pm

// WorkingResult is the type parameter for PM's working phase toolloop.
// Currently a placeholder as result extraction is handled via ProcessEffect signals.
// Signal constants are defined in pkg/tools/mcp.go (tools.Signal*).
type WorkingResult struct {
	// Signal indicates which terminal condition was reached (populated by toolloop)
	Signal string
}
