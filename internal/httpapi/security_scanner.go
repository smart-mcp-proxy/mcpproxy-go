package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// SecurityController defines the interface for security scanner operations (Spec 039).
type SecurityController interface {
	ListScanners(ctx context.Context) ([]*scanner.ScannerPlugin, error)
	InstallScanner(ctx context.Context, id string) error
	RemoveScanner(ctx context.Context, id string) error
	ConfigureScanner(ctx context.Context, id string, env map[string]string, dockerImage string) error
	GetScannerStatus(ctx context.Context, id string) (*scanner.ScannerPlugin, error)

	StartScan(ctx context.Context, serverName string, dryRun bool, scannerIDs []string, sourceDir string) (*scanner.ScanJob, error)
	GetScanStatus(ctx context.Context, serverName string) (*scanner.ScanJob, error)
	GetScanStatusByPass(ctx context.Context, serverName string, pass int) (*scanner.ScanJob, error)
	GetScanReport(ctx context.Context, serverName string) (*scanner.AggregatedReport, error)
	CancelScan(ctx context.Context, serverName string) error

	ApproveServer(ctx context.Context, serverName string, force bool, approvedBy string) error
	RejectServer(ctx context.Context, serverName string) error
	CheckIntegrity(ctx context.Context, serverName string) (*scanner.IntegrityCheckResult, error)

	GetSecurityOverview(ctx context.Context) (*scanner.SecurityOverview, error)
	GetScanSummary(ctx context.Context, serverName string) *scanner.ScanSummary

	// Batch scan operations
	ScanAll(ctx context.Context, servers []scanner.ServerStatus, scannerIDs []string) (*scanner.QueueProgress, error)
	GetQueueProgress() *scanner.QueueProgress
	CancelAllScans() error
	IsQueueRunning() bool

	// Scan history
	ListScanHistory(ctx context.Context) ([]scanner.ScanJobSummary, error)
	GetScanReportByJobID(ctx context.Context, jobID string) (*scanner.AggregatedReport, error)
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
	s.writeSuccess(w, scanners)
}

func (s *Server) handleInstallScanner(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}

	// Accept scanner ID from URL path (new /enable endpoint) or request body (legacy /install)
	id := chi.URLParam(r, "id")
	if id == "" {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, r, http.StatusBadRequest, "invalid request body")
			return
		}
		id = req.ID
	}
	if id == "" {
		s.writeError(w, r, http.StatusBadRequest, "scanner id is required")
		return
	}

	if err := s.securityController.InstallScanner(r.Context(), id); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeSuccess(w, map[string]string{"status": "enabled", "id": id})
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
	s.writeSuccess(w, map[string]string{"status": "disabled", "id": id})
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
		Env         map[string]string `json:"env"`
		DockerImage string            `json:"docker_image,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Env) == 0 && req.DockerImage == "" {
		s.writeError(w, r, http.StatusBadRequest, "env map or docker_image is required")
		return
	}

	if err := s.securityController.ConfigureScanner(r.Context(), id, req.Env, req.DockerImage); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeSuccess(w, map[string]string{"status": "configured", "id": id})
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
	s.writeSuccess(w, sc)
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
		if strings.Contains(err.Error(), "already in progress") {
			s.writeError(w, r, http.StatusConflict, err.Error())
		} else {
			s.writeError(w, r, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.writeJSON(w, http.StatusAccepted, contracts.NewSuccessResponse(job))
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
	s.writeSuccess(w, job)
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

	// Strip sarif_raw from response unless ?include_sarif=true (it can be 2MB+)
	if r.URL.Query().Get("include_sarif") != "true" {
		for i := range report.Reports {
			report.Reports[i].SarifRaw = nil
		}
	}

	s.writeSuccess(w, report)
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
	s.writeSuccess(w, map[string]string{"status": "cancelled", "server_name": name})
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
	s.writeSuccess(w, map[string]string{"status": "approved", "server_name": name})
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
	s.writeSuccess(w, map[string]string{"status": "rejected", "server_name": name})
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
	s.writeSuccess(w, result)
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
	s.writeSuccess(w, overview)
}

func (s *Server) handleGetScanFiles(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	name := chi.URLParam(r, "id")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "server name is required")
		return
	}

	// Pagination: ?limit=100&offset=0&suspicious_only=true&pass=1
	limit := 100
	offset := 0
	suspiciousOnly := false
	pass := 0 // 0 = latest, 1 = security scan, 2 = supply chain
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	if r.URL.Query().Get("suspicious_only") == "true" {
		suspiciousOnly = true
	}
	if p := r.URL.Query().Get("pass"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && (parsed == 1 || parsed == 2) {
			pass = parsed
		}
	}

	// Get scan status for the requested pass (default: pass 1 for consistency with scan context header)
	if pass == 0 {
		pass = 1 // Default to Pass 1 (security scan) to match the scan context shown in the UI
	}
	job, err := s.securityController.GetScanStatusByPass(r.Context(), name, pass)
	if err != nil {
		s.writeError(w, r, http.StatusNotFound, err.Error())
		return
	}

	// Get report for finding locations
	report, _ := s.securityController.GetScanReport(r.Context(), name)

	// Build file tree with suspicious markers and pagination
	result := buildFileTreePaginated(job, report, limit, offset, suspiciousOnly)
	s.writeSuccess(w, result)
}

// fileTreeEntry represents a file in the scanned directory tree
type fileTreeEntry struct {
	Path       string   `json:"path"`
	Suspicious bool     `json:"suspicious"`
	Findings   []string `json:"findings,omitempty"` // Finding titles for this file
}

type fileTreeResponse struct {
	SourceMethod    string          `json:"source_method"`
	SourcePath      string          `json:"source_path"`
	DockerIsolation bool            `json:"docker_isolation"`
	TotalFiles      int             `json:"total_files"`
	TotalSizeBytes  int64           `json:"total_size_bytes"`
	SuspiciousCount int             `json:"suspicious_count"`
	Files           []fileTreeEntry `json:"files"`
	Offset          int             `json:"offset"`
	Limit           int             `json:"limit"`
	HasMore         bool            `json:"has_more"`
}

func buildFileTreePaginated(job *scanner.ScanJob, report *scanner.AggregatedReport, limit, offset int, suspiciousOnly bool) *fileTreeResponse {
	resp := &fileTreeResponse{Limit: limit, Offset: offset}

	if job == nil || job.ScanContext == nil {
		return resp
	}

	ctx := job.ScanContext
	resp.SourceMethod = ctx.SourceMethod
	resp.SourcePath = ctx.SourcePath
	resp.DockerIsolation = ctx.DockerIsolation
	resp.TotalFiles = ctx.TotalFiles
	resp.TotalSizeBytes = ctx.TotalSizeBytes

	// Build location-to-findings lookup from report
	locationFindings := make(map[string][]string)
	if report != nil {
		for _, f := range report.Findings {
			if f.Location != "" {
				filePath := f.Location
				if idx := lastIndexByte(filePath, ':'); idx > 0 {
					filePath = filePath[:idx]
				}
				filePath = trimScanPrefix(filePath)
				locationFindings[filePath] = append(locationFindings[filePath], f.Title)
			}
		}
	}

	// Build all entries first (for counting), then paginate
	// Suspicious files always sorted first
	var suspiciousFiles []fileTreeEntry
	var normalFiles []fileTreeEntry

	for _, path := range ctx.ScannedFiles {
		normalized := trimScanPrefix(path)
		entry := fileTreeEntry{Path: path}
		if findings, ok := locationFindings[normalized]; ok {
			entry.Suspicious = true
			entry.Findings = findings
			suspiciousFiles = append(suspiciousFiles, entry)
		} else {
			normalFiles = append(normalFiles, entry)
		}
	}

	resp.SuspiciousCount = len(suspiciousFiles)

	// Combine: suspicious first, then normal
	var allFiles []fileTreeEntry
	if suspiciousOnly {
		allFiles = suspiciousFiles
	} else {
		allFiles = append(suspiciousFiles, normalFiles...)
	}

	// Apply pagination
	totalFiltered := len(allFiles)
	if offset >= totalFiltered {
		resp.HasMore = false
		return resp
	}

	end := offset + limit
	if end > totalFiltered {
		end = totalFiltered
	}
	resp.Files = allFiles[offset:end]
	resp.HasMore = end < totalFiltered

	return resp
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func trimScanPrefix(path string) string {
	prefixes := []string{"/scan/source/", "scan/source/", "/src/"}
	for _, p := range prefixes {
		if len(path) > len(p) && path[:len(p)] == p {
			return path[len(p):]
		}
	}
	return path
}

// --- Batch scan handlers ---

func (s *Server) handleScanAll(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}

	// Parse optional request body
	var req struct {
		ScannerIDs []string `json:"scanner_ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	// Get server list from controller
	genericServers, err := s.controller.GetAllServers()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "failed to get server list: "+err.Error())
		return
	}

	// Build scanner.ServerStatus list
	var servers []scanner.ServerStatus
	for _, gs := range genericServers {
		name, _ := gs["name"].(string)
		if name == "" {
			continue
		}
		enabled := true
		if e, ok := gs["enabled"].(bool); ok {
			enabled = e
		}
		connected := false
		if c, ok := gs["connected"].(bool); ok {
			connected = c
		}
		protocol, _ := gs["protocol"].(string)
		servers = append(servers, scanner.ServerStatus{
			Name:      name,
			Enabled:   enabled,
			Connected: connected,
			Protocol:  protocol,
		})
	}

	progress, err := s.securityController.ScanAll(r.Context(), servers, req.ScannerIDs)
	if err != nil {
		s.writeError(w, r, http.StatusConflict, err.Error())
		return
	}
	s.writeJSON(w, http.StatusAccepted, contracts.NewSuccessResponse(progress))
}

func (s *Server) handleGetQueueProgress(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}

	progress := s.securityController.GetQueueProgress()
	if progress == nil {
		s.writeSuccess(w, map[string]interface{}{
			"status":  "idle",
			"message": "no batch scan in progress or completed",
		})
		return
	}
	s.writeSuccess(w, progress)
}

func (s *Server) handleCancelAllScans(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}

	if err := s.securityController.CancelAllScans(); err != nil {
		s.writeError(w, r, http.StatusConflict, err.Error())
		return
	}
	s.writeSuccess(w, map[string]string{"status": "cancelled"})
}

// --- Scan history handlers ---

func (s *Server) handleListScanHistory(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}

	summaries, err := s.securityController.ListScanHistory(r.Context())
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}

	// Sort
	sortField := r.URL.Query().Get("sort")
	order := r.URL.Query().Get("order")
	if order == "" {
		order = "desc"
	}

	sort.Slice(summaries, func(i, j int) bool {
		var less bool
		switch sortField {
		case "server_name":
			less = summaries[i].ServerName < summaries[j].ServerName
		case "status":
			less = summaries[i].Status < summaries[j].Status
		case "findings_count":
			less = summaries[i].FindingsCount < summaries[j].FindingsCount
		case "risk_score":
			less = summaries[i].RiskScore < summaries[j].RiskScore
		default: // started_at
			less = summaries[i].StartedAt.Before(summaries[j].StartedAt)
		}
		if order == "desc" {
			return !less
		}
		return less
	})

	// Status filter
	if statusFilter := r.URL.Query().Get("status"); statusFilter != "" {
		filtered := summaries[:0]
		for _, s := range summaries {
			if s.Status == statusFilter {
				filtered = append(filtered, s)
			}
		}
		summaries = filtered
	}

	// Pagination
	limit := 50
	offset := 0
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	if o, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && o >= 0 {
		offset = o
	}

	total := len(summaries)
	if offset >= len(summaries) {
		summaries = nil
	} else {
		end := offset + limit
		if end > len(summaries) {
			end = len(summaries)
		}
		summaries = summaries[offset:end]
	}

	s.writeSuccess(w, map[string]interface{}{
		"scans": summaries,
		"total": total,
	})
}

func (s *Server) handleGetScanReportByJobID(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	jobID := chi.URLParam(r, "jobId")
	if jobID == "" {
		s.writeError(w, r, http.StatusBadRequest, "job ID is required")
		return
	}

	report, err := s.securityController.GetScanReportByJobID(r.Context(), jobID)
	if err != nil {
		s.writeError(w, r, http.StatusNotFound, err.Error())
		return
	}

	// Strip sarif_raw unless requested
	if r.URL.Query().Get("include_sarif") != "true" {
		for i := range report.Reports {
			report.Reports[i].SarifRaw = nil
		}
	}

	s.writeSuccess(w, report)
}
