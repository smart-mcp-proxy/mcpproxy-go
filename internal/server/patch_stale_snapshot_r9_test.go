package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
)

// staleSnapshotController serves the REST handler a config snapshot taken
// before later commits landed, which is what a PATCH that paused after its
// snapshot read observes.
type staleSnapshotController struct {
	*Server
	stale *config.Config
}

func (c *staleSnapshotController) GetConfig() (*config.Config, error) { return c.stale, nil }

// UX-01 r9: a REST PATCH must resolve omitted fields and map merges inside the
// config commit. Resolving them from a snapshot read earlier reverted a later
// stricter trust mode, a removed forwarding allowlist and a disable, and
// dropped disjoint env/header edits.
func TestRESTPatch_ResolvesOmittedFieldsAndMapMergesInsideTheCommit(t *testing.T) {
	srv, cfgPath := guardedApplyServer(t, func(cfg *config.Config) {
		cfg.Servers = []*config.ServerConfig{{
			Name: "a", Enabled: true, Protocol: "http", URL: "http://127.0.0.1:1/mcp",
			TrustMode:      "auto",
			ForwardHeaders: []string{"X-Forward"},
			Env:            map[string]string{"E1": "1"},
			Headers:        map[string]string{"H1": "1"},
		}}
	})
	seedServerA(t, srv)

	stale := cloneConfig(t, srv.runtime.Config())

	// Later commits through other paths.
	require.NoError(t, srv.UpdateServer(context.Background(), "a", &config.ServerConfig{
		Enabled:        true,
		TrustMode:      "manual",
		ForwardHeaders: []string{},
		Env:            map[string]string{"E1": "1", "E3": "3"},
		Headers:        map[string]string{"H1": "1", "H3": "3"},
	}))
	require.NoError(t, srv.EnableServer("a", false))

	api := httpapi.NewServer(&staleSnapshotController{Server: srv, stale: stale}, zap.NewNop().Sugar(), nil)
	body := []byte(`{"env":{"E2":"2"},"headers":{"H2":"2"}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/a", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "guard-apply-key")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	check := func(where string, sc *config.ServerConfig) {
		t.Helper()
		require.NotNil(t, sc, where)
		require.Equal(t, "manual", sc.TrustMode, where+": trust mode reverted")
		require.Empty(t, sc.ForwardHeaders, where+": removed allowlist restored")
		require.False(t, sc.Enabled, where+": disabled server re-enabled")
		require.Equal(t, map[string]string{"E1": "1", "E2": "2", "E3": "3"}, sc.Env, where+": env edits lost")
		require.Equal(t, map[string]string{"H1": "1", "H2": "2", "H3": "3"}, sc.Headers, where+": header edits lost")
	}
	stored, err := srv.runtime.StorageManager().GetUpstreamServer("a")
	require.NoError(t, err)
	check("storage", stored)
	var live *config.ServerConfig
	for _, s := range srv.runtime.Config().Servers {
		if s.Name == "a" {
			live = s
		}
	}
	check("live config", live)
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var onDisk config.Config
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	var disk *config.ServerConfig
	for _, s := range onDisk.Servers {
		if s.Name == "a" {
			disk = s
		}
	}
	check("file", disk)
}
