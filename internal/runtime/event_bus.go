package runtime

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// serversChangedCoalescer collapses bursts of servers.changed events into at
// most one publish per interval window, last-write-wins. See spec 047 §B2.
//
// Producers (emitServersChanged) submit a lightweight marker (reason + extra
// fields) via submit(); the marker is stored in `pending` (atomic.Pointer,
// last-write-wins) and the drainer is signalled via the `wake` channel. The
// drainer wakes on either a wake signal or its own timer; on wake it sleeps
// for one interval, then atomically swaps `pending`, materialises the full
// SSE payload via rt.buildServersChangedPayload, and publishes once.
//
// Why build in the drainer, not the producer: the payload requires a
// ListServers call and an N-row BBolt scan per server. Building eagerly in
// the producer means a bulk caller that fires K rapid emits pays K×(1+N)
// BBolt ops, of which K-1 are wasted (the coalescer drops them at publish
// time). Building lazily in the drainer means exactly one build per publish
// window, regardless of how many submits land — Spec 047's amortisation
// promise extended from publish to build.
//
// Tests can drive the coalescer synchronously via flushNow() instead of
// sleeping for the interval.
type serversChangedCoalescer struct {
	rt       *Runtime
	pending  atomic.Pointer[serversChangedMarker]
	wake     chan struct{}
	interval time.Duration

	// parentCtx is set by start() and is the appCtx the drainer was launched
	// with. flushNow uses it as the parent for buildServersChangedPayload's
	// ListServers timeout so app-shutdown cancellation aborts the build
	// instead of waiting up to 2s on a Background()-rooted timeout. nil
	// before start() is called (tests that exercise flushNow directly fall
	// back to context.Background()).
	parentCtx context.Context

	// flush coordination
	flushMu   sync.Mutex
	lastFlush time.Time
}

// serversChangedMarker is the lightweight "something happened" record the
// coalescer stores between submit and flush. The drainer materialises the
// full server-list payload at flush time using the marker's reason + extra.
type serversChangedMarker struct {
	reason string
	extra  map[string]any
}

func newServersChangedCoalescer(rt *Runtime, interval time.Duration) *serversChangedCoalescer {
	return &serversChangedCoalescer{
		rt:       rt,
		wake:     make(chan struct{}, 1),
		interval: interval,
	}
}

// submit stores a marker as the latest pending change (overwriting any prior
// pending marker) and signals the drainer. Cheap by design: no BBolt or
// ListServers call here — the build happens in flushNow.
func (c *serversChangedCoalescer) submit(reason string, extra map[string]any) {
	c.pending.Store(&serversChangedMarker{reason: reason, extra: extra})
	select {
	case c.wake <- struct{}{}:
	default:
		// Drainer is already armed.
	}
}

// flushNow materialises and publishes any pending marker immediately.
// Intended for tests and for shutdown drain. The build (ListServers + N×
// ListToolApprovals) runs synchronously on the caller's goroutine, parented
// to c.parentCtx so app-shutdown cancellation aborts ListServers instead of
// waiting up to 2s on a detached Background()-rooted timeout.
func (c *serversChangedCoalescer) flushNow() {
	marker := c.pending.Swap(nil)
	if marker == nil {
		return
	}
	c.flushMu.Lock()
	c.lastFlush = time.Now()
	c.flushMu.Unlock()
	ctx := c.parentCtx
	if ctx == nil {
		ctx = context.Background()
	}
	evt := c.rt.buildServersChangedPayload(ctx, marker.reason, marker.extra)
	c.rt.publishEvent(evt)
}

// start launches the drainer goroutine. It exits when ctx is canceled, after
// a final flush of any residual pending event.
func (c *serversChangedCoalescer) start(ctx context.Context) {
	c.parentCtx = ctx
	go func() {
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				c.flushNow()
				return
			case <-c.wake:
				// Coalesce: hold for at most one interval before publishing.
				select {
				case <-ctx.Done():
					c.flushNow()
					return
				case <-time.After(c.interval):
					c.flushNow()
				}
			case <-ticker.C:
				// Periodic safety net: if a producer raced past the wake
				// signal slot (channel was full), this catches it.
				if c.pending.Load() != nil {
					c.flushNow()
				}
			}
		}
	}()
}

// serversLister is the minimal slice of the management service needed by
// emitServersChanged to fetch the current server list. The runtime stores the
// management service as interface{} (avoiding an import cycle), so this local
// interface is the type assertion target. Implemented by
// internal/management.(*service).
type serversLister interface {
	ListServers(ctx context.Context) ([]*contracts.Server, *contracts.ServerStats, error)
}

// containsAny reports whether s contains any of the listed substrings (case-insensitive).
// Used by Spec 042 telemetry classification on error messages — only the
// resulting enum category is ever recorded; the message itself is never.
func containsAny(s string, substrs ...string) bool {
	low := strings.ToLower(s)
	for _, sub := range substrs {
		if strings.Contains(low, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

const defaultEventBuffer = 256 // Increased from 16 to prevent event dropping when many servers.changed events flood the bus

// SubscribeEvents registers a new subscriber and returns a channel that will receive runtime events.
// Callers must not close the returned channel; use UnsubscribeEvents when finished.
func (r *Runtime) SubscribeEvents() chan Event {
	ch := make(chan Event, defaultEventBuffer)
	r.eventMu.Lock()
	r.eventSubs[ch] = struct{}{}
	r.eventMu.Unlock()
	return ch
}

// UnsubscribeEvents removes the subscriber and closes the channel.
func (r *Runtime) UnsubscribeEvents(ch chan Event) {
	r.eventMu.Lock()
	if _, ok := r.eventSubs[ch]; ok {
		delete(r.eventSubs, ch)
		close(ch)
	}
	r.eventMu.Unlock()
}

func (r *Runtime) publishEvent(evt Event) {
	r.eventMu.RLock()
	for ch := range r.eventSubs {
		select {
		case ch <- evt:
		default:
		}
	}
	r.eventMu.RUnlock()
}

// emitServersChanged signals that the server list (or any per-server stat)
// may have changed. When the coalescer is wired up (production path) this
// is essentially free: it just stores a marker and signals the drainer.
// The expensive build runs once per coalescing window in the drainer.
//
// The no-coalescer branch (some tests, shutdown paths) builds and publishes
// synchronously to preserve the prior semantics.
func (r *Runtime) emitServersChanged(reason string, extra map[string]any) {
	if r.coalescer != nil {
		r.coalescer.submit(reason, extra)
		return
	}
	// No coalescer (some tests, code paths that haven't wired one yet) — fall
	// back to inline build + publish. Parented to appCtx so shutdown cancels
	// the build; context.Background() if even the runtime context isn't set
	// yet (very early in test bootstrap).
	ctx := r.appCtx
	if ctx == nil {
		ctx = context.Background()
	}
	evt := r.buildServersChangedPayload(ctx, reason, extra)
	r.publishEvent(evt)
}

// buildServersChangedPayload materialises the full servers.changed event
// from a (reason, extra) marker. Spec 047 embeds the current server list +
// stats so SSE subscribers (Swift tray, Web UI) can update local state
// without a follow-up GET /api/v1/servers round trip; on any error we fall
// back to notify-only — older clients (and resilient new clients) handle
// the missing keys.
//
// Cost: 1 ListServers call + N enrichServersWithQuarantineStats reads
// (one BBolt View per server). Lives here so the coalescer drainer can
// invoke it at flush time, amortising the build to one per publish window
// regardless of how many submits land. The caller-supplied parent ctx is
// used as the root of the 2-second ListServers timeout so app-shutdown
// cancellation propagates instead of leaving the drainer goroutine
// blocked on a detached Background-rooted timer.
func (r *Runtime) buildServersChangedPayload(parentCtx context.Context, reason string, extra map[string]any) Event {
	payload := make(map[string]any, len(extra)+3)
	for k, v := range extra {
		payload[k] = v
	}
	payload["reason"] = reason

	if lister, ok := r.managementService.(serversLister); ok && lister != nil {
		ctx, cancel := context.WithTimeout(parentCtx, 2*time.Second)
		servers, stats, err := lister.ListServers(ctx)
		cancel()
		if err != nil {
			r.logger.Warn("buildServersChangedPayload: ListServers failed; emitting notify-only event",
				zap.String("reason", reason),
				zap.Error(err))
		} else {
			redacted := make([]contracts.Server, 0, len(servers))
			for _, s := range servers {
				if s != nil {
					redacted = append(redacted, *s)
				}
			}
			r.redactServerHeaders(redacted)
			// Mirror httpapi.enrichServersWithQuarantineStats so SSE
			// subscribers see the same Quarantine.{Pending,Changed,Blocked}
			// counts the REST list returns. Without this, the Web UI's
			// store-merge strips the previously-set Quarantine field
			// (mergeServers treats incoming as authoritative — absent
			// fields are deleted) and the "N disabled" pill goes stale
			// after a per-tool toggle.
			r.enrichServersWithQuarantineStats(redacted)
			payload["servers"] = redacted
			payload["stats"] = stats
		}
	}

	return newEvent(EventTypeServersChanged, payload)
}

// enrichServersWithQuarantineStats mirrors
// httpapi.(*Server).enrichServersWithQuarantineStats so SSE subscribers receive
// the same pending/changed/blocked counts the REST /api/v1/servers list does.
// Without parity, the frontend's mergeServers (which deletes absent fields)
// would wipe the Quarantine struct on every SSE delivery — the original symptom
// users hit as "quarantine badges go stale after toggling tools, need page
// refresh" and the corresponding "N disabled pill stays after re-enabling".
func (r *Runtime) enrichServersWithQuarantineStats(servers []contracts.Server) {
	if r.storageManager == nil {
		return
	}
	for i := range servers {
		records, err := r.storageManager.ListToolApprovals(servers[i].Name)
		if err != nil {
			r.logger.Debug("Failed to load tool approvals for SSE enrichment",
				zap.String("server", servers[i].Name),
				zap.Error(err))
			continue
		}

		var pending, changed, blocked int
		for _, rec := range records {
			if rec.Disabled {
				blocked++
			}
			switch rec.Status {
			case storage.ToolApprovalStatusPending:
				pending++
			case storage.ToolApprovalStatusChanged:
				changed++
			}
		}

		// Emit the Quarantine block whenever any of the counts is non-zero
		// (matches the REST handler) AND also explicitly when the previous
		// emission set non-zero counts — but here we can't see the prior
		// state, so we follow the same "omit when all-zero" rule as the
		// REST path. The Web UI's mergeServers correctly handles the
		// transition to all-zero by dropping the field.
		if pending > 0 || changed > 0 || blocked > 0 {
			servers[i].Quarantine = &contracts.QuarantineStats{
				PendingCount: pending,
				ChangedCount: changed,
				BlockedCount: blocked,
			}
		}
	}
}

// redactServerHeaders mirrors httpapi.(*Server).redactServerHeaders. It strips
// sensitive header values (Authorization, Cookie, X-API-Key, ...) unless the
// loaded config opts out via reveal_secret_headers: true. Centralizing this in
// the runtime keeps SSE subscribers behind the same trust boundary as the
// HTTP API.
func (r *Runtime) redactServerHeaders(servers []contracts.Server) {
	cfg := r.Config()
	if cfg != nil && cfg.RevealSecretHeaders {
		return
	}
	for i := range servers {
		if len(servers[i].Headers) > 0 {
			servers[i].Headers = oauth.RedactStringHeaders(servers[i].Headers)
		}
	}
}

func (r *Runtime) emitConfigReloaded(path string) {
	payload := map[string]any{"path": path}
	r.publishEvent(newEvent(EventTypeConfigReloaded, payload))
}

func (r *Runtime) emitConfigSaved(path string) {
	payload := map[string]any{"path": path}
	r.publishEvent(newEvent(EventTypeConfigSaved, payload))
}

func (r *Runtime) emitSecretsChanged(operation string, secretName string, extra map[string]any) {
	payload := make(map[string]any, len(extra)+2)
	for k, v := range extra {
		payload[k] = v
	}
	payload["operation"] = operation
	payload["secret_name"] = secretName
	r.publishEvent(newEvent(EventTypeSecretsChanged, payload))
}

// EmitOAuthTokenRefreshed emits an event when proactive token refresh succeeds.
// This is used by the RefreshManager to notify subscribers of successful token refresh.
func (r *Runtime) EmitOAuthTokenRefreshed(serverName string, expiresAt time.Time) {
	payload := map[string]any{
		"server_name": serverName,
		"expires_at":  expiresAt.Format(time.RFC3339),
	}
	r.publishEvent(newEvent(EventTypeOAuthTokenRefreshed, payload))
}

// EmitOAuthRefreshFailed emits an event when proactive token refresh fails after retries.
// This is used by the RefreshManager to notify subscribers that re-authentication is needed.
func (r *Runtime) EmitOAuthRefreshFailed(serverName string, errorMsg string) {
	// Spec 042: increment the error category counter (no message recorded).
	telemetry.RecordErrorOn(r.TelemetryRegistry(), telemetry.ErrCatOAuthRefreshFailed)

	payload := map[string]any{
		"server_name": serverName,
		"error":       errorMsg,
	}
	r.publishEvent(newEvent(EventTypeOAuthRefreshFailed, payload))
}

// EmitActivityToolCallStarted emits an event when a tool execution begins.
// This is used to track activity for observability and debugging.
// source indicates how the call was triggered: "mcp", "cli", or "api"
func (r *Runtime) EmitActivityToolCallStarted(serverName, toolName, sessionID, requestID, source string, args map[string]any) {
	payload := map[string]any{
		"server_name": serverName,
		"tool_name":   toolName,
		"session_id":  sessionID,
		"request_id":  requestID,
		"source":      source,
		"arguments":   args,
	}
	r.publishEvent(newEvent(EventTypeActivityToolCallStarted, payload))
}

// EmitActivityToolCallCompleted emits an event when a tool execution finishes.
// This is used to track activity for observability and debugging.
// source indicates how the call was triggered: "mcp", "cli", or "api"
// arguments is the input parameters passed to the tool call
// toolVariant is the MCP tool variant used (call_tool_read/write/destructive) - optional
// intent is the intent declaration metadata - optional
func (r *Runtime) EmitActivityToolCallCompleted(serverName, toolName, sessionID, requestID, source, status, errorMsg string, durationMs int64, arguments map[string]interface{}, response string, responseTruncated bool, toolVariant string, intent map[string]interface{}, contentTrust string) {
	// Spec 042: classify failed tool calls into the upstream error categories.
	// We never record the error message itself; only a fixed enum value.
	if status == "error" && errorMsg != "" {
		switch {
		case containsAny(errorMsg, "i/o timeout", "context deadline exceeded", "connect: timeout"):
			telemetry.RecordErrorOn(r.TelemetryRegistry(), telemetry.ErrCatUpstreamConnectTimeout)
		case containsAny(errorMsg, "connection refused", "connect: refused"):
			telemetry.RecordErrorOn(r.TelemetryRegistry(), telemetry.ErrCatUpstreamConnectRefused)
		case containsAny(errorMsg, "handshake", "tls:"):
			telemetry.RecordErrorOn(r.TelemetryRegistry(), telemetry.ErrCatUpstreamHandshakeFailed)
		}
	}

	payload := map[string]any{
		"server_name":        serverName,
		"tool_name":          toolName,
		"session_id":         sessionID,
		"request_id":         requestID,
		"source":             source,
		"status":             status,
		"error_message":      errorMsg,
		"duration_ms":        durationMs,
		"response":           response,
		"response_truncated": responseTruncated,
	}
	// Add arguments if provided
	if arguments != nil {
		payload["arguments"] = arguments
	}
	// Add intent metadata if provided (Spec 018)
	if toolVariant != "" {
		payload["tool_variant"] = toolVariant
	}
	if intent != nil {
		payload["intent"] = intent
	}
	// Add content trust metadata if provided (Spec 035)
	if contentTrust != "" {
		payload["content_trust"] = contentTrust
	}
	r.publishEvent(newEvent(EventTypeActivityToolCallCompleted, payload))
}

// EmitActivityPolicyDecision emits an event when a policy blocks a tool call.
func (r *Runtime) EmitActivityPolicyDecision(serverName, toolName, sessionID, decision, reason string) {
	// Spec 042: classify policy blocks as a tool quarantine error category.
	// "blocked" decisions are user-visible reliability events worth counting.
	if decision == "blocked" || decision == "block" {
		telemetry.RecordErrorOn(r.TelemetryRegistry(), telemetry.ErrCatToolQuarantineBlocked)
	}

	payload := map[string]any{
		"server_name": serverName,
		"tool_name":   toolName,
		"session_id":  sessionID,
		"decision":    decision,
		"reason":      reason,
	}
	r.publishEvent(newEvent(EventTypeActivityPolicyDecision, payload))
}

// EmitActivityQuarantineChange emits an event when a server's quarantine state changes.
func (r *Runtime) EmitActivityQuarantineChange(serverName string, quarantined bool, reason string) {
	payload := map[string]any{
		"server_name": serverName,
		"quarantined": quarantined,
		"reason":      reason,
	}
	r.publishEvent(newEvent(EventTypeActivityQuarantineChange, payload))
}

// EmitActivitySystemStart emits an event when MCPProxy server starts (Spec 024).
func (r *Runtime) EmitActivitySystemStart(version, listenAddress string, startupDurationMs int64, configPath string) {
	payload := map[string]any{
		"version":             version,
		"listen_address":      listenAddress,
		"startup_duration_ms": startupDurationMs,
		"config_path":         configPath,
	}
	r.publishEvent(newEvent(EventTypeActivitySystemStart, payload))
}

// EmitActivitySystemStop emits an event when MCPProxy server stops (Spec 024).
func (r *Runtime) EmitActivitySystemStop(reason, signal string, uptimeSeconds int64, errorMsg string) {
	payload := map[string]any{
		"reason":         reason,
		"signal":         signal,
		"uptime_seconds": uptimeSeconds,
		"error_message":  errorMsg,
	}
	r.publishEvent(newEvent(EventTypeActivitySystemStop, payload))
}

// EmitActivityInternalToolCall emits an event when an internal tool is called (Spec 024).
// internalToolName is the name of the internal tool (retrieve_tools, call_tool_read, etc.)
// targetServer and targetTool are used for call_tool_* handlers
// arguments contains the input parameters, response contains the output
// intent is the intent declaration metadata
func (r *Runtime) EmitActivityInternalToolCall(internalToolName, targetServer, targetTool, toolVariant, sessionID, requestID, status, errorMsg string, durationMs int64, arguments map[string]interface{}, response interface{}, intent map[string]interface{}, contentTrust string) {
	payload := map[string]any{
		"internal_tool_name": internalToolName,
		"session_id":         sessionID,
		"request_id":         requestID,
		"status":             status,
		"error_message":      errorMsg,
		"duration_ms":        durationMs,
	}
	if targetServer != "" {
		payload["target_server"] = targetServer
	}
	if targetTool != "" {
		payload["target_tool"] = targetTool
	}
	if toolVariant != "" {
		payload["tool_variant"] = toolVariant
	}
	if arguments != nil {
		payload["arguments"] = arguments
	}
	if response != nil {
		payload["response"] = response
	}
	if intent != nil {
		payload["intent"] = intent
	}
	if contentTrust != "" {
		payload["content_trust"] = contentTrust
	}
	r.publishEvent(newEvent(EventTypeActivityInternalToolCall, payload))
}

// EmitActivityConfigChange emits an event when configuration changes (Spec 024).
// action is one of: server_added, server_removed, server_updated, settings_changed
// source indicates how the change was triggered: "mcp", "cli", or "api"
func (r *Runtime) EmitActivityConfigChange(action, affectedEntity, source string, changedFields []string, previousValues, newValues map[string]interface{}) {
	payload := map[string]any{
		"action":          action,
		"affected_entity": affectedEntity,
		"source":          source,
	}
	if len(changedFields) > 0 {
		payload["changed_fields"] = changedFields
	}
	if previousValues != nil {
		payload["previous_values"] = previousValues
	}
	if newValues != nil {
		payload["new_values"] = newValues
	}
	r.publishEvent(newEvent(EventTypeActivityConfigChange, payload))
}

// EmitSensitiveDataDetected emits an event when sensitive data is detected in a tool call (Spec 026).
// activityID is the ID of the activity record where sensitive data was detected.
// detectionCount is the number of sensitive data detections found.
// maxSeverity is the highest severity level among detections (e.g., "high", "medium", "low").
// detectionTypes is a list of detection type names (e.g., "credit_card", "api_key").
func (r *Runtime) EmitSensitiveDataDetected(activityID string, detectionCount int, maxSeverity string, detectionTypes []string) {
	payload := map[string]any{
		"activity_id":     activityID,
		"detection_count": detectionCount,
		"max_severity":    maxSeverity,
		"detection_types": detectionTypes,
	}
	r.publishEvent(newEvent(EventTypeSensitiveDataDetected, payload))
}

// EmitSecurityScanStarted emits an event when a security scan begins (Spec 039).
func (r *Runtime) EmitSecurityScanStarted(serverName string, scanners []string, jobID string) {
	payload := map[string]any{
		"server_name": serverName,
		"scanners":    scanners,
		"job_id":      jobID,
	}
	r.publishEvent(newEvent(EventTypeSecurityScanStarted, payload))
}

// EmitSecurityScanProgress emits an event for scanner progress updates (Spec 039).
func (r *Runtime) EmitSecurityScanProgress(serverName, scannerID, status string, progress int) {
	payload := map[string]any{
		"server_name": serverName,
		"scanner_id":  scannerID,
		"status":      status,
		"progress":    progress,
	}
	r.publishEvent(newEvent(EventTypeSecurityScanProgress, payload))
}

// EmitSecurityScanCompleted emits an event when a security scan completes (Spec 039).
func (r *Runtime) EmitSecurityScanCompleted(serverName string, findingsSummary map[string]int) {
	payload := map[string]any{
		"server_name":      serverName,
		"findings_summary": findingsSummary,
	}
	r.publishEvent(newEvent(EventTypeSecurityScanCompleted, payload))
}

// EmitSecurityScanFailed emits an event when a scanner fails (Spec 039).
func (r *Runtime) EmitSecurityScanFailed(serverName, scannerID, errMsg string) {
	payload := map[string]any{
		"server_name": serverName,
		"scanner_id":  scannerID,
		"error":       errMsg,
	}
	r.publishEvent(newEvent(EventTypeSecurityScanFailed, payload))
}

// EmitSecurityIntegrityAlert emits an event for integrity violations (Spec 039).
func (r *Runtime) EmitSecurityIntegrityAlert(serverName, alertType, action string) {
	payload := map[string]any{
		"server_name": serverName,
		"alert_type":  alertType,
		"action":      action,
	}
	r.publishEvent(newEvent(EventTypeSecurityIntegrityAlert, payload))
}

// EmitSecurityScannerChanged emits an event when a scanner plugin's state
// changes — notably while a Docker image is being pulled in the background
// (Spec 039). The UI listens for this and refreshes its scanner list.
func (r *Runtime) EmitSecurityScannerChanged(scannerID, status, errMsg string) {
	payload := map[string]any{
		"scanner_id": scannerID,
		"status":     status,
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	r.publishEvent(newEvent(EventTypeSecurityScannerChanged, payload))
}
