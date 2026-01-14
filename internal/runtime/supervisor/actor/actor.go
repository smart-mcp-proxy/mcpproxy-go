package actor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Actor represents a single server actor with its own goroutine and state machine.
// Each actor manages the lifecycle of one upstream server connection independently.
type Actor struct {
	config ActorConfig
	logger *zap.Logger

	// State machine
	state atomic.Value // State
	mu    sync.RWMutex  // Protects state transitions

	// Communication channels
	commandCh chan Command
	eventCh   chan Event

	// Underlying client (wrapped, not directly exposed)
	client *managed.Client
	clientMu sync.RWMutex

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Retry tracking
	retryCount atomic.Int32
	lastError  error
	lastErrorMu sync.RWMutex
}

// New creates a new actor for a server.
func New(cfg ActorConfig, client *managed.Client, logger *zap.Logger) *Actor {
	if logger == nil {
		logger = zap.NewNop()
	}

	ctx, cancel := context.WithCancel(context.Background())

	a := &Actor{
		config:    cfg,
		logger:    logger.With(zap.String("actor", cfg.Name)),
		commandCh: make(chan Command, 10),
		eventCh:   make(chan Event, 50),
		client:    client,
		ctx:       ctx,
		cancel:    cancel,
	}

	a.state.Store(StateIdle)

	return a
}

// Start begins the actor's event loop in a dedicated goroutine.
func (a *Actor) Start() {
	a.wg.Add(1)
	go a.run()

	a.logger.Info("Actor started")
}

// run is the main event loop for the actor.
func (a *Actor) run() {
	defer a.wg.Done()
	defer a.setState(StateStopped)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	a.logger.Debug("Actor event loop starting")

	for {
		select {
		case <-a.ctx.Done():
			a.logger.Info("Actor stopping due to context cancellation")
			return

		case cmd := <-a.commandCh:
			a.handleCommand(cmd)

		case <-ticker.C:
			// Periodic health check
			a.checkHealth()
		}
	}
}

// handleCommand processes incoming commands.
func (a *Actor) handleCommand(cmd Command) {
	a.logger.Debug("Actor received command",
		zap.String("command", string(cmd.Type)),
		zap.String("current_state", string(a.GetState())))

	switch cmd.Type {
	case CommandConnect:
		a.handleConnect()

	case CommandDisconnect:
		a.handleDisconnect()

	case CommandUpdateConfig:
		a.handleUpdateConfig(cmd)

	case CommandStop:
		a.logger.Info("Actor received stop command")
		a.cancel()

	default:
		a.logger.Warn("Unknown command type", zap.String("type", string(cmd.Type)))
	}
}

// handleConnect attempts to establish a connection.
func (a *Actor) handleConnect() {
	currentState := a.GetState()

	// Validate state transition
	if currentState == StateConnected {
		a.logger.Debug("Already connected, ignoring connect command")
		return
	}

	if currentState == StateConnecting {
		a.logger.Debug("Connection in progress, ignoring connect command")
		return
	}

	// Transition to connecting
	a.setState(StateConnecting)
	a.emitEvent(Event{
		Type:      EventStateChanged,
		State:     StateConnecting,
		Timestamp: time.Now(),
	})

	// Attempt connection with timeout
	ctx, cancel := context.WithTimeout(a.ctx, a.config.ConnectTimeout)
	defer cancel()

	a.clientMu.RLock()
	client := a.client
	a.clientMu.RUnlock()

	if client == nil {
		a.handleConnectionError(fmt.Errorf("no client available"))
		return
	}

	// Perform connection
	if err := client.Connect(ctx); err != nil {
		a.handleConnectionError(err)
		return
	}

	// Connection successful
	a.retryCount.Store(0)
	a.lastErrorMu.Lock()
	a.lastError = nil
	a.lastErrorMu.Unlock()
	a.setState(StateConnected)

	a.emitEvent(Event{
		Type:      EventConnected,
		State:     StateConnected,
		Timestamp: time.Now(),
	})

	a.logger.Info("Actor successfully connected")
}

// handleConnectionError handles connection failures with retry logic.
func (a *Actor) handleConnectionError(err error) {
	retryCount := a.retryCount.Add(1)

	a.lastErrorMu.Lock()
	a.lastError = err
	a.lastErrorMu.Unlock()

	a.logger.Warn("Connection attempt failed",
		zap.Error(err),
		zap.Int("retry_count", int(retryCount)),
		zap.Int("max_retries", a.config.MaxRetries))

	a.setState(StateError)

	a.emitEvent(Event{
		Type:      EventError,
		State:     StateError,
		Timestamp: time.Now(),
		Error:     err,
		Data: map[string]interface{}{
			"retry_count": int(retryCount),
			"max_retries": a.config.MaxRetries,
		},
	})

	// Schedule retry if within limit
	if a.config.MaxRetries == 0 || int(retryCount) < a.config.MaxRetries {
		a.wg.Add(1)
		go a.scheduleRetry()
	} else {
		a.logger.Error("Max retries exceeded, giving up",
			zap.Int("retry_count", int(retryCount)))
	}
}

// scheduleRetry schedules a retry attempt after the configured interval.
func (a *Actor) scheduleRetry() {
	defer a.wg.Done()

	retryCount := a.retryCount.Load()

	a.emitEvent(Event{
		Type:      EventRetrying,
		State:     StateError,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"retry_in":    a.config.RetryInterval.String(),
			"retry_count": int(retryCount),
		},
	})

	select {
	case <-time.After(a.config.RetryInterval):
		// Check context again before sending to avoid race with Stop()
		select {
		case <-a.ctx.Done():
			return
		default:
		}

		// Send connect command to retry
		select {
		case a.commandCh <- Command{Type: CommandConnect}:
			a.logger.Debug("Retry scheduled", zap.Int("attempt", int(retryCount)))
		case <-a.ctx.Done():
			return
		}
	case <-a.ctx.Done():
		return
	}
}

// handleDisconnect gracefully disconnects the client.
func (a *Actor) handleDisconnect() {
	currentState := a.GetState()

	if currentState != StateConnected {
		a.logger.Debug("Not connected, ignoring disconnect command",
			zap.String("state", string(currentState)))
		return
	}

	a.setState(StateDisconnecting)

	a.clientMu.RLock()
	client := a.client
	a.clientMu.RUnlock()

	if client != nil {
		if err := client.Disconnect(); err != nil {
			a.logger.Warn("Error during disconnect", zap.Error(err))
		}
	}

	a.setState(StateIdle)

	a.emitEvent(Event{
		Type:      EventDisconnected,
		State:     StateIdle,
		Timestamp: time.Now(),
	})

	a.logger.Info("Actor disconnected")
}

// handleUpdateConfig updates the actor's configuration.
func (a *Actor) handleUpdateConfig(cmd Command) {
	a.logger.Info("Updating actor configuration")

	// Extract new config from command
	if newConfig, ok := cmd.Data["config"].(*config.ServerConfig); ok {
		a.mu.Lock()
		a.config.ServerConfig = newConfig
		a.mu.Unlock()

		// If connected, reconnect with new config
		if a.GetState() == StateConnected {
			a.logger.Info("Configuration changed while connected, triggering reconnect")
			a.handleDisconnect()
			time.Sleep(100 * time.Millisecond) // Brief delay before reconnecting
			a.handleConnect()
		}
	}
}

// checkHealth performs periodic health checks.
func (a *Actor) checkHealth() {
	currentState := a.GetState()

	if currentState != StateConnected {
		return
	}

	a.clientMu.RLock()
	client := a.client
	a.clientMu.RUnlock()

	if client == nil || !client.IsConnected() {
		a.logger.Warn("Health check failed: client not connected")
		a.setState(StateError)
		a.emitEvent(Event{
			Type:      EventError,
			State:     StateError,
			Timestamp: time.Now(),
			Error:     fmt.Errorf("connection lost during health check"),
		})
	}
}

// SendCommand sends a command to the actor.
func (a *Actor) SendCommand(cmd Command) {
	select {
	case a.commandCh <- cmd:
	case <-a.ctx.Done():
		a.logger.Warn("Cannot send command, actor is stopped")
	}
}

// Events returns the event channel for this actor.
func (a *Actor) Events() <-chan Event {
	return a.eventCh
}

// GetState returns the current state (thread-safe).
func (a *Actor) GetState() State {
	return a.state.Load().(State)
}

// setState updates the state (thread-safe).
func (a *Actor) setState(newState State) {
	a.mu.Lock()
	defer a.mu.Unlock()

	oldState := a.state.Load().(State)
	if oldState == newState {
		return
	}

	a.state.Store(newState)

	a.logger.Debug("Actor state transition",
		zap.String("from", string(oldState)),
		zap.String("to", string(newState)))
}

// emitEvent sends an event to subscribers.
func (a *Actor) emitEvent(event Event) {
	select {
	case a.eventCh <- event:
	default:
		a.logger.Warn("Event channel full, dropping event",
			zap.String("event_type", string(event.Type)))
	}
}

// Stop gracefully stops the actor.
func (a *Actor) Stop() {
	a.logger.Info("Stopping actor")

	// Send stop command
	select {
	case a.commandCh <- Command{Type: CommandStop}:
	case <-time.After(1 * time.Second):
		// Force cancel if command doesn't go through
		a.cancel()
	}

	// Wait for goroutine to finish
	a.wg.Wait()

	// Close channels
	close(a.commandCh)
	close(a.eventCh)

	a.logger.Info("Actor stopped")
}

// UpdateClient updates the underlying managed client (used during reconfiguration).
func (a *Actor) UpdateClient(client *managed.Client) {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()

	a.client = client
	a.logger.Debug("Actor client updated")
}

// GetClient returns the current managed client (for compatibility).
func (a *Actor) GetClient() *managed.Client {
	a.clientMu.RLock()
	defer a.clientMu.RUnlock()
	return a.client
}
