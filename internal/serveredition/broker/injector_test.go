//go:build server

package broker

import (
	"context"
	"errors"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// fakeResolver returns a per-user token so the injector can be exercised without
// a real store/exchanger. It records the per-user resolution so tests can assert
// one user's credential is never produced for another (FR-018).
type fakeResolver struct {
	tokens map[string]string // userID -> access token
	err    error
}

func (f *fakeResolver) Resolve(_ context.Context, userID string, _ *config.ServerConfig) (*UpstreamCredential, error) {
	if f.err != nil {
		return nil, f.err
	}
	tok, ok := f.tokens[userID]
	if !ok {
		return nil, ErrNoCredential
	}
	return &UpstreamCredential{AccessToken: tok, TokenType: "Bearer"}, nil
}

func httpBrokerServer() *config.ServerConfig {
	s := &config.ServerConfig{
		Name:     "ghe",
		URL:      "https://ghe.example/mcp",
		Protocol: "streamable-http",
		AuthBroker: &config.AuthBrokerConfig{
			Mode:          config.AuthBrokerModeTokenExchange,
			TokenEndpoint: "https://idp.example/token",
		},
	}
	s.AuthBroker.ApplyDefaults()
	return s
}

// FR-016: the resolved per-user credential is rendered into the configured
// header/format (default Authorization: Bearer {token}).
func TestInjector_InjectFor_ProducesBrokeredAuth(t *testing.T) {
	inj := NewHeaderInjector(&fakeResolver{tokens: map[string]string{"alice": "alice-tok"}})

	ba, err := inj.InjectFor(context.Background(), "alice", httpBrokerServer())
	if err != nil {
		t.Fatalf("InjectFor: %v", err)
	}
	if ba.Header != "Authorization" {
		t.Fatalf("header = %q, want Authorization", ba.Header)
	}
	if ba.HeaderValue() != "Bearer alice-tok" {
		t.Fatalf("header value = %q, want %q", ba.HeaderValue(), "Bearer alice-tok")
	}
}

// FR-018 + SC-002/003: two users brokered against the SAME shared upstream get
// two distinct outbound tokens; one user's credential is never reused for the
// other.
func TestInjector_TwoUsers_TwoTokens(t *testing.T) {
	inj := NewHeaderInjector(&fakeResolver{tokens: map[string]string{
		"alice": "alice-tok",
		"bob":   "bob-tok",
	}})
	server := httpBrokerServer()

	aliceBA, err := inj.InjectFor(context.Background(), "alice", server)
	if err != nil {
		t.Fatalf("alice: %v", err)
	}
	bobBA, err := inj.InjectFor(context.Background(), "bob", server)
	if err != nil {
		t.Fatalf("bob: %v", err)
	}

	if aliceBA.HeaderValue() == bobBA.HeaderValue() {
		t.Fatalf("two users produced the same outbound token %q (FR-018 violation)", aliceBA.HeaderValue())
	}

	// And the effective outbound headers must carry each user's own token.
	aliceHdr := transport.EffectiveHeaders(nil, aliceBA)
	bobHdr := transport.EffectiveHeaders(nil, bobBA)
	if aliceHdr["Authorization"] != "Bearer alice-tok" || bobHdr["Authorization"] != "Bearer bob-tok" {
		t.Fatalf("cross-user token leak: alice=%q bob=%q", aliceHdr["Authorization"], bobHdr["Authorization"])
	}

	// Per-(user,server) connection keys must differ so connections are never
	// pooled across users (FR-018).
	if ConnectionKey("alice", server) == ConnectionKey("bob", server) {
		t.Fatalf("connection key collided across users (FR-018 violation)")
	}
}

// FR-018: ConnectionKey is stable for the same (user, server) and distinct per
// user and per server so a brokered connection is never reused across either.
func TestConnectionKey_StableAndDistinct(t *testing.T) {
	s1 := httpBrokerServer()
	s2 := httpBrokerServer()
	s2.Name = "other"
	s2.URL = "https://other.example/mcp"

	k1 := ConnectionKey("alice", s1)
	k1again := ConnectionKey("alice", s1)
	if k1 != k1again {
		t.Fatal("ConnectionKey must be stable for the same (user, server)")
	}
	if ConnectionKey("alice", s1) == ConnectionKey("alice", s2) {
		t.Fatal("ConnectionKey must differ per server")
	}
	if ConnectionKey("alice", s1) == ConnectionKey("bob", s1) {
		t.Fatal("ConnectionKey must differ per user")
	}
}

// FR-002: brokering on a stdio upstream is rejected with a clear, actionable
// message — never silently injected.
func TestInjector_RejectsStdio(t *testing.T) {
	inj := NewHeaderInjector(&fakeResolver{tokens: map[string]string{"alice": "x"}})
	stdio := &config.ServerConfig{
		Name:     "local",
		Protocol: "stdio",
		Command:  "my-mcp",
		AuthBroker: &config.AuthBrokerConfig{
			Mode:          config.AuthBrokerModeTokenExchange,
			TokenEndpoint: "https://idp.example/token",
		},
	}
	stdio.AuthBroker.ApplyDefaults()

	_, err := inj.InjectFor(context.Background(), "alice", stdio)
	if !errors.Is(err, ErrBrokerStdioUnsupported) {
		t.Fatalf("stdio brokering must be rejected with ErrBrokerStdioUnsupported, got %v", err)
	}
}

// A server with no auth_broker block is not brokered: the injector returns
// ErrBrokerNotConfigured and the caller proceeds with today's behaviour.
func TestInjector_NotConfigured(t *testing.T) {
	inj := NewHeaderInjector(&fakeResolver{})
	plain := &config.ServerConfig{Name: "plain", URL: "https://x/mcp", Protocol: "streamable-http"}

	_, err := inj.InjectFor(context.Background(), "alice", plain)
	if !errors.Is(err, ErrBrokerNotConfigured) {
		t.Fatalf("non-brokered server must return ErrBrokerNotConfigured, got %v", err)
	}
}

// An empty userID is rejected before any resolution — brokering is strictly
// per-user (FR-014).
func TestInjector_RejectsAnonymous(t *testing.T) {
	inj := NewHeaderInjector(&fakeResolver{tokens: map[string]string{"alice": "x"}})
	_, err := inj.InjectFor(context.Background(), "", httpBrokerServer())
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("anonymous caller must be rejected with ErrUnauthenticated, got %v", err)
	}
}
