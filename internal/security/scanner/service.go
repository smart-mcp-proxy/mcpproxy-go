package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
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
	// EmitSecurityScannerChanged signals that a scanner plugin's state has
	// changed (e.g., background pull started/finished/failed) so the web UI
	// can refresh its scanner list without polling.
	EmitSecurityScannerChanged(scannerID, status, errMsg string)
}

// NoopEmitter is a no-op implementation of EventEmitter
type NoopEmitter struct{}

func (n *NoopEmitter) EmitSecurityScanStarted(string, []string, string)     {}
func (n *NoopEmitter) EmitSecurityScanProgress(string, string, string, int) {}
func (n *NoopEmitter) EmitSecurityScanCompleted(string, map[string]int)     {}
func (n *NoopEmitter) EmitSecurityScanFailed(string, string, string)        {}
func (n *NoopEmitter) EmitSecurityIntegrityAlert(string, string, string)    {}
func (n *NoopEmitter) EmitSecurityScannerChanged(string, string, string)    {}

// ServerInfoProvider resolves server configuration for auto-source resolution
type ServerInfoProvider interface {
	GetServerInfo(serverName string) (*ServerInfo, error)
	GetServerTools(serverName string) ([]map[string]interface{}, error)
	// EnsureConnected attempts to connect a disconnected/quarantined server
	// so that tool definitions can be retrieved for scanning.
	// Returns nil if already connected or successfully connected.
	EnsureConnected(ctx context.Context, serverName string) error
	// IsConnected returns whether the server has an active MCP connection.
	IsConnected(serverName string) bool
}

// ServerUnquarantiner performs the full unquarantine workflow for a server.
// Implementations are expected to:
//   - Clear the quarantined flag in storage and persist config
//   - Trigger a tool (re)index for the server
//   - Emit the same events/activity entries that the normal unquarantine path
//     emits
//
// This interface is intentionally small so the scanner service does not need
// to depend on the full runtime package.
type ServerUnquarantiner interface {
	UnquarantineServer(serverName string) error
}

// Service coordinates scanner management, scan execution, and approval workflow
type Service struct {
	storage        Storage
	engine         *Engine
	registry       *Registry
	docker         *DockerRunner
	emitter        atomic.Pointer[EventEmitter]
	sourceResolver *SourceResolver
	serverInfo     ServerInfoProvider
	unquarantiner  ServerUnquarantiner
	secretStore    SecretStore
	queue          *ScanQueue
	pulls          *pullManager
	logger         *zap.Logger

	// In-memory scan summary cache — avoids expensive BBolt reads per server
	summaryCache   map[string]*ScanSummary
	summaryCacheMu sync.RWMutex
}

// NewService creates a new SecurityService
func NewService(storage Storage, registry *Registry, docker *DockerRunner, dataDir string, logger *zap.Logger) *Service {
	engine := NewEngine(docker, registry, dataDir, logger)
	svc := &Service{
		storage:        storage,
		engine:         engine,
		registry:       registry,
		docker:         docker,
		sourceResolver: NewSourceResolver(logger),
		queue:          NewScanQueue(logger),
		summaryCache:   make(map[string]*ScanSummary),
		logger:         logger,
	}
	var noop EventEmitter = &NoopEmitter{}
	svc.emitter.Store(&noop)
	svc.pulls = newPullManager(docker, storage, registry, logger, svc.emit)
	// Restore installed scanner state from storage (survives restart)
	svc.syncRegistryFromStorage()
	// Heal scanners that were left in the "pulling" state after a crash.
	svc.resumePendingPulls()
	return svc
}

// SetEmitter sets the event emitter for the service.
func (s *Service) SetEmitter(emitter EventEmitter) {
	s.emitter.Store(&emitter)
}

// SetScannerDisableNoNewPrivileges controls whether scanner containers are
// launched without `--security-opt no-new-privileges`. This is the runtime
// knob for SecurityConfig.ScannerDisableNoNewPrivileges. See the config
// field doc for background on why a user might need to enable this.
func (s *Service) SetScannerDisableNoNewPrivileges(disable bool) {
	if s.engine == nil {
		return
	}
	s.engine.disableNoNewPrivileges = disable
	if disable {
		s.logger.Warn("Scanner containers will run WITHOUT --security-opt no-new-privileges " +
			"(security.scanner_disable_no_new_privileges=true). This is a workaround for " +
			"snap-docker + AppArmor hosts; prefer replacing snap docker with a distro package.")
	}
}

// emit returns the most recently configured EventEmitter. Always returns a
// non-nil value; callers can invoke methods on it directly.
func (s *Service) emit() EventEmitter {
	if e := s.emitter.Load(); e != nil {
		return *e
	}
	return &NoopEmitter{}
}

// SetServerInfoProvider sets the provider for resolving server configuration
func (s *Service) SetServerInfoProvider(provider ServerInfoProvider) {
	s.serverInfo = provider
}

// SetServerUnquarantiner wires the unquarantine callback used by ApproveServer.
// If not set, ApproveServer will still succeed in storing a baseline but will
// log a warning because it cannot actually unquarantine the server.
func (s *Service) SetServerUnquarantiner(u ServerUnquarantiner) {
	s.unquarantiner = u
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

	// Stable, alphabetical order by ID so the web UI, CLI, and API all
	// agree on the same order.
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

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

// InstallScanner enables a scanner and kicks off a background Docker image
// pull. Returns immediately — the UI tracks progress via SSE events
// (security.scanner_changed) or by polling GET /api/v1/security/scanners.
//
// Behavior:
//  1. If the image is already present locally, the scanner is marked
//     "installed" synchronously and the function returns nil.
//  2. Otherwise the scanner is marked "pulling" and a goroutine performs the
//     actual docker pull. On success status → "installed"; on failure
//     status → "error" with ErrorMsg set.
//  3. If Docker itself is not running, the scanner is marked "error" so the
//     user gets clear feedback.
func (s *Service) InstallScanner(ctx context.Context, id string) error {
	scanner, err := s.registry.Get(id)
	if err != nil {
		return fmt.Errorf("scanner not found in registry: %w", err)
	}

	// Reuse any previously-stored configured env / image override so that
	// toggling the scanner off and back on doesn't wipe the user's API keys.
	if existing, err := s.storage.GetScanner(id); err == nil && existing != nil {
		if len(existing.ConfiguredEnv) > 0 {
			scanner.ConfiguredEnv = existing.ConfiguredEnv
		}
		if existing.ImageOverride != "" {
			scanner.ImageOverride = existing.ImageOverride
		}
	}

	image := scanner.EffectiveImage()

	// Fast path: image already present → no need to pull at all.
	if s.docker != nil && s.docker.ImageExists(ctx, image) {
		scanner.Status = targetStatusAfterPull(scanner)
		scanner.InstalledAt = time.Now()
		scanner.ErrorMsg = ""
		if err := s.storage.SaveScanner(scanner); err != nil {
			return fmt.Errorf("failed to save scanner: %w", err)
		}
		_ = s.registry.UpdateStatus(id, scanner.Status)
		s.emit().EmitSecurityScannerChanged(id, scanner.Status, "")
		return nil
	}

	// Docker daemon must be running to pull anything.
	if s.docker != nil && !s.docker.IsDockerAvailable(ctx) {
		scanner.Status = ScannerStatusError
		scanner.ErrorMsg = "Docker is not available; start Docker Desktop and try again"
		_ = s.storage.SaveScanner(scanner)
		_ = s.registry.UpdateStatus(id, ScannerStatusError)
		s.emit().EmitSecurityScannerChanged(id, ScannerStatusError, scanner.ErrorMsg)
		return fmt.Errorf("%s", scanner.ErrorMsg)
	}

	// Mark as "pulling" and return immediately. The pullManager will flip
	// the state to installed/configured or error when it's done.
	scanner.Status = ScannerStatusPulling
	scanner.ErrorMsg = ""
	if err := s.storage.SaveScanner(scanner); err != nil {
		return fmt.Errorf("failed to save scanner: %w", err)
	}
	_ = s.registry.UpdateStatus(id, ScannerStatusPulling)
	s.emit().EmitSecurityScannerChanged(id, ScannerStatusPulling, "")
	s.pulls.Enqueue(id, targetStatusAfterPull(scanner))
	return nil
}

// targetStatusAfterPull picks the success status for a scanner once its
// image has been pulled. Scanners with stored env vars land in "configured";
// everything else lands in "installed".
func targetStatusAfterPull(sc *ScannerPlugin) string {
	if len(sc.ConfiguredEnv) > 0 {
		return ScannerStatusConfigured
	}
	return ScannerStatusInstalled
}

// resumePendingPulls scans the persistent scanner table at startup and
// either reschedules or heals scanners that were left in the "pulling"
// state after a crash. Without this, a scanner that was pulling when
// mcpproxy died would be stuck in that state forever.
func (s *Service) resumePendingPulls() {
	scanners, err := s.storage.ListScanners()
	if err != nil || s.pulls == nil || s.docker == nil {
		return
	}
	for _, sc := range scanners {
		if sc.Status != ScannerStatusPulling {
			continue
		}
		image := sc.EffectiveImage()
		// If the image already exists locally, just fix the status.
		if image != "" && s.docker.ImageExists(context.Background(), image) {
			sc.Status = targetStatusAfterPull(sc)
			sc.ErrorMsg = ""
			_ = s.storage.SaveScanner(sc)
			_ = s.registry.UpdateStatus(sc.ID, sc.Status)
			continue
		}
		// Otherwise re-queue the pull so it finishes in the background.
		s.logger.Info("Resuming interrupted scanner image pull",
			zap.String("scanner", sc.ID),
			zap.String("image", image),
		)
		s.pulls.Enqueue(sc.ID, targetStatusAfterPull(sc))
	}
}

// RemoveScanner removes a scanner, its Docker image, and stored configuration.
// If a background pull is in progress for this scanner it is cancelled.
func (s *Service) RemoveScanner(ctx context.Context, id string) error {
	sc, err := s.storage.GetScanner(id)
	if err != nil {
		return fmt.Errorf("scanner not installed: %w", err)
	}

	// Stop any in-flight background pull for this scanner.
	if s.pulls != nil {
		s.pulls.cancelPending(id)
	}

	// Remove Docker image (best effort)
	if sc.DockerImage != "" {
		_ = s.docker.RemoveImage(ctx, sc.DockerImage)
	}

	// Delete from storage
	if err := s.storage.DeleteScanner(id); err != nil {
		return fmt.Errorf("failed to delete scanner: %w", err)
	}

	// Update registry status and broadcast the change.
	_ = s.registry.UpdateStatus(id, ScannerStatusAvailable)
	s.emit().EmitSecurityScannerChanged(id, ScannerStatusAvailable, "")
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

// ConfigureScanner sets environment variables (API keys) and/or an image
// override for a scanner.
//
// Scanner env values are stored DIRECTLY in the scanner's ConfiguredEnv
// map in BBolt, NOT in the OS keyring. Previous versions attempted to write
// to the OS keyring and fall back on failure, but that path is unsafe on
// macOS: keyring.Set (which wraps Security.framework under the hood) can
// pop a blocking "Keychain Not Found" system modal when the user's default
// keychain is in an unusual state, and the underlying goroutine cannot be
// cancelled once started (it stays live until it hits the real backend,
// which may never happen). The scanner env values end up in the container's
// /proc/environ at scan time anyway, so keyring storage adds no meaningful
// confidentiality — it's a trust-boundary we don't actually have.
//
// Users who want OS-keyring storage for a specific secret can still use a
// `${keyring:my-secret-name}` reference as the env value. The resolver
// expands it at scan time via a read-only keyring Get, which is safe on
// all platforms.
//
// If the effective Docker image changes (user set a new override) and the
// new image is not already cached locally, the scanner is transitioned to
// the "pulling" state and a background pull is kicked off. The call returns
// immediately — the UI tracks the pull via SSE or polling.
func (s *Service) ConfigureScanner(_ context.Context, id string, env map[string]string, dockerImage string) error {
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

	// Store scanner env values directly in the scanner record. Do NOT call
	// keyring.Set — see the doc comment above for the reason. Values that
	// look like `${keyring:name}` references are passed through as-is; the
	// resolver expands them via a read-only Get at scan time.
	for k, v := range env {
		sc.ConfiguredEnv[k] = v
	}

	// Track whether the effective image changed so we know when to re-pull.
	prevImage := sc.EffectiveImage()
	if dockerImage != "" {
		sc.ImageOverride = dockerImage
	}
	newImage := sc.EffectiveImage()
	imageChanged := newImage != prevImage

	// Pick the resting status. If we have to pull, we'll override this to
	// "pulling" just below.
	sc.Status = targetStatusAfterPull(sc)
	sc.ErrorMsg = ""

	// Only kick off a background pull if the effective image actually
	// changed. If the user is just updating env vars we trust the previous
	// installation (status "installed"/"configured"/"error" handled via
	// retry/Install, not here).
	needsPull := imageChanged && newImage != "" && s.docker != nil
	if needsPull {
		sc.Status = ScannerStatusPulling
	}

	if err := s.storage.SaveScanner(sc); err != nil {
		return fmt.Errorf("failed to save scanner config: %w", err)
	}

	_ = s.registry.UpdateStatus(id, sc.Status)

	// Also update the registry's ConfiguredEnv and ImageOverride so the engine
	// picks up changes without requiring a restart
	if reg, err := s.registry.Get(id); err == nil {
		reg.ConfiguredEnv = sc.ConfiguredEnv
		reg.ImageOverride = sc.ImageOverride
	}

	s.emit().EmitSecurityScannerChanged(id, sc.Status, "")

	if needsPull {
		s.pulls.Enqueue(id, targetStatusAfterPull(sc))
	}

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
	service    *Service
	cleanup    func()      // Optional cleanup function (e.g., remove temp source dir)
	scanPass   int         // Which pass this callback is for (1 or 2)
	serverInfo *ServerInfo // Cached server info for pass 2 auto-start
}

func (a *scanCallbackAdapter) OnScanStarted(job *ScanJob) {
	_ = a.service.storage.SaveScanJob(job)
	a.service.emit().EmitSecurityScanStarted(job.ServerName, job.Scanners, job.ID)
	// Invalidate cached summary so server list shows "scanning"
	a.service.invalidateScanSummaryCache(job.ServerName)
}

func (a *scanCallbackAdapter) OnScannerStarted(job *ScanJob, scannerID string) {
	_ = a.service.storage.SaveScanJob(job)
	a.service.emit().EmitSecurityScanProgress(job.ServerName, scannerID, ScanJobStatusRunning, 0)
}

func (a *scanCallbackAdapter) OnScannerCompleted(job *ScanJob, scannerID string, report *ScanReport) {
	_ = a.service.storage.SaveScanReport(report)
	_ = a.service.storage.SaveScanJob(job)
	a.service.emit().EmitSecurityScanProgress(job.ServerName, scannerID, ScanJobStatusCompleted, 100)
}

func (a *scanCallbackAdapter) OnScannerFailed(job *ScanJob, scannerID string, err error) {
	_ = a.service.storage.SaveScanJob(job)
	a.service.emit().EmitSecurityScanFailed(job.ServerName, scannerID, err.Error())
}

func (a *scanCallbackAdapter) OnScanCompleted(job *ScanJob, reports []*ScanReport) {
	_ = a.service.storage.SaveScanJob(job)
	// Invalidate cached summary so next read gets fresh results
	a.service.invalidateScanSummaryCache(job.ServerName)
	// Aggregate findings summary for event
	summary := make(map[string]int)
	for _, r := range reports {
		for _, f := range r.Findings {
			summary[f.Severity]++
		}
	}
	a.service.emit().EmitSecurityScanCompleted(job.ServerName, summary)
	// Cleanup auto-resolved source directory
	if a.cleanup != nil {
		a.cleanup()
	}
	// Auto-start Pass 2 (supply chain audit) in background after Pass 1 completes.
	// Skip for HTTP/URL servers — they have no filesystem to do supply chain analysis on.
	if a.scanPass == ScanPassSecurityScan && !job.DryRun {
		isURLServer := a.serverInfo != nil && (a.serverInfo.Protocol == "http" || a.serverInfo.Protocol == "sse" || a.serverInfo.Protocol == "streamable-http")
		if !isURLServer {
			go a.service.startPass2(job.ServerName, a.serverInfo)
		}
	}
}

func (a *scanCallbackAdapter) OnScanFailed(job *ScanJob, err error) {
	_ = a.service.storage.SaveScanJob(job)
	// Invalidate cached summary
	a.service.invalidateScanSummaryCache(job.ServerName)
	// Cleanup auto-resolved source directory
	if a.cleanup != nil {
		a.cleanup()
	}
}

// StartScan triggers a security scan for a server (Pass 1: fast security scan).
// After Pass 1 completes, Pass 2 (supply chain audit) is auto-started in the background.
func (s *Service) StartScan(ctx context.Context, serverName string, dryRun bool, scannerIDs []string, sourceDir string) (*ScanJob, error) {
	req := ScanRequest{
		ServerName: serverName,
		DryRun:     dryRun,
		ScannerIDs: scannerIDs,
		SourceDir:  sourceDir,
		ScanPass:   ScanPassSecurityScan,
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

	// Export server tool definitions for Cisco scanner (which scans tool descriptions).
	// If the server is disconnected (e.g., quarantined), auto-connect it first so we
	// can retrieve tool definitions. The quarantine state is preserved — tools remain
	// blocked for clients; we only need the definitions for scanning.
	if s.serverInfo != nil {
		// If no source dir was resolved (no Docker container, no working_dir),
		// create a temp dir so Cisco scanner can at least scan tool definitions.
		if req.SourceDir == "" {
			tempDir, err := os.MkdirTemp("", fmt.Sprintf("mcpproxy-scan-tools-%s-", serverName))
			if err == nil {
				req.SourceDir = tempDir
				// For HTTP/URL servers, preserve the "url" source method and path
				// (the temp dir is only for tool definitions, not the real source)
				if scanCtx.SourceMethod != "url" {
					scanCtx.SourceMethod = "tool_definitions_only"
					scanCtx.SourcePath = tempDir
				}
				oldCleanup := resolvedCleanup
				resolvedCleanup = func() {
					os.RemoveAll(tempDir)
					if oldCleanup != nil {
						oldCleanup()
					}
				}
				s.logger.Info("Created temp dir for tool definitions (no source files available)",
					zap.String("server", serverName), zap.String("dir", tempDir))
			}
		}

		if req.SourceDir != "" {
			if !s.serverInfo.IsConnected(serverName) {
				s.logger.Info("Server is disconnected, attempting to connect for scan",
					zap.String("server", serverName))
				if err := s.serverInfo.EnsureConnected(ctx, serverName); err != nil {
					s.logger.Warn("Failed to connect server for tool export (scan will continue without tool definitions)",
						zap.String("server", serverName), zap.Error(err))
				} else {
					// Wait for the server to actually connect (up to 30 seconds)
					s.waitForConnection(serverName, 30*time.Second)
				}
			}
			scanCtx.ToolsExported = s.exportToolDefinitions(serverName, req.SourceDir)

			// If export failed but server should be connected, retry once after
			// ensuring connection (handles quarantined servers that need an
			// inspection exemption, and stale StateView snapshots)
			if scanCtx.ToolsExported == 0 {
				s.logger.Info("Tool export returned 0, retrying after EnsureConnected",
					zap.String("server", serverName))
				if err := s.serverInfo.EnsureConnected(ctx, serverName); err != nil {
					s.logger.Warn("Retry EnsureConnected failed",
						zap.String("server", serverName), zap.Error(err))
				} else {
					s.waitForConnection(serverName, 30*time.Second)
					scanCtx.ToolsExported = s.exportToolDefinitions(serverName, req.SourceDir)
				}
			}
		}
	}

	// Abort scan if we have no source files AND no tool definitions to scan.
	// This prevents running scanners on an empty directory (wasting time).
	// Covers both:
	//   - "tool_definitions_only": stdio servers with no source dir
	//   - "url": HTTP/SSE servers where source is the URL (no local files)
	noSourceFiles := scanCtx.SourceMethod == "tool_definitions_only" ||
		(scanCtx.SourceMethod == "url" && scanCtx.TotalFiles == 0)
	if noSourceFiles && scanCtx.ToolsExported == 0 {
		// Clean up the empty temp dir
		if resolvedCleanup != nil {
			resolvedCleanup()
		}
		connected := s.serverInfo != nil && s.serverInfo.IsConnected(serverName)
		if connected {
			return nil, fmt.Errorf("cannot scan server %s: no source files available and tool export failed (server is connected but returned 0 tools). Check server logs", serverName)
		}
		return nil, fmt.Errorf("cannot scan server %s: no source files available and server is disconnected (unable to export tool definitions). Connect the server first or configure a working_dir", serverName)
	}

	// Attach context to the scan request so the engine can set it on the job
	req.ScanContext = scanCtx

	callback := &scanCallbackAdapter{
		service:    s,
		cleanup:    resolvedCleanup,
		scanPass:   ScanPassSecurityScan,
		serverInfo: serverInfo,
	}
	job, err := s.engine.StartScan(ctx, req, callback)
	if err != nil {
		return nil, err
	}

	// Prune old scans (keep last MaxScansPerServer)
	go s.pruneOldScans(serverName)

	return job, err
}

// startPass2 starts the background supply chain audit (Pass 2) for a server.
// It re-resolves source WITHOUT filtering (full container filesystem including deps)
// and runs only Trivy-compatible scanners for deep CVE analysis.
func (s *Service) startPass2(serverName string, serverInfo *ServerInfo) {
	s.logger.Info("Starting Pass 2 (supply chain audit) in background",
		zap.String("server", serverName),
	)

	ctx := context.Background()

	req := ScanRequest{
		ServerName: serverName,
		DryRun:     false,
		ScanPass:   ScanPassSupplyChainAudit,
	}

	// Build scan context
	scanCtx := &ScanContext{
		SourceMethod: "none",
	}

	// Re-resolve source for Pass 2: include full filesystem (deps, site-packages, etc.)
	var resolvedCleanup func()
	if serverInfo != nil {
		scanCtx.ServerProtocol = serverInfo.Protocol
		scanCtx.ServerCommand = serverInfo.Command

		resolved, err := s.sourceResolver.ResolveFullSource(ctx, *serverInfo)
		if err != nil {
			s.logger.Warn("Pass 2 source resolution failed",
				zap.String("server", serverName),
				zap.Error(err),
			)
			// Fall back to a failed pass 2 job so the UI knows it was attempted
			s.saveFailedPass2Job(serverName, "source resolution failed: "+err.Error())
			return
		}
		req.SourceDir = resolved.SourceDir
		resolvedCleanup = resolved.Cleanup
		scanCtx.SourceMethod = resolved.Method + "_full"
		scanCtx.SourcePath = resolved.SourceDir
		if resolved.ServerURL != "" {
			scanCtx.SourcePath = resolved.ServerURL
		}
		scanCtx.ContainerID = resolved.ContainerID
		if resolved.Method == "docker_extract" {
			scanCtx.DockerIsolation = true
		}
		s.sourceResolver.EnrichWithFileList(resolved)
		scanCtx.ScannedFiles = resolved.Files
		scanCtx.TotalFiles = resolved.TotalFiles
		scanCtx.TotalSizeBytes = resolved.TotalSize

		// Export tool definitions for Cisco scanner
		if s.serverInfo != nil {
			s.exportToolDefinitions(serverName, req.SourceDir)
		}
	} else {
		s.logger.Warn("No server info available for Pass 2, skipping",
			zap.String("server", serverName),
		)
		return
	}

	req.ScanContext = scanCtx

	callback := &scanCallbackAdapter{
		service:  s,
		cleanup:  resolvedCleanup,
		scanPass: ScanPassSupplyChainAudit,
	}

	_, err := s.engine.StartScan(ctx, req, callback)
	if err != nil {
		s.logger.Warn("Failed to start Pass 2 scan",
			zap.String("server", serverName),
			zap.Error(err),
		)
		// If the engine rejected (e.g., scan already in progress for same server),
		// just log and move on. Pass 2 is best-effort.
	}
}

// saveFailedPass2Job creates a failed ScanJob for Pass 2 so the UI knows it was attempted.
func (s *Service) saveFailedPass2Job(serverName, errMsg string) {
	job := &ScanJob{
		ID:          fmt.Sprintf("scan-%s-pass2-%d", serverName, time.Now().UnixNano()),
		ServerName:  serverName,
		Status:      ScanJobStatusFailed,
		ScanPass:    ScanPassSupplyChainAudit,
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
		Error:       errMsg,
	}
	_ = s.storage.SaveScanJob(job)
}

// GetScanStatus returns the current scan status for a server.
// Prefers Pass 1 (security scan) which contains the primary scanner execution data.
// Pass 2 (supply chain audit) is only returned if Pass 1 is not available.
func (s *Service) GetScanStatus(ctx context.Context, serverName string) (*ScanJob, error) {
	// Check for active scan first
	if active := s.engine.GetActiveJob(serverName); active != nil {
		return active, nil
	}

	// Prefer Pass 1 — it has the primary security scan results and scanner execution logs.
	// Pass 2 is a background follow-up (supply chain audit) with different scope.
	pass1Job, pass2Job, err := s.findLatestPassJobs(serverName)
	if err != nil {
		return s.storage.GetLatestScanJob(serverName)
	}

	if pass1Job != nil {
		return pass1Job, nil
	}

	if pass2Job != nil {
		return pass2Job, nil
	}

	return s.storage.GetLatestScanJob(serverName)
}

// GetScanStatusByPass returns the scan job for a specific pass (1=security, 2=supply chain).
// If pass is 0 or not found, falls back to GetScanStatus behavior (latest job).
func (s *Service) GetScanStatusByPass(ctx context.Context, serverName string, pass int) (*ScanJob, error) {
	if pass == 0 {
		return s.GetScanStatus(ctx, serverName)
	}

	pass1Job, pass2Job, err := s.findLatestPassJobs(serverName)
	if err != nil {
		return nil, err
	}

	switch pass {
	case ScanPassSecurityScan:
		if pass1Job != nil {
			return pass1Job, nil
		}
	case ScanPassSupplyChainAudit:
		if pass2Job != nil {
			return pass2Job, nil
		}
	}

	// Fall back to latest job
	return s.GetScanStatus(ctx, serverName)
}

// GetScanReport returns the aggregated report for a server, merging both Pass 1 and Pass 2 results.
func (s *Service) GetScanReport(ctx context.Context, serverName string) (*AggregatedReport, error) {
	// Find the latest Pass 1 and Pass 2 jobs
	pass1Job, pass2Job, err := s.findLatestPassJobs(serverName)
	if err != nil {
		return nil, fmt.Errorf("no scan found for server %s: %w", serverName, err)
	}

	// Build report from Pass 1
	var allReports []*ScanReport
	var primaryJob *ScanJob

	if pass1Job != nil {
		primaryJob = pass1Job
		reports, err := s.storage.ListScanReportsByJob(pass1Job.ID)
		if err == nil {
			// Tag findings with scan pass
			for _, r := range reports {
				for i := range r.Findings {
					r.Findings[i].ScanPass = ScanPassSecurityScan
				}
			}
			allReports = append(allReports, reports...)
		}
	}

	// Merge Pass 2 findings if available
	if pass2Job != nil && pass2Job.Status == ScanJobStatusCompleted {
		if primaryJob == nil {
			primaryJob = pass2Job
		}
		reports, err := s.storage.ListScanReportsByJob(pass2Job.ID)
		if err == nil {
			// Tag findings with scan pass
			for _, r := range reports {
				for i := range r.Findings {
					r.Findings[i].ScanPass = ScanPassSupplyChainAudit
				}
			}
			allReports = append(allReports, reports...)
		}
	}

	if primaryJob == nil {
		return nil, fmt.Errorf("no scan found for server %s", serverName)
	}

	// Deduplicate Pass 2 findings that overlap with Pass 1 (e.g., trivy scanning same lockfiles)
	allReports = deduplicatePass2Findings(allReports)

	agg := AggregateReportsWithJobStatus(primaryJob.ID, serverName, allReports, primaryJob)

	// Set pass completion status
	agg.Pass1Complete = pass1Job != nil && pass1Job.Status == ScanJobStatusCompleted
	agg.Pass2Complete = pass2Job != nil && pass2Job.Status == ScanJobStatusCompleted

	// Check if Pass 2 is currently running
	if activeJob := s.engine.GetActiveJob(serverName); activeJob != nil && activeJob.ScanPass == ScanPassSupplyChainAudit {
		agg.Pass2Running = true
	}

	// Attach scan context from primary job
	agg.ScanContext = primaryJob.ScanContext
	agg.ScannerStatuses = primaryJob.ScannerStatuses

	return agg, nil
}

// CleanupStaleJobs marks any running/pending scan jobs as failed.
// Called on startup to clean up jobs that were interrupted by a process crash.
func (s *Service) CleanupStaleJobs() {
	jobs, err := s.storage.ListScanJobs("")
	if err != nil {
		s.logger.Warn("failed to list scan jobs for stale cleanup", zap.Error(err))
		return
	}

	cleaned := 0
	for _, job := range jobs {
		if job.Status == ScanJobStatusRunning || job.Status == ScanJobStatusPending {
			job.Status = ScanJobStatusFailed
			job.Error = "interrupted by server restart"
			job.CompletedAt = time.Now()
			if err := s.storage.SaveScanJob(job); err != nil {
				s.logger.Warn("failed to clean up stale scan job",
					zap.String("job_id", job.ID),
					zap.Error(err),
				)
			} else {
				cleaned++
			}
		}
	}

	if cleaned > 0 {
		s.logger.Info("cleaned up stale scan jobs on startup",
			zap.Int("count", cleaned),
		)
	}
}

// ListScanHistory returns all scan jobs as summaries, enriched with findings count and risk score.
func (s *Service) ListScanHistory(ctx context.Context) ([]ScanJobSummary, error) {
	jobs, err := s.storage.ListScanJobs("")
	if err != nil {
		return nil, fmt.Errorf("failed to list scan jobs: %w", err)
	}

	summaries := make([]ScanJobSummary, 0, len(jobs))
	for _, job := range jobs {
		summary := ScanJobSummary{
			ID:          job.ID,
			ServerName:  job.ServerName,
			Status:      job.Status,
			ScanPass:    job.ScanPass,
			StartedAt:   job.StartedAt,
			CompletedAt: job.CompletedAt,
			Scanners:    job.Scanners,
		}

		// Use findings count from scanner statuses (already on the job — no extra DB reads)
		for _, ss := range job.ScannerStatuses {
			summary.FindingsCount += ss.FindingsCount
		}

		summaries = append(summaries, summary)
	}

	return summaries, nil
}

// GetScanReportByJobID returns an aggregated report for a specific scan job ID.
func (s *Service) GetScanReportByJobID(ctx context.Context, jobID string) (*AggregatedReport, error) {
	job, err := s.storage.GetScanJob(jobID)
	if err != nil {
		return nil, fmt.Errorf("scan job not found: %w", err)
	}

	reports, err := s.storage.ListScanReportsByJob(jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get reports for job %s: %w", jobID, err)
	}

	// Tag findings with scan pass
	scanPass := ScanPassSecurityScan
	if job.ScanPass == ScanPassSupplyChainAudit {
		scanPass = ScanPassSupplyChainAudit
	}
	for _, r := range reports {
		for i := range r.Findings {
			r.Findings[i].ScanPass = scanPass
		}
	}

	agg := AggregateReportsWithJobStatus(job.ID, job.ServerName, reports, job)
	agg.Pass1Complete = job.ScanPass == ScanPassSecurityScan && job.Status == ScanJobStatusCompleted
	agg.Pass2Complete = job.ScanPass == ScanPassSupplyChainAudit && job.Status == ScanJobStatusCompleted

	// If this is a Pass 1 job, try to find and merge companion Pass 2 results
	if job.ScanPass == ScanPassSecurityScan || job.ScanPass == 0 {
		allJobs, _ := s.storage.ListScanJobs(job.ServerName)
		for _, j := range allJobs {
			if j.ScanPass == ScanPassSupplyChainAudit && j.Status == ScanJobStatusCompleted && j.StartedAt.After(job.StartedAt) {
				pass2Reports, err := s.storage.ListScanReportsByJob(j.ID)
				if err == nil {
					for _, r := range pass2Reports {
						for i := range r.Findings {
							r.Findings[i].ScanPass = ScanPassSupplyChainAudit
						}
					}
					allMerged := append(reports, pass2Reports...)
					allMerged = deduplicatePass2Findings(allMerged)
					agg = AggregateReportsWithJobStatus(job.ID, job.ServerName, allMerged, job)
					agg.Pass1Complete = true
					agg.Pass2Complete = true
				}
				break
			}
		}

		// Check if Pass 2 is running
		if activeJob := s.engine.GetActiveJob(job.ServerName); activeJob != nil && activeJob.ScanPass == ScanPassSupplyChainAudit {
			agg.Pass2Running = true
		}
	}

	// Attach scan context and scanner execution logs from job
	agg.ScanContext = job.ScanContext
	agg.ScannerStatuses = job.ScannerStatuses

	return agg, nil
}

// deduplicatePass2Findings removes Pass 2 findings that duplicate Pass 1 findings.
// The dedup key is scanner + rule_id + title (not location, since paths may differ between passes).
func deduplicatePass2Findings(reports []*ScanReport) []*ScanReport {
	// Build a set of Pass 1 finding keys
	pass1Keys := make(map[string]struct{})
	for _, r := range reports {
		for _, f := range r.Findings {
			if f.ScanPass == ScanPassSecurityScan {
				key := f.Scanner + "|" + f.RuleID + "|" + f.Title
				pass1Keys[key] = struct{}{}
			}
		}
	}

	// If no Pass 1 findings, nothing to deduplicate
	if len(pass1Keys) == 0 {
		return reports
	}

	// Filter Pass 2 findings, removing duplicates
	for _, r := range reports {
		filtered := r.Findings[:0]
		for _, f := range r.Findings {
			if f.ScanPass == ScanPassSupplyChainAudit {
				key := f.Scanner + "|" + f.RuleID + "|" + f.Title
				if _, dup := pass1Keys[key]; dup {
					continue // Skip Pass 2 finding that duplicates Pass 1
				}
			}
			filtered = append(filtered, f)
		}
		r.Findings = filtered
	}

	return reports
}

// findLatestPassJobs finds the latest Pass 1 and Pass 2 jobs for a server.
// Returns (pass1Job, pass2Job, error). At least one must be non-nil on success.
func (s *Service) findLatestPassJobs(serverName string) (*ScanJob, *ScanJob, error) {
	jobs, err := s.storage.ListScanJobs(serverName)
	if err != nil || len(jobs) == 0 {
		return nil, nil, fmt.Errorf("no scan jobs found for server: %s", serverName)
	}

	// Sort by start time descending (newest first)
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].StartedAt.After(jobs[j].StartedAt)
	})

	var pass1Job, pass2Job *ScanJob
	for _, j := range jobs {
		if j.ScanPass == ScanPassSupplyChainAudit && pass2Job == nil {
			pass2Job = j
		} else if (j.ScanPass == ScanPassSecurityScan || j.ScanPass == 0) && pass1Job == nil {
			// ScanPass == 0 handles legacy jobs (before two-pass was added)
			pass1Job = j
		}
		if pass1Job != nil && pass2Job != nil {
			break
		}
	}

	if pass1Job == nil && pass2Job == nil {
		return nil, nil, fmt.Errorf("no scan jobs found for server: %s", serverName)
	}

	return pass1Job, pass2Job, nil
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

	// Actually unquarantine the server: clear the flag in storage, persist
	// config, trigger a tool (re)index, and emit the same events the normal
	// unquarantine path emits. This is the primary user-visible effect of
	// approval — without it, the server stays quarantined forever and the
	// approval is cosmetic.
	if s.unquarantiner != nil {
		if err := s.unquarantiner.UnquarantineServer(serverName); err != nil {
			// Report the error to the caller but keep the baseline we just
			// saved — the caller can retry via the normal unquarantine path.
			s.logger.Error("Failed to unquarantine server after approval",
				zap.String("server", serverName),
				zap.Error(err),
			)
			return fmt.Errorf("failed to unquarantine server %q after saving baseline: %w", serverName, err)
		}
	} else {
		s.logger.Warn("ApproveServer: no unquarantiner configured; server will not be unquarantined automatically",
			zap.String("server", serverName),
		)
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
			s.emit().EmitSecurityIntegrityAlert(serverName, "digest_mismatch", "re-quarantine")
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

	// Count installed scanners. ScannersInstalled is the total number of
	// scanners persisted in storage; ScannersEnabled is the subset the engine
	// will actually run (status installed or configured). UI uses
	// ScannersEnabled to decide whether to show scan-trigger buttons.
	scanners, err := s.storage.ListScanners()
	if err == nil {
		overview.ScannersInstalled = len(scanners)
		for _, sc := range scanners {
			if sc.Status == ScannerStatusInstalled || sc.Status == ScannerStatusConfigured {
				overview.ScannersEnabled++
			}
		}
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
			for i, f := range r.Findings {
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

				// Classify threat level if not already set (stored findings may lack it)
				if f.ThreatLevel == "" {
					ClassifyThreat(&r.Findings[i])
					f = r.Findings[i]
				}
				switch f.ThreatLevel {
				case ThreatLevelDangerous:
					overview.FindingsBySeverity.Dangerous++
				case ThreatLevelWarning:
					overview.FindingsBySeverity.Warnings++
				case ThreatLevelInfo:
					overview.FindingsBySeverity.InfoLevel++
				}
			}
		}
	}

	// Check Docker availability
	overview.DockerAvailable = s.docker.IsDockerAvailable(ctx)

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
// Considers both Pass 1 and Pass 2 results when computing status.
func (s *Service) GetScanSummary(ctx context.Context, serverName string) *ScanSummary {
	// Check cache first (avoids expensive BBolt reads for every server)
	s.summaryCacheMu.RLock()
	if cached, ok := s.summaryCache[serverName]; ok {
		s.summaryCacheMu.RUnlock()
		return cached
	}
	s.summaryCacheMu.RUnlock()

	// Check for active scan (Pass 1 takes priority in status display)
	if active := s.engine.GetActiveJob(serverName); active != nil {
		if active.ScanPass == ScanPassSecurityScan {
			return &ScanSummary{
				RiskScore: 0,
				Status:    "scanning",
			}
		}
		// Pass 2 running in background: show results from Pass 1 if available
	}

	// Find latest Pass 1 and Pass 2 jobs
	pass1Job, pass2Job, err := s.findLatestPassJobs(serverName)
	if err != nil {
		return nil // No scans run
	}

	// Use Pass 1 job as primary for timestamp
	primaryJob := pass1Job
	if primaryJob == nil {
		primaryJob = pass2Job
	}

	summary := &ScanSummary{
		LastScanAt: &primaryJob.StartedAt,
		Status:     "clean",
	}

	// Check if the primary job failed
	if primaryJob.Status == ScanJobStatusFailed {
		summary.Status = "failed"
		s.cacheScanSummary(serverName, summary)
		return summary
	}

	// Check scanner statuses on primary job
	if len(primaryJob.ScannerStatuses) > 0 {
		allFailed := true
		for _, ss := range primaryJob.ScannerStatuses {
			if ss.Status == ScanJobStatusCompleted {
				allFailed = false
				break
			}
		}
		if allFailed {
			summary.Status = "failed"
			s.cacheScanSummary(serverName, summary)
			return summary
		}
	}

	// Collect reports from both passes
	var allFindings []ScanFinding

	if pass1Job != nil {
		reports, err := s.storage.ListScanReportsByJob(pass1Job.ID)
		if err == nil {
			for _, r := range reports {
				allFindings = append(allFindings, r.Findings...)
			}
		}
	}

	if pass2Job != nil && pass2Job.Status == ScanJobStatusCompleted {
		reports, err := s.storage.ListScanReportsByJob(pass2Job.ID)
		if err == nil {
			for _, r := range reports {
				allFindings = append(allFindings, r.Findings...)
			}
		}
	}

	if len(allFindings) == 0 {
		if primaryJob.Status == ScanJobStatusCompleted {
			// Check if there are actually any reports
			reports, _ := s.storage.ListScanReportsByJob(primaryJob.ID)
			if len(reports) == 0 {
				summary.Status = "failed"
			}
			// Detect empty scans: scanners ran but had no files to analyze.
			// Without this, the server list shows "clean" when nothing was scanned.
			// Valid when: URL scan, tool_definitions_only, or tools were exported.
			if primaryJob.ScanContext != nil && primaryJob.ScanContext.TotalFiles == 0 &&
				primaryJob.ScanContext.SourceMethod != "url" &&
				primaryJob.ScanContext.SourceMethod != "tool_definitions_only" &&
				primaryJob.ScanContext.ToolsExported == 0 {
				summary.Status = "failed"
			}
		}
		s.cacheScanSummary(serverName, summary)
		return summary
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

	// Cache for fast subsequent reads
	s.cacheScanSummary(serverName, summary)
	return summary
}

// ScanSummary is a compact representation of scan status for the server list.
type ScanSummary struct {
	LastScanAt    *time.Time     `json:"last_scan_at,omitempty"`
	RiskScore     int            `json:"risk_score"`
	Status        string         `json:"status"` // clean, warnings, dangerous, failed, not_scanned, scanning
	FindingCounts *FindingCounts `json:"finding_counts,omitempty"`
}

// FindingCounts groups findings by user-facing threat level.
type FindingCounts struct {
	Dangerous int `json:"dangerous"`
	Warning   int `json:"warning"`
	Info      int `json:"info"`
	Total     int `json:"total"`
}

// invalidateScanSummaryCache removes a server's cached scan summary,
// forcing the next GetScanSummary call to recompute from storage.
func (s *Service) invalidateScanSummaryCache(serverName string) {
	s.summaryCacheMu.Lock()
	delete(s.summaryCache, serverName)
	s.summaryCacheMu.Unlock()
}

// cacheScanSummary stores a computed scan summary in the cache.
func (s *Service) cacheScanSummary(serverName string, summary *ScanSummary) {
	if summary == nil {
		return
	}
	s.summaryCacheMu.Lock()
	s.summaryCache[serverName] = summary
	s.summaryCacheMu.Unlock()
}

// waitForConnection polls IsConnected until the server connects or the timeout expires.
func (s *Service) waitForConnection(serverName string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.serverInfo.IsConnected(serverName) {
			s.logger.Info("Server connected for scan",
				zap.String("server", serverName))
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	s.logger.Warn("Timed out waiting for server to connect for scan",
		zap.String("server", serverName),
		zap.Duration("timeout", timeout))
}

// exportToolDefinitions writes a tools.json file to the source directory
// so the Cisco MCP Scanner can analyze tool descriptions for poisoning attacks.
// Returns the number of tools exported.
func (s *Service) exportToolDefinitions(serverName, sourceDir string) int {
	tools, err := s.serverInfo.GetServerTools(serverName)
	if err != nil {
		s.logger.Warn("Could not export tool definitions for scanning",
			zap.String("server", serverName), zap.Error(err))
		return 0
	}
	if len(tools) == 0 {
		return 0
	}

	// Format as MCP tools/list output
	toolsData := map[string]interface{}{
		"tools": tools,
	}
	data, err := json.MarshalIndent(toolsData, "", "  ")
	if err != nil {
		return 0
	}

	toolsPath := filepath.Join(sourceDir, "tools.json")
	if err := os.WriteFile(toolsPath, data, 0644); err != nil {
		s.logger.Debug("Failed to write tools.json", zap.Error(err))
		return 0
	}
	s.logger.Info("Exported tool definitions for scanning",
		zap.String("server", serverName),
		zap.Int("tools", len(tools)),
		zap.String("path", toolsPath),
	)
	return len(tools)
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

// --- Batch Scan (Scan All) ---

// ScanAll starts scanning all eligible servers using the worker pool.
// Disabled servers are skipped with a reason.
func (s *Service) ScanAll(ctx context.Context, servers []ServerStatus, scannerIDs []string) (*QueueProgress, error) {
	scanFunc := func(ctx context.Context, serverName string) (*ScanJob, error) {
		return s.StartScan(ctx, serverName, false, scannerIDs, "")
	}
	return s.queue.StartScanAll(servers, scanFunc)
}

// GetQueueProgress returns the current batch scan progress
func (s *Service) GetQueueProgress() *QueueProgress {
	return s.queue.GetProgress()
}

// CancelAllScans cancels the current batch scan
func (s *Service) CancelAllScans() error {
	return s.queue.CancelAll()
}

// IsQueueRunning returns true if a batch scan is in progress
func (s *Service) IsQueueRunning() bool {
	return s.queue.IsRunning()
}
