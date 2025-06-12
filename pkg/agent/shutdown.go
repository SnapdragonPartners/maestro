package agent

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ShutdownManager handles graceful shutdown of agent components
type ShutdownManager struct {
	mu          sync.RWMutex
	components  []ShutdownComponent
	timeouts    map[string]time.Duration
	shutdownCtx context.Context
	shutdownFn  context.CancelFunc
	done        chan struct{}
	once        sync.Once
}

// ShutdownComponent defines interface for components that need graceful shutdown
type ShutdownComponent interface {
	Shutdown(ctx context.Context) error
	Name() string
}

// NewShutdownManager creates a new shutdown manager
func NewShutdownManager() *ShutdownManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &ShutdownManager{
		components:  make([]ShutdownComponent, 0),
		timeouts:    make(map[string]time.Duration),
		shutdownCtx: ctx,
		shutdownFn:  cancel,
		done:        make(chan struct{}),
	}
}

// Register adds a component for graceful shutdown
func (sm *ShutdownManager) Register(component ShutdownComponent, timeout time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	sm.components = append(sm.components, component)
	sm.timeouts[component.Name()] = timeout
}

// Shutdown performs graceful shutdown of all registered components
func (sm *ShutdownManager) Shutdown(ctx context.Context) error {
	var shutdownErr error
	
	sm.once.Do(func() {
		defer close(sm.done)
		
		// Signal all components to start shutdown
		sm.shutdownFn()
		
		sm.mu.RLock()
		components := make([]ShutdownComponent, len(sm.components))
		copy(components, sm.components)
		timeouts := make(map[string]time.Duration)
		for k, v := range sm.timeouts {
			timeouts[k] = v
		}
		sm.mu.RUnlock()
		
		// Shutdown components in reverse order (LIFO)
		var errors []error
		for i := len(components) - 1; i >= 0; i-- {
			component := components[i]
			timeout := timeouts[component.Name()]
			if timeout == 0 {
				timeout = DefaultTimeoutConfig.ShutdownTimeout
			}
			
			// Create timeout context for this component
			componentCtx, cancel := context.WithTimeout(ctx, timeout)
			
			if err := component.Shutdown(componentCtx); err != nil {
				errors = append(errors, fmt.Errorf("failed to shutdown %s: %w", component.Name(), err))
			}
			
			cancel()
		}
		
		// Combine errors if any
		if len(errors) > 0 {
			shutdownErr = fmt.Errorf("shutdown errors: %v", errors)
		}
	})
	
	// Wait for shutdown completion
	select {
	case <-sm.done:
		return shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// IsShuttingDown returns true if shutdown has been initiated
func (sm *ShutdownManager) IsShuttingDown() bool {
	select {
	case <-sm.shutdownCtx.Done():
		return true
	default:
		return false
	}
}

// Wait blocks until shutdown is complete
func (sm *ShutdownManager) Wait() {
	<-sm.done
}

// ShutdownContext returns a context that is cancelled when shutdown begins
func (sm *ShutdownManager) ShutdownContext() context.Context {
	return sm.shutdownCtx
}

// Enhanced BaseDriver with shutdown handling
type ShutdownableDriver struct {
	*BaseDriver
	shutdownMgr *ShutdownManager
	name        string
}

// NewShutdownableDriver creates a driver with shutdown management
func NewShutdownableDriver(config *AgentConfig, initialState State, shutdownMgr *ShutdownManager) (*ShutdownableDriver, error) {
	baseDriver, err := NewBaseDriver(config, initialState)
	if err != nil {
		return nil, err
	}
	
	driver := &ShutdownableDriver{
		BaseDriver:  baseDriver,
		shutdownMgr: shutdownMgr,
		name:        fmt.Sprintf("driver-%s", config.ID),
	}
	
	// Register with shutdown manager
	if shutdownMgr != nil {
		shutdownMgr.Register(driver, DefaultTimeoutConfig.ShutdownTimeout)
	}
	
	return driver, nil
}

// Name returns the component name for shutdown management
func (d *ShutdownableDriver) Name() string {
	return d.name
}

// Run executes the driver's main loop with shutdown handling
func (d *ShutdownableDriver) Run(ctx context.Context) error {
	// Use shutdown-aware context if available
	if d.shutdownMgr != nil {
		// Combine contexts - cancel if either parent or shutdown context is cancelled
		shutdownCtx := d.shutdownMgr.ShutdownContext()
		combinedCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		
		go func() {
			select {
			case <-shutdownCtx.Done():
				cancel()
			case <-ctx.Done():
				cancel()
			case <-combinedCtx.Done():
				return
			}
		}()
		
		ctx = combinedCtx
	}
	
	// Initialize if not already done
	if err := d.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize driver: %w", err)
	}
	
	// Run the state machine loop with shutdown awareness
	for {
		select {
		case <-ctx.Done():
			// Graceful shutdown initiated
			return d.handleShutdown(ctx)
		default:
			done, err := d.Step(ctx)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
		}
	}
}

// handleShutdown performs graceful shutdown procedures
func (d *ShutdownableDriver) handleShutdown(ctx context.Context) error {
	// Try to transition to a safe state before shutting down
	if d.GetCurrentState() != StateDone && d.GetCurrentState() != StateError {
		// Attempt to save current work
		if err := d.Persist(); err != nil {
			d.config.Context.Logger.Printf("Warning: failed to persist state during shutdown: %v", err)
		}
		
		// Mark state as interrupted for later resume
		metadata := map[string]interface{}{
			"shutdown_reason": "graceful_shutdown",
			"shutdown_time":   time.Now().UTC(),
			"can_resume":      true,
		}
		
		if err := d.TransitionTo(ctx, StateError, metadata); err != nil {
			d.config.Context.Logger.Printf("Warning: failed to transition to error state during shutdown: %v", err)
		}
	}
	
	return ctx.Err()
}

// Shutdown implements ShutdownComponent interface
func (d *ShutdownableDriver) Shutdown(ctx context.Context) error {
	// Persist final state
	if err := d.Persist(); err != nil {
		return fmt.Errorf("failed to persist final state: %w", err)
	}
	
	// Mark as cleanly shutdown
	metadata := map[string]interface{}{
		"shutdown_clean": true,
		"shutdown_time":  time.Now().UTC(),
	}
	
	if err := d.TransitionTo(ctx, StateDone, metadata); err != nil {
		// Don't fail shutdown for transition errors
		d.config.Context.Logger.Printf("Warning: failed to mark clean shutdown: %v", err)
	}
	
	return nil
}

// CanResume checks if the driver can resume from its current state
func (d *ShutdownableDriver) CanResume() bool {
	data := d.StateMachine.(*BaseStateMachine).GetStateData()
	
	// Check if marked as resumable
	if canResume, ok := data["can_resume"].(bool); ok && canResume {
		return true
	}
	
	// Check if in a resumable state
	state := d.GetCurrentState()
	return state == StatePlanning || state == StateCoding || state == StateTesting
}

// Resume attempts to resume operations from a previous state
func (d *ShutdownableDriver) Resume(ctx context.Context) error {
	if !d.CanResume() {
		return fmt.Errorf("driver cannot be resumed from current state")
	}
	
	// Clear shutdown markers
	sm := d.StateMachine.(*BaseStateMachine)
	sm.SetStateData("shutdown_reason", nil)
	sm.SetStateData("can_resume", false)
	
	// Persist the clean state
	return d.Persist()
}