package core

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/proto"
)

// TestEnterSuspend tests the EnterSuspend method.
func TestEnterSuspend(t *testing.T) {
	testTable := TransitionTable{
		proto.StateWaiting:     {proto.State("WORKING")},
		proto.State("WORKING"): {proto.StateDone},
		proto.StateSuspend:     {},
	}

	t.Run("enters suspend from WORKING", func(t *testing.T) {
		sm := NewBaseStateMachine("test-agent", proto.State("WORKING"), nil, testTable)
		ctx := context.Background()

		err := sm.EnterSuspend(ctx)
		if err != nil {
			t.Fatalf("expected EnterSuspend to succeed, got: %v", err)
		}

		if sm.GetCurrentState() != proto.StateSuspend {
			t.Errorf("expected state to be SUSPEND, got %s", sm.GetCurrentState())
		}

		// Verify originating state is stored
		suspendedFrom, exists := sm.GetStateValue(KeySuspendedFrom)
		if !exists {
			t.Fatal("expected KeySuspendedFrom to be set")
		}
		if suspendedFrom != proto.State("WORKING") {
			t.Errorf("expected KeySuspendedFrom to be WORKING, got %v", suspendedFrom)
		}
	})

	t.Run("rejects suspend from DONE", func(t *testing.T) {
		sm := NewBaseStateMachine("test-agent", proto.StateDone, nil, testTable)
		ctx := context.Background()

		err := sm.EnterSuspend(ctx)
		if err == nil {
			t.Error("expected EnterSuspend from DONE to fail")
		}
	})

	t.Run("rejects suspend from ERROR", func(t *testing.T) {
		sm := NewBaseStateMachine("test-agent", proto.StateError, nil, testTable)
		ctx := context.Background()

		err := sm.EnterSuspend(ctx)
		if err == nil {
			t.Error("expected EnterSuspend from ERROR to fail")
		}
	})

	t.Run("rejects suspend from SUSPEND", func(t *testing.T) {
		sm := NewBaseStateMachine("test-agent", proto.StateSuspend, nil, testTable)
		ctx := context.Background()

		err := sm.EnterSuspend(ctx)
		if err == nil {
			t.Error("expected EnterSuspend from SUSPEND to fail")
		}
	})
}

// TestHandleSuspend tests the HandleSuspend method.
func TestHandleSuspend(t *testing.T) {
	testTable := TransitionTable{
		proto.StateWaiting:     {proto.State("WORKING")},
		proto.State("WORKING"): {proto.StateDone},
		proto.StateSuspend:     {},
	}

	t.Run("returns to originating state on restore signal", func(t *testing.T) {
		sm := NewBaseStateMachine("test-agent", proto.StateSuspend, nil, testTable)
		sm.SetStateData(KeySuspendedFrom, proto.State("WORKING"))

		// Create a restore channel and send signal
		restoreCh := make(chan struct{}, 1)
		sm.SetRestoreChannel(restoreCh)

		// Send restore signal
		restoreCh <- struct{}{}

		ctx := context.Background()
		nextState, done, err := sm.HandleSuspend(ctx)

		if err != nil {
			t.Fatalf("expected HandleSuspend to succeed, got: %v", err)
		}
		if done {
			t.Error("expected done to be false")
		}
		if nextState != proto.State("WORKING") {
			t.Errorf("expected next state to be WORKING, got %s", nextState)
		}
	})

	t.Run("returns ERROR on timeout", func(t *testing.T) {
		sm := NewBaseStateMachine("test-agent", proto.StateSuspend, nil, testTable)
		sm.SetStateData(KeySuspendedFrom, proto.State("WORKING"))
		sm.SetSuspendTimeout(50 * time.Millisecond) // Very short timeout for test

		// Create restore channel but don't send anything
		restoreCh := make(chan struct{}, 1)
		sm.SetRestoreChannel(restoreCh)

		ctx := context.Background()
		nextState, _, err := sm.HandleSuspend(ctx)

		if err == nil {
			t.Error("expected HandleSuspend to return error on timeout")
		}
		if nextState != proto.StateError {
			t.Errorf("expected next state to be ERROR on timeout, got %s", nextState)
		}
	})

	t.Run("returns ERROR when no restore channel", func(t *testing.T) {
		sm := NewBaseStateMachine("test-agent", proto.StateSuspend, nil, testTable)
		sm.SetStateData(KeySuspendedFrom, proto.State("WORKING"))
		// Don't set restore channel

		ctx := context.Background()
		nextState, _, err := sm.HandleSuspend(ctx)

		if err == nil {
			t.Error("expected HandleSuspend to return error when no restore channel")
		}
		if nextState != proto.StateError {
			t.Errorf("expected next state to be ERROR, got %s", nextState)
		}
	})

	t.Run("returns ERROR when context cancelled", func(t *testing.T) {
		sm := NewBaseStateMachine("test-agent", proto.StateSuspend, nil, testTable)
		sm.SetStateData(KeySuspendedFrom, proto.State("WORKING"))

		restoreCh := make(chan struct{}, 1)
		sm.SetRestoreChannel(restoreCh)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		nextState, _, err := sm.HandleSuspend(ctx)

		if err == nil {
			t.Error("expected HandleSuspend to return error on context cancellation")
		}
		if nextState != proto.StateError {
			t.Errorf("expected next state to be ERROR, got %s", nextState)
		}
	})

	t.Run("returns ERROR when KeySuspendedFrom not set", func(t *testing.T) {
		sm := NewBaseStateMachine("test-agent", proto.StateSuspend, nil, testTable)
		// Don't set KeySuspendedFrom

		restoreCh := make(chan struct{}, 1)
		sm.SetRestoreChannel(restoreCh)

		ctx := context.Background()
		nextState, _, err := sm.HandleSuspend(ctx)

		if err == nil {
			t.Error("expected HandleSuspend to return error when KeySuspendedFrom not set")
		}
		if nextState != proto.StateError {
			t.Errorf("expected next state to be ERROR, got %s", nextState)
		}
	})
}

// TestIsSuspended tests the IsSuspended method.
func TestIsSuspended(t *testing.T) {
	testTable := TransitionTable{
		proto.StateWaiting: {proto.State("WORKING")},
		proto.StateSuspend: {},
	}

	t.Run("returns true when suspended", func(t *testing.T) {
		sm := NewBaseStateMachine("test-agent", proto.StateSuspend, nil, testTable)
		if !sm.IsSuspended() {
			t.Error("expected IsSuspended to return true when in SUSPEND state")
		}
	})

	t.Run("returns false when not suspended", func(t *testing.T) {
		sm := NewBaseStateMachine("test-agent", proto.StateWaiting, nil, testTable)
		if sm.IsSuspended() {
			t.Error("expected IsSuspended to return false when not in SUSPEND state")
		}
	})
}
