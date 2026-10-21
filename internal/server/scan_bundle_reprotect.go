package server

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// Post-activation re-protection sweep (Spec 101 FR-019, SC-009; tasks B17).
//
// When a new TPA bundle activates, already-approved tools may match a signature
// that did not exist when they were approved. The sweep re-checks them from
// cached data only: leg (i) refreshes the held-scan evidence of pending and
// changed tools, leg (ii) runs a cached informational scan of every
// non-quarantined server with approved tools. It never approves, unquarantines
// or revokes anything (RT-02): findings flow toward human review only. It never
// connects to an upstream and never calls the live StartScan, so a server that
// is down, slow or hostile cannot influence it.

const (
	// defaultReprotectServerPause is the pause between two servers' scans, so a
	// large install does not run a burst of scans back to back.
	defaultReprotectServerPause = 2 * time.Second
	// reprotectMaxAttempts is how often one server's cached scan may fail to
	// start before the sweep gives up on it (and does not write its marker).
	reprotectMaxAttempts = 3

	// Reasons a sweep reports when it failed (BundleReprotectStatus.Reason).
	reprotectReasonScanFailed         = "scan_failed"
	reprotectReasonStorageUnavailable = "storage_unavailable"
)

// bundleReprotectState is the sweep's bookkeeping. The zero value is ready to
// use; the func and duration fields are test seams (nil/zero = production).
type bundleReprotectState struct {
	mu      sync.Mutex
	closed  bool
	nextID  int
	cancels map[int]context.CancelFunc
	// last is the digest of the most recent sweep started and latest the
	// digest of the newest activation seen: the same digest re-fired starts
	// nothing, and a sweep whose digest is no longer the latest is abandoned.
	last   string
	latest string
	// runMu serializes sweeps.
	runMu sync.Mutex

	statusSink     func(scanner.BundleReprotectStatus)
	refreshHeld    func(server string) error
	activeDigest   func() string
	recordHit      func()
	serverPause    time.Duration
	busyRetryDelay time.Duration
}

func (st *bundleReprotectState) pause() time.Duration {
	if st.serverPause != 0 {
		return st.serverPause
	}
	return defaultReprotectServerPause
}

func (st *bundleReprotectState) busyDelay() time.Duration {
	if st.busyRetryDelay != 0 {
		return st.busyRetryDelay
	}
	return informationalRescanDebounce
}

// beginReprotect registers one sweep goroutine: it derives its context from
// parent, records the cancel so stopBundleReprotect can end it, and adds it to
// the WaitGroup Shutdown waits on. It refuses (ok=false) once the server is
// stopping. The closed check and the Add share one critical section with
// stopBundleReprotect's flag flip, so no Add can race the Wait.
func (s *Server) beginReprotect(parent context.Context) (ctx context.Context, end func(), ok bool) {
	st := &s.bundleReprotect
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(parent)
	if st.cancels == nil {
		st.cancels = make(map[int]context.CancelFunc)
	}
	st.nextID++
	id := st.nextID
	st.cancels[id] = cancel
	s.bundleReprotectWG.Add(1)
	return ctx, func() {
		cancel()
		st.mu.Lock()
		delete(st.cancels, id)
		st.mu.Unlock()
		s.bundleReprotectWG.Done()
	}, true
}

// stopBundleReprotect stops the sweep machinery for Shutdown: it refuses new
// sweeps, cancels the running ones and waits for their goroutines, so no sweep
// writes to the store once runtime.Close begins.
func (s *Server) stopBundleReprotect() {
	st := &s.bundleReprotect
	st.mu.Lock()
	st.closed = true
	for _, cancel := range st.cancels {
		cancel()
	}
	st.mu.Unlock()
	s.bundleReprotectWG.Wait()
}

// handleBundleActivation is the activation observer. It runs the sweep for one
// activation on the observer's own goroutine (the scanner dispatches observers
// off bundleRT.mu), tracked by bundleReprotectWG and bound to the informational
// scan context.
func (s *Server) handleBundleActivation(act scanner.BundleActivation) {
	ctx, end, ok := s.beginReprotect(s.informationalScanContext())
	if !ok {
		return
	}
	defer end()
	s.runBundleReprotect(ctx, act)
}

// runBundleReprotectCatchUp re-runs the sweep at startup when the persisted
// marker (storage.BundleReprotectState) differs from the active bundle digest:
// it covers the construction-time activation, a crash mid-sweep and a binary
// upgrade that changes the embedded corpus. It waits out the same startup delay
// as the baseline sweep so the cross-server snapshot is populated.
func (s *Server) runBundleReprotectCatchUp(ctx context.Context) {
	sweepCtx, end, ok := s.beginReprotect(ctx)
	if !ok {
		return
	}
	defer end()
	sm := s.runtime.StorageManager()
	if sm == nil {
		return
	}
	if delay := s.infoScanSweepDelay; delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-sweepCtx.Done():
			return
		case <-timer.C:
		}
	}
	digest := s.activeBundleDigest()
	if digest == "" {
		return
	}
	state, err := sm.LoadBundleReprotectState()
	if err != nil {
		// Unknown marker state: do not sweep on every start.
		s.logger.Warn("bundle re-protection catch-up: could not read the marker, skipping", zap.Error(err))
		return
	}
	if state.FullDigest == digest {
		return
	}
	s.runBundleReprotect(sweepCtx, scanner.BundleActivation{Digest: digest, ActivatedAt: time.Now()})
}

func (s *Server) activeBundleDigest() string {
	if fn := s.bundleReprotect.activeDigest; fn != nil {
		return fn()
	}
	return scanner.BundleStatus().FullDigest
}

// runBundleReprotect sweeps once for act's bundle. A digest equal to the last
// sweep's starts nothing; sweeps are serialized, and one whose digest was
// superseded by a newer activation stops between servers.
func (s *Server) runBundleReprotect(ctx context.Context, act scanner.BundleActivation) {
	if act.Digest == "" {
		return
	}
	st := &s.bundleReprotect
	st.mu.Lock()
	st.latest = act.Digest
	if st.last == act.Digest {
		st.mu.Unlock()
		return
	}
	st.last = act.Digest
	st.mu.Unlock()

	st.runMu.Lock()
	defer st.runMu.Unlock()
	superseded := func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.latest != act.Digest
	}
	if ctx.Err() != nil || superseded() {
		return
	}
	s.reprotectSweep(ctx, act, superseded)
}

// reprotectSweep walks every configured server once: leg (i) for servers with
// held records, then leg (ii) for non-quarantined servers with approved ones.
func (s *Server) reprotectSweep(ctx context.Context, act scanner.BundleActivation, superseded func() bool) {
	sm := s.runtime.StorageManager()
	if sm == nil {
		return
	}
	s.publishReprotectStatus(scanner.BundleReprotectStatus{State: scanner.ReprotectStateRunning})
	servers, err := sm.ListUpstreamServers()
	if err != nil {
		s.logger.Warn("bundle re-protection: could not list servers", zap.Error(err))
		s.publishReprotectStatus(scanner.BundleReprotectStatus{State: scanner.ReprotectStateFailed, Reason: reprotectReasonStorageUnavailable})
		return
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })

	scanSvc := s.securityScannerSvc()
	// Leg (ii) needs a scanner service (absent in stdio mode) and the
	// auto_baseline_scan kill switch; with the switch off the sweep is
	// suspended and says so on every status surface.
	suspended := scanSvc != nil && !s.informationalScansEnabled()

	var scanned, hits, failed int
	scannedAny := false
	for _, sc := range servers {
		if ctx.Err() != nil || superseded() {
			s.logger.Debug("bundle re-protection sweep abandoned", zap.String("digest", shortDigest(act.Digest)))
			return
		}
		if sc == nil || sc.Name == "" {
			continue
		}
		records, err := sm.ListToolApprovals(sc.Name)
		if err != nil {
			failed++
			s.logger.Debug("bundle re-protection: could not read tool approvals", zap.String("server", sc.Name), zap.Error(err))
			continue
		}
		hasHeld, hasApproved := false, false
		for _, rec := range records {
			if rec == nil {
				continue
			}
			switch rec.Status {
			case storage.ToolApprovalStatusPending, storage.ToolApprovalStatusChanged:
				hasHeld = true
			case storage.ToolApprovalStatusApproved:
				hasApproved = true
			}
		}

		// Leg (i): evidence on held tools, quarantined servers included.
		if hasHeld {
			if err := s.reprotectRefreshHeld(sc.Name); err != nil {
				failed++
				s.logger.Debug("bundle re-protection: held evidence refresh failed", zap.String("server", sc.Name), zap.Error(err))
			}
		}

		// Leg (ii): a cached informational scan of the approved tools.
		if !hasApproved || sc.Quarantined || scanSvc == nil {
			continue
		}
		if !s.informationalScansEnabled() {
			suspended = true
			continue
		}
		if scannedAny && !sleepContext(ctx, s.bundleReprotect.pause()) {
			return
		}
		if superseded() {
			return
		}
		outcome := s.reprotectScanServer(ctx, scanSvc, sc.Name, act.Digest)
		switch outcome.kind {
		case reprotectAborted:
			return
		case reprotectSuspended:
			suspended = true
		case reprotectFailed:
			failed++
		case reprotectScanned:
			scanned++
			scannedAny = true
			if outcome.hit {
				hits++
				s.reprotectRecordHit()
			}
		}
	}

	if ctx.Err() != nil || superseded() {
		return
	}
	status := scanner.BundleReprotectStatus{State: scanner.ReprotectStateIdle, ServersScanned: scanned, ServersWithHits: hits}
	switch {
	case failed > 0:
		status.State, status.Reason = scanner.ReprotectStateFailed, reprotectReasonScanFailed
	case suspended:
		status.State, status.Reason = scanner.ReprotectStateSuspended, scanner.ReprotectReasonAutoBaselineScanOff
	default:
		now := time.Now().UTC()
		status.LastCompletedAt = &now
	}
	s.publishReprotectStatus(status)

	// The marker is written only when the whole sweep, scan leg included, ran
	// and nothing failed: it is the catch-up's only memory.
	if failed == 0 && !suspended && scanSvc != nil && ctx.Err() == nil {
		if err := sm.SaveBundleReprotectState(storage.BundleReprotectState{FullDigest: act.Digest, CompletedAt: time.Now().UTC()}); err != nil {
			s.logger.Warn("bundle re-protection: failed to persist the completion marker", zap.Error(err))
		}
	}
	s.logger.Info("bundle re-protection sweep completed",
		zap.String("digest", shortDigest(act.Digest)),
		zap.Bool("rollback", act.Rollback),
		zap.Int("servers_scanned", scanned),
		zap.Int("servers_with_hits", hits),
		zap.Int("failed", failed),
		zap.Bool("suspended", suspended))
}

type reprotectOutcomeKind int

const (
	reprotectSkipped reprotectOutcomeKind = iota
	reprotectScanned
	reprotectFailed
	reprotectSuspended
	reprotectAborted
)

type reprotectOutcome struct {
	kind reprotectOutcomeKind
	hit  bool
}

// reprotectScanServer runs one server's cached scan with its retry rules: a
// busy answer (ErrScanInProgress) re-arms without spending a failure, a server
// with nothing to scan is skipped, any other error spends one of the
// reprotectMaxAttempts. Each attempt holds infoScanRunMu, so the sweep never
// runs concurrently with an admission or per-change informational scan.
func (s *Server) reprotectScanServer(ctx context.Context, scanSvc securityScannerService, name, digest string) reprotectOutcome {
	attempts, busy := 0, 0
	for {
		if ctx.Err() != nil {
			return reprotectOutcome{kind: reprotectAborted}
		}
		if !s.informationalScansEnabled() {
			return reprotectOutcome{kind: reprotectSuspended}
		}
		summary, err := s.startReprotectScan(ctx, scanSvc, name, digest)
		switch {
		case err == nil:
			if summary != nil && summary.Status == "failed" {
				return reprotectOutcome{kind: reprotectFailed}
			}
			return reprotectOutcome{kind: reprotectScanned, hit: summary != nil && summary.Status == "dangerous"}
		case errors.Is(err, scanner.ErrNoCachedContracts):
			return reprotectOutcome{kind: reprotectSkipped}
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), ctx.Err() != nil:
			return reprotectOutcome{kind: reprotectAborted}
		case errors.Is(err, scanner.ErrScanInProgress):
			busy++
			if busy > maxRescanBusyRetries {
				return reprotectOutcome{kind: reprotectFailed}
			}
			if !sleepContext(ctx, s.bundleReprotect.busyDelay()) {
				return reprotectOutcome{kind: reprotectAborted}
			}
		default:
			attempts++
			s.logger.Debug("bundle re-protection scan did not start", zap.String("server", name), zap.Int("attempt", attempts), zap.Error(err))
			if attempts >= reprotectMaxAttempts {
				return reprotectOutcome{kind: reprotectFailed}
			}
		}
	}
}

// startReprotectScan starts one cached scan under infoScanRunMu and waits for
// its settle, returning the settled summary (nil when none was published in
// time). The wait only serializes the queue; a timeout is not an error.
func (s *Server) startReprotectScan(ctx context.Context, scanSvc securityScannerService, name, digest string) (*scanner.ScanSummary, error) {
	s.infoScanRunMu.Lock()
	defer s.infoScanRunMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := scanSvc.StartScanFromCachedContracts(ctx, name, digest); err != nil {
		return nil, err
	}
	timeout := s.infoScanSettleTimeout
	summary := scanSvc.GetScanSummary(ctx, name)
	if timeout <= 0 {
		return summary, nil
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(informationalScanPollInterval)
	defer ticker.Stop()
	for summary == nil || !isTerminalScanStatus(summary.Status) {
		select {
		case <-ctx.Done():
			return summary, nil
		case <-deadline.C:
			return summary, nil
		case <-ticker.C:
			summary = scanSvc.GetScanSummary(ctx, name)
		}
	}
	return summary, nil
}

func (s *Server) reprotectRefreshHeld(server string) error {
	if fn := s.bundleReprotect.refreshHeld; fn != nil {
		return fn(server)
	}
	return s.runtime.RefreshHeldScanEvidence(server)
}

func (s *Server) reprotectRecordHit() {
	if fn := s.bundleReprotect.recordHit; fn != nil {
		fn()
		return
	}
	telemetry.RecordTPABundleReprotectHitOn(s.runtime.TelemetryRegistry())
}

func (s *Server) publishReprotectStatus(status scanner.BundleReprotectStatus) {
	if fn := s.bundleReprotect.statusSink; fn != nil {
		fn(status)
		return
	}
	scanner.SetReprotectStatus(status)
}

// sleepContext waits d or until ctx ends; it reports whether the full wait
// elapsed.
func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// shortDigest is the first 12 characters of a digest, for logs.
func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}
