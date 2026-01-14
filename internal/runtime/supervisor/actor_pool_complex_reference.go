package supervisor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/supervisor/actor"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// ActorPool manages the lifecycle of server actors and provides stats for Supervisor.
// This replaces UpstreamAdapter with direct Actor integration (Phase 7.2).
type ActorPool struct {
	actors   map[string]*actor.Actor
	mu       sync.RWMutex
	logger   *zap.Logger
	manager  *upstream.Manager // Use existing manager for client creation

	// Event aggregation
	eventCh   chan Event
	listeners []chan Event
	eventMu   sync.RWMutex
}

// NewActorPool creates a new actor pool.
func NewActorPool(manager *upstream.Manager, logger *zap.Logger) *ActorPool {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &ActorPool{
		actors:    make(map[string]*actor.Actor),
		logger:    logger,
		manager:   manager,
		eventCh:   make(chan Event, 100),
		listeners: make([]chan Event, 0),
	}
}

// AddServer creates and starts an actor for the given server.
// If the actor already exists, it updates the configuration.
func (p *ActorPool) AddServer(name string, cfg *config.ServerConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Phase 7.2: If actor already exists, just update config instead of failing
	if a, exists := p.actors[name]; exists {
		p.logger.Debug("Actor already exists, updating config", zap.String("server", name))
		// Update client config in manager
		if err := p.manager.AddServerConfig(name, cfg); err != nil {
			return fmt.Errorf("failed to update server config: %w", err)
		}
		// Send update command to actor
		a.SendCommand(actor.Command{
			Type: actor.CommandUpdateConfig,
			Data: map[string]interface{}{
				"config": cfg,
			},
		})
		return nil
	}

	// Phase 7.2: Use UpstreamManager to add server config and get client
	// This maintains compatibility with existing OAuth, notifications, etc.
	if err := p.manager.AddServerConfig(name, cfg); err != nil {
		return fmt.Errorf("failed to add server config: %w", err)
	}

	client, exists := p.manager.GetClient(name)
	if !exists {
		return fmt.Errorf("failed to get client for %s after adding config", name)
	}

	// Create actor with config
	actorCfg := actor.ActorConfig{
		Name:           name,
		ServerConfig:   cfg,
		MaxRetries:     5,
		RetryInterval:  5 * time.Second,
		ConnectTimeout: 30 * time.Second,
	}

	a := actor.New(actorCfg, client, p.logger)

	// Subscribe to actor events
	go p.forwardActorEvents(name, a)

	// Start actor
	a.Start()

	// Send connect command if enabled
	if cfg.Enabled && !cfg.Quarantined {
		a.SendCommand(actor.Command{
			Type: actor.CommandConnect,
		})
	}

	p.actors[name] = a
	p.logger.Info("Actor started", zap.String("server", name))

	// Emit event
	p.emitEvent(Event{
		Type:       EventServerAdded,
		ServerName: name,
		Timestamp:  time.Now(),
		Payload: map[string]interface{}{
			"enabled":     cfg.Enabled,
			"quarantined": cfg.Quarantined,
		},
	})

	return nil
}

// RemoveServer stops and removes an actor.
func (p *ActorPool) RemoveServer(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	a, exists := p.actors[name]
	if !exists {
		return fmt.Errorf("actor not found: %s", name)
	}

	a.Stop()
	delete(p.actors, name)

	// Also remove from manager
	p.manager.RemoveServer(name)

	p.logger.Info("Actor stopped", zap.String("server", name))

	// Emit event
	p.emitEvent(Event{
		Type:       EventServerRemoved,
		ServerName: name,
		Timestamp:  time.Now(),
		Payload:    map[string]interface{}{},
	})

	return nil
}

// ConnectServer sends a connect command to an actor.
func (p *ActorPool) ConnectServer(ctx context.Context, name string) error {
	p.mu.RLock()
	a, exists := p.actors[name]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("actor not found: %s", name)
	}

	a.SendCommand(actor.Command{
		Type: actor.CommandConnect,
	})

	return nil
}

// DisconnectServer sends a disconnect command to an actor.
func (p *ActorPool) DisconnectServer(name string) error {
	p.mu.RLock()
	a, exists := p.actors[name]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("actor not found: %s", name)
	}

	a.SendCommand(actor.Command{
		Type: actor.CommandDisconnect,
	})

	return nil
}

// ConnectAll sends connect commands to all actors.
func (p *ActorPool) ConnectAll(ctx context.Context) error {
	p.mu.RLock()
	actors := make([]*actor.Actor, 0, len(p.actors))
	for _, a := range p.actors {
		actors = append(actors, a)
	}
	p.mu.RUnlock()

	for _, a := range actors {
		a.SendCommand(actor.Command{
			Type: actor.CommandConnect,
		})
	}

	return nil
}

// GetServerState returns the current state of a server from its actor.
func (p *ActorPool) GetServerState(name string) (*ServerState, error) {
	p.mu.RLock()
	a, exists := p.actors[name]
	p.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("actor not found: %s", name)
	}

	client := a.GetClient()
	if client == nil {
		return &ServerState{
			Name:      name,
			Connected: false,
		}, nil
	}

	state := &ServerState{
		Name:      name,
		Config:    client.Config,
		Enabled:   client.Config.Enabled,
		Connected: client.IsConnected(),
	}

	if client.Config.Quarantined {
		state.Quarantined = true
	}

	// Get connection info
	connInfo := client.GetConnectionInfo()
	state.ConnectionInfo = &connInfo

	return state, nil
}

// GetAllStates returns the current state of all servers.
func (p *ActorPool) GetAllStates() map[string]*ServerState {
	p.mu.RLock()
	actors := make(map[string]*actor.Actor, len(p.actors))
	for name, a := range p.actors {
		actors[name] = a
	}
	p.mu.RUnlock()

	states := make(map[string]*ServerState, len(actors))

	for name, a := range actors {
		client := a.GetClient()
		if client == nil {
			states[name] = &ServerState{
				Name:      name,
				Connected: false,
			}
			continue
		}

		connected := client.IsConnected()
		state := &ServerState{
			Name:      name,
			Config:    client.Config,
			Enabled:   client.Config.Enabled,
			Connected: connected,
		}

		if client.Config.Quarantined {
			state.Quarantined = true
		}

		// Get connection info
		connInfo := client.GetConnectionInfo()
		state.ConnectionInfo = &connInfo

		// Phase 7.2: Fetch tools for connected servers
		if connected {
			if tools, err := client.ListTools(context.Background()); err == nil {
				state.Tools = tools
				state.ToolCount = len(tools)
			}
		}

		states[name] = state
	}

	return states
}

// IsUserLoggedOut returns true if the user explicitly logged out from the server.
// This prevents automatic reconnection after explicit logout.
func (p *ActorPool) IsUserLoggedOut(name string) bool {
	p.mu.RLock()
	a, exists := p.actors[name]
	p.mu.RUnlock()

	if !exists {
		return false
	}

	client := a.GetClient()
	if client == nil {
		return false
	}
	return client.IsUserLoggedOut()
}

// forwardActorEvents subscribes to actor events and forwards them as supervisor events.
func (p *ActorPool) forwardActorEvents(name string, a *actor.Actor) {
	events := a.Events()

	for event := range events {
		// Convert actor events to supervisor events
		var eventType EventType

		switch event.Type {
		case actor.EventConnected:
			eventType = EventServerConnected
		case actor.EventDisconnected:
			eventType = EventServerDisconnected
		case actor.EventStateChanged, actor.EventRetrying, actor.EventError:
			eventType = EventServerStateChanged
		default:
			continue
		}

		// Emit supervisor event
		p.emitEvent(Event{
			Type:       eventType,
			ServerName: name,
			Timestamp:  event.Timestamp,
			Payload: map[string]interface{}{
				"connected":    event.State == actor.StateConnected,
				"state":        string(event.State),
				"actor_event":  string(event.Type),
			},
		})
	}
}

// Subscribe returns a channel that receives supervisor events.
func (p *ActorPool) Subscribe() <-chan Event {
	p.eventMu.Lock()
	defer p.eventMu.Unlock()

	ch := make(chan Event, 50)
	p.listeners = append(p.listeners, ch)
	return ch
}

// Unsubscribe removes a subscriber channel.
func (p *ActorPool) Unsubscribe(ch <-chan Event) {
	p.eventMu.Lock()
	defer p.eventMu.Unlock()

	for i, listener := range p.listeners {
		if listener == ch {
			p.listeners = append(p.listeners[:i], p.listeners[i+1:]...)
			close(listener)
			break
		}
	}
}

// emitEvent sends an event to all subscribers.
func (p *ActorPool) emitEvent(event Event) {
	p.eventMu.RLock()
	defer p.eventMu.RUnlock()

	for _, ch := range p.listeners {
		select {
		case ch <- event:
		default:
			p.logger.Warn("Event channel full, dropping event",
				zap.String("event_type", string(event.Type)),
				zap.String("server", event.ServerName))
		}
	}
}

// Close cleans up the actor pool.
func (p *ActorPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Stop all actors
	for name, a := range p.actors {
		a.Stop()
		p.logger.Info("Actor stopped during cleanup", zap.String("server", name))
	}
	p.actors = nil

	// Close event channels
	p.eventMu.Lock()
	for _, ch := range p.listeners {
		close(ch)
	}
	p.listeners = nil
	p.eventMu.Unlock()
}
