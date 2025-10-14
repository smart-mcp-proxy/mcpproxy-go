package supervisor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/runtime/configsvc"
	"mcpproxy-go/internal/runtime/stateview"
)

// Supervisor manages the desired vs actual state reconciliation for upstream servers.
// It subscribes to config changes and emits events when server states change.
type Supervisor struct {
	logger *zap.Logger

	// Config service for desired state
	configSvc *configsvc.Service

	// Upstream adapter for actual state
	upstream UpstreamInterface

	// State tracking
	snapshot atomic.Value // *ServerStateSnapshot
	version  int64
	stateMu  sync.RWMutex

	// State view for read model (Phase 4)
	stateView *stateview.View

	// Event publishing
	eventCh   chan Event
	listeners []chan Event
	eventMu   sync.RWMutex

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// UpstreamInterface defines the interface for upstream adapters.
type UpstreamInterface interface {
	AddServer(name string, cfg *config.ServerConfig) error
	RemoveServer(name string) error
	ConnectServer(ctx context.Context, name string) error
	DisconnectServer(name string) error
	ConnectAll(ctx context.Context) error
	GetServerState(name string) (*ServerState, error)
	GetAllStates() map[string]*ServerState
	Subscribe() <-chan Event
	Unsubscribe(ch <-chan Event)
	Close()
}

// New creates a new supervisor.
func New(configSvc *configsvc.Service, upstream UpstreamInterface, logger *zap.Logger) *Supervisor {
	if logger == nil {
		logger = zap.NewNop()
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &Supervisor{
		logger:    logger,
		configSvc: configSvc,
		upstream:  upstream,
		version:   0,
		stateView: stateview.New(),
		eventCh:   make(chan Event, 500), // Phase 6: Increased buffer for async operations
		listeners: make([]chan Event, 0),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Initialize empty snapshot
	s.snapshot.Store(&ServerStateSnapshot{
		Servers:   make(map[string]*ServerState),
		Timestamp: time.Now(),
		Version:   0,
	})

	return s
}

// Start begins the supervisor's reconciliation loop.
func (s *Supervisor) Start() {
	s.logger.Info("Starting supervisor")

	// Subscribe to config changes
	configUpdates := s.configSvc.Subscribe(s.ctx)

	// Subscribe to upstream events
	upstreamEvents := s.upstream.Subscribe()

	// Start event forwarding goroutine
	s.wg.Add(1)
	go s.forwardUpstreamEvents(upstreamEvents)

	// Start reconciliation loop
	s.wg.Add(1)
	go s.reconciliationLoop(configUpdates)

	s.logger.Info("Supervisor started")
}

// reconciliationLoop processes config updates and reconciles state.
func (s *Supervisor) reconciliationLoop(configUpdates <-chan configsvc.Update) {
	defer s.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			s.logger.Info("Supervisor reconciliation loop stopping")
			return

		case update, ok := <-configUpdates:
			if !ok {
				s.logger.Warn("Config updates channel closed")
				return
			}

			s.logger.Info("Config update received, reconciling",
				zap.String("type", string(update.Type)),
				zap.Int64("version", update.Snapshot.Version))

			if err := s.reconcile(update.Snapshot); err != nil {
				s.logger.Error("Reconciliation failed", zap.Error(err))
				s.emitEvent(Event{
					Type:      EventReconciliationFailed,
					Timestamp: time.Now(),
					Payload: map[string]interface{}{
						"error":   err.Error(),
						"version": update.Snapshot.Version,
					},
				})
			} else {
				s.emitEvent(Event{
					Type:      EventReconciliationComplete,
					Timestamp: time.Now(),
					Payload: map[string]interface{}{
						"version": update.Snapshot.Version,
					},
				})
			}

		case <-ticker.C:
			// Periodic reconciliation to handle drift
			s.logger.Debug("Periodic reconciliation check")
			currentConfig := s.configSvc.Current()
			if err := s.reconcile(currentConfig); err != nil {
				s.logger.Error("Periodic reconciliation failed", zap.Error(err))
			}
		}
	}
}

// reconcile compares desired vs actual state and takes corrective actions.
// Phase 6 Fix: Made fully async to prevent blocking HTTP server startup.
func (s *Supervisor) reconcile(configSnapshot *configsvc.Snapshot) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	s.logger.Debug("Starting reconciliation",
		zap.Int("desired_servers", configSnapshot.ServerCount()))

	plan := s.computeReconcilePlan(configSnapshot)

	// Phase 6 Fix: Execute actions asynchronously to prevent blocking
	// Each action runs in its own goroutine with timeout
	actionCount := 0
	for serverName, action := range plan.Actions {
		if action == ActionNone {
			continue // Skip no-op actions
		}

		actionCount++
		// Launch each action in a goroutine - no waiting!
		go func(name string, act ReconcileAction, snapshot *configsvc.Snapshot) {
			if err := s.executeAction(name, act, snapshot); err != nil {
				s.logger.Error("Failed to execute action",
					zap.String("server", name),
					zap.String("action", string(act)),
					zap.Error(err))
			} else {
				s.logger.Debug("Action completed successfully",
					zap.String("server", name),
					zap.String("action", string(act)))
			}
		}(serverName, action, configSnapshot)
	}

	// Update state snapshot immediately (actions run in background)
	s.updateSnapshot(configSnapshot)

	s.logger.Debug("Reconciliation dispatched",
		zap.Int("actions_dispatched", actionCount),
		zap.String("note", "actions running asynchronously"))

	return nil
}

// computeReconcilePlan determines what actions need to be taken.
func (s *Supervisor) computeReconcilePlan(configSnapshot *configsvc.Snapshot) *ReconcilePlan {
	plan := &ReconcilePlan{
		Actions:   make(map[string]ReconcileAction),
		Timestamp: time.Now(),
		Reason:    "config_update",
	}

	currentSnapshot := s.CurrentSnapshot()
	desiredServers := configSnapshot.Config.Servers

	// Check for servers that need to be added or updated
	for _, desiredServer := range desiredServers {
		if desiredServer == nil {
			continue
		}

		name := desiredServer.Name
		currentState, exists := currentSnapshot.Servers[name]

		if !exists {
			// New server needs to be added
			if desiredServer.Enabled && !desiredServer.Quarantined {
				plan.Actions[name] = ActionConnect
			} else {
				plan.Actions[name] = ActionNone
			}
		} else {
			// Existing server - check if config changed
			if s.configChanged(currentState.Config, desiredServer) {
				plan.Actions[name] = ActionReconnect
			} else if desiredServer.Enabled && !desiredServer.Quarantined && !currentState.Connected {
				// Should be connected but isn't
				plan.Actions[name] = ActionConnect
			} else if (!desiredServer.Enabled || desiredServer.Quarantined) && currentState.Connected {
				// Shouldn't be connected but is
				plan.Actions[name] = ActionDisconnect
			} else {
				plan.Actions[name] = ActionNone
			}
		}
	}

	// Check for servers that need to be removed
	desiredNames := make(map[string]bool)
	for _, srv := range desiredServers {
		if srv != nil {
			desiredNames[srv.Name] = true
		}
	}

	for name := range currentSnapshot.Servers {
		if !desiredNames[name] {
			plan.Actions[name] = ActionRemove
		}
	}

	return plan
}

// configChanged checks if server configuration has changed.
func (s *Supervisor) configChanged(old, new *config.ServerConfig) bool {
	if old == nil || new == nil {
		return old != new
	}

	return old.URL != new.URL ||
		old.Protocol != new.Protocol ||
		old.Command != new.Command ||
		old.Enabled != new.Enabled ||
		old.Quarantined != new.Quarantined
}

// executeAction performs the specified action on a server.
func (s *Supervisor) executeAction(serverName string, action ReconcileAction, configSnapshot *configsvc.Snapshot) error {
	s.logger.Debug("Executing action",
		zap.String("server", serverName),
		zap.String("action", string(action)))

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	switch action {
	case ActionNone:
		// No action needed
		return nil

	case ActionConnect:
		// Add server and connect
		serverConfig := configSnapshot.GetServer(serverName)
		if serverConfig == nil {
			return fmt.Errorf("server config not found: %s", serverName)
		}

		if err := s.upstream.AddServer(serverName, serverConfig); err != nil {
			return fmt.Errorf("failed to add server: %w", err)
		}

		if serverConfig.Enabled && !serverConfig.Quarantined {
			if err := s.upstream.ConnectServer(ctx, serverName); err != nil {
				s.logger.Warn("Failed to connect server (will retry)",
					zap.String("server", serverName),
					zap.Error(err))
				// Don't return error - managed client will retry
			}
		}

		return nil

	case ActionDisconnect:
		return s.upstream.DisconnectServer(serverName)

	case ActionReconnect:
		// Disconnect then reconnect
		if err := s.upstream.DisconnectServer(serverName); err != nil {
			s.logger.Warn("Failed to disconnect server during reconnect",
				zap.String("server", serverName),
				zap.Error(err))
		}

		// Get updated config
		serverConfig := configSnapshot.GetServer(serverName)
		if serverConfig == nil {
			return fmt.Errorf("server config not found: %s", serverName)
		}

		// Add with new config
		if err := s.upstream.AddServer(serverName, serverConfig); err != nil {
			return fmt.Errorf("failed to add server: %w", err)
		}

		// Connect if enabled
		if serverConfig.Enabled && !serverConfig.Quarantined {
			if err := s.upstream.ConnectServer(ctx, serverName); err != nil {
				s.logger.Warn("Failed to reconnect server (will retry)",
					zap.String("server", serverName),
					zap.Error(err))
			}
		}

		return nil

	case ActionRemove:
		return s.upstream.RemoveServer(serverName)

	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

// updateSnapshot updates the current state snapshot.
func (s *Supervisor) updateSnapshot(configSnapshot *configsvc.Snapshot) {
	s.version++

	// Get actual state from upstream
	actualStates := s.upstream.GetAllStates()

	// Merge desired and actual state
	newSnapshot := &ServerStateSnapshot{
		Servers:   make(map[string]*ServerState),
		Timestamp: time.Now(),
		Version:   s.version,
	}

	// Add all configured servers
	for _, srv := range configSnapshot.Config.Servers {
		if srv == nil {
			continue
		}

		state := &ServerState{
			Name:           srv.Name,
			Config:         srv,
			Enabled:        srv.Enabled,
			Quarantined:    srv.Quarantined,
			DesiredVersion: configSnapshot.Version,
			LastReconcile:  time.Now(),
		}

		// Merge with actual state if available
		if actualState, ok := actualStates[srv.Name]; ok {
			state.Connected = actualState.Connected
			state.ConnectionInfo = actualState.ConnectionInfo
			state.LastSeen = actualState.LastSeen
			state.ToolCount = actualState.ToolCount
		}

		newSnapshot.Servers[srv.Name] = state

		// Update stateview (Phase 4)
		s.updateStateView(srv.Name, state)
	}

	s.snapshot.Store(newSnapshot)

	// Remove servers from stateview that are no longer in config
	currentView := s.stateView.Snapshot()
	for name := range currentView.Servers {
		if _, exists := newSnapshot.Servers[name]; !exists {
			s.stateView.RemoveServer(name)
		}
	}
}

// updateStateView updates the stateview with current server state.
func (s *Supervisor) updateStateView(name string, state *ServerState) {
	s.stateView.UpdateServer(name, func(status *stateview.ServerStatus) {
		status.Config = state.Config
		status.Enabled = state.Enabled
		status.Quarantined = state.Quarantined
		status.Connected = state.Connected
		status.ToolCount = state.ToolCount

		// Map connection state to string
		if state.Connected {
			status.State = "connected"
			if !state.LastSeen.IsZero() {
				t := state.LastSeen
				status.ConnectedAt = &t
			}
		} else if state.Enabled && !state.Quarantined {
			status.State = "connecting"
		} else {
			status.State = "idle"
		}

		// Update connection info if available
		if state.ConnectionInfo != nil {
			if status.Metadata == nil {
				status.Metadata = make(map[string]interface{})
			}
			status.Metadata["connection_info"] = state.ConnectionInfo
		}
	})
}

// forwardUpstreamEvents forwards upstream events to supervisor listeners.
func (s *Supervisor) forwardUpstreamEvents(upstreamEvents <-chan Event) {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return

		case event, ok := <-upstreamEvents:
			if !ok {
				return
			}

			// Forward to supervisor listeners
			s.emitEvent(event)

			// Update snapshot on state changes
			if event.Type == EventServerStateChanged || event.Type == EventServerConnected || event.Type == EventServerDisconnected {
				s.updateSnapshotFromEvent(event)
			}
		}
	}
}

// updateSnapshotFromEvent updates the snapshot based on an upstream event.
func (s *Supervisor) updateSnapshotFromEvent(event Event) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	current := s.CurrentSnapshot()
	if state, ok := current.Servers[event.ServerName]; ok {
		// Update connection status
		if connected, ok := event.Payload["connected"].(bool); ok {
			state.Connected = connected
			state.LastSeen = event.Timestamp

			// Update stateview
			s.stateView.UpdateServer(event.ServerName, func(status *stateview.ServerStatus) {
				status.Connected = connected
				if connected {
					status.State = "connected"
					t := event.Timestamp
					status.ConnectedAt = &t
				} else {
					status.State = "disconnected"
					t := event.Timestamp
					status.DisconnectedAt = &t
				}
			})
		}
	}
}

// CurrentSnapshot returns the current state snapshot (lock-free read).
func (s *Supervisor) CurrentSnapshot() *ServerStateSnapshot {
	return s.snapshot.Load().(*ServerStateSnapshot)
}

// StateView returns the read-only state view (Phase 4).
// This provides a lock-free view of server statuses for API consumers.
func (s *Supervisor) StateView() *stateview.View {
	return s.stateView
}

// Subscribe returns a channel that receives supervisor events.
func (s *Supervisor) Subscribe() <-chan Event {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()

	ch := make(chan Event, 200) // Phase 6: Increased buffer for async reconciliation
	s.listeners = append(s.listeners, ch)
	return ch
}

// Unsubscribe removes a subscriber.
func (s *Supervisor) Unsubscribe(ch <-chan Event) {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()

	for i, listener := range s.listeners {
		if listener == ch {
			s.listeners = append(s.listeners[:i], s.listeners[i+1:]...)
			close(listener)
			break
		}
	}
}

// emitEvent sends an event to all subscribers.
func (s *Supervisor) emitEvent(event Event) {
	s.eventMu.RLock()
	defer s.eventMu.RUnlock()

	for _, ch := range s.listeners {
		select {
		case ch <- event:
		default:
			s.logger.Warn("Supervisor event channel full, dropping event",
				zap.String("event_type", string(event.Type)))
		}
	}
}

// Stop gracefully stops the supervisor.
func (s *Supervisor) Stop() {
	s.logger.Info("Stopping supervisor")
	s.cancel()
	s.wg.Wait()

	// Close upstream adapter
	s.upstream.Close()

	// Close event channels
	s.eventMu.Lock()
	for _, ch := range s.listeners {
		close(ch)
	}
	s.listeners = nil
	s.eventMu.Unlock()

	s.logger.Info("Supervisor stopped")
}
