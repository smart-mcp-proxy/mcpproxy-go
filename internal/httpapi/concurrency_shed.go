package httpapi

import (
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// toolCallRequestSource classifies who made a REST tool call, so the activity
// record — and the "rejected" record a shed produces (spec 093 FR-012) — names
// the real origin. POST /api/v1/tools/call is shared by the CLI, the Web UI and
// the tray; the surface header those clients already send ("cli/<version>",
// "webui/web", "tray/<version>") is what tells them apart. Anything that does
// not identify itself as the CLI is plain REST.
func toolCallRequestSource(r *http.Request) reqcontext.RequestSource {
	if strings.HasPrefix(strings.ToLower(r.Header.Get(XMCPProxyClientHeader)), "cli/") {
		return reqcontext.SourceCLI
	}
	return reqcontext.SourceRESTAPI
}

// restClientSurface names the first-party surface behind a REST request, from
// the X-MCPProxy-Client header the CLI, the Web UI and the tray already send,
// or "rest" for any other caller (a script, curl). Spec 124 FR-002 uses it as
// the client name of a call that has no MCP clientInfo.
func restClientSurface(r *http.Request) string {
	switch s := telemetry.ParseClientSurface(r.Header.Get(XMCPProxyClientHeader)); s {
	case telemetry.SurfaceCLI, telemetry.SurfaceWebUI, telemetry.SurfaceTray:
		return s.String()
	default:
		return "rest"
	}
}

// defaultRetryAfterSeconds is the hint used when the shedding scope reported no
// queue_timeout (a `queue_size: 0` scope sheds instantly and has no wait
// budget). Something small and concrete beats omitting the header: a client
// with no hint typically retries immediately and re-sheds.
const defaultRetryAfterSeconds = 1

// retryAfterSeconds converts a scope's effective queue_timeout into the
// Retry-After header value (RFC 9110 delta-seconds, spec 093 FR-011). Sub-second
// budgets round UP to 1 so the header never says "retry now".
func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return defaultRetryAfterSeconds
	}
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		return 1
	}
	return secs
}
