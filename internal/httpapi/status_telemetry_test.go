package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 109 FR-044a (codex first-run user test F-03): GET /api/v1/status
// reports the EFFECTIVE telemetry state so the Web wizard/Home banner, Web and
// macOS Settings and the macOS welcome can say "off — disabled by
// MCPPROXY_TELEMETRY=false" instead of a fixed "sends anonymous usage
// statistics". The block is operator plane: withheld from scoped callers like
// `activation`.

func statusTelemetryServer(t *testing.T, enabled *bool) (*Server, string) {
	t.Helper()
	cfg := scopeFixtureConfig(false)
	cfg.Telemetry = &config.TelemetryConfig{Enabled: enabled}
	ctrl := &scopeController{cfg: cfg, servers: scopeFixtureServers(), withManagement: true}
	return scopedAgentServer(t, ctrl, []string{"alpha"})
}

func statusTelemetryBlock(t *testing.T, rec *httptest.ResponseRecorder) (map[string]interface{}, bool) {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	block, ok := scopeDecodeData(t, rec)["telemetry"].(map[string]interface{})
	return block, ok
}

func setStatusTelemetryEnv(t *testing.T, mcpproxyTelemetry string) {
	t.Helper()
	// CI sets CI=true on GitHub Actions; pin all three.
	t.Setenv("CI", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MCPPROXY_TELEMETRY", mcpproxyTelemetry)
}

func TestStatusTelemetry_EnvOptOutOverridesConfig(t *testing.T) {
	setStatusTelemetryEnv(t, "false")
	on := true
	srv, _ := statusTelemetryServer(t, &on)

	block, ok := statusTelemetryBlock(t, scopeGet(t, srv, "/api/v1/status", scopeAdminAPIKey))
	require.True(t, ok, "admin caller must get the telemetry block")
	assert.Equal(t, false, block["enabled"])
	assert.Equal(t, "env", block["source"])
	assert.Equal(t, "MCPPROXY_TELEMETRY=false", block["disabled_by"])
}

func TestStatusTelemetry_ConfigDisabled(t *testing.T) {
	setStatusTelemetryEnv(t, "")
	off := false
	srv, _ := statusTelemetryServer(t, &off)

	block, ok := statusTelemetryBlock(t, scopeGet(t, srv, "/api/v1/status", scopeAdminAPIKey))
	require.True(t, ok)
	assert.Equal(t, false, block["enabled"])
	assert.Equal(t, "config", block["source"])
	_, hasBy := block["disabled_by"]
	assert.False(t, hasBy, "disabled_by is only present for source=env")
}

func TestStatusTelemetry_UnsetDefaultsOn(t *testing.T) {
	setStatusTelemetryEnv(t, "")
	srv, _ := statusTelemetryServer(t, nil)

	block, ok := statusTelemetryBlock(t, scopeGet(t, srv, "/api/v1/status", scopeAdminAPIKey))
	require.True(t, ok)
	assert.Equal(t, true, block["enabled"])
	assert.Equal(t, "default", block["source"])
}

func TestStatusTelemetry_WithheldFromScopedCaller(t *testing.T) {
	setStatusTelemetryEnv(t, "false")
	on := true
	srv, token := statusTelemetryServer(t, &on)

	// Positive control on the same router: the admin really is served it.
	_, ok := statusTelemetryBlock(t, scopeGet(t, srv, "/api/v1/status", scopeAdminAPIKey))
	require.True(t, ok, "precondition: admin is served the telemetry block")

	_, scoped := statusTelemetryBlock(t, scopeGet(t, srv, "/api/v1/status", token))
	assert.False(t, scoped, "telemetry state is operator plane and must be withheld from an agent token")
}

func TestStatusTelemetry_SocketCallerIsServed(t *testing.T) {
	setStatusTelemetryEnv(t, "false")
	srv, _ := statusTelemetryServer(t, nil)

	// The tray connects over the Unix socket, which the server marks as an
	// admin caller (OS-level auth, no API key).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", http.NoBody)
	req = req.WithContext(auth.WithAuthContext(req.Context(), auth.AdminContext()))
	rec := httptest.NewRecorder()
	srv.handleGetStatus(rec, req)

	block, ok := statusTelemetryBlock(t, rec)
	require.True(t, ok, "socket/tray caller must be served the telemetry block")
	assert.Equal(t, "env", block["source"])
}
