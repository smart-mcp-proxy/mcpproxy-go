package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type policyImportController struct {
	mockImportController
	cfg   *config.Config
	cfgPath string
	mu    sync.Mutex
	added []string
}

func (m *policyImportController) GetCurrentConfig() *config.Config { return m.cfg }

func (m *policyImportController) AddServer(_ context.Context, s *config.ServerConfig) error {
	if errs := config.ServerSecretPolicyErrors(m.cfg, m.cfgPath, s); len(errs) > 0 {
		return &config.InvalidServerUpdateError{Server: s.Name, Errors: errs}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.added = append(m.added, s.Name)
	return nil
}

func TestImportServersJSON_RefusesSecretPolicyBeforeSave(t *testing.T) {
	dir := t.TempDir()
	allowed := config.SecretFileDirs{dir + "/allowed"}
	mock := &policyImportController{
		mockImportController: mockImportController{apiKey: "test-key"},
		cfg: &config.Config{
			APIKey:         "test-key",
			DataDir:        dir,
			SecretFileDirs: &allowed,
		},
		cfgPath: dir + "/mcp_config.json",
	}
	srv := NewServer(mock, zap.NewNop().Sugar(), nil)

	body, _ := json.Marshal(ImportRequest{
		Content: `{"mcpServers": {
			"bad": {"url": "https://example.com/mcp", "env": {"K": "${env:MCPPROXY_API_KEY}"}},
			"good": {"command": "echo", "args": ["ok"]}
		}}`,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/import/json", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	rr := httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var wrapped wrappedImportResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &wrapped))
	require.Len(t, wrapped.Data.Failed, 1)
	assert.Equal(t, "bad", wrapped.Data.Failed[0].Name)
	assert.Contains(t, wrapped.Data.Failed[0].Details, secret.ErrReservedEnvVar.Error())
	mock.mu.Lock()
	assert.Equal(t, []string{"good"}, mock.added)
	mock.mu.Unlock()
}
