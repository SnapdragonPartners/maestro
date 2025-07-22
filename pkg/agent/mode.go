package agent

// Mode defines the system operation mode.
type Mode int

const (
	// ModeLive indicates production mode with real LLM and minimal logging.
	ModeLive Mode = iota
	// ModeDebug indicates development mode with real LLM and verbose logging.
	ModeDebug
	// ModeMock indicates testing mode with mock LLM and controlled responses.
	ModeMock
)

// SystemMode is the global system operation mode, set once at startup.
var SystemMode Mode //nolint:gochecknoglobals

// InitMode sets the system mode. Must be called before any agent initialization.
// Panics if called more than once outside of tests.
func InitMode(mode Mode) {
	if SystemMode != 0 && !isTestMode() {
		panic("agent: InitMode called multiple times")
	}
	SystemMode = mode
}

func (m Mode) String() string {
	switch m {
	case ModeLive:
		return "LIVE"
	case ModeDebug:
		return "DEBUG"
	case ModeMock:
		return "MOCK"
	default:
		return "UNKNOWN"
	}
}

// resetMode resets the system mode to 0. Only used in tests.
func resetMode() {
	SystemMode = 0
}
