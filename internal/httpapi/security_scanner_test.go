package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// --- Mock SecurityController ---

type mockSecurityController struct {
	scanners  []*scanner.ScannerPlugin
	scanJob   *scanner.ScanJob
	report    *scanner.AggregatedReport
	overview  *scanner.SecurityOverview
	integrity *scanner.IntegrityCheckResult

	installErr   error
	removeErr    error
	configureErr error
	startScanErr error
	cancelErr    error
	approveErr   error
	rejectErr    error
	integrityErr error
}

func (m *mockSecurityController) ListScanners(_ context.Context) ([]*scanner.ScannerPlugin, error) {
	return m.scanners, nil
}

func (m *mockSecurityController) InstallScanner(_ context.Context, id string) error {
	return m.installErr
}

func (m *mockSecurityController) RemoveScanner(_ context.Context, id string) error {
	return m.removeErr
}

func (m *mockSecurityController) ConfigureScanner(_ context.Context, id string, env map[string]string) error {
	return m.configureErr
}

func (m *mockSecurityController) GetScannerStatus(_ context.Context, id string) (*scanner.ScannerPlugin, error) {
	for _, s := range m.scanners {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("scanner not found: %s", id)
}

func (m *mockSecurityController) StartScan(_ context.Context, serverName string, dryRun bool, scannerIDs []string, sourceDir string) (*scanner.ScanJob, error) {
	if m.startScanErr != nil {
		return nil, m.startScanErr
	}
	if m.scanJob != nil {
		return m.scanJob, nil
	}
	return &scanner.ScanJob{
		ID:         "scan-test-123",
		ServerName: serverName,
		Status:     scanner.ScanJobStatusRunning,
		Scanners:   scannerIDs,
		StartedAt:  time.Now(),
		DryRun:     dryRun,
	}, nil
}

func (m *mockSecurityController) GetScanStatus(_ context.Context, serverName string) (*scanner.ScanJob, error) {
	if m.scanJob != nil {
		return m.scanJob, nil
	}
	return nil, fmt.Errorf("no scan found for server: %s", serverName)
}

func (m *mockSecurityController) GetScanReport(_ context.Context, serverName string) (*scanner.AggregatedReport, error) {
	if m.report != nil {
		return m.report, nil
	}
	return nil, fmt.Errorf("no report found for server: %s", serverName)
}

func (m *mockSecurityController) CancelScan(_ context.Context, serverName string) error {
	return m.cancelErr
}

func (m *mockSecurityController) ApproveServer(_ context.Context, serverName string, force bool, approvedBy string) error {
	return m.approveErr
}

func (m *mockSecurityController) RejectServer(_ context.Context, serverName string) error {
	return m.rejectErr
}

func (m *mockSecurityController) CheckIntegrity(_ context.Context, serverName string) (*scanner.IntegrityCheckResult, error) {
	if m.integrityErr != nil {
		return nil, m.integrityErr
	}
	if m.integrity != nil {
		return m.integrity, nil
	}
	return &scanner.IntegrityCheckResult{
		ServerName: serverName,
		Passed:     true,
		CheckedAt:  time.Now(),
	}, nil
}

func (m *mockSecurityController) GetSecurityOverview(_ context.Context) (*scanner.SecurityOverview, error) {
	if m.overview != nil {
		return m.overview, nil
	}
	return &scanner.SecurityOverview{}, nil
}

// secTestController embeds baseController and adds GetCurrentConfig
// returning nil to bypass auth middleware in tests.
type secTestController struct {
	baseController
}

func (m *secTestController) GetCurrentConfig() interface{} {
	return nil // nil config = testing scenario, bypasses auth
}

// helper to create a test server with security controller

// secParseData extracts the "data" field from an APIResponse wrapper {success: true, data: ...}
func secParseData(t *testing.T, body *bytes.Buffer, target interface{}) {
	t.Helper()
	var wrapper struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	err := json.NewDecoder(body).Decode(&wrapper)
	require.NoError(t, err)
	assert.True(t, wrapper.Success)
	if target != nil && wrapper.Data != nil {
		require.NoError(t, json.Unmarshal(wrapper.Data, target))
	}
}

func newTestServerWithSecurity(t *testing.T, secCtrl SecurityController) *Server {
	t.Helper()
	logger := zap.NewNop().Sugar()
	ctrl := &secTestController{}
	srv := NewServer(ctrl, logger, nil)
	srv.SetSecurityController(secCtrl)
	// Re-setup routes after setting the controller
	srv.router = chi.NewRouter()
	srv.setupRoutes()
	return srv
}

func TestSecurityHandlerListScanners(t *testing.T) {
	secCtrl := &mockSecurityController{
		scanners: []*scanner.ScannerPlugin{
			{ID: "mcp-scan", Name: "MCP Scan", Status: scanner.ScannerStatusInstalled},
			{ID: "trivy", Name: "Trivy", Status: scanner.ScannerStatusAvailable},
		},
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("GET", "/api/v1/security/scanners", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var scanners []*scanner.ScannerPlugin
	secParseData(t, w.Body, &scanners)
	assert.Len(t, scanners, 2)
	assert.Equal(t, "mcp-scan", scanners[0].ID)
}

func TestSecurityHandlerInstallScanner(t *testing.T) {
	secCtrl := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, secCtrl)

	body := bytes.NewBufferString(`{"id": "mcp-scan"}`)
	req := httptest.NewRequest("POST", "/api/v1/security/scanners/install", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	secParseData(t, w.Body, &resp)
	assert.Equal(t, "installed", resp["status"])
	assert.Equal(t, "mcp-scan", resp["id"])
}

func TestSecurityHandlerInstallScannerError(t *testing.T) {
	secCtrl := &mockSecurityController{
		installErr: fmt.Errorf("Docker is not available"),
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	body := bytes.NewBufferString(`{"id": "mcp-scan"}`)
	req := httptest.NewRequest("POST", "/api/v1/security/scanners/install", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestSecurityHandlerInstallScannerMissingID(t *testing.T) {
	secCtrl := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, secCtrl)

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest("POST", "/api/v1/security/scanners/install", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSecurityHandlerRemoveScanner(t *testing.T) {
	secCtrl := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("DELETE", "/api/v1/security/scanners/mcp-scan", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	secParseData(t, w.Body, &resp)
	assert.Equal(t, "removed", resp["status"])
}

func TestSecurityHandlerConfigureScanner(t *testing.T) {
	secCtrl := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, secCtrl)

	body := bytes.NewBufferString(`{"env": {"API_KEY": "test-key"}}`)
	req := httptest.NewRequest("PUT", "/api/v1/security/scanners/mcp-scan/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	secParseData(t, w.Body, &resp)
	assert.Equal(t, "configured", resp["status"])
}

func TestSecurityHandlerConfigureScannerEmptyEnv(t *testing.T) {
	secCtrl := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, secCtrl)

	body := bytes.NewBufferString(`{"env": {}}`)
	req := httptest.NewRequest("PUT", "/api/v1/security/scanners/mcp-scan/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSecurityHandlerGetScannerStatus(t *testing.T) {
	secCtrl := &mockSecurityController{
		scanners: []*scanner.ScannerPlugin{
			{ID: "mcp-scan", Name: "MCP Scan", Status: scanner.ScannerStatusInstalled},
		},
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("GET", "/api/v1/security/scanners/mcp-scan/status", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var sc scanner.ScannerPlugin
	secParseData(t, w.Body, &sc)
	assert.Equal(t, "mcp-scan", sc.ID)
	assert.Equal(t, scanner.ScannerStatusInstalled, sc.Status)
}

func TestSecurityHandlerGetScannerStatusNotFound(t *testing.T) {
	secCtrl := &mockSecurityController{
		scanners: []*scanner.ScannerPlugin{},
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("GET", "/api/v1/security/scanners/nonexistent/status", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSecurityHandlerStartScan(t *testing.T) {
	secCtrl := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, secCtrl)

	body := bytes.NewBufferString(`{"dry_run": true, "scanner_ids": ["mcp-scan"]}`)
	req := httptest.NewRequest("POST", "/api/v1/servers/my-server/scan", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)

	var job scanner.ScanJob
	secParseData(t, w.Body, &job)
	assert.Equal(t, "my-server", job.ServerName)
	assert.True(t, job.DryRun)
}

func TestSecurityHandlerStartScanError(t *testing.T) {
	secCtrl := &mockSecurityController{
		startScanErr: fmt.Errorf("no scanners installed"),
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("POST", "/api/v1/servers/my-server/scan", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestSecurityHandlerGetScanStatus(t *testing.T) {
	secCtrl := &mockSecurityController{
		scanJob: &scanner.ScanJob{
			ID:         "scan-123",
			ServerName: "my-server",
			Status:     scanner.ScanJobStatusCompleted,
			Scanners:   []string{"mcp-scan"},
			StartedAt:  time.Now(),
		},
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("GET", "/api/v1/servers/my-server/scan/status", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var job scanner.ScanJob
	secParseData(t, w.Body, &job)
	assert.Equal(t, "scan-123", job.ID)
	assert.Equal(t, scanner.ScanJobStatusCompleted, job.Status)
}

func TestSecurityHandlerGetScanStatusNotFound(t *testing.T) {
	secCtrl := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("GET", "/api/v1/servers/no-such-server/scan/status", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSecurityHandlerGetScanReport(t *testing.T) {
	secCtrl := &mockSecurityController{
		report: &scanner.AggregatedReport{
			JobID:      "scan-123",
			ServerName: "my-server",
			Findings: []scanner.ScanFinding{
				{RuleID: "R1", Severity: scanner.SeverityHigh, Title: "High issue"},
			},
			Summary: scanner.ReportSummary{High: 1, Total: 1},
		},
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("GET", "/api/v1/servers/my-server/scan/report", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var report scanner.AggregatedReport
	secParseData(t, w.Body, &report)
	assert.Equal(t, "scan-123", report.JobID)
	assert.Len(t, report.Findings, 1)
	assert.Equal(t, 1, report.Summary.High)
}

func TestSecurityHandlerCancelScan(t *testing.T) {
	secCtrl := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("POST", "/api/v1/servers/my-server/scan/cancel", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSecurityHandlerCancelScanError(t *testing.T) {
	secCtrl := &mockSecurityController{
		cancelErr: fmt.Errorf("no active scan"),
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("POST", "/api/v1/servers/my-server/scan/cancel", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestSecurityHandlerApproveServer(t *testing.T) {
	secCtrl := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, secCtrl)

	body := bytes.NewBufferString(`{"force": false}`)
	req := httptest.NewRequest("POST", "/api/v1/servers/my-server/security/approve", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	secParseData(t, w.Body, &resp)
	assert.Equal(t, "approved", resp["status"])
}

func TestSecurityHandlerApproveServerBlocked(t *testing.T) {
	secCtrl := &mockSecurityController{
		approveErr: fmt.Errorf("server has 2 critical findings"),
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("POST", "/api/v1/servers/my-server/security/approve", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestSecurityHandlerRejectServer(t *testing.T) {
	secCtrl := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("POST", "/api/v1/servers/my-server/security/reject", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	secParseData(t, w.Body, &resp)
	assert.Equal(t, "rejected", resp["status"])
}

func TestSecurityHandlerCheckIntegrity(t *testing.T) {
	secCtrl := &mockSecurityController{
		integrity: &scanner.IntegrityCheckResult{
			ServerName: "my-server",
			Passed:     true,
			CheckedAt:  time.Now(),
		},
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("GET", "/api/v1/servers/my-server/integrity", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result scanner.IntegrityCheckResult
	secParseData(t, w.Body, &result)
	assert.True(t, result.Passed)
}

func TestSecurityHandlerCheckIntegrityNoBaseline(t *testing.T) {
	secCtrl := &mockSecurityController{
		integrityErr: fmt.Errorf("no integrity baseline"),
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("GET", "/api/v1/servers/my-server/integrity", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSecurityHandlerOverview(t *testing.T) {
	secCtrl := &mockSecurityController{
		overview: &scanner.SecurityOverview{
			TotalScans:        5,
			ActiveScans:       1,
			ScannersInstalled: 2,
			ServersScanned:    3,
			FindingsBySeverity: scanner.ReportSummary{
				Critical: 1,
				High:     3,
				Medium:   5,
				Total:    9,
			},
		},
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	req := httptest.NewRequest("GET", "/api/v1/security/overview", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var overview scanner.SecurityOverview
	secParseData(t, w.Body, &overview)
	assert.Equal(t, 5, overview.TotalScans)
	assert.Equal(t, 1, overview.ActiveScans)
	assert.Equal(t, 2, overview.ScannersInstalled)
	assert.Equal(t, 1, overview.FindingsBySeverity.Critical)
}

func TestSecurityRoutesReturnNotImplementedWithoutController(t *testing.T) {
	logger := zap.NewNop().Sugar()
	ctrl := &secTestController{}
	srv := NewServer(ctrl, logger, nil)
	// Do NOT set security controller

	req := httptest.NewRequest("GET", "/api/v1/security/scanners", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	// Should be 501 since security controller is not configured
	assert.Equal(t, http.StatusNotImplemented, w.Code)
}
