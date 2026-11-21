package server

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// watchClientRoots forwards a client's notifications/roots/list_changed to the
// upstreams it was the last client to call, so they ask it for its roots again
// (#1966). Every MCP server mcpproxy serves clients from installs it.
func watchClientRoots(srv *mcpserver.MCPServer, manager *upstream.Manager) {
	if srv == nil || manager == nil {
		return
	}
	srv.AddNotificationHandler(string(mcp.MethodNotificationRootsListChanged), func(ctx context.Context, _ mcp.JSONRPCNotification) {
		if s := mcpserver.ClientSessionFromContext(ctx); s != nil {
			manager.NotifyClientRootsChanged(s.SessionID())
		}
	})
}
