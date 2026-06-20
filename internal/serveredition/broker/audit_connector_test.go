//go:build server

package broker

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
)

// connectorWithSink builds a connector wired to a recording audit sink, pointed
// at the given mock token server.
func connectorWithSink(t *testing.T, m *mockTokenServer) (*OAuthConnector, *recordingSink) {
	t.Helper()
	sink := &recordingSink{}
	c, err := NewOAuthConnector(newConnectorTestStore(t), connectorTestConfig(m.srv.URL), zap.NewNop(), sink)
	if err != nil {
		t.Fatalf("NewOAuthConnector: %v", err)
	}
	return c, sink
}

// assertNoConnectorSecret asserts no event leaked the upstream token or the
// configured client secret (FR-029).
func assertNoConnectorSecret(t *testing.T, m *mockTokenServer, evs []AuditEvent) {
	t.Helper()
	secrets := []string{m.accessToken, m.refreshToken, "gateway-client-secret"}
	for i, ev := range evs {
		for _, val := range []string{ev.Reason, ev.Method, ev.Action, ev.Outcome, ev.ServerName, ev.UserID} {
			for _, secret := range secrets {
				if secret != "" && strings.Contains(val, secret) {
					t.Fatalf("event %d leaked secret %q in %q", i, secret, val)
				}
			}
		}
	}
}

func TestAuditConnect_CompleteSuccess_EmitsConnectSuccess(t *testing.T) {
	m := newMockTokenServer(t)
	c, sink := connectorWithSink(t, m)

	authURL, state, err := c.BuildAuthorizationURL("user-alice")
	if err != nil {
		t.Fatalf("BuildAuthorizationURL: %v", err)
	}
	_ = authURL

	ctx := reqcontext.WithRequestID(context.Background(), "req-connect-1")
	if _, err := c.Complete(ctx, state, "auth-code"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	ev := sink.last(t)
	if ev.Method != AuditMethodConnect || ev.Action != AuditActionConnect {
		t.Fatalf("expected connect/connect, got %s/%s", ev.Method, ev.Action)
	}
	if ev.Outcome != AuditOutcomeSuccess {
		t.Fatalf("expected success, got %s (reason %q)", ev.Outcome, ev.Reason)
	}
	if ev.UserID != "user-alice" {
		t.Fatalf("expected user attribution, got %q", ev.UserID)
	}
	if ev.ServerName != "github-mcp" {
		t.Fatalf("expected server name, got %q", ev.ServerName)
	}
	if ev.RequestID != "req-connect-1" {
		t.Fatalf("expected request_id correlation, got %q", ev.RequestID)
	}
	assertNoConnectorSecret(t, m, sink.all())
}

func TestAuditConnect_CompleteTokenFailure_EmitsFailure_NoBody(t *testing.T) {
	m := newMockTokenServer(t)
	m.status = http.StatusBadRequest // AS rejects; body is {"error":"invalid_grant"}
	c, sink := connectorWithSink(t, m)

	_, state, err := c.BuildAuthorizationURL("user-bob")
	if err != nil {
		t.Fatalf("BuildAuthorizationURL: %v", err)
	}
	if _, err := c.Complete(context.Background(), state, "auth-code"); err == nil {
		t.Fatalf("expected Complete to fail")
	}

	ev := sink.last(t)
	if ev.Outcome != AuditOutcomeFailure {
		t.Fatalf("expected failure, got %s", ev.Outcome)
	}
	if ev.UserID != "user-bob" {
		t.Fatalf("expected user attribution on failure, got %q", ev.UserID)
	}
	// The audit reason is the fixed coarse label, never the upstream body.
	if ev.Reason != "token endpoint exchange failed" {
		t.Fatalf("expected coarse reason, got %q", ev.Reason)
	}
	if strings.Contains(ev.Reason, "invalid_grant") {
		t.Fatalf("audit reason leaked upstream body: %q", ev.Reason)
	}
	assertNoConnectorSecret(t, m, sink.all())
}

func TestAuditConnect_InvalidState_EmitsFailure(t *testing.T) {
	m := newMockTokenServer(t)
	c, sink := connectorWithSink(t, m)

	if _, err := c.Complete(context.Background(), "bogus-state", "auth-code"); err == nil {
		t.Fatalf("expected Complete to fail on unknown state")
	}
	ev := sink.last(t)
	if ev.Outcome != AuditOutcomeFailure || ev.Action != AuditActionConnect {
		t.Fatalf("expected connect/failure, got %s/%s", ev.Action, ev.Outcome)
	}
	if ev.UserID != "" {
		t.Fatalf("unknown state has no attributable user, got %q", ev.UserID)
	}
}

func TestAuditConnect_Deny_EmitsFailure(t *testing.T) {
	m := newMockTokenServer(t)
	c, sink := connectorWithSink(t, m)

	_, state, err := c.BuildAuthorizationURL("user-carol")
	if err != nil {
		t.Fatalf("BuildAuthorizationURL: %v", err)
	}
	if err := c.Deny(state, "access_denied"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	ev := sink.last(t)
	if ev.Outcome != AuditOutcomeFailure || ev.Action != AuditActionConnect {
		t.Fatalf("expected connect/failure, got %s/%s", ev.Action, ev.Outcome)
	}
	if ev.UserID != "user-carol" {
		t.Fatalf("expected user attribution on deny, got %q", ev.UserID)
	}
	if !strings.Contains(ev.Reason, "access_denied") {
		t.Fatalf("expected denial reason, got %q", ev.Reason)
	}
}
