// Package state provides runtime state management for container orchestration.
package state

import (
	"sync"
	"time"
)

// Role represents the role of a container in the orchestration system.
type Role string

const (
	// RoleSafe represents the safe fallback container image.
	RoleSafe Role = "safe"
	// RoleTarget represents the target container image being promoted.
	RoleTarget Role = "target"
)

// ActiveContainer represents the currently active container in the orchestration system.
type ActiveContainer struct {
	Started time.Time `json:"started"` // When this container was started
	Role    Role      `json:"role"`
	CID     string    `json:"cid"`     // Container ID
	ImageID string    `json:"imageId"` // Docker image ID (sha256:...)
	Name    string    `json:"name"`    // Container name
}

// HistoryEntry represents a historical container that was previously active.
type HistoryEntry struct {
	Started time.Time `json:"started"` // When this container was started
	Stopped time.Time `json:"stopped"` // When this container was stopped
	Role    Role      `json:"role"`
	ImageID string    `json:"imageId"` // Docker image ID (sha256:...)
	Name    string    `json:"name"`    // Container name
}

// RuntimeState manages the orchestration system's container state.
//
//nolint:govet // fieldalignment: Logical grouping preferred for readability
type RuntimeState struct {
	mu      sync.RWMutex     // Protects concurrent access
	Active  *ActiveContainer `json:"active"`
	History []HistoryEntry   `json:"history"` // newest-first ring buffer
}

// NewRuntimeState creates a new runtime state manager.
func NewRuntimeState() *RuntimeState {
	return &RuntimeState{
		History: make([]HistoryEntry, 0),
	}
}

// GetActive returns the currently active container (thread-safe).
func (s *RuntimeState) GetActive() *ActiveContainer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.Active == nil {
		return nil
	}

	// Return a copy to prevent external mutation
	active := *s.Active
	return &active
}

// SetActive sets the currently active container (thread-safe).
func (s *RuntimeState) SetActive(container *ActiveContainer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if container == nil {
		s.Active = nil
	} else {
		// Store a copy to prevent external mutation
		active := *container
		s.Active = &active
	}
}

// HistoryPush adds a container to the history (thread-safe).
// Maintains a ring buffer with newest entries first.
func (s *RuntimeState) HistoryPush(entry *HistoryEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Add to front of history (newest first)
	s.History = append([]HistoryEntry{*entry}, s.History...)

	// Maintain ring buffer size (keep last 10 entries)
	const maxHistorySize = 10
	if len(s.History) > maxHistorySize {
		s.History = s.History[:maxHistorySize]
	}
}

// HistoryTop returns the most recent history entry, or nil if none exists (thread-safe).
func (s *RuntimeState) HistoryTop() *HistoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.History) == 0 {
		return nil
	}

	// Return a copy to prevent external mutation
	entry := s.History[0]
	return &entry
}

// GetHistory returns a copy of the complete history (thread-safe).
func (s *RuntimeState) GetHistory() []HistoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy to prevent external mutation
	history := make([]HistoryEntry, len(s.History))
	copy(history, s.History)
	return history
}

// ClearHistory removes all history entries (thread-safe).
func (s *RuntimeState) ClearHistory() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.History = s.History[:0]
}

// IsActiveImageID checks if the given image ID matches the currently active container (thread-safe).
func (s *RuntimeState) IsActiveImageID(imageID string) bool {
	active := s.GetActive()
	return active != nil && active.ImageID == imageID
}
