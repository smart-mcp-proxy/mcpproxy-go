//go:build server

package api

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/broker"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// waitForActivities polls the activity log until at least n records of the given
// type exist or the deadline passes (SaveActivityAsync writes in a goroutine).
func waitForActivities(t *testing.T, m *storage.Manager, n int) []*storage.ActivityRecord {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		recs, _, err := m.ListActivities(storage.ActivityFilter{
			Types: []string{string(storage.ActivityTypeCredentialBroker)},
			Limit: 100,
		})
		if err != nil {
			t.Fatalf("ListActivities: %v", err)
		}
		if len(recs) >= n {
			return recs
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d credential_broker records, got %d", n, len(recs))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func findByAction(recs []*storage.ActivityRecord, action string) *storage.ActivityRecord {
	for _, r := range recs {
		if r.Metadata != nil && r.Metadata["broker_action"] == action {
			return r
		}
	}
	return nil
}

func TestActivityAuditSink_PersistsAttributionNoSecret(t *testing.T) {
	mgr, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	sink := NewActivityAuditSink(mgr, zap.NewNop().Sugar())
	if sink == nil {
		t.Fatal("expected non-nil sink for a real storage manager")
	}

	// A successful acquisition and a failed connect.
	sink.RecordBrokerEvent(context.Background(), broker.AuditEvent{
		UserID:     "alice",
		ServerName: "grafana",
		Method:     broker.AuditMethodTokenExchange,
		Action:     broker.AuditActionAcquire,
		Outcome:    broker.AuditOutcomeSuccess,
		RequestID:  "req-abc",
	})
	sink.RecordBrokerEvent(context.Background(), broker.AuditEvent{
		UserID:     "bob",
		ServerName: "github",
		Method:     broker.AuditMethodConnect,
		Action:     broker.AuditActionConnect,
		Outcome:    broker.AuditOutcomeFailure,
		Reason:     "token endpoint exchange failed",
		RequestID:  "req-def",
	})

	recs := waitForActivities(t, mgr, 2)

	acq := findByAction(recs, broker.AuditActionAcquire)
	if acq == nil {
		t.Fatal("acquire record not found")
	}
	if acq.UserID != "alice" || acq.ServerName != "grafana" {
		t.Fatalf("missing attribution: user=%q server=%q", acq.UserID, acq.ServerName)
	}
	if acq.RequestID != "req-abc" {
		t.Fatalf("request_id not persisted: %q", acq.RequestID)
	}
	if acq.Status != "success" {
		t.Fatalf("expected success status, got %q", acq.Status)
	}
	if acq.Metadata["broker_method"] != broker.AuditMethodTokenExchange {
		t.Fatalf("method metadata missing: %v", acq.Metadata["broker_method"])
	}

	conn := findByAction(recs, broker.AuditActionConnect)
	if conn == nil {
		t.Fatal("connect record not found")
	}
	if conn.Status != "error" {
		t.Fatalf("expected error status, got %q", conn.Status)
	}
	if conn.ErrorMessage != "token endpoint exchange failed" {
		t.Fatalf("expected coarse error message, got %q", conn.ErrorMessage)
	}

	// No record may carry token/secret material in any visible field.
	for _, r := range recs {
		if r.Arguments != nil {
			t.Fatalf("credential_broker record must not carry arguments: %v", r.Arguments)
		}
		if r.Response != "" {
			t.Fatalf("credential_broker record must not carry a response: %q", r.Response)
		}
	}
}

func TestNewActivityAuditSink_NilStorageReturnsNil(t *testing.T) {
	if sink := NewActivityAuditSink(nil, zap.NewNop().Sugar()); sink != nil {
		t.Fatalf("expected nil sink for nil storage, got %T", sink)
	}
}
