package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newSpec116ServerWithSecretDirs(t *testing.T, allowed string, servers ...*config.ServerConfig) (*Server, string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = spec116APIKey
	cfg.Servers = servers
	w := config.SecretFileDirs{allowed}
	cfg.SecretFileDirs = &w
	cfgPath := filepath.Join(t.TempDir(), "mcp_config.json")
	raw, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, raw, 0o600))
	loaded, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	loaded.DataDir = cfg.DataDir
	loaded.SecretFileDirs = &w
	srv, err := NewServerWithConfigPath(loaded, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })
	bootSpec116(t, srv)
	return srv, cfgPath
}

func TestREST_AddServer_RefusesSecretPolicyBeforeSave(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "secrets")
	srv, cfgPath := newSpec116ServerWithSecretDirs(t, allowed)
	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)

	body, _ := json.Marshal(map[string]any{
		"name":    "blocked",
		"url":     "http://127.0.0.1:9/mcp",
		"enabled": false,
		"env":     map[string]string{"K": "${env:MCPPROXY_API_KEY}"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", spec116APIKey)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), secret.ErrReservedEnvVar.Error())
	assert.Nil(t, liveServer(srv, "blocked"))
	assert.Nil(t, diskServer(t, cfgPath, "blocked"))
}

func TestREST_PatchServer_RefusesSecretPolicyBeforeSave(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "secrets")
	existing := &config.ServerConfig{
		Name: "s", URL: "http://127.0.0.1:9/mcp", Protocol: "streamable-http", Enabled: false,
	}
	srv, cfgPath := newSpec116ServerWithSecretDirs(t, allowed, existing)
	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)

	rec := restPatch(t, api, "s", `{"env":{"K":"${env:MCPPROXY_API_KEY}"}}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), secret.ErrReservedEnvVar.Error())
	assert.Empty(t, liveServer(srv, "s").Env)
	assert.Empty(t, diskServer(t, cfgPath, "s").Env)
}

func TestImportServersJSON_RealCommit_RefusesSecretPolicyBeforeSave(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "secrets")
	srv, _ := newSpec116ServerWithSecretDirs(t, allowed)
	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)

	body, _ := json.Marshal(httpapi.ImportRequest{
		Content: `{"mcpServers": {
			"bad": {"url": "http://127.0.0.1:9/mcp", "enabled": false, "env": {"K": "${env:MCPPROXY_API_KEY}"}},
			"good": {"command": "true", "enabled": false}
		}}`,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/import/json", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", spec116APIKey)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var wrapped struct {
		Data struct {
			Failed []struct {
				Name    string `json:"name"`
				Details string `json:"details"`
			} `json:"failed"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &wrapped))
	require.Len(t, wrapped.Data.Failed, 1)
	assert.Equal(t, "bad", wrapped.Data.Failed[0].Name)
	assert.Contains(t, wrapped.Data.Failed[0].Details, secret.ErrReservedEnvVar.Error())
	assert.NotNil(t, liveServer(srv, "good"))
	assert.Nil(t, liveServer(srv, "bad"))
}
