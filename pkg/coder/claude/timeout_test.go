package claude

import (
	"testing"
	"time"
)

func TestTimeoutManager_Creation(t *testing.T) {
	tm := NewTimeoutManager(5*time.Minute, 1*time.Minute)

	if tm.totalTimeout != 5*time.Minute {
		t.Errorf("expected totalTimeout 5m, got %v", tm.totalTimeout)
	}
	if tm.inactivityTimeout != 1*time.Minute {
		t.Errorf("expected inactivityTimeout 1m, got %v", tm.inactivityTimeout)
	}
	if tm.IsRunning() {
		t.Error("expected IsRunning=false before Start")
	}
}

func TestTimeoutManager_StartStop(t *testing.T) {
	tm := NewTimeoutManager(5*time.Minute, 1*time.Minute)

	tm.Start()
	if !tm.IsRunning() {
		t.Error("expected IsRunning=true after Start")
	}

	// Give goroutine time to start
	time.Sleep(10 * time.Millisecond)

	tm.Stop()
	if tm.IsRunning() {
		t.Error("expected IsRunning=false after Stop")
	}
}

func TestTimeoutManager_RecordActivity(t *testing.T) {
	tm := NewTimeoutManager(5*time.Minute, 100*time.Millisecond)
	tm.Start()
	defer tm.Stop()

	// Check initial state
	initialInactivity := tm.TimeSinceActivity()

	// Wait a bit
	time.Sleep(20 * time.Millisecond)

	afterWait := tm.TimeSinceActivity()
	if afterWait <= initialInactivity {
		t.Error("TimeSinceActivity should increase over time")
	}

	// Record activity
	tm.RecordActivity()
	afterRecord := tm.TimeSinceActivity()

	// Should be reset to near zero
	if afterRecord > 10*time.Millisecond {
		t.Errorf("expected TimeSinceActivity near 0 after RecordActivity, got %v", afterRecord)
	}
}

func TestTimeoutManager_TotalExpired(t *testing.T) {
	// Use very short timeout for testing
	tm := NewTimeoutManager(50*time.Millisecond, 1*time.Second)
	tm.Start()
	defer tm.Stop()

	if tm.IsTotalExpired() {
		t.Error("should not be expired immediately after start")
	}

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	if !tm.IsTotalExpired() {
		t.Error("expected IsTotalExpired=true after timeout period")
	}
}

func TestTimeoutManager_InactivityExpired(t *testing.T) {
	tm := NewTimeoutManager(1*time.Second, 50*time.Millisecond)
	tm.Start()
	defer tm.Stop()

	if tm.IsInactivityExpired() {
		t.Error("should not be expired immediately after start")
	}

	// Wait for inactivity timeout
	time.Sleep(60 * time.Millisecond)

	if !tm.IsInactivityExpired() {
		t.Error("expected IsInactivityExpired=true after inactivity period")
	}
}

func TestTimeoutManager_InactivityResetByActivity(t *testing.T) {
	tm := NewTimeoutManager(1*time.Second, 80*time.Millisecond)
	tm.Start()
	defer tm.Stop()

	// Wait partway through inactivity timeout
	time.Sleep(40 * time.Millisecond)

	// Record activity to reset
	tm.RecordActivity()

	// Wait another partial period
	time.Sleep(40 * time.Millisecond)

	// Should not be expired because we recorded activity
	if tm.IsInactivityExpired() {
		t.Error("should not be expired because activity was recorded")
	}

	// Wait for full inactivity timeout from last activity
	time.Sleep(50 * time.Millisecond)

	if !tm.IsInactivityExpired() {
		t.Error("expected IsInactivityExpired=true after full inactivity period")
	}
}

func TestTimeoutManager_RemainingTotal(t *testing.T) {
	tm := NewTimeoutManager(100*time.Millisecond, 1*time.Second)

	// Before start, should return full timeout
	if tm.RemainingTotal() != 100*time.Millisecond {
		t.Errorf("expected RemainingTotal=100ms before start, got %v", tm.RemainingTotal())
	}

	tm.Start()
	defer tm.Stop()

	// Should decrease over time
	time.Sleep(30 * time.Millisecond)
	remaining := tm.RemainingTotal()
	if remaining > 80*time.Millisecond {
		t.Errorf("expected RemainingTotal < 80ms, got %v", remaining)
	}

	// After timeout, should be 0
	time.Sleep(100 * time.Millisecond)
	if tm.RemainingTotal() != 0 {
		t.Errorf("expected RemainingTotal=0 after expiry, got %v", tm.RemainingTotal())
	}
}

func TestTimeoutManager_RemainingInactivity(t *testing.T) {
	tm := NewTimeoutManager(1*time.Second, 100*time.Millisecond)
	tm.Start()
	defer tm.Stop()

	// Should decrease over time
	time.Sleep(30 * time.Millisecond)
	remaining := tm.RemainingInactivity()
	if remaining > 80*time.Millisecond {
		t.Errorf("expected RemainingInactivity < 80ms, got %v", remaining)
	}

	// Record activity to reset
	tm.RecordActivity()
	afterRecord := tm.RemainingInactivity()
	if afterRecord < 90*time.Millisecond {
		t.Errorf("expected RemainingInactivity near 100ms after RecordActivity, got %v", afterRecord)
	}
}

func TestTimeoutManager_TimeSinceStart(t *testing.T) {
	tm := NewTimeoutManager(1*time.Second, 1*time.Second)

	// Before start should be 0
	if tm.TimeSinceStart() != 0 {
		t.Errorf("expected TimeSinceStart=0 before start, got %v", tm.TimeSinceStart())
	}

	tm.Start()
	defer tm.Stop()

	// Should increase over time
	time.Sleep(30 * time.Millisecond)
	elapsed := tm.TimeSinceStart()
	if elapsed < 25*time.Millisecond || elapsed > 50*time.Millisecond {
		t.Errorf("expected TimeSinceStart around 30ms, got %v", elapsed)
	}
}

func TestTimeoutManager_Stats(t *testing.T) {
	tm := NewTimeoutManager(200*time.Millisecond, 100*time.Millisecond)
	tm.Start()
	defer tm.Stop()

	time.Sleep(30 * time.Millisecond)

	stats := tm.Stats()

	if stats.TotalTimeout != 200*time.Millisecond {
		t.Errorf("expected TotalTimeout=200ms, got %v", stats.TotalTimeout)
	}
	if stats.InactivityTimeout != 100*time.Millisecond {
		t.Errorf("expected InactivityTimeout=100ms, got %v", stats.InactivityTimeout)
	}
	if stats.ElapsedTotal < 25*time.Millisecond {
		t.Errorf("expected ElapsedTotal > 25ms, got %v", stats.ElapsedTotal)
	}
	if stats.TotalExpired {
		t.Error("expected TotalExpired=false")
	}
	if stats.InactivityExpired {
		t.Error("expected InactivityExpired=false")
	}
}

func TestTimeoutManager_InactivityChannel(t *testing.T) {
	// Note: monitor goroutine checks every 1 second, so we need timeout > 1s
	// to ensure the check happens and detects expiration
	tm := NewTimeoutManager(5*time.Second, 100*time.Millisecond)
	tm.Start()
	defer tm.Stop()

	select {
	case <-tm.InactivityCh():
		// Good, channel was closed
	case <-time.After(2 * time.Second):
		t.Error("expected inactivity channel to close within timeout period")
	}
}

func TestTimeoutManager_InactivityChannelNotTriggeredWithActivity(t *testing.T) {
	tm := NewTimeoutManager(1*time.Second, 80*time.Millisecond)
	tm.Start()
	defer tm.Stop()

	// Keep recording activity to prevent inactivity timeout
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			time.Sleep(30 * time.Millisecond)
			tm.RecordActivity()
		}
		close(done)
	}()

	select {
	case <-tm.InactivityCh():
		t.Error("inactivity channel should not close while activity is being recorded")
	case <-done:
		// Good, activity kept it alive
	}
}

func TestOutputMonitor(t *testing.T) {
	tm := NewTimeoutManager(1*time.Second, 100*time.Millisecond)
	tm.Start()
	defer tm.Stop()

	var processed []string
	monitor := NewOutputMonitor(tm, func(line string) {
		processed = append(processed, line)
	})

	// Wait partway to inactivity
	time.Sleep(50 * time.Millisecond)

	// Process a line - should reset inactivity
	monitor.ProcessLine("test line 1")

	// Should not be expired
	if tm.IsInactivityExpired() {
		t.Error("should not be expired after ProcessLine")
	}

	// Check callback was called
	if len(processed) != 1 || processed[0] != "test line 1" {
		t.Errorf("expected [test line 1], got %v", processed)
	}

	// Process another line
	monitor.ProcessLine("test line 2")

	if len(processed) != 2 {
		t.Errorf("expected 2 processed lines, got %d", len(processed))
	}
}

func TestOutputMonitor_NilCallback(_ *testing.T) {
	tm := NewTimeoutManager(1*time.Second, 100*time.Millisecond)
	tm.Start()
	defer tm.Stop()

	// Should not panic with nil callback
	monitor := NewOutputMonitor(tm, nil)
	monitor.ProcessLine("test") // Should not panic
}

func TestTimeoutManager_BeforeStartStates(t *testing.T) {
	tm := NewTimeoutManager(100*time.Millisecond, 50*time.Millisecond)

	// Before Start, expiry checks should return false
	if tm.IsTotalExpired() {
		t.Error("IsTotalExpired should be false before Start")
	}
	if tm.IsInactivityExpired() {
		t.Error("IsInactivityExpired should be false before Start")
	}
	if tm.TimeSinceStart() != 0 {
		t.Errorf("TimeSinceStart should be 0 before Start, got %v", tm.TimeSinceStart())
	}
	if tm.TimeSinceActivity() != 0 {
		t.Errorf("TimeSinceActivity should be 0 before Start, got %v", tm.TimeSinceActivity())
	}
}

func TestTimeoutManager_DoubleStop(_ *testing.T) {
	tm := NewTimeoutManager(1*time.Second, 1*time.Second)
	tm.Start()

	// Double stop should not panic
	tm.Stop()
	tm.Stop()
}
