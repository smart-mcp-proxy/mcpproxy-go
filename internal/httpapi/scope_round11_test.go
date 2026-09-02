package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// Round-11 cross-review findings on the #1166/#1167 branch. Same discipline as
// round 9: every test drives the PRODUCTION chi route table through
// (*Server).ServeHTTP with a real mcp_agt_ token, and every scoped assertion is
// paired with an admin control on the SAME router proving the fixture landed
// and the route matched.

// ---------------------------------------------------------------------------
// M1 — GET /api/v1/status served the activation block to any agent token
// ---------------------------------------------------------------------------

// Distinctive fixture values. They are strings/counts no other part of a
// /status response can produce, so a raw-body assertion cannot pass because the
// value happened to be absent for an unrelated reason.
const (
	activationClientAlpha = "cursor-on-operators-laptop"
	activationClientBeta  = "claude-desktop-on-operators-laptop"
	// A count no other field of a /status response can produce: the sibling
	// numbers are unix timestamps and single-digit server counts, so a raw-body
	// "4711" assertion cannot pass for an unrelated reason.
	activationCalls24h    = 4711
	activationSavedBucket = "10k_100k"
)

// scopeR11ActivationServer wires a scoped agent token onto the shared #1166
// fixture and attaches a telemetry service whose activation store is loaded
// with the values above. The store and DB are real (telemetry.NewActivationStore
// over a temp BBolt file), not a fake, so the handler takes exactly the
// production branch: provider → service → store → db → Load.
func scopeR11ActivationServer(t *testing.T) (*Server, string) {
	t.Helper()

	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	db, err := bbolt.Open(filepath.Join(t.TempDir(), "activation.db"), 0o600, &bbolt.Options{Timeout: 2 * time.Second})
	require.NoError(t, err)
	// The counter is only writable one increment at a time, and Load applies a
	// 24h decay window to it. NoSync keeps 4711 transactions to well under a
	// second; durability is irrelevant to a temp-dir fixture.
	db.NoSync = true
	t.Cleanup(func() { _ = db.Close() })

	store := telemetry.NewActivationStore()
	// Flags and the client list go through Save; the 24h counters have their
	// own encoding (count + window start) that Save deliberately does not
	// touch, so they are driven through the same mutators production uses.
	require.NoError(t, store.Save(db, telemetry.ActivationState{
		FirstConnectedServerEver:   true,
		FirstMCPClientEver:         true,
		FirstRetrieveToolsCallEver: true,
		FirstRealToolCallEver:      true,
		MCPClientsSeenEver:         []string{activationClientAlpha, activationClientBeta},
	}))
	for i := 0; i < activationCalls24h; i++ {
		require.NoError(t, store.IncrementRetrieveToolsCall(db))
	}
	require.NoError(t, store.AddTokensSaved(db, 50_000))

	svc := telemetry.New(ctrl.cfg, "", "v0.0.0-test", "personal", zap.NewNop())
	svc.SetActivationStore(store, db)
	srv.SetTelemetryPayloadProvider(func() *telemetry.Service { return svc })

	return srv, token
}

// TestGetStatus_ActivationBlockIsOperatorOnly.
//
// `activation` is the operator plane in one object: mcp_clients_seen_ever is
// the operator's entire MCP-client inventory (the class /sessions and
// /onboarding/state answer 403 for) and retrieve_tools_calls_24h /
// configured_ide_count are exact deployment-wide counters — the same count
// oracle this very route removed from upstream_stats.total_servers.
//
// The admin half is a real positive control: it proves the store is wired and
// the values genuinely reach the wire on this route, so the scoped half cannot
// pass because nothing was ever there.
func TestGetStatus_ActivationBlockIsOperatorOnly(t *testing.T) {
	srv, token := scopeR11ActivationServer(t)

	admin := scopeGet(t, srv, "/api/v1/status", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code, "body: %s", admin.Body.String())
	adminActivation, ok := scopeDecodeData(t, admin)["activation"].(map[string]interface{})
	require.True(t, ok, "precondition: an admin really is served the activation block: %s", admin.Body.String())
	require.Equal(t, float64(activationCalls24h), adminActivation["retrieve_tools_calls_24h"],
		"precondition: the fixture's counter reaches the wire")
	require.Equal(t, activationSavedBucket, adminActivation["estimated_tokens_saved_24h_bucket"],
		"precondition: the tokens-saved bucket reaches the wire")
	require.Contains(t, admin.Body.String(), activationClientAlpha,
		"precondition: the client inventory reaches the wire")

	agent := scopeGet(t, srv, "/api/v1/status", token)
	require.Equal(t, http.StatusOK, agent.Code, "body: %s", agent.Body.String())
	agentData := scopeDecodeData(t, agent)

	_, present := agentData["activation"]
	assert.False(t, present, "the activation block must be withheld from a scoped caller: %s", agent.Body.String())

	// Raw-body assertions, not just the decoded key: a projection that kept the
	// values under a different key would still be a leak.
	body := agent.Body.String()
	assert.NotContains(t, body, activationClientAlpha, "MCP-client inventory leaked to a scoped caller")
	assert.NotContains(t, body, activationClientBeta, "MCP-client inventory leaked to a scoped caller")
	assert.NotContains(t, body, "4711", "exact deployment-wide retrieve_tools count leaked to a scoped caller")
	assert.NotContains(t, body, activationSavedBucket, "deployment-wide tokens-saved bucket leaked to a scoped caller")
	// Key names too, so a projection that kept the numbers under a renamed or
	// re-nested key would still fail.
	for _, key := range []string{
		"mcp_clients_seen_ever",
		"retrieve_tools_calls_24h",
		"estimated_tokens_saved_24h_bucket",
		"first_real_tool_call_ever",
	} {
		assert.NotContains(t, body, key, "activation field %q reached a scoped caller", key)
	}

	// /status is a liveness surface an agent legitimately polls: withholding
	// one block must not turn into denying the route. Blanking the whole
	// response would be a different bug, so assert the neighbours survive.
	assert.Equal(t, true, agentData["running"], "scoped caller must still get liveness from /status")
	assert.NotEmpty(t, agentData["timestamp"])
	assert.Contains(t, agentData, "env_kind")
}

// ---------------------------------------------------------------------------
// M2 — PUT /api/v1/profiles/active had no admin gate
// ---------------------------------------------------------------------------

// scopeR11Put issues a PUT through the production route table.
func scopeR11Put(t *testing.T, srv *Server, path, apiKey, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// activeProfileOf reads the server-level active profile through the GET door,
// so the assertion is about observable state rather than a private field.
func activeProfileOf(t *testing.T, srv *Server) string {
	t.Helper()
	rec := scopeGet(t, srv, "/api/v1/profiles/active", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	active, _ := scopeDecodeData(t, rec)["active_profile"].(string)
	return active
}

// TestSetActiveProfile_DeniedToScopedCaller.
//
// The active profile is server-level SHARED state: it decides which servers the
// Web UI and the tray render. A read-scoped agent token could rewrite it,
// changing what the operator sees. The admin control proves the profile is
// really settable on this router, so the 403 is the gate and not a 404, a
// decode failure, or an unknown-profile rejection.
func TestSetActiveProfile_DeniedToScopedCaller(t *testing.T) {
	cfg := scopeFixtureConfig(false)
	cfg.Profiles = []config.ProfileConfig{{Name: "alpha-only", Servers: []string{"alpha"}}}
	ctrl := &scopeController{cfg: cfg, servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	require.Empty(t, activeProfileOf(t, srv), "precondition: nothing is active yet")

	agent := scopeR11Put(t, srv, "/api/v1/profiles/active", token, `{"profile":"alpha-only"}`)
	assert.Equal(t, http.StatusForbidden, agent.Code,
		"a read-scoped agent token must not mutate server-level shared state: %s", agent.Body.String())
	assert.Empty(t, activeProfileOf(t, srv),
		"the write must not have landed")

	// Positive control on the SAME router: the route exists, the body parses,
	// and the profile is genuinely settable — so the 403 above is the gate.
	admin := scopeR11Put(t, srv, "/api/v1/profiles/active", scopeAdminAPIKey, `{"profile":"alpha-only"}`)
	require.Equal(t, http.StatusOK, admin.Code, "body: %s", admin.Body.String())
	require.Equal(t, "alpha-only", activeProfileOf(t, srv))
}
