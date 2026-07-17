package supervisor

import (
	"sync"
	"testing"
)

// P-6 regression: an agent death fires both the ERROR-notification and the
// unexpected-exit restart paths; only one may proceed or two live instances
// race for the dispatcher reply channel.
func TestClaimRestartMutualExclusion(t *testing.T) {
	s := &Supervisor{restartInFlight: make(map[string]bool)}

	if !s.claimRestart("coder-001") {
		t.Fatal("first claim should succeed")
	}
	if s.claimRestart("coder-001") {
		t.Fatal("second claim for same agent must fail while first is in flight")
	}
	if !s.claimRestart("coder-002") {
		t.Fatal("claim for a different agent must be independent")
	}

	s.releaseRestart("coder-001")
	if !s.claimRestart("coder-001") {
		t.Fatal("claim should succeed again after release")
	}
}

// P-6 regression: concurrent claims for the same agent admit exactly one winner.
func TestClaimRestartConcurrent(t *testing.T) {
	s := &Supervisor{restartInFlight: make(map[string]bool)}

	const contenders = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.claimRestart("coder-001") {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("expected exactly 1 winning claim, got %d", winners)
	}
}
