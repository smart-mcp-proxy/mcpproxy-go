//go:build server

package api

import (
	"context"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/broker"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// activityAuditSink persists per-user credential-brokering audit events to the
// existing activity log (spec 074 T10). It implements broker.AuditSink by writing
// an ActivityRecord of type ActivityTypeCredentialBroker. Writes are async so the
// broker's request/connect path is never blocked on storage.
//
// The record carries attribution (UserID, ServerName, RequestID) and the
// method/action/outcome metadata only — there is no field, here or in the source
// AuditEvent, able to hold a token or secret value (FR-028/FR-029).
type activityAuditSink struct {
	storage *storage.Manager
	logger  *zap.SugaredLogger
}

// NewActivityAuditSink builds an audit sink backed by the activity log. It
// returns nil when no storage manager is available, so callers fall back to the
// broker's internal no-op sink and brokering still works (auditing is best-effort).
func NewActivityAuditSink(sm *storage.Manager, logger *zap.SugaredLogger) broker.AuditSink {
	if sm == nil {
		return nil
	}
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	return &activityAuditSink{storage: sm, logger: logger}
}

// RecordBrokerEvent maps a broker.AuditEvent onto an ActivityRecord and stores it
// asynchronously. It never copies token/secret material (FR-029).
func (s *activityAuditSink) RecordBrokerEvent(_ context.Context, ev broker.AuditEvent) {
	if s == nil || s.storage == nil {
		return
	}

	status := "success"
	if ev.Outcome == broker.AuditOutcomeFailure {
		status = "error"
	}

	record := &storage.ActivityRecord{
		Type:       storage.ActivityTypeCredentialBroker,
		Source:     storage.ActivitySourceInternal,
		ServerName: ev.ServerName,
		Status:     status,
		UserID:     ev.UserID,
		RequestID:  ev.RequestID,
		Metadata: map[string]interface{}{
			"broker_method": ev.Method,
			"broker_action": ev.Action,
		},
	}
	if ev.Outcome == broker.AuditOutcomeFailure && ev.Reason != "" {
		// The reason is a coarse, secret-free label produced by the broker.
		record.ErrorMessage = ev.Reason
		record.Metadata["reason"] = ev.Reason
	}

	s.storage.SaveActivityAsync(record)
}
