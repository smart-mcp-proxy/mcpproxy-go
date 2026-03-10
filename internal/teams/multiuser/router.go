//go:build teams

package multiuser

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/workspace"
)

// ServerOwnership indicates who owns a server.
type ServerOwnership string

const (
	// OwnershipShared indicates a server defined in the shared config file,
	// accessible to all authenticated users.
	OwnershipShared ServerOwnership = "shared"

	// OwnershipPersonal indicates a server owned by a specific user,
	// stored in their workspace.
	OwnershipPersonal ServerOwnership = "personal"
)

// ServerInfo holds a server config with ownership metadata.
type ServerInfo struct {
	Config    *config.ServerConfig
	Ownership ServerOwnership
}

// Router routes MCP operations to the correct upstream based on user identity.
// It merges shared servers (from the config file) with personal servers (from
// the user's workspace) to provide a unified view of accessible servers.
type Router struct {
	sharedServers    []*config.ServerConfig
	workspaceManager *workspace.Manager
	mu               sync.RWMutex
	logger           *zap.SugaredLogger
}

// NewRouter creates a new multi-user router.
//
// sharedServers are the servers defined in the main config file, accessible
// to all authenticated users. workspaceManager provides access to per-user
// personal server configurations.
func NewRouter(sharedServers []*config.ServerConfig, workspaceManager *workspace.Manager, logger *zap.SugaredLogger) *Router {
	return &Router{
		sharedServers:    sharedServers,
		workspaceManager: workspaceManager,
		logger:           logger.With("component", "multiuser_router"),
	}
}

// GetUserServers returns all servers accessible to the user from context.
// Admin users get all shared servers. Regular users get shared servers plus
// their own personal servers. Returns an error if no auth context is present.
func (r *Router) GetUserServers(ctx context.Context) ([]ServerInfo, error) {
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil {
		return nil, fmt.Errorf("no authentication context")
	}

	r.mu.RLock()
	shared := make([]*config.ServerConfig, len(r.sharedServers))
	copy(shared, r.sharedServers)
	r.mu.RUnlock()

	var result []ServerInfo

	// All authenticated users can access shared servers.
	for _, s := range shared {
		result = append(result, ServerInfo{
			Config:    s,
			Ownership: OwnershipShared,
		})
	}

	// Users (both regular and admin) also get their personal servers.
	if ac.IsUser() && ac.GetUserID() != "" {
		ws, err := r.workspaceManager.GetOrCreateWorkspace(ac.GetUserID())
		if err != nil {
			r.logger.Warnw("Failed to load workspace for user",
				"user_id", ac.GetUserID(), "error", err)
			// Return shared servers even if workspace loading fails.
			return result, nil
		}

		for _, s := range ws.GetServers() {
			// Skip personal servers that shadow a shared server name to avoid
			// ambiguity. Shared servers take precedence.
			if r.isSharedServer(s.Name) {
				r.logger.Debugw("Skipping personal server that shadows shared server",
					"server", s.Name, "user_id", ac.GetUserID())
				continue
			}
			result = append(result, ServerInfo{
				Config:    s,
				Ownership: OwnershipPersonal,
			})
		}
	}

	return result, nil
}

// GetServerForUser finds a specific server by name, checking both shared and
// personal servers. Returns an error if the server is not found or the user
// is not authorized to access it.
func (r *Router) GetServerForUser(ctx context.Context, serverName string) (*ServerInfo, error) {
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil {
		return nil, fmt.Errorf("no authentication context")
	}

	// Check shared servers first.
	r.mu.RLock()
	for _, s := range r.sharedServers {
		if s.Name == serverName {
			r.mu.RUnlock()
			return &ServerInfo{
				Config:    s,
				Ownership: OwnershipShared,
			}, nil
		}
	}
	r.mu.RUnlock()

	// Check user's personal servers.
	if ac.IsUser() && ac.GetUserID() != "" {
		ws, err := r.workspaceManager.GetOrCreateWorkspace(ac.GetUserID())
		if err != nil {
			return nil, fmt.Errorf("failed to load workspace: %w", err)
		}

		if s, ok := ws.GetServer(serverName); ok {
			return &ServerInfo{
				Config:    s,
				Ownership: OwnershipPersonal,
			}, nil
		}
	}

	return nil, fmt.Errorf("server %q not found or not accessible", serverName)
}

// IsServerAccessible returns true if the user from the context can access
// the named server. This is a convenience method that does not return
// detailed error information.
func (r *Router) IsServerAccessible(ctx context.Context, serverName string) bool {
	info, err := r.GetServerForUser(ctx, serverName)
	return err == nil && info != nil
}

// UpdateSharedServers replaces the shared server list. This is called on
// config reload to pick up changes to the shared server definitions.
func (r *Router) UpdateSharedServers(servers []*config.ServerConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.sharedServers = servers
	r.logger.Infow("Updated shared servers", "count", len(servers))
}

// GetSharedServerNames returns a sorted list of shared server names.
func (r *Router) GetSharedServerNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.sharedServers))
	for _, s := range r.sharedServers {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}

// isSharedServer checks if a server name matches any shared server.
// Caller must not hold r.mu (this method acquires the read lock).
func (r *Router) isSharedServer(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, s := range r.sharedServers {
		if s.Name == name {
			return true
		}
	}
	return false
}
