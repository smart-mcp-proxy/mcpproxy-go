//go:build server

package workspace

import (
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

// Manager manages user workspaces, creating them on demand and cleaning up idle ones.
type Manager struct {
	workspaces  map[string]*UserWorkspace
	mu          sync.RWMutex
	userStore   *users.UserStore
	idleTimeout time.Duration
	logger      *zap.SugaredLogger
	done        chan struct{}
}

// NewManager creates a new workspace manager.
func NewManager(userStore *users.UserStore, idleTimeout time.Duration, logger *zap.SugaredLogger) *Manager {
	return &Manager{
		workspaces:  make(map[string]*UserWorkspace),
		userStore:   userStore,
		idleTimeout: idleTimeout,
		logger:      logger.With("component", "workspace_manager"),
		done:        make(chan struct{}),
	}
}

// GetOrCreateWorkspace returns an existing workspace for the user, or creates
// a new one and loads the user's servers from the database.
func (m *Manager) GetOrCreateWorkspace(userID string) (*UserWorkspace, error) {
	// Fast path: check with read lock
	m.mu.RLock()
	ws, exists := m.workspaces[userID]
	m.mu.RUnlock()

	if exists {
		ws.Touch()
		return ws, nil
	}

	// Slow path: create with write lock
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if ws, exists = m.workspaces[userID]; exists {
		ws.Touch()
		return ws, nil
	}

	ws = NewUserWorkspace(userID, m.logger.Desugar().Sugar())
	if err := ws.LoadServers(m.userStore); err != nil {
		return nil, err
	}

	m.workspaces[userID] = ws
	m.logger.Infow("Created workspace", "user_id", userID, "active_workspaces", len(m.workspaces))
	return ws, nil
}

// GetWorkspace returns the workspace for a user if it exists.
func (m *Manager) GetWorkspace(userID string) (*UserWorkspace, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ws, exists := m.workspaces[userID]
	return ws, exists
}

// RemoveWorkspace removes and shuts down a user's workspace.
func (m *Manager) RemoveWorkspace(userID string) {
	m.mu.Lock()
	ws, exists := m.workspaces[userID]
	if exists {
		delete(m.workspaces, userID)
	}
	m.mu.Unlock()

	if exists {
		ws.Shutdown()
		m.logger.Infow("Removed workspace", "user_id", userID)
	}
}

// ActiveWorkspaceCount returns the number of currently active workspaces.
func (m *Manager) ActiveWorkspaceCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.workspaces)
}

// StartCleanup starts a background goroutine that periodically removes
// workspaces that have been idle longer than the configured timeout.
func (m *Manager) StartCleanup() {
	// Run cleanup at half the idle timeout interval, minimum 30 seconds
	interval := m.idleTimeout / 2
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				m.cleanupIdle()
			case <-m.done:
				return
			}
		}
	}()

	m.logger.Infow("Started workspace cleanup", "interval", interval, "idle_timeout", m.idleTimeout)
}

// Stop stops the cleanup goroutine and shuts down all active workspaces.
func (m *Manager) Stop() {
	close(m.done)

	m.mu.Lock()
	workspaces := make(map[string]*UserWorkspace, len(m.workspaces))
	for k, v := range m.workspaces {
		workspaces[k] = v
	}
	m.workspaces = make(map[string]*UserWorkspace)
	m.mu.Unlock()

	for userID, ws := range workspaces {
		ws.Shutdown()
		m.logger.Debugw("Shut down workspace during stop", "user_id", userID)
	}

	m.logger.Infow("Workspace manager stopped", "workspaces_shut_down", len(workspaces))
}

// cleanupIdle removes workspaces that haven't been accessed within the idle timeout.
func (m *Manager) cleanupIdle() {
	cutoff := time.Now().Add(-m.idleTimeout)

	m.mu.Lock()
	var toRemove []string
	for userID, ws := range m.workspaces {
		if ws.LastAccess().Before(cutoff) {
			toRemove = append(toRemove, userID)
		}
	}

	removed := make(map[string]*UserWorkspace, len(toRemove))
	for _, userID := range toRemove {
		removed[userID] = m.workspaces[userID]
		delete(m.workspaces, userID)
	}
	m.mu.Unlock()

	// Shutdown outside the lock to avoid holding it during cleanup
	for userID, ws := range removed {
		ws.Shutdown()
		m.logger.Infow("Cleaned up idle workspace", "user_id", userID)
	}

	if len(removed) > 0 {
		m.logger.Infow("Idle workspace cleanup complete", "removed", len(removed), "remaining", m.ActiveWorkspaceCount())
	}
}
