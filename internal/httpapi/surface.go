package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// XMCPProxySurfaceHeader names the surface a profile-mutating request comes
// from (web, macos, cli). Attribution only, never authorization: a caller can
// set it to anything, so it selects a label on the profile_change record and
// nothing else.
const XMCPProxySurfaceHeader = "X-MCPProxy-Surface"

// surfaceFromRequest attributes a request to a surface (plan D20): an explicit
// X-MCPProxy-Surface in {web, macos, cli} wins; otherwise the X-MCPProxy-Client
// prefix decides (cli/ -> cli, tray/ -> macos, webui/ or web/ -> web); anything
// else is a plain API call.
func surfaceFromRequest(r *http.Request) profile.Surface {
	switch profile.Surface(strings.ToLower(strings.TrimSpace(r.Header.Get(XMCPProxySurfaceHeader)))) {
	case profile.SurfaceWeb:
		return profile.SurfaceWeb
	case profile.SurfaceMacOS:
		return profile.SurfaceMacOS
	case profile.SurfaceCLI:
		return profile.SurfaceCLI
	}
	client := strings.ToLower(r.Header.Get(XMCPProxyClientHeader))
	switch {
	case strings.HasPrefix(client, "cli/"):
		return profile.SurfaceCLI
	case strings.HasPrefix(client, "tray/"):
		return profile.SurfaceMacOS
	case strings.HasPrefix(client, "webui/"), strings.HasPrefix(client, "web/"):
		return profile.SurfaceWeb
	}
	return profile.SurfaceAPI
}

// actorFromRequest is the profile_change actor for a request: the credential
// kind and name from the AuthContext (plan D21) plus the surface above.
func actorFromRequest(r *http.Request) internalRuntime.Actor {
	a := internalRuntime.ActorFromContext(r.Context(), surfaceFromRequest(r))
	if transport.GetConnectionSource(r.Context()) == transport.ConnectionSourceTray {
		// The tray / CLI socket bypasses the API key (OS-level auth).
		a.Kind = string(auth.CredentialKindSocket)
	}
	return a
}

// actorContextMiddleware stamps the request's actor (actorFromRequest) into
// the context once the API-key / token middleware has run, so a runtime
// writer the handler calls can name the caller on its activity row without
// each handler threading it (issue #1698; runtime.RequestActor).
func (s *Server) actorContextMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := internalRuntime.ContextWithActor(r.Context(), actorFromRequest(r))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// quarantineContextController is the controller's optional capability to take
// the request context on a quarantine toggle (issue #1698). A controller
// without it still works; its rows just name no caller.
type quarantineContextController interface {
	QuarantineServerCtx(ctx context.Context, serverName string, quarantined bool) error
	QuarantineServerAtCtx(ctx context.Context, serverName string, quarantined bool, expectedIncarnation int64) error
}

func (s *Server) quarantineServer(ctx context.Context, serverName string, quarantined bool) error {
	if c, ok := s.controller.(quarantineContextController); ok {
		return c.QuarantineServerCtx(ctx, serverName, quarantined)
	}
	return s.controller.QuarantineServer(serverName, quarantined)
}

func (s *Server) quarantineServerAt(ctx context.Context, serverName string, quarantined bool, expectedIncarnation int64) error {
	if c, ok := s.controller.(quarantineContextController); ok {
		return c.QuarantineServerAtCtx(ctx, serverName, quarantined, expectedIncarnation)
	}
	return s.incarnationController().QuarantineServerAt(serverName, quarantined, expectedIncarnation)
}

// lifecycleContextController is the controller's optional capability to take
// the request context on the legacy enable/disable and restart fallbacks
// (issue #1698, review round 2), used when no management service is wired.
type lifecycleContextController interface {
	EnableServerCtx(ctx context.Context, serverName string, enabled bool) error
	RestartServerCtx(ctx context.Context, serverName string) error
}

func (s *Server) enableServer(ctx context.Context, serverName string, enabled bool) error {
	if c, ok := s.controller.(lifecycleContextController); ok {
		return c.EnableServerCtx(ctx, serverName, enabled)
	}
	return s.controller.EnableServer(serverName, enabled)
}

func (s *Server) restartServer(ctx context.Context, serverName string) error {
	if c, ok := s.controller.(lifecycleContextController); ok {
		return c.RestartServerCtx(ctx, serverName)
	}
	return s.controller.RestartServer(serverName)
}
