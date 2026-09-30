package server

import (
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"
)

// NotifyBindingChanged implements runtime.BindingNotifier (Spec 108 FR-026,
// plan D18): for every live session authenticated by the named token it
// clears the stored set_profile selection and sends
// notifications/tools/list_changed on the server instance that serves the
// session. It runs synchronously, so the clearing is complete before the
// binding mutation returns — enforcement never depends on delivery (every
// request re-resolves against the credential's current binding, FR-020), but
// no stale selection window is left either.
func (p *MCPProxyServer) NotifyBindingChanged(tokenName string) {
	if p.sessionStore == nil {
		return
	}
	for _, target := range p.sessionStore.NotifyTargets(tokenName) {
		p.sessionStore.SetActiveProfile(target.ID, "")
		p.sendToolsListChanged(target)
	}
}

// sendToolsListChanged pushes tools/list_changed to one session on its
// recorded instance; when the instance was not recorded it tries each routing
// server in turn (a session lives on exactly one, the others answer
// ErrSessionNotFound), so a session is notified exactly once.
func (p *MCPProxyServer) sendToolsListChanged(target sessionTarget) {
	candidates := []*mcpserver.MCPServer{target.Server}
	if target.Server == nil {
		candidates = []*mcpserver.MCPServer{p.server, p.directServer, p.codeExecServer, p.callToolServer}
	}
	for _, srv := range candidates {
		if srv == nil {
			continue
		}
		err := srv.SendNotificationToSpecificClient(target.ID, mcp.MethodNotificationToolsListChanged, nil)
		switch {
		case err == nil:
			return
		case errors.Is(err, mcpserver.ErrSessionNotFound):
			continue
		default:
			if p.logger != nil {
				p.logger.Debug("tools/list_changed not delivered after a binding change",
					zap.String("session_id", target.ID), zap.Error(err))
			}
			return
		}
	}
}
