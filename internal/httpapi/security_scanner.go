package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// SecurityController defines the interface for security scanner operations (Spec 039).
type SecurityController interface {
	ListScanners(ctx context.Context) ([]*scanner.ScannerPlugin, error)
	InstallScanner(ctx context.Context, id string) error
	RemoveScanner(ctx context.Context, id string) error
	ConfigureScanner(ctx context.Context, id string, env map[string]string) error
	GetScannerStatus(ctx context.Context, id string) (*scanner.ScannerPlugin, error)

	StartScan(ctx context.Context, serverName string, dryRun bool, scannerIDs []string, sourceDir string) (*scanner.ScanJob, error)
	GetScanStatus(ctx context.Context, serverName string) (*scanner.ScanJob, error)
	GetScanReport(ctx context.Context, serverName string) (*scanner.AggregatedReport, error)
	CancelScan(ctx context.Context, serverName string) error

	ApproveServer(ctx context.Context, serverName string, force bool, approvedBy string) error
	RejectServer(ctx context.Context, serverName string) error
	CheckIntegrity(ctx context.Context, serverName string) (*scanner.IntegrityCheckResult, error)

	GetSecurityOverview(ctx context.Context) (*scanner.SecurityOverview, error)
}

// SetSecurityController configures the security scanner controller on the server.
// This must be called after NewServer and before serving requests
// to enable the /api/v1/security endpoints.
func (s *Server) SetSecurityController(ctrl SecurityController) {
	s.securityController = ctrl
}

// requireSecurity returns true if the security controller is available, writing a 501 error if not.
func (s *Server) requireSecurity(w http.ResponseWriter, r *http.Request) bool {
	if s.securityController == nil {
		s.writeError(w, r, http.StatusNotImplemented, "security scanner feature is not configured")
		return false
	}
	return true
}

// --- Scanner management handlers ---

func (s *Server) handleListScanners(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	scanners, err := s.securityController.ListScanners(r.Context())
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, scanners)
}

func (s *Server) handleInstallScanner(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID == "" {
		s.writeError(w, r, http.StatusBadRequest, "scanner id is required")
		return
	}

	if err := s.securityController.InstallScanner(r.Context(), req.ID); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "installed", "id": req.ID})
}

func (s *Server) handleRemoveScanner(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		s.writeError(w, r, http.StatusBadRequest, "scanner id is required")
		return
	}

	if err := s.securityController.RemoveScanner(r.Context(), id); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "id": id})
}

func (s *Server) handleConfigureScanner(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		s.writeError(w, r, http.StatusBadRequest, "scanner id is required")
		return
	}

	var req struct {
		Env map[string]string `json:"env"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Env) == 0 {
		s.writeError(w, r, http.StatusBadRequest, "env map is required")
		return
	}

	if err := s.securityController.ConfigureScanner(r.Context(), id, req.Env); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "configured", "id": id})
}

func (s *Server) handleGetScannerStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		s.writeError(w, r, http.StatusBadRequest, "scanner id is required")
		return
	}

	sc, err := s.securityController.GetScannerStatus(r.Context(), id)
	if err != nil {
		s.writeError(w, r, http.StatusNotFound, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, sc)
}

// --- Scan operation handlers ---

func (s *Server) handleStartScan(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	name := chi.URLParam(r, "id")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "server name is required")
		return
	}

	var req struct {
		DryRun     bool     `json:"dry_run"`
		ScannerIDs []string `json:"scanner_ids"`
		SourceDir  string   `json:"source_dir"`
	}
	// Body is optional for simple scans
	_ = json.NewDecoder(r.Body).Decode(&req)

	job, err := s.securityController.StartScan(r.Context(), name, req.DryRun, req.ScannerIDs, req.SourceDir)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleGetScanStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	name := chi.URLParam(r, "id")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "server name is required")
		return
	}

	job, err := s.securityController.GetScanStatus(r.Context(), name)
	if err != nil {
		s.writeError(w, r, http.StatusNotFound, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleGetScanReport(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	name := chi.URLParam(r, "id")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "server name is required")
		return
	}

	report, err := s.securityController.GetScanReport(r.Context(), name)
	if err != nil {
		s.writeError(w, r, http.StatusNotFound, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleCancelScan(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	name := chi.URLParam(r, "id")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "server name is required")
		return
	}

	if err := s.securityController.CancelScan(r.Context(), name); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled", "server_name": name})
}

// --- Approval handlers ---

func (s *Server) handleSecurityApprove(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	name := chi.URLParam(r, "id")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "server name is required")
		return
	}

	var req struct {
		Force bool `json:"force"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	// Use "api" as the approver since we don't have user context in personal edition
	if err := s.securityController.ApproveServer(r.Context(), name, req.Force, "api"); err != nil {
		s.writeError(w, r, http.StatusConflict, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "approved", "server_name": name})
}

func (s *Server) handleSecurityReject(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	name := chi.URLParam(r, "id")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "server name is required")
		return
	}

	if err := s.securityController.RejectServer(r.Context(), name); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "rejected", "server_name": name})
}

func (s *Server) handleCheckIntegrity(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	name := chi.URLParam(r, "id")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "server name is required")
		return
	}

	result, err := s.securityController.CheckIntegrity(r.Context(), name)
	if err != nil {
		s.writeError(w, r, http.StatusNotFound, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

// --- Overview handler ---

func (s *Server) handleSecurityOverview(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	overview, err := s.securityController.GetSecurityOverview(r.Context())
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, overview)
}
