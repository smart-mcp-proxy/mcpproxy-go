package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/stringutil"
)

// SecretResolverFunc resolves a secret reference like ${keyring:name} to its value
type SecretResolverFunc func(ctx context.Context, ref string) (string, error)

// Engine orchestrates parallel scanner execution for a server
type Engine struct {
	docker         *DockerRunner
	registry       *Registry
	logger         *zap.Logger
	dataDir        string
	secretResolver SecretResolverFunc

	// disableNoNewPrivileges controls whether scanner containers are
	// launched with `--security-opt no-new-privileges`. Default (false)
	// preserves the historical hardened behaviour; set to true via
	// Service.SetScannerDisableNoNewPrivileges on hosts where the snap
	// docker × AppArmor transition would otherwise deny entrypoint exec.
	disableNoNewPrivileges bool

	// runScannerContainer runs a scanner container. Nil means
	// docker.RunScanner; tests replace it to observe the run config and to
	// act while the container "runs".
	runScannerContainer func(ctx context.Context, cfg ScannerRunConfig) (stdout, stderr string, exitCode int, err error)

	// isolationMode is the engine-wide DEFAULT isolation mode ("docker",
	// "sandbox", "none", or "" == docker for back-compat), used only when a
	// ScanRequest does not carry a per-server IsolationMode. The per-server
	// resolved mode on the request takes precedence (MCP-34.4) so a server with
	// an explicit isolation.mode override gets the right scanner behaviour.
	// Scanner *plugins* are Docker-based (Spec 039); under "sandbox"/"none" the
	// host runs no Docker for scanners, so Docker-based scanners are cleanly
	// skipped (prefailed with an honest reason) while in-process scanners still
	// run — D3 option (b). Set via Service.SetIsolationMode.
	isolationMode string

	// deepScanEnabled gates the opt-in "deep scan" layer (Spec 077 US3): the
	// Docker-based scanner plugins plus published-package-source extraction.
	// Default false (FR-006) — only the deterministic in-process baseline scanner
	// runs, with zero Docker invoked. When false, resolveScanners drops every
	// Docker-based (non-in-process) scanner entirely so no container is launched
	// and no deep-scanner failure can degrade the baseline verdict (FR-007/008).
	// Set via Service.SetDeepScan from security.deep_scan.enabled.
	deepScanEnabled bool

	// deepScanScanners optionally restricts which deep scanners may run when
	// deepScanEnabled is true (security.deep_scan.scanners). Nil/empty ⇒ every
	// enabled deep scanner is eligible. Ignored when deepScanEnabled is false.
	deepScanScanners map[string]bool

	// Track active scans (one per server)
	mu          sync.Mutex
	activeScans map[string]*ScanJob // keyed by server name

	// registrationCheck, when set, reports whether a job bound to
	// incarnation inc may still register for serverName (Spec 116 FR-014a,
	// review round 2). It runs under mu, so it is ordered against
	// DetachObsoleteJob: the purge observer runs after the removal published,
	// so a job registered before the observer is detached by it, and one
	// registering after it sees the new incarnation and is refused. It must
	// not take a lock that is held while calling into the engine.
	registrationCheck func(serverName string, inc int64) bool
}

// deepScanAllowed reports whether a scanner is eligible to run given the
// deep-scan gate (Spec 077 US3). The in-process baseline scanner is ALWAYS
// allowed — it is the zero-dependency default. A Docker-based (deep) scanner is
// allowed only when the deep-scan layer is enabled and, when a per-scanner
// allow-list is configured, the scanner is on it.
// setDeepScan replaces the deep-scan switch and allow-list under e.mu. The
// config hot-reload path (Service.SetDeepScan via ApplySecurityConfig) runs on
// the routing-mode-refresh goroutine while StartScan reads the same fields
// under e.mu in resolveScanners, so the writer must take the lock too.
func (e *Engine) setDeepScan(enabled bool, allow map[string]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deepScanEnabled = enabled
	e.deepScanScanners = allow
}

// deepScanIsEnabled reads the deep-scan switch under e.mu for callers outside
// the StartScan critical section.
func (e *Engine) deepScanIsEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deepScanEnabled
}

func (e *Engine) deepScanAllowed(s *ScannerPlugin) bool {
	if s.InProcess {
		return true
	}
	if !e.deepScanEnabled {
		return false
	}
	if len(e.deepScanScanners) == 0 {
		return true
	}
	return e.deepScanScanners[s.ID]
}

// NewEngine creates a new scan orchestration engine
func NewEngine(docker *DockerRunner, registry *Registry, dataDir string, logger *zap.Logger) *Engine {
	return &Engine{
		docker:      docker,
		registry:    registry,
		logger:      logger,
		dataDir:     dataDir,
		activeScans: make(map[string]*ScanJob),
	}
}

// ScanRequest describes a scan to execute
type ScanRequest struct {
	ServerName string
	// Incarnation is the scanned server's incarnation when the scan was
	// requested (Spec 116 FR-014a); the job carries it.
	Incarnation    int64
	SourceDir      string            // Path to server source files (for "source" input)
	ContainerImage string            // Docker image reference (for "container_image" input)
	DryRun         bool              // If true, don't affect quarantine state
	ScannerIDs     []string          // Specific scanners to use (empty = all installed)
	Env            map[string]string // Additional environment variables
	ScanContext    *ScanContext      // Context metadata (set by service)
	ScanPass       int               // 1 = security scan (fast), 2 = supply chain audit (background)
	// IsolationMode is THIS server's resolved isolation mode ("docker",
	// "sandbox", "none", or "" to fall back to the engine's service-wide
	// default). Set per-server by the Service so the Docker-scanner skip
	// (MCP-34.4) follows the scanned server's effective mode — a per-server
	// isolation.mode override beats the global mode (internal/upstream/core
	// IsolationManager.ResolveMode). Under "sandbox"/"none" Docker scanner
	// plugins are skipped; under "docker" they run.
	IsolationMode string
	// PeerTools is a cross-server snapshot (other servers' current tool
	// definitions, keyed by server name) used by the in-process tpa-descriptions
	// scanner to build a multi-server RegistryView so the deterministic
	// shadowing.cross_server check can detect impersonation/collisions across
	// servers. Populated by the Service; nil for callers that don't supply it.
	PeerTools map[string][]map[string]interface{}
	// ToolDefinitions is the exact tools.json document the Service exported
	// for this scan (RT-06, Sol r4 P1): an immutable in-memory snapshot of the
	// live + ingested contracts. The in-process baseline scanner analyses it
	// instead of re-reading SourceDir/tools.json, which an upstream that can
	// write its own source dir could swap for clean metadata between export
	// and the asynchronous scanner read. Docker scanners still read the file
	// from the mounted source dir; their result is discarded when the file no
	// longer matches this snapshot. Nil for callers that export nothing.
	ToolDefinitions []byte
	// ToolDefinitionsUnavailable marks a scan whose tool definitions could not
	// be exported (Pass 2 only, Sol r4 P2): the source dir may hold a
	// tools.json this pass did not write, so every scanner that may analyse
	// it is failed, while scanners independent of tool metadata (supply-chain
	// / CVE analysis) still run.
	ToolDefinitionsUnavailable bool
}

// toolMetadataIndependentScanners are the scanners whose analysis never uses
// the exported tool definitions (tools.json): they audit dependencies and
// images only, so they may run when the definitions could not be exported.
var toolMetadataIndependentScanners = map[string]bool{
	"trivy-mcp": true,
}

// toolDefinitionsGate decides, before a Docker scanner runs, whether the
// tools.json it would read may be analysed: not at all when the definitions
// could not be exported (only metadata-independent scanners run, Sol r4 P2),
// and only while it still holds the exported snapshot (Sol r4 P1). A scanner
// independent of tool metadata never reads tools.json, so a later change to
// the file must not fail or discard its CVE audit (Sol r5 P2).
func toolDefinitionsGate(s *ScannerPlugin, req ScanRequest) error {
	_, err := pinToolDefinitions(s, req)
	return err
}

// pinToolDefinitions is toolDefinitionsGate that also returns the identity of
// the tools.json it checked (nil when there is nothing to pin), so a caller
// can tell whether the file was replaced later (Sol r20 P2).
func pinToolDefinitions(s *ScannerPlugin, req ScanRequest) (os.FileInfo, error) {
	if toolMetadataIndependentScanners[s.ID] {
		return nil, nil
	}
	if req.ToolDefinitionsUnavailable {
		return nil, fmt.Errorf("scanner %s skipped: the tool definitions could not be exported for this scan, and the source dir may hold a tools.json this scan did not write", s.ID)
	}
	info, err := checkToolDefinitionsIntact(req)
	if err != nil {
		return nil, fmt.Errorf("scanner %s: %w", s.ID, err)
	}
	return info, nil
}

// toolDefinitionsReplaced reports why the tools.json in the source dir is no
// longer the pinned file (a symlink, another file, or gone), or "" when it
// still is. The scanner reads a staged copy, so a swap cannot reach it, but a
// symlink retargeted or another file swapped in (even with the bytes restored
// before the post-run check) is tampering, and the result is discarded
// (Sol r20 P2).
func toolDefinitionsReplaced(req ScanRequest, pinned os.FileInfo) string {
	if pinned == nil {
		return ""
	}
	cur, err := os.Lstat(filepath.Join(req.SourceDir, "tools.json"))
	switch {
	case err != nil:
		return "tools.json was removed during the scan"
	case !cur.Mode().IsRegular():
		return "tools.json became a symlink or special file during the scan"
	case !os.SameFile(pinned, cur):
		return "tools.json was replaced by another file during the scan"
	}
	return ""
}

// toolDefinitionsWatchInterval is how often the source dir's tools.json is
// checked against the pinned file while a Docker scanner runs.
const toolDefinitionsWatchInterval = 20 * time.Millisecond

// watchToolDefinitions checks, until stop is closed, that tools.json stays the
// pinned regular file, and returns the first violation seen ("" if none). The
// post-run check alone cannot see a swap that is undone before the run ends.
func watchToolDefinitions(req ScanRequest, pinned os.FileInfo, stop <-chan struct{}) <-chan string {
	out := make(chan string, 1)
	if pinned == nil {
		out <- ""
		return out
	}
	go func() {
		ticker := time.NewTicker(toolDefinitionsWatchInterval)
		defer ticker.Stop()
		for {
			if why := toolDefinitionsReplaced(req, pinned); why != "" {
				out <- why
				return
			}
			select {
			case <-stop:
				out <- toolDefinitionsReplaced(req, pinned)
				return
			case <-ticker.C:
			}
		}
	}()
	return out
}

// checkToolDefinitionsIntact reports an error when the tools.json in the
// scanned source dir no longer holds the exported snapshot (RT-06, Sol r4 P1),
// or is not a regular file (Sol r20 P2): a symlinked tools.json in the
// upstream's source dir is refused outright. It returns the identity of the checked file (nil when nothing is checked).
func checkToolDefinitionsIntact(req ScanRequest) (os.FileInfo, error) {
	if len(req.ToolDefinitions) == 0 || req.SourceDir == "" {
		return nil, nil
	}
	path := filepath.Join(req.SourceDir, "tools.json")
	linfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("exported tool definitions are no longer readable: %w", err)
	}
	if !linfo.Mode().IsRegular() {
		return nil, fmt.Errorf("exported tool definitions (tools.json) are not a regular file (a symlink or special file); the result is discarded")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("exported tool definitions are no longer readable: %w", err)
	}
	defer f.Close()
	finfo, err := f.Stat()
	if err != nil || !os.SameFile(linfo, finfo) {
		return nil, fmt.Errorf("exported tool definitions (tools.json) were replaced while being checked; the result is discarded")
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("exported tool definitions are no longer readable: %w", err)
	}
	if !bytes.Equal(data, req.ToolDefinitions) {
		return nil, fmt.Errorf("exported tool definitions (tools.json) were modified after export; the result is discarded")
	}
	return finfo, nil
}

// ScanCallback receives scan lifecycle events
type ScanCallback interface {
	OnScanStarted(job *ScanJob)
	OnScannerStarted(job *ScanJob, scannerID string)
	OnScannerCompleted(job *ScanJob, scannerID string, report *ScanReport)
	OnScannerFailed(job *ScanJob, scannerID string, err error)
	OnScanCompleted(job *ScanJob, reports []*ScanReport)
	OnScanFailed(job *ScanJob, err error)
}

// NoopCallback is a no-op implementation of ScanCallback
type NoopCallback struct{}

func (n *NoopCallback) OnScanStarted(_ *ScanJob)                               {}
func (n *NoopCallback) OnScannerStarted(_ *ScanJob, _ string)                  {}
func (n *NoopCallback) OnScannerCompleted(_ *ScanJob, _ string, _ *ScanReport) {}
func (n *NoopCallback) OnScannerFailed(_ *ScanJob, _ string, _ error)          {}
func (n *NoopCallback) OnScanCompleted(_ *ScanJob, _ []*ScanReport)            {}
func (n *NoopCallback) OnScanFailed(_ *ScanJob, _ error)                       {}

// ErrScanInProgress is returned (wrapped) by StartScan when the server already
// has a registered scan job.
var ErrScanInProgress = errors.New("scan already in progress")

// StartScan begins a scan of the specified server
// Returns the scan job immediately; scanning runs in the background
func (e *Engine) StartScan(_ context.Context, req ScanRequest, callback ScanCallback) (*ScanJob, error) {
	// Detached on purpose: the HTTP request context that usually reaches here
	// may be cancelled once the response is sent, which must not kill the scan.
	return e.startScan(context.Background(), req, callback)
}

// StartScanBound is StartScan with a caller-owned execution context: the scan
// runs under execCtx instead of a detached one, so the caller's lifecycle
// (a sweep, a shutdown) ends it. The returned job's Done channel closes once
// the scan goroutine has returned, i.e. after its completion callback ran, so
// the caller can join the worker before the stores it writes to close.
func (e *Engine) StartScanBound(execCtx context.Context, req ScanRequest, callback ScanCallback) (*ScanJob, error) {
	return e.startScan(execCtx, req, callback)
}

func (e *Engine) startScan(execCtx context.Context, req ScanRequest, callback ScanCallback) (*ScanJob, error) {
	// A lifecycle that already ended starts nothing (no job is registered, no
	// callback fires), so a stopping caller leaves no record behind.
	if err := execCtx.Err(); err != nil {
		return nil, err
	}
	if callback == nil {
		callback = &NoopCallback{}
	}

	// Check for existing scan
	e.mu.Lock()
	if existing, ok := e.activeScans[req.ServerName]; ok && e.obsoleteLocked(existing) {
		// The slot is held by a removed server's job whose purge observer
		// has not detached it yet (review round 4): detach it here, exactly
		// as DetachObsoleteJob would, so the replacement can register.
		delete(e.activeScans, req.ServerName)
	}
	if existing, ok := e.activeScans[req.ServerName]; ok {
		// Snapshot: the running scan keeps writing to the live record.
		snapshot := existing.clone()
		e.mu.Unlock()
		return snapshot, fmt.Errorf("%w for server %s (job %s)", ErrScanInProgress, req.ServerName, snapshot.ID)
	}
	if check := e.registrationCheck; check != nil && !check(req.ServerName, req.Incarnation) {
		e.mu.Unlock()
		return nil, fmt.Errorf("scan for server %s refused: the server was removed or replaced since the scan was prepared", req.ServerName)
	}

	// Determine which scanners to use. The Docker-scanner skip (MCP-34.4)
	// follows THIS server's resolved isolation mode (per-server override beats
	// the global default); fall back to the engine-wide default when the
	// request didn't carry one.
	resolved, err := e.resolveScanners(req.ScannerIDs, e.effectiveIsolationMode(req.IsolationMode))
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}

	if len(resolved) == 0 {
		e.mu.Unlock()
		return nil, fmt.Errorf("no scanners available; install scanners with 'mcpproxy security install <scanner-id>'")
	}

	// Create job
	scanPass := req.ScanPass
	if scanPass == 0 {
		scanPass = ScanPassSecurityScan // Default to pass 1
	}
	job := &ScanJob{
		ID:          fmt.Sprintf("scan-%s-%d", req.ServerName, time.Now().UnixNano()),
		ServerName:  req.ServerName,
		Status:      ScanJobStatusRunning,
		ScanPass:    scanPass,
		Scanners:    make([]string, len(resolved)),
		StartedAt:   time.Now(),
		DryRun:      req.DryRun,
		ScanContext: req.ScanContext,
		Incarnation: req.Incarnation,
	}
	finish := job.TrackCompletion()
	for i, rs := range resolved {
		job.Scanners[i] = rs.plugin.ID
		job.ScannerStatuses = append(job.ScannerStatuses, ScannerJobStatus{
			ScannerID: rs.plugin.ID,
			Status:    ScanJobStatusPending,
		})
	}

	e.activeScans[req.ServerName] = job
	// Snapshot before releasing the lock: from here on the scan goroutines own
	// the live record, so neither the callback nor the caller may hold it.
	started := job.clone()
	returned := job.clone()
	e.mu.Unlock()

	callback.OnScanStarted(started)

	// Run scanners in the background under execCtx (detached for StartScan).
	go func() {
		defer finish()
		e.executeScan(execCtx, job, resolved, req, callback)
	}()

	return returned, nil
}

// CancelScan cancels a running scan for a server.
//
// A job stays in activeScans for a short window AFTER executeScan has written
// its terminal status — the completion callback persists the report and emits
// the completion event inside that window. Cancelling there must fail rather
// than succeed: the terminal outcome has already been cloned and is on its way
// to storage, so flipping Status to cancelled would leave the API reporting a
// cancellation that the stored job contradicts.
func (e *Engine) CancelScan(serverName string) error {
	e.mu.Lock()
	job, ok := e.activeScans[serverName]
	if !ok || e.obsoleteLocked(job) {
		e.mu.Unlock()
		return fmt.Errorf("no active scan for server %s", serverName)
	}
	if job.Status == ScanJobStatusCompleted || job.Status == ScanJobStatusFailed ||
		job.Status == ScanJobStatusCancelled {
		status := job.Status
		e.mu.Unlock()
		return fmt.Errorf("scan for server %s already finished (status: %s)", serverName, status)
	}
	job.Status = ScanJobStatusCancelled
	job.CompletedAt = time.Now()
	delete(e.activeScans, serverName)
	e.mu.Unlock()
	return nil
}

// DetachObsoleteJob releases serverName's activeScans slot when obsolete
// reports that its job belongs to a server identity that no longer exists
// (Spec 116 FR-014a, review round 1: a removed server's scan must not keep
// the slot of a same-name replacement). The detached job is NOT cancelled:
// its goroutine runs to its normal end so its callbacks still release the
// temporary source directory, and every write it makes is bound to its own
// (obsolete) incarnation and dropped by storage. Its clearActiveJob then
// finds the slot not held by it and leaves a replacement's job alone. It
// reports whether a job was detached.
func (e *Engine) DetachObsoleteJob(serverName string, obsolete func(*ScanJob) bool) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, ok := e.activeScans[serverName]
	if !ok || !obsolete(job) {
		return false
	}
	delete(e.activeScans, serverName)
	return true
}

// clearActiveJob releases the activeScans slot held by `job`, and ONLY if that
// job still holds it.
//
// The identity check is load-bearing. CancelScan drops a job from activeScans
// while its scanner goroutines are still running, so a replacement scan for the
// same server can take the slot before the cancelled scan unwinds. Deleting by
// server name alone would then evict the replacement — leaving a running scan
// invisible to GetActiveJob and letting StartScan accept a second concurrent
// scan of the same server.
func (e *Engine) clearActiveJob(serverName string, job *ScanJob) {
	e.mu.Lock()
	if current, ok := e.activeScans[serverName]; ok && current == job {
		delete(e.activeScans, serverName)
	}
	e.mu.Unlock()
}

// GetActiveJob returns a snapshot of the active scan job for a server, or nil
// when no scan is running.
//
// It MUST be a snapshot: the scan goroutines keep writing Status, CompletedAt
// and ScannerStatuses on the live record until the job leaves activeScans, so
// handing the live pointer to a REST reader (GET /api/v1/security/scan/status,
// which JSON-encodes it) is a data race.
func (e *Engine) GetActiveJob(serverName string) *ScanJob {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, ok := e.activeScans[serverName]
	if !ok || e.obsoleteLocked(job) {
		return nil
	}
	return job.clone()
}

// obsoleteLocked reports whether job belongs to an incarnation of its server
// that is no longer current (Spec 116 FR-014a, review round 4): a removed
// server's job keeps the name's slot until the purge observer detaches it,
// and in that window it is neither the active scan of a same-name
// replacement nor a reason to refuse the replacement's own. Called with mu
// held.
func (e *Engine) obsoleteLocked(job *ScanJob) bool {
	check := e.registrationCheck
	return check != nil && job != nil && !check(job.ServerName, job.Incarnation)
}

// snapshotJob copies a job under the engine lock so it can be handed to a scan
// callback (which persists and JSON-encodes it) while sibling scanner
// goroutines are still updating the live record.
func (e *Engine) snapshotJob(job *ScanJob) *ScanJob {
	e.mu.Lock()
	defer e.mu.Unlock()
	return job.clone()
}

// resolvedScanner pairs a plugin with a precomputed error that will cause
// the scan job to mark it as failed without actually running docker. This
// lets resolveScanners report "image missing" as a first-class failure in
// the scan report instead of silently dropping the scanner.
type resolvedScanner struct {
	plugin  *ScannerPlugin
	prefail string // non-empty → mark as failed with this message and skip execution
}

// resolveScanners determines which scanners to use.
//
// Unlike the old behaviour (silently dropping scanners with missing Docker
// images), this now includes every enabled scanner in the result. When a
// scanner's image is absent locally it is flagged with prefail so the
// executeScan loop records it as a failed scanner in the job. That way the
// aggregated scan report shows "X of Y scanners failed" instead of pretending
// nothing is wrong.
// effectiveIsolationMode resolves the isolation mode to use for a scan: the
// per-server mode carried on the ScanRequest when present, otherwise the
// engine-wide default set via Service.SetIsolationMode. This is what lets the
// Docker-scanner skip (MCP-34.4) honour a per-server isolation.mode override.
func (e *Engine) effectiveIsolationMode(requestMode string) string {
	if requestMode != "" {
		return requestMode
	}
	return e.isolationMode
}

// resolveScanners determines which scanners to use.
//
// Unlike the old behaviour (silently dropping scanners with missing Docker
// images), this now includes every enabled scanner in the result. When a
// scanner's image is absent locally it is flagged with prefail so the
// executeScan loop records it as a failed scanner in the job. That way the
// aggregated scan report shows "X of Y scanners failed" instead of pretending
// nothing is wrong.
//
// isolationMode is THIS server's resolved isolation mode (per-server override
// already applied by the caller); under "sandbox"/"none" Docker scanner plugins
// are skipped (MCP-34.4).
func (e *Engine) resolveScanners(requestedIDs []string, isolationMode string) ([]resolvedScanner, error) {
	all := e.registry.List()

	// Helper: check whether a scanner's image is present locally. Returns a
	// prefail message if it is missing (caller marks the scanner failed).
	checkImage := func(s *ScannerPlugin) string {
		// In-process scanners have no Docker image — they never prefail on
		// image availability, so they run even for remote servers with no
		// local Docker (MCP-2082).
		if s.InProcess {
			return ""
		}
		// MCP-34.4 / D3 option (b): under a non-Docker isolation mode the host
		// runs no Docker for scanner plugins, so Docker-based scanners cannot
		// run at all. Skip them with an honest, mode-specific reason (pointing
		// at the snap-docker/AppArmor failure doc) instead of the misleading
		// "pull the image locally" guidance below — there is nothing to pull.
		// They stay in the resolved set (recorded as failed) and are surfaced
		// via the informational deep-scan descriptor (Spec 077 US3); this never
		// degrades the baseline verdict.
		// `isolationMode` is per-server resolved, so a server pinned to
		// isolation.mode:docker still runs Docker scanners under a global
		// sandbox default, and vice versa.
		if mode := isolationMode; mode == "sandbox" || mode == "none" {
			return fmt.Sprintf("Docker-based scanner %s skipped: isolation mode %q runs no Docker containers, so Docker scanner plugins cannot run for this server. "+
				"In-process scanners still ran. To run Docker scanners, set isolation.mode to \"docker\" for this server on a host with a working Docker daemon. "+
				"See docs/errors/MCPX_DOCKER_SNAP_APPARMOR.md.", s.ID, mode)
		}
		if e.docker == nil {
			return ""
		}
		if s.Status == ScannerStatusPulling {
			return fmt.Sprintf("scanner %s is still pulling its Docker image; try again in a moment", s.ID)
		}
		image := s.EffectiveImage()
		if image == "" {
			return fmt.Sprintf("scanner %s has no Docker image configured", s.ID)
		}
		if !e.docker.ImageExists(context.Background(), image) {
			return fmt.Sprintf("Docker image %s is not available locally. Toggle the scanner off and on in the Security page to pull it, or run `docker pull %s` manually.", image, image)
		}
		return ""
	}

	if len(requestedIDs) > 0 {
		// Use specifically requested scanners.
		var result []resolvedScanner
		for _, id := range requestedIDs {
			s, err := e.registry.Get(id)
			if err != nil {
				return nil, fmt.Errorf("scanner %s not found", id)
			}
			if s.Status != ScannerStatusInstalled && s.Status != ScannerStatusConfigured && s.Status != ScannerStatusError && s.Status != ScannerStatusPulling {
				return nil, fmt.Errorf("scanner %s is not enabled (status: %s)", id, s.Status)
			}
			// Spec 077 US3: drop Docker deep scanners entirely when the
			// deep-scan layer is off (or the scanner is not on the allow-list)
			// so no container is invoked and no failure can degrade the
			// baseline. The in-process baseline is always allowed.
			if !e.deepScanAllowed(s) {
				continue
			}
			result = append(result, resolvedScanner{plugin: s, prefail: checkImage(s)})
		}
		return result, nil
	}

	// Default: every enabled scanner, including pulling/error ones so the
	// user sees what happened to them in the scan report.
	var result []resolvedScanner
	for _, s := range all {
		// Spec 077 US3 deep-scan gate: skip Docker deep scanners while the
		// opt-in layer is off. Baseline (in-process) always runs.
		if !e.deepScanAllowed(s) {
			continue
		}
		switch s.Status {
		case ScannerStatusInstalled, ScannerStatusConfigured:
			result = append(result, resolvedScanner{plugin: s, prefail: checkImage(s)})
		case ScannerStatusPulling:
			result = append(result, resolvedScanner{
				plugin:  s,
				prefail: fmt.Sprintf("scanner %s is still pulling its Docker image; try again once the pull completes", s.ID),
			})
		case ScannerStatusError:
			// Surface the original error so the user knows why this scanner
			// didn't run this time.
			msg := s.ErrorMsg
			if msg == "" {
				msg = "scanner is in error state; reconfigure it from the Security page"
			}
			result = append(result, resolvedScanner{plugin: s, prefail: msg})
		}
	}
	return result, nil
}

// executeScan runs all scanners in parallel and collects results.
// Scanners marked with a non-empty prefail message are recorded as failed
// immediately and skipped — this keeps missing-image scanners visible in
// the aggregated scan report instead of being silently dropped.
func (e *Engine) executeScan(ctx context.Context, job *ScanJob, scanners []resolvedScanner, req ScanRequest, callback ScanCallback) {
	defer e.clearActiveJob(req.ServerName, job)

	var (
		reports []*ScanReport
		mu      sync.Mutex
		wg      sync.WaitGroup
	)

	for i, rs := range scanners {
		wg.Add(1)
		go func(idx int, item resolvedScanner) {
			defer wg.Done()

			scanner := item.plugin
			// Every callback gets a snapshot: the adapter persists and
			// JSON-encodes the job while sibling goroutines are still writing
			// ScannerStatuses on the live record.
			callback.OnScannerStarted(e.snapshotJob(job), scanner.ID)

			// Fast-fail for prefailed scanners (e.g. missing Docker image).
			// We still emit started/failed callbacks so the UI sees the
			// scanner in the job's ScannerStatuses with a real error.
			if item.prefail != "" {
				e.logger.Warn("Scanner skipped (prefail)",
					zap.String("scanner", scanner.ID),
					zap.String("server", req.ServerName),
					zap.String("reason", item.prefail),
				)
				e.updateScannerStatus(job, scanner.ID, ScanJobStatusFailed, time.Now(), time.Now(), item.prefail, 0)
				callback.OnScannerFailed(e.snapshotJob(job), scanner.ID, fmt.Errorf("%s", item.prefail))
				return
			}

			e.updateScannerStatus(job, scanner.ID, ScanJobStatusRunning, time.Now(), time.Time{}, "", 0)

			report, scanLogs, err := e.runSingleScanner(ctx, scanner, req)
			if err != nil {
				e.logger.Warn("Scanner failed",
					zap.String("scanner", scanner.ID),
					zap.String("server", req.ServerName),
					zap.Error(err),
				)
				e.updateScannerStatus(job, scanner.ID, ScanJobStatusFailed, time.Time{}, time.Now(), err.Error(), 0)
				// Store logs even on failure
				e.setScannerLogs(job, scanner.ID, scanLogs)
				callback.OnScannerFailed(e.snapshotJob(job), scanner.ID, err)
				return
			}

			report.JobID = job.ID
			report.ServerName = req.ServerName

			mu.Lock()
			reports = append(reports, report)
			mu.Unlock()

			e.updateScannerStatus(job, scanner.ID, ScanJobStatusCompleted, time.Time{}, time.Now(), "", len(report.Findings))
			e.setScannerLogs(job, scanner.ID, scanLogs)
			callback.OnScannerCompleted(e.snapshotJob(job), scanner.ID, report)
		}(i, rs)
	}

	wg.Wait()

	// Read the cancellation flag, decide the terminal status and write it in a
	// SINGLE locked section. The job is still in activeScans at this point, so
	// a concurrent GetActiveJob (REST scan-status) can be snapshotting it while
	// we write — doing this outside the lock is a data race.
	e.mu.Lock()
	if job.Status == ScanJobStatusCancelled {
		e.mu.Unlock()
		return
	}

	// Determine final status
	allFailed := true
	for _, ss := range job.ScannerStatuses {
		if ss.Status == ScanJobStatusCompleted {
			allFailed = false
			break
		}
	}
	failed := allFailed && len(scanners) > 0
	if failed {
		job.Status = ScanJobStatusFailed
		job.Error = "all scanners failed"
	} else {
		job.Status = ScanJobStatusCompleted
	}
	job.CompletedAt = time.Now()
	final := job.clone()
	e.mu.Unlock()

	if failed {
		callback.OnScanFailed(final, fmt.Errorf("all scanners failed"))
		return
	}
	callback.OnScanCompleted(final, reports)
}

// runSingleScanner executes one scanner and returns its report plus execution logs
// scannerSupportsInput reports whether the scanner declares the given input type.
func scannerSupportsInput(s *ScannerPlugin, input string) bool {
	for _, in := range s.Inputs {
		if in == input {
			return true
		}
	}
	return false
}

// effectiveScannerCommand returns the command to run for a scanner. When the
// scan target is a Docker image (req.ContainerImage set) and the scanner both
// declares the "container_image" input and provides an ImageCommand template,
// the image-mode command is returned with "{{IMAGE}}" substituted for the image
// reference. Otherwise the scanner's default (source-mode) Command is returned.
func effectiveScannerCommand(s *ScannerPlugin, req ScanRequest) []string {
	if req.ContainerImage == "" || len(s.ImageCommand) == 0 || !scannerSupportsInput(s, "container_image") {
		return s.Command
	}
	out := make([]string, len(s.ImageCommand))
	for i, tok := range s.ImageCommand {
		out[i] = strings.ReplaceAll(tok, "{{IMAGE}}", req.ContainerImage)
	}
	return out
}

func (e *Engine) runSingleScanner(ctx context.Context, s *ScannerPlugin, req ScanRequest) (*ScanReport, scannerLogs, error) {
	// In-process scanners run directly in Go — no Docker container, no source
	// files required. They analyze the tool definitions exported to
	// req.SourceDir/tools.json (MCP-2082).
	if s.InProcess {
		return e.runInProcessScanner(s, req)
	}
	pinned, err := pinToolDefinitions(s, req)
	if err != nil {
		return nil, scannerLogs{}, err
	}

	// Parse timeout
	timeout := 120 * time.Second
	if s.Timeout != "" {
		if parsed, err := time.ParseDuration(s.Timeout); err == nil {
			timeout = parsed
		}
	}
	// The scan deadline starts before source staging so staging runs under it
	// (#1602); RunScanner derives its own, no-later timeout from this ctx.
	// A non-positive timeout means "no limit" (as in DockerRunner.RunScanner),
	// so it must not produce an already-expired context.
	if timeout > 0 {
		var cancelScan context.CancelFunc
		ctx, cancelScan = context.WithTimeout(ctx, timeout)
		defer cancelScan()
	}

	// Prepare report directory
	jobID := fmt.Sprintf("scan-%s-%d", req.ServerName, time.Now().UnixNano())
	reportDir, err := PrepareReportDir(e.dataDir, jobID, s.ID)
	if err != nil {
		return nil, scannerLogs{}, fmt.Errorf("failed to prepare report directory: %w", err)
	}

	// Build env vars: scanner config + request env
	// Resolve ${keyring:...} references if a secret resolver is available
	env := make(map[string]string)
	for k, v := range s.ConfiguredEnv {
		if e.secretResolver != nil && strings.HasPrefix(v, "${keyring:") {
			resolved, err := e.secretResolver(ctx, v)
			if err != nil {
				e.logger.Warn("Failed to resolve secret for scanner env",
					zap.String("key", k), zap.Error(err))
				continue // Skip unresolvable secrets
			}
			env[k] = resolved
		} else {
			env[k] = v
		}
	}
	for k, v := range req.Env {
		env[k] = v
	}

	// Determine network mode
	networkMode := "none"
	if s.NetworkReq {
		networkMode = "bridge"
	}

	// Create per-scanner cache directory (persists DB downloads between runs)
	cacheDir := filepath.Join(e.dataDir, "scanner-cache", s.ID)
	os.MkdirAll(cacheDir, 0755)

	// Mount Claude credentials for AI-powered scanners (read-only)
	var extraMounts []string
	if s.ID == "mcp-ai-scanner" {
		// Auto-forward Claude auth tokens from host environment
		if token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); token != "" {
			if _, exists := env["CLAUDE_CODE_OAUTH_TOKEN"]; !exists {
				env["CLAUDE_CODE_OAUTH_TOKEN"] = token
			}
		}
		if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
			if _, exists := env["ANTHROPIC_API_KEY"]; !exists {
				env["ANTHROPIC_API_KEY"] = key
			}
		}
		// Build a temp .claude directory with credentials for the scanner container.
		// The scanner uses Claude Agent SDK which reads from ~/.claude/.credentials.json.
		// We copy .claude.json from host (if exists) and generate .credentials.json
		// from the configured CLAUDE_CODE_OAUTH_TOKEN (stored in keyring).
		tmpClaudeDir, err := os.MkdirTemp("", "mcpproxy-claude-creds-")
		if err == nil {
			mounted := false
			// Copy .claude.json from host if it exists
			homeDir, _ := os.UserHomeDir()
			hostClaudeJSON := filepath.Join(homeDir, ".claude", ".claude.json")
			if data, err := os.ReadFile(hostClaudeJSON); err == nil {
				os.WriteFile(filepath.Join(tmpClaudeDir, ".claude.json"), data, 0644)
			}
			// Generate .credentials.json from OAuth token
			if oauthToken, ok := env["CLAUDE_CODE_OAUTH_TOKEN"]; ok && oauthToken != "" {
				credsJSON := fmt.Sprintf(`{"oauth_tokens":{"default":{"accessToken":"%s","expiresAt":"2099-12-31T23:59:59Z"}}}`, oauthToken)
				if err := os.WriteFile(filepath.Join(tmpClaudeDir, ".credentials.json"), []byte(credsJSON), 0600); err == nil {
					mounted = true
					e.logger.Debug("Generated OAuth credentials for AI scanner")
				}
			}
			if mounted {
				extraMounts = append(extraMounts, tmpClaudeDir+":/app/.claude:ro")
			} else {
				// Fall back to host .claude dir
				claudeDir := filepath.Join(homeDir, ".claude")
				if _, err := os.Stat(claudeDir); err == nil {
					extraMounts = append(extraMounts, claudeDir+":/app/.claude:ro")
				}
				os.RemoveAll(tmpClaudeDir)
			}
		}
	}

	// A scanner that reads tool metadata mounts a proxy-owned copy of the
	// source dir holding the exported snapshot as tools.json, never a path
	// inside the upstream-writable source dir (RT-06, Sol r20 P2).
	sourceDir, cleanupSource, err := e.stageScannerSource(ctx, s, req)
	if err != nil {
		return nil, scannerLogs{}, fmt.Errorf("scanner %s: %w", s.ID, err)
	}
	defer cleanupSource()

	// Log which env keys are being passed (redact values for security)
	envKeys := make([]string, 0, len(env))
	for k := range env {
		envKeys = append(envKeys, k)
	}
	e.logger.Info("Scanner env vars resolved",
		zap.String("scanner", s.ID),
		zap.Strings("env_keys", envKeys),
		zap.Int("configured_env_count", len(s.ConfiguredEnv)),
	)

	// Run scanner container
	cfg := ScannerRunConfig{
		ContainerName:          GenerateContainerName(s.ID, req.ServerName),
		Image:                  s.EffectiveImage(),
		Command:                effectiveScannerCommand(s, req),
		Env:                    env,
		SourceDir:              sourceDir,
		ReportDir:              reportDir,
		CacheDir:               cacheDir,
		NetworkMode:            networkMode,
		Timeout:                timeout,
		ReadOnly:               false, // Scanner containers need to write cache/temp files
		MemoryLimit:            "512m",
		ExtraMounts:            extraMounts,
		DisableNoNewPrivileges: e.disableNoNewPrivileges,
	}

	run := e.runScannerContainer
	if run == nil {
		run = e.docker.RunScanner
	}
	// The scanner reads the staged snapshot, so the upstream's tools.json
	// cannot reach it. It must still stay the pinned regular file for the
	// whole run: a symlink retargeted or a file swapped in mid-run (and
	// restored before the end) is evidence of tampering, and the result is
	// discarded (Sol r20 P2).
	stopWatch := make(chan struct{})
	watch := watchToolDefinitions(req, pinned, stopWatch)
	stdout, stderr, exitCode, err := run(ctx, cfg)
	close(stopWatch)
	replaced := <-watch
	logs := scannerLogs{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}
	if err != nil {
		return nil, logs, fmt.Errorf("scanner %s execution failed: %w", s.ID, err)
	}
	if replaced != "" {
		return nil, logs, fmt.Errorf("scanner %s: exported tool definitions: %s; the result is discarded", s.ID, replaced)
	}
	// The scanner read the proxy-owned snapshot; a tools.json that no longer
	// holds it is still evidence of tampering, so the result is discarded
	// (RT-06, Sol r4 P1). Metadata-independent scanners are exempt.
	if err := toolDefinitionsGate(s, req); err != nil {
		return nil, logs, fmt.Errorf("scanner %s: %w", s.ID, err)
	}

	e.logger.Debug("Scanner finished",
		zap.String("scanner", s.ID),
		zap.Int("exit_code", exitCode),
		zap.Int("stdout_len", len(stdout)),
		zap.Int("stderr_len", len(stderr)),
	)

	// Collect results: try file first, then stdout
	var reportData []byte

	// Try reading from report directory
	reportData, err = e.docker.ReadReportFile(reportDir)
	if err != nil {
		// Fall back to stdout
		if len(stdout) > 0 {
			reportData = []byte(stdout)
		} else {
			// When the container never produced output, check for known
			// host-side failure modes (e.g. snap-docker + AppArmor denying
			// entrypoint exec) and surface a remediation hint so the user
			// is not left staring at a bare "operation not permitted".
			if hint := ClassifyScannerExecFailure(stderr, exitCode); hint != "" {
				e.logger.Warn("Scanner failed to exec entrypoint",
					zap.String("scanner", s.ID),
					zap.Int("exit_code", exitCode),
					zap.String("hint", hint),
				)
				return nil, logs, fmt.Errorf("scanner %s produced no output (exit code: %d, stderr: %s) — %s", s.ID, exitCode, truncate(stderr, 500), hint)
			}
			return nil, logs, fmt.Errorf("scanner %s produced no output (exit code: %d, stderr: %s)", s.ID, exitCode, truncate(stderr, 500))
		}
	}

	// Parse results
	report, parseErr := e.parseResults(reportData, s.ID)
	return report, logs, parseErr
}

// parseResults parses scanner output into a ScanReport
func (e *Engine) parseResults(data []byte, scannerID string) (*ScanReport, error) {
	report := &ScanReport{
		ID:        fmt.Sprintf("report-%s-%d", scannerID, time.Now().UnixNano()),
		ScannerID: scannerID,
		ScannedAt: time.Now(),
	}

	// Try SARIF first
	if IsSARIF(data) {
		sarifReport, err := ParseSARIF(data)
		if err != nil {
			return nil, fmt.Errorf("failed to parse SARIF from %s: %w", scannerID, err)
		}
		report.Findings = NormalizeFindings(sarifReport, scannerID)
		ClassifyAllFindings(report.Findings)
		report.SarifRaw = json.RawMessage(data)
		report.RiskScore = CalculateRiskScore(report.Findings)
		return report, nil
	}

	// Try Ramparts JSON format
	if isRampartsOutput(data) {
		findings := parseRampartsOutput(data, scannerID)
		report.Findings = findings
		if len(findings) > 0 {
			ClassifyAllFindings(report.Findings)
		}
		report.RiskScore = CalculateRiskScore(report.Findings)
		return report, nil
	}

	// Try Snyk Agent Scan JSON format
	if isSnykAgentScanOutput(data) {
		findings := parseSnykAgentScanOutput(data, scannerID)
		report.Findings = findings
		if len(findings) > 0 {
			ClassifyAllFindings(report.Findings)
		}
		report.RiskScore = CalculateRiskScore(report.Findings)
		return report, nil
	}

	// Try Cisco MCP Scanner raw JSON format
	if isCiscoScannerOutput(data) {
		findings := parseCiscoScannerOutput(data, scannerID)
		report.Findings = findings
		if len(findings) > 0 {
			ClassifyAllFindings(report.Findings)
		}
		report.RiskScore = CalculateRiskScore(report.Findings)
		return report, nil
	}

	// Try generic JSON with findings array
	var generic struct {
		Findings []ScanFinding `json:"findings"`
		Results  []ScanFinding `json:"results"`
	}
	if err := json.Unmarshal(data, &generic); err == nil {
		if len(generic.Findings) > 0 {
			report.Findings = generic.Findings
		} else if len(generic.Results) > 0 {
			report.Findings = generic.Results
		}
		if len(report.Findings) > 0 {
			// Ensure scanner is set on all findings
			for i := range report.Findings {
				if report.Findings[i].Scanner == "" {
					report.Findings[i].Scanner = scannerID
				}
				// Text spans are an IN-PROCESS detect concept and are never
				// accepted off the wire. This decodes a third-party scanner's
				// own report file, so `spans` here is data that tool chose to
				// emit — and a span is a claim about WHICH CHARACTERS of an
				// attacker-authored tool description the Web UI should mark as
				// dangerous. No bundled external scanner produces them (SARIF
				// and the Cisco/Ramparts/Snyk parsers all set none), so this
				// drops nothing real; it stops an offset this proxy did not
				// compute, against text it cannot check here, from reaching the
				// renderer. detect.DescriptionSpan remains the only producer.
				report.Findings[i].Spans = nil
			}
			report.RiskScore = CalculateRiskScore(report.Findings)
			return report, nil
		}
	}

	// If we got data but couldn't parse it, treat as no findings
	e.logger.Warn("Scanner output could not be parsed, treating as clean",
		zap.String("scanner", scannerID),
		zap.Int("data_length", len(data)),
	)
	report.Findings = []ScanFinding{}
	report.RiskScore = 0
	return report, nil
}

// scannerLogs captures stdout/stderr from a scanner execution
type scannerLogs struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// setScannerLogs stores stdout/stderr on a scanner's job status
func (e *Engine) setScannerLogs(job *ScanJob, scannerID string, logs scannerLogs) {
	if scannerID == ciscoScannerID {
		logs.Stdout = sanitizeCiscoStdout(logs.Stdout)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range job.ScannerStatuses {
		if job.ScannerStatuses[i].ScannerID == scannerID {
			job.ScannerStatuses[i].Stdout = truncate(logs.Stdout, MaxLogBytes)
			job.ScannerStatuses[i].Stderr = truncate(logs.Stderr, MaxLogBytes)
			job.ScannerStatuses[i].ExitCode = logs.ExitCode
			return
		}
	}
}

// updateScannerStatus updates a specific scanner's status within a job
func (e *Engine) updateScannerStatus(job *ScanJob, scannerID, status string, startedAt, completedAt time.Time, errMsg string, findingsCount int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range job.ScannerStatuses {
		if job.ScannerStatuses[i].ScannerID == scannerID {
			job.ScannerStatuses[i].Status = status
			if !startedAt.IsZero() {
				job.ScannerStatuses[i].StartedAt = startedAt
			}
			if !completedAt.IsZero() {
				job.ScannerStatuses[i].CompletedAt = completedAt
			}
			job.ScannerStatuses[i].Error = errMsg
			job.ScannerStatuses[i].FindingsCount = findingsCount
			// Recompute duration once both timestamps are known. The live scan
			// path sets StartedAt and CompletedAt in separate calls, so this
			// derives from the stored StartedAt when CompletedAt arrives.
			job.ScannerStatuses[i].DurationMs = job.ScannerStatuses[i].Duration().Milliseconds()
			return
		}
	}
}

// AggregateReports combines multiple scan reports into an aggregated report.
// Note: scannersTotal and scannersFailed should be provided by the caller
// from the ScanJob.ScannerStatuses, since reports only contains successful results.
func AggregateReports(jobID, serverName string, reports []*ScanReport) *AggregatedReport {
	agg := &AggregatedReport{
		JobID:      jobID,
		ServerName: serverName,
		ScannedAt:  time.Now(),
		Reports:    make([]ScanReport, 0, len(reports)),
	}

	for _, r := range reports {
		agg.Findings = append(agg.Findings, r.Findings...)
		agg.Reports = append(agg.Reports, *r)
	}

	// Classify findings that lack threat_type/threat_level (legacy data). This
	// must run before MergeFindings so consensus (which keys on threat_type) and
	// severity backfill see fully-classified findings.
	ClassifyAllFindings(agg.Findings)

	// Spec 077 (T021, FR-010/FR-011/FR-012): collapse the per-scanner findings
	// into a single unified list. Findings sharing (rule_id, location) merge into
	// one entry whose Sources lists every contributing scanner; cross-source
	// agreement on the same (location, threat_type) raises confidence.
	agg.Findings = MergeFindings(agg.Findings)

	// Flag findings that belong in the Supply Chain Audit (CVEs) UI section.
	// Match only real CVE/package vulnerabilities so AI-scanner output from Pass 2
	// doesn't get routed into the CVE section.
	for i := range agg.Findings {
		agg.Findings[i].SupplyChainAudit = isSupplyChainAudit(&agg.Findings[i])
	}

	agg.RiskScore = CalculateRiskScore(agg.Findings)
	agg.Summary = SummarizeFindings(agg.Findings)

	// Spec 077 FR-014 verdict purity: the report-level verdict uses the SAME
	// tier-driven, baseline-only derivation as the server-list summary
	// (GetScanSummary via deriveBaselineVerdict), so the report page can never
	// disagree with the server verdict — a tierless deep-scan/external
	// "dangerous" finding never moves it. Summary above keeps the RAW
	// threat-level counts for transparency; verdict-bearing UI reads these.
	verdict, counts := deriveBaselineVerdict(agg.Findings)
	agg.Verdict = verdict
	agg.FindingCounts = &counts

	// ScannersRun = number of successful reports
	agg.ScannersRun = len(reports)
	// ScanComplete = at least one scanner succeeded
	agg.ScanComplete = len(reports) > 0

	return agg
}

// AggregateReportsWithJobStatus combines reports and enriches with scanner failure info from the job.
func AggregateReportsWithJobStatus(jobID, serverName string, reports []*ScanReport, job *ScanJob) *AggregatedReport {
	agg := AggregateReports(jobID, serverName, reports)

	if job != nil {
		agg.ScannersTotal = len(job.ScannerStatuses)
		failed := 0
		succeeded := 0
		for _, ss := range job.ScannerStatuses {
			if ss.Status == ScanJobStatusFailed {
				failed++
			}
			if ss.Status == ScanJobStatusCompleted {
				succeeded++
			}
		}
		agg.ScannersFailed = failed
		agg.ScannersRun = succeeded
		agg.ScanComplete = succeeded > 0

		// Detect empty scans: scanners "succeeded" but scanned nothing meaningful.
		// This happens when a quarantined/disconnected server has no extractable
		// source files. Without this check, the UI would show a misleading "0/100"
		// risk score that implies the server is safe when nothing was actually analyzed.
		// A scan is considered empty if no source files AND no tool definitions were analyzed.
		// Exception: "url" and "tool_definitions_only" methods are inherently file-less —
		// scanners analyze the URL endpoint or tool descriptions, so 0 files is expected.
		if agg.ScanComplete && len(agg.Findings) == 0 && job.ScanContext != nil {
			method := job.ScanContext.SourceMethod
			filelessMethod := method == "url" || method == "tool_definitions_only"
			if job.ScanContext.TotalFiles == 0 && job.ScanContext.ToolsExported == 0 && !filelessMethod {
				agg.ScanComplete = false
				agg.EmptyScan = true
			}
		}
	}

	return agg
}

// isCiscoScannerOutput checks if the data looks like Cisco MCP Scanner output
func isCiscoScannerOutput(data []byte) bool {
	var probe struct {
		ScanResults []any `json:"scan_results"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.ScanResults != nil
}

// parseCiscoScannerOutput parses Cisco MCP Scanner's raw JSON format into ScanFindings.
// The Cisco format has: { "scan_results": [ { "tool_name": "x", "is_safe": false, "findings": { "yara_analyzer": { ... } } } ] }
func parseCiscoScannerOutput(data []byte, scannerID string) []ScanFinding {
	var cisco struct {
		ScanResults []struct {
			ToolName        string `json:"tool_name"`
			ToolDescription string `json:"tool_description"`
			IsSafe          bool   `json:"is_safe"`
			Findings        map[string]struct {
				Severity      string   `json:"severity"`
				ThreatNames   []string `json:"threat_names"`
				ThreatSummary string   `json:"threat_summary"`
				TotalFindings int      `json:"total_findings"`
				MCPTaxonomies []struct {
					ScannerCategory string `json:"scanner_category"`
					AITechName      string `json:"aitech_name"`
					AISubtechName   string `json:"aisubtech_name"`
					Description     string `json:"description"`
				} `json:"mcp_taxonomies"`
			} `json:"findings"`
		} `json:"scan_results"`
	}

	if err := json.Unmarshal(data, &cisco); err != nil {
		return nil
	}
	if len(cisco.ScanResults) == 0 {
		return nil
	}

	var findings []ScanFinding
	for _, result := range cisco.ScanResults {
		if result.IsSafe {
			continue // Skip safe tools
		}
		for analyzerName, analyzerResult := range result.Findings {
			if analyzerResult.TotalFindings == 0 {
				continue
			}
			for _, threat := range analyzerResult.ThreatNames {
				// Truncate tool description for evidence (max 500 chars)
				evidence := result.ToolDescription
				if len(evidence) > 500 {
					evidence = stringutil.UTF8Prefix(evidence, 500) + "..."
				}
				finding := ScanFinding{
					RuleID:      strings.ToLower(strings.ReplaceAll(threat, " ", "_")),
					Title:       threat + " in tool: " + result.ToolName,
					Description: analyzerResult.ThreatSummary,
					Scanner:     scannerID,
					Location:    "tool:" + result.ToolName,
					Evidence:    evidence,
				}

				// Map Cisco severity to our severity
				switch strings.ToUpper(analyzerResult.Severity) {
				case "HIGH":
					finding.Severity = SeverityHigh
					finding.ThreatType = ThreatToolPoisoning
					finding.ThreatLevel = ThreatLevelDangerous
				case "MEDIUM":
					finding.Severity = SeverityMedium
					finding.ThreatType = ThreatToolPoisoning
					finding.ThreatLevel = ThreatLevelWarning
				default:
					finding.Severity = SeverityLow
					finding.ThreatType = ThreatUncategorized
					finding.ThreatLevel = ThreatLevelInfo
				}

				// Classify based on threat name
				threatLower := strings.ToLower(threat)
				if strings.Contains(threatLower, "credential") || strings.Contains(threatLower, "exfiltrat") {
					finding.ThreatType = ThreatToolPoisoning
					finding.ThreatLevel = ThreatLevelDangerous
				} else if strings.Contains(threatLower, "injection") {
					finding.ThreatType = ThreatPromptInjection
					finding.ThreatLevel = ThreatLevelDangerous
				}

				// Add taxonomy description if available
				if len(analyzerResult.MCPTaxonomies) > 0 {
					finding.Description = analyzerResult.MCPTaxonomies[0].Description
					finding.Category = analyzerResult.MCPTaxonomies[0].ScannerCategory
				}

				_ = analyzerName // used for context but not in the finding
				findings = append(findings, finding)
			}
		}
	}

	return findings
}

// isSnykAgentScanOutput checks if the data looks like Snyk Agent Scan output.
// The format is: { "/path/to/config.json": { "servers": [{ "name": "...", "signature": {...}, "issues": [...] }] } }
func isSnykAgentScanOutput(data []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	for _, v := range probe {
		var entry struct {
			Client  string `json:"client"`
			Servers []any  `json:"servers"`
		}
		if json.Unmarshal(v, &entry) == nil && entry.Servers != nil {
			return true
		}
	}
	return false
}

// parseSnykAgentScanOutput parses Snyk Agent Scan JSON output into ScanFindings.
// Snyk format: { "/path": { "servers": [{ "name": "x", "issues": [{ "code": "E001", "severity": "critical", ... }] }] } }
func parseSnykAgentScanOutput(data []byte, scannerID string) []ScanFinding {
	var snyk map[string]struct {
		Servers []struct {
			Name   string `json:"name"`
			Issues []struct {
				Code        string `json:"code"`
				Severity    string `json:"severity"`
				Title       string `json:"title"`
				Message     string `json:"message"`
				Description string `json:"description"`
				ToolName    string `json:"tool_name"`
				Category    string `json:"category"`
			} `json:"issues"`
		} `json:"servers"`
	}

	if err := json.Unmarshal(data, &snyk); err != nil {
		return nil
	}

	var findings []ScanFinding
	for _, config := range snyk {
		for _, srv := range config.Servers {
			for _, issue := range srv.Issues {
				title := issue.Title
				if title == "" {
					title = issue.Message
				}
				if title == "" {
					title = issue.Code
				}

				desc := issue.Description
				if desc == "" {
					desc = issue.Message
				}

				finding := ScanFinding{
					RuleID:      issue.Code,
					Title:       title,
					Description: desc,
					Scanner:     scannerID,
					Category:    issue.Category,
				}
				if issue.ToolName != "" {
					finding.Location = "tool:" + issue.ToolName
				}

				switch strings.ToLower(issue.Severity) {
				case "critical":
					finding.Severity = SeverityCritical
				case "high":
					finding.Severity = SeverityHigh
				case "medium":
					finding.Severity = SeverityMedium
				case "low":
					finding.Severity = SeverityLow
				default:
					finding.Severity = SeverityMedium
				}

				findings = append(findings, finding)
			}
		}
	}

	return findings
}

// isRampartsOutput checks if the data looks like Ramparts MCP Scanner output
func isRampartsOutput(data []byte) bool {
	var probe struct {
		URL            string `json:"url"`
		SecurityIssues any    `json:"security_issues"`
		YaraResults    []any  `json:"yara_results"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.SecurityIssues != nil && probe.YaraResults != nil
}

// parseRampartsOutput parses Ramparts MCP Scanner's JSON format into ScanFindings.
// Ramparts output has: { "security_issues": { "tool_issues": [...], "prompt_issues": [...] }, "yara_results": [...] }
func parseRampartsOutput(data []byte, scannerID string) []ScanFinding {
	var ramparts struct {
		SecurityIssues struct {
			ToolIssues     []rampartsIssue `json:"tool_issues"`
			PromptIssues   []rampartsIssue `json:"prompt_issues"`
			ResourceIssues []rampartsIssue `json:"resource_issues"`
		} `json:"security_issues"`
		YaraResults []struct {
			TargetType   string `json:"target_type"`
			TargetName   string `json:"target_name"`
			RuleName     string `json:"rule_name"`
			RuleFile     string `json:"rule_file"`
			Context      string `json:"context"`
			Status       string `json:"status"`
			RuleMetadata *struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Severity    string `json:"severity"`
				Category    string `json:"category"`
			} `json:"rule_metadata"`
		} `json:"yara_results"`
	}

	if err := json.Unmarshal(data, &ramparts); err != nil {
		return nil
	}

	var findings []ScanFinding

	// Parse YARA results (individual matches, not summaries)
	for _, yr := range ramparts.YaraResults {
		if yr.TargetType == "summary" {
			continue // Skip pre-scan/post-scan summaries
		}
		if yr.Status != "warning" && yr.Status != "alert" {
			continue
		}

		finding := ScanFinding{
			RuleID:      strings.ToLower(strings.ReplaceAll(yr.RuleName, " ", "_")),
			Title:       yr.RuleName,
			Description: yr.Context,
			Scanner:     scannerID,
			Location:    yr.TargetType + ":" + yr.TargetName,
		}

		if yr.RuleMetadata != nil {
			if yr.RuleMetadata.Name != "" {
				finding.Title = yr.RuleMetadata.Name + " in " + yr.TargetType + ": " + yr.TargetName
			}
			if yr.RuleMetadata.Description != "" {
				finding.Description = yr.RuleMetadata.Description
			}
			finding.Category = yr.RuleFile

			switch strings.ToUpper(yr.RuleMetadata.Severity) {
			case "CRITICAL":
				finding.Severity = SeverityCritical
			case "HIGH":
				finding.Severity = SeverityHigh
			case "MEDIUM":
				finding.Severity = SeverityMedium
			default:
				finding.Severity = SeverityLow
			}
		} else {
			finding.Severity = SeverityMedium
		}

		findings = append(findings, finding)
	}

	// Parse security_issues (tool_issues, prompt_issues, resource_issues)
	parseIssues := func(issues []rampartsIssue, issueType string) {
		for _, issue := range issues {
			// The subject differs per issue kind: tools carry tool_name,
			// prompts prompt_name, resources resource_uri (v0.8.x).
			subject := issue.ToolName
			if subject == "" {
				subject = issue.PromptName
			}
			if subject == "" {
				subject = issue.ResourceURI
			}

			title := issue.Message
			if title == "" {
				title = issue.IssueType + " in " + issueType + ": " + subject
			}
			desc := issue.Description
			if desc == "" {
				desc = issue.Details
			}

			finding := ScanFinding{
				RuleID:      strings.ToLower(issue.IssueType),
				Title:       title,
				Description: desc,
				Scanner:     scannerID,
				Location:    issueType + ":" + subject,
			}
			switch strings.ToUpper(issue.Severity) {
			case "CRITICAL":
				finding.Severity = SeverityCritical
			case "HIGH":
				finding.Severity = SeverityHigh
			case "MEDIUM":
				finding.Severity = SeverityMedium
			case "LOW":
				finding.Severity = SeverityLow
			default:
				finding.Severity = SeverityMedium
			}
			findings = append(findings, finding)
		}
	}

	parseIssues(ramparts.SecurityIssues.ToolIssues, "tool")
	parseIssues(ramparts.SecurityIssues.PromptIssues, "prompt")
	parseIssues(ramparts.SecurityIssues.ResourceIssues, "resource")

	return findings
}

// rampartsIssue represents a security issue from Ramparts. Field names track
// the v0.8.x `SecurityIssue` serialization (issue_type/severity/message/details
// + per-kind subject fields); the pre-v0.8 `type`/`impact` keys no longer exist
// upstream, so reading them yielded empty rule IDs and mis-graded severities
// (MCP-2422).
type rampartsIssue struct {
	ToolName    string `json:"tool_name"`
	PromptName  string `json:"prompt_name"`
	ResourceURI string `json:"resource_uri"`
	IssueType   string `json:"issue_type"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
	Description string `json:"description"`
	Details     string `json:"details"`
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return stringutil.UTF8Prefix(s, maxLen) + "..."
}

// ciscoScannerID is the single source of truth for the bundled Cisco AI Defense
// scanner's plugin ID. registry_bundled.go references this const, and
// setScannerLogs uses it to gate Cisco-specific stdout sanitization.
const ciscoScannerID = "cisco-mcp-scanner"

// ciscoServerURLPattern matches the placeholder server_url line emitted by
// the upstream cisco-ai-mcp-scanner PyPI package in its raw stdout output.
// The URL is hardcoded in the upstream tool and does not represent a real
// network request; mcpproxy strips it from the user-visible execution log
// to avoid the false impression of data exfiltration. See issue #383.
var ciscoServerURLPattern = regexp.MustCompile(
	`(?m)^[ \t]*"server_url"[ \t]*:[ \t]*"https?://[^"]*deepwiki[^"]*"[ \t]*,?[ \t]*\r?\n?`,
)

// ciscoCoverageCaveat is prepended to every cisco-mcp-scanner execution log so
// the scanner's coverage is never over-trusted. The bundled cisco scanner runs
// `static --tools tools.json` (registry_bundled.go): it analyzes the exported
// tool definitions with YARA + readiness rules and never connects to the live
// server endpoint, so an is_safe/SAFE result reflects the tool definitions, not
// the server's runtime behavior. Surfacing this unconditionally (not only when
// the upstream deepwiki placeholder happens to appear) keeps the caveat honest
// even if a future upstream release changes its raw output. See issue #383 / MCP-2399.
const ciscoCoverageCaveat = "// [mcpproxy] coverage caveat: cisco-mcp-scanner performs STATIC analysis of " +
	"the exported tool definitions only. It does not connect to or probe the live server endpoint and " +
	"makes no network request, so a \"safe\"/is_safe result reflects the analyzed tool definitions, not " +
	"the server's live runtime behavior (see issue #383).\n"

// sanitizeCiscoStdout prepends a static-analysis coverage caveat and replaces
// the upstream-hardcoded deepwiki placeholder URL line with a short annotation
// referencing the tracking issue. The rest of the raw output is preserved for
// debugging. The caveat leads the output so it survives MaxLogBytes truncation.
//
// Assumes pretty-printed multi-line output; minified single-line JSON
// bypasses this filter (acceptable since the cisco scanner emits multi-line).
func sanitizeCiscoStdout(stdout string) string {
	sanitized := stdout
	if strings.Contains(sanitized, "deepwiki") {
		sanitized = ciscoServerURLPattern.ReplaceAllString(
			sanitized,
			"// [mcpproxy] upstream cisco-ai-mcp-scanner emits a hardcoded "+
				"placeholder server_url; no network request was made (see issue #383)\n",
		)
	}
	return ciscoCoverageCaveat + sanitized
}
