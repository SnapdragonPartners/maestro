package toolloop

// ActivityTracker records toolloop iteration heartbeats for watchdog monitoring.
// The supervisor implements this interface to detect stalled agents.
type ActivityTracker interface {
	// RecordActivity records that the given agent started a new toolloop iteration.
	// Called at the top of each iteration before the LLM call.
	RecordActivity(agentID string)
}
