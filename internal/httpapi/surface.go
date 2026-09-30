package httpapi

import (
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
