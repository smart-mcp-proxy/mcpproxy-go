package server

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Informational Pass-1 baseline scanning.
//
// The spec-086 admission scan (maybeStartAdmissionScan) only fires for
// trust_mode:"scan" servers, which is never the default — so on a stock install
// nothing ever triggered the free, in-process TPA baseline scan and the security
// badges stayed empty forever. This file adds the two paths that fix that:
//
//  1. every NEW server, in ANY trust mode, gets one baseline scan when it is
//     admitted (maybeStartInformationalScans, driven by servers.changed), and
//  2. once per installation, pre-existing never-scanned servers are swept
//     (runBaselineSweep, driven by startup behind a persisted marker).
//
// Both are INFORMATIONAL: the result is stored through the normal scan-summary
// path so the UI verdict/badge lights up, and it drives NO gating whatsoever.
// Quarantine and approval semantics are untouched — servers the scan-mode
// admission gate owns are deliberately skipped here so they are never scanned
// twice, and the settle handler (maybeAutoApproveScanSettled) can only approve
// scan-mode quarantined servers, which this path never scans.

const (
	// informationalScanSettleTimeout bounds how long a serialized informational
	// scan waits for its verdict before moving to the next server. The wait is
	// what serializes the sweep — without it every scan would be launched at
	// once, since StartScan returns as soon as the job is created.
	informationalScanSettleTimeout = 2 * time.Minute
	// informationalScanPollInterval is how often the settle wait re-reads the
	// scan summary.
	informationalScanPollInterval = 250 * time.Millisecond
	// baselineSweepStartDelay holds the one-shot sweep back until upstream
	// servers have had a chance to connect. A scan of a still-connecting server
	// cannot export its tool definitions and fails outright, which would burn
	// the one-shot marker on an empty result.
	baselineSweepStartDelay = 45 * time.Second
)

// isTerminalScanStatus reports whether a scan summary status means the scan has
// finished (successfully or not). "scanning" and "not_scanned" are transient.
func isTerminalScanStatus(status string) bool {
	switch status {
	case "clean", "warnings", "dangerous", "failed":
		return true
	default:
		return false
	}
}

// scanModeAdmissionOwns reports whether the spec-086 trust_mode:"scan" admission
// gate — and the settle-driven auto-approval behind it — is responsible for this
// server. Those servers must NOT be picked up by the informational path.
//
// The predicate deliberately does NOT look at sc.Quarantined, even though the
// gating path itself does. Quarantine is MUTABLE while a scan is in flight, and
// maybeAutoApproveScanSettled re-reads it when the scan settles: a scan-mode
// server that is unquarantined when we claim it, and is quarantined by the
// operator before the clean verdict lands, would be silently unquarantined by
// the settle handler — an informational scan causing a gating state change.
// Keying only on the IMMUTABLE-for-this-purpose pair (trust mode, prior approval
// baseline) closes that window: a scan-mode server without an approval baseline
// is exactly the set the settle handler can act on, so the informational path
// never touches it in any quarantine state. Scan-mode servers that already have
// an approval baseline are safe (the settle handler bails on them) and stay
// eligible for an informational badge.
func scanModeAdmissionOwns(sc *config.ServerConfig, hasApprovalBaseline bool) bool {
	if sc == nil {
		return false
	}
	return sc.EffectiveTrustMode() == config.TrustModeScan && !hasApprovalBaseline
}

// informationalScansEnabled resolves the security.auto_baseline_scan kill switch
// (default ON) against the live config, and requires a scanner service.
func (s *Server) informationalScansEnabled() bool {
	if s.securityScanner == nil {
		return false
	}
	var sec *config.SecurityConfig
	if cfg := s.runtime.Config(); cfg != nil {
		sec = cfg.Security
	}
	return sec.IsAutoBaselineScanEnabled()
}

// informationalScanContext returns the context informational scans run under:
// the live server context, so an in-flight scan or sweep is cancelled on
// shutdown. Falls back to context.Background() before the server has started
// (and in unit tests that drive the hooks directly).
func (s *Server) informationalScanContext() context.Context {
	s.mu.RLock()
	ctx := s.serverCtx
	s.mu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// seedKnownServers records the servers present at process start so the
// servers.changed handler can tell a genuinely NEW admission from the set that
// was already configured. Seeding from the startup config (rather than from the
// first servers.changed) is what makes "the very first server a fresh install
// adds" count as new.
func (s *Server) seedKnownServers(servers []*config.ServerConfig) {
	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	if s.infoScanKnown == nil {
		s.infoScanKnown = make(map[string]bool, len(servers))
	}
	for _, sc := range servers {
		if sc != nil && sc.Name != "" {
			s.infoScanKnown[sc.Name] = true
		}
	}
}

// listStoredServers returns a fresh snapshot of configured servers from storage.
// Storage — not runtime.Config().Servers — because Config() hands back a shared
// snapshot whose entries other goroutines mutate in place (see the same note on
// maybeStartAdmissionScans).
func (s *Server) listStoredServers() []*config.ServerConfig {
	sm := s.runtime.StorageManager()
	if sm == nil {
		return nil
	}
	servers, err := sm.ListUpstreamServers()
	if err != nil {
		s.logger.Debug("informational scan: failed to list servers", zap.Error(err))
		return nil
	}
	return servers
}

// maybeStartInformationalScans is the servers.changed hook for change 1: any
// server that appears for the first time since process start is a new admission
// and gets one informational baseline scan, regardless of trust mode. Servers
// that were already configured at startup are the baseline sweep's job and are
// only recorded here.
func (s *Server) maybeStartInformationalScans(ctx context.Context) {
	if !s.informationalScansEnabled() {
		return
	}
	servers := s.listStoredServers()
	if len(servers) == 0 {
		return
	}

	var candidates []*config.ServerConfig
	s.infoScanMu.Lock()
	if s.infoScanKnown == nil {
		s.infoScanKnown = make(map[string]bool, len(servers))
	}
	for _, sc := range servers {
		if sc == nil || sc.Name == "" {
			continue
		}
		if s.infoScanKnown[sc.Name] {
			continue
		}
		s.infoScanKnown[sc.Name] = true
		candidates = append(candidates, sc)
	}
	s.infoScanMu.Unlock()

	for _, sc := range candidates {
		s.startInformationalScan(ctx, sc, "admission")
	}
}

// claimInformationalScan applies the eligibility rules and, when the server
// qualifies, claims it so no other path scans it again this process. Returns
// false (without claiming) when the server must be skipped.
func (s *Server) claimInformationalScan(ctx context.Context, sc *config.ServerConfig) bool {
	if sc == nil || sc.Name == "" || s.securityScanner == nil {
		return false
	}
	// Disabled servers are never scanned: the scan would have to start the
	// server to export its tool definitions.
	if !sc.Enabled {
		return false
	}
	// Already scanned (or a scan is in flight): GetScanSummary returns nil only
	// when no scan job exists at all — the one "never scanned" signal.
	if summary := s.securityScanner.GetScanSummary(ctx, sc.Name); summary != nil {
		return false
	}
	// Leave the gating admission path's servers alone (no double scan).
	if scanModeAdmissionOwns(sc, s.securityScanner.HasApprovalBaseline(sc.Name)) {
		return false
	}

	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	if s.infoScanQueued == nil {
		s.infoScanQueued = make(map[string]bool)
	}
	if s.infoScanQueued[sc.Name] {
		return false
	}
	s.infoScanQueued[sc.Name] = true
	return true
}

// releaseInformationalScan drops a claim so a later servers.changed can retry a
// scan that failed to start.
//
// It must forget the server in BOTH maps. maybeStartInformationalScans records a
// name in infoScanKnown at the moment it decides the server is new, before the
// scan is attempted; dropping only the infoScanQueued claim would leave the
// server permanently "not new", so no later servers.changed could ever retry it
// and (once the one-shot sweep marker is burned) nothing would scan it again.
func (s *Server) releaseInformationalScan(name string) {
	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	delete(s.infoScanQueued, name)
	delete(s.infoScanKnown, name)
}

// startInformationalScan claims and runs one informational scan in the
// background. The scan itself is serialized against every other informational
// scan (see runInformationalScan), so a burst of admissions never fans out into
// concurrent scans.
func (s *Server) startInformationalScan(ctx context.Context, sc *config.ServerConfig, reason string) {
	if !s.claimInformationalScan(ctx, sc) {
		return
	}
	name := sc.Name
	s.logger.Info("queueing informational baseline scan",
		zap.String("server", name),
		zap.String("reason", reason),
		zap.String("trust_mode", string(sc.EffectiveTrustMode())))
	go func() {
		if _, err := s.runInformationalScan(ctx, name); err != nil {
			s.logger.Debug("informational baseline scan did not run",
				zap.String("server", name),
				zap.Error(err))
			s.releaseInformationalScan(name)
		}
	}()
}

// runInformationalScan launches one Pass-1 scan and waits for its verdict,
// returning the number of findings it produced. The mutex serializes every
// informational scan in the process (admission scans and sweep alike), which is
// what keeps the sweep from launching every server's scan at once.
func (s *Server) runInformationalScan(ctx context.Context, name string) (int, error) {
	s.infoScanRunMu.Lock()
	defer s.infoScanRunMu.Unlock()

	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if _, err := s.securityScanner.StartScan(ctx, name, false, nil, ""); err != nil {
		return 0, err
	}
	return s.waitForInformationalScan(ctx, name), nil
}

// waitForInformationalScan blocks until the server's scan summary reaches a
// terminal status (or the timeout / shutdown fires) and returns its finding
// count. A timeout is not an error: the scan keeps running in the background,
// the wait only exists to serialize the queue.
func (s *Server) waitForInformationalScan(ctx context.Context, name string) int {
	timeout := s.infoScanSettleTimeout
	if timeout <= 0 {
		return 0
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(informationalScanPollInterval)
	defer ticker.Stop()

	for {
		if summary := s.securityScanner.GetScanSummary(ctx, name); summary != nil && isTerminalScanStatus(summary.Status) {
			if summary.FindingCounts != nil {
				return summary.FindingCounts.Total
			}
			return 0
		}
		select {
		case <-ctx.Done():
			return 0
		case <-deadline.C:
			s.logger.Debug("informational baseline scan did not settle in time",
				zap.String("server", name),
				zap.Duration("timeout", timeout))
			return 0
		case <-ticker.C:
		}
	}
}

// runBaselineSweep is change 2: the one-shot, post-upgrade catch-up. On startup,
// if the persisted marker is absent, every enabled server that has never been
// scanned is swept through the informational path (serialized), then the marker
// is persisted so the sweep never runs again. Cancellable: a shutdown mid-sweep
// leaves the marker unwritten so the next start resumes it.
func (s *Server) runBaselineSweep(ctx context.Context) {
	if !s.informationalScansEnabled() {
		return
	}
	sm := s.runtime.StorageManager()
	if sm == nil {
		return
	}
	if s.infoScanSweepDelay > 0 {
		timer := time.NewTimer(s.infoScanSweepDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
	state, err := sm.LoadBaselineSweepState()
	if err != nil {
		// Unknown marker state: do NOT sweep. Re-running the sweep on every
		// start would be worse than skipping it once.
		s.logger.Warn("baseline sweep: could not read sweep marker, skipping", zap.Error(err))
		return
	}
	if state != nil {
		s.logger.Debug("baseline sweep: already completed, skipping",
			zap.String("version", state.Version),
			zap.Time("completed_at", state.CompletedAt))
		return
	}

	// Read the inventory DIRECTLY rather than through listStoredServers, which
	// collapses "storage read failed" into an empty slice. An empty slice is the
	// sweep's "nothing to do" signal and burns the one-shot marker — so a
	// transient storage error would permanently mark a sweep that never looked
	// at a single server. Fail closed: skip this start, retry on the next one.
	servers, err := sm.ListUpstreamServers()
	if err != nil {
		s.logger.Warn("baseline sweep: could not list servers, skipping (marker left unset)", zap.Error(err))
		return
	}
	scanned := 0
	findings := 0
	failed := 0
	for _, sc := range servers {
		if ctx.Err() != nil {
			s.logger.Info("baseline sweep cancelled before completion; will resume on next start",
				zap.Int("servers_scanned", scanned))
			return
		}
		if !s.claimInformationalScan(ctx, sc) {
			continue
		}
		n, err := s.runInformationalScan(ctx, sc.Name)
		if err != nil {
			failed++
			s.releaseInformationalScan(sc.Name)
			s.logger.Debug("baseline sweep: scan did not run",
				zap.String("server", sc.Name),
				zap.Error(err))
			continue
		}
		scanned++
		findings += n
	}
	if ctx.Err() != nil {
		s.logger.Info("baseline sweep cancelled before completion; will resume on next start",
			zap.Int("servers_scanned", scanned))
		return
	}

	// Burn the one-shot marker only when the sweep actually achieved something:
	// scanned at least one server, or had nothing to scan at all. A sweep where
	// every candidate failed (servers still connecting, unreachable) is left
	// unmarked so the next start retries it.
	if scanned > 0 || failed == 0 {
		if err := sm.SaveBaselineSweepState(&storage.BaselineSweepState{
			Version:        httpapi.GetBuildVersion(),
			CompletedAt:    time.Now(),
			ServersScanned: scanned,
			Findings:       findings,
		}); err != nil {
			s.logger.Warn("baseline sweep: failed to persist sweep marker", zap.Error(err))
		}
	}

	s.logger.Info("baseline sweep completed",
		zap.Int("servers_scanned", scanned),
		zap.Int("findings", findings),
		zap.Int("servers_failed", failed),
		zap.Int("servers_considered", len(servers)))
}
