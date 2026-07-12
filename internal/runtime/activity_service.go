package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Default retention configuration
const (
	// DefaultRetentionMaxAge is the default max age for activity records (7 days)
	DefaultRetentionMaxAge = 7 * 24 * time.Hour
	// DefaultRetentionMaxRecords is the default max number of records (10000)
	DefaultRetentionMaxRecords = 10000
	// DefaultRetentionCheckInterval is the default interval between retention checks (1 hour)
	DefaultRetentionCheckInterval = 1 * time.Hour
	// DefaultRetentionMaxSizeBytes is the default total activity-log size cap (256MB)
	DefaultRetentionMaxSizeBytes int64 = 256 * 1024 * 1024
)

// SensitiveDataEventEmitter provides the ability to emit sensitive data detection events.
// This interface is implemented by Runtime to enable event emission from ActivityService.
type SensitiveDataEventEmitter interface {
	// EmitSensitiveDataDetected emits an event when sensitive data is detected.
	EmitSensitiveDataDetected(activityID string, detectionCount int, maxSeverity string, detectionTypes []string)
}

// SessionClientResolver maps an MCP session id to the client that opened it
// (clientInfo.name / clientInfo.version from the initialize handshake).
// Returns empty strings when the session is unknown.
type SessionClientResolver func(sessionID string) (name, version string)

// SessionWorkSessionResolver maps an MCP session id to the WORK session it
// belongs to (Spec 082): one client, one project, across reconnects.
//
// It returns the id cached on the connection — deliberately not a fresh
// derivation, so every record from one connection agrees on its work session.
type SessionWorkSessionResolver func(sessionID string) string

// ActivityService subscribes to activity events and persists them to storage.
// It runs as a background goroutine and handles activity recording non-blocking.
type ActivityService struct {
	storage *storage.Manager
	logger  *zap.Logger

	// clientResolver stamps the MCP client onto each activity record at WRITE
	// time. Read-time joining against the sessions API is not viable: the core
	// retains only the 100 most recent sessions, while activity is retained for
	// 90 days — and an IDE that reconnects every few minutes burns through 100
	// sessions in about a day. Any name resolved by lookup therefore decays back
	// to a bare session id. Denormalizing it here makes it permanent, and it
	// costs nothing: the resolver reads the in-memory session store (O(1)) and
	// the session is by definition still open when its activity is emitted.
	//
	// nil until wired via SetSessionClientResolver; the records simply carry no
	// client name in that case.
	clientResolver SessionClientResolver

	// workSessionResolver returns the WORK session a record belongs to (Spec 082):
	// one client, one project, across reconnects. Stamped at write time for the
	// same reason the client name is — a value resolved later decays once the
	// record it points at is evicted.
	workSessionResolver SessionWorkSessionResolver

	// workSessionReaper drops idle work sessions so the tracker cannot grow
	// without bound. Wired alongside the resolver.
	workSessionReaper func(time.Duration) int

	// Channel for receiving events
	eventCh chan Event

	// Shutdown coordination (Spec 080 FR-010): Runtime.Close must be able to
	// await every BBolt writer this service owns BEFORE the clean-shutdown
	// marker resolves and the DB closes. done signals the main event loop's
	// exit (the final flush-on-shutdown included); workersWG tracks the
	// background loops (retention, usage flush) and the per-event async
	// detection goroutines, all of which write to BBolt. startMu/started make
	// Stop return immediately when Start never ran (done would never close).
	// stopped is the terminal state (Spec 080, review round 5): production
	// launches Start via `go` (lifecycle.go), so a fast shutdown can run Stop
	// BEFORE the Start goroutine is scheduled — Stop marks stopped under
	// startMu and a later Start becomes a no-op instead of launching BBolt
	// writers after the shutdown-marker path began. Start's registration
	// (subscribe + every workersWG.Add) happens entirely under startMu, so a
	// Stop that loses the race blocks until registration is complete and its
	// Wait cannot miss a late worker.
	done      chan struct{}
	workersWG sync.WaitGroup
	startMu   sync.Mutex
	started   bool
	stopped   bool

	// Retention configuration
	maxAge        time.Duration
	maxRecords    int
	maxSizeBytes  int64 // total activity-log size cap in bytes (0 = disabled)
	checkInterval time.Duration

	// Sensitive data detector (Spec 026)
	detector *security.Detector

	// Event emitter for sensitive data detection events (Spec 026)
	eventEmitter SensitiveDataEventEmitter

	// Usage aggregate (Spec 069 A2): actor-owned rollup of tool-call activity.
	// Mutated only on this goroutine via Apply; published to readers as an
	// immutable snapshot. usagePersistIntervalNs is the hot-reloadable flush
	// cadence in nanoseconds.
	usage                  *UsageStore
	usagePersistIntervalNs atomic.Int64
}

// NewActivityService creates a new activity service.
func NewActivityService(storage *storage.Manager, logger *zap.Logger) *ActivityService {
	s := &ActivityService{
		storage:       storage,
		logger:        logger,
		eventCh:       make(chan Event, 100), // Buffer for non-blocking event delivery
		done:          make(chan struct{}),
		maxAge:        DefaultRetentionMaxAge,
		maxRecords:    DefaultRetentionMaxRecords,
		maxSizeBytes:  DefaultRetentionMaxSizeBytes,
		checkInterval: DefaultRetentionCheckInterval,
		detector:      nil, // Detector is optional, set via SetDetector
		usage:         newUsageStore(),
	}
	s.usagePersistIntervalNs.Store(int64(DefaultUsagePersistInterval))
	return s
}

// SetSessionClientResolver wires the session -> MCP client lookup. Safe to leave
// unset (records then carry no client name).
func (s *ActivityService) SetSessionClientResolver(r SessionClientResolver) {
	s.clientResolver = r
}

// SetWorkSessionResolver wires the session -> work-session lookup (Spec 082).
func (s *ActivityService) SetWorkSessionResolver(r SessionWorkSessionResolver) {
	s.workSessionResolver = r
}

// SetWorkSessionReaper wires the idle-work-session sweep into the retention loop.
func (s *ActivityService) SetWorkSessionReaper(f func(time.Duration) int) {
	s.workSessionReaper = f
}

// resolveWorkSession returns the work session a record belongs to, or "" when it
// cannot be attributed (an unattributed record beats one filed under a bucket
// that means nothing).
func (s *ActivityService) resolveWorkSession(sessionID string) string {
	if sessionID == "" || s.workSessionResolver == nil {
		return ""
	}
	return s.workSessionResolver(sessionID)
}

// withClientInfo stamps client_name / client_version onto an activity record's
// metadata, so the Activity Log can name the client that made the call long
// after the session record itself has been evicted.
//
// Returns the metadata map to assign; it allocates one only when there is
// something to add, so records for sessionless events stay exactly as they were.
func (s *ActivityService) withClientInfo(metadata map[string]interface{}, sessionID string) map[string]interface{} {
	if sessionID == "" || s.clientResolver == nil {
		return metadata
	}
	name, version := s.clientResolver(sessionID)
	if name == "" {
		return metadata // unknown session (e.g. already closed) — nothing to add
	}

	if metadata == nil {
		metadata = make(map[string]interface{})
	}
	metadata["client_name"] = name
	if version != "" {
		metadata["client_version"] = version
	}
	return metadata
}

// SetDetector sets the sensitive data detector for async scanning (Spec 026).
// If set, tool call arguments and responses will be scanned for sensitive data.
func (s *ActivityService) SetDetector(detector *security.Detector) {
	s.detector = detector
}

// SetEventEmitter sets the event emitter for sensitive data detection events (Spec 026).
// If set, events will be emitted when sensitive data is detected in tool calls.
func (s *ActivityService) SetEventEmitter(emitter SensitiveDataEventEmitter) {
	s.eventEmitter = emitter
}

// SetRetentionConfig updates the retention configuration.
// maxAge: maximum age for records (0 = no age limit)
// maxRecords: maximum number of records (0 = no count limit)
// checkInterval: how often to run retention cleanup
func (s *ActivityService) SetRetentionConfig(maxAge time.Duration, maxRecords int, checkInterval time.Duration, maxSizeBytes int64) {
	if maxAge > 0 {
		s.maxAge = maxAge
	}
	if maxRecords > 0 {
		s.maxRecords = maxRecords
	}
	if checkInterval > 0 {
		s.checkInterval = checkInterval
	}
	// maxSizeBytes may be explicitly set to 0 to DISABLE the size cap, so a
	// negative sentinel (-1) means "leave unchanged"; >= 0 is applied verbatim.
	if maxSizeBytes >= 0 {
		s.maxSizeBytes = maxSizeBytes
	}
}

// Start begins listening for activity events and persisting them.
// It should be called as a goroutine: go svc.Start(ctx, runtime)
func (s *ActivityService) Start(ctx context.Context, rt *Runtime) {
	// Registration runs entirely under startMu (Spec 080 FR-010, review round
	// 5). If Stop already ran (fast shutdown beat this goroutine — production
	// launches Start via `go`), the service is terminally stopped: return
	// without subscribing or launching any BBolt-writing worker. Otherwise
	// mark started so Stop knows the done channel WILL close, and refuse a
	// second Start (the done/WaitGroup bookkeeping is single-shot). Holding
	// startMu through every workersWG.Add below means a concurrent Stop
	// blocks until registration is complete — its Wait cannot miss a worker.
	s.startMu.Lock()
	if s.stopped {
		s.startMu.Unlock()
		s.logger.Debug("Activity service Start called after Stop; not starting")
		return
	}
	if s.started {
		s.startMu.Unlock()
		s.logger.Warn("Activity service Start called twice; ignoring")
		return
	}
	s.started = true

	// Subscribe to runtime events
	eventCh := rt.SubscribeEvents()

	// Start retention loop in a separate goroutine. Tracked in workersWG: it
	// prunes activity records (BBolt writes), so Stop must await it.
	s.workersWG.Add(1)
	go func() {
		defer s.workersWG.Done()
		s.runRetentionLoop(ctx)
	}()

	// Spec 069 A2: load/rebuild the usage aggregate before processing events,
	// then start the periodic snapshot flush loop (tracked: it writes BBolt).
	s.initUsageFromStorage()
	s.workersWG.Add(1)
	go func() {
		defer s.workersWG.Done()
		s.runUsageFlushLoop(ctx)
	}()
	s.startMu.Unlock()

	defer rt.UnsubscribeEvents(eventCh)

	s.logger.Info("Activity service started")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Activity service shutting down")
			// Flush-on-shutdown: persist the final usage snapshot (Spec 069 A2).
			s.persistUsage()
			close(s.done)
			return
		case evt, ok := <-eventCh:
			if !ok {
				s.logger.Info("Activity service event channel closed")
				s.persistUsage()
				close(s.done)
				return
			}
			s.handleEvent(evt)
		}
	}
}

// runRetentionLoop periodically cleans up old activity records.
func (s *ActivityService) runRetentionLoop(ctx context.Context) {
	// Run initial cleanup
	s.runRetentionCleanup()

	ticker := time.NewTicker(s.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("Activity retention loop stopping")
			return
		case <-ticker.C:
			s.runRetentionCleanup()
			s.reapWorkSessions()
		}
	}
}

// workSessionReapAfter is how long a work session may sit idle before the
// tracker forgets it. Comfortably past the 30-minute idle window, so an entry is
// only dropped once it can no longer be continued.
const workSessionReapAfter = 4 * time.Hour

// reapWorkSessions stops the tracker growing without bound on a long-lived
// daemon: one map entry per distinct identity, forever, is a slow leak.
func (s *ActivityService) reapWorkSessions() {
	if s.workSessionReaper == nil {
		return
	}
	if n := s.workSessionReaper(workSessionReapAfter); n > 0 {
		s.logger.Debug("reaped idle work sessions", zap.Int("count", n))
	}
}

// runRetentionCleanup performs the actual retention cleanup.
func (s *ActivityService) runRetentionCleanup() {
	s.logger.Debug("Running activity retention cleanup",
		zap.Duration("max_age", s.maxAge),
		zap.Int("max_records", s.maxRecords))

	// Prune by age
	if s.maxAge > 0 {
		deleted, err := s.storage.PruneOldActivities(s.maxAge)
		if err != nil {
			s.logger.Error("Failed to prune old activities", zap.Error(err))
		} else if deleted > 0 {
			s.logger.Info("Pruned old activity records",
				zap.Int("deleted", deleted),
				zap.Duration("max_age", s.maxAge))
		}
	}

	// Prune by count
	if s.maxRecords > 0 {
		deleted, err := s.storage.PruneExcessActivities(s.maxRecords, 0.9)
		if err != nil {
			s.logger.Error("Failed to prune excess activities", zap.Error(err))
		} else if deleted > 0 {
			s.logger.Info("Pruned excess activity records",
				zap.Int("deleted", deleted),
				zap.Int("max_records", s.maxRecords))
		}
	}

	// Prune by total size (runs after age+count; 0 disables). Bounds config.db
	// growth from large per-record payloads that stay under the count/age caps.
	if s.maxSizeBytes > 0 {
		deleted, err := s.storage.PruneActivitiesToSize(s.maxSizeBytes)
		if err != nil {
			s.logger.Error("Failed to prune activities to size budget", zap.Error(err))
		} else if deleted > 0 {
			s.logger.Info("Pruned activity records to size budget",
				zap.Int("deleted", deleted),
				zap.Int64("max_size_mb", s.maxSizeBytes/(1024*1024)))
		}
	}
}

// Stop gracefully shuts down the activity service, waiting for every BBolt
// writer it owns to finish: the main event loop (including its final
// flush-on-shutdown of the usage snapshot), the retention and usage-flush
// loops, and any in-flight async detection goroutines (Spec 080 FR-010: no
// activity write may land after Runtime.Close resolves the clean-shutdown
// marker or closes the DB).
//
// Callers must cancel the context passed to Start FIRST — the final usage
// flush runs on ctx.Done inside the event loop, and Stop waits for it, so the
// flush is captured before the marker resolves. Idempotent, and returns
// immediately when Start never ran.
//
// Stop is also terminal (Spec 080, review round 5): it marks stopped under
// startMu, so a Start that has not yet registered (production starts the
// service via `go` in lifecycle.go) becomes a no-op instead of launching
// retention/usage/persist loops after the shutdown-marker path began. If
// Start is mid-registration, acquiring startMu here blocks until every
// workersWG.Add has happened, so the Wait below cannot miss a worker.
func (s *ActivityService) Stop() {
	s.startMu.Lock()
	s.stopped = true
	started := s.started
	s.startMu.Unlock()
	if !started {
		return
	}
	// Main loop exit (closes done AFTER the shutdown flush). All workersWG.Add
	// calls happen before done closes — the loop goroutines are registered at
	// the top of Start and detection goroutines are only spawned from the event
	// loop — so Wait below cannot race an Add.
	<-s.done
	s.workersWG.Wait()
}

// handleEvent processes an activity event and persists it to storage.
func (s *ActivityService) handleEvent(evt Event) {
	switch evt.Type {
	case EventTypeActivityToolCallCompleted:
		s.handleToolCallCompleted(evt)
	case EventTypeActivityPolicyDecision:
		s.handlePolicyDecision(evt)
	case EventTypeActivityQuarantineChange:
		s.handleQuarantineChange(evt)
	case EventTypeActivityToolCallStarted:
		// Started events are logged but not persisted - we wait for completion
		s.logger.Debug("Activity tool call started",
			zap.String("server_name", getStringPayload(evt.Payload, "server_name")),
			zap.String("tool_name", getStringPayload(evt.Payload, "tool_name")),
			zap.String("session_id", getStringPayload(evt.Payload, "session_id")),
			zap.String("request_id", getStringPayload(evt.Payload, "request_id")))
	// Spec 024: System lifecycle events
	case EventTypeActivitySystemStart:
		s.handleSystemStart(evt)
	case EventTypeActivitySystemStop:
		s.handleSystemStop(evt)
	case EventTypeActivityInternalToolCall:
		s.handleInternalToolCall(evt)
	case EventTypeActivityConfigChange:
		s.handleConfigChange(evt)
	// Spec 032: Tool-level quarantine events
	case EventTypeActivityToolQuarantineChange:
		s.handleToolQuarantineChange(evt)
	// Spec 077 US4: one settled activity record per server per scan replaces the
	// former per-scanner started/completed/failed storm (Spec 039).
	case EventTypeSecurityScanSettled:
		s.handleSecurityScanSettled(evt)
	default:
		// Ignore other event types
	}
}

// handleToolCallCompleted persists a tool call completion event.
func (s *ActivityService) handleToolCallCompleted(evt Event) {
	serverName := getStringPayload(evt.Payload, "server_name")
	toolName := getStringPayload(evt.Payload, "tool_name")
	sessionID := getStringPayload(evt.Payload, "session_id")
	requestID := getStringPayload(evt.Payload, "request_id")
	source := getStringPayload(evt.Payload, "source")
	status := getStringPayload(evt.Payload, "status")
	errorMsg := getStringPayload(evt.Payload, "error_message")
	arguments := getMapPayload(evt.Payload, "arguments")
	response := getStringPayload(evt.Payload, "response")
	responseTruncated := getBoolPayload(evt.Payload, "response_truncated")
	durationMs := getInt64Payload(evt.Payload, "duration_ms")

	// Extract intent metadata if present (Spec 018)
	toolVariant := getStringPayload(evt.Payload, "tool_variant")
	intent := getMapPayload(evt.Payload, "intent")
	// Extract content trust metadata if present (Spec 035)
	contentTrust := getStringPayload(evt.Payload, "content_trust")
	// Spec 057 FR-011: profile slug for tool calls from a /mcp/p/<slug> URL.
	profileSlug := getStringPayload(evt.Payload, "profile")
	// Default source to "mcp" if not specified (backwards compatibility)
	activitySource := storage.ActivitySourceMCP
	if source != "" {
		activitySource = storage.ActivitySource(source)
	}

	// Build metadata with intent information if present
	var metadata map[string]interface{}
	if toolVariant != "" || intent != nil || contentTrust != "" || profileSlug != "" {
		metadata = make(map[string]interface{})
		if toolVariant != "" {
			metadata["tool_variant"] = toolVariant
		}
		if intent != nil {
			metadata["intent"] = intent
		}
		// Spec 035: Tag activity with content trust level based on openWorldHint
		if contentTrust != "" {
			metadata["content_trust"] = contentTrust
		}
		// Spec 057 FR-011: top-level profile slug (NOT nested under intent), so
		// operators can correlate activity to the profile it came from.
		if profileSlug != "" {
			metadata["profile"] = profileSlug
		}
	}
	// Name the MCP client on the record itself, so it survives session eviction.
	metadata = s.withClientInfo(metadata, sessionID)

	// Spec 069 A1: byte sizes measured pre-truncation by the emitter.
	requestBytes := int(getInt64Payload(evt.Payload, "request_bytes"))
	responseBytes := int(getInt64Payload(evt.Payload, "response_bytes"))

	record := &storage.ActivityRecord{
		Type:              storage.ActivityTypeToolCall,
		Source:            activitySource,
		ServerName:        serverName,
		ToolName:          toolName,
		Arguments:         arguments,
		Response:          response,
		ResponseTruncated: responseTruncated,
		Status:            status,
		ErrorMessage:      errorMsg,
		DurationMs:        durationMs,
		Timestamp:         evt.Timestamp,
		SessionID:         sessionID,
		WorkSessionID:     s.resolveWorkSession(sessionID),
		RequestID:         requestID,
		Metadata:          metadata,
		RequestBytes:      requestBytes,
		ResponseBytes:     responseBytes,
	}

	// Extract user identity from auth metadata injected into arguments (server edition)
	if arguments != nil {
		if userID, ok := arguments["_auth_user_id"].(string); ok && userID != "" {
			record.UserID = userID
		}
		if userEmail, ok := arguments["_auth_user_email"].(string); ok && userEmail != "" {
			record.UserEmail = userEmail
		}
	}

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save activity record",
			zap.Error(err),
			zap.String("server_name", serverName),
			zap.String("tool_name", toolName))
	} else {
		s.logger.Debug("Activity record saved",
			zap.String("id", record.ID),
			zap.String("server_name", serverName),
			zap.String("tool_name", toolName),
			zap.String("status", status))

		// Fold the persisted call into the usage aggregate (Spec 069 A2). Done
		// only on save success so the in-memory rollup stays consistent with a
		// cold-start rebuild that re-scans persisted records.
		if s.usage != nil {
			s.usage.Apply(record)
		}

		// Run async sensitive data detection (Spec 026). Tracked in workersWG:
		// it updates the record's metadata in BBolt, so Stop must await it.
		if s.detector != nil {
			s.workersWG.Add(1)
			go func() {
				defer s.workersWG.Done()
				s.runAsyncDetection(record.ID, arguments, response)
			}()
		}
	}
}

// handlePolicyDecision persists a policy decision event.
func (s *ActivityService) handlePolicyDecision(evt Event) {
	serverName := getStringPayload(evt.Payload, "server_name")
	toolName := getStringPayload(evt.Payload, "tool_name")
	sessionID := getStringPayload(evt.Payload, "session_id")
	decision := getStringPayload(evt.Payload, "decision")
	reason := getStringPayload(evt.Payload, "reason")

	record := &storage.ActivityRecord{
		Type:       storage.ActivityTypePolicyDecision,
		ServerName: serverName,
		ToolName:   toolName,
		Status:     decision,
		Metadata: s.withClientInfo(map[string]interface{}{
			"decision": decision,
			"reason":   reason,
		}, sessionID),
		Timestamp:     evt.Timestamp,
		SessionID:     sessionID,
		WorkSessionID: s.resolveWorkSession(sessionID),
	}

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save policy decision activity",
			zap.Error(err),
			zap.String("server_name", serverName),
			zap.String("decision", decision))
		return
	}

	// Fold blocked attempts into the usage aggregate (Spec 069 A2). Apply
	// ignores non-blocked decisions, so passing every policy decision is safe.
	// Done only on save success so the in-memory rollup stays consistent with a
	// cold-start rebuild that re-scans persisted records.
	if s.usage != nil {
		s.usage.Apply(record)
	}
}

// handleQuarantineChange persists a quarantine change event.
func (s *ActivityService) handleQuarantineChange(evt Event) {
	serverName := getStringPayload(evt.Payload, "server_name")
	quarantined := getBoolPayload(evt.Payload, "quarantined")
	reason := getStringPayload(evt.Payload, "reason")

	status := "enabled"
	if quarantined {
		status = "quarantined"
	}

	record := &storage.ActivityRecord{
		Type:       storage.ActivityTypeQuarantineChange,
		ServerName: serverName,
		Status:     status,
		Metadata: map[string]interface{}{
			"quarantined": quarantined,
			"reason":      reason,
		},
		Timestamp: evt.Timestamp,
	}

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save quarantine change activity",
			zap.Error(err),
			zap.String("server_name", serverName),
			zap.Bool("quarantined", quarantined))
	}
}

// handleSystemStart persists a system start event (Spec 024).
func (s *ActivityService) handleSystemStart(evt Event) {
	version := getStringPayload(evt.Payload, "version")
	listenAddress := getStringPayload(evt.Payload, "listen_address")
	startupDurationMs := getInt64Payload(evt.Payload, "startup_duration_ms")
	configPath := getStringPayload(evt.Payload, "config_path")

	record := &storage.ActivityRecord{
		Type:   storage.ActivityTypeSystemStart,
		Source: storage.ActivitySourceAPI, // System events come from the API server
		Status: "success",
		Metadata: map[string]interface{}{
			"version":             version,
			"listen_address":      listenAddress,
			"startup_duration_ms": startupDurationMs,
			"config_path":         configPath,
		},
		Timestamp: evt.Timestamp,
	}

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save system start activity",
			zap.Error(err),
			zap.String("version", version))
	} else {
		s.logger.Info("System start activity recorded",
			zap.String("id", record.ID),
			zap.String("version", version),
			zap.Int64("startup_duration_ms", startupDurationMs))
	}
}

// handleSystemStop persists a system stop event (Spec 024).
func (s *ActivityService) handleSystemStop(evt Event) {
	reason := getStringPayload(evt.Payload, "reason")
	signal := getStringPayload(evt.Payload, "signal")
	uptimeSeconds := getInt64Payload(evt.Payload, "uptime_seconds")
	errorMsg := getStringPayload(evt.Payload, "error_message")

	status := "success"
	if errorMsg != "" {
		status = "error"
	}

	record := &storage.ActivityRecord{
		Type:         storage.ActivityTypeSystemStop,
		Source:       storage.ActivitySourceAPI,
		Status:       status,
		ErrorMessage: errorMsg,
		Metadata: map[string]interface{}{
			"reason":         reason,
			"signal":         signal,
			"uptime_seconds": uptimeSeconds,
		},
		Timestamp: evt.Timestamp,
	}

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save system stop activity",
			zap.Error(err),
			zap.String("reason", reason))
	} else {
		s.logger.Info("System stop activity recorded",
			zap.String("id", record.ID),
			zap.String("reason", reason),
			zap.Int64("uptime_seconds", uptimeSeconds))
	}
}

// handleInternalToolCall persists an internal tool call event (Spec 024).
func (s *ActivityService) handleInternalToolCall(evt Event) {
	internalToolName := getStringPayload(evt.Payload, "internal_tool_name")
	targetServer := getStringPayload(evt.Payload, "target_server")
	targetTool := getStringPayload(evt.Payload, "target_tool")
	toolVariant := getStringPayload(evt.Payload, "tool_variant")
	sessionID := getStringPayload(evt.Payload, "session_id")
	requestID := getStringPayload(evt.Payload, "request_id")
	status := getStringPayload(evt.Payload, "status")
	errorMsg := getStringPayload(evt.Payload, "error_message")
	durationMs := getInt64Payload(evt.Payload, "duration_ms")
	intent := getMapPayload(evt.Payload, "intent")
	arguments := getMapPayload(evt.Payload, "arguments")

	// Extract response - can be various types, convert to string
	var responseStr string
	if resp := evt.Payload["response"]; resp != nil {
		switch r := resp.(type) {
		case string:
			responseStr = r
		default:
			// Convert to JSON for other types
			if jsonBytes, err := json.Marshal(r); err == nil {
				responseStr = string(jsonBytes)
			}
		}
	}

	// Extract content trust metadata if present (Spec 035)
	contentTrust := getStringPayload(evt.Payload, "content_trust")

	metadata := map[string]interface{}{
		"internal_tool_name": internalToolName,
	}
	if targetServer != "" {
		metadata["target_server"] = targetServer
	}
	if targetTool != "" {
		metadata["target_tool"] = targetTool
	}
	if toolVariant != "" {
		metadata["tool_variant"] = toolVariant
	}
	if intent != nil {
		metadata["intent"] = intent
	}
	// Spec 035: Tag activity with content trust level based on openWorldHint
	if contentTrust != "" {
		metadata["content_trust"] = contentTrust
	}
	// Name the MCP client on the record itself, so it survives session eviction.
	// retrieve_tools calls arrive here, and they are the bulk of session-bearing
	// activity — without this they would be the rows left showing a bare id.
	metadata = s.withClientInfo(metadata, sessionID)

	record := &storage.ActivityRecord{
		Type:          storage.ActivityTypeInternalToolCall,
		Source:        storage.ActivitySourceMCP,
		ToolName:      internalToolName,
		ServerName:    targetServer,
		Arguments:     arguments,
		Response:      responseStr,
		Status:        status,
		ErrorMessage:  errorMsg,
		DurationMs:    durationMs,
		Metadata:      metadata,
		Timestamp:     evt.Timestamp,
		SessionID:     sessionID,
		WorkSessionID: s.resolveWorkSession(sessionID),
		RequestID:     requestID,
	}

	// Extract user identity from auth metadata injected into arguments (server edition)
	if arguments != nil {
		if userID, ok := arguments["_auth_user_id"].(string); ok && userID != "" {
			record.UserID = userID
		}
		if userEmail, ok := arguments["_auth_user_email"].(string); ok && userEmail != "" {
			record.UserEmail = userEmail
		}
	}

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save internal tool call activity",
			zap.Error(err),
			zap.String("internal_tool_name", internalToolName))
	} else {
		s.logger.Debug("Internal tool call activity recorded",
			zap.String("id", record.ID),
			zap.String("internal_tool_name", internalToolName),
			zap.String("status", status))
	}
}

// handleConfigChange persists a config change event (Spec 024).
func (s *ActivityService) handleConfigChange(evt Event) {
	action := getStringPayload(evt.Payload, "action")
	affectedEntity := getStringPayload(evt.Payload, "affected_entity")
	source := getStringPayload(evt.Payload, "source")

	var activitySource storage.ActivitySource
	switch source {
	case "cli":
		activitySource = storage.ActivitySourceCLI
	case "mcp":
		activitySource = storage.ActivitySourceMCP
	default:
		activitySource = storage.ActivitySourceAPI
	}

	metadata := map[string]interface{}{
		"action":          action,
		"affected_entity": affectedEntity,
	}
	if changedFields := getSlicePayload(evt.Payload, "changed_fields"); len(changedFields) > 0 {
		metadata["changed_fields"] = changedFields
	}
	if prevValues := getMapPayload(evt.Payload, "previous_values"); prevValues != nil {
		metadata["previous_values"] = prevValues
	}
	if newValues := getMapPayload(evt.Payload, "new_values"); newValues != nil {
		metadata["new_values"] = newValues
	}

	record := &storage.ActivityRecord{
		Type:       storage.ActivityTypeConfigChange,
		Source:     activitySource,
		ServerName: affectedEntity,
		Status:     "success",
		Metadata:   metadata,
		Timestamp:  evt.Timestamp,
	}

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save config change activity",
			zap.Error(err),
			zap.String("action", action),
			zap.String("affected_entity", affectedEntity))
	} else {
		s.logger.Info("Config change activity recorded",
			zap.String("id", record.ID),
			zap.String("action", action),
			zap.String("affected_entity", affectedEntity))
	}
}

// Helper functions to extract payload values safely

func getStringPayload(payload map[string]any, key string) string {
	if v, ok := payload[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getBoolPayload(payload map[string]any, key string) bool {
	if v, ok := payload[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func getInt64Payload(payload map[string]any, key string) int64 {
	if v, ok := payload[key]; ok {
		switch n := v.(type) {
		case int64:
			return n
		case int:
			return int64(n)
		case float64:
			return int64(n)
		}
	}
	return 0
}

func getMapPayload(payload map[string]any, key string) map[string]interface{} {
	if v, ok := payload[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
		// Also handle map[string]any which is an alias
		if m, ok := v.(map[string]any); ok {
			return m
		}
	}
	return nil
}

func getSlicePayload(payload map[string]any, key string) []string {
	if v, ok := payload[key]; ok {
		if s, ok := v.([]string); ok {
			return s
		}
		// Also handle []interface{} and convert to []string
		if arr, ok := v.([]interface{}); ok {
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if str, ok := item.(string); ok {
					result = append(result, str)
				}
			}
			return result
		}
		if arr, ok := v.([]any); ok {
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if str, ok := item.(string); ok {
					result = append(result, str)
				}
			}
			return result
		}
	}
	return nil
}

// runAsyncDetection performs sensitive data detection asynchronously (Spec 026).
// It scans tool call arguments and responses for sensitive data, then updates
// the activity record metadata with the detection results and emits an event.
func (s *ActivityService) runAsyncDetection(recordID string, arguments map[string]interface{}, response string) {
	if s.detector == nil {
		return
	}

	// Convert arguments to JSON string for scanning
	var argsStr string
	if arguments != nil {
		if argsBytes, err := json.Marshal(arguments); err == nil {
			argsStr = string(argsBytes)
		}
	}

	// Run the detection scan
	result := s.detector.Scan(argsStr, response)

	// Only update the record if something was detected
	if result.Detected {
		s.logger.Info("Sensitive data detected in tool call",
			zap.String("record_id", recordID),
			zap.Int("detection_count", len(result.Detections)),
			zap.Int64("scan_duration_ms", result.ScanDurationMs))

		// Convert result to metadata format
		detectionMeta := map[string]interface{}{
			"sensitive_data_detection": map[string]interface{}{
				"detected":         result.Detected,
				"detection_count":  len(result.Detections),
				"detections":       result.Detections,
				"scan_duration_ms": result.ScanDurationMs,
				"truncated":        result.Truncated,
			},
		}

		// Update the activity record metadata
		if err := s.storage.UpdateActivityMetadata(recordID, detectionMeta); err != nil {
			s.logger.Error("Failed to update activity metadata with detection results",
				zap.Error(err),
				zap.String("record_id", recordID))
		}

		// Emit sensitive_data.detected event (Spec 026)
		if s.eventEmitter != nil {
			// Extract max severity and unique detection types
			maxSeverity := s.extractMaxSeverity(result.Detections)
			detectionTypes := s.extractDetectionTypes(result.Detections)

			s.eventEmitter.EmitSensitiveDataDetected(
				recordID,
				len(result.Detections),
				maxSeverity,
				detectionTypes,
			)
		}
	} else {
		s.logger.Debug("No sensitive data detected in tool call",
			zap.String("record_id", recordID),
			zap.Int64("scan_duration_ms", result.ScanDurationMs))
	}
}

// extractMaxSeverity returns the highest severity level from a list of detections.
// Severity order: critical > high > medium > low
func (s *ActivityService) extractMaxSeverity(detections []security.Detection) string {
	severityOrder := map[string]int{
		"critical": 4,
		"high":     3,
		"medium":   2,
		"low":      1,
	}

	maxSeverity := ""
	maxOrder := 0

	for _, d := range detections {
		order, exists := severityOrder[d.Severity]
		if exists && order > maxOrder {
			maxOrder = order
			maxSeverity = d.Severity
		}
	}

	if maxSeverity == "" && len(detections) > 0 {
		// Fallback to first detection's severity if none matched
		maxSeverity = detections[0].Severity
	}

	return maxSeverity
}

// handleToolQuarantineChange persists a tool quarantine state change event (Spec 032).
func (s *ActivityService) handleToolQuarantineChange(evt Event) {
	serverName := getStringPayload(evt.Payload, "server_name")
	toolName := getStringPayload(evt.Payload, "tool_name")
	action := getStringPayload(evt.Payload, "action")
	metadataStr := getStringPayload(evt.Payload, "metadata")

	// Parse metadata from JSON string
	var metadata map[string]interface{}
	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
			s.logger.Debug("Failed to parse tool quarantine metadata",
				zap.Error(err))
			metadata = map[string]interface{}{
				"action":    action,
				"tool_name": toolName,
			}
		}
	} else {
		metadata = map[string]interface{}{
			"action":    action,
			"tool_name": toolName,
		}
	}

	record := &storage.ActivityRecord{
		Type:       storage.ActivityTypeToolQuarantineChange,
		ServerName: serverName,
		ToolName:   toolName,
		Status:     action,
		Metadata:   metadata,
		Timestamp:  evt.Timestamp,
	}

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save tool quarantine activity",
			zap.Error(err),
			zap.String("server_name", serverName),
			zap.String("tool_name", toolName),
			zap.String("action", action))
	}
}

// handleSecurityScanStarted records a security scan start event (Spec 039).
// handleSecurityScanSettled records the single settled scan result per server
// per scan (Spec 077 US4, MCP-2207). It replaces the former started/completed/
// failed handlers so the activity log carries one entry per scan instead of a
// per-scanner storm.
func (s *ActivityService) handleSecurityScanSettled(evt Event) {
	serverName := getStringPayload(evt.Payload, "server_name")
	scanStatus := getStringPayload(evt.Payload, "status")
	errMsg := getStringPayload(evt.Payload, "error")

	metadata := map[string]interface{}{}
	if findingsSummary := getMapPayload(evt.Payload, "findings_summary"); findingsSummary != nil {
		metadata["findings_summary"] = findingsSummary
	}

	// Map the scan's terminal state onto the activity record status.
	status := "success"
	if scanStatus == "failed" {
		status = "error"
	}

	record := &storage.ActivityRecord{
		Type:         storage.ActivityTypeSecurityScan,
		Source:       storage.ActivitySourceInternal,
		ServerName:   serverName,
		ToolName:     "security_scan",
		Status:       status,
		ErrorMessage: errMsg,
		Timestamp:    evt.Timestamp,
		Metadata:     metadata,
	}

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save settled security scan activity",
			zap.String("server", serverName),
			zap.Error(err))
	}
}

// extractDetectionTypes returns a unique list of detection types from a list of detections.
func (s *ActivityService) extractDetectionTypes(detections []security.Detection) []string {
	seen := make(map[string]struct{})
	types := make([]string, 0, len(detections))

	for _, d := range detections {
		if _, exists := seen[d.Type]; !exists {
			seen[d.Type] = struct{}{}
			types = append(types, d.Type)
		}
	}

	return types
}
