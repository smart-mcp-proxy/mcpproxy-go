package httpapi

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// denyingReader returns a permission-denied PathError for every read, simulating
// a macOS App-Data (TCC) block without a real OS denial (Spec 075).
func denyingReader(path string) ([]byte, error) {
	return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.EPERM}
}

func TestHandleConnectClient_OpenCodeAdoptedAlreadyExistsReturns200(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	// Pin %LOCALAPPDATA% under the test temp dir so OpenCode's Windows
	// config-path lookup uses the same root as homeDir. No-op on macOS/Linux.
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home)
	srv.SetConnectService(svc)

	cfgPath := connect.ConfigPath("opencode", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"mcp":{"proxy-alt":{"type":"remote","url":"http://127.0.0.1:8080/mcp"}}}`), 0o644))

	body, _ := json.Marshal(ConnectRequest{ServerName: "mcpproxy", Force: false})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/opencode", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Success bool                  `json:"success"`
		Data    connect.ConnectResult `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Success)
	assert.Equal(t, "already_exists", resp.Data.Action)
	assert.Equal(t, "proxy-alt", resp.Data.ServerName)
}

func TestHandleConnectClient_OpenCodeMissingConfigReturns400(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	// Pin %LOCALAPPDATA% under the test temp dir so OpenCode's Windows
	// config-path lookup uses the same root as homeDir. No-op on macOS/Linux.
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	srv.SetConnectService(connect.NewServiceWithHome("127.0.0.1:8080", "", home))

	body, _ := json.Marshal(ConnectRequest{ServerName: "mcpproxy", Force: false})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/opencode", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "does not exist")
}

// TestHandleGetConnectStatus_IncludesAccessStateUnknown asserts the overall
// GET /connect payload is additive: each entry carries access_state, set to
// "unknown" by the content-read-free overall status (Spec 075 T025, contract).
func TestHandleGetConnectStatus_IncludesAccessStateUnknown(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	srv.SetConnectService(connect.NewServiceWithHome("127.0.0.1:8080", "", home))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/connect", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Success bool                   `json:"success"`
		Data    []connect.ClientStatus `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Success)
	require.NotEmpty(t, resp.Data)
	for _, st := range resp.Data {
		assert.Equal(t, "unknown", st.AccessState, "overall status must not content-read client %s", st.ID)
		assert.Empty(t, st.Remediation)
	}
}

// TestHandleGetConnectClientStatus_Connected asserts the on-demand per-client
// route resolves access_state=accessible and connected=true after a real
// connect (Spec 075 T025, contract GET /connect/{client}).
func TestHandleGetConnectClientStatus_Connected(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home)
	srv.SetConnectService(svc)

	_, err := svc.Connect("claude-code", "mcpproxy", false)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/connect/claude-code", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Success bool                 `json:"success"`
		Data    connect.ClientStatus `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Success)
	assert.Equal(t, "claude-code", resp.Data.ID)
	assert.True(t, resp.Data.Connected)
	assert.Equal(t, "accessible", resp.Data.AccessState)
}

// TestHandleGetConnectClientStatus_Absent asserts the on-demand route reports
// access_state=absent for a client with no config file present.
func TestHandleGetConnectClientStatus_Absent(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	srv.SetConnectService(connect.NewServiceWithHome("127.0.0.1:8080", "", home))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/connect/claude-code", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Success bool                 `json:"success"`
		Data    connect.ClientStatus `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Success)
	assert.False(t, resp.Data.Connected)
	assert.Equal(t, "absent", resp.Data.AccessState)
}

// TestHandleGetConnectClientStatus_UnknownClient asserts an unknown client id
// yields 404 (not a 200 with an empty body).
func TestHandleGetConnectClientStatus_UnknownClient(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	srv.SetConnectService(connect.NewServiceWithHome("127.0.0.1:8080", "", t.TempDir()))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/connect/not-a-real-client", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleGetConnectClientStatus_DeniedSurfacesRemediation asserts that a
// macOS App-Data block on the on-demand read resolves access_state=denied and
// carries remediation text in the 200 body (Spec 075 contract).
func TestHandleGetConnectClientStatus_DeniedSurfacesRemediation(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()

	// Config must stat-exist so the on-demand content read is attempted; the
	// injected reader then denies it.
	cfgPath := connect.ConfigPath("claude-code", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{}`), 0o644))

	svc := connect.NewServiceWithReader("127.0.0.1:8080", "", home, denyingReader)
	srv.SetConnectService(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/connect/claude-code", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Success bool                 `json:"success"`
		Data    connect.ClientStatus `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Success)
	assert.False(t, resp.Data.Connected, "denied must not read as plain not-connected")
	assert.Equal(t, "denied", resp.Data.AccessState)
	assert.Contains(t, resp.Data.Remediation, "tccutil reset SystemPolicyAppData")
}

// TestHandleConnectClient_DeniedReturnsRemediation asserts a permission-denied
// write surfaces remediation in the HTTP error body, distinct from a generic
// 400 or a 404 not-found (Spec 075 contract POST /connect/{client}).
func TestHandleConnectClient_DeniedReturnsRemediation(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	svc := connect.NewServiceWithReader("127.0.0.1:8080", "", home, denyingReader)
	srv.SetConnectService(svc)

	body, _ := json.Marshal(ConnectRequest{ServerName: "mcpproxy", Force: false})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/claude-code", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "tccutil reset SystemPolicyAppData")
}

func TestHandleConnectClient_TrueConflictStillReturns409(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home)
	srv.SetConnectService(svc)

	_, err := svc.Connect("claude-code", "mcpproxy", false)
	require.NoError(t, err)

	body, _ := json.Marshal(ConnectRequest{ServerName: "mcpproxy", Force: false})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/claude-code", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)

	var resp struct {
		Success bool                  `json:"success"`
		Data    connect.ConnectResult `json:"data"`
		Error   string                `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.Success)
	assert.Equal(t, "already_exists", resp.Data.Action)
	assert.NotEmpty(t, resp.Error)
}

// TestHandleConnectClientPreview_MaskedNoSideEffects exercises the Spec 078 US1
// preview endpoint end-to-end: it returns the exact entry a connect would write
// with the API key masked, does not modify the config, and creates no backup.
func TestHandleConnectClientPreview_MaskedNoSideEffects(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()

	cfgPath := connect.ConfigPath("claude-code", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	original := []byte(`{"mcpServers":{"other":{"url":"http://x"}}}`)
	require.NoError(t, os.WriteFile(cfgPath, original, 0o644))

	const secret = "rest-secret-key-9999"
	// require_mcp_auth on so a credential is written (masked in the preview).
	svc := connect.NewServiceWithHome("127.0.0.1:8080", secret, home).WithRequireMCPAuth(true)
	srv.SetConnectService(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/connect/claude-code/preview", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	rawBody := w.Body.Bytes()
	assert.NotContains(t, string(rawBody), secret, "real API key must not appear in the preview payload")

	var resp struct {
		Success bool                   `json:"success"`
		Data    connect.ConnectPreview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rawBody, &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, cfgPath, resp.Data.ConfigPath)
	assert.Equal(t, "mcpServers", resp.Data.ServerKey)
	assert.Equal(t, "mcpproxy", resp.Data.ServerName)
	assert.True(t, resp.Data.ContainsAPIKey)
	assert.False(t, resp.Data.EntryExists)
	assert.Equal(t, "accessible", resp.Data.AccessState)
	assert.Contains(t, resp.Data.EntryText, "http://127.0.0.1:8080/mcp")
	// claude-code carries the masked credential in a header, not the URL.
	assert.Contains(t, resp.Data.EntryText, "X-API-Key")
	assert.NotContains(t, resp.Data.EntryText, "apikey=")

	// No write, no backup.
	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(original), string(after))
	entries, err := os.ReadDir(filepath.Dir(cfgPath))
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".bak.", "preview must not create a backup")
	}
}

// TestHandleConnectClientPreview_HonorsServerName asserts the preview endpoint
// previews the exact entry name a subsequent POST connect (which accepts
// server_name) will write, instead of always defaulting to "mcpproxy" — so a
// caller previewing then connecting under a custom name does not diverge
// (Spec 078 FR-002).
func TestHandleConnectClientPreview_HonorsServerName(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()

	cfgPath := connect.ConfigPath("claude-code", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	// Pre-existing entry named "custom" so entry_exists reflects THIS name.
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"mcpServers":{"custom":{"url":"http://x"}}}`), 0o644))

	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home)
	srv.SetConnectService(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/connect/claude-code/preview?server_name=custom", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Success bool                   `json:"success"`
		Data    connect.ConnectPreview `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Success)
	assert.Equal(t, "custom", resp.Data.ServerName)
	assert.True(t, resp.Data.EntryExists, "entry_exists must reflect the requested server_name")
}

// TestHandleConnectClientPreview_DeniedReturns403 asserts a permission-denied
// config read during preview surfaces 403 + remediation, matching connect.
func TestHandleConnectClientPreview_DeniedReturns403(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()

	cfgPath := connect.ConfigPath("claude-code", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{}`), 0o644))

	svc := connect.NewServiceWithReader("127.0.0.1:8080", "", home, denyingReader)
	srv.SetConnectService(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/connect/claude-code/preview", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "tccutil reset SystemPolicyAppData")
}

// ---- Spec 078 US3: POST /api/v1/connect/{client}/undo ----

// TestHandleUndoConnectClient_RestoresFile exercises the happy path end-to-end:
// connect (force-overwriting a user entry), then undo with the backup_path the
// connect returned — the config must come back byte-identical.
func TestHandleUndoConnectClient_RestoresFile(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home)
	srv.SetConnectService(svc)

	cfgPath := connect.ConfigPath("claude-code", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	original := []byte(`{"mcpServers":{"mcpproxy":{"url":"http://user-owned/mcp"}}}`)
	require.NoError(t, os.WriteFile(cfgPath, original, 0o644))

	res, err := svc.Connect("claude-code", "mcpproxy", true)
	require.NoError(t, err)
	require.NotEmpty(t, res.BackupPath)

	body, _ := json.Marshal(UndoConnectRequest{ServerName: "mcpproxy", BackupPath: res.BackupPath})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/claude-code/undo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Success bool                  `json:"success"`
		Data    connect.ConnectResult `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Success)
	assert.Equal(t, "restored", resp.Data.Action)
	assert.NotEmpty(t, resp.Data.BackupPath, "undo must report its safety backup")

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(original), string(after), "config must be byte-identical to pre-connect state")
}

// TestHandleUndoConnectClient_ConflictWhenDrifted asserts a 409 (and no file
// mutation) when the config changed after the connect.
func TestHandleUndoConnectClient_ConflictWhenDrifted(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home)
	srv.SetConnectService(svc)

	res, err := svc.Connect("claude-code", "mcpproxy", false)
	require.NoError(t, err)
	cfgPath := res.ConfigPath
	drifted := []byte(`{"mcpServers":{"mcpproxy":{"type":"http","url":"http://127.0.0.1:8080/mcp"},"mine":{"url":"http://y"}}}`)
	require.NoError(t, os.WriteFile(cfgPath, drifted, 0o644))

	body, _ := json.Marshal(UndoConnectRequest{ServerName: "mcpproxy", BackupPath: res.BackupPath})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/claude-code/undo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	var resp struct {
		Success bool                  `json:"success"`
		Data    connect.ConnectResult `json:"data"`
		Error   string                `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.Success)
	assert.Equal(t, "conflict", resp.Data.Action)
	assert.NotEmpty(t, resp.Error)

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(drifted), string(after), "refused undo must not touch the file")
}

// TestHandleUndoConnectClient_MissingBackupReturns404 asserts a vanished backup
// maps to 404 like the other not_found results.
func TestHandleUndoConnectClient_MissingBackupReturns404(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home)
	srv.SetConnectService(svc)

	res, err := svc.Connect("claude-code", "mcpproxy", false)
	require.NoError(t, err)

	body, _ := json.Marshal(UndoConnectRequest{BackupPath: res.ConfigPath + ".bak.19990101-000000"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/claude-code/undo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleUndoConnectClient_DeniedReturns403 mirrors the other per-client
// connect routes: a macOS App-Data (TCC) denial surfaces as 403 + remediation.
func TestHandleUndoConnectClient_DeniedReturns403(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()

	cfgPath := connect.ConfigPath("claude-code", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{}`), 0o644))
	require.NoError(t, os.WriteFile(cfgPath+".bak.20260702-000000", []byte(`{}`), 0o644))

	svc := connect.NewServiceWithReader("127.0.0.1:8080", "", home, denyingReader)
	srv.SetConnectService(svc)

	body, _ := json.Marshal(UndoConnectRequest{BackupPath: cfgPath + ".bak.20260702-000000"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/claude-code/undo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "tccutil reset SystemPolicyAppData")
}

// TestHandleUndoConnectClient_UnknownClient yields 404, not a 200/empty body.
func TestHandleUndoConnectClient_UnknownClient(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	srv.SetConnectService(connect.NewServiceWithHome("127.0.0.1:8080", "", t.TempDir()))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/not-a-real-client/undo", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleUndoConnectClient_NoPriorFileDeletes covers the created-file case:
// undo with an empty backup_path removes the file connect created.
func TestHandleUndoConnectClient_NoPriorFileDeletes(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home)
	srv.SetConnectService(svc)

	res, err := svc.Connect("claude-code", "mcpproxy", false)
	require.NoError(t, err)
	require.Empty(t, res.BackupPath, "no prior file: connect returns no backup")

	body, _ := json.Marshal(UndoConnectRequest{ServerName: "mcpproxy", BackupPath: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/claude-code/undo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Success bool                  `json:"success"`
		Data    connect.ConnectResult `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Success)
	assert.Equal(t, "deleted", resp.Data.Action)
	_, statErr := os.Stat(res.ConfigPath)
	assert.True(t, os.IsNotExist(statErr), "config created by connect must be removed")
}
