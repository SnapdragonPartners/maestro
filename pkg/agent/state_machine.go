package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

const (
	// DefaultMaxRetries is the default maximum number of retries for operations.
	DefaultMaxRetries = 3
)

// StateTransition represents a transition between states.
type StateTransition struct {
	FromState proto.State
	ToState   proto.State
	Timestamp time.Time
	Metadata  map[string]any
}

// StateMachine defines the interface for state machine implementations.
type StateMachine interface {
	// GetCurrentState returns the current state.
	GetCurrentState() proto.State

	// ProcessState handles the logic for the current state.
	// Returns next state and whether processing is complete.
	ProcessState(ctx context.Context) (next proto.State, done bool, err error)

	// TransitionTo moves to a new state.
	TransitionTo(ctx context.Context, newState proto.State, metadata map[string]any) error

	// Initialize sets up the state machine.
	Initialize(ctx context.Context) error

	// Persist saves the current state to durable storage.
	Persist() error

	// CompactIfNeeded compacts state data if size threshold is exceeded.
	CompactIfNeeded() error
}

// StateData represents generic state storage.
type StateData map[string]any

// TransitionTable represents valid state transitions for an agent instance.
type TransitionTable map[proto.State][]proto.State

// StateStore defines the interface for state persistence.
type StateStore interface {
	// Save persists a value with the given ID.
	Save(id string, value any) error
	// Load retrieves a value by ID into the provided destination.
	Load(id string, dest any) error
}

// BaseStateMachine provides common state machine functionality.
type BaseStateMachine struct {
	agentID      string
	currentState proto.State
	stateData    StateData
	transitions  []StateTransition
	store        StateStore      // State persistence
	table        TransitionTable // Instance-local transition table
	mu           sync.Mutex      // Protects state changes
	retryCount   int             // Tracks retry attempts
	maxRetries   int             // Maximum retries before failing
	logger       *logx.Logger    // Agent-specific logger

	// State change notifications.
	stateNotifCh chan<- *proto.StateChangeNotification // Channel for state change notifications
}

// NewBaseStateMachine creates a new base state machine with an optional transition table.
func NewBaseStateMachine(agentID string, initialState proto.State, store StateStore, table TransitionTable) *BaseStateMachine {
	// Use global ValidTransitions as fallback to preserve existing behavior.
	if table == nil {
		table = ValidTransitions
	}

	return &BaseStateMachine{
		agentID:      agentID,
		currentState: initialState,
		stateData:    make(StateData),
		transitions:  make([]StateTransition, 0),
		store:        store,
		table:        table,
		maxRetries:   DefaultMaxRetries,
		logger:       logx.NewLogger(agentID),
	}
}

// GetCurrentState returns the current state.
func (sm *BaseStateMachine) GetCurrentState() proto.State {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.currentState
}

// GetStateData returns a copy of the current state data.
func (sm *BaseStateMachine) GetStateData() StateData {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	result := make(StateData)
	for k, v := range sm.stateData {
		result[k] = v
	}
	return result
}

// SetStateData sets a value in the state data.
func (sm *BaseStateMachine) SetStateData(key string, value any) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.stateData[key] = value
}

// GetStateValue gets a value from the state data.
func (sm *BaseStateMachine) GetStateValue(key string) (any, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	value, exists := sm.stateData[key]
	return value, exists
}

// SetTyped stores a typed value in the state data with compile-time type safety.
func SetTyped[T any](sm *BaseStateMachine, key string, value T) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.stateData[key] = value
}

// GetTyped retrieves a typed value from the state data with compile-time type safety.
// Returns the value and a boolean indicating if the key was found.
func GetTyped[T any](sm *BaseStateMachine, key string) (T, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var zero T
	value, exists := sm.stateData[key]
	if !exists {
		return zero, false
	}

	// Type assertion with error handling.
	typedValue, ok := value.(T)
	if !ok {
		return zero, false
	}

	return typedValue, true
}

// TransitionTo moves to a new state and records the transition.
func (sm *BaseStateMachine) TransitionTo(ctx context.Context, newState proto.State, metadata map[string]any) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("state transition cancelled: %w", ctx.Err())
	default:
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	oldState := sm.currentState

	// Validate transition.
	if !sm.IsValidTransition(oldState, newState) {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidTransition, oldState, newState)
	}

	// Record the transition.
	transition := StateTransition{
		FromState: oldState,
		ToState:   newState,
		Timestamp: time.Now().UTC(),
		Metadata:  metadata,
	}
	sm.transitions = append(sm.transitions, transition)

	// Update current state.
	sm.currentState = newState

	// Log the transition.
	sm.logger.Info("🔄 State machine transition: %s → %s", oldState, newState)

	// Send state change notification (non-blocking)
	if sm.stateNotifCh != nil {
		notification := &proto.StateChangeNotification{
			AgentID:   sm.agentID,
			FromState: oldState,
			ToState:   newState,
			Timestamp: transition.Timestamp,
			Metadata:  metadata,
		}

		select {
		case sm.stateNotifCh <- notification:
			// Notification sent successfully.
		default:
			// Channel full, log warning but don't block.
			sm.logger.Warn("State notification channel full, dropping notification for %s: %s->%s",
				sm.agentID, oldState, newState)
		}
	}

	// Update state data with transition metadata.
	sm.stateData["previous_state"] = oldState.String()
	sm.stateData["current_state"] = newState.String()
	sm.stateData["transition_at"] = transition.Timestamp

	// Reset retry count on state change.
	if oldState != newState {
		sm.retryCount = 0
	}

	// Merge additional metadata if provided.
	for k, v := range metadata {
		sm.stateData[k] = v
	}

	// Persist state changes.
	if err := sm.Persist(); err != nil {
		return fmt.Errorf("failed to persist state transition: %w", err)
	}

	return nil
}

// GetTransitions returns the state transition history.
func (sm *BaseStateMachine) GetTransitions() []StateTransition {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return append([]StateTransition{}, sm.transitions...)
}

// GetAgentID returns the agent ID.
func (sm *BaseStateMachine) GetAgentID() string {
	return sm.agentID
}

// Persist saves the current state to durable storage.
func (sm *BaseStateMachine) Persist() error {
	if sm.store == nil {
		return nil // No storage configured
	}

	// Save current state and data.
	state := map[string]any{
		"current_state": sm.currentState.String(),
		"state_data":    sm.stateData,
		"transitions":   sm.transitions,
		"retry_count":   sm.retryCount,
	}

	if err := sm.store.Save(sm.agentID, state); err != nil {
		return fmt.Errorf("failed to save agent state: %w", err)
	}
	return nil
}

// CompactIfNeeded compacts state data if size threshold is exceeded.
func (sm *BaseStateMachine) CompactIfNeeded() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	const maxTransitions = 100 // Keep last 100 transitions
	if len(sm.transitions) > maxTransitions {
		sm.transitions = sm.transitions[len(sm.transitions)-maxTransitions:]
	}

	// TODO: Add additional compaction strategies (e.g., for state data)
	return nil
}

// IncrementRetry increments the retry counter and checks against max retries.
func (sm *BaseStateMachine) IncrementRetry() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.retryCount++
	if sm.retryCount >= sm.maxRetries {
		return fmt.Errorf("exceeded maximum retries (%d)", sm.maxRetries)
	}
	return nil
}

// SetMaxRetries sets the maximum number of retries.
func (sm *BaseStateMachine) SetMaxRetries(max int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.maxRetries = max
}

// SetStateNotificationChannel sets the channel for state change notifications.
func (sm *BaseStateMachine) SetStateNotificationChannel(ch chan<- *proto.StateChangeNotification) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.stateNotifCh = ch
}

// ProcessState provides a default implementation that derived types should override.
func (sm *BaseStateMachine) ProcessState(_ context.Context) (proto.State, bool, error) {
	return sm.currentState, false, fmt.Errorf("ProcessState not implemented")
}

// Initialize sets up the state machine.
func (sm *BaseStateMachine) Initialize(_ context.Context) error {
	// Load previous state if available.
	if sm.store != nil {
		var state map[string]any
		if err := sm.store.Load(sm.agentID, &state); err != nil {
			// No state found is OK - this is first run.
			if errors.Is(err, ErrStateNotFound) {
				return nil
			}
			return fmt.Errorf("failed to load state: %w", err)
		}

		// Handle nil state map (no previous state)
		if state == nil {
			return nil
		}

		// Restore state from storage.
		sm.mu.Lock()
		defer sm.mu.Unlock()

		// Restore transitions.
		if transitionsAny, ok := state["transitions"].([]any); ok {
			transitions := make([]StateTransition, 0, len(transitionsAny))
			for _, t := range transitionsAny {
				if tMap, ok := t.(map[string]any); ok {
					transition := StateTransition{}

					// Safely extract from_state.
					if fromState, ok := tMap["from_state"].(string); ok {
						transition.FromState = proto.State(fromState)
					}

					// Safely extract to_state.
					if toState, ok := tMap["to_state"].(string); ok {
						transition.ToState = proto.State(toState)
					}

					// Safely extract timestamp.
					if ts, ok := tMap["timestamp"].(string); ok {
						if t, err := time.Parse(time.RFC3339, ts); err == nil {
							transition.Timestamp = t
						}
					}

					// Safely extract metadata.
					if meta, ok := tMap["metadata"].(map[string]any); ok {
						transition.Metadata = meta
					}

					transitions = append(transitions, transition)
				}
			}
			sm.transitions = transitions
		}

		// Restore state data.
		if stateData, ok := state["state_data"].(map[string]any); ok {
			sm.stateData = make(StateData)
			for k, v := range stateData {
				sm.stateData[k] = v
			}
		}

		// Restore retry count.
		if retryCount, ok := state["retry_count"].(float64); ok {
			sm.retryCount = int(retryCount)
		}

		// Restore current state.
		if currentState, ok := state["current_state"].(string); ok {
			sm.currentState = proto.State(currentState)
		}
	}

	return nil
}
