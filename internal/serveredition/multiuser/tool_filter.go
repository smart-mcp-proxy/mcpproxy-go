//go:build server

package multiuser

import (
	"context"
	"sort"

	"go.uber.org/zap"
)

// ToolInfo represents a tool with its source server.
type ToolInfo struct {
	ToolName   string
	ServerName string
	Ownership  ServerOwnership
}

// ToolFilter provides user-scoped tool discovery.
// It wraps a Router to filter global tool lists down to only those tools
// that belong to servers the current user has access to.
type ToolFilter struct {
	router *Router
	logger *zap.SugaredLogger
}

// NewToolFilter creates a new ToolFilter backed by the given Router.
func NewToolFilter(router *Router, logger *zap.SugaredLogger) *ToolFilter {
	return &ToolFilter{
		router: router,
		logger: logger.With("component", "tool_filter"),
	}
}

// FilterToolsByUser filters a global tool list to only include tools from
// servers the user has access to. Tools from inaccessible servers are silently
// dropped. If no auth context is present, an empty list is returned.
func (f *ToolFilter) FilterToolsByUser(ctx context.Context, allTools []ToolInfo) []ToolInfo {
	servers, err := f.router.GetUserServers(ctx)
	if err != nil {
		f.logger.Debugw("Cannot filter tools: no auth context", "error", err)
		return nil
	}

	// Build a set of accessible server names for O(1) lookup.
	accessible := make(map[string]ServerOwnership, len(servers))
	for _, s := range servers {
		accessible[s.Config.Name] = s.Ownership
	}

	var result []ToolInfo
	for _, tool := range allTools {
		if ownership, ok := accessible[tool.ServerName]; ok {
			result = append(result, ToolInfo{
				ToolName:   tool.ToolName,
				ServerName: tool.ServerName,
				Ownership:  ownership,
			})
		}
	}

	return result
}

// GetAccessibleServerNames returns a sorted list of all server names the user
// can access. This is useful for scoping BM25 search queries to only include
// tools from accessible servers.
func (f *ToolFilter) GetAccessibleServerNames(ctx context.Context) ([]string, error) {
	servers, err := f.router.GetUserServers(ctx)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(servers))
	for _, s := range servers {
		names = append(names, s.Config.Name)
	}
	sort.Strings(names)
	return names, nil
}

// IsToolAccessible checks if a tool's server is accessible to the user.
// This is a convenience method for quick access checks during tool calls.
func (f *ToolFilter) IsToolAccessible(ctx context.Context, serverName string) bool {
	return f.router.IsServerAccessible(ctx, serverName)
}
