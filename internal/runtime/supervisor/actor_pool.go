package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
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

// AddServerAt installs (or keeps) the manager instance serving cfg at commit
// generation gen. upstream.ErrStaleGeneration when gen is no longer current.
func (p *ActorPoolSimple) AddServerAt(name string, cfg *config.ServerConfig, gen int64) error {
	p.logger.Debug("Adding server via manager", zap.String("name", name), zap.Int64("generation", gen))

	if err := p.manager.AddServerConfigAt(name, cfg, gen); err != nil {
		if errors.Is(err, upstream.ErrStaleGeneration) {
			return err
		}
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

// RemoveServerAt removes the manager instance serving name (see
// upstream.Manager.RemoveServerAt for the gen-0 removal semantics).
func (p *ActorPoolSimple) RemoveServerAt(name string, gen int64) error {
	p.logger.Debug("Removing server via manager", zap.String("name", name), zap.Int64("generation", gen))

	if err := p.manager.RemoveServerAt(name, gen); err != nil {
		return err
	}

	// Emit event
	p.emitEvent(Event{
		Type:       EventServerRemoved,
		ServerName: name,
		Timestamp:  time.Now(),
		Payload:    map[string]interface{}{},
	})

	return nil
}

// ConnectServerAt connects the manager instance serving name at gen. A connect
// error other than a stale generation is logged and not returned: the
// supervisor's next reconcile retries (as before Spec 116 PR3).
func (p *ActorPoolSimple) ConnectServerAt(ctx context.Context, name string, gen int64) error {
	p.logger.Debug("Connecting server via manager", zap.String("name", name), zap.Int64("generation", gen))

	if err := p.manager.ConnectServerAt(ctx, name, gen); err != nil {
		if errors.Is(err, upstream.ErrStaleGeneration) {
			return err
		}
		p.logger.Debug("Connect returned error (may be already connecting/connected)",
			zap.String("server", name),
			zap.Error(err))
	}
	return nil
}

// DisconnectServerAt disconnects exactly instance inst serving name at gen. It
// never resolves the name to whatever instance serves it now (review R5.2).
func (p *ActorPoolSimple) DisconnectServerAt(name string, gen int64, inst managed.InstanceID) error {
	p.logger.Debug("Disconnecting server via manager", zap.String("name", name), zap.Int64("generation", gen))
	return p.manager.DisconnectServerAt(name, gen, inst)
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
		Name:            name,
		Config:          config,
		Enabled:         config.Enabled,
		Connected:       connected,
		ConnectionEpoch: client.ConnectionEpoch(),
		InstanceID:      client.InstanceID(),
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
			Name:            name,
			Config:          client.GetConfig(),
			Enabled:         client.GetConfig().Enabled,
			Connected:       connected,
			ConnectionEpoch: client.ConnectionEpoch(),
			InstanceID:      client.InstanceID(),
		}

		if client.GetConfig().Quarantined {
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
