package supervisor

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/upstream"
)

// UpstreamAdapter wraps upstream.Manager and provides a supervisor-friendly interface.
// It translates supervisor commands into upstream manager operations and emits events.
type UpstreamAdapter struct {
	manager   *upstream.Manager
	logger    *zap.Logger
	eventCh   chan Event
	eventMu   sync.RWMutex
	listeners []chan Event
}

// NewUpstreamAdapter creates a new adapter wrapping the given upstream manager.
func NewUpstreamAdapter(manager *upstream.Manager, logger *zap.Logger) *UpstreamAdapter {
	if logger == nil {
		logger = zap.NewNop()
	}

	adapter := &UpstreamAdapter{
		manager:   manager,
		logger:    logger,
		eventCh:   make(chan Event, 100), // Buffered for async event emission
		listeners: make([]chan Event, 0),
	}

	// Set up notification handler to capture state changes from managed clients
	manager.AddNotificationHandler(adapter)

	return adapter
}

// SendNotification implements upstream.NotificationHandler interface
func (a *UpstreamAdapter) SendNotification(notification *upstream.Notification) {
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
			"level":   notification.Level.String(),
			"title":   notification.Title,
			"message": notification.Message,
		},
	}

	a.emitEvent(event)
}

// AddServer adds a server to the upstream manager.
func (a *UpstreamAdapter) AddServer(name string, cfg *config.ServerConfig) error {
	a.logger.Debug("Adapter: Adding server", zap.String("name", name))

	if err := a.manager.AddServerConfig(name, cfg); err != nil {
		return fmt.Errorf("failed to add server config: %w", err)
	}

	a.emitEvent(Event{
		Type:       EventServerAdded,
		ServerName: name,
		Payload: map[string]interface{}{
			"enabled":     cfg.Enabled,
			"quarantined": cfg.Quarantined,
		},
	})

	return nil
}

// RemoveServer removes a server from the upstream manager.
func (a *UpstreamAdapter) RemoveServer(name string) error {
	a.logger.Debug("Adapter: Removing server", zap.String("name", name))

	a.manager.RemoveServer(name)

	a.emitEvent(Event{
		Type:       EventServerRemoved,
		ServerName: name,
		Payload:    map[string]interface{}{},
	})

	return nil
}

// ConnectServer attempts to connect a specific server.
func (a *UpstreamAdapter) ConnectServer(ctx context.Context, name string) error {
	a.logger.Debug("Adapter: Connecting server", zap.String("name", name))

	// Upstream manager handles connection automatically via managed clients
	// We just need to ensure the server is added and enabled
	// The managed client will handle the actual connection attempt
	return nil
}

// DisconnectServer disconnects a specific server.
func (a *UpstreamAdapter) DisconnectServer(name string) error {
	a.logger.Debug("Adapter: Disconnecting server", zap.String("name", name))

	// Get client and disconnect
	client, exists := a.manager.GetClient(name)
	if !exists {
		return fmt.Errorf("server %s not found", name)
	}

	return client.Disconnect()
}

// ConnectAll attempts to connect all enabled servers.
func (a *UpstreamAdapter) ConnectAll(ctx context.Context) error {
	a.logger.Debug("Adapter: Connecting all servers")

	return a.manager.ConnectAll(ctx)
}

// GetServerState returns the current state of a server from the upstream manager.
func (a *UpstreamAdapter) GetServerState(name string) (*ServerState, error) {
	client, exists := a.manager.GetClient(name)
	if !exists {
		return nil, fmt.Errorf("server %s not found", name)
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
func (a *UpstreamAdapter) GetAllStates() map[string]*ServerState {
	stats := a.manager.GetStats()
	states := make(map[string]*ServerState)

	// Extract server states from stats
	if serversMap, ok := stats["servers"].(map[string]interface{}); ok {
		for name, serverInfo := range serversMap {
			if serverMap, ok := serverInfo.(map[string]interface{}); ok {
				connected := getBool(serverMap, "connected")

				state := &ServerState{
					Name:      name,
					Connected: connected,
					ToolCount: getInt(serverMap, "tool_count"),
				}

				// Phase 7.1: Fetch tools for connected servers
				if connected {
					if client, exists := a.manager.GetClient(name); exists {
						if tools, err := client.ListTools(context.Background()); err == nil {
							state.Tools = tools
							state.ToolCount = len(tools) // Update count from actual tools
						}
					}
				}

				states[name] = state
			}
		}
	}

	return states
}

// Subscribe returns a channel that receives supervisor events.
func (a *UpstreamAdapter) Subscribe() <-chan Event {
	a.eventMu.Lock()
	defer a.eventMu.Unlock()

	ch := make(chan Event, 50)
	a.listeners = append(a.listeners, ch)
	return ch
}

// Unsubscribe removes a subscriber channel.
func (a *UpstreamAdapter) Unsubscribe(ch <-chan Event) {
	a.eventMu.Lock()
	defer a.eventMu.Unlock()

	for i, listener := range a.listeners {
		if listener == ch {
			a.listeners = append(a.listeners[:i], a.listeners[i+1:]...)
			close(listener)
			break
		}
	}
}

// emitEvent sends an event to all subscribers.
func (a *UpstreamAdapter) emitEvent(event Event) {
	a.eventMu.RLock()
	defer a.eventMu.RUnlock()

	for _, ch := range a.listeners {
		select {
		case ch <- event:
		default:
			a.logger.Warn("Event channel full, dropping event",
				zap.String("event_type", string(event.Type)),
				zap.String("server", event.ServerName))
		}
	}
}

// Close cleans up the adapter.
func (a *UpstreamAdapter) Close() {
	a.eventMu.Lock()
	defer a.eventMu.Unlock()

	for _, ch := range a.listeners {
		close(ch)
	}
	a.listeners = nil
}

// Helper functions for type assertions
func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok {
		switch i := v.(type) {
		case int:
			return i
		case int64:
			return int(i)
		case float64:
			return int(i)
		}
	}
	return 0
}
