package stateview

import (
	"sync"
	"sync/atomic"
	"time"

	"mcpproxy-go/internal/config"
)

// ToolInfo represents a cached tool definition.
type ToolInfo struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
	Annotations *config.ToolAnnotations
}

// ServerStatus represents the runtime status of an upstream server.
type ServerStatus struct {
	Name           string
	Config         *config.ServerConfig
	State          string // Actor state: idle, connecting, connected, error, etc.
	Enabled        bool
	Quarantined    bool
	Connected      bool
	LastError      string
	LastErrorTime  *time.Time
	ConnectedAt    *time.Time
	DisconnectedAt *time.Time
	RetryCount     int
	ToolCount      int
	Tools          []ToolInfo // Phase 7.1: Cached tool list for lock-free reads
	Metadata       map[string]interface{}
}

// ServerStatusSnapshot is an immutable snapshot of all server statuses.
type ServerStatusSnapshot struct {
	Servers   map[string]*ServerStatus
	Timestamp time.Time
}

// View provides a read-only view of server statuses.
// It maintains an in-memory snapshot updated via supervisor events.
type View struct {
	snapshot atomic.Value // *ServerStatusSnapshot
	mu       sync.RWMutex // Protects updates only
}

// New creates a new state view.
func New() *View {
	v := &View{}
	v.snapshot.Store(&ServerStatusSnapshot{
		Servers:   make(map[string]*ServerStatus),
		Timestamp: time.Now(),
	})
	return v
}

// Snapshot returns the current immutable snapshot of all server statuses.
// This is a lock-free read operation.
func (v *View) Snapshot() *ServerStatusSnapshot {
	return v.snapshot.Load().(*ServerStatusSnapshot)
}

// GetServer returns the status for a specific server (lock-free).
func (v *View) GetServer(name string) (*ServerStatus, bool) {
	snap := v.Snapshot()
	status, ok := snap.Servers[name]
	return status, ok
}

// GetAll returns all server statuses (lock-free).
func (v *View) GetAll() map[string]*ServerStatus {
	return v.Snapshot().Servers
}

// UpdateServer updates or creates a server status entry.
func (v *View) UpdateServer(name string, fn func(*ServerStatus)) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Get current snapshot
	oldSnap := v.snapshot.Load().(*ServerStatusSnapshot)

	// Deep clone the map
	newServers := make(map[string]*ServerStatus, len(oldSnap.Servers))
	for k, vs := range oldSnap.Servers {
		// Clone each status
		newStatus := *vs
		if vs.LastErrorTime != nil {
			t := *vs.LastErrorTime
			newStatus.LastErrorTime = &t
		}
		if vs.ConnectedAt != nil {
			t := *vs.ConnectedAt
			newStatus.ConnectedAt = &t
		}
		if vs.DisconnectedAt != nil {
			t := *vs.DisconnectedAt
			newStatus.DisconnectedAt = &t
		}
		if vs.Tools != nil {
			// Phase 7.1: Clone tools slice
			newStatus.Tools = make([]ToolInfo, len(vs.Tools))
			copy(newStatus.Tools, vs.Tools)
		}
		if vs.Metadata != nil {
			newStatus.Metadata = make(map[string]interface{}, len(vs.Metadata))
			for mk, mv := range vs.Metadata {
				newStatus.Metadata[mk] = mv
			}
		}
		newServers[k] = &newStatus
	}

	// Get or create the server status
	status, exists := newServers[name]
	if !exists {
		status = &ServerStatus{
			Name:     name,
			Metadata: make(map[string]interface{}),
		}
		newServers[name] = status
	}

	// Apply the update function
	fn(status)

	// Store the new snapshot
	v.snapshot.Store(&ServerStatusSnapshot{
		Servers:   newServers,
		Timestamp: time.Now(),
	})
}

// RemoveServer removes a server from the view.
func (v *View) RemoveServer(name string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Get current snapshot
	oldSnap := v.snapshot.Load().(*ServerStatusSnapshot)

	// Check if server exists
	if _, exists := oldSnap.Servers[name]; !exists {
		return
	}

	// Deep clone the map without the removed server
	newServers := make(map[string]*ServerStatus, len(oldSnap.Servers)-1)
	for k, vs := range oldSnap.Servers {
		if k == name {
			continue
		}
		// Clone each status
		newStatus := *vs
		if vs.LastErrorTime != nil {
			t := *vs.LastErrorTime
			newStatus.LastErrorTime = &t
		}
		if vs.ConnectedAt != nil {
			t := *vs.ConnectedAt
			newStatus.ConnectedAt = &t
		}
		if vs.DisconnectedAt != nil {
			t := *vs.DisconnectedAt
			newStatus.DisconnectedAt = &t
		}
		if vs.Tools != nil {
			// Phase 7.1: Clone tools slice
			newStatus.Tools = make([]ToolInfo, len(vs.Tools))
			copy(newStatus.Tools, vs.Tools)
		}
		if vs.Metadata != nil {
			newStatus.Metadata = make(map[string]interface{}, len(vs.Metadata))
			for mk, mv := range vs.Metadata {
				newStatus.Metadata[mk] = mv
			}
		}
		newServers[k] = &newStatus
	}

	// Store the new snapshot
	v.snapshot.Store(&ServerStatusSnapshot{
		Servers:   newServers,
		Timestamp: time.Now(),
	})
}

// Count returns the number of servers in the view (lock-free).
func (v *View) Count() int {
	return len(v.Snapshot().Servers)
}

// CountByState returns the number of servers in a specific state (lock-free).
func (v *View) CountByState(state string) int {
	snap := v.Snapshot()
	count := 0
	for _, status := range snap.Servers {
		if status.State == state {
			count++
		}
	}
	return count
}

// CountConnected returns the number of connected servers (lock-free).
func (v *View) CountConnected() int {
	snap := v.Snapshot()
	count := 0
	for _, status := range snap.Servers {
		if status.Connected {
			count++
		}
	}
	return count
}
