package httpapi

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
)

type recordingImportController struct {
	mockImportController
	mu    sync.Mutex
	added []string
}

func (m *recordingImportController) AddServer(_ context.Context, s *config.ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.added = append(m.added, s.Name)
	return nil
}

// RC4-IMPORT-001: a URL-only entry imported with a forced claude-desktop
// format and skip_quarantine=true used to be persisted as a commandless stdio
// server, which made the next core startup exit with code 4.
func TestImportServersJSON_RejectsInvalidServerNotPersisted(t *testing.T) {
	mock := &recordingImportController{mockImportController: mockImportController{apiKey: "test-key"}}
	server := NewServer(mock, zap.NewNop().Sugar(), nil)

	body, _ := json.Marshal(ImportRequest{
		Format: "claude-desktop",
		Content: `{"mcpServers": {
			"broken": {"url": "https://example.com/mcp"},
			"good": {"command": "npx", "args": ["-y", "some-server"]}
		}}`,
	})
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?skip_quarantine=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var wrapped wrappedImportResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &wrapped))

	require.Len(t, wrapped.Data.Imported, 1)
	assert.Equal(t, "good", wrapped.Data.Imported[0].Name)
	require.Len(t, wrapped.Data.Failed, 1)
	assert.Equal(t, "broken", wrapped.Data.Failed[0].Name)
	assert.Contains(t, wrapped.Data.Failed[0].Details, "command is required")

	mock.mu.Lock()
	defer mock.mu.Unlock()
	assert.Equal(t, []string{"good"}, mock.added, "the invalid server must never reach AddServer")
}
