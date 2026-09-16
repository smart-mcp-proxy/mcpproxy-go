package supervisor

import (
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
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
	// ToolsDiscovered mirrors stateview.ServerStatus.ToolsDiscovered on the
	// retained snapshot: set when discovery publishes a tool set for the
	// server (even an empty one). It is PER CONNECTION: cleared on every
	// connection-state edge (disconnect AND connect) and whenever reconcile
	// observes the server disconnected, so a stale stamp can never certify
	// the previous connection's retained Tools for a new connection (Spec 105
	// FR-009, research D4; astra r1 I1/I4).
	ToolsDiscovered bool
	// ConnectionGeneration counts the server's connection-state edges
	// (bumped on every connect / disconnect event and on a disconnect
	// reconcile observes). A discovery result is published only if the
	// generation it was captured under is still current, so a result whose
	// capture straddled a reconnect is dropped instead of landing on the new
	// connection (Spec 105 FR-009 "stale generation"; astra r1 I2).
	ConnectionGeneration uint64
	// ConnectionEpoch is the live client's connection-instance token
	// (managed.Client.ConnectionEpoch) as the adapter last reported it: set
	// on the states the adapter builds (ActorPoolSimple) and recorded on the
	// retained snapshot by every event and reconcile pass. A reconcile that
	// finds the live token moved since the last observation has missed a
	// connection edge (dropped events) and treats it as one: marker cleared,
	// generation bumped, rediscovery kicked (astra r2 C3). Zero when the
	// adapter reports no token (unit fixtures).
	ConnectionEpoch int64
	// DiscoveryEpoch is the ConnectionEpoch the current discovery stamp was
	// captured under (DiscoveryCapture.Epoch), published together with
	// ToolsDiscovered and mirrored to stateview.ServerStatus.DiscoveryEpoch,
	// which identity resolution compares with the live client's token. Zero
	// while no discovery has been published for this connection.
	DiscoveryEpoch int64

	// Reconciliation metadata
	DesiredVersion int64 // Config version that defines this desired state
	LastReconcile  time.Time
	ReconcileCount int
}

// DiscoveryCapture binds a discovery result to the connection it was listed
// under (Spec 105 FR-009 "stale generation"). A discovery caller takes it
// BEFORE listing a server's tools and hands it back to the publish call:
//
//   - Generation is the Supervisor's own edge counter
//     (ServerState.ConnectionGeneration); the publish is dropped when it has
//     moved on (astra r1 I2).
//   - Epoch is the live client's connection-instance token
//     (managed.Client.ConnectionEpoch) at capture time; it is stamped on the
//     snapshot with the result so every identity read can tell whether the
//     stamp still describes the live connection (astra r2 C3).
type DiscoveryCapture struct {
	Generation uint64
	Epoch      int64
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
