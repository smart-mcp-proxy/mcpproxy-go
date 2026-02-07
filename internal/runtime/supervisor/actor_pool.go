package supervisor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// ActorPoolSimple is a simplified facade over UpstreamManager that delegates all operations.
// Phase 7.3: Avoids double lifecycle management by using UpstreamManager's existing client management.
type ActorPoolSimple struct {
	manager *upstream.Manager
	logger  *zap.Logger

	// Event forwarding
	eventCh   chan Event
	listeners []chan Event
	eventMu   sync.RWMutex
}

// NewActorPoolSimple creates a simplified actor pool that delegates to UpstreamManager.
func NewActorPoolSimple(manager *upstream.Manager, logger *zap.Logger) *ActorPoolSimple {
	if logger == nil {
		logger = zap.NewNop()
	}

	pool := &ActorPoolSimple{
		manager:   manager,
		logger:    logger,
		eventCh:   make(chan Event, 100),
		listeners: make([]chan Event, 0),
	}

	// Subscribe to manager notifications and forward as events
	manager.AddNotificationHandler(pool)

	return pool
}

// SendNotification implements upstream.NotificationHandler interface
func (p *ActorPoolSimple) SendNotification(notification *upstream.Notification) {
	// Convert upstream notifications to supervisor events
	var eventType EventType

	switch notification.Level {
	case upstream.NotificationInfo:
		if notification.Title == "Server Connected" {
			eventType = EventServerConnected
		} else {
			eventType = EventServerStateChanged
		}
	case upstream.NotificationWarning, upstream.NotificationError:
		if notification.Title == "Server Disconnected" {
			eventType = EventServerDisconnected
		} else {
			eventType = EventServerStateChanged
		}
	default:
		eventType = EventServerStateChanged
	}

	event := Event{
		Type:       eventType,
		ServerName: notification.ServerName,
		Timestamp:  notification.Timestamp,
		Payload: map[string]interface{}{
			"level":     notification.Level.String(),
			"title":     notification.Title,
			"message":   notification.Message,
			"connected": notification.Title == "Server Connected",
		},
	}

	p.emitEvent(event)
}

// AddServer adds a server configuration to the manager.
func (p *ActorPoolSimple) AddServer(name string, cfg *config.ServerConfig) error {
	p.logger.Debug("Adding server via manager", zap.String("name", name))

	if err := p.manager.AddServerConfig(name, cfg); err != nil {
		return fmt.Errorf("failed to add server config: %w", err)
	}

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

// RemoveServer removes a server from the manager.
func (p *ActorPoolSimple) RemoveServer(name string) error {
	p.logger.Debug("Removing server via manager", zap.String("name", name))

	p.manager.RemoveServer(name)

	// Emit event
	p.emitEvent(Event{
		Type:       EventServerRemoved,
		ServerName: name,
		Timestamp:  time.Now(),
		Payload:    map[string]interface{}{},
	})

	return nil
}

// ConnectServer tells the manager to connect a server.
func (p *ActorPoolSimple) ConnectServer(ctx context.Context, name string) error {
	p.logger.Debug("Connecting server via manager", zap.String("name", name))

	client, exists := p.manager.GetClient(name)
	if !exists {
		return fmt.Errorf("server %s not found", name)
	}

	// Attempt to connect (managed client will handle state checks)
	// If client is already connecting/connected, Connect() will return an error
	// which we log but don't treat as fatal (supervisor will retry)
	if err := client.Connect(ctx); err != nil {
		p.logger.Debug("Connect returned error (may be already connecting/connected)",
			zap.String("server", name),
			zap.Error(err))
		// Not returning error - this is expected if client is already connecting
		// Supervisor's reconciliation logic will handle retries if needed
	}

	return nil
}

// DisconnectServer tells the manager to disconnect a server.
func (p *ActorPoolSimple) DisconnectServer(name string) error {
	p.logger.Debug("Disconnecting server via manager", zap.String("name", name))

	client, exists := p.manager.GetClient(name)
	if !exists {
		return fmt.Errorf("server %s not found", name)
	}

	return client.Disconnect()
}

// ConnectAll tells the manager to connect all servers.
func (p *ActorPoolSimple) ConnectAll(ctx context.Context) error {
	p.logger.Debug("Connecting all servers via manager")
	return p.manager.ConnectAll(ctx)
}

// GetServerState returns the current state of a server from the manager.
// Phase 7.1 FIX: Fetches tools for the specific server to avoid blocking all servers.
func (p *ActorPoolSimple) GetServerState(name string) (*ServerState, error) {
	client, exists := p.manager.GetClient(name)
	if !exists {
		return nil, fmt.Errorf("server %s not found", name)
	}

	connected := client.IsConnected()
	config := client.GetConfig() // Thread-safe config access

	state := &ServerState{
		Name:      name,
		Config:    config,
		Enabled:   config.Enabled,
		Connected: connected,
	}

	if config.Quarantined {
		state.Quarantined = true
	}

	// Get connection info
	connInfo := client.GetConnectionInfo()
	state.ConnectionInfo = &connInfo

	// Phase 7.1 FIX: Don't fetch tools here! It blocks on slow servers.
	// Tools are populated via:
	// 1. Background tool indexing (runtime.DiscoverAndIndexTools)
	// 2. Cached tool count from managed client (non-blocking)
	// State updates happen via events, not synchronous tool fetching.
	if connected {
		// Use cached tool count (non-blocking) instead of fetching tools
		state.ToolCount = client.GetCachedToolCountNonBlocking()
	}

	return state, nil
}

// GetAllStates returns the current state of all servers from the manager.
func (p *ActorPoolSimple) GetAllStates() map[string]*ServerState {
	states := make(map[string]*ServerState)

	// Get all clients from manager
	clients := p.manager.GetAllClients()

	for name, client := range clients {
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

		// Phase 7.1 FIX: Don't fetch tools synchronously! Use cached data instead.
		if connected {
			// Use cached tool count (non-blocking)
			state.ToolCount = client.GetCachedToolCountNonBlocking()
		}

		states[name] = state
	}

	return states
}

// IsUserLoggedOut returns true if the user explicitly logged out from the server.
// This prevents automatic reconnection after explicit logout.
func (p *ActorPoolSimple) IsUserLoggedOut(name string) bool {
	client, exists := p.manager.GetClient(name)
	if !exists {
		return false
	}
	return client.IsUserLoggedOut()
}

// Subscribe returns a channel that receives supervisor events.
func (p *ActorPoolSimple) Subscribe() <-chan Event {
	p.eventMu.Lock()
	defer p.eventMu.Unlock()

	ch := make(chan Event, 50)
	p.listeners = append(p.listeners, ch)
	return ch
}

// Unsubscribe removes a subscriber channel.
func (p *ActorPoolSimple) Unsubscribe(ch <-chan Event) {
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
func (p *ActorPoolSimple) emitEvent(event Event) {
	p.eventMu.RLock()
	defer p.eventMu.RUnlock()

	for _, ch := range p.listeners {
		select {
		case ch <- event:
		default:
			p.logger.Warn("⚠️ Event channel full, DROPPING event (stateview may become stale)",
				zap.String("event_type", string(event.Type)),
				zap.String("server", event.ServerName),
				zap.Any("connected", event.Payload["connected"]),
				zap.Any("title", event.Payload["title"]))
		}
	}
}

// Close cleans up the pool.
func (p *ActorPoolSimple) Close() {
	p.eventMu.Lock()
	defer p.eventMu.Unlock()

	for _, ch := range p.listeners {
		close(ch)
	}
	p.listeners = nil
}
