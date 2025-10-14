package supervisor

import (
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/upstream/types"
)

// ServerState represents the desired and actual state of an upstream server.
type ServerState struct {
	// Desired state from configuration
	Name        string
	Config      *config.ServerConfig
	Enabled     bool
	Quarantined bool

	// Actual state from upstream manager
	Connected      bool
	ConnectionInfo *types.ConnectionInfo
	LastSeen       time.Time
	ToolCount      int
	Tools          []*config.ToolMetadata // Phase 7.1: Cached tools for lock-free reads

	// Reconciliation metadata
	DesiredVersion int64 // Config version that defines this desired state
	LastReconcile  time.Time
	ReconcileCount int
}

// ServerStateSnapshot is an immutable view of all server states.
type ServerStateSnapshot struct {
	Servers   map[string]*ServerState
	Timestamp time.Time
	Version   int64 // Monotonically increasing
}

// Clone creates a deep copy of the snapshot.
func (s *ServerStateSnapshot) Clone() *ServerStateSnapshot {
	if s == nil {
		return nil
	}

	cloned := &ServerStateSnapshot{
		Servers:   make(map[string]*ServerState, len(s.Servers)),
		Timestamp: s.Timestamp,
		Version:   s.Version,
	}

	for name, state := range s.Servers {
		if state != nil {
			stateCopy := *state
			// Clone nested config
			if state.Config != nil {
				configCopy := *state.Config
				stateCopy.Config = &configCopy
			}
			// Clone connection info
			if state.ConnectionInfo != nil {
				infoCopy := *state.ConnectionInfo
				stateCopy.ConnectionInfo = &infoCopy
			}
			// Phase 7.1: Clone tools slice
			if state.Tools != nil {
				stateCopy.Tools = make([]*config.ToolMetadata, len(state.Tools))
				copy(stateCopy.Tools, state.Tools)
			}
			cloned.Servers[name] = &stateCopy
		}
	}

	return cloned
}

// EventType represents supervisor lifecycle events.
type EventType string

const (
	// EventServerAdded is emitted when a new server is added to desired state
	EventServerAdded EventType = "server.added"

	// EventServerRemoved is emitted when a server is removed from desired state
	EventServerRemoved EventType = "server.removed"

	// EventServerUpdated is emitted when a server's desired config changes
	EventServerUpdated EventType = "server.updated"

	// EventServerConnected is emitted when a server successfully connects
	EventServerConnected EventType = "server.connected"

	// EventServerDisconnected is emitted when a server disconnects
	EventServerDisconnected EventType = "server.disconnected"

	// EventServerStateChanged is emitted when a server's connection state changes
	EventServerStateChanged EventType = "server.state_changed"

	// EventReconciliationComplete is emitted after a reconciliation cycle completes
	EventReconciliationComplete EventType = "reconciliation.complete"

	// EventReconciliationFailed is emitted when reconciliation fails
	EventReconciliationFailed EventType = "reconciliation.failed"
)

// Event represents a supervisor lifecycle event.
type Event struct {
	Type       EventType
	ServerName string
	Timestamp  time.Time
	Payload    map[string]interface{}
}

// ReconcileAction describes what action the supervisor should take.
type ReconcileAction string

const (
	// ActionNone means no action is needed
	ActionNone ReconcileAction = "none"

	// ActionConnect means the server should be connected
	ActionConnect ReconcileAction = "connect"

	// ActionDisconnect means the server should be disconnected
	ActionDisconnect ReconcileAction = "disconnect"

	// ActionReconnect means the server should be disconnected and reconnected
	ActionReconnect ReconcileAction = "reconnect"

	// ActionRemove means the server should be removed
	ActionRemove ReconcileAction = "remove"
)

// ReconcilePlan describes the actions to take during reconciliation.
type ReconcilePlan struct {
	Actions   map[string]ReconcileAction // server name -> action
	Timestamp time.Time
	Reason    string
}
