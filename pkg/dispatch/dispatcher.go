package dispatch

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

type Agent interface {
	ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error)
	GetID() string
	Shutdown(ctx context.Context) error
}

type Dispatcher struct {
	agents      map[string]Agent
	rateLimiter *limiter.Limiter
	eventLog    *eventlog.Writer
	logger      *logx.Logger
	config      *config.Config
	inputChan   chan *proto.AgentMsg
	shutdown    chan struct{}
	wg          sync.WaitGroup
	mu          sync.RWMutex
	running     bool
}

type DispatchResult struct {
	Message *proto.AgentMsg
	Error   error
}

func NewDispatcher(cfg *config.Config, rateLimiter *limiter.Limiter, eventLog *eventlog.Writer) (*Dispatcher, error) {
	return &Dispatcher{
		agents:      make(map[string]Agent),
		rateLimiter: rateLimiter,
		eventLog:    eventLog,
		logger:      logx.NewLogger("dispatcher"),
		config:      cfg,
		inputChan:   make(chan *proto.AgentMsg, 100), // Buffered channel for message queue
		shutdown:    make(chan struct{}),
		running:     false,
	}, nil
}

func (d *Dispatcher) RegisterAgent(agent Agent) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	agentID := agent.GetID()
	if _, exists := d.agents[agentID]; exists {
		return fmt.Errorf("agent %s already registered", agentID)
	}

	d.agents[agentID] = agent
	d.logger.Info("Registered agent: %s", agentID)

	return nil
}

func (d *Dispatcher) UnregisterAgent(agentID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.agents[agentID]; !exists {
		return fmt.Errorf("agent %s not found", agentID)
	}

	delete(d.agents, agentID)
	d.logger.Info("Unregistered agent: %s", agentID)

	return nil
}

func (d *Dispatcher) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return fmt.Errorf("dispatcher is already running")
	}
	d.running = true
	d.mu.Unlock()

	d.logger.Info("Starting dispatcher")

	// Start message processing worker
	d.wg.Add(1)
	go d.messageProcessor(ctx)

	return nil
}

func (d *Dispatcher) Stop(ctx context.Context) error {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return nil
	}
	d.running = false
	d.mu.Unlock()

	d.logger.Info("Stopping dispatcher")

	// Signal shutdown
	close(d.shutdown)

	// Wait for workers to finish
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		d.logger.Info("Dispatcher stopped successfully")
		return nil
	case <-ctx.Done():
		d.logger.Warn("Dispatcher stop timed out")
		return ctx.Err()
	}
}

func (d *Dispatcher) DispatchMessage(msg *proto.AgentMsg) error {
	d.mu.RLock()
	running := d.running
	d.mu.RUnlock()

	if !running {
		return fmt.Errorf("dispatcher is not running")
	}

	// Log incoming message
	if err := d.eventLog.WriteMessage(msg); err != nil {
		d.logger.Error("Failed to log incoming message: %v", err)
		// Continue processing even if logging fails
	}

	select {
	case d.inputChan <- msg:
		d.logger.Debug("Queued message %s: %s → %s", msg.ID, msg.FromAgent, msg.ToAgent)
		return nil
	default:
		return fmt.Errorf("message queue is full")
	}
}

func (d *Dispatcher) messageProcessor(ctx context.Context) {
	defer d.wg.Done()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Message processor stopped by context")
			return
		case <-d.shutdown:
			d.logger.Info("Message processor stopped by shutdown signal")
			return
		case msg := <-d.inputChan:
			d.processMessage(ctx, msg)
		}
	}
}

func (d *Dispatcher) processMessage(ctx context.Context, msg *proto.AgentMsg) {
	d.logger.Debug("Processing message %s: %s → %s (%s)", msg.ID, msg.FromAgent, msg.ToAgent, msg.Type)

	// Find target agent
	d.mu.RLock()
	targetAgent, exists := d.agents[msg.ToAgent]
	d.mu.RUnlock()

	if !exists {
		d.sendErrorResponse(msg, fmt.Errorf("target agent %s not found", msg.ToAgent))
		return
	}

	// Process with retry logic
	response := d.processWithRetry(ctx, msg, targetAgent)

	// If there's a response, send it back
	if response.Message != nil {
		d.sendResponse(response.Message)
	} else if response.Error != nil {
		d.sendErrorResponse(msg, response.Error)
	}
}

func (d *Dispatcher) processWithRetry(ctx context.Context, msg *proto.AgentMsg, agent Agent) *DispatchResult {
	maxRetries := d.config.MaxRetryAttempts
	backoffMultiplier := d.config.RetryBackoffMultiplier

	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Extract model name from agent logID (format: "model:id")
		modelName := msg.ToAgent
		if parts := strings.Split(msg.ToAgent, ":"); len(parts) >= 2 {
			modelName = parts[0]
		}
		
		// Check rate limiting before each attempt
		if err := d.checkRateLimit(modelName); err != nil {
			d.logger.Warn("Rate limit exceeded for %s (model %s): %v", msg.ToAgent, modelName, err)
			return &DispatchResult{Error: err}
		}

		// Create retry message
		retryMsg := msg.Clone()
		retryMsg.RetryCount = attempt

		// Process message
		d.logger.Debug("Attempt %d/%d for message %s", attempt+1, maxRetries+1, msg.ID)

		response, err := agent.ProcessMessage(ctx, retryMsg)

		// Release agent slot after processing attempt
		d.rateLimiter.ReleaseAgent(modelName)

		if err == nil {
			// Success
			d.logger.Debug("Message %s processed successfully on attempt %d", msg.ID, attempt+1)

			return &DispatchResult{Message: response}
		}

		lastErr = err
		d.logger.Warn("Attempt %d failed for message %s: %v", attempt+1, msg.ID, err)

		// Don't wait after the last attempt
		if attempt < maxRetries {
			backoffDuration := time.Duration(math.Pow(backoffMultiplier, float64(attempt))) * time.Second
			d.logger.Debug("Waiting %v before retry", backoffDuration)

			select {
			case <-ctx.Done():
				return &DispatchResult{Error: ctx.Err()}
			case <-time.After(backoffDuration):
				// Continue to next attempt
			}
		}
	}

	d.logger.Error("All retry attempts failed for message %s: %v", msg.ID, lastErr)
	return &DispatchResult{Error: fmt.Errorf("failed after %d attempts: %w", maxRetries+1, lastErr)}
}

func (d *Dispatcher) checkRateLimit(agentModel string) error {
	// Reserve agent slot
	if err := d.rateLimiter.ReserveAgent(agentModel); err != nil {
		return err
	}

	// For now, we don't know token count in advance, so we'll reserve a default amount
	// In a real implementation, this might be estimated or configured
	defaultTokenReservation := 100

	if err := d.rateLimiter.Reserve(agentModel, defaultTokenReservation); err != nil {
		// Release the agent slot if token reservation fails
		d.rateLimiter.ReleaseAgent(agentModel)
		return err
	}

	return nil
}

func (d *Dispatcher) sendResponse(response *proto.AgentMsg) {
	// In a full implementation, this would route the response back to the original sender
	// For now, we just log it and write to event log
	d.logger.Info("Received response %s: %s → %s (%s)", response.ID, response.FromAgent, response.ToAgent, response.Type)

	if err := d.eventLog.WriteMessage(response); err != nil {
		d.logger.Error("Failed to log response message: %v", err)
	}
}

func (d *Dispatcher) sendErrorResponse(originalMsg *proto.AgentMsg, err error) {
	errorMsg := proto.NewAgentMsg(proto.MsgTypeERROR, "dispatcher", originalMsg.FromAgent)
	errorMsg.ParentMsgID = originalMsg.ID
	errorMsg.SetPayload("error", err.Error())
	errorMsg.SetPayload("original_message_id", originalMsg.ID)
	errorMsg.SetMetadata("error_type", "processing_error")

	d.logger.Error("Sending error response for message %s: %v", originalMsg.ID, err)

	if logErr := d.eventLog.WriteMessage(errorMsg); logErr != nil {
		d.logger.Error("Failed to log error message: %v", logErr)
	}
}

func (d *Dispatcher) GetStats() map[string]interface{} {
	d.mu.RLock()
	defer d.mu.RUnlock()

	agentList := make([]string, 0, len(d.agents))
	for agentID := range d.agents {
		agentList = append(agentList, agentID)
	}

	return map[string]interface{}{
		"running":        d.running,
		"agents":         agentList,
		"queue_length":   len(d.inputChan),
		"queue_capacity": cap(d.inputChan),
	}
}
