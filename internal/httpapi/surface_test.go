package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

func TestSurfaceFromRequest(t *testing.T) {
	cases := []struct {
		surface, client string
		want            profile.Surface
	}{
		{"", "", profile.SurfaceAPI},
		{"web", "", profile.SurfaceWeb},
		{"macos", "cli/1.0", profile.SurfaceMacOS},
		{"cli", "", profile.SurfaceCLI},
		{"MCP", "", profile.SurfaceAPI}, // mcp is an internal surface, never claimed over REST
		{"bogus", "", profile.SurfaceAPI},
		{"", "cli/0.70.0", profile.SurfaceCLI},
		{"", "tray/0.70.0", profile.SurfaceMacOS},
		{"", "webui/web", profile.SurfaceWeb},
		{"", "curl/8", profile.SurfaceAPI},
	}
	for _, c := range cases {
		req := httptest.NewRequest("PUT", "/x", nil)
		if c.surface != "" {
			req.Header.Set(XMCPProxySurfaceHeader, c.surface)
		}
		if c.client != "" {
			req.Header.Set(XMCPProxyClientHeader, c.client)
		}
		require.Equal(t, c.want, surfaceFromRequest(req), "surface=%q client=%q", c.surface, c.client)
	}
}
