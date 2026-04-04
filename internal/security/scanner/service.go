package scanner

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Storage defines the storage interface needed by SecurityService
type Storage interface {
	SaveScanner(s *ScannerPlugin) error
	GetScanner(id string) (*ScannerPlugin, error)
	ListScanners() ([]*ScannerPlugin, error)
	DeleteScanner(id string) error

	SaveScanJob(job *ScanJob) error
	GetScanJob(id string) (*ScanJob, error)
	ListScanJobs(serverName string) ([]*ScanJob, error)
	GetLatestScanJob(serverName string) (*ScanJob, error)
	DeleteScanJob(id string) error
	DeleteServerScanJobs(serverName string) error

	SaveScanReport(report *ScanReport) error
	GetScanReport(id string) (*ScanReport, error)
	ListScanReports(serverName string) ([]*ScanReport, error)
	ListScanReportsByJob(jobID string) ([]*ScanReport, error)
	DeleteScanReport(id string) error
	DeleteServerScanReports(serverName string) error

	SaveIntegrityBaseline(baseline *IntegrityBaseline) error
	GetIntegrityBaseline(serverName string) (*IntegrityBaseline, error)
	DeleteIntegrityBaseline(serverName string) error
	ListIntegrityBaselines() ([]*IntegrityBaseline, error)
}

// EventEmitter defines how the service emits events
type EventEmitter interface {
	EmitSecurityScanStarted(serverName string, scanners []string, jobID string)
	EmitSecurityScanProgress(serverName, scannerID, status string, progress int)
	EmitSecurityScanCompleted(serverName string, findingsSummary map[string]int)
	EmitSecurityScanFailed(serverName, scannerID, errMsg string)
	EmitSecurityIntegrityAlert(serverName, alertType, action string)
}

// NoopEmitter is a no-op implementation of EventEmitter
type NoopEmitter struct{}

func (n *NoopEmitter) EmitSecurityScanStarted(string, []string, string)     {}
func (n *NoopEmitter) EmitSecurityScanProgress(string, string, string, int) {}
func (n *NoopEmitter) EmitSecurityScanCompleted(string, map[string]int)     {}
func (n *NoopEmitter) EmitSecurityScanFailed(string, string, string)        {}
func (n *NoopEmitter) EmitSecurityIntegrityAlert(string, string, string)    {}

// ServerInfoProvider resolves server configuration for auto-source resolution
type ServerInfoProvider interface {
	GetServerInfo(serverName string) (*ServerInfo, error)
}

// Service coordinates scanner management, scan execution, and approval workflow
type Service struct {
	storage        Storage
	engine         *Engine
	registry       *Registry
	docker         *DockerRunner
	emitter        EventEmitter
	sourceResolver *SourceResolver
	serverInfo     ServerInfoProvider
	secretStore    SecretStore
	logger         *zap.Logger
}

// NewService creates a new SecurityService
func NewService(storage Storage, registry *Registry, docker *DockerRunner, dataDir string, logger *zap.Logger) *Service {
	engine := NewEngine(docker, registry, dataDir, logger)
	svc := &Service{
		storage:        storage,
		engine:         engine,
		registry:       registry,
		docker:         docker,
		emitter:        &NoopEmitter{},
		sourceResolver: NewSourceResolver(logger),
		logger:         logger,
	}
	// Restore installed scanner state from storage (survives restart)
	svc.syncRegistryFromStorage()
	return svc
}

// SetEmitter sets the event emitter for the service
func (s *Service) SetEmitter(emitter EventEmitter) {
	s.emitter = emitter
}

// SetServerInfoProvider sets the provider for resolving server configuration
func (s *Service) SetServerInfoProvider(provider ServerInfoProvider) {
	s.serverInfo = provider
}

// syncRegistryFromStorage updates the in-memory registry status from
// persistent storage. This is needed after restart so the engine knows
// which scanners are installed/configured.
func (s *Service) syncRegistryFromStorage() {
	installed, err := s.storage.ListScanners()
	if err != nil || len(installed) == 0 {
		return
	}
	for _, inst := range installed {
		_ = s.registry.UpdateStatus(inst.ID, inst.Status)
		// Also update configured env so the engine can pass it to containers
		if inst.ConfiguredEnv != nil {
			if reg, err := s.registry.Get(inst.ID); err == nil {
				reg.ConfiguredEnv = inst.ConfiguredEnv
			}
		}
	}
	s.logger.Info("Synced scanner registry from storage", zap.Int("count", len(installed)))
}

// --- Scanner Management ---

// ListScanners returns all scanners from registry merged with installed state from storage
func (s *Service) ListScanners(ctx context.Context) ([]*ScannerPlugin, error) {
	registryScanners := s.registry.List()
	installedScanners, err := s.storage.ListScanners()
	if err != nil {
		return nil, fmt.Errorf("failed to list installed scanners: %w", err)
	}

	// Build installed lookup
	installed := make(map[string]*ScannerPlugin)
	for _, sc := range installedScanners {
		installed[sc.ID] = sc
	}

	// Merge: registry provides metadata, storage provides state
	var result []*ScannerPlugin
	for _, reg := range registryScanners {
		if inst, ok := installed[reg.ID]; ok {
			// Merge installed state into registry metadata
			merged := *reg
			merged.Status = inst.Status
			merged.InstalledAt = inst.InstalledAt
			merged.ConfiguredEnv = inst.ConfiguredEnv
			merged.LastUsedAt = inst.LastUsedAt
			merged.ErrorMsg = inst.ErrorMsg
			result = append(result, &merged)
		} else {
			result = append(result, reg)
		}
	}

	// Add installed scanners not in registry (custom)
	for _, inst := range installedScanners {
		found := false
		for _, r := range result {
			if r.ID == inst.ID {
				found = true
				break
			}
		}
		if !found {
			result = append(result, inst)
		}
	}

	return result, nil
}

// GetScanner returns a scanner by ID
func (s *Service) GetScanner(ctx context.Context, id string) (*ScannerPlugin, error) {
	// Try storage first (installed)
	if inst, err := s.storage.GetScanner(id); err == nil {
		return inst, nil
	}
	// Fall back to registry
	return s.registry.Get(id)
}

// InstallScanner pulls the Docker image and saves scanner as installed
func (s *Service) InstallScanner(ctx context.Context, id string) error {
	scanner, err := s.registry.Get(id)
	if err != nil {
		return fmt.Errorf("scanner not found in registry: %w", err)
	}

	// Check Docker availability
	if !s.docker.IsDockerAvailable(ctx) {
		return fmt.Errorf("Docker is not available; scanner installation requires Docker")
	}

	// Pull Docker image
	if err := s.docker.PullImage(ctx, scanner.DockerImage); err != nil {
		scanner.Status = ScannerStatusError
		scanner.ErrorMsg = err.Error()
		_ = s.storage.SaveScanner(scanner)
		return fmt.Errorf("failed to pull Docker image: %w", err)
	}

	// Save as installed
	scanner.Status = ScannerStatusInstalled
	scanner.InstalledAt = time.Now()
	scanner.ErrorMsg = ""
	if err := s.storage.SaveScanner(scanner); err != nil {
		return fmt.Errorf("failed to save scanner: %w", err)
	}

	// Update registry status
	_ = s.registry.UpdateStatus(id, ScannerStatusInstalled)
	return nil
}

// RemoveScanner removes a scanner, its Docker image, and stored configuration
func (s *Service) RemoveScanner(ctx context.Context, id string) error {
	sc, err := s.storage.GetScanner(id)
	if err != nil {
		return fmt.Errorf("scanner not installed: %w", err)
	}

	// Remove Docker image (best effort)
	if sc.DockerImage != "" {
		_ = s.docker.RemoveImage(ctx, sc.DockerImage)
	}

	// Delete from storage
	if err := s.storage.DeleteScanner(id); err != nil {
		return fmt.Errorf("failed to delete scanner: %w", err)
	}

	// Update registry status
	_ = s.registry.UpdateStatus(id, ScannerStatusAvailable)
	return nil
}

// SecretStore allows storing and resolving secrets via the OS keyring
type SecretStore interface {
	StoreSecret(ctx context.Context, name, value string) error
	ResolveSecret(ctx context.Context, ref string) (string, error)
}

// SetSecretStore sets the secret store for secure API key management.
// Also wires secret resolution into the scan engine for resolving
// ${keyring:...} references in scanner env vars at scan time.
func (s *Service) SetSecretStore(store SecretStore) {
	s.secretStore = store
	if store != nil {
		s.engine.secretResolver = func(ctx context.Context, ref string) (string, error) {
			return store.ResolveSecret(ctx, ref)
		}
	}
}

// ConfigureScanner sets environment variables (API keys) for a scanner.
// Secret values are stored in the OS keyring; only references are kept in config.
func (s *Service) ConfigureScanner(ctx context.Context, id string, env map[string]string) error {
	sc, err := s.storage.GetScanner(id)
	if err != nil {
		// If not in storage yet (just registered in registry), create from registry
		regScanner, regErr := s.registry.Get(id)
		if regErr != nil {
			return fmt.Errorf("scanner not found: %w", err)
		}
		sc = regScanner
	}

	if sc.ConfiguredEnv == nil {
		sc.ConfiguredEnv = make(map[string]string)
	}

	// Store secrets in keyring, keep references in config
	for k, v := range env {
		if s.secretStore != nil && v != "" {
			keyringName := fmt.Sprintf("scanner_%s_%s", id, strings.ToLower(k))
			if err := s.secretStore.StoreSecret(ctx, keyringName, v); err != nil {
				s.logger.Warn("Failed to store secret in keyring, storing as reference",
					zap.String("key", k), zap.Error(err))
				sc.ConfiguredEnv[k] = v // Fallback: store directly
			} else {
				sc.ConfiguredEnv[k] = fmt.Sprintf("${keyring:%s}", keyringName)
			}
		} else {
			sc.ConfiguredEnv[k] = v
		}
	}
	sc.Status = ScannerStatusConfigured

	if err := s.storage.SaveScanner(sc); err != nil {
		return fmt.Errorf("failed to save scanner config: %w", err)
	}

	_ = s.registry.UpdateStatus(id, ScannerStatusConfigured)
	return nil
}

// GetScannerStatus returns the current status of a scanner
func (s *Service) GetScannerStatus(ctx context.Context, id string) (*ScannerPlugin, error) {
	sc, err := s.GetScanner(ctx, id)
	if err != nil {
		return nil, err
	}

	// Check Docker image exists
	if sc.Status == ScannerStatusInstalled || sc.Status == ScannerStatusConfigured {
		if !s.docker.ImageExists(ctx, sc.DockerImage) {
			sc.Status = ScannerStatusError
			sc.ErrorMsg = "Docker image not found locally"
		}
	}

	return sc, nil
}

// --- Scan Operations ---

// scanCallbackAdapter adapts scan engine callbacks to service operations
type scanCallbackAdapter struct {
	service *Service
	cleanup func() // Optional cleanup function (e.g., remove temp source dir)
}

func (a *scanCallbackAdapter) OnScanStarted(job *ScanJob) {
	_ = a.service.storage.SaveScanJob(job)
	a.service.emitter.EmitSecurityScanStarted(job.ServerName, job.Scanners, job.ID)
}

func (a *scanCallbackAdapter) OnScannerStarted(job *ScanJob, scannerID string) {
	_ = a.service.storage.SaveScanJob(job)
	a.service.emitter.EmitSecurityScanProgress(job.ServerName, scannerID, ScanJobStatusRunning, 0)
}

func (a *scanCallbackAdapter) OnScannerCompleted(job *ScanJob, scannerID string, report *ScanReport) {
	_ = a.service.storage.SaveScanReport(report)
	_ = a.service.storage.SaveScanJob(job)
	a.service.emitter.EmitSecurityScanProgress(job.ServerName, scannerID, ScanJobStatusCompleted, 100)
}

func (a *scanCallbackAdapter) OnScannerFailed(job *ScanJob, scannerID string, err error) {
	_ = a.service.storage.SaveScanJob(job)
	a.service.emitter.EmitSecurityScanFailed(job.ServerName, scannerID, err.Error())
}

func (a *scanCallbackAdapter) OnScanCompleted(job *ScanJob, reports []*ScanReport) {
	_ = a.service.storage.SaveScanJob(job)
	// Aggregate findings summary for event
	summary := make(map[string]int)
	for _, r := range reports {
		for _, f := range r.Findings {
			summary[f.Severity]++
		}
	}
	a.service.emitter.EmitSecurityScanCompleted(job.ServerName, summary)
	// Cleanup auto-resolved source directory
	if a.cleanup != nil {
		a.cleanup()
	}
}

func (a *scanCallbackAdapter) OnScanFailed(job *ScanJob, err error) {
	_ = a.service.storage.SaveScanJob(job)
	// Cleanup auto-resolved source directory
	if a.cleanup != nil {
		a.cleanup()
	}
}

// StartScan triggers a security scan for a server
func (s *Service) StartScan(ctx context.Context, serverName string, dryRun bool, scannerIDs []string, sourceDir string) (*ScanJob, error) {
	req := ScanRequest{
		ServerName: serverName,
		DryRun:     dryRun,
		ScannerIDs: scannerIDs,
		SourceDir:  sourceDir,
	}

	// Build scan context for transparency
	scanCtx := &ScanContext{
		SourceMethod: "none",
	}

	// Get server info for context
	var serverInfo *ServerInfo
	if s.serverInfo != nil {
		if info, err := s.serverInfo.GetServerInfo(serverName); err == nil {
			serverInfo = info
			scanCtx.ServerProtocol = info.Protocol
			scanCtx.ServerCommand = info.Command
		}
	}

	// Auto-resolve source if not explicitly provided
	var resolvedCleanup func()
	if req.SourceDir == "" && serverInfo != nil {
		resolved, err := s.sourceResolver.Resolve(ctx, *serverInfo)
		if err != nil {
			s.logger.Warn("Auto-source resolution failed",
				zap.String("server", serverName),
				zap.Error(err),
			)
		} else {
			req.SourceDir = resolved.SourceDir
			resolvedCleanup = resolved.Cleanup
			scanCtx.SourceMethod = resolved.Method
			scanCtx.SourcePath = resolved.SourceDir
			if resolved.ServerURL != "" {
				scanCtx.SourcePath = resolved.ServerURL
			}
			scanCtx.ContainerID = resolved.ContainerID
			// Determine Docker isolation status
			if resolved.Method == "docker_extract" {
				scanCtx.DockerIsolation = true
			}
			// Collect file list for transparency
			s.sourceResolver.EnrichWithFileList(resolved)
			scanCtx.ScannedFiles = resolved.Files
			scanCtx.TotalFiles = resolved.TotalFiles
			scanCtx.TotalSizeBytes = resolved.TotalSize

			s.logger.Info("Scan source resolved",
				zap.String("server", serverName),
				zap.String("method", resolved.Method),
				zap.String("source_dir", resolved.SourceDir),
				zap.Int("files", resolved.TotalFiles),
				zap.Int64("size_bytes", resolved.TotalSize),
			)
		}
	} else if req.SourceDir != "" {
		scanCtx.SourceMethod = "manual"
		scanCtx.SourcePath = req.SourceDir
		files, total, size := CollectFileList(req.SourceDir)
		scanCtx.ScannedFiles = files
		scanCtx.TotalFiles = total
		scanCtx.TotalSizeBytes = size
	}

	// Attach context to the scan request so the engine can set it on the job
	req.ScanContext = scanCtx

	callback := &scanCallbackAdapter{service: s, cleanup: resolvedCleanup}
	job, err := s.engine.StartScan(ctx, req, callback)
	if err != nil {
		return nil, err
	}

	// Prune old scans (keep last MaxScansPerServer)
	go s.pruneOldScans(serverName)

	return job, err
}

// GetScanStatus returns the current scan status for a server
func (s *Service) GetScanStatus(ctx context.Context, serverName string) (*ScanJob, error) {
	// Check for active scan first
	if active := s.engine.GetActiveJob(serverName); active != nil {
		return active, nil
	}
	// Return latest completed scan
	return s.storage.GetLatestScanJob(serverName)
}

// GetScanReport returns the aggregated report for a server's latest scan
func (s *Service) GetScanReport(ctx context.Context, serverName string) (*AggregatedReport, error) {
	job, err := s.storage.GetLatestScanJob(serverName)
	if err != nil {
		return nil, fmt.Errorf("no scan found for server %s: %w", serverName, err)
	}

	reports, err := s.storage.ListScanReportsByJob(job.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load reports for job %s: %w", job.ID, err)
	}

	return AggregateReports(job.ID, serverName, reports), nil
}

// CancelScan cancels a running scan for a server
func (s *Service) CancelScan(ctx context.Context, serverName string) error {
	return s.engine.CancelScan(serverName)
}

// --- Approval Flow ---

// ApproveServer approves a scanned server, storing the integrity baseline
func (s *Service) ApproveServer(ctx context.Context, serverName string, force bool, approvedBy string) error {
	// Get latest scan report
	aggReport, err := s.GetScanReport(ctx, serverName)
	if err != nil {
		if !force {
			return fmt.Errorf("no scan results found; run a scan first or use --force")
		}
	}

	// Check for critical findings (block unless force)
	if aggReport != nil && aggReport.Summary.Critical > 0 && !force {
		return fmt.Errorf("server has %d critical findings; resolve them or use --force to approve anyway", aggReport.Summary.Critical)
	}

	// Create integrity baseline
	baseline := &IntegrityBaseline{
		ServerName: serverName,
		ApprovedAt: time.Now(),
		ApprovedBy: approvedBy,
	}

	// Get image digest if available
	if digest, err := s.docker.GetImageDigest(ctx, "mcpproxy-snapshot-"+serverName); err == nil {
		baseline.ImageDigest = digest
	}

	// Store report IDs
	if aggReport != nil {
		for _, r := range aggReport.Reports {
			baseline.ScanReportIDs = append(baseline.ScanReportIDs, r.ID)
		}
	}

	if err := s.storage.SaveIntegrityBaseline(baseline); err != nil {
		return fmt.Errorf("failed to save integrity baseline: %w", err)
	}

	s.logger.Info("Server approved after security scan",
		zap.String("server", serverName),
		zap.String("approved_by", approvedBy),
	)

	return nil
}

// RejectServer rejects a server, cleaning up all artifacts
func (s *Service) RejectServer(ctx context.Context, serverName string) error {
	// Delete scan reports
	if err := s.storage.DeleteServerScanReports(serverName); err != nil {
		s.logger.Warn("Failed to delete scan reports", zap.String("server", serverName), zap.Error(err))
	}

	// Delete scan jobs
	if err := s.storage.DeleteServerScanJobs(serverName); err != nil {
		s.logger.Warn("Failed to delete scan jobs", zap.String("server", serverName), zap.Error(err))
	}

	// Delete integrity baseline
	if err := s.storage.DeleteIntegrityBaseline(serverName); err != nil {
		s.logger.Warn("Failed to delete integrity baseline", zap.String("server", serverName), zap.Error(err))
	}

	// Remove snapshot image (best effort)
	_ = s.docker.RemoveImage(ctx, "mcpproxy-snapshot-"+serverName)

	s.logger.Info("Server rejected and artifacts cleaned up", zap.String("server", serverName))
	return nil
}

// --- Integrity ---

// CheckIntegrity verifies a server's runtime integrity against its baseline
func (s *Service) CheckIntegrity(ctx context.Context, serverName string) (*IntegrityCheckResult, error) {
	baseline, err := s.storage.GetIntegrityBaseline(serverName)
	if err != nil {
		return nil, fmt.Errorf("no integrity baseline for server %s: %w", serverName, err)
	}

	result := &IntegrityCheckResult{
		ServerName: serverName,
		CheckedAt:  time.Now(),
		Passed:     true,
	}

	// Check image digest
	if baseline.ImageDigest != "" {
		currentDigest, err := s.docker.GetImageDigest(ctx, "mcpproxy-snapshot-"+serverName)
		if err != nil {
			result.Passed = false
			result.Violations = append(result.Violations, IntegrityViolation{
				Type:    "digest_check_failed",
				Message: fmt.Sprintf("Failed to get image digest: %v", err),
			})
		} else if currentDigest != baseline.ImageDigest {
			result.Passed = false
			result.Violations = append(result.Violations, IntegrityViolation{
				Type:     "digest_mismatch",
				Message:  "Image digest does not match approved baseline",
				Expected: baseline.ImageDigest,
				Actual:   currentDigest,
			})
			s.emitter.EmitSecurityIntegrityAlert(serverName, "digest_mismatch", "re-quarantine")
		}
	}

	return result, nil
}

// --- Overview ---

// GetSecurityOverview returns aggregated security statistics.
// Satisfies the httpapi.SecurityController interface.
func (s *Service) GetSecurityOverview(ctx context.Context) (*SecurityOverview, error) {
	return s.GetOverview(ctx)
}

// GetOverview returns aggregated security statistics
func (s *Service) GetOverview(ctx context.Context) (*SecurityOverview, error) {
	overview := &SecurityOverview{}

	// Count installed scanners
	scanners, err := s.storage.ListScanners()
	if err == nil {
		overview.ScannersInstalled = len(scanners)
	}

	// Count scan jobs
	jobs, err := s.storage.ListScanJobs("")
	if err == nil {
		overview.TotalScans = len(jobs)
		serversScanned := make(map[string]bool)
		for _, j := range jobs {
			serversScanned[j.ServerName] = true
			if j.Status == ScanJobStatusRunning {
				overview.ActiveScans++
			}
			if overview.LastScanAt.IsZero() || j.StartedAt.After(overview.LastScanAt) {
				overview.LastScanAt = j.StartedAt
			}
		}
		overview.ServersScanned = len(serversScanned)
	}

	// Aggregate findings from all reports
	reports, err := s.storage.ListScanReports("")
	if err == nil {
		for _, r := range reports {
			for _, f := range r.Findings {
				switch f.Severity {
				case SeverityCritical:
					overview.FindingsBySeverity.Critical++
				case SeverityHigh:
					overview.FindingsBySeverity.High++
				case SeverityMedium:
					overview.FindingsBySeverity.Medium++
				case SeverityLow:
					overview.FindingsBySeverity.Low++
				case SeverityInfo:
					overview.FindingsBySeverity.Info++
				}
				overview.FindingsBySeverity.Total++
			}
		}
	}

	return overview, nil
}

// IntegrityCheckResult holds the result of an integrity check
type IntegrityCheckResult struct {
	ServerName string               `json:"server_name"`
	Passed     bool                 `json:"passed"`
	CheckedAt  time.Time            `json:"checked_at"`
	Violations []IntegrityViolation `json:"violations,omitempty"`
}

// IntegrityViolation describes a specific integrity check failure
type IntegrityViolation struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
}

// GetScanSummary returns a compact scan summary for a server (for the server list API).
// Returns nil if no scans have been run for this server.
func (s *Service) GetScanSummary(ctx context.Context, serverName string) *ScanSummary {

	// Check for active scan
	if active := s.engine.GetActiveJob(serverName); active != nil {
		return &ScanSummary{
			RiskScore: 0,
			Status:    "scanning",
		}
	}

	// Get latest job
	job, err := s.storage.GetLatestScanJob(serverName)
	if err != nil {
		return nil // No scans run
	}

	summary := &ScanSummary{
		LastScanAt: &job.StartedAt,
		Status:     "clean",
	}

	// Get reports for this job
	reports, err := s.storage.ListScanReportsByJob(job.ID)
	if err != nil || len(reports) == 0 {
		return summary
	}

	// Aggregate findings and apply classification if missing
	var allFindings []ScanFinding
	for _, r := range reports {
		allFindings = append(allFindings, r.Findings...)
	}
	// Re-classify findings that lack threat_level (legacy data)
	ClassifyAllFindings(allFindings)

	summary.RiskScore = CalculateRiskScore(allFindings)

	// Count by threat level
	counts := FindingCounts{Total: len(allFindings)}
	for _, f := range allFindings {
		switch f.ThreatLevel {
		case ThreatLevelDangerous:
			counts.Dangerous++
		case ThreatLevelWarning:
			counts.Warning++
		default:
			counts.Info++
		}
	}
	summary.FindingCounts = &counts

	// Determine status
	if counts.Dangerous > 0 {
		summary.Status = "dangerous"
	} else if counts.Warning > 0 {
		summary.Status = "warnings"
	} else if counts.Total > 0 {
		summary.Status = "clean" // Only informational findings
	}

	return summary
}

// ScanSummary is a compact representation of scan status for the server list.
type ScanSummary struct {
	LastScanAt    *time.Time     `json:"last_scan_at,omitempty"`
	RiskScore     int            `json:"risk_score"`
	Status        string         `json:"status"` // clean, warnings, dangerous, not_scanned, scanning
	FindingCounts *FindingCounts `json:"finding_counts,omitempty"`
}

// FindingCounts groups findings by user-facing threat level.
type FindingCounts struct {
	Dangerous int `json:"dangerous"`
	Warning   int `json:"warning"`
	Info      int `json:"info"`
	Total     int `json:"total"`
}

// pruneOldScans removes old scan jobs and reports beyond MaxScansPerServer
func (s *Service) pruneOldScans(serverName string) {
	jobs, err := s.storage.ListScanJobs(serverName)
	if err != nil || len(jobs) <= MaxScansPerServer {
		return
	}

	// Sort by start time descending (newest first)
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].StartedAt.After(jobs[j].StartedAt)
	})

	// Delete jobs beyond the limit
	for i := MaxScansPerServer; i < len(jobs); i++ {
		// Delete associated reports
		reports, _ := s.storage.ListScanReportsByJob(jobs[i].ID)
		for _, r := range reports {
			_ = s.storage.DeleteScanReport(r.ID)
		}
		_ = s.storage.DeleteScanJob(jobs[i].ID)
	}

	s.logger.Debug("Pruned old scans",
		zap.String("server", serverName),
		zap.Int("deleted", len(jobs)-MaxScansPerServer),
		zap.Int("kept", MaxScansPerServer),
	)
}
