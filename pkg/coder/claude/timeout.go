package claude

import (
	"context"
	"sync"
	"time"
)

// TimeoutManager handles timeout tracking for Claude Code sessions.
// It tracks both total execution time and inactivity (time without output).
type TimeoutManager struct {
	totalTimeout      time.Duration
	inactivityTimeout time.Duration

	startTime        time.Time
	lastActivityTime time.Time
	mu               sync.RWMutex

	// Channels for signaling
	stopCh     chan struct{}
	inactivity chan struct{}
	running    bool

	// cancelFunc, when set, is called when inactivity timeout fires.
	// This allows inactivity detection to actively kill the running process
	// rather than just setting a flag for post-hoc checking.
	cancelFunc context.CancelFunc
}

// NewTimeoutManager creates a new timeout manager.
func NewTimeoutManager(totalTimeout, inactivityTimeout time.Duration) *TimeoutManager {
	return &TimeoutManager{
		totalTimeout:      totalTimeout,
		inactivityTimeout: inactivityTimeout,
		stopCh:            make(chan struct{}),
		inactivity:        make(chan struct{}),
	}
}

// Start begins timeout tracking.
func (tm *TimeoutManager) Start() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now()
	tm.startTime = now
	tm.lastActivityTime = now
	tm.running = true

	// Start background goroutine to check for inactivity
	go tm.monitorInactivity()
}

// Stop stops timeout tracking.
func (tm *TimeoutManager) Stop() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.running {
		tm.running = false
		close(tm.stopCh)
	}
}

// RecordActivity records that output was received, resetting the inactivity timer.
func (tm *TimeoutManager) RecordActivity() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.lastActivityTime = time.Now()
}

// IsRunning returns whether the timeout manager is currently running.
func (tm *TimeoutManager) IsRunning() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.running
}

// IsTotalExpired returns whether the total timeout has been exceeded.
func (tm *TimeoutManager) IsTotalExpired() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if tm.startTime.IsZero() {
		return false
	}
	return time.Since(tm.startTime) > tm.totalTimeout
}

// IsInactivityExpired returns whether the inactivity timeout has been exceeded.
func (tm *TimeoutManager) IsInactivityExpired() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if tm.lastActivityTime.IsZero() {
		return false
	}
	return time.Since(tm.lastActivityTime) > tm.inactivityTimeout
}

// TimeSinceStart returns the time elapsed since Start() was called.
func (tm *TimeoutManager) TimeSinceStart() time.Duration {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if tm.startTime.IsZero() {
		return 0
	}
	return time.Since(tm.startTime)
}

// TimeSinceActivity returns the time elapsed since the last activity.
func (tm *TimeoutManager) TimeSinceActivity() time.Duration {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if tm.lastActivityTime.IsZero() {
		return 0
	}
	return time.Since(tm.lastActivityTime)
}

// RemainingTotal returns the remaining time before total timeout.
func (tm *TimeoutManager) RemainingTotal() time.Duration {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if tm.startTime.IsZero() {
		return tm.totalTimeout
	}
	remaining := tm.totalTimeout - time.Since(tm.startTime)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// RemainingInactivity returns the remaining time before inactivity timeout.
func (tm *TimeoutManager) RemainingInactivity() time.Duration {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if tm.lastActivityTime.IsZero() {
		return tm.inactivityTimeout
	}
	remaining := tm.inactivityTimeout - time.Since(tm.lastActivityTime)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// SetCancelFunc sets a context cancel function that will be called when
// inactivity timeout fires. This turns inactivity from a diagnostic into
// an active interrupt that kills the running process.
func (tm *TimeoutManager) SetCancelFunc(cancel context.CancelFunc) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.cancelFunc = cancel
}

// InactivityCh returns a channel that will be closed when inactivity timeout is reached.
func (tm *TimeoutManager) InactivityCh() <-chan struct{} {
	return tm.inactivity
}

// monitorInactivity runs in a goroutine to detect stalls.
func (tm *TimeoutManager) monitorInactivity() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-tm.stopCh:
			return
		case <-ticker.C:
			if tm.IsInactivityExpired() {
				// Signal inactivity timeout (only once)
				select {
				case <-tm.inactivity:
					// Already closed
				default:
					close(tm.inactivity)
				}

				// Cancel the running process if a cancel func was provided.
				tm.mu.RLock()
				cancel := tm.cancelFunc
				tm.mu.RUnlock()
				if cancel != nil {
					cancel()
				}

				return
			}
		}
	}
}

// Stats returns current timeout statistics.
func (tm *TimeoutManager) Stats() TimeoutStats {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	return TimeoutStats{
		TotalTimeout:        tm.totalTimeout,
		InactivityTimeout:   tm.inactivityTimeout,
		ElapsedTotal:        time.Since(tm.startTime),
		ElapsedInactivity:   time.Since(tm.lastActivityTime),
		RemainingTotal:      tm.RemainingTotal(),
		RemainingInactivity: tm.RemainingInactivity(),
		TotalExpired:        tm.IsTotalExpired(),
		InactivityExpired:   tm.IsInactivityExpired(),
	}
}

// TimeoutStats contains timeout status information.
type TimeoutStats struct {
	TotalTimeout        time.Duration
	InactivityTimeout   time.Duration
	ElapsedTotal        time.Duration
	ElapsedInactivity   time.Duration
	RemainingTotal      time.Duration
	RemainingInactivity time.Duration
	TotalExpired        bool
	InactivityExpired   bool
}

// OutputMonitor wraps output processing with activity recording.
type OutputMonitor struct {
	tm       *TimeoutManager
	callback func(line string)
}

// NewOutputMonitor creates a monitor that records activity on each output line.
func NewOutputMonitor(tm *TimeoutManager, callback func(line string)) *OutputMonitor {
	return &OutputMonitor{
		tm:       tm,
		callback: callback,
	}
}

// ProcessLine processes an output line and records activity.
func (om *OutputMonitor) ProcessLine(line string) {
	// Record activity first
	om.tm.RecordActivity()

	// Then call the callback
	if om.callback != nil {
		om.callback(line)
	}
}
