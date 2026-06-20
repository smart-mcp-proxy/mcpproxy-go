//go:build server

package broker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
)

// recordingSink captures audit events for assertions. It is concurrency-safe so
// it can be used under the resolver's single-flight without data races.
type recordingSink struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (s *recordingSink) RecordBrokerEvent(_ context.Context, ev AuditEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *recordingSink) all() []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEvent, len(s.events))
	copy(out, s.events)
	return out
}

func (s *recordingSink) last(t *testing.T) AuditEvent {
	t.Helper()
	evs := s.all()
	if len(evs) == 0 {
		t.Fatalf("expected at least one audit event, got none")
	}
	return evs[len(evs)-1]
}

// secretToken is a sentinel access-token value asserted to never appear in any
// audit event (FR-029: no secret material in records).
const secretToken = "SUPER-SECRET-ACCESS-TOKEN-do-not-log"

func assertNoSecret(t *testing.T, evs []AuditEvent) {
	t.Helper()
	for i, ev := range evs {
		for field, val := range map[string]string{
			"Reason": ev.Reason, "Method": ev.Method, "Action": ev.Action,
			"Outcome": ev.Outcome, "ServerName": ev.ServerName, "UserID": ev.UserID,
		} {
			if strings.Contains(val, secretToken) {
				t.Fatalf("event %d %s leaked secret material: %q", i, field, val)
			}
		}
	}
}

func TestAudit_CacheHit_EmitsInjectSuccess(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	key := oauth.GenerateServerKey(server.Name, server.URL)
	store.seed("alice", key, &UpstreamCredential{AccessToken: secretToken, ExpiresAt: time.Now().Add(time.Hour)})

	sink := &recordingSink{}
	r := NewCredentialResolver(ResolverDeps{Store: store, Audit: sink})

	ctx := reqcontext.WithRequestID(context.Background(), "req-123")
	if _, err := r.Resolve(ctx, "alice", server); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ev := sink.last(t)
	if ev.UserID != "alice" || ev.ServerName != "grafana" {
		t.Fatalf("missing attribution: %+v", ev)
	}
	if ev.Method != AuditMethodTokenExchange {
		t.Fatalf("expected method %q, got %q", AuditMethodTokenExchange, ev.Method)
	}
	if ev.Action != AuditActionInject {
		t.Fatalf("expected action %q, got %q", AuditActionInject, ev.Action)
	}
	if ev.Outcome != AuditOutcomeSuccess {
		t.Fatalf("expected success, got %q (reason %q)", ev.Outcome, ev.Reason)
	}
	if ev.RequestID != "req-123" {
		t.Fatalf("expected request_id correlation, got %q", ev.RequestID)
	}
	assertNoSecret(t, sink.all())
}

func TestAudit_FreshTokenExchange_EmitsAcquireSuccess(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	ex := &fakeExchanger{cred: &UpstreamCredential{AccessToken: secretToken}}

	sink := &recordingSink{}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex, Audit: sink})

	if _, err := r.Resolve(context.Background(), "bob", server); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ev := sink.last(t)
	if ev.Action != AuditActionAcquire || ev.Outcome != AuditOutcomeSuccess {
		t.Fatalf("expected acquire/success, got %s/%s", ev.Action, ev.Outcome)
	}
	if ev.Method != AuditMethodTokenExchange {
		t.Fatalf("expected token_exchange method, got %q", ev.Method)
	}
	assertNoSecret(t, sink.all())
}

func TestAudit_NearExpiry_EmitsRefreshSuccess(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	key := oauth.GenerateServerKey(server.Name, server.URL)
	store.seed("carol", key, &UpstreamCredential{AccessToken: "old", ExpiresAt: time.Now().Add(5 * time.Second)})
	ex := &fakeExchanger{cred: &UpstreamCredential{AccessToken: secretToken}}

	sink := &recordingSink{}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex, Audit: sink})

	if _, err := r.Resolve(context.Background(), "carol", server); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ev := sink.last(t)
	if ev.Action != AuditActionRefresh || ev.Outcome != AuditOutcomeSuccess {
		t.Fatalf("expected refresh/success, got %s/%s", ev.Action, ev.Outcome)
	}
	assertNoSecret(t, sink.all())
}

func TestAudit_EntraOBO_MethodMapping(t *testing.T) {
	store := newFakeStore()
	b := &config.AuthBrokerConfig{Mode: config.AuthBrokerModeEntraOBO, TokenEndpoint: "https://idp/token"}
	b.ApplyDefaults()
	server := httpServer("graph", b)
	ex := &fakeExchanger{cred: &UpstreamCredential{AccessToken: secretToken}}

	sink := &recordingSink{}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex, Audit: sink})

	if _, err := r.Resolve(context.Background(), "dave", server); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev := sink.last(t); ev.Method != AuditMethodEntraOBO {
		t.Fatalf("expected entra_obo method, got %q", ev.Method)
	}
}

func TestAudit_ExchangeFailure_EmitsFailureWithReason_NoSecret(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	// The exchanger fails; its error must not contain secret material, and the
	// audit reason is derived from it.
	ex := &fakeExchanger{err: errors.New("token exchange rejected by authorization server")}

	sink := &recordingSink{}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex, Audit: sink})

	if _, err := r.Resolve(context.Background(), "erin", server); err == nil {
		t.Fatalf("expected error")
	}
	ev := sink.last(t)
	if ev.Action != AuditActionAcquire || ev.Outcome != AuditOutcomeFailure {
		t.Fatalf("expected acquire/failure, got %s/%s", ev.Action, ev.Outcome)
	}
	if ev.Reason == "" {
		t.Fatalf("failure event must carry a reason")
	}
	assertNoSecret(t, sink.all())
}

func TestAudit_PolicyDenied_EmitsInjectFailure(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	key := oauth.GenerateServerKey(server.Name, server.URL)
	store.seed("frank", key, &UpstreamCredential{AccessToken: secretToken, ExpiresAt: time.Now().Add(time.Hour)})

	sink := &recordingSink{}
	deny := PolicyHookFunc(func(_ context.Context, _ PolicyInput) (PolicyDecision, error) {
		return PolicyDecision{Allow: false, Reason: "blocked by org policy"}, nil
	})
	r := NewCredentialResolver(ResolverDeps{Store: store, Policy: deny, Audit: sink})

	if _, err := r.Resolve(context.Background(), "frank", server); err == nil {
		t.Fatalf("expected policy-denied error")
	}
	ev := sink.last(t)
	if ev.Action != AuditActionInject || ev.Outcome != AuditOutcomeFailure {
		t.Fatalf("expected inject/failure, got %s/%s", ev.Action, ev.Outcome)
	}
	if !strings.Contains(ev.Reason, "org policy") {
		t.Fatalf("expected policy reason, got %q", ev.Reason)
	}
	assertNoSecret(t, sink.all())
}

func TestAudit_NotConnected_EmitsConnectFailure(t *testing.T) {
	store := newFakeStore()
	server := httpServer("github", connectBroker())
	key := oauth.GenerateServerKey(server.Name, server.URL)
	conn := &fakeConnector{serverKey: key, authURL: "https://idp/authorize?state=xyz"}

	sink := &recordingSink{}
	r := NewCredentialResolver(ResolverDeps{Store: store, Connectors: &fakeConnectorProvider{conn: conn}, Audit: sink})

	if _, err := r.Resolve(context.Background(), "grace", server); err == nil {
		t.Fatalf("expected not-connected error")
	}
	ev := sink.last(t)
	if ev.Action != AuditActionConnect || ev.Outcome != AuditOutcomeFailure {
		t.Fatalf("expected connect/failure, got %s/%s", ev.Action, ev.Outcome)
	}
	if ev.Method != AuditMethodConnect {
		t.Fatalf("expected connect method, got %q", ev.Method)
	}
}

func TestAudit_StoreDisabled_EmitsInjectFailure(t *testing.T) {
	store := newFakeStore()
	store.enabled = false
	server := httpServer("grafana", tokenExchangeBroker())

	sink := &recordingSink{}
	r := NewCredentialResolver(ResolverDeps{Store: store, Audit: sink})

	if _, err := r.Resolve(context.Background(), "heidi", server); err == nil {
		t.Fatalf("expected store-disabled error")
	}
	if ev := sink.last(t); ev.Action != AuditActionInject || ev.Outcome != AuditOutcomeFailure {
		t.Fatalf("expected inject/failure, got %s/%s", ev.Action, ev.Outcome)
	}
}

func TestAudit_Unauthenticated_NoEvent(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	sink := &recordingSink{}
	r := NewCredentialResolver(ResolverDeps{Store: store, Audit: sink})

	if _, err := r.Resolve(context.Background(), "", server); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
	if evs := sink.all(); len(evs) != 0 {
		t.Fatalf("expected no audit event for anonymous caller, got %d", len(evs))
	}
}
