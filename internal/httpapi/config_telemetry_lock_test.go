package httpapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 109 FR-044a (user-test F-03 follow-up): when an environment variable
// (DO_NOT_TRACK, CI, MCPPROXY_TELEMETRY=false) forces telemetry off, the Web
// and macOS UIs lock telemetry.enabled. The REST write doors enforce the same
// lock server-side, so a client that skips the UI (curl, a hand-edited Raw JSON
// post, a miscased key) cannot save a value the environment overrides.

func telemetryLockServer(t *testing.T, stored *bool) (*Server, *funnelController) {
	t.Helper()
	ctrl := &funnelController{desired: &config.Config{
		Listen:    "127.0.0.1:8080",
		Telemetry: &config.TelemetryConfig{Enabled: stored},
	}}
	return NewServer(ctrl, zap.NewNop().Sugar(), nil), ctrl
}

func TestApplyConfig_EnvLockRefusesTelemetryChange(t *testing.T) {
	cases := []struct {
		name   string
		stored bool
		doc    string
	}{
		{"exact key", true, `{"listen":"127.0.0.1:8080","telemetry":{"enabled":false}}`},
		{"miscased key", true, `{"listen":"127.0.0.1:8080","Telemetry":{"ENABLED":false}}`},
		{"flip to on", false, `{"listen":"127.0.0.1:8080","telemetry":{"enabled":true}}`},
		{"telemetry unset", true, `{"listen":"127.0.0.1:8080"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setStatusTelemetryEnv(t, "false")
			srv, ctrl := telemetryLockServer(t, boolPtr(tc.stored))

			w := funnelRequest(t, srv, http.MethodPost, "/api/v1/config/apply", tc.doc)
			assert.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), "MCPPROXY_TELEMETRY=false")
			require.NotNil(t, ctrl.desired.Telemetry)
			require.NotNil(t, ctrl.desired.Telemetry.Enabled)
			assert.Equal(t, tc.stored, *ctrl.desired.Telemetry.Enabled, "the stored value must be untouched")
		})
	}
}

func TestApplyConfig_EnvLockAllowsUnchangedTelemetry(t *testing.T) {
	setStatusTelemetryEnv(t, "false")
	srv, ctrl := telemetryLockServer(t, boolPtr(true))

	// The Raw JSON editor's GET -> edit -> POST round trip echoes telemetry back
	// unchanged while the operator edits something else.
	w := funnelRequest(t, srv, http.MethodPost, "/api/v1/config/apply",
		`{"listen":"127.0.0.1:7070","telemetry":{"enabled":true}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "127.0.0.1:7070", ctrl.desired.Listen)
}

func TestApplyConfig_NoEnvOverrideAllowsTelemetryChange(t *testing.T) {
	setStatusTelemetryEnv(t, "")
	srv, ctrl := telemetryLockServer(t, boolPtr(true))

	w := funnelRequest(t, srv, http.MethodPost, "/api/v1/config/apply",
		`{"listen":"127.0.0.1:8080","telemetry":{"enabled":false}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, ctrl.desired.Telemetry)
	assert.False(t, *ctrl.desired.Telemetry.Enabled)
}

func TestPatchConfig_EnvLockRefusesTelemetryChange(t *testing.T) {
	setStatusTelemetryEnv(t, "false")
	srv, ctrl := telemetryLockServer(t, boolPtr(true))

	w := funnelRequest(t, srv, http.MethodPatch, "/api/v1/config", `{"telemetry":{"enabled":false}}`)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assert.True(t, *ctrl.desired.Telemetry.Enabled)

	// An unrelated patch is unaffected, and leaves telemetry as stored.
	w = funnelRequest(t, srv, http.MethodPatch, "/api/v1/config", `{"listen":"127.0.0.1:9090"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.True(t, *ctrl.desired.Telemetry.Enabled)
}

// A config that never set telemetry.enabled is rendered by GET /api/v1/config
// with the resolved value; posting that document back is not a write to the
// locked setting, whichever variable forces telemetry off.
func TestApplyConfig_EnvLockAllowsRoundTripOfUnsetTelemetry(t *testing.T) {
	cases := []struct {
		name                string
		ci, dnt, mcpproxy   string
		renderedByGetConfig string
	}{
		{"MCPPROXY_TELEMETRY=false", "", "", "false", "false"},
		{"CI=true", "true", "", "", "true"},
		{"DO_NOT_TRACK=1", "", "1", "", "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CI", tc.ci)
			t.Setenv("DO_NOT_TRACK", tc.dnt)
			t.Setenv("MCPPROXY_TELEMETRY", tc.mcpproxy)
			srv, ctrl := telemetryLockServer(t, nil)

			w := funnelRequest(t, srv, http.MethodPost, "/api/v1/config/apply",
				`{"listen":"127.0.0.1:7070","telemetry":{"enabled":`+tc.renderedByGetConfig+`}}`)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, "127.0.0.1:7070", ctrl.desired.Listen)
		})
	}
}

func TestApplyConfig_EnvLockRefusesTurningUnsetTelemetryOn(t *testing.T) {
	setStatusTelemetryEnv(t, "false")
	srv, ctrl := telemetryLockServer(t, nil)

	w := funnelRequest(t, srv, http.MethodPost, "/api/v1/config/apply",
		`{"listen":"127.0.0.1:8080","telemetry":{"enabled":true}}`)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assert.Nil(t, ctrl.desired.Telemetry.Enabled)
}
