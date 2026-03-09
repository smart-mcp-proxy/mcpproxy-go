//go:build server

package workspace

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

// UserWorkspace holds a user's personal server configurations and state.
// For the MVP, it acts as a configuration container rather than maintaining
// live MCP connections. The MultiUserRouter uses it to determine which
// servers a user has access to.
type UserWorkspace struct {
	UserID     string
	mu         sync.RWMutex
	servers    map[string]*config.ServerConfig
	lastAccess time.Time
	logger     *zap.SugaredLogger
}

// NewUserWorkspace creates a new workspace for the given user.
func NewUserWorkspace(userID string, logger *zap.SugaredLogger) *UserWorkspace {
	return &UserWorkspace{
		UserID:     userID,
		servers:    make(map[string]*config.ServerConfig),
		lastAccess: time.Now(),
		logger:     logger.With("component", "user_workspace", "user_id", userID),
	}
}

// LoadServers loads the user's server configurations from the persistent store.
func (w *UserWorkspace) LoadServers(store *users.UserStore) error {
	servers, err := store.ListUserServers(w.UserID)
	if err != nil {
		return fmt.Errorf("failed to load servers for user %s: %w", w.UserID, err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.servers = make(map[string]*config.ServerConfig, len(servers))
	for _, s := range servers {
		w.servers[s.Name] = s
	}

	w.logger.Infow("Loaded user servers", "count", len(w.servers))
	return nil
}

// GetServers returns all personal server configurations.
func (w *UserWorkspace) GetServers() []*config.ServerConfig {
	w.mu.RLock()
	defer w.mu.RUnlock()

	result := make([]*config.ServerConfig, 0, len(w.servers))
	for _, s := range w.servers {
		result = append(result, s)
	}
	return result
}

// GetServer returns a specific server configuration by name.
func (w *UserWorkspace) GetServer(name string) (*config.ServerConfig, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	s, ok := w.servers[name]
	return s, ok
}

// AddServer adds a server configuration and persists it to the store.
func (w *UserWorkspace) AddServer(store *users.UserStore, server *config.ServerConfig) error {
	if server.Name == "" {
		return fmt.Errorf("server name is required")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.servers[server.Name]; exists {
		return fmt.Errorf("server %q already exists in workspace", server.Name)
	}

	if err := store.CreateUserServer(w.UserID, server); err != nil {
		return fmt.Errorf("failed to persist server %q: %w", server.Name, err)
	}

	w.servers[server.Name] = server
	w.logger.Infow("Added server to workspace", "server", server.Name)
	return nil
}

// RemoveServer removes a server configuration and deletes it from the store.
func (w *UserWorkspace) RemoveServer(store *users.UserStore, name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.servers[name]; !exists {
		return fmt.Errorf("server %q not found in workspace", name)
	}

	if err := store.DeleteUserServer(w.UserID, name); err != nil {
		return fmt.Errorf("failed to delete server %q: %w", name, err)
	}

	delete(w.servers, name)
	w.logger.Infow("Removed server from workspace", "server", name)
	return nil
}

// UpdateServer updates an existing server configuration and persists the change.
func (w *UserWorkspace) UpdateServer(store *users.UserStore, server *config.ServerConfig) error {
	if server.Name == "" {
		return fmt.Errorf("server name is required")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.servers[server.Name]; !exists {
		return fmt.Errorf("server %q not found in workspace", server.Name)
	}

	if err := store.UpdateUserServer(w.UserID, server); err != nil {
		return fmt.Errorf("failed to update server %q: %w", server.Name, err)
	}

	w.servers[server.Name] = server
	w.logger.Infow("Updated server in workspace", "server", server.Name)
	return nil
}

// ServerNames returns a sorted list of server names in the workspace.
func (w *UserWorkspace) ServerNames() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	names := make([]string, 0, len(w.servers))
	for name := range w.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Touch updates the last access time to now.
func (w *UserWorkspace) Touch() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.lastAccess = time.Now()
}

// LastAccess returns the time the workspace was last accessed.
func (w *UserWorkspace) LastAccess() time.Time {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.lastAccess
}

// Shutdown cleans up any resources held by the workspace.
// For the MVP, this clears the in-memory server map. Future versions
// may close MCP connections here.
func (w *UserWorkspace) Shutdown() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.logger.Infow("Shutting down workspace", "server_count", len(w.servers))
	w.servers = make(map[string]*config.ServerConfig)
}
