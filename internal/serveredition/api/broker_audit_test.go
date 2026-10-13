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

func findByUser(recs []*storage.ActivityRecord, userID string) *storage.ActivityRecord {
	for _, r := range recs {
		if r.UserID == userID {
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

	// A successful connect and a failed connect (the connect flow is the one
	// operation the broker performs since Spec 107 FR-031).
	sink.RecordBrokerEvent(context.Background(), broker.AuditEvent{
		UserID:     "alice",
		ServerName: "grafana",
		Method:     broker.AuditMethodConnect,
		Action:     broker.AuditActionConnect,
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

	acq := findByUser(recs, "alice")
	if acq == nil {
		t.Fatal("alice's connect record not found")
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
	if acq.Metadata["broker_method"] != broker.AuditMethodConnect {
		t.Fatalf("method metadata missing: %v", acq.Metadata["broker_method"])
	}

	conn := findByUser(recs, "bob")
	if conn == nil {
		t.Fatal("bob's connect record not found")
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
