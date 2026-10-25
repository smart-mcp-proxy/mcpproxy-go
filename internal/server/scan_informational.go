package server

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Informational Pass-1 baseline scanning.
//
// The spec-086 admission scan (maybeStartAdmissionScan) only fires for
// trust_mode:"scan" servers. Before spec 119 made scan the default tier, a stock
// install never triggered the free, in-process TPA baseline scan and the
// security badges stayed empty forever; manual and auto servers still need
// these two paths:
//
//  1. every NEW server, in ANY trust mode, gets one baseline scan when it is
//     admitted (maybeStartInformationalScans, driven by servers.changed), and
//  2. once per installation, pre-existing never-scanned servers are swept
//     (runBaselineSweep, driven by startup behind a persisted marker).
//
// A third path (Spec 118, see maybeStartInformationalRescan below) re-scans a
// server a debounce after its tool inventory moves, so the review screen's scan
// coverage returns to "current" instead of "stale".
//
// All are INFORMATIONAL: the result is stored through the normal scan-summary
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
	// maxInformationalScanAttempts caps how many times one server's informational
	// scan may be retried per process. StartScan fails outright for a server it
	// cannot connect to ("no source files available and server is disconnected"),
	// and that failure path costs up to ~60s inside StartScan (EnsureConnected +
	// a 30s connection wait, twice) while holding infoScanRunMu. Retrying is
	// worth it for a server that was merely still connecting; retrying forever on
	// every servers.changed for a permanently broken one would stall the queue
	// and respawn the upstream process endlessly. After the cap the server keeps
	// its "known" mark and is left to a manual scan.
	maxInformationalScanAttempts = 3
)

// errInformationalScansDisabled is returned when the security.auto_baseline_scan
// kill switch was turned off after a scan was queued but before it ran. It is a
// clean skip, not a failure: the caller releases the claim so the server is
// picked up again if the flag comes back.
var errInformationalScansDisabled = errors.New("informational baseline scans are disabled")

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
// The rule is deliberately the blunt one: EVERY trust_mode:"scan" server belongs
// to the gating path, whatever its quarantine state or approval history.
//
// Narrower predicates were tried and are unsafe, because every input the settle
// handler gates on is MUTABLE while a scan is in flight, and
// maybeAutoApproveScanSettled re-reads all of them when the verdict lands:
//
//   - Quarantine: a scan-mode server unquarantined at claim time that the
//     operator quarantines mid-scan gets silently unquarantined by the clean
//     settle.
//   - Approval baseline: a scan-mode server WITH a baseline (which the settle
//     handler would normally bail on) loses it if the operator rejects the
//     server mid-scan — POST .../reject calls RejectServer, which deletes the
//     integrity baseline — and the clean settle then auto-approves the very
//     server that was just rejected.
//
// Since the informational path's whole contract is that it can never cause a
// gating state change, it simply never scans a server the settle handler could
// act on. Scan-mode servers still get their verdict from the gating path's own
// admission scan, or from a manual scan.
//
// KNOWN RESIDUAL WINDOW (cross-model review, PR #1031). The predicate is
// evaluated when the scan is CLAIMED, but the settle handler re-reads
// everything — including the trust mode itself — when the verdict lands. So a
// server informationally scanned as manual/auto that the operator switches to
// trust_mode:"scan" AND quarantines while the scan is in flight can still reach
// maybeAutoApproveScanSettled and be auto-approved by that clean verdict. No
// predicate here can close it: the decision belongs to the settle handler, and
// this path has no say once StartScan has been issued.
//
// Not closed in this change because the only real fix is scan PROVENANCE — the
// settle handler acting solely on scans the gating path itself started — which
// means threading a scan id through the runtime event payload
// (publishScanSettled carries server_name/status/findings only) and into
// spec-086's gating path, whose semantics this change deliberately leaves
// untouched. The window is narrow and the outcome is policy-consistent (a
// scan-mode + quarantined + no-baseline + clean server is exactly what spec 086
// auto-approves), and ApproveServer(force=false) still re-gates independently.
//
// KNOWN COVERAGE GAP, same review: a trust_mode:"scan" server that is NOT
// quarantined (e.g. quarantine_enabled:false globally) is scanned by NEITHER
// path — this one skips all scan-mode servers, and the gating path only handles
// quarantined ones. Narrowing this predicate to match is what the two bullets
// above rule out, so closing that gap also needs provenance.
func scanModeAdmissionOwns(sc *config.ServerConfig) bool {
	if sc == nil {
		return false
	}
	return sc.EffectiveTrustMode() == config.TrustModeScan
}

// informationalScansEnabled resolves the security.auto_baseline_scan kill switch
// (default ON) against the live config, and requires a scanner service.
func (s *Server) informationalScansEnabled() bool {
	if s.securityScannerSvc() == nil {
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
//
// DISABLED servers are deliberately NOT seeded — see the note on
// maybeStartInformationalScans. They are not the sweep's job either (the sweep
// skips them), so recording them here would strand them: enabling one later
// would look like a server that had "already been seen" and it would never be
// scanned by either path.
func (s *Server) seedKnownServers(servers []*config.ServerConfig) {
	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	if s.infoScanKnown == nil {
		s.infoScanKnown = make(map[string]bool, len(servers))
	}
	for _, sc := range servers {
		if sc != nil && sc.Name != "" && sc.Enabled {
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
//
// A DISABLED server is neither scanned nor recorded as "known". Recording it
// would be a permanent strand: claimInformationalScan refuses to scan a disabled
// server (the scan would have to start it to export tool definitions), so a
// server admitted disabled and enabled minutes later would look like one that
// had already been seen — the admission path would skip it as not-new and the
// sweep, being one-shot and marker-gated, would never come back for it. Its
// badge would read "not scanned" forever. Leaving it unrecorded costs one map
// lookup per servers.changed and lets the enable act as the admission.
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
		// Not recorded, not scanned: a disabled server stays "unseen" so that
		// enabling it later is what admits it. See the note above.
		if !sc.Enabled {
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
	scanSvc := s.securityScannerSvc()
	if sc == nil || sc.Name == "" || scanSvc == nil {
		return false
	}
	// Disabled servers are never scanned: the scan would have to start the
	// server to export its tool definitions.
	if !sc.Enabled {
		return false
	}
	// Already scanned (or a scan is in flight): GetScanSummary returns nil only
	// when no scan job exists at all — the one "never scanned" signal.
	if summary := scanSvc.GetScanSummary(ctx, sc.Name); summary != nil {
		return false
	}
	// Leave the gating admission path's servers alone: no double scan, and no
	// way for an informational verdict to reach the settle-driven auto-approval.
	if scanModeAdmissionOwns(sc) {
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
//
// Bounded by maxInformationalScanAttempts: past the cap the "known" mark stays,
// which retires the server from the automatic path instead of retrying a broken
// upstream on every servers.changed.
// unclaimInformationalScan reverses a claim WITHOUT counting a failed attempt.
// Used when the scan was skipped for a reason that says nothing about the server
// (the kill switch flipped while it was queued).
func (s *Server) unclaimInformationalScan(name string) {
	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	delete(s.infoScanQueued, name)
	delete(s.infoScanKnown, name)
}

// forgetInformationalScan drops every trace of a removed server: the claim, the
// known mark and the retry budget. A server added again under the same name is
// a new admission and must neither be skipped as "already seen" nor inherit a
// retired server's exhausted attempts.
func (s *Server) forgetInformationalScan(name string) {
	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	delete(s.infoScanQueued, name)
	delete(s.infoScanKnown, name)
	delete(s.infoScanAttempts, name)
	if pending := s.infoRescanTimers[name]; pending != nil {
		pending.timer.Stop()
		delete(s.infoRescanTimers, name)
	}
}

// forgetInformationalScansOnPurge registers the purge observer that forgets a
// removed (or replaced) server's informational-scan bookkeeping. It fires after
// a successful commit only, on every removal door (REST Server.RemoveServer and
// the MCP commit writer both commit through removeServerFromConfig), so a
// re-added server is a fresh admission while a removal that failed to persist
// changes nothing.
func (s *Server) forgetInformationalScansOnPurge() {
	if s.runtime == nil {
		return
	}
	s.runtime.AddServerPurgeObserver(func(names []string) {
		for _, name := range names {
			s.forgetInformationalScan(name)
		}
	})
}

func (s *Server) releaseInformationalScan(name string) {
	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	delete(s.infoScanQueued, name)
	if s.infoScanAttempts == nil {
		s.infoScanAttempts = make(map[string]int)
	}
	s.infoScanAttempts[name]++
	if s.infoScanAttempts[name] >= maxInformationalScanAttempts {
		s.logger.Debug("informational baseline scan retired after repeated start failures",
			zap.String("server", name),
			zap.Int("attempts", s.infoScanAttempts[name]))
		return
	}
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
			// A skip that says nothing about the SERVER is not a failed attempt
			// and must not consume one of its bounded retries. Two such skips:
			// the kill switch flipping while the scan sat in the queue, and the
			// server context being cancelled (shutdown) before StartScan ran. If
			// either counted, toggling the flag — or a shutdown draining a queue
			// on a Server object that is later restarted in-process — would
			// silently retire servers that never actually failed a scan.
			if errors.Is(err, errInformationalScansDisabled) || errors.Is(err, context.Canceled) ||
				errors.Is(err, context.DeadlineExceeded) {
				s.unclaimInformationalScan(name)
				return
			}
			s.releaseInformationalScan(name)
		}
	}()
}

// runInformationalScan launches one Pass-1 scan and waits for its verdict,
// returning the number of findings it produced. The mutex serializes every
// informational scan in the process (admission scans and sweep alike), which is
// what keeps the sweep from launching every server's scan at once.
func (s *Server) runInformationalScan(ctx context.Context, name string) (int, error) {
	return s.runInformationalScanGated(ctx, name, s.informationalScansEnabled)
}

// runInformationalScanGated is runInformationalScan with the kill switch it
// re-reads at the last decision point supplied by the caller: the per-change
// rescan also honours security.auto_rescan_on_change.
func (s *Server) runInformationalScanGated(ctx context.Context, name string, enabled func() bool) (int, error) {
	s.infoScanRunMu.Lock()
	defer s.infoScanRunMu.Unlock()

	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// Re-read the kill switch HERE, not only where the scan was queued. A queued
	// scan can sit behind infoScanRunMu for minutes while earlier scans settle,
	// and the sweep waits out its startup delay first — so an operator who sets
	// security.auto_baseline_scan:false in that window would otherwise still see
	// the automatic scans they just switched off fire one by one. The documented
	// contract is that the flag is read live at each decision point; this is the
	// last decision point before a scan actually starts.
	if !enabled() {
		return 0, errInformationalScansDisabled
	}
	scanSvc := s.securityScannerSvc()
	if scanSvc == nil {
		return 0, errInformationalScansDisabled
	}
	if _, err := scanSvc.StartScan(ctx, name, false, nil, ""); err != nil {
		return 0, err
	}
	return s.waitForInformationalScan(ctx, scanSvc, name), nil
}

// waitForInformationalScan blocks until the server's scan summary reaches a
// terminal status (or the timeout / shutdown fires) and returns its finding
// count. A timeout is not an error: the scan keeps running in the background,
// the wait only exists to serialize the queue.
func (s *Server) waitForInformationalScan(ctx context.Context, scanSvc securityScannerService, name string) int {
	timeout := s.infoScanSettleTimeout
	if timeout <= 0 {
		return 0
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(informationalScanPollInterval)
	defer ticker.Stop()

	for {
		if summary := scanSvc.GetScanSummary(ctx, name); summary != nil && isTerminalScanStatus(summary.Status) {
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
	// Re-read the kill switch after the startup delay: the operator has had 45
	// seconds to disable automatic scanning, and the sweep must honour that
	// rather than acting on the value it read before waiting.
	if !s.informationalScansEnabled() {
		s.logger.Debug("baseline sweep: disabled during startup delay, skipping (marker left unset)")
		return
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
		if errors.Is(err, errInformationalScansDisabled) {
			// The operator disabled automatic scanning mid-sweep. Abandon the
			// sweep WITHOUT marking it done — treating the remaining servers as
			// "failed" would still burn the marker whenever an earlier server had
			// already scanned, and they were never actually examined.
			s.unclaimInformationalScan(sc.Name)
			s.logger.Info("baseline sweep abandoned: automatic scanning was disabled; will resume if re-enabled",
				zap.Int("servers_scanned", scanned))
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// Shutdown, not a scan failure. Same rule as the admission path: an
			// uncounted unclaim, so draining the sweep on shutdown cannot spend a
			// server's bounded retries on scans that never ran. The loop's own
			// ctx.Err() checks handle abandoning without burning the marker.
			s.unclaimInformationalScan(sc.Name)
			continue
		}
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

	// Burn the one-shot marker only when the sweep actually FINISHED its job —
	// no candidate failed. "Nothing to scan at all" (failed == 0, scanned == 0)
	// still counts as finished.
	//
	// The weaker rule "scanned > 0 || failed == 0" stranded the failures: in a
	// mixed sweep where A scanned and B was still connecting, the marker was
	// burned on A's success and B never got a baseline scan on any later start.
	// (Within THIS process B is still retried — releaseInformationalScan clears
	// its known-mark for the next servers.changed — but that dies with the
	// process, and the marker is what outlives it.)
	//
	// Retrying is cheap and self-limiting: the next sweep's only candidates are
	// servers that still have no scan summary, so everything already scanned is
	// skipped by claimInformationalScan. A permanently unscannable server costs
	// one failed StartScan per start, in the background, off the startup path.
	if failed == 0 {
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

// ---------------------------------------------------------------------------
// Spec 118: informational rescan after a tool-definition change.
//
// The admission scan and the one-shot sweep scan a server once. After that, a
// tool whose definition changes (or a tool added after the baseline) is newer
// than the last scan, and the review screen reports "Scan out of date ...
// Rescan" until someone clicks. The runtime reports every post-baseline
// inventory move (AddToolDefinitionsChangedObserver); this path re-scans the
// server a few seconds later so the scan line returns to "current".
//
// Informational ONLY, like the other two paths: it starts a scan and nothing
// else. It approves nothing, unquarantines nothing and changes no tool's
// status (RT-02: a clean verdict never approves a changed contract). It reuses
// the admission machinery: the per-server claim (infoScanQueued), execution
// serialized behind infoScanRunMu, and the maxInformationalScanAttempts cap on
// failed starts.
// ---------------------------------------------------------------------------

const (
	// informationalRescanDebounce coalesces a burst of tool changes (one
	// reconnect re-listing many tools, a server that keeps editing a
	// description) into one rescan per server.
	informationalRescanDebounce = 5 * time.Second
	// maxRescanBusyRetries bounds how often a rescan that found the server's
	// scan slot taken (a manual scan, the admission scan, Pass 2) is re-armed.
	// A busy slot says nothing about the server, so it never consumes the
	// failure budget; 36 x 5s outlasts the 2-minute informational settle wait.
	maxRescanBusyRetries = 36
)

// infoRescanTimer is one server's pending debounce timer. seq identifies the
// arming that created it, so a firing timer only clears its own entry.
type infoRescanTimer struct {
	timer *time.Timer
	seq   uint64
}

// registerToolChangeRescan subscribes the debounced rescan to the runtime's
// post-baseline tool-inventory changes. Registered at construction, like the
// purge observer, so no change can slip in before HTTP startup.
func (s *Server) registerToolChangeRescan() {
	if s.runtime == nil {
		return
	}
	s.runtime.AddToolDefinitionsChangedObserver(s.scheduleInformationalRescan)
}

// autoRescanEnabled resolves security.auto_baseline_scan AND
// security.auto_rescan_on_change (each with its env override) against the live
// config, and requires a scanner service.
func (s *Server) autoRescanEnabled() bool {
	if s.securityScannerSvc() == nil {
		return false
	}
	var sec *config.SecurityConfig
	if cfg := s.runtime.Config(); cfg != nil {
		sec = cfg.Security
	}
	return sec.IsAutoChangeScanEnabled()
}

// rescanEligible reports whether a server may be rescanned after a tool change:
// enabled (a scan would have to start it), not trust_mode auto (changes
// auto-approve there, nothing is held for review), and not a quarantined
// scan-mode server — the spec-086 admission gate owns those, and an
// informational verdict must never reach its settle-driven auto-approval.
// A scan-mode server that is NOT quarantined has no auto-approval to reach and
// is rescanned; the residual window described on scanModeAdmissionOwns (an
// operator who quarantines it mid-scan) applies to it unchanged.
func rescanEligible(sc *config.ServerConfig) bool {
	if sc == nil || sc.Name == "" || !sc.Enabled {
		return false
	}
	if sc.EffectiveTrustMode() == config.TrustModeAuto {
		return false
	}
	return !scanModeAdmissionOwns(sc) || !sc.Quarantined
}

// scheduleInformationalRescan arms (or re-arms) the server's debounce timer.
// It runs on the discovery goroutine under the server's approval lock, so it
// only reads the config and sets a timer.
func (s *Server) scheduleInformationalRescan(name string) {
	if name == "" || !s.autoRescanEnabled() {
		return
	}
	s.armInformationalRescan(name, 0)
}

func (s *Server) armInformationalRescan(name string, busyRetries int) {
	debounce := s.infoRescanDebounce
	if debounce <= 0 {
		debounce = informationalRescanDebounce
	}
	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	if s.infoRescanTimers == nil {
		s.infoRescanTimers = make(map[string]*infoRescanTimer)
	}
	if pending := s.infoRescanTimers[name]; pending != nil {
		pending.timer.Stop() // coalesce: the newest change restarts the window
	}
	s.infoRescanSeq++
	seq := s.infoRescanSeq
	s.infoRescanTimers[name] = &infoRescanTimer{
		seq: seq,
		timer: time.AfterFunc(debounce, func() {
			s.fireInformationalRescan(name, seq, busyRetries)
		}),
	}
}

// fireInformationalRescan is the debounce timer's callback: it re-evaluates
// every gate (the switches, the server's state) at fire time, claims the
// server and runs one informational scan.
func (s *Server) fireInformationalRescan(name string, seq uint64, busyRetries int) {
	s.infoScanMu.Lock()
	if pending := s.infoRescanTimers[name]; pending != nil && pending.seq == seq {
		delete(s.infoRescanTimers, name)
	}
	s.infoScanMu.Unlock()

	ctx := s.informationalScanContext()
	if ctx.Err() != nil || !s.autoRescanEnabled() {
		return
	}
	var sc *config.ServerConfig
	for _, candidate := range s.listStoredServers() {
		if candidate != nil && candidate.Name == name {
			sc = candidate
			break
		}
	}
	if !rescanEligible(sc) {
		return
	}

	switch s.claimInformationalRescan(name) {
	case rescanClaimed:
	case rescanBusy:
		// A scan of this server is queued or running. The change may have landed
		// after that scan read the tool list, so look again after the window.
		if busyRetries < maxRescanBusyRetries {
			s.armInformationalRescan(name, busyRetries+1)
		}
		return
	default:
		return
	}

	s.logger.Info("queueing informational rescan after tool definition change",
		zap.String("server", name),
		zap.String("trust_mode", string(sc.EffectiveTrustMode())))
	_, err := s.runInformationalScanGated(ctx, name, s.autoRescanEnabled)
	s.infoScanMu.Lock()
	delete(s.infoScanQueued, name)
	s.infoScanMu.Unlock()
	switch {
	case err == nil:
	case errors.Is(err, errInformationalScansDisabled), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// Says nothing about the server: no failed attempt.
	case errors.Is(err, scanner.ErrScanInProgress):
		if busyRetries < maxRescanBusyRetries {
			s.armInformationalRescan(name, busyRetries+1)
		}
	default:
		s.logger.Debug("informational rescan did not run", zap.String("server", name), zap.Error(err))
		s.countRescanFailure(name)
	}
}

type rescanClaim int

const (
	rescanSkipped rescanClaim = iota // retired: the failure cap was reached
	rescanBusy                       // another informational scan holds the claim
	rescanClaimed
)

// claimInformationalRescan applies the shared claim rules to a rescan. Unlike
// the admission claim it does not require "never scanned" (that is the point),
// but it honours the same per-server claim and the failure cap.
func (s *Server) claimInformationalRescan(name string) rescanClaim {
	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	if s.infoScanAttempts[name] >= maxInformationalScanAttempts {
		return rescanSkipped
	}
	if s.infoScanQueued == nil {
		s.infoScanQueued = make(map[string]bool)
	}
	if s.infoScanQueued[name] {
		return rescanBusy
	}
	s.infoScanQueued[name] = true
	return rescanClaimed
}

// countRescanFailure spends one of the server's bounded attempts. Past the cap
// the server is left to a manual scan, exactly as for the admission scan.
func (s *Server) countRescanFailure(name string) {
	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	if s.infoScanAttempts == nil {
		s.infoScanAttempts = make(map[string]int)
	}
	s.infoScanAttempts[name]++
	if s.infoScanAttempts[name] >= maxInformationalScanAttempts {
		s.logger.Debug("informational rescan retired after repeated start failures",
			zap.String("server", name), zap.Int("attempts", s.infoScanAttempts[name]))
	}
}
