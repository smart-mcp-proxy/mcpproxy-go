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
	"mcpproxy-go/internal/upstream/types"
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

	// Callback for reactive tool discovery on server connection
	onServerConnectedCallback func(serverName string)
	callbackMu                sync.RWMutex

	// Inspection exemptions for temporary connections to quarantined servers
	inspectionExemptions   map[string]time.Time
	inspectionExemptionsMu sync.RWMutex

	// Circuit breaker for inspection failures (Phase 2: Issue #105 stability)
	inspectionFailures   map[string]*inspectionFailureInfo
	inspectionFailuresMu sync.RWMutex

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// inspectionFailureInfo tracks inspection failures for circuit breaker pattern
type inspectionFailureInfo struct {
	consecutiveFailures int
	lastFailureTime     time.Time
	cooldownUntil       time.Time
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
		logger:               logger,
		configSvc:            configSvc,
		upstream:             upstream,
		version:              0,
		stateView:            stateview.New(),
		eventCh:              make(chan Event, 500), // Phase 6: Increased buffer for async operations
		listeners:            make([]chan Event, 0),
		inspectionExemptions: make(map[string]time.Time),
		inspectionFailures:   make(map[string]*inspectionFailureInfo),
		ctx:                  ctx,
		cancel:               cancel,
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

	// Start exemption cleanup loop
	s.wg.Add(1)
	go s.exemptionCleanupLoop()

	// Phase 7.1: Trigger initial reconciliation to populate StateView
	go func() {
		time.Sleep(500 * time.Millisecond) // Give servers time to connect
		currentConfig := s.configSvc.Current()
		if err := s.reconcile(currentConfig); err != nil {
			s.logger.Error("Initial reconciliation failed", zap.Error(err))
		} else {
			s.logger.Info("Initial reconciliation completed, StateView populated")
		}
	}()

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

// exemptionCleanupLoop periodically checks for expired inspection exemptions.
func (s *Supervisor) exemptionCleanupLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			s.logger.Info("Exemption cleanup loop stopping")
			return

		case <-ticker.C:
			// Check for expired exemptions
			s.inspectionExemptionsMu.Lock()
			expiredServers := make([]string, 0)
			for serverName, expiryTime := range s.inspectionExemptions {
				if time.Now().After(expiryTime) {
					expiredServers = append(expiredServers, serverName)
					delete(s.inspectionExemptions, serverName)
				}
			}
			s.inspectionExemptionsMu.Unlock()

			// Trigger reconciliation for each expired exemption to disconnect servers
			for _, serverName := range expiredServers {
				s.logger.Warn("⚠️ Inspection exemption expired, triggering disconnect",
					zap.String("server", serverName))

				currentConfig := s.configSvc.Current()
				if err := s.reconcile(currentConfig); err != nil {
					s.logger.Error("Failed to trigger reconciliation after exemption expiry",
						zap.String("server", serverName),
						zap.Error(err))
				}
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
			if desiredServer.Enabled && (!desiredServer.Quarantined || s.IsInspectionExempted(name)) {
				plan.Actions[name] = ActionConnect
			} else {
				plan.Actions[name] = ActionNone
			}
		} else {
			// Existing server - check if config changed
			if s.configChanged(currentState.Config, desiredServer) {
				plan.Actions[name] = ActionReconnect
			} else if desiredServer.Enabled && (!desiredServer.Quarantined || s.IsInspectionExempted(name)) && !currentState.Connected {
				// Should be connected but isn't (or has inspection exemption)
				plan.Actions[name] = ActionConnect
			} else if (!desiredServer.Enabled || (desiredServer.Quarantined && !s.IsInspectionExempted(name))) && currentState.Connected {
				// Shouldn't be connected but is (or exemption expired)
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

		// Connect if enabled and (not quarantined OR has inspection exemption)
		if serverConfig.Enabled && (!serverConfig.Quarantined || s.IsInspectionExempted(serverName)) {
			// Log security event when connecting quarantined server via exemption
			if serverConfig.Quarantined && s.IsInspectionExempted(serverName) {
				s.logger.Warn("⚠️ Connecting quarantined server for inspection",
					zap.String("server", serverName))
			}

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

		// Connect if enabled and (not quarantined OR has inspection exemption)
		if serverConfig.Enabled && (!serverConfig.Quarantined || s.IsInspectionExempted(serverName)) {
			// Log security event when connecting quarantined server via exemption
			if serverConfig.Quarantined && s.IsInspectionExempted(serverName) {
				s.logger.Warn("⚠️ Reconnecting quarantined server for inspection",
					zap.String("server", serverName))
			}

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
// Phase 7.1 FIX: Removed GetAllStates() call to prevent blocking on slow servers.
// State updates now happen via events only, keeping this method fast and non-blocking.
func (s *Supervisor) updateSnapshot(configSnapshot *configsvc.Snapshot) {
	s.version++

	// Phase 7.1 FIX: Don't call GetAllStates() here! It blocks on ListTools() for all servers.
	// Instead, rely on existing state and event-driven updates.
	// Get actual state from existing snapshot (non-blocking)
	currentSnapshot := s.CurrentSnapshot()
	actualStates := make(map[string]*ServerState)
	if currentSnapshot != nil {
		for name, state := range currentSnapshot.Servers {
			actualStates[name] = state
		}
	}

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
			state.Tools = actualState.Tools // Phase 7.1: Copy tools for caching
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

		// Phase 7.1: Convert ToolMetadata to ToolInfo and cache in StateView
		if state.Tools != nil {
			status.Tools = make([]stateview.ToolInfo, len(state.Tools))
			for i, tool := range state.Tools {
				// Parse ParamsJSON into InputSchema
				var inputSchema map[string]interface{}
				if tool.ParamsJSON != "" {
					// ParamsJSON is already a JSON string, we'll store it as-is
					// The API endpoint will parse it if needed
					inputSchema = map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{}, // TODO: Parse ParamsJSON
					}
				}

				status.Tools[i] = stateview.ToolInfo{
					Name:        tool.Name,
					Description: tool.Description,
					InputSchema: inputSchema,
				}
			}
		} else {
			status.Tools = nil
		}

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
			// Extract LastError from ConnectionInfo and convert to string with limit
			if state.ConnectionInfo.LastError != nil {
				errorStr := state.ConnectionInfo.LastError.Error()
				// Limit error string to 500 characters to prevent UI hangs
				const maxErrorLen = 500
				if len(errorStr) > maxErrorLen {
					errorStr = errorStr[:maxErrorLen] + "... (truncated)"
				}
				status.LastError = errorStr

				// Set last error time if available
				if !state.ConnectionInfo.LastRetryTime.IsZero() {
					t := state.ConnectionInfo.LastRetryTime
					status.LastErrorTime = &t
				}
			} else {
				status.LastError = ""
				status.LastErrorTime = nil
			}

			// Copy retry count
			status.RetryCount = state.ConnectionInfo.RetryCount

			// Store full connection info in metadata for debugging
			if status.Metadata == nil {
				status.Metadata = make(map[string]interface{})
			}
			status.Metadata["connection_info"] = state.ConnectionInfo
		}
	})
}

// SetOnServerConnectedCallback sets a callback to be invoked when a server connects.
// This allows for reactive tool discovery instead of relying on periodic polling.
func (s *Supervisor) SetOnServerConnectedCallback(callback func(serverName string)) {
	s.callbackMu.Lock()
	defer s.callbackMu.Unlock()
	s.onServerConnectedCallback = callback
}

// RefreshToolsFromDiscovery updates both the Supervisor snapshot and StateView with tools from background discovery.
// This is called after DiscoverAndIndexTools completes to populate the UI cache.
func (s *Supervisor) RefreshToolsFromDiscovery(tools []*config.ToolMetadata) error {
	if tools == nil {
		return nil
	}

	// Group tools by server name
	toolsByServer := make(map[string][]*config.ToolMetadata)
	for _, tool := range tools {
		toolsByServer[tool.ServerName] = append(toolsByServer[tool.ServerName], tool)
	}

	// Update Supervisor's snapshot first (source of truth for StateView)
	s.stateMu.Lock()
	currentSnapshot := s.snapshot.Load().(*ServerStateSnapshot)

	// Clone the snapshot
	newServers := make(map[string]*ServerState)
	for name, state := range currentSnapshot.Servers {
		// Shallow copy of ServerState
		newState := *state
		newServers[name] = &newState
	}

	// Update tool counts and tools for servers with discovered tools
	for serverName, serverTools := range toolsByServer {
		if state, exists := newServers[serverName]; exists {
			state.ToolCount = len(serverTools)
			state.Tools = serverTools
		}
	}

	newSnapshot := &ServerStateSnapshot{
		Servers:   newServers,
		Timestamp: time.Now(),
		Version:   currentSnapshot.Version + 1,
	}

	s.snapshot.Store(newSnapshot)
	s.version++
	s.stateMu.Unlock()

	// Update StateView for each server
	for serverName, serverTools := range toolsByServer {
		s.stateView.UpdateServer(serverName, func(status *stateview.ServerStatus) {
			// Defensive check: Only update if we have more or equal tools than currently shown
			// This prevents overwriting valid tools with stale data from delayed discoveries
			// Exception: Always update if current tools is 0 (initial population)
			if len(status.Tools) > 0 && len(serverTools) < len(status.Tools) {
				s.logger.Debug("StateView already has more tools, skipping update to prevent stale data",
					zap.String("server", serverName),
					zap.Int("current_tools", len(status.Tools)),
					zap.Int("new_tools", len(serverTools)))
				return
			}

			status.ToolCount = len(serverTools)
			status.Tools = make([]stateview.ToolInfo, len(serverTools))

			for i, tool := range serverTools {
				// Parse ParamsJSON into InputSchema
				var inputSchema map[string]interface{}
				if tool.ParamsJSON != "" {
					// ParamsJSON is already a JSON string
					inputSchema = map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{}, // TODO: Parse ParamsJSON
					}
				}

				status.Tools[i] = stateview.ToolInfo{
					Name:        tool.Name,
					Description: tool.Description,
					InputSchema: inputSchema,
				}
			}
		})
	}

	s.logger.Debug("Refreshed tools in Supervisor snapshot and StateView from discovery",
		zap.Int("server_count", len(toolsByServer)),
		zap.Int("total_tools", len(tools)))

	return nil
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

			// Phase 7.1 FIX: Don't fetch tools on connect events - let background indexing handle it
			// Just update the cached tool count and connection info from the client (non-blocking)
			var toolCount int
			var connInfo *types.ConnectionInfo
			if actualState, err := s.upstream.GetServerState(event.ServerName); err == nil {
				toolCount = actualState.ToolCount
				// Update ConnectionInfo for error propagation to UI
				state.ConnectionInfo = actualState.ConnectionInfo
				connInfo = actualState.ConnectionInfo
			} else {
				s.logger.Warn("Failed to get server state for tool count",
					zap.String("server", event.ServerName),
					zap.Error(err))
			}

			// Update stateview
			s.stateView.UpdateServer(event.ServerName, func(status *stateview.ServerStatus) {
				status.Connected = connected
				if connected {
					status.State = "connected"
					t := event.Timestamp
					status.ConnectedAt = &t
					// Don't populate Tools here - background indexing will handle it
					// Only update the count if tools haven't been discovered yet
					// This prevents overwriting tool data from background discovery
					if len(status.Tools) == 0 {
						status.ToolCount = toolCount
					}
					// If tools are already populated, keep the existing count
				} else {
					status.State = "disconnected"
					t := event.Timestamp
					status.DisconnectedAt = &t
					status.Tools = nil // Clear tools on disconnect
					status.ToolCount = 0
				}

				// Update ConnectionInfo for immediate error propagation to UI
				if connInfo != nil {
					if connInfo.LastError != nil {
						errorStr := connInfo.LastError.Error()
						const maxErrorLen = 500
						if len(errorStr) > maxErrorLen {
							errorStr = errorStr[:maxErrorLen] + "... (truncated)"
						}
						status.LastError = errorStr

						if !connInfo.LastRetryTime.IsZero() {
							t := connInfo.LastRetryTime
							status.LastErrorTime = &t
						}
					} else {
						status.LastError = ""
						status.LastErrorTime = nil
					}
					status.RetryCount = connInfo.RetryCount
				}
			})

			// Trigger reactive tool discovery when server connects
			if connected {
				s.callbackMu.RLock()
				callback := s.onServerConnectedCallback
				s.callbackMu.RUnlock()

				if callback != nil {
					// Run callback asynchronously to avoid blocking supervisor
					go callback(event.ServerName)
				}
			}
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

// RequestInspectionExemption grants temporary connection permission for a quarantined server.
// This allows security inspection to temporarily connect to quarantined servers.
// Triggers immediate reconciliation to connect the server.
func (s *Supervisor) RequestInspectionExemption(serverName string, duration time.Duration) error {
	s.inspectionExemptionsMu.Lock()
	expiryTime := time.Now().Add(duration)
	s.inspectionExemptions[serverName] = expiryTime
	s.inspectionExemptionsMu.Unlock()

	s.logger.Warn("⚠️ Temporary connection exemption granted for quarantined server inspection",
		zap.String("server", serverName),
		zap.Duration("duration", duration),
		zap.Time("expires_at", expiryTime))

	// Trigger immediate reconciliation to connect the server
	currentConfig := s.configSvc.Current()
	if err := s.reconcile(currentConfig); err != nil {
		s.logger.Error("Failed to trigger reconciliation after exemption grant",
			zap.String("server", serverName),
			zap.Error(err))
		return fmt.Errorf("failed to trigger reconciliation: %w", err)
	}

	return nil
}

// RevokeInspectionExemption revokes the temporary connection permission and triggers disconnection.
func (s *Supervisor) RevokeInspectionExemption(serverName string) {
	s.inspectionExemptionsMu.Lock()
	_, exists := s.inspectionExemptions[serverName]
	if exists {
		delete(s.inspectionExemptions, serverName)
	}
	s.inspectionExemptionsMu.Unlock()

	if exists {
		s.logger.Warn("⚠️ Inspection exemption revoked for quarantined server",
			zap.String("server", serverName))

		// Trigger immediate reconciliation to disconnect the server
		currentConfig := s.configSvc.Current()
		if err := s.reconcile(currentConfig); err != nil {
			s.logger.Error("Failed to trigger reconciliation after exemption revocation",
				zap.String("server", serverName),
				zap.Error(err))
		}
	}
}

// IsInspectionExempted checks if a server has an active inspection exemption.
// Automatically cleans up expired exemptions.
func (s *Supervisor) IsInspectionExempted(serverName string) bool {
	s.inspectionExemptionsMu.Lock()
	defer s.inspectionExemptionsMu.Unlock()

	expiryTime, exists := s.inspectionExemptions[serverName]
	if !exists {
		return false
	}

	// Check if exemption has expired
	if time.Now().After(expiryTime) {
		delete(s.inspectionExemptions, serverName)
		s.logger.Warn("⚠️ Inspection exemption expired, forcing disconnect",
			zap.String("server", serverName))
		return false
	}

	return true
}

// ===== Circuit Breaker for Inspection Failures (Issue #105) =====

const (
	maxInspectionFailures  = 3                // Max consecutive failures before cooldown
	inspectionCooldown     = 5 * time.Minute  // Cooldown duration after max failures
	failureResetTimeout    = 10 * time.Minute // Reset counter if no failures for this long
)

// CanInspect checks if inspection is allowed for a server (circuit breaker)
// Returns (allowed bool, reason string, cooldownRemaining time.Duration)
func (s *Supervisor) CanInspect(serverName string) (bool, string, time.Duration) {
	s.inspectionFailuresMu.RLock()
	defer s.inspectionFailuresMu.RUnlock()

	info, exists := s.inspectionFailures[serverName]
	if !exists {
		// No failure history - allow inspection
		return true, "", 0
	}

	now := time.Now()

	// Check if cooldown is active
	if now.Before(info.cooldownUntil) {
		remaining := info.cooldownUntil.Sub(now)
		reason := fmt.Sprintf("Server '%s' has failed inspection %d times. Circuit breaker active - please wait %v before retrying. This prevents cascading failures with unstable servers (see issue #105).",
			serverName, info.consecutiveFailures, remaining.Round(time.Second))
		return false, reason, remaining
	}

	// Check if failures should be reset (no failures for failureResetTimeout)
	if now.Sub(info.lastFailureTime) > failureResetTimeout {
		// Failures are old - will be reset on next inspection
		return true, "", 0
	}

	// Within failure window but not in cooldown
	return true, "", 0
}

// RecordInspectionFailure records an inspection failure for circuit breaker
func (s *Supervisor) RecordInspectionFailure(serverName string) {
	s.inspectionFailuresMu.Lock()
	defer s.inspectionFailuresMu.Unlock()

	now := time.Now()

	info, exists := s.inspectionFailures[serverName]
	if !exists {
		info = &inspectionFailureInfo{}
		s.inspectionFailures[serverName] = info
	}

	// Reset counter if last failure was too long ago
	if now.Sub(info.lastFailureTime) > failureResetTimeout {
		info.consecutiveFailures = 0
	}

	info.consecutiveFailures++
	info.lastFailureTime = now

	s.logger.Warn("Inspection failure recorded",
		zap.String("server", serverName),
		zap.Int("consecutive_failures", info.consecutiveFailures),
		zap.Int("max_before_cooldown", maxInspectionFailures))

	// Activate cooldown if max failures reached
	if info.consecutiveFailures >= maxInspectionFailures {
		info.cooldownUntil = now.Add(inspectionCooldown)
		s.logger.Error("⚠️ Inspection circuit breaker activated - too many failures",
			zap.String("server", serverName),
			zap.Int("failures", info.consecutiveFailures),
			zap.Duration("cooldown", inspectionCooldown),
			zap.Time("cooldown_until", info.cooldownUntil),
			zap.String("issue", "#105 - preventing cascading failures"))
	}
}

// RecordInspectionSuccess records a successful inspection, resetting failure counter
func (s *Supervisor) RecordInspectionSuccess(serverName string) {
	s.inspectionFailuresMu.Lock()
	defer s.inspectionFailuresMu.Unlock()

	info, exists := s.inspectionFailures[serverName]
	if !exists {
		return
	}

	if info.consecutiveFailures > 0 {
		s.logger.Info("Inspection succeeded - resetting failure counter",
			zap.String("server", serverName),
			zap.Int("previous_failures", info.consecutiveFailures))
	}

	// Reset failure counter
	delete(s.inspectionFailures, serverName)
}

// GetInspectionStats returns inspection failure statistics for a server
func (s *Supervisor) GetInspectionStats(serverName string) (failures int, inCooldown bool, cooldownRemaining time.Duration) {
	s.inspectionFailuresMu.RLock()
	defer s.inspectionFailuresMu.RUnlock()

	info, exists := s.inspectionFailures[serverName]
	if !exists {
		return 0, false, 0
	}

	now := time.Now()
	if now.Before(info.cooldownUntil) {
		return info.consecutiveFailures, true, info.cooldownUntil.Sub(now)
	}

	return info.consecutiveFailures, false, 0
}
