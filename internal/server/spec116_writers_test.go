package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
)

func restPatch(t *testing.T, api *httpapi.Server, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/"+name, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", spec116APIKey)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	return rec
}

func tenantServer() *config.ServerConfig {
	sc := &config.ServerConfig{
		Name: "tenant", URL: "http://127.0.0.1:9/mcp", Protocol: "http", Enabled: true,
		TrustMode:      string(config.TrustModeAuto),
		ForwardHeaders: []string{"X-Tenant"},
		Env:            map[string]string{"K1": "v1"},
		Headers:        map[string]string{"H1": "v1"},
		Args:           []string{"old"},
	}
	sc.MarkQuarantineExplicitlySet(true)
	return sc
}

// T1.3 (#1560): a REST PATCH that only changes args must not revert newer
// writes to other fields. On main the handler resolves every omitted field
// from a configuration read before the write; after Spec 116 the resolution
// runs inside the commit, on the live entry.
func TestSpec116_T1_3_ParkedRESTPatchKeepsNewerFields(t *testing.T) {
	srv, cfgPath := newSpec116Server(t, tenantServer())
	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	api.SetPatchAfterParseHookForTest(func(string) {
		first := false
		once.Do(func() { first = true })
		if first {
			close(entered)
			<-release
		}
	})

	patchDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { patchDone <- restPatch(t, api, "tenant", `{"args":["new"]}`) }()
	awaitSignal(t, entered, "the REST PATCH to park")

	res, _, err := srv.mcpProxy.handlePatchUpstream(context.Background(), mcpCall(map[string]interface{}{
		"operation": "patch", "name": "tenant",
		"trust_mode": "manual", "forward_headers_json": "[]", "enabled": false,
		"env_json": `{"K2":"v2"}`,
	}))
	require.NoError(t, err)
	require.False(t, res.IsError, "MCP patch must succeed")
	rec := restPatch(t, api, "tenant", `{"headers":{"H2":"v2"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	close(release)
	parked := <-patchDone
	require.Equal(t, http.StatusOK, parked.Code, parked.Body.String())

	for label, sc := range map[string]*config.ServerConfig{
		"live":    liveServer(srv, "tenant"),
		"storage": storedServer(t, srv, "tenant"),
		"disk":    diskServer(t, cfgPath, "tenant"),
	} {
		require.NotNil(t, sc, label)
		assert.Equal(t, []string{"new"}, sc.Args, "%s: the parked PATCH's own field", label)
		assert.Equal(t, string(config.TrustModeManual), sc.TrustMode, "%s: newer trust_mode survives", label)
		assert.Empty(t, sc.ForwardHeaders, "%s: the removed allowlist stays removed", label)
		assert.False(t, sc.Enabled, "%s: the newer disable survives", label)
		assert.Equal(t, "v2", sc.Env["K2"], "%s: the newer env key survives", label)
		assert.Equal(t, "v1", sc.Env["K1"], "%s", label)
		assert.Equal(t, "v2", sc.Headers["H2"], "%s: the newer header survives", label)
		assert.Equal(t, "v1", sc.Headers["H1"], "%s", label)
	}
}

// T1.5 (#1558): an add that read the configuration before a concurrent
// ApplyConfig must not overwrite the newer profiles with its stale copy.
func TestSpec116_T1_5_ParkedAddKeepsNewerProfiles(t *testing.T) {
	srv, cfgPath := newSpec116Server(t,
		&config.ServerConfig{Name: "alpha", Command: "true", Protocol: "stdio", Enabled: false})

	entered := make(chan struct{})
	release := make(chan struct{})
	srv.createAfterConfigRead = func(string) {
		close(entered)
		<-release
	}
	addDone := make(chan error, 1)
	go func() {
		addDone <- srv.AddServer(context.Background(), &config.ServerConfig{
			Name: "bravo", Command: "true", Protocol: "stdio", Enabled: false,
		})
	}()
	awaitSignal(t, entered, "the add to park")

	next := cloneConfig(t, srv.runtime.Config())
	next.Profiles = append(next.Profiles, config.ProfileConfig{Name: "ops", Servers: []string{"alpha"}})
	_, err := srv.runtime.ApplyConfig(next, cfgPath)
	require.NoError(t, err)

	close(release)
	require.NoError(t, awaitErr(t, addDone, "AddServer"))

	hasOps := func(profiles []config.ProfileConfig) bool {
		for _, p := range profiles {
			if p.Name == "ops" {
				return true
			}
		}
		return false
	}
	assert.True(t, hasOps(srv.runtime.Config().Profiles), "the newer profile must survive in the live config")
	disk, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	assert.True(t, hasOps(disk.Profiles), "the newer profile must survive on disk")
	assert.NotNil(t, liveServer(srv, "bravo"), "the add must be committed")
	assert.NotNil(t, storedServer(t, srv, "bravo"))
}

// T1.6 (B1, #1061): MCP quarantine_security quarantine_server must purge the
// server's tools from the search index before it returns.
func TestSpec116_T1_6_MCPQuarantinePurgesIndex(t *testing.T) {
	trusted := &config.ServerConfig{
		Name: "trusted-library", URL: "http://127.0.0.1:9/mcp", Protocol: "http", Enabled: true,
	}
	trusted.MarkQuarantineExplicitlySet(true)
	srv, _ := newSpec116Server(t, trusted)
	idx := srv.runtime.IndexManager()
	require.NoError(t, idx.BatchIndexTools([]*config.ToolMetadata{
		{ServerName: "trusted-library", Name: "read_customer_records", Description: "Read customer records with pagination", ParamsJSON: `{"type":"object"}`, Hash: "h1"},
	}))
	before, err := idx.GetToolsByServer("trusted-library")
	require.NoError(t, err)
	require.Len(t, before, 1)

	res, err := srv.mcpProxy.handleQuarantineUpstream(context.Background(), mcpCall(map[string]interface{}{
		"operation": "quarantine_server", "name": "trusted-library",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError)

	after, err := idx.GetToolsByServer("trusted-library")
	require.NoError(t, err)
	assert.Empty(t, after, "a quarantined server's tools must be gone from the index when the call returns")
	if live := liveServer(srv, "trusted-library"); assert.NotNil(t, live) {
		assert.True(t, live.Quarantined)
	}
}

func sharedBrokerServer(t *testing.T) *config.ServerConfig {
	t.Helper()
	var sc config.ServerConfig
	require.NoError(t, json.Unmarshal([]byte(`{
		"name":"shared-broker","url":"http://127.0.0.1:9/mcp","protocol":"http","enabled":false,
		"quarantined":false,"shared":true,
		"auth_broker":{"mode":"oauth_connect","token_endpoint":"https://idp.example/token","authorization_endpoint":"https://idp.example/authorize"}
	}`), &sc))
	return &sc
}

// T1.7 (r9-B #2): REST and MCP patches of a server must keep the config-only
// fields Shared and AuthBroker, which the storage row cannot carry.
func TestSpec116_T1_7_PatchKeepsSharedAndAuthBroker(t *testing.T) {
	srv, cfgPath := newSpec116Server(t, sharedBrokerServer(t))
	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)

	check := func(step string) {
		t.Helper()
		for label, sc := range map[string]*config.ServerConfig{
			"live": liveServer(srv, "shared-broker"),
			"disk": diskServer(t, cfgPath, "shared-broker"),
		} {
			require.NotNil(t, sc, "%s/%s", step, label)
			assert.True(t, sc.Shared, "%s/%s: shared must survive", step, label)
			assert.NotNil(t, sc.AuthBroker, "%s/%s: auth_broker must survive", step, label)
		}
	}

	rec := restPatch(t, api, "shared-broker", `{"args":["x"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	check("rest")

	res, _, err := srv.mcpProxy.handlePatchUpstream(context.Background(), mcpCall(map[string]interface{}{
		"operation": "patch", "name": "shared-broker", "args_json": `["y"]`,
	}))
	require.NoError(t, err)
	require.False(t, res.IsError)
	check("mcp")
}
