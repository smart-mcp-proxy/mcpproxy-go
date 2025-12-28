package runtime

import (
	"context"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/storage"
)

// Default retention configuration
const (
	// DefaultRetentionMaxAge is the default max age for activity records (7 days)
	DefaultRetentionMaxAge = 7 * 24 * time.Hour
	// DefaultRetentionMaxRecords is the default max number of records (10000)
	DefaultRetentionMaxRecords = 10000
	// DefaultRetentionCheckInterval is the default interval between retention checks (1 hour)
	DefaultRetentionCheckInterval = 1 * time.Hour
)

// ActivityService subscribes to activity events and persists them to storage.
// It runs as a background goroutine and handles activity recording non-blocking.
type ActivityService struct {
	storage *storage.Manager
	logger  *zap.Logger

	// Channel for receiving events
	eventCh chan Event
	// Done channel for graceful shutdown
	done chan struct{}

	// Retention configuration
	maxAge        time.Duration
	maxRecords    int
	checkInterval time.Duration
}

// NewActivityService creates a new activity service.
func NewActivityService(storage *storage.Manager, logger *zap.Logger) *ActivityService {
	return &ActivityService{
		storage:       storage,
		logger:        logger,
		eventCh:       make(chan Event, 100), // Buffer for non-blocking event delivery
		done:          make(chan struct{}),
		maxAge:        DefaultRetentionMaxAge,
		maxRecords:    DefaultRetentionMaxRecords,
		checkInterval: DefaultRetentionCheckInterval,
	}
}

// SetRetentionConfig updates the retention configuration.
// maxAge: maximum age for records (0 = no age limit)
// maxRecords: maximum number of records (0 = no count limit)
// checkInterval: how often to run retention cleanup
func (s *ActivityService) SetRetentionConfig(maxAge time.Duration, maxRecords int, checkInterval time.Duration) {
	if maxAge > 0 {
		s.maxAge = maxAge
	}
	if maxRecords > 0 {
		s.maxRecords = maxRecords
	}
	if checkInterval > 0 {
		s.checkInterval = checkInterval
	}
}

// Start begins listening for activity events and persisting them.
// It should be called as a goroutine: go svc.Start(ctx, runtime)
func (s *ActivityService) Start(ctx context.Context, rt *Runtime) {
	// Subscribe to runtime events
	eventCh := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventCh)

	// Start retention loop in a separate goroutine
	go s.runRetentionLoop(ctx)

	s.logger.Info("Activity service started")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Activity service shutting down")
			close(s.done)
			return
		case evt, ok := <-eventCh:
			if !ok {
				s.logger.Info("Activity service event channel closed")
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
		}
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
}

// Stop gracefully shuts down the activity service.
func (s *ActivityService) Stop() {
	<-s.done
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
	response := getStringPayload(evt.Payload, "response")
	responseTruncated := getBoolPayload(evt.Payload, "response_truncated")
	durationMs := getInt64Payload(evt.Payload, "duration_ms")

	// Default source to "mcp" if not specified (backwards compatibility)
	activitySource := storage.ActivitySourceMCP
	if source != "" {
		activitySource = storage.ActivitySource(source)
	}

	record := &storage.ActivityRecord{
		Type:              storage.ActivityTypeToolCall,
		Source:            activitySource,
		ServerName:        serverName,
		ToolName:          toolName,
		Response:          response,
		ResponseTruncated: responseTruncated,
		Status:            status,
		ErrorMessage:      errorMsg,
		DurationMs:        durationMs,
		Timestamp:         evt.Timestamp,
		SessionID:         sessionID,
		RequestID:         requestID,
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
		Metadata: map[string]interface{}{
			"decision": decision,
			"reason":   reason,
		},
		Timestamp: evt.Timestamp,
		SessionID: sessionID,
	}

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save policy decision activity",
			zap.Error(err),
			zap.String("server_name", serverName),
			zap.String("decision", decision))
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

