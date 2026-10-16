package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/supervisor"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

const connectAttemptTimeout = 3*time.Minute + 15*time.Second // Must exceed per-server Docker timeout (3min)

// StartBackgroundInitialization kicks off configuration sync and background loops.
func (r *Runtime) StartBackgroundInitialization() {
	// Start activity service for persisting tool call events
	if r.activityService != nil {
		// Set event emitter for sensitive data detection events (Spec 026)
		r.activityService.SetEventEmitter(r)
		go r.activityService.Start(r.appCtx, r)
		r.logger.Info("Activity service started for event logging")
	}

	// Start update checker for background version checking
	if r.updateChecker != nil {
		go r.updateChecker.Start(r.appCtx)
		r.logger.Info("Update checker background process started")
	}

	// Start telemetry service for anonymous usage heartbeats (Spec 036)
	if r.telemetryService != nil {
		go r.telemetryService.Start(r.appCtx)
		r.logger.Info("Telemetry service background process started")
	}

	// Clean up orphaned OAuth tokens before starting the refresh manager
	// This removes tokens for servers that were deleted while mcpproxy was not running
	if r.storageManager != nil {
		r.mu.RLock()
		validServerNames := make([]string, 0, len(r.cfg.Servers))
		for _, server := range r.cfg.Servers {
			validServerNames = append(validServerNames, server.Name)
		}
		r.mu.RUnlock()

		if deleted, err := r.storageManager.CleanupOrphanedOAuthTokens(validServerNames); err != nil {
			r.logger.Warn("Failed to cleanup orphaned OAuth tokens", zap.Error(err))
		} else if deleted > 0 {
			r.logger.Info("Cleaned up orphaned OAuth tokens during startup",
				zap.Int("deleted", deleted))
		}
	}

	// Start proactive OAuth token refresh manager
	if r.refreshManager != nil {
		r.refreshManager.SetRuntime(r)
		r.refreshManager.SetEventEmitter(r)
		if err := r.refreshManager.Start(r.appCtx); err != nil {
			r.logger.Error("Failed to start OAuth refresh manager", zap.Error(err))
		} else {
			r.logger.Info("OAuth refresh manager started")
		}

		// Register token saved callback to wire PersistentTokenStore -> RefreshManager
		// Spec 116 PR2b: the binding the token was saved under goes onto the
		// schedule, so a superseded instance's reactive failure never fails it.
		oauth.GetTokenStoreManager().SetTokenSavedBoundCallback(func(serverName string, expiresAt time.Time, binding commitview.CredentialBinding) {
			r.logger.Info("OnTokenSaved callback fired - scheduling proactive refresh",
				zap.String("server", serverName),
				zap.Time("expires_at", expiresAt),
				zap.Duration("valid_for", time.Until(expiresAt)),
				zap.String("binding", binding.String()))
			r.refreshManager.OnTokenSavedFor(serverName, expiresAt, binding)
		})
		r.logger.Info("Token saved callback registered for proactive refresh")
	}

	// Issue #937 (review): admission must be settled BEFORE the supervisor can
	// reconcile. gateInitialConfig commits the snapshot NewService was
	// constructed with through the config commit, whose gate step runs the
	// admission gate (Spec 116: every later commit runs it too). It runs while
	// the supervisor is still stopped, so the startup gate cannot race a
	// reconcile that would connect an ungated server and index its tools.
	r.gateInitialConfig()

	// Phase 6: Start Supervisor for state reconciliation and lock-free reads
	if r.supervisor != nil {
		r.supervisor.Start()
		r.logger.Info("Supervisor started for state reconciliation")

		// Set up reactive tool discovery callback with deduplication
		r.supervisor.SetOnServerConnectedCallback(func(serverName string) {
			// Spec 044 (T040): activation funnel — mark first-ever successful
			// upstream connect. Monotonic; cheap; safe to call on every event.
			r.MarkFirstConnectedServerForActivation()

			r.onServerConnectedDiscovery(serverName)
		})
		r.logger.Info("Reactive tool discovery callback registered")

		// Subscribe to supervisor events and emit servers.changed for Web UI updates
		go r.supervisorEventForwarder()
	}

	// Set up tool discovery callback on upstream manager for notifications/tools/list_changed
	// This enables reactive tool re-indexing when upstream servers change their available tools
	if r.upstreamManager != nil {
		r.upstreamManager.SetToolDiscoveryCallback(r.onUpstreamToolsListChanged)
		r.logger.Info("Tool discovery callback registered on upstream manager")

		// F13: keep the aggregated prompt list fresh when an upstream adds/removes
		// a prompt at runtime (notifications/prompts/list_changed). Prompts are
		// aggregated inside the MCP server layer, so we cannot RefreshPrompts from
		// here — instead publish a debounced EventTypeUpstreamPromptsChanged that
		// listenForRoutingModeRefresh turns into a single RefreshPrompts on its own
		// goroutine (no new reentrancy).
		r.promptsRefresh = newPromptsRefreshDebouncer(promptsRefreshDebounceWindow, r.emitUpstreamPromptsChanged)
		r.upstreamManager.SetPromptsChangedCallback(func(serverName string) {
			// Only meaningful while aggregation is on. Read live config so a
			// hot-reload flip of aggregate_upstream_prompts takes effect without a
			// restart; short-circuiting here avoids waking the listener (and
			// re-setting built-ins on every routing-mode server) for a feature
			// nobody enabled. RefreshPrompts double-guards on the same live flags.
			cfg := r.Config()
			if cfg == nil || !cfg.EnablePrompts || !cfg.AggregateUpstreamPrompts {
				return
			}
			r.logger.Debug("upstream prompts/list_changed received; scheduling prompt refresh",
				zap.String("server", serverName))
			r.promptsRefresh.trigger()
		})
		r.logger.Info("Upstream prompts-changed callback registered on upstream manager")
	}

	// Spec 113-d FR-068: a rolling call-failure-rate flip changes a server's
	// computed health level with no connection-state event, so nudge the
	// (coalesced) servers.changed stream. The manager already debounces
	// per server.
	if r.upstreamManager != nil {
		r.upstreamManager.SetCallHealthObserver(func(serverName string) {
			r.emitServersChanged("call_failure_rate", map[string]any{"server": serverName})
		})
	}

	// Watch the config file for external edits (editors, CLI, `jq > tmp && mv`)
	// and hot-reload them through the canonical disk-reload path. Failure
	// degrades gracefully to no hot-reload (warning logged inside).
	if p := r.ConfigSnapshot().Path; p != "" {
		_ = r.startConfigFileWatcher(r.appCtx, p)
	}

	go r.backgroundInitialization()
}

func (r *Runtime) backgroundInitialization() {
	if r.CurrentPhase() == PhaseInitializing {
		r.UpdatePhase(PhaseLoading, "Loading configuration...")
	} else {
		r.UpdatePhaseMessage("Loading configuration...")
	}

	appCtx := r.AppContext()

	// The boot reconcile (Spec 116 review R9.6): one commit that repairs the
	// storage projection synchronously (fast) and schedules a connect for every
	// enabled server (slow, asynchronous). Synchronous so the API /servers
	// endpoint has data immediately.
	if err := r.bootReconcile(); err != nil {
		r.logger.Error("Failed to load configured servers", zap.Error(err))
		// Don't set error phase - servers can be loaded later via config reload
	}

	// Mark as ready - storage is now populated with server configs
	switch r.CurrentPhase() {
	case PhaseInitializing, PhaseLoading, PhaseReady:
		r.UpdatePhase(PhaseReady, "Server is ready (upstream servers connecting in background)")
	default:
		r.UpdatePhaseMessage("Server is ready (upstream servers connecting in background)")
	}

	// Start connection retry attempts in background
	go r.backgroundConnections(appCtx)

	// Start tool indexing with reduced delay
	go r.backgroundToolIndexing(appCtx)

	// Start session inactivity cleanup
	go r.backgroundSessionCleanup(appCtx)
}

func (r *Runtime) backgroundConnections(ctx context.Context) {
	r.connectAllWithRetry(ctx)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.connectAllWithRetry(ctx)
		case <-ctx.Done():
			r.logger.Info("Background connections stopped due to context cancellation")
			return
		}
	}
}

func (r *Runtime) connectAllWithRetry(ctx context.Context) {
	if r.upstreamManager == nil {
		return
	}

	stats := r.upstreamManager.GetStats()
	connectedCount := 0
	totalCount := 0

	if serverStats, ok := stats["servers"].(map[string]interface{}); ok {
		totalCount = len(serverStats)
		for _, serverStat := range serverStats {
			if stat, ok := serverStat.(map[string]interface{}); ok {
				if connected, ok := stat["connected"].(bool); ok && connected {
					connectedCount++
				}
			}
		}
	}

	if connectedCount < totalCount {
		r.UpdatePhaseMessage(fmt.Sprintf("Connected to %d/%d servers, retrying...", connectedCount, totalCount))

		connectCtx, cancel := context.WithTimeout(ctx, connectAttemptTimeout)
		defer cancel()

		if err := r.upstreamManager.ConnectAll(connectCtx); err != nil {
			r.logger.Warn("Some upstream servers failed to connect", zap.Error(err))
		}
	}
}

// toolDiscoveryDisabledRecheckInterval is how long the indexing loop sleeps
// between re-checks when the periodic sweep is disabled (resolved interval
// <= 0). It is NOT a sweep — connect-time discovery and reactive
// notifications/tools/list_changed still keep the index fresh; this only lets a
// later config hot-reload re-enable the sweep without a restart (spec 074).
const toolDiscoveryDisabledRecheckInterval = 5 * time.Minute

// planToolDiscoveryCycle decides one iteration of the indexing loop: whether to
// run a periodic sweep and how long to wait first. When at least one server (or
// the global default) has a positive resolved interval the loop ticks at the
// smallest such cadence (tick) and sweeps; when every interval is disabled
// (anyEnabled=false) the loop waits the re-check window without sweeping so a
// later hot-reload can re-enable it.
func planToolDiscoveryCycle(tick time.Duration, anyEnabled bool, disabledRecheck time.Duration) (sweep bool, wait time.Duration) {
	if !anyEnabled || tick <= 0 {
		return false, disabledRecheck
	}
	return true, tick
}

func (r *Runtime) backgroundToolIndexing(ctx context.Context) {
	r.cleanupOrphanedIndexEntries()

	select {
	case <-time.After(2 * time.Second):
		_ = r.DiscoverAndIndexTools(ctx)
	case <-ctx.Done():
		r.logger.Info("Background tool indexing stopped during initial delay")
		return
	}

	// Re-resolve the tool-discovery cadence every cycle so a config hot-reload
	// changes the sweep interval (or disables it) without a restart (spec 074,
	// FR-012). The loop ticks at the smallest per-server cadence and the sweep
	// itself (DiscoverToolsDue) only re-lists servers whose own interval has
	// elapsed, so per-server overrides take effect (US3/SC-006/FR-005). The tick
	// is resolved from the upstream manager's thread-safe per-client config
	// snapshots — iterating r.Config().Servers here would race in-place config
	// mutation (the shared snapshot is copy-on-write; see runtime.Config()).
	for {
		tick, anyEnabled := r.upstreamManager.ResolveToolDiscoverySweepTick(r.Config())
		sweep, wait := planToolDiscoveryCycle(tick, anyEnabled, toolDiscoveryDisabledRecheckInterval)
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
			if sweep {
				_ = r.discoverAndIndexTools(ctx, true)
			}
		case <-ctx.Done():
			timer.Stop()
			r.logger.Info("Background tool indexing stopped due to context cancellation")
			return
		}
	}
}

// backgroundSessionCleanup periodically closes sessions that haven't had activity.
// This handles the HTTP transport limitation where OnUnregisterSession is never called.
func (r *Runtime) backgroundSessionCleanup(ctx context.Context) {
	// Session inactivity timeout: 30 minutes
	// MCP clients may have gaps between tool calls (e.g., user reading results).
	// 5 minutes was too aggressive and caused sessions to appear stale.
	const sessionInactivityTimeout = 30 * time.Minute

	// Check every minute for inactive sessions
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if r.storageManager != nil {
				closedCount, err := r.storageManager.CloseInactiveSessions(sessionInactivityTimeout)
				if err != nil {
					r.logger.Warn("Failed to close inactive sessions", zap.Error(err))
				} else if closedCount > 0 {
					r.logger.Debug("Closed inactive sessions",
						zap.Int("count", closedCount),
						zap.Duration("timeout", sessionInactivityTimeout))
				}
			}
		case <-ctx.Done():
			r.logger.Info("Background session cleanup stopped due to context cancellation")
			return
		}
	}
}

// DiscoverAndIndexTools discovers tools from ALL connected upstream servers and
// indexes them. Used by event-driven callers (boot, server reload, manual
// refresh) that want a full sweep regardless of per-server cadence.
func (r *Runtime) DiscoverAndIndexTools(ctx context.Context) error {
	return r.discoverAndIndexTools(ctx, false)
}

// discoverAndIndexTools discovers tools and updates the index. When dueOnly is
// true (the periodic spec-074 sweep) only servers whose per-server
// tool_discovery_interval has elapsed are re-listed; servers omitted from the
// sweep keep their last-good index snapshot, so the index does not shrink.
func (r *Runtime) discoverAndIndexTools(ctx context.Context, dueOnly bool) error {
	if r.upstreamManager == nil || r.indexManager == nil {
		return fmt.Errorf("runtime managers not initialized")
	}

	r.logger.Info("Discovering and indexing tools...", zap.Bool("due_only", dueOnly))

	// Capture every server's connection generation AND live connection
	// token BEFORE listing: the publish below is bound to the generation, so
	// a result whose capture straddled a reconnect is dropped rather than
	// landing on the new connection (Spec 105 FR-009 "stale generation";
	// astra r1 I2), and stamped with the token so an identity read can tell
	// whether it still describes the live connection (astra r2 C3).
	gens := r.discoveryGenerations()
	instanceGens := r.instanceGenerations()

	// Each listed server comes with the capture of its own tools/list
	// (Spec 117 PR-3, FR-031), so every server's result is applied in
	// capture order: a result older than one already applied for that server
	// (a concurrent connect or tools/list_changed pass) writes nothing.
	tools, listed, captures, err := r.upstreamManager.DiscoverToolsReportCaptured(ctx, dueOnly)
	if err != nil {
		return fmt.Errorf("failed to discover tools: %w", err)
	}

	// Group tools by server name for differential updates
	toolsByServer := make(map[string][]*config.ToolMetadata)
	for _, tool := range tools {
		toolsByServer[tool.ServerName] = append(toolsByServer[tool.ServerName], tool)
	}

	// A server whose tools/list SUCCEEDED with zero tools has completed its
	// discovery just as surely as one that listed ten: stamp it so its names
	// resolve as absent instead of lingering in the connect→discovery window
	// (Spec 105 FR-009, research D4). Its tool set is left alone — the sweep
	// never wipes a server on an empty result — and servers the sweep skipped
	// or that failed to list are not stamped.
	// An empty result from a superseded instance generation is re-listed
	// below like a non-empty one (review round 7).
	staleEmpty := r.markZeroToolServersDiscovered(listed, toolsByServer, gens, instanceGens, captures)

	if len(tools) == 0 {
		r.logger.Warn("No tools discovered from upstream servers")
		r.relistStaleSweepServers(ctx, staleEmpty)
		return nil
	}

	// Snapshot the set of currently-known servers so we can prune entries for
	// servers that have been removed from config and avoid an unbounded map.
	knownServers := r.upstreamManager.GetAllServerNames()
	knownServerSet := make(map[string]struct{}, len(knownServers))
	for _, name := range knownServers {
		knownServerSet[name] = struct{}{}
	}

	// Prune entries for servers removed from config. Fresh snapshots are NOT
	// published here: each server's snapshot, capture stamp and approval
	// records are committed together under captureSnapMu by
	// commitDiscoveredTools, so a concurrent review never reads one
	// generation's records with another's stamp.
	r.lastGoodToolsMu.Lock()
	for serverName := range r.lastGoodTools {
		if _, ok := knownServerSet[serverName]; !ok {
			delete(r.lastGoodTools, serverName)
			delete(r.lastGoodGen, serverName)
			delete(r.lastCaptureAt, serverName)
			delete(r.lastGoodCapture, serverName)
		}
	}
	r.lastGoodToolsMu.Unlock()

	// Commit each discovered server: publish its snapshot and apply the
	// differential update atomically with respect to review reads. A
	// quarantined or disabled server is never (re)indexed by the sweep
	// (issue #873).
	//
	// Each server's StateView publish runs inside that commit, in the same
	// approval-lock hold as its D15 check (FR-008a). A server whose connection
	// changed while the sweep was listing had its result dropped and is
	// collected in stale.
	processedServers := make(map[string]struct{}, len(toolsByServer))
	stale := staleEmpty
	for serverName, serverTools := range toolsByServer {
		processedServers[serverName] = struct{}{}
		// No generation captured before the list: the result cannot be bound
		// to a connection, so it is indexed but not published into the
		// StateView, and the server is re-listed (as before).
		var capture *supervisor.DiscoveryCapture
		c, captured := gens[serverName]
		if r.supervisor != nil && captured {
			capture = &c
		}
		eligible, published, err := r.commitInventoryAt(ctx, serverName, serverTools, instanceGens[serverName], capture,
			inventoryRef{capture: captures[serverName]})
		if err != nil {
			r.logger.Error("Failed to apply differential update for server during sweep",
				zap.String("server", serverName), zap.Error(err))
		}
		if eligible && r.supervisor != nil && (!captured || !published) {
			stale = append(stale, serverName)
		}
	}

	// For connected servers that were temporarily missing from discovery results,
	// re-apply last-good snapshot to avoid transient index shrink.
	for _, serverName := range knownServers {
		if _, ok := processedServers[serverName]; ok {
			continue
		}

		// SECURITY-CRITICAL GUARD (issue #873): this last-good fallback is the
		// real exposure — DiscoverTools already skips quarantined/disabled servers
		// (so they never enter the primary loop), but a quarantined server stays
		// connected and keeps its pre-quarantine snapshot, so without a guard the
		// fallback would reapply it. QuarantineServer deletes the server's index
		// entries and then triggers this very sweep, which would immediately
		// restore them. Skip ineligible servers before doing any snapshot work.
		if !r.serverEligibleForIndexing(serverName) {
			continue
		}

		client, ok := r.upstreamManager.GetClient(serverName)
		if !ok || client == nil || !client.IsConnected() {
			continue
		}

		r.reapplyLastGoodSnapshot(ctx, serverName)
	}

	// Invalidate tool count caches since tools may have changed
	r.upstreamManager.InvalidateAllToolCountCaches()

	// A server whose connection changed while the sweep was listing had its
	// result dropped: re-list it under its current connection so it does not
	// linger in the connect→discovery window until the next sweep tick (the
	// reactive connect discovery may have been skipped by the in-progress
	// dedup).
	r.relistStaleSweepServers(ctx, stale)

	// Profiles v2 (Spec 057, T1): reconcile per-profile indexes against the
	// current config — build new profiles, rebuild those whose membership changed
	// and drop removed ones. Unchanged profiles are left untouched.
	r.reconcileProfileIndexes()

	r.logger.Info("Successfully indexed tools", zap.Int("count", len(tools)))
	return nil
}

// lastGoodToolsSnapshot returns a copy of the most recently discovered tool set
// for a server, or nil when none has been captured yet. Returning a copy lets
// callers pass it to applyDifferentialToolUpdate without holding the lock.
func (r *Runtime) lastGoodToolsSnapshot(serverName string) []*config.ToolMetadata {
	r.lastGoodToolsMu.RLock()
	defer r.lastGoodToolsMu.RUnlock()
	snapshot := r.lastGoodTools[serverName]
	if len(snapshot) == 0 {
		return nil
	}
	cp := make([]*config.ToolMetadata, len(snapshot))
	copy(cp, snapshot)
	return cp
}

// DiscoverAndIndexToolsForServer discovers and indexes tools for a single server.
// This is the LENIENT entry point used by reactive tool discovery (server
// connect, notifications/tools/list_changed): a successful ListTools that
// returns zero tools is treated as a transient blip and leaves the existing
// index untouched (see the authoritative variant for the explicit-refresh path).
// Implements retry logic with exponential backoff for robustness.
func (r *Runtime) DiscoverAndIndexToolsForServer(ctx context.Context, serverName string) error {
	return r.discoverAndIndexToolsForServer(ctx, serverName, false)
}

// RefreshServerTools re-discovers and re-indexes a single server AUTHORITATIVELY
// (issue #873): a successful ListTools that returns zero tools is taken as the
// truth — the server's stale index entries are removed and its last-good
// snapshot is cleared, so a later approval-driven reindex cannot resurface tools
// that no longer exist upstream. This is the explicit operator refresh/discover
// path; the reactive callbacks keep the lenient behavior above.
func (r *Runtime) RefreshServerTools(ctx context.Context, serverName string) error {
	// A quarantined server is deliberately excluded from the search index, but
	// an operator can still explicitly fetch its definitions for informed
	// review. Capture the same approval records as normal discovery without
	// publishing untrusted text to search or tool-routing surfaces.
	if r.serverIsQuarantined(serverName) {
		return r.captureQuarantinedToolDefinitions(ctx, serverName)
	}
	return r.discoverAndIndexToolsForServer(ctx, serverName, true)
}

func (r *Runtime) serverIsQuarantined(serverName string) bool {
	for _, candidate := range r.Config().Servers {
		if candidate != nil && candidate.Name == serverName {
			return candidate.Enabled && candidate.Quarantined
		}
	}
	return false
}

// captureQuarantinedToolDefinitions records a live tools/list response for
// review without indexing it. The caller is the explicit refresh route, which
// is invoked after the scanner's temporary inspection connection is ready.
func (r *Runtime) captureQuarantinedToolDefinitions(ctx context.Context, serverName string) error {
	if r.upstreamManager == nil {
		return fmt.Errorf("upstream manager not initialized")
	}

	// The server may have been disconnected solely because it is quarantined.
	// An explicit review refresh is an inspection-only operation: grant the
	// supervisor's bounded exemption, request a connection, then re-fetch the
	// live client as reconciliation may replace it.
	if r.supervisor != nil {
		if err := r.supervisor.RequestInspectionExemption(serverName, 15*time.Minute); err != nil {
			r.logger.Warn("Failed to grant inspection exemption for definition capture", zap.String("server", serverName), zap.Error(err))
		}
	}
	var lastErr error
	// RequestInspectionExemption triggers supervisor reconciliation. Do not call
	// RestartServer here: a second restart can cancel that in-flight inspection
	// connection before tools/list completes. Re-read the managed client until
	// the supervisor has published its replacement.
	for attempt := 0; attempt < 24; attempt++ {
		// Take the supervisor capture before reading the managed client. If a
		// replacement lands between these reads, the pointer/epoch validation
		// below rejects the mismatched pair and retries under the replacement.
		capture := r.discoveryGeneration(serverName)
		if client, ok := r.upstreamManager.GetClient(serverName); ok {
			// The commit generation of the config this instance runs, taken
			// before the list (Spec 116 FR-008a): a capture from an instance a
			// commit has since superseded is dropped and re-listed.
			instanceGen := client.Generation()
			// Bind this inspection result to the connection that supplied it.
			// RequestInspectionExemption may reconcile while tools/list is in
			// flight, replacing the managed client. Capturing an old client's
			// untrusted definition under the replacement connection would make
			// the review record lie about what is currently offered.
			// A fresh list: an explicit inspection must not reuse a
			// tools/list that was already in flight (Sol r5 P2).
			if tools, inv, err := client.ListToolsFreshCaptured(ctx); err == nil {
				if !r.quarantinedCaptureIsCurrent(serverName, client, capture) {
					lastErr = fmt.Errorf("inspection client replaced during tools/list")
					r.logger.Info("Quarantined definition capture used a superseded connection; re-listing",
						zap.String("server", serverName), zap.Int("attempt", attempt+1))
					continue
				}
				published, err := r.captureQuarantinedToolDefinitionsFromCurrentClient(serverName, client, capture, instanceGen, tools, inv)
				if err != nil {
					return err
				}
				if published {
					return nil
				}
				lastErr = fmt.Errorf("inspection client replaced before definition capture")
				continue
			} else {
				lastErr = err
			}
		} else {
			lastErr = fmt.Errorf("client not found")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("capture review definitions for %s: %w", serverName, ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("list tools for quarantined server %s: %w", serverName, lastErr)
}

// onUpstreamToolsListChanged is the upstream manager's tool-discovery
// callback for notifications/tools/list_changed: it re-runs single-server
// discovery (which, for a quarantined server with an approved baseline,
// verifies its contracts without indexing them — RT-05), deduplicated against
// a discovery already in flight for the same server. A notification that
// arrives during an in-flight discovery is coalesced into a follow-up run of
// that discovery (Sol r3 P2): the in-flight run may already have listed the
// tools the notification announces as replaced, and periodic discovery skips
// quarantined servers, so dropping it could leave a stale approval in place
// indefinitely.
//
// The returned error is that of the notification's own run only. Follow-ups
// may run in a deferred worker after this handler returned, so they never
// write the handler's state: their failures are logged where they run
// (Sol r5 P2, data race).
func (r *Runtime) onUpstreamToolsListChanged(ctx context.Context, serverName string) error {
	var ownErr error
	r.runCoalescedDiscovery(serverName, func(followUp bool) error {
		runCtx := ctx
		if followUp {
			// A follow-up runs after the notification's own run and may
			// outlive the notification's 30s deadline: give it a fresh
			// runtime-scoped timeout, as connect-owned discovery does, so an
			// expired notification context never turns the follow-up into a
			// silent no-op (Sol r4 P2). It derives from the lifetime context
			// so a follow-up started after shutdown is already cancelled.
			var cancel context.CancelFunc
			runCtx, cancel = context.WithTimeout(r.lifetimeContext(), discoveryRunTimeout)
			defer cancel()
		}
		r.logger.Info("Tool discovery triggered by notification",
			zap.String("server", serverName), zap.Bool("follow_up", followUp))
		discover := r.DiscoverAndIndexToolsForServer
		if r.notificationDiscoverHook != nil {
			discover = r.notificationDiscoverHook
		}
		runErr := discover(runCtx, serverName)
		if followUp {
			if runErr != nil {
				r.logger.Warn("Coalesced tool discovery follow-up failed",
					zap.String("server", serverName), zap.Error(runErr))
			}
			return runErr
		}
		ownErr = runErr
		return runErr
	})
	return ownErr
}

// onServerConnectedDiscovery runs connect-owned reactive tool discovery,
// deduplicated against a discovery already in flight for this server; a
// trigger arriving meanwhile is coalesced into one follow-up run rather than
// dropped (Sol r3 P2).
//
// Every run derives its context from the lifetime context, not AppContext:
// Close replaces the app context with context.Background(), so a follow-up
// that starts after shutdown would otherwise run uncancelled (Sol r7 P2).
func (r *Runtime) onServerConnectedDiscovery(serverName string) {
	r.runCoalescedDiscovery(serverName, func(followUp bool) error {
		ctx, cancel := context.WithTimeout(r.lifetimeContext(), discoveryRunTimeout)
		defer cancel()

		r.logger.Info("Reactive tool discovery triggered",
			zap.String("server", serverName), zap.Bool("follow_up", followUp))
		discover := r.DiscoverAndIndexToolsForServer
		if r.connectDiscoverHook != nil {
			discover = r.connectDiscoverHook
		}
		if err := discover(ctx, serverName); err != nil {
			r.logger.Error("Failed to discover tools for connected server",
				zap.String("server", serverName),
				zap.Error(err))
			return err
		}
		return nil
	})
}

// discoveryRunTimeout bounds one coalesced discovery run that owns its context.
const discoveryRunTimeout = 30 * time.Second

// maxCoalescedDiscoveryFollowUps bounds how many follow-up runs one owner
// performs back to back for triggers coalesced while it ran, so a server that
// signals tools/list_changed continuously cannot pin a discovery goroutine.
const maxCoalescedDiscoveryFollowUps = 4

// defaultCoalescedDiscoveryContinuationDelay is how long an owner that reached
// the follow-up limit waits before a fresh worker takes the still-pending
// trigger (Runtime.coalescedDiscoveryContinuationDelay overrides it in tests).
const defaultCoalescedDiscoveryContinuationDelay = 2 * time.Second

func (r *Runtime) coalescedContinuationDelay() time.Duration {
	if r.coalescedDiscoveryContinuationDelay > 0 {
		return r.coalescedDiscoveryContinuationDelay
	}
	return defaultCoalescedDiscoveryContinuationDelay
}

// runCoalescedDiscovery runs run under the per-server discovery marker. When
// a discovery for serverName is already in flight, the trigger is recorded as
// pending and this call returns immediately; the in-flight owner then re-runs
// once after it finishes (several coalesced triggers collapse into one
// follow-up), so a trigger that raced the owner's tools/list is never lost
// (Sol r3 P2). run is told whether it is such a follow-up (it must then not
// rely on a context the original trigger owned — Sol r4 P2).
//
// Ordering: a trigger records the pending flag BEFORE it tries to take the
// marker, and the owner checks the flag AFTER it releases the marker. Either
// the trigger saw the marker held — then its flag was stored before the
// owner's check — or it took the marker itself and runs.
//
// Bounded, never lossy: after maxCoalescedDiscoveryFollowUps follow-ups the
// owner returns, and a still-pending trigger is handed to a fresh worker after
// coalescedContinuationDelay instead of being left to a future
// connect or notification. Periodic discovery skips quarantined servers, so a
// burst that ends with an armed contract would otherwise stay unverified
// (Sol r4 P2). The continuation stops when the runtime shuts down.
func (r *Runtime) runCoalescedDiscovery(serverName string, run func(followUp bool) error) {
	r.discoveryRerunPending.Store(serverName, struct{}{})
	if _, loaded := r.discoveryInProgress.LoadOrStore(serverName, struct{}{}); loaded {
		r.logger.Debug("Tool discovery already in progress for server; coalescing this trigger into a follow-up run",
			zap.String("server", serverName))
		return
	}
	r.ownCoalescedDiscovery(serverName, run, false)
}

// ownCoalescedDiscovery is the owner loop of runCoalescedDiscovery; the caller
// holds the discovery marker. continued marks a deferred continuation, whose
// every run is a follow-up.
func (r *Runtime) ownCoalescedDiscovery(serverName string, run func(followUp bool) error, continued bool) {
	for followUps := 0; ; followUps++ {
		// Clear before running: a trigger recorded from here on is either
		// covered by this run's tools/list or re-arms the flag.
		r.discoveryRerunPending.Delete(serverName)
		_ = run(continued || followUps > 0)
		r.discoveryInProgress.Delete(serverName)
		if _, pending := r.discoveryRerunPending.Load(serverName); !pending {
			return
		}
		if r.lifetimeContext().Err() != nil {
			// The runtime shut down: no further follow-ups (Sol r5 P2).
			return
		}
		if followUps >= maxCoalescedDiscoveryFollowUps {
			r.logger.Warn("Tool discovery follow-up limit reached; deferring the coalesced trigger to a fresh worker",
				zap.String("server", serverName),
				zap.Duration("delay", r.coalescedContinuationDelay()))
			r.scheduleCoalescedDiscoveryContinuation(serverName, run)
			return
		}
		if _, loaded := r.discoveryInProgress.LoadOrStore(serverName, struct{}{}); loaded {
			// Another trigger took ownership; it runs for the pending flag.
			return
		}
		r.logger.Info("Re-running tool discovery for a trigger that arrived while it was in flight",
			zap.String("server", serverName))
	}
}

// scheduleCoalescedDiscoveryContinuation runs a pending coalesced trigger in a
// fresh, bounded worker after coalescedContinuationDelay. If another
// owner holds the marker by then, that owner covers the pending flag.
//
// It waits on the lifetime context, not AppContext: Close replaces the app
// context with context.Background(), so an owner that reaches the cap after
// shutdown started would otherwise schedule an uncancellable continuation
// (Sol r5 P2).
func (r *Runtime) scheduleCoalescedDiscoveryContinuation(serverName string, run func(followUp bool) error) {
	lifetime := r.lifetimeContext()
	if lifetime.Err() != nil {
		return
	}
	gate := r.coalescedDiscoveryContinuationGate
	go func() {
		if gate != nil {
			select {
			case <-lifetime.Done():
				return
			case <-gate:
			}
		} else {
			timer := time.NewTimer(r.coalescedContinuationDelay())
			defer timer.Stop()
			select {
			case <-lifetime.Done():
				return
			case <-timer.C:
			}
		}
		if lifetime.Err() != nil {
			return
		}
		if _, pending := r.discoveryRerunPending.Load(serverName); !pending {
			return
		}
		if _, loaded := r.discoveryInProgress.LoadOrStore(serverName, struct{}{}); loaded {
			return
		}
		r.ownCoalescedDiscovery(serverName, run, true)
	}()
}

// verifyQuarantinedServerContracts runs the tool-approval gate over a
// connected quarantined server's live tools/list WITHOUT indexing it (RT-05),
// when the server has an approved baseline to protect:
// approved contracts that changed are marked changed, new tools are filed
// pending, and nothing reaches the search index or routing surfaces. Unlike
// the explicit review refresh it never requests an inspection exemption or a
// connection — it only uses a connection the supervisor already keeps — and
// it is best effort: a disconnected client or a failed list is skipped (the
// next connect or tools/list_changed re-runs it).
//
// It reports superseded=true when the list was captured under a connection
// that was replaced before the gate ran. The caller's stale-generation retry
// must then re-verify under the replacement: that connection's own connect
// callback may have been dropped by the in-progress dedup, and periodic
// discovery skips quarantined servers, so nothing else would verify it
// (Sol r2 P2).
func (r *Runtime) verifyQuarantinedServerContracts(ctx context.Context, serverName string) (superseded bool) {
	if r.upstreamManager == nil || r.storageManager == nil || r.skipQuarantinedContractVerify {
		return false
	}
	// Only a server with an approved baseline has contracts that can go
	// stale (a re-quarantined, previously trusted server — EXP-2a). A server
	// that was never approved keeps its review uncaptured until an operator
	// or scan explicitly inspects it (UX-02 review snapshot semantics).
	if !r.serverHasApprovedBaseline(serverName) {
		return false
	}
	capture := r.discoveryGeneration(serverName)
	client, ok := r.upstreamManager.GetClient(serverName)
	if !ok || client == nil || !client.IsConnected() {
		return false
	}
	// The commit generation of the config this instance runs, taken before
	// the list (Spec 116 FR-008a): a capture from an instance a commit has
	// since superseded is dropped, not persisted.
	instanceGen := client.Generation()
	// A fresh list (Sol r5 P2): joining a tools/list already in flight
	// when tools/list_changed arrived could verify the contract as it was
	// BEFORE the change, and periodic discovery never re-verifies a
	// quarantined server.
	tools, inv, err := client.ListToolsFreshCaptured(ctx)
	if err != nil {
		r.logger.Debug("Quarantined contract verification: tools/list failed",
			zap.String("server", serverName), zap.Error(err))
		return false
	}
	if !r.quarantinedCaptureIsCurrent(serverName, client, capture) {
		return true
	}
	if len(tools) == 0 {
		return false
	}
	published, err := r.captureQuarantinedToolDefinitionsFromCurrentClient(serverName, client, capture, instanceGen, tools, inv)
	if err != nil {
		r.logger.Warn("Quarantined contract verification failed",
			zap.String("server", serverName), zap.Error(err))
		return false
	}
	if !published {
		return true
	}
	r.logger.Info("Verified quarantined server tool contracts against approvals (not indexed)",
		zap.String("server", serverName), zap.Int("tools", len(tools)))
	return false
}

// serverHasApprovedBaseline reports whether any of the server's tool-approval
// records is approved or changed (changed implies it was approved once).
func (r *Runtime) serverHasApprovedBaseline(serverName string) bool {
	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		return false
	}
	for _, rec := range records {
		if rec.Status == storage.ToolApprovalStatusApproved || rec.Status == storage.ToolApprovalStatusChanged {
			return true
		}
	}
	return false
}

// quarantinedCaptureIsCurrent confirms that the tools/list response belongs to
// the currently managed client and the same supervisor/managed-client
// generation captured before the request. Pointer equality closes the gap
// where a replacement could otherwise pair A's client with B's generation.
func (r *Runtime) quarantinedCaptureIsCurrent(serverName string, client interface{ ConnectionEpoch() int64 }, capture supervisor.DiscoveryCapture) bool {
	current, ok := r.upstreamManager.GetClient(serverName)
	return ok && current == client && client.ConnectionEpoch() == capture.Epoch && r.discoveryGeneration(serverName) == capture
}

// captureQuarantinedToolDefinitionsFromCurrentClient performs the final
// current-generation check immediately before checkToolApprovals writes review
// records. A false result is deliberately retried by the caller; no stale
// response is persisted or announced as review.changed.
func (r *Runtime) captureQuarantinedToolDefinitionsFromCurrentClient(serverName string, client *managed.Client, capture supervisor.DiscoveryCapture, instanceGen int64, tools []*config.ToolMetadata, inv managed.InventoryCapture) (bool, error) {
	if r.quarantinedCaptureBeforePersist != nil {
		r.quarantinedCaptureBeforePersist()
	}
	r.inventoryBeforeApply(serverName, inv)
	if r.upstreamManager == nil {
		return false, fmt.Errorf("upstream manager not initialized")
	}
	persisted := false
	current, err := r.upstreamManager.WithCurrentClientOnEpoch(serverName, client, capture.Epoch, func() error {
		// A delayed supervisor event is also a stale capture signal. The
		// manager/client locks below are the linearization point for actual
		// client replacement or reconnect; this check preserves the supervisor
		// generation contract before any review record is written.
		if r.discoveryGeneration(serverName) != capture {
			return nil
		}
		if r.quarantinedCaptureDuringPersist != nil {
			r.quarantinedCaptureDuringPersist()
		}
		if r.discoveryGeneration(serverName) != capture {
			return nil
		}
		ok, err := r.persistInventoryDefinitions(serverName, tools, instanceGen, inv)
		if err != nil {
			return err
		}
		persisted = ok
		return nil
	})
	if err != nil {
		return false, err
	}
	if !current || !persisted {
		return false, nil
	}
	r.emitReviewChanged(serverName)
	r.logger.Info("Captured quarantined server tool definitions for review",
		zap.String("server", serverName), zap.Int("count", len(tools)))
	return true, nil
}

// pruneAbsentUndecidedApprovals drops pending review records for tools
// the upstream no longer lists, so a quarantined capture reflects the live
// toolset rather than the union of every past capture. Operator decisions are
// kept: approved and changed (rug-pull) records and operator-blocked (Disabled) records survive.
func (r *Runtime) pruneAbsentUndecidedApprovals(serverName string, tools []*config.ToolMetadata) error {
	if r.storageManager == nil {
		return nil
	}
	live := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		live[config.RawToolName(tool)] = struct{}{}
	}
	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		return fmt.Errorf("%w: list review records of %s for prune: %v", errCaptureIncomplete, serverName, err)
	}
	undecidedAbsent := func(rec *storage.ToolApprovalRecord) bool {
		if _, ok := live[rec.ToolName]; ok {
			return false
		}
		return !rec.Disabled && rec.Status == storage.ToolApprovalStatusPending
	}
	var first error
	for _, rec := range records {
		if !undecidedAbsent(rec) {
			continue
		}
		// Eligibility is re-checked against the stored record inside the delete
		// transaction: a decision saved after the snapshot above (block, disable,
		// approve) must survive the prune.
		if hook := r.pruneAfterSnapshot; hook != nil {
			hook(rec.ToolName)
		}
		var derr error
		if f := r.testHooks.toolApprovalWriteFault; f != nil {
			derr = f(serverName, rec.ToolName, "delete-record")
		}
		if derr == nil {
			_, derr = r.storageManager.DeleteToolApprovalIf(serverName, rec.ToolName, undecidedAbsent)
		}
		if derr != nil {
			r.noteFailedTool(serverName, rec.ToolName)
			if first == nil {
				// Spec 117 FR-018: a review capture whose prune did not
				// complete is incomplete; the caller marks the server dirty.
				first = fmt.Errorf("%w: prune review record %s:%s: %v", errCaptureIncomplete, serverName, rec.ToolName, derr)
			}
		}
	}
	return first
}

// persistQuarantinedToolDefinitionsAt is persistQuarantinedToolDefinitions
// for tools that came from an instance at commit generation instanceGen (0 =
// unknown). persisted is false when the capture was dropped as stale.
func (r *Runtime) persistQuarantinedToolDefinitionsAt(serverName string, tools []*config.ToolMetadata, instanceGen int64) (persisted bool, err error) {
	return r.persistInventoryDefinitions(serverName, tools, instanceGen, managed.InventoryCapture{})
}

// persistInventoryDefinitions is persistQuarantinedToolDefinitionsAt for a
// captured inventory (Spec 117 PR-3): a capture older than one already
// applied for the server, or from a retired or reconnected client, persists
// nothing.
func (r *Runtime) persistInventoryDefinitions(serverName string, tools []*config.ToolMetadata, instanceGen int64, inv managed.InventoryCapture) (persisted bool, err error) {
	r.captureSnapMu.Lock()
	defer r.captureSnapMu.Unlock()
	return r.persistToolDefinitionsLocked(serverName, tools, instanceGen, inv)
}

// persistToolDefinitionsLocked records, prunes and publishes one review
// capture. Caller holds captureSnapMu for writing.
//
// Spec 116 FR-008a (review R2.2): the capture's final generation check and
// all of its writes (review records, prune, last-good snapshot, capture
// stamp) run in ONE hold of the server's approval lock, the lock a config
// commit writes the row under. A capture from an instance whose generation is
// no longer the row's (a tools/list that was in flight when a commit changed
// the endpoint and quarantined the server) writes nothing and reports
// persisted=false; the commit's after-unlock discovery request replaces it.
//
// Spec 117 PR-3: the capture is admitted in the same hold, after the
// generation check; a capture older than one already applied, or from a
// retired or reconnected client, writes nothing and reports persisted=false.
func (r *Runtime) persistToolDefinitionsLocked(serverName string, tools []*config.ToolMetadata, instanceGen int64, inv managed.InventoryCapture) (persisted bool, err error) {
	defer r.lockToolApprovals(serverName)()
	if !r.discoveryCaptureCurrentHeld(serverName, instanceGen) {
		r.logger.Info("Review capture dropped: the server's committed generation moved on",
			zap.String("server", serverName), zap.Int64("instance_generation", instanceGen))
		return false, nil
	}
	if r.admitInventoryHeld(serverName, inventoryRef{capture: inv}) != inventoryAdmitted {
		return false, nil
	}
	if _, err := r.checkToolApprovalsHeld(serverName, tools); err != nil {
		// Spec 117 FR-018: an incomplete review capture marks the server
		// review-dirty, so neither a server approval nor a tool approval can
		// act on records older than what the upstream now serves.
		r.markReviewDirtyHeld(serverName, inv.Seq, err)
		return false, fmt.Errorf("capture review definitions for %s: %w", serverName, err)
	}
	if err := r.pruneAbsentUndecidedApprovals(serverName, tools); err != nil {
		r.markReviewDirtyHeld(serverName, inv.Seq, err)
		return false, fmt.Errorf("capture review definitions for %s: %w", serverName, err)
	}
	if hook := r.captureAfterPrune; hook != nil {
		hook()
	}
	r.lastGoodToolsMu.Lock()
	r.storeLastGoodLocked(serverName, tools, inv, instanceGen)
	r.lastGoodToolsMu.Unlock()
	r.clearReviewDirtyHeld(serverName, inv.Seq)
	return true, nil
}

// noteCaptureLocked stamps a fresh capture for the review payload. Caller
// holds lastGoodToolsMu. The stamp is strictly increasing per server so a
// caller can tell a new capture from a retained one without comparing clocks.
func (r *Runtime) noteCaptureLocked(serverName string) {
	if r.lastCaptureAt == nil {
		r.lastCaptureAt = make(map[string]time.Time)
	}
	now := time.Now().UTC()
	if prev := r.lastCaptureAt[serverName]; !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	r.lastCaptureAt[serverName] = now
}

// commitDiscoveredTools publishes a discovery result and updates the stored
// approval records under captureSnapMu, so GetServerReview never observes the
// new capture stamp with the previous generation's records (or vice versa).
// For an eligible server the last-good snapshot and capture stamp are written,
// together with the differential index update, in ONE hold of the server's
// approval lock and only after its final eligibility and generation check
// (Spec 116 D15, FR-008a): a pass the check rejects writes nothing. For a
// server that is quarantined or disabled it records the definitions for review
// without indexing. eligible reports whether the index update was attempted.
func (r *Runtime) commitDiscoveredTools(ctx context.Context, serverName string, tools []*config.ToolMetadata, instanceGen int64) (eligible bool, err error) {
	eligible, _, err = r.commitDiscoveredToolsAt(ctx, serverName, tools, instanceGen, nil)
	return eligible, err
}

// commitDiscoveredToolsAt is commitDiscoveredTools that also publishes the
// result into the supervisor snapshot and StateView inside the same approval
// lock hold when capture (the DiscoveryGeneration taken before the list) is
// non-nil. published is false when that publish was dropped because the
// server's connection changed while the tools were listed, or when the D15
// check found the result came from a superseded instance generation; the
// caller re-lists in both cases. A pass rejected by the D15 check because the
// server was removed, disabled or quarantined returns eligible=false
// (terminal, no re-list).
func (r *Runtime) commitDiscoveredToolsAt(ctx context.Context, serverName string, tools []*config.ToolMetadata, instanceGen int64, capture *supervisor.DiscoveryCapture) (eligible, published bool, err error) {
	return r.commitInventoryAt(ctx, serverName, tools, instanceGen, capture, inventoryRef{})
}

// commitInventoryAt is commitDiscoveredToolsAt for the inventory inv (Spec
// 117 PR-3). The capture is admitted under the server's approval lock in the
// same hold as every write of the pass. A capture older than one already
// applied writes nothing and reports eligible and published (the newer
// capture stands; no re-list). A capture from a retired or reconnected client
// writes nothing and reports eligible but not published, so the caller
// re-lists the current client.
func (r *Runtime) commitInventoryAt(ctx context.Context, serverName string, tools []*config.ToolMetadata, instanceGen int64, capture *supervisor.DiscoveryCapture, inv inventoryRef) (eligible, published bool, err error) {
	r.inventoryBeforeApply(serverName, inv.capture)
	r.captureSnapMu.Lock()
	defer r.captureSnapMu.Unlock()
	if !r.serverEligibleForIndexing(serverName) {
		if _, perr := r.persistToolDefinitionsLocked(serverName, tools, instanceGen, inv.capture); perr != nil {
			r.logger.Warn("Failed to capture review definitions for ineligible server",
				zap.String("server", serverName), zap.Error(perr))
		}
		return false, false, nil
	}
	published = true
	// held runs after every write of the pass landed (FR-018): it stores the
	// last-good snapshot and capture stamp and publishes the StateView, and
	// reports a failed or rejected publication, which leaves the capture
	// incomplete. The snapshot and stamp are stored only after the
	// publication was accepted, so a rejected one leaves both at the prior
	// capture.
	held := func() error {
		if capture != nil && r.supervisor != nil {
			// The per-server variant stamps the server's discovery as
			// completed even when the result is EMPTY, so a tool-less
			// server is not left in the connect→discovery window (Spec 105
			// FR-009, research D4). The supervisor never calls back into
			// the runtime (plan.md lock rule 5), so this adds no edge.
			ok, rerr := r.publishDiscoveredTools(serverName, tools, *capture)
			if rerr != nil {
				r.logger.Warn("Failed to refresh tools in StateView for server",
					zap.String("server", serverName), zap.Error(rerr))
				published = false
				return fmt.Errorf("%w: publish tools of %s: %v", errCaptureIncomplete, serverName, rerr)
			}
			if !ok {
				published = false
				return errPublishRejected
			}
		}
		r.lastGoodToolsMu.Lock()
		r.storeLastGoodLocked(serverName, tools, inv.capture, instanceGen)
		r.lastGoodToolsMu.Unlock()
		if hook := r.discoveryAfterPublish; hook != nil {
			hook()
		}
		return nil
	}
	if err := r.applyInventoryGuarded(ctx, serverName, tools, instanceGen, inv, nil, held); err != nil {
		if errors.Is(err, errDiscoveryIneligible) {
			return false, false, nil
		}
		if errors.Is(err, errInventoryStale) {
			// A newer capture of this server was already applied: it
			// stands, and nothing of this one was written.
			return true, true, nil
		}
		if errors.Is(err, errDiscoveryStaleGeneration) || errors.Is(err, errInventoryNotCurrent) || errors.Is(err, errPublishRejected) {
			// Eligible but not published: the caller re-lists the
			// current instance (review round 7).
			return true, false, nil
		}
		if errors.Is(err, errBaselineMarker) {
			// Spec 117 FR-027a: aborted before held ran, so nothing of
			// this capture was published.
			return true, false, err
		}
		return true, false, err
	}
	return true, published, nil
}

// errPublishRejected reports a StateView publication the supervisor dropped
// because the server's connection changed while its tools were listed. The
// capture is incomplete (FR-018) and the caller re-lists the current
// connection, as before.
var errPublishRejected = errors.New("tool inventory publication rejected: the connection changed while it was listed")

// publishDiscoveredTools is the supervisor's per-server StateView publish,
// with the FR-018 test fault seam in front of it.
func (r *Runtime) publishDiscoveredTools(serverName string, tools []*config.ToolMetadata, capture supervisor.DiscoveryCapture) (bool, error) {
	if f := r.testHooks.discoveryPublishFault; f != nil {
		return f(serverName)
	}
	return r.supervisor.RefreshServerToolsFromDiscovery(serverName, tools, capture)
}

func (r *Runtime) discoverAndIndexToolsForServer(ctx context.Context, serverName string, authoritative bool) error {
	// A result captured under a connection that changed before it was
	// published is dropped by the supervisor (Spec 105 FR-009 "stale
	// generation"; astra r1 I2). Re-list under the current connection a
	// bounded number of times rather than leave the server in the
	// connect→discovery window until the next sweep — the reactive connect
	// discovery for the new connection may have been skipped by the
	// in-progress dedup while this one was still listing.
	const maxStaleRelists = 2
	for attempt := 0; ; attempt++ {
		published, err := r.discoverAndIndexToolsForServerOnce(ctx, serverName, authoritative)
		if err != nil || published || attempt >= maxStaleRelists {
			return err
		}
		r.logger.Info("Discovery result was captured under a superseded connection; re-listing the server",
			zap.String("server", serverName), zap.Int("attempt", attempt+1))
	}
}

// discoverAndIndexToolsForServerOnce is one list→index→publish attempt; it
// reports whether the result was published (false only when the server's
// connection generation moved on while it was captured).
func (r *Runtime) discoverAndIndexToolsForServerOnce(ctx context.Context, serverName string, authoritative bool) (published bool, err error) {
	if r.upstreamManager == nil || r.indexManager == nil {
		return false, fmt.Errorf("runtime managers not initialized")
	}

	// SECURITY-CRITICAL GUARD (issue #873): never (re)index a quarantined or
	// disabled server. applyDifferentialToolUpdate below does not itself withhold
	// a quarantined server's tools, so the upstream_servers "refresh" op and the
	// reactive discovery callbacks that reach this function must gate here — else
	// a quarantined server (kept connected for inspection) has its poisoned tool
	// descriptions surfaced into retrieve_tools. Unquarantine reindexes via the
	// full sweep (HandleUpstreamServerChange → DiscoverAndIndexTools), not this
	// single-server path, so the guard does not strand a newly-trusted server.
	if !r.serverEligibleForIndexing(serverName) {
		// RT-05 (red-team 2026-10-11): a quarantined server is kept connected
		// for inspection, and a restart re-quarantined by config used to leave
		// its stale APPROVED contracts unchecked for the whole quarantine
		// window while the (possibly armed) upstream ran. Hash-compare its
		// live contracts now, without indexing anything, so a rug pull is
		// recorded as changed before an unquarantine can serve it.
		if r.serverIsQuarantined(serverName) && r.verifyQuarantinedServerContracts(ctx, serverName) {
			// Superseded: report unpublished so the stale-generation
			// retry in discoverAndIndexToolsForServer re-verifies the
			// replacement connection.
			return false, nil
		}
		r.logger.Info("Skipping single-server tool discovery for ineligible server (disabled or quarantined)",
			zap.String("server", serverName))
		return true, nil
	}

	r.logger.Info("Discovering and indexing tools for server", zap.String("server", serverName))

	// Get the upstream client for this server
	client, ok := r.upstreamManager.GetClient(serverName)
	if !ok {
		return false, fmt.Errorf("client not found for server %s", serverName)
	}
	// The commit generation of the config this instance runs (Spec 116 D15):
	// the publish below is dropped when it no longer matches the stored row.
	instanceGen := client.Generation()

	// The connection generation and live connection token this result will
	// be published under: captured BEFORE the list so a reconnect during it
	// is detected at publish time (generation) or at identity-read time
	// (token, astra r2 C3).
	gen := r.discoveryGeneration(serverName)

	// Retry logic: Sometimes connection events fire slightly before the server is fully ready
	// We retry up to 3 times with exponential backoff (500ms, 1s, 2s)
	var tools []*config.ToolMetadata
	var inv managed.InventoryCapture
	maxRetries := 3
	baseDelay := 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(1<<uint(attempt-1)) // Exponential backoff
			r.logger.Debug("Retrying tool discovery after delay",
				zap.String("server", serverName),
				zap.Int("attempt", attempt+1),
				zap.Duration("delay", delay))

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return false, fmt.Errorf("context cancelled during retry backoff: %w", ctx.Err())
			}
		}

		// Discover tools from this server with a fresh list (Sol r6 P2):
		// this path serves connect and notifications/tools/list_changed, so
		// joining a tools/list that was already in flight when the trigger
		// fired (e.g. a tool-count refresh) could hash the contract as it was
		// BEFORE the change and keep a stale approval — which persists when
		// periodic discovery is disabled. ListToolsFresh waits for such a
		// request and lists again.
		tools, inv, err = client.ListToolsFreshCaptured(ctx)
		if err == nil {
			break // Success!
		}

		// Log the error for debugging
		r.logger.Warn("Tool discovery attempt failed",
			zap.String("server", serverName),
			zap.Int("attempt", attempt+1),
			zap.Int("max_retries", maxRetries),
			zap.Error(err))

		// Don't retry on context cancellation
		if ctx.Err() != nil {
			return false, fmt.Errorf("context cancelled during tool discovery: %w", ctx.Err())
		}
	}

	// After all retries, check if we still have an error
	if err != nil {
		return false, fmt.Errorf("failed to list tools for server %s after %d attempts: %w", serverName, maxRetries, err)
	}
	if h := r.testHooks.discoveryAfterList; h != nil {
		h(serverName)
	}

	if len(tools) == 0 {
		if !authoritative {
			// Lenient path (reactive discovery): a transient empty result must
			// not wipe a server's tools. Leave the index and last-good snapshot
			// untouched; the next sweep or a real change will reconcile. The
			// discovery itself did complete, though: stamp the server so a
			// genuinely tool-less one does not stay in the connect→discovery
			// window (Spec 105 FR-009). A server still holding a RETAINED set
			// (the previous connection's, restored on reconnect) is not
			// stamped — this connection listed none of it — and stays in the
			// window until a non-empty or authoritative pass (astra r1 I1).
			r.logger.Warn("No tools discovered from server; keeping existing index (lenient path)",
				zap.String("server", serverName))
			// A stale-generation empty result is re-listed like a
			// non-empty one (review round 7).
			return r.markServerZeroToolsDiscovered(serverName, gen, instanceGen, inv), nil
		}
		// Authoritative path (explicit refresh/discover, issue #873): zero tools
		// is the truth. Fall through with an empty toolset so the differential
		// update removes stale index entries and the snapshot below is cleared —
		// otherwise refresh would report success while stale tools stayed
		// searchable and a later approval reindex could resurface them.
		r.logger.Info("Refresh discovered zero tools; clearing server's index entries and snapshot",
			zap.String("server", serverName))
	}

	// Publish the last-good snapshot, capture stamp and approval records as
	// one generation (captureSnapMu), then apply the differential index
	// update. The snapshot gives the approval-driven reindex (issue #873) a
	// source without a network round-trip; on an authoritative empty refresh
	// it stores an empty slice so no stale set lingers.
	//
	// TOCTOU GUARD (issue #873): the eligibility check at the top of this
	// function ran BEFORE the seconds-wide ListTools above, so
	// commitDiscoveredTools re-checks eligibility under the lock. A server
	// quarantined in that window has its records captured for review (no index
	// write); a microsecond window remains before the index mutation, which is
	// deliberately not closed with a lock across the index write.
	eligible, published, err := r.commitInventoryAt(ctx, serverName, tools, instanceGen, &gen, inventoryRef{capture: inv})
	if !eligible {
		r.logger.Info("Server became ineligible during discovery (quarantined or disabled); skipping index write",
			zap.String("server", serverName))
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to apply differential tool update for server %s: %w", serverName, err)
	}

	// Invalidate tool count caches since tools may have changed
	r.upstreamManager.InvalidateAllToolCountCaches()

	// The StateView publish ran inside the commit above, bound to the
	// generation captured before the list; a dropped (stale) result is
	// reported to the caller, which re-lists.
	r.logger.Debug("Refreshed tools in StateView for server",
		zap.String("server", serverName),
		zap.Int("tool_count", len(tools)),
		zap.Bool("published", published))

	r.logger.Info("Successfully indexed tools for server",
		zap.String("server", serverName),
		zap.Int("count", len(tools)))
	return published, nil
}

// instanceGenerations captures the Spec 116 commit generation of every
// registered instance before a sweep lists their tools (D15).
func (r *Runtime) instanceGenerations() map[string]int64 {
	out := map[string]int64{}
	if r.upstreamManager == nil {
		return out
	}
	for _, name := range r.upstreamManager.GetAllServerNames() {
		if c, ok := r.upstreamManager.GetClient(name); ok && c != nil {
			out[name] = c.Generation()
		}
	}
	return out
}

// discoveryGenerations is supervisor.DiscoveryGenerations, nil-safe for
// fixtures without a supervisor.
func (r *Runtime) discoveryGenerations() map[string]supervisor.DiscoveryCapture {
	if r.supervisor == nil {
		return nil
	}
	return r.supervisor.DiscoveryGenerations()
}

// discoveryGeneration is supervisor.DiscoveryGeneration, nil-safe.
func (r *Runtime) discoveryGeneration(serverName string) supervisor.DiscoveryCapture {
	if r.supervisor == nil {
		return supervisor.DiscoveryCapture{}
	}
	return r.supervisor.DiscoveryGeneration(serverName)
}

// markZeroToolServersDiscovered stamps ToolsDiscovered on every server in
// listed that contributed no tools to toolsByServer — a completed tools/list
// that returned nothing — without touching its tool set (Spec 105 FR-009,
// research D4; supervisor.MarkServersToolsDiscovered, which also refuses to
// stamp a server still holding a previous connection's retained set). gens
// is the connection-generation capture and instanceGens the commit
// generations (Spec 116 D15) taken before the list.
//
// It returns the servers whose empty result came from a superseded instance
// generation, for the caller to re-list (review round 7).
func (r *Runtime) markZeroToolServersDiscovered(listed []string, toolsByServer map[string][]*config.ToolMetadata, gens map[string]supervisor.DiscoveryCapture, instanceGens map[string]int64, captures map[string]managed.InventoryCapture) []string {
	if r.supervisor == nil {
		return nil
	}
	var stale []string
	for _, serverName := range listed {
		if _, hasTools := toolsByServer[serverName]; hasTools {
			continue
		}
		if !r.markServerZeroToolsDiscoveredWith(serverName, gens, instanceGens[serverName], captures[serverName]) {
			stale = append(stale, serverName)
		}
	}
	return stale
}

// relistStaleSweepServers re-lists, in name order, every server whose sweep
// result was dropped because its connection or its instance generation
// changed while the sweep was listing.
func (r *Runtime) relistStaleSweepServers(ctx context.Context, stale []string) {
	sort.Strings(stale)
	for _, serverName := range stale {
		r.logger.Info("Sweep discovery result was captured under a superseded connection; re-listing the server",
			zap.String("server", serverName))
		if err := r.discoverAndIndexToolsForServer(ctx, serverName, false); err != nil {
			r.logger.Warn("Failed to re-list server after a stale sweep result",
				zap.String("server", serverName), zap.Error(err))
		}
	}
}

// markServerZeroToolsDiscovered stamps one server's completed, empty
// discovery. Spec 116 FR-008a (review R2.3): like every other discovery
// write, the stamp is made in one hold of the server's approval lock after
// the final eligibility and commit-generation check, so a stale empty
// tools/list (captured at G) cannot mark discovery complete after a
// connection-changing commit at G+1. The supervisor never calls back into the
// runtime, so stateMu under the approval lock adds no edge (the non-empty
// publish already nests it there).
//
// It reports false only when the result came from a superseded instance
// generation, so the caller re-lists the current instance (review round 7).
func (r *Runtime) markServerZeroToolsDiscovered(serverName string, gen supervisor.DiscoveryCapture, instanceGen int64, inv managed.InventoryCapture) bool {
	return r.markServerZeroToolsDiscoveredWith(serverName, map[string]supervisor.DiscoveryCapture{serverName: gen}, instanceGen, inv)
}

// markServerZeroToolsDiscoveredWith is markServerZeroToolsDiscovered with the
// sweep's capture map handed to the supervisor as is (a server missing from it
// is judged by the supervisor's own rules, as before).
//
// Spec 117 PR-3 (FR-033a): the empty result is admitted as an observation in
// the same hold. It advances the server's applied sequence, so an older
// non-empty capture still in flight writes nothing afterwards, and it keeps
// the retained inventory. An empty result older than one already applied is
// dropped (no re-list); one from a retired or reconnected client is re-listed.
func (r *Runtime) markServerZeroToolsDiscoveredWith(serverName string, gens map[string]supervisor.DiscoveryCapture, instanceGen int64, inv managed.InventoryCapture) bool {
	r.inventoryBeforeApply(serverName, inv)
	defer r.lockToolApprovals(serverName)()
	switch r.discoveryVerdictHeld(serverName, instanceGen) {
	case discoveryIneligible:
		r.logger.Info("Empty discovery result dropped: the server is no longer eligible at the captured generation",
			zap.String("server", serverName), zap.Int64("instance_generation", instanceGen))
		return true
	case discoveryStaleGen:
		r.logger.Info("Empty discovery result dropped: it came from a superseded instance generation",
			zap.String("server", serverName), zap.Int64("instance_generation", instanceGen))
		return false
	}
	switch r.admitInventoryHeld(serverName, inventoryRef{capture: inv, mode: inventoryObservation}) {
	case inventoryStale:
		return true
	case inventoryNotCurrent:
		return false
	}
	if r.supervisor == nil {
		return true
	}
	r.supervisor.MarkServersToolsDiscovered([]string{serverName}, gens)
	return true
}

// errDiscoveryIneligible reports a discovery result dropped by the final
// eligibility check (D15). It is not an error for the caller's caller.
var errDiscoveryIneligible = errors.New("server not eligible for indexing at the committed generation")

// errDiscoveryStaleGeneration reports a discovery result dropped by the final
// check because it came from an instance whose commit generation no longer
// matches the stored row (a connection-changing commit ran while it listed).
// Unlike errDiscoveryIneligible it is not terminal: the caller re-lists the
// current instance, whose own discovery may have been deduped while this
// pass was in flight (review round 7).
var errDiscoveryStaleGeneration = errors.New("discovery result came from a superseded instance generation")

// discoveryVerdict is the outcome of the final D15 check.
type discoveryVerdict int

const (
	discoveryCurrent    discoveryVerdict = iota // eligible at the instance's generation
	discoveryIneligible                         // removed, disabled or quarantined: terminal
	discoveryStaleGen                           // generation mismatch: re-list the current instance
)

// discoveryVerdictHeld is the final eligibility check of a discovery pass
// (Spec 116 D15), made while the caller holds serverName's approval lock: the
// server's stored row exists, is enabled and not quarantined, and — when both
// are known — carries the generation of the instance the tools came from. A
// config commit writes the row (and purges the index) under the same lock, so
// a pass either finished before the commit and was purged by it, or sees the
// new row here and writes nothing. A database no commit has written yet
// (unit-test fixtures) is not authoritative and is not consulted.
//
// A removed, disabled or quarantined row is terminal (discoveryIneligible),
// while a generation mismatch on an otherwise eligible row means a newer
// instance now serves the server and must be listed (discoveryStaleGen,
// review round 7).
func (r *Runtime) discoveryVerdictHeld(serverName string, instanceGen int64) discoveryVerdict {
	if r.storageManager == nil {
		return discoveryCurrent
	}
	if committed, err := r.storageManager.ConfigCommitted(); err != nil || !committed {
		if err == nil {
			return discoveryCurrent
		}
		return discoveryIneligible
	}
	row, err := r.storageManager.GetStoredServer(serverName)
	if err != nil || row == nil || row.Config == nil {
		return discoveryIneligible
	}
	if !row.Config.Enabled || row.Config.Quarantined {
		return discoveryIneligible
	}
	if row.Stamp.Gen != 0 && row.Stamp.Gen != instanceGen {
		return discoveryStaleGen
	}
	return discoveryCurrent
}

// discoveryCaptureCurrentHeld is the final check of a REVIEW capture (a
// discovery result for a server that is not eligible for indexing), made
// while the caller holds serverName's approval lock: the stored row exists
// and — when both are known — carries the generation of the instance the
// tools came from. Unlike discoveryVerdictHeld it accepts a quarantined or
// disabled row: recording definitions for review is the point. A database no
// commit has written yet is not authoritative and is not consulted.
func (r *Runtime) discoveryCaptureCurrentHeld(serverName string, instanceGen int64) bool {
	if r.storageManager == nil {
		return true
	}
	if committed, err := r.storageManager.ConfigCommitted(); err != nil || !committed {
		return err == nil
	}
	row, err := r.storageManager.GetStoredServer(serverName)
	if err != nil || row == nil || row.Config == nil {
		return false
	}
	if row.Stamp.Gen != 0 && row.Stamp.Gen != instanceGen {
		return false
	}
	return true
}

// applyDifferentialToolUpdateAt is applyDifferentialToolUpdate for tools that
// came from an instance at commit generation instanceGen (0 = unknown). Its
// final eligibility check, the approval decisions, every index mutation and
// the approval-record deletes run in ONE hold of the server's approval lock
// (Spec 116 D15, FR-008a); the per-profile rebuild and the events run after
// the lock is released.
func (r *Runtime) applyDifferentialToolUpdateAt(ctx context.Context, serverName string, newTools []*config.ToolMetadata, instanceGen int64) error {
	return r.applyDifferentialToolUpdateHeld(ctx, serverName, newTools, instanceGen, nil)
}

// applyDifferentialToolUpdateHeld is applyDifferentialToolUpdateAt with held,
// which runs inside the same approval-lock hold right after the final
// eligibility check passed and before any index write (the discovery pass's
// last-good and StateView publish, D15). It never runs for a rejected pass.
func (r *Runtime) applyDifferentialToolUpdateHeld(ctx context.Context, serverName string, newTools []*config.ToolMetadata, instanceGen int64, held func() error) error {
	return r.applyDifferentialToolUpdateGuarded(ctx, serverName, newTools, instanceGen, nil, held)
}

// errLocalSnapshotPurged reports a local reindex whose last-good snapshot was
// purged by a removal after it was selected (review round 3): the copy belongs
// to the removed server and must not reach a same-name replacement.
var errLocalSnapshotPurged = errors.New("last-good snapshot was purged by a removal after it was selected")

// applyDifferentialToolUpdateGuarded is applyDifferentialToolUpdateHeld with
// stale, checked under the approval lock right after the eligibility verdict
// and before held and any write: when it reports true the update writes
// nothing and returns errLocalSnapshotPurged. The local reindex paths pass a
// purge-sequence check, because their tools carry no instance generation.
func (r *Runtime) applyDifferentialToolUpdateGuarded(ctx context.Context, serverName string, newTools []*config.ToolMetadata, instanceGen int64, stale func() bool, held func() error) error {
	return r.applyInventoryGuarded(ctx, serverName, newTools, instanceGen, inventoryRef{}, stale, held)
}

// applyInventoryGuarded is applyDifferentialToolUpdateGuarded for the
// inventory inv (Spec 117 PR-3). Right after the D15 verdict and the purge
// check, still before held and any write, the capture is admitted in capture
// order (admitInventoryHeld): a refused capture writes nothing and returns
// errInventoryStale or errInventoryNotCurrent.
//
// Spec 117 FR-018 (#1610): once admitted, every write of the pass — approval
// records, the absent-tool removal (FR-032a), blocked eviction, the index
// writes — runs before held, which stores the last-good snapshot and capture
// stamp and publishes the StateView, and held reports whether that
// publication was accepted. Any failure on the way marks the server
// review-dirty at the capture's sequence and returns before held, so a failed
// capture publishes no snapshot, stamp or StateView entry. A fresh capture
// that ran through held clears an older dirty mark.
func (r *Runtime) applyInventoryGuarded(ctx context.Context, serverName string, newTools []*config.ToolMetadata, instanceGen int64, inv inventoryRef, stale func() bool, held func() error) error {
	unlock := r.lockToolApprovals(serverName)
	locked := true
	release := func() {
		if locked {
			locked = false
			unlock()
		}
	}
	defer release()

	switch r.discoveryVerdictHeld(serverName, instanceGen) {
	case discoveryIneligible:
		r.logger.Info("Discovery result dropped: the server is no longer eligible at the captured generation",
			zap.String("server", serverName), zap.Int64("instance_generation", instanceGen))
		return errDiscoveryIneligible
	case discoveryStaleGen:
		r.logger.Info("Discovery result dropped: it came from a superseded instance generation; the current instance is re-listed",
			zap.String("server", serverName), zap.Int64("instance_generation", instanceGen))
		return errDiscoveryStaleGeneration
	}
	if stale != nil && stale() {
		r.logger.Info("Local reindex dropped: the server was removed after its last-good snapshot was selected",
			zap.String("server", serverName))
		return errLocalSnapshotPurged
	}
	if err := inventoryVerdictErr(r.admitInventoryHeld(serverName, inv)); err != nil {
		return err
	}
	// FR-018: from here on a failure leaves the capture incomplete.
	incomplete := func(err error) error {
		r.markReviewDirtyHeld(serverName, inv.capture.Seq, err)
		return err
	}
	// complete publishes the capture (held) and, for a fresh capture, clears
	// an older dirty mark. A reconciliation re-applies retained definitions
	// and never clears it (it is not admitted while dirty, admitInventoryHeld).
	complete := func() error {
		if held != nil {
			if err := held(); err != nil {
				return incomplete(err)
			}
		}
		if inv.mode == inventoryDefinitions {
			r.clearReviewDirtyHeld(serverName, inv.capture.Seq)
		}
		return nil
	}
	// Spec 117 PR-1 (FR-027, FR-027a): the server's tool baseline is resolved
	// (and an older store's marker back-filled) BEFORE anything of this pass
	// is published or written. If it cannot be, the pass aborts here: no
	// last-good snapshot, capture stamp or StateView publish (held), no
	// approval record, no absent-tool or tool-hash delete, no index write.
	// The index and records still describe the last applied capture, so the
	// holds computed then stay in force; the next pass retries.
	baseline, err := r.resolveToolBaselineHeld(serverName)
	if err != nil {
		return incomplete(err)
	}

	// Check tool-level quarantine approvals before indexing. Any error
	// (errCaptureIncomplete: a record write failed; errBaselineMarker) leaves
	// the capture incomplete: nothing below runs and nothing is published.
	approvalResult, err := r.checkToolApprovalsResolved(serverName, newTools, baseline)
	if err != nil {
		return incomplete(err)
	}
	if h := r.testHooks.discoveryBeforeIndex; h != nil {
		h(serverName)
	}

	// Query existing tools from the index
	existingTools, err := r.indexedToolsOf(serverName)
	if err != nil {
		r.logger.Warn("Failed to query existing tools, performing full re-index",
			zap.String("server", serverName),
			zap.Error(err))
		// Filter out blocked tools before full batch index
		allowedTools := filterBlockedTools(newTools, approvalResult.BlockedTools)
		if berr := r.batchIndexTools(serverName, allowedTools); berr != nil {
			return incomplete(berr)
		}
		r.warmSignatureCache(allowedTools)
		// FR-018: without the indexed set this pass could neither remove
		// absent tools nor evict blocked ones, so it is not a complete
		// capture: nothing is published and the server stays (or becomes)
		// dirty until a pass that can diff the index runs.
		ierr := incomplete(fmt.Errorf("%w: query indexed tools of %s: %v", errCaptureIncomplete, serverName, err))
		release()
		// The shared index changed for this server; refresh dependent profiles.
		r.reindexAffectedProfiles(serverName)
		r.reconcileSignatureCache()
		return ierr
	}

	// Build maps for efficient lookup, keyed by the tool's RAW upstream name
	// (Spec 105 FR-009). The index hands back canonical "<server>:<raw>" ids and
	// discovery hands in raw names; config.RawToolName maps both onto the same
	// exact key, so "erase" and "ns:erase" stay two entries on both sides and a
	// rediscovery of an unchanged set diffs to nothing. The previous
	// first-colon strip collapsed "ns:erase" to "erase" here, which both lost
	// one of the two tools and — because the OLD key (read back from the
	// canonical name) never matched the NEW collapsed key — mis-reported
	// "ns:erase" as removed on every pass, deleting its exact-name approval
	// record (the operator's Disabled toggle) in step 1 below.
	oldToolsMap := make(map[string]*config.ToolMetadata)
	for _, tool := range existingTools {
		oldToolsMap[config.RawToolName(tool)] = tool
	}

	newToolsMap := make(map[string]*config.ToolMetadata)
	for _, tool := range newTools {
		newToolsMap[config.RawToolName(tool)] = tool
	}

	// Detect changes
	var addedTools []*config.ToolMetadata
	var modifiedTools []*config.ToolMetadata
	var removedTools []string
	// Review round 2, finding 4: annotations are deliberately excluded from
	// Hash (see calculateToolApprovalHash's comment — they are unstable
	// across reconnections, and folding them into change-detection caused
	// false "tool_description_changed" spam before). That means a tool whose
	// Hash never changes can never reach the modifiedTools branch, so
	// annotations_json — written to the index only at (re)index time — stays
	// stale forever for anything indexed before this field existed (or
	// before an upstream started reporting hints), short of an operator
	// deleting index.bleve or removing/re-adding the server. Tracked
	// separately from modifiedTools so it never touches Hash, approval state,
	// or the "Tool schema changed" log line — this is a silent, best-effort
	// metadata sync, not change detection.
	var annotationsOnlyTools []*config.ToolMetadata

	// Find added and modified tools
	for toolName, newTool := range newToolsMap {
		oldTool, exists := oldToolsMap[toolName]
		if !exists {
			// Tool is new
			addedTools = append(addedTools, newTool)
		} else if oldTool.Hash != newTool.Hash {
			// Tool exists but has changed (different hash)
			modifiedTools = append(modifiedTools, newTool)
		} else if !reflect.DeepEqual(oldTool.Annotations, newTool.Annotations) {
			annotationsOnlyTools = append(annotationsOnlyTools, newTool)
		}
		// else: tool unchanged, no action needed
	}

	// Find removed tools
	for toolName := range oldToolsMap {
		if _, exists := newToolsMap[toolName]; !exists {
			removedTools = append(removedTools, toolName)
		}
	}

	// Spec 105 FR-009 migration (astra r1 P2): an unmatched OLD key that is
	// the collapsed (after-first-colon) suffix of a raw name the server still
	// serves is not a removed tool — it is the pre-105 docID the old
	// derivation keyed the namespaced tool by ("erase" for a live "ns:erase"),
	// and its approval record is the server's pre-105 baseline evidence for
	// that tool. Its index document is still replaced below (the healed
	// a:ns:erase document takes over), but the record must survive: deleting
	// it made the NEXT discovery a trust-baseline pass when it was the
	// server's only approved/changed record (checkToolApprovals'
	// serverHasBaseline), which promoted the still-pending ns:erase — held
	// on this pass precisely because its contract did not match the
	// baseline — to approved with ApprovedBy "auto-baseline". A rug pull
	// across the upgrade thereby dispatched on the second discovery. An
	// unrestricting alias record is stamped inert for the legacy consults by
	// the pass that filed ns:erase, so retaining it changes nothing else; a
	// restricting one (user-disabled, pending, changed) keeps binding every
	// tool that collapses to it, exactly as pre-105 (stampConsultedLegacySibling).
	// A genuinely removed bare "erase" whose "ns:erase" sibling is still
	// served keeps an orphan record, consistent with #873's "the record
	// survives eviction".
	migrationAliasOfServed := make(map[string]bool)
	for rawName := range newToolsMap {
		if _, suffix, ok := strings.Cut(rawName, ":"); ok && suffix != "" {
			migrationAliasOfServed[suffix] = true
		}
	}

	// Spec 117 FR-032a: the removal workset also covers approved, enabled
	// records of tools this capture does not list, not only the index
	// documents the previous pass left. A pass whose index delete landed and
	// whose record delete failed leaves a record no index diff ever revisits
	// again; deriving it from storage also survives a restart.
	recordOnlyRemoved, err := r.absentApprovedRecords(serverName, newToolsMap, oldToolsMap, migrationAliasOfServed)
	if err != nil {
		return incomplete(err)
	}

	// Log the changes
	if len(addedTools) > 0 || len(modifiedTools) > 0 || len(removedTools) > 0 || len(recordOnlyRemoved) > 0 {
		r.logger.Info("Tool changes detected for server",
			zap.String("server", serverName),
			zap.Int("added", len(addedTools)),
			zap.Int("modified", len(modifiedTools)),
			zap.Int("removed", len(removedTools)),
			zap.Int("removed_records", len(recordOnlyRemoved)))
	} else {
		r.logger.Debug("No tool changes detected for server",
			zap.String("server", serverName),
			zap.Int("tool_count", len(newTools)))
	}

	// Apply changes

	// 1. Delete removed tools. Every delete is idempotent, so a retry after a
	// partial failure converges; any failure leaves the capture incomplete
	// (FR-018) instead of a logged and ignored error.
	var removalErr error
	noteRemoval := func(err error) {
		if err != nil && removalErr == nil {
			removalErr = err
		}
	}
	for _, toolName := range removedTools {
		r.logger.Info("Removing tool from index",
			zap.String("server", serverName),
			zap.String("tool", toolName))
		// The record is kept when the key is a pre-105 collapsed alias of a
		// namespaced tool still served (see migrationAliasOfServed above).
		keepRecord := migrationAliasOfServed[toolName]
		if keepRecord {
			r.logger.Info("Keeping approval record for a pre-105 collapsed docID whose namespaced tool is still served (Spec 105 FR-009)",
				zap.String("server", serverName),
				zap.String("legacy_key", toolName))
		}
		// Only an approved, enabled record is removed (FR-032a retention):
		// a pending, changed or user-disabled record whose stale index
		// document survived an earlier failed removal keeps the decision.
		noteRemoval(r.removeAbsentTool(serverName, toolName, !keepRecord, func(rec *storage.ToolApprovalRecord) bool {
			return absentApprovedRemovable(rec, newToolsMap)
		}))
	}
	for _, toolName := range recordOnlyRemoved {
		r.logger.Info("Removing approval record of a tool the server no longer lists",
			zap.String("server", serverName),
			zap.String("tool", toolName))
		noteRemoval(r.removeAbsentTool(serverName, toolName, true, func(rec *storage.ToolApprovalRecord) bool {
			return absentApprovedRemovable(rec, newToolsMap)
		}))
	}
	if removalErr != nil {
		return incomplete(fmt.Errorf("%w: remove absent tools of %s: %v", errCaptureIncomplete, serverName, removalErr))
	}

	// 2. Remove blocked tools from index if previously indexed
	for blockedToolName := range approvalResult.BlockedTools {
		if _, wasIndexed := oldToolsMap[blockedToolName]; wasIndexed {
			r.logger.Info("Removing blocked tool from index (quarantine)",
				zap.String("server", serverName),
				zap.String("tool", blockedToolName))
			if err := r.indexManager.DeleteTool(serverName, blockedToolName); err != nil {
				r.logger.Error("Failed to remove blocked tool from index",
					zap.String("server", serverName),
					zap.String("tool", blockedToolName),
					zap.Error(err))
				return incomplete(fmt.Errorf("%w: evict blocked tool %s:%s: %v", errCaptureIncomplete, serverName, blockedToolName, err))
			}
		}
	}

	// 3. Index added tools (excluding blocked)
	allowedAddedTools := filterBlockedTools(addedTools, approvalResult.BlockedTools)
	if len(allowedAddedTools) > 0 {
		r.logger.Info("Indexing new tools",
			zap.String("server", serverName),
			zap.Int("count", len(allowedAddedTools)),
			zap.Int("blocked", len(addedTools)-len(allowedAddedTools)))

		if err := r.batchIndexTools(serverName, allowedAddedTools); err != nil {
			return incomplete(fmt.Errorf("failed to index added tools: %w", err))
		}
	}

	// 4. Re-index modified tools (excluding blocked)
	allowedModifiedTools := filterBlockedTools(modifiedTools, approvalResult.BlockedTools)
	if len(allowedModifiedTools) > 0 {
		r.logger.Info("Re-indexing modified tools",
			zap.String("server", serverName),
			zap.Int("count", len(allowedModifiedTools)),
			zap.Int("blocked", len(modifiedTools)-len(allowedModifiedTools)))

		for _, tool := range allowedModifiedTools {
			r.logger.Debug("Tool schema changed",
				zap.String("server", serverName),
				zap.String("tool", tool.Name),
				zap.String("old_hash", oldToolsMap[config.RawToolName(tool)].Hash),
				zap.String("new_hash", tool.Hash))
		}

		if err := r.batchIndexTools(serverName, allowedModifiedTools); err != nil {
			return incomplete(fmt.Errorf("failed to re-index modified tools: %w", err))
		}
	}

	// 4b. Silently backfill/refresh annotations_json for otherwise-unchanged
	// tools (review round 2, finding 4). Deliberately not logged at Info and
	// not described as a "change" — see the comment on annotationsOnlyTools
	// above — this is metadata hygiene, not a tool-contract update.
	allowedAnnotationsOnlyTools := filterBlockedTools(annotationsOnlyTools, approvalResult.BlockedTools)
	if len(allowedAnnotationsOnlyTools) > 0 {
		r.logger.Debug("Refreshing stored annotations for unchanged tools",
			zap.String("server", serverName),
			zap.Int("count", len(allowedAnnotationsOnlyTools)))

		if err := r.batchIndexTools(serverName, allowedAnnotationsOnlyTools); err != nil {
			return incomplete(fmt.Errorf("failed to refresh tool annotations: %w", err))
		}
	}

	// 5. Warm the signature cache for every tool this server still serves —
	// deliberately the WHOLE allowed set, not just what steps 3 and 4 touched.
	//
	// The narrow form (warming only added/modified tools) left the cache
	// permanently empty on any restart against an existing index: the
	// differential update finds nothing to do, so neither branch ran and nothing
	// was ever warmed. Spec 085 did not notice, because its compact
	// retrieve_tools reads through the COMPILING accessor and merely paid a
	// first-call compile. Spec 102's deferred direct listing reads through Peek,
	// which never compiles — so every entry silently lost its compact signature
	// after a restart while the listing still looked well-formed. Found by live
	// verification, not by a unit test; see
	// TestApplyDifferentialToolUpdate_WarmsUnchangedToolsOnRestart.
	//
	// Idempotent and cheap: Warm returns on the first cache hit, so the steady
	// state is one map lookup per tool per discovery.
	r.warmSignatureCache(filterBlockedTools(newTools, approvalResult.BlockedTools))

	// 6. Publish: last-good snapshot, capture stamp and StateView (held),
	// only now that every write of the pass landed (FR-018).
	if err := complete(); err != nil {
		return err
	}

	// Index and approval writes are done; the per-profile rebuild and the
	// events below must not run under the approval lock (D15).
	release()

	// If the shared index changed for this server, refresh the per-profile indexes
	// that include it (Profiles v2, Spec 057). Profiles without this server are
	// untouched. Skipped when nothing changed to avoid churn on idle sweeps.
	changed := len(addedTools) > 0 || len(modifiedTools) > 0 || len(removedTools) > 0 ||
		len(annotationsOnlyTools) > 0 || len(approvalResult.BlockedTools) > 0
	if changed {
		r.reindexAffectedProfiles(serverName)
		// Evict signature-cache entries orphaned by removed/redefined tools —
		// warming above only ever ADDS entries.
		r.reconcileSignatureCache()

		// Tell the routing-mode surfaces that a tool DEFINITION moved, not just
		// that the server list did. Two live-verified bugs share this gap:
		//
		//   1. On a first-ever start the direct surface is rebuilt when the
		//      server connects, which is BEFORE indexing warms the signature
		//      cache — so every deferred entry rendered with a Peek miss and
		//      shipped with no compact signature at all, permanently. An agent
		//      then had the schema taken away and got nothing in exchange. It
		//      healed only on the next unrelated servers.changed, and a restart
		//      hid it entirely because the cache was already warm.
		//   2. After a rug-pull the catalog kept the OLD schema forever, so
		//      pre-dispatch validation rejected correct arguments and
		//      describe_tool handed back the same stale schema — turning the
		//      self-healing path into the unbounded loop it exists to prevent.
		//
		// Emitting here closes both: the direct rebuild re-renders from the
		// freshly indexed definitions with a warm cache. It is guarded by
		// `changed`, so an idle discovery sweep that found nothing new still
		// emits nothing.
		r.emitServersChanged("tools_changed", map[string]any{
			"server":   serverName,
			"added":    len(addedTools),
			"modified": len(modifiedTools),
			"removed":  len(removedTools) + len(recordOnlyRemoved),
		})
	}

	return nil
}

// warmSignatureCache pre-compiles compact signatures for freshly indexed
// tools (Spec 085 US1 T024, FR-008: signatures are compiled at index time
// into the ONE Runtime-owned cache, keyed by the Spec-032 tool hash), so a
// later compact retrieve_tools is a pure cache read — never a per-request
// compile. Hashless tools are skipped: warming them would memoize distinct
// schemas under one "" key (indexed tools always carry a hash).
func (r *Runtime) warmSignatureCache(tools []*config.ToolMetadata) {
	if r.sigCache == nil {
		return
	}
	for _, tool := range tools {
		if tool == nil || tool.Hash == "" {
			continue
		}
		r.sigCache.Warm(tool.Hash, tool.ParamsJSON, tool.Description)
	}
}

// reconcileSignatureCache evicts signature-cache entries whose hash no longer
// backs any indexed tool (Spec 085 FR-008 hygiene). warmSignatureCache only
// ever ADDS entries, so after tool removals/redefinitions the dead hashes
// would otherwise accumulate for the life of the process. Called after index
// rebuilds and differential updates; enumerates the SHARED index only —
// per-profile indexes hold subsets of the same tools, hence the same hashes.
func (r *Runtime) reconcileSignatureCache() {
	if r.sigCache == nil || r.indexManager == nil {
		return
	}
	serverNames, err := r.indexManager.GetAllIndexedServerNames()
	if err != nil {
		r.logger.Debug("signature cache reconcile skipped: cannot enumerate indexed servers", zap.Error(err))
		return
	}
	live := make(map[string]struct{})
	for _, serverName := range serverNames {
		tools, err := r.indexManager.GetToolsByServer(serverName)
		if err != nil {
			// Fail open (skip the sweep) rather than evict entries we could not
			// enumerate: a lingering stale entry is harmless memory, an evicted
			// live one costs a recompile on the next compact retrieve.
			r.logger.Debug("signature cache reconcile skipped: cannot enumerate server tools",
				zap.String("server", serverName), zap.Error(err))
			return
		}
		for _, tool := range tools {
			if tool != nil && tool.Hash != "" {
				live[tool.Hash] = struct{}{}
			}
		}
	}
	if evicted := r.sigCache.RetainHashes(live); evicted > 0 {
		r.logger.Debug("evicted stale signature cache entries",
			zap.Int("evicted", evicted),
			zap.Int("live", len(live)))
	}
}

// filterBlockedTools removes tools that are blocked by quarantine from the list.
func filterBlockedTools(tools []*config.ToolMetadata, blocked map[string]bool) []*config.ToolMetadata {
	if len(blocked) == 0 {
		return tools
	}
	var allowed []*config.ToolMetadata
	for _, tool := range tools {
		// BlockedTools is keyed by raw name (checkToolApprovals), so the
		// lookup must use the same exact identity (Spec 105 FR-009).
		if !blocked[config.RawToolName(tool)] {
			allowed = append(allowed, tool)
		}
	}
	return allowed
}

// boolPtrEqual reports whether two tri-state *bool overrides carry the same
// value, treating two nils as equal.
func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// bootReconcile is the boot commit: ReconcileLive plus Bootstrap (Spec 116
// D13, D14, review R9.6). The configuration file is the source of truth; rows
// that match it keep their stored generations, every enabled server gets a
// connect worker, and the file is not rewritten.
func (r *Runtime) bootReconcile() error {
	if r.configSvc == nil || r.storageManager == nil || r.upstreamManager == nil || r.indexManager == nil {
		return fmt.Errorf("runtime managers not initialized")
	}
	r.logger.Info("Synchronizing servers from configuration (config as source of truth)")
	_, err := r.Commit(commitReasonBoot, func(tx *CommitTx) error {
		tx.ReconcileLive()
		tx.Bootstrap()
		tx.noDisk()
		return nil
	})
	return err
}

// ReconcileServers reconciles storage and the upstream manager with the live
// configuration (projection repair, Spec 116 ReconcileLive). It never replaces
// the configuration and never writes the config file.
func (r *Runtime) ReconcileServers() error {
	_, err := r.Commit("reconcile", func(tx *CommitTx) error {
		tx.ReconcileLive()
		tx.noDisk()
		return nil
	})
	return err
}

// CommitConfig makes cfg the live configuration through one config commit and
// returns its error. cfg must be a configuration the caller owns and submits
// now (D14); see UpdateConfig.
func (r *Runtime) CommitConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config cannot be nil")
	}
	_, err := r.Commit("runtime_update", func(tx *CommitTx) error {
		tx.ReplaceConfig(cfg)
		return nil
	})
	return err
}

// SaveConfiguration persists the live configuration to disk.
//
// Spec 116 (FR-004): it no longer rebuilds the server list from storage —
// storage is a projection of the live configuration written by every commit,
// so there is nothing to splice back and nothing a pending removal could be
// resurrected from (#1559). It is a commit with ForceDisk: the file is written
// even when nothing changed, pending-aware as before (a restart-gated value
// saved but not yet adopted stays on disk).
func (r *Runtime) SaveConfiguration() error {
	snapshot := r.ConfigSnapshot()
	if snapshot == nil || snapshot.Config == nil {
		return fmt.Errorf("runtime configuration is not available")
	}
	if snapshot.Path == "" {
		r.logger.Warn("Configuration file path is not available, cannot save configuration")
		return fmt.Errorf("configuration file path is not available")
	}
	_, err := r.Commit("save_configuration", func(tx *CommitTx) error {
		tx.ForceDisk()
		return nil
	})
	if err != nil {
		r.logger.Error("Failed to save configuration", zap.Error(err))
	}
	return err
}

// ReloadConfiguration reloads the configuration from disk and resyncs state.
//
// Spec 116: one commit. The file is read inside the commit lock and published
// once, pinned (the raw file is never published unpinned); storage and the
// manager are reconciled against it before the snapshot is published.
func (r *Runtime) ReloadConfiguration() error {
	r.logger.Info("Reloading configuration from disk")

	var (
		oldCfg  *config.Config
		fileCfg *config.Config
		running *config.Config
		path    string
	)
	res, err := r.Commit(commitReasonFileReload, func(tx *CommitTx) error {
		oldCfg = tx.Base()
		path = r.configSvc.Current().Path
		if path == "" {
			return fmt.Errorf("failed to reload config: no configuration file path set")
		}
		loaded, loadErr := config.LoadFromFile(path)
		if loadErr != nil {
			return fmt.Errorf("failed to reload config: failed to load config from %s: %w", path, loadErr)
		}
		// Spec 107 FR-035: a hot reload re-runs the loader, which records (but
		// cannot log) the removed-key / deprecated-key findings; emit them here,
		// once per successful reload, with the logger the reload path has.
		config.LogLoadDiagnostics(loaded, r.logger)
		fileCfg = loaded

		// A hand-edited restart-gated field cannot be adopted any more than an
		// API-applied one can: /mcp stays bound to the mode it registered at
		// startup, the listener stays bound, the DB stays open. Pin them to the
		// live values. The loader re-applied the MCPPROXY_* env overrides but
		// knows nothing about the serve flags; the RUNNING config is the file
		// plus both, so a hand edit of an unrelated key must not switch
		// `--read-only` off. Applied to the pinned copy only (nested blocks
		// copy-on-write): the desired config stays the file.
		r.mu.RLock()
		live := r.cfg
		r.mu.RUnlock()
		pinned := pinRestartGated(live, loaded)
		config.ReapplyFlagOverrides(pinned, live)
		running = pinned

		tx.ReplaceConfig(pinned)
		// The file is the source here: the reload never writes it back.
		tx.noDisk()
		// The file IS the desired configuration, so a disk reload resets it —
		// including over an API change that was still waiting for a restart:
		// whoever edited the file wins. The hot serve flags ride along exactly
		// as they do in the startup desired config. Restart-gated fields stay
		// the file's, so a pending edit of listen is still reported as pending.
		tx.setDesired(pinRestartGated(loaded, pinned))
		// Parity with ApplyConfig's live per-component side effects (PR #857
		// review): logging, the tool-response truncator and the observability
		// cadence must follow disk reloads too.
		tx.publishHook(func(o, n *config.Config) {
			r.applyComponentConfigLocked(o, n)
			// Spec 079 FR-012: re-gate the update checker on the disk-reload
			// path too (ApplyConfig covers the API path). Applied here, under
			// the commit lock, so checker state follows commit order: run
			// after the commit, a reload paused past publication could apply
			// its older update_check over a newer commit's (review round 8).
			r.applyUpdateCheckConfig(n)
		})
		return nil
	})
	if err != nil {
		return err
	}

	// GH #965 review: an external file edit is applied silently even when it
	// touches a restart-required field; at least make it loud in the log.
	if oldCfg != nil && fileCfg != nil {
		if result := DetectConfigChanges(oldCfg, fileCfg); result.RequiresRestart {
			r.logger.Warn("Config file change includes restart-required fields; the running server keeps the old values until restart",
				zap.Strings("changed_fields", result.ChangedFields),
				zap.String("reason", result.RestartReason))
		}
	}

	// Issue #1458: a hand edit of profiles / anonymous_profile must reach the
	// per-profile search indexes now, not after postConfigReload's reconnect and
	// discovery pass.
	if oldCfg != nil && running != nil &&
		profileIndexInputsChanged(DetectConfigChanges(oldCfg, running).ChangedFields) {
		r.reconcileProfileIndexes()
	}

	// MCP-2482 (telemetry opt-out beacon) is covered by the commit's telemetry
	// notification; the update checker is re-gated in the publish hook above.

	go r.postConfigReload()

	newCount := 0
	var version int64
	if res != nil && res.Snapshot != nil {
		newCount = res.Snapshot.ServerCount()
		version = res.Snapshot.Version
	}
	oldCount := 0
	if oldCfg != nil {
		oldCount = len(oldCfg.Servers)
	}
	r.logger.Info("Configuration reload completed",
		zap.String("path", path),
		zap.Int64("version", version),
		zap.Int("old_server_count", oldCount),
		zap.Int("new_server_count", newCount),
		zap.Int("server_delta", newCount-oldCount))

	r.emitConfigReloaded(path)

	return nil
}

func (r *Runtime) postConfigReload() {
	ctx := r.AppContext()
	if ctx == nil {
		r.logger.Error("Application context is nil, cannot trigger reconnection")
		return
	}

	r.logger.Info("Triggering immediate reconnection after config reload")

	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := r.upstreamManager.ConnectAll(connectCtx); err != nil {
		r.logger.Warn("Some servers failed to reconnect after config reload", zap.Error(err))
	}

	select {
	case <-time.After(2 * time.Second):
		if err := r.DiscoverAndIndexTools(ctx); err != nil {
			r.logger.Error("Failed to re-index tools after config reload", zap.Error(err))
		}
	case <-ctx.Done():
		r.logger.Info("Tool re-indexing cancelled during config reload")
	}
}

// EnableServer enables or disables a server and persists the change.
//
// Spec 116: one commit. The flag reaches the live config, storage and the file
// together, the commit purges a disabled server's tools from the index, and
// the connect/disconnect runs after the commit lock is released.
func (r *Runtime) EnableServer(serverName string, enabled bool) error {
	r.logger.Info("Request to change server enabled state",
		zap.String("server", serverName),
		zap.Bool("enabled", enabled))

	if _, err := r.Commit("enable_toggle", func(tx *CommitTx) error {
		// The commit sends the one servers.changed of this toggle (FR-028).
		tx.EventExtra(map[string]any{"server": serverName, "enabled": enabled})
		return tx.SetEnabled(serverName, enabled)
	}); err != nil {
		r.logger.Error("Failed to update server enabled state", zap.Error(err))
		return fmt.Errorf("failed to update server '%s': %w", serverName, err)
	}

	// Emit config change activity for audit trail (Spec 024)
	action := "server_disabled"
	if enabled {
		action = "server_enabled"
	}
	r.EmitActivityConfigChange(action, serverName, "api", []string{"enabled"}, map[string]interface{}{"enabled": !enabled}, map[string]interface{}{"enabled": enabled})

	// Reindexing and reconnect checks are slow; run them off the caller's
	// goroutine so the HTTP handler returns right after the commit.
	go r.HandleUpstreamServerChange(r.AppContext())

	return nil
}

// QuarantineServer updates the quarantine state and persists the change.
// Security: When quarantining a server, all its tools are removed from the index
// to prevent Tool Poisoning Attacks (TPA) from exposing potentially malicious tool descriptions.
//
// Spec 116: one commit. The flag is stamped as an explicit decision (#937),
// written to storage under the server's approval lock in the same step that
// purges the index (FR-008), and published before this returns.
func (r *Runtime) QuarantineServer(serverName string, quarantined bool) error {
	return r.QuarantineServerAt(serverName, quarantined, 0)
}

// QuarantineServerAt is QuarantineServer bound to expectedIncarnation (Spec
// 116 FR-015, FR-017): when it is > 0 the commit fails with ErrServerReplaced
// (and changes nothing) unless the server still has that incarnation — the
// scanner approval and an unquarantine the operator reviewed in the Web UI use
// it, so a remove and re-add in between can never release the replacement.
func (r *Runtime) QuarantineServerAt(serverName string, quarantined bool, expectedIncarnation int64) error {
	r.logger.Info("Request to change server quarantine state",
		zap.String("server", serverName),
		zap.Bool("quarantined", quarantined))

	// An unquarantine without an expected incarnation is bound to the one
	// current when it arrives (FR-017, review round 4), so a remove and
	// re-add that lands before its commit fails it with ErrServerReplaced
	// instead of releasing the replacement. Quarantining stays unbound: it
	// never releases anything.
	if !quarantined && expectedIncarnation <= 0 {
		inc, err := r.CaptureApprovalIncarnation(serverName, 0)
		if err != nil {
			return fmt.Errorf("failed to update quarantine state for server '%s': %w", serverName, err)
		}
		expectedIncarnation = inc
	}

	var baseInc int64
	res, err := r.Commit("quarantine_toggle", func(tx *CommitTx) error {
		if g, ok := tx.Generation(serverName); ok {
			baseInc = g.Incarnation
		}
		if expectedIncarnation > 0 {
			if err := tx.ExpectIncarnation(serverName, expectedIncarnation); err != nil {
				return err
			}
		}
		// The commit sends the one servers.changed and review.changed of this
		// transition (FR-028); no caller-side copy follows.
		tx.EventExtra(map[string]any{"server": serverName, "quarantined": quarantined})
		return tx.SetQuarantined(serverName, quarantined)
	})
	if err != nil {
		r.logger.Error("Failed to update server quarantine state", zap.Error(err))
		return fmt.Errorf("failed to update quarantine state for server '%s': %w", serverName, err)
	}

	// On unquarantine/approval, baseline-trust the server's CURRENT tool
	// snapshot: promote its pending (never-reviewed) tool records to approved.
	// Tool-level quarantine then guards only status=changed (rug-pull) records.
	// (Spec 032, MCP-2100; trust model confirmed in MCP-2081.) Done before the
	// re-index in HandleUpstreamServerChange so the newly-trusted tools become
	// immediately searchable. Best-effort: a promotion failure must not abort
	// the unquarantine the user already requested and that is already persisted.
	//
	// The follow-up is bound to the incarnation of the commit that
	// unquarantined, taken from its result and never recaptured by name
	// (FR-014i, review R9.5): under the approval lock it promotes nothing
	// when the row no longer has that incarnation (a remove and re-add ran
	// after this commit) or is quarantined again.
	if !quarantined {
		var inc int64
		if res != nil && res.Snapshot != nil {
			if g, ok := res.Snapshot.ServerGeneration(serverName); ok {
				inc = g.Incarnation
			}
		}
		if inc == 0 {
			// The commit published nothing (the server was already
			// unquarantined): bind to the incarnation the commit read
			// under its lock.
			inc = baseInc
		}
		if h := r.testHooks.approvalAfterUnquarantineCommit; h != nil {
			h(serverName, inc)
		}
		if err := r.approveBaselineToolsForServerAt(serverName, inc); err != nil && !errors.Is(err, ErrServerReplaced) {
			r.logger.Warn("Failed to baseline-approve tools on server unquarantine",
				zap.String("server", serverName),
				zap.Error(err))
		}
	}

	// Emit activity event for quarantine state change
	reason := "Server unquarantined by administrator"
	if quarantined {
		reason = "Server quarantined for security review"
	}
	r.EmitActivityQuarantineChange(serverName, quarantined, reason)

	r.HandleUpstreamServerChange(r.AppContext())

	r.logger.Info("Successfully persisted server quarantine state change",
		zap.String("server", serverName),
		zap.Bool("quarantined", quarantined))

	return nil
}

// BulkEnableServers toggles the enabled state for multiple servers in one
// config commit (one storage transaction, one file write). Returns a map of
// per-server errors for operations that could not be applied.
func (r *Runtime) BulkEnableServers(serverNames []string, enabled bool) (map[string]error, error) {
	resultErrs := make(map[string]error)
	if len(serverNames) == 0 {
		return resultErrs, nil
	}

	var changed []string
	_, err := r.Commit("bulk_enable_toggle", func(tx *CommitTx) error {
		for _, name := range serverNames {
			cur, ok := tx.Server(name)
			if !ok {
				resultErrs[name] = fmt.Errorf("server '%s' not found", name)
				continue
			}
			if cur.Enabled == enabled {
				r.logger.Debug("Skipping server already in desired enabled state",
					zap.String("server", name),
					zap.Bool("enabled", enabled))
				continue
			}
			if err := tx.SetEnabled(name, enabled); err != nil {
				resultErrs[name] = err
				continue
			}
			changed = append(changed, name)
		}
		// The commit sends the one servers.changed of this bulk toggle.
		tx.EventExtra(map[string]any{"enabled": enabled, "count": len(changed)})
		return nil
	})
	if err != nil {
		return resultErrs, fmt.Errorf("failed to save configuration: %w", err)
	}

	// Nothing changed; return collected errors (if any)
	if len(changed) == 0 {
		return resultErrs, nil
	}

	r.HandleUpstreamServerChange(r.AppContext())

	return resultErrs, nil
}

// refreshServerFromDiskForRestart is RestartServer's disk refresh (Spec 116
// FR-003): the named server's entry is re-read from mcp_config.json and
// committed, so a fast edit-then-restart cannot race the watcher's debounce
// (issue #467) and the edit reaches the live config, storage and the manager
// together. The disk entry is admission-gated (#937): a server whose file
// entry never stated `quarantined` keeps its recorded quarantine. When the file
// is unreadable or does not list the server, the live entry stands. The file
// itself is not rewritten.
func (r *Runtime) refreshServerFromDiskForRestart(serverName string) (*config.ServerConfig, error) {
	_, err := r.Commit("restart_refresh", func(tx *CommitTx) error {
		// The file is the source here and is never written back (as on
		// main): rewriting it from the live config would overwrite an
		// unreloaded external edit of another server or of a global field,
		// and its self-write marker would stop the watcher from applying it
		// (review round 1).
		tx.noDisk()
		path := r.configSvc.Current().Path
		var disk *config.ServerConfig
		var diskCfg *config.Config
		if path != "" {
			loaded, err := config.LoadFromFile(path)
			if err != nil {
				r.logger.Warn("Failed to re-read config from disk during restart, using the live configuration",
					zap.String("path", path),
					zap.String("server", serverName),
					zap.Error(err))
			} else {
				diskCfg = loaded
				for _, srv := range loaded.Servers {
					if srv != nil && srv.Name == serverName {
						disk = srv
						break
					}
				}
			}
		}
		if disk != nil {
			stored, serr := tx.loadStored()
			storedCfg := make(map[string]*config.ServerConfig, len(stored))
			for name, s := range stored {
				storedCfg[name] = s.Config
			}
			gated, _ := r.admitServerForRestart(diskCfg, disk, storedCfg, serr == nil && r.storageManager != nil)
			if _, live := tx.Server(serverName); live {
				return tx.UpdateServer(serverName, func(cur *config.ServerConfig) error {
					*cur = *config.CopyServerConfig(gated)
					return nil
				})
			}
			return tx.CreateServer(gated)
		}
		if _, ok := tx.Server(serverName); !ok {
			return &ServerNotFoundError{Name: serverName}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, sc := range r.Config().Servers {
		if sc != nil && sc.Name == serverName {
			return sc, nil
		}
	}
	return nil, nil
}

// RestartServer restarts an upstream server by disconnecting and reconnecting it.
// Validation and disconnect are synchronous; reconnection and reindexing happen
// asynchronously so the caller (HTTP handler) returns immediately.
func (r *Runtime) RestartServer(serverName string) error {
	r.logger.Info("Request to restart server", zap.String("server", serverName))

	// Issue #467: pull the latest server config from disk before falling
	// back to BoltDB. The fsnotify config file watcher (config_watcher.go)
	// now hot-reloads external edits, but its debounce window means a fast
	// edit-then-restart could still race a stale BoltDB record — disk-first
	// here keeps the edit-then-restart UX deterministic regardless.
	serverConfig, err := r.refreshServerFromDiskForRestart(serverName)
	if err != nil {
		if errors.Is(err, ErrServerNotFound) {
			return fmt.Errorf("server '%s' not found in configuration", serverName)
		}
		return fmt.Errorf("failed to refresh server '%s' from disk: %w", serverName, err)
	}
	if serverConfig == nil {
		return fmt.Errorf("server '%s' not found in configuration", serverName)
	}

	// If server is not enabled, enable it first (EnableServer is already async-safe)
	if !serverConfig.Enabled {
		r.logger.Info("Server is disabled, enabling it",
			zap.String("server", serverName))
		return r.EnableServer(serverName, true)
	}

	// Spec 116 PR3 (#1563, review R5.4): the restart is bound to the
	// server's committed generation, read together with its config from one
	// snapshot. The old instance is replaced at that SAME generation by
	// ReplaceInstanceAt, which acts only while that generation is current and
	// the instance is the one observed here; the new instance is connected
	// by a worker that holds that exact instance, never the name, so a commit
	// that removes or changes the server meanwhile wins.
	for attempt := 0; ; attempt++ {
		snap := r.configSvc.Current()
		cfg := snap.GetServer(serverName)
		sg, ok := snap.ServerGeneration(serverName)
		if cfg == nil || !ok {
			return fmt.Errorf("server '%s' not found in configuration", serverName)
		}
		if !cfg.Enabled {
			return r.EnableServer(serverName, true)
		}
		client, exists := r.upstreamManager.GetClient(serverName)
		if !exists {
			// Enabled but no instance (its commit's worker has not run yet,
			// or construction failed): install one at the committed
			// generation.
			r.logger.Info("Server client not found, attempting to create and connect",
				zap.String("server", serverName))
			go r.restartInstallWorker(serverName, cfg, sg.Gen)
			return nil
		}

		// Recreate the client to pick up new secrets: simply reconnecting
		// reuses the old client with old (unresolved) secrets.
		r.logger.Info("Replacing the server's client to recreate it with fresh secret resolution",
			zap.String("server", serverName))
		fresh, err := r.upstreamManager.ReplaceInstanceAt(serverName, sg.Gen, client, cfg)
		if errors.Is(err, upstream.ErrStaleGeneration) {
			if attempt < 3 {
				continue
			}
			return fmt.Errorf("server '%s' changed while it was being restarted; try again", serverName)
		}
		if err != nil {
			return fmt.Errorf("failed to restart server '%s': %w", serverName, err)
		}
		go r.restartConnectWorker(serverName, fresh, sg.Gen)
		break
	}

	r.logger.Info("Server restart initiated asynchronously", zap.String("server", serverName))
	return nil
}

// restartInstallWorker is RestartServer's worker for a server with no
// instance: it installs and connects one at generation gen, and is inert once
// a newer commit superseded gen.
func (r *Runtime) restartInstallWorker(serverName string, cfg *config.ServerConfig, gen int64) {
	defer r.workerActEnter(serverName, gen)()
	if err := r.upstreamManager.AddServerAt(serverName, cfg, gen); err != nil {
		if errors.Is(err, upstream.ErrStaleGeneration) {
			r.logger.Debug("Restart worker superseded by a newer config commit; nothing to do",
				zap.String("server", serverName), zap.Int64("generation", gen))
			return
		}
		r.logger.Error("Failed to add server during restart",
			zap.String("server", serverName),
			zap.Error(err))
		return
	}
	r.logger.Info("Successfully added server", zap.String("server", serverName))
	r.emitServersChanged("restart", map[string]any{
		"server": serverName,
		"reason": "server_added",
	})
}

// restartConnectWorker connects inst, the instance RestartServer installed,
// then reindexes. An instance a newer commit (or another replacement)
// unregistered meanwhile refuses to connect, so nothing comes back.
func (r *Runtime) restartConnectWorker(serverName string, inst *managed.Client, gen int64) {
	defer r.workerActEnter(serverName, gen)()
	r.logger.Info("Connecting the recreated client",
		zap.String("server", serverName),
		zap.Uint64("instance", uint64(inst.InstanceID())))
	if err := r.upstreamManager.ConnectInstance(serverName, inst); err != nil {
		r.logger.Error("Failed to recreate server after restart",
			zap.String("server", serverName),
			zap.Error(err))
		return
	}
	if inst.Unregistered() {
		r.logger.Debug("Restarted instance was superseded before it connected",
			zap.String("server", serverName))
		return
	}

	r.logger.Info("Successfully recreated server with fresh secrets",
		zap.String("server", serverName))

	// Trigger tool reindexing and emit SSE event AFTER completion
	// to ensure frontend receives accurate tool counts
	if err := r.DiscoverAndIndexTools(r.AppContext()); err != nil {
		r.logger.Error("Failed to reindex tools after restart", zap.Error(err))
	}

	// Emit event AFTER tool discovery completes so frontend gets fresh stats
	r.emitServersChanged("restart", map[string]any{
		"server": serverName,
		"reason": "tool_discovery_complete",
	})
}

// ForceReconnectAllServers triggers reconnection attempts for all managed servers.
func (r *Runtime) ForceReconnectAllServers(reason string) error {
	if r.upstreamManager == nil {
		return fmt.Errorf("upstream manager not initialized")
	}

	if r.logger != nil {
		r.logger.Info("Force reconnect requested for all upstream servers",
			zap.String("reason", reason))
	}

	result := r.upstreamManager.ForceReconnectAll(reason)

	if r.logger != nil {
		r.logger.Info("Force reconnect completed",
			zap.Int("total_servers", result.TotalServers),
			zap.Int("attempted", result.AttemptedServers),
			zap.Int("successful", len(result.SuccessfulServers)),
			zap.Int("failed", len(result.FailedServers)),
			zap.Int("skipped", len(result.SkippedServers)))
	}

	return nil
}

// HandleUpstreamServerChange should be called when upstream servers change.
func (r *Runtime) HandleUpstreamServerChange(ctx context.Context) {
	if ctx == nil {
		ctx = r.AppContext()
	}

	r.logger.Info("Upstream server configuration changed, triggering comprehensive update")

	phase := r.CurrentStatus().Phase
	r.UpdatePhase(phase, "Upstream servers updated")

	// Tool discovery runs in background goroutine, and SSE event is emitted
	// AFTER discovery completes to ensure frontend receives accurate tool counts.
	// This fixes the race condition where stale stats were returned because
	// the event was emitted before StateView was updated.
	go func() {
		if err := r.DiscoverAndIndexTools(ctx); err != nil {
			r.logger.Error("Failed to update tool index after upstream change", zap.Error(err))
		}
		r.cleanupOrphanedIndexEntries()

		// Emit event AFTER tool discovery completes so frontend gets fresh stats
		r.emitServersChanged("tools_indexed", map[string]any{
			"phase":  phase,
			"reason": "tool_discovery_complete",
		})
	}()
}

func (r *Runtime) cleanupOrphanedIndexEntries() {
	if r.indexManager == nil || r.upstreamManager == nil {
		return
	}

	r.logger.Debug("Checking for orphaned index entries")

	orphans, activeCount, indexedCount, err := findOrphanedIndexServers(
		r.indexManager.GetAllIndexedServerNames, r.upstreamManager.GetAllServerNames)
	if err != nil {
		r.logger.Warn("Failed to retrieve indexed server names for orphan cleanup", zap.Error(err))
		return
	}

	var removedCount int
	for _, indexedServer := range orphans {
		r.logger.Info("Removing orphaned index entries for server no longer in config",
			zap.String("server", indexedServer))
		if err := r.indexManager.DeleteServerTools(indexedServer); err != nil {
			r.logger.Warn("Failed to delete orphaned index entries",
				zap.String("server", indexedServer),
				zap.Error(err))
		} else {
			removedCount++
		}
	}

	r.logger.Debug("Orphaned index cleanup completed",
		zap.Int("active_servers", activeCount),
		zap.Int("indexed_servers", indexedCount),
		zap.Int("orphans_removed", removedCount))

	if removedCount > 0 {
		// Removed servers' tools leave the index; their signatures leave too.
		r.reconcileSignatureCache()
	}
}

// findOrphanedIndexServers returns the indexed servers that are not active,
// plus the sizes of both lists for logging.
func findOrphanedIndexServers(
	listIndexed func() ([]string, error),
	listActive func() []string,
) (orphans []string, activeCount, indexedCount int, err error) {
	// Indexed first: a server is registered before its tools are indexed, so a
	// server added while this runs is either missing from the indexed list or
	// already present in the active list read after it. This covers servers newly
	// added during cleanup; index entries persisted from a previous run can still
	// be pruned if their server's async re-registration has not finished yet, and
	// are re-indexed once discovery runs for it.
	indexedServers, err := listIndexed()
	if err != nil {
		return nil, 0, 0, err
	}

	activeServers := listActive()
	active := make(map[string]bool, len(activeServers))
	for _, name := range activeServers {
		active[name] = true
	}

	for _, name := range indexedServers {
		if !active[name] {
			orphans = append(orphans, name)
		}
	}
	return orphans, len(activeServers), len(indexedServers), nil
}

// supervisorEventForwarder subscribes to supervisor events and emits runtime events
// to notify Web UI via SSE when server connection state changes.
func (r *Runtime) supervisorEventForwarder() {
	eventCh := r.supervisor.Subscribe()
	defer r.supervisor.Unsubscribe(eventCh)

	r.logger.Info("Supervisor event forwarder started - will emit servers.changed on connection state changes")

	// Get app context once with proper locking
	appCtx := r.AppContext()

	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				r.logger.Info("Supervisor event channel closed, stopping event forwarder")
				return
			}

			// Emit servers.changed event for connection state changes
			// This triggers Web UI to refresh server list via SSE
			switch event.Type {
			case supervisor.EventServerConnected:
				r.logger.Info("Server connected - emitting servers.changed event",
					zap.String("server", event.ServerName))
				r.emitServersChanged("server_connected", map[string]any{
					"server": event.ServerName,
				})

			case supervisor.EventServerDisconnected:
				r.logger.Info("Server disconnected - emitting servers.changed event",
					zap.String("server", event.ServerName))
				r.emitServersChanged("server_disconnected", map[string]any{
					"server": event.ServerName,
				})

			case supervisor.EventServerStateChanged:
				r.logger.Debug("Server state changed - emitting servers.changed event",
					zap.String("server", event.ServerName))
				r.emitServersChanged("server_state_changed", map[string]any{
					"server": event.ServerName,
				})
			}

		case <-appCtx.Done():
			r.logger.Info("App context cancelled, stopping supervisor event forwarder")
			return
		}
	}
}

// configsEquivalent reports whether two configs marshal to the same JSON —
// the same comparison the config watcher uses to recognise its own saves.
func configsEquivalent(a, b *config.Config) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// reapplyLastGoodSnapshot re-applies a server's last-good snapshot to the index
// (the sweep fallback for a connected server missing from discovery). The
// snapshot is selected and applied under one captureSnapMu hold: a snapshot
// read outside the lock could be superseded by a concurrent capture that marks
// a tool changed, and applying the stale one afterwards would look like a
// genuine revert and clear that hold.
func (r *Runtime) reapplyLastGoodSnapshot(ctx context.Context, serverName string) {
	r.captureSnapMu.Lock()
	defer r.captureSnapMu.Unlock()

	snapshot, inv, seq, snapshotGen := r.lastGoodSnapshotWithCaptureGen(serverName)
	if len(snapshot) == 0 {
		return
	}
	if hook := r.localReindexAfterSnapshot; hook != nil {
		hook()
	}

	// Logged at Info: this can fire repeatedly during reconnect storms and
	// is benign self-healing, not a warning condition.
	r.logger.Info("Server missing from discovery result; reusing last-good tool snapshot",
		zap.String("server", serverName),
		zap.Int("snapshot_tools", len(snapshot)))

	// Re-checks eligibility again immediately before the write.
	// Spec 117 PR-3 (FR-037): only while the snapshot's capture is still
	// current (same client and connection, no newer definitions); otherwise
	// the server's connect discovery owns it and the fallback skips it.
	r.applyServerDiffIfEligibleGuarded(ctx, serverName, snapshot, snapshotGen, inventoryRef{capture: inv, mode: inventoryReconcile}, func() bool {
		return r.lastGoodPurgedSince(serverName, seq)
	})
}
