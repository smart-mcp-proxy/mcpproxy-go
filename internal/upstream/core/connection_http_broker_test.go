package core

import (
	"reflect"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

func strategyNames(strategies []authStrategy) []string {
	names := make([]string, len(strategies))
	for i, s := range strategies {
		names[i] = s.name
	}
	return names
}

// Spec 074 T7: a per-user brokered credential set on the client drives outbound
// header injection on the headers-auth strategy, replacing any configured auth
// header (FR-016/FR-017).

func TestClient_BrokeredHTTPConfig_ReplacesConfiguredAuth(t *testing.T) {
	c := &Client{
		config: &config.ServerConfig{
			URL:     "https://upstream.example/mcp",
			Headers: map[string]string{"Authorization": "Bearer INBOUND-GATEWAY"},
		},
	}
	c.SetBrokeredAuth(&transport.BrokeredAuth{
		Header: "Authorization", Format: "Bearer {token}", Token: "user-1-token",
	})

	cfg := c.brokeredHTTPConfig()
	if cfg.BrokeredAuth == nil || cfg.BrokeredAuth.Token != "user-1-token" {
		t.Fatalf("brokered auth not threaded into transport config: %+v", cfg.BrokeredAuth)
	}

	eff := transport.EffectiveHeaders(cfg.Headers, cfg.BrokeredAuth)
	if eff["Authorization"] != "Bearer user-1-token" {
		t.Fatalf("outbound auth = %q, want per-user token (inbound must be replaced)", eff["Authorization"])
	}
}

// FR-016: a brokered server may carry no static headers; the headers-auth
// strategy must still be usable so the resolved credential gets injected.
func TestClient_CanUseHeadersStrategy_WithBrokeredAuthOnly(t *testing.T) {
	c := &Client{config: &config.ServerConfig{URL: "https://upstream.example/mcp"}}

	if c.canUseHeadersStrategy() {
		t.Fatalf("no headers and no brokered auth: headers strategy must be skipped")
	}

	c.SetBrokeredAuth(&transport.BrokeredAuth{Header: "Authorization", Format: "Bearer {token}", Token: "t"})
	if !c.canUseHeadersStrategy() {
		t.Fatalf("brokered auth present: headers strategy must be usable even with no static headers")
	}
}

// Spec 074 T7 (security-critical): a per-user brokered connection must be
// FAIL-CLOSED. The only permitted auth strategy is the brokered headers; on
// failure the connection must be refused — it must NEVER fall back to no-auth
// (would connect unauthenticated) or shared OAuth (would borrow another
// identity), either of which defeats per-user isolation (FR-014/FR-017).
func TestClient_BrokeredConnection_FailsClosed_OnlyHeadersStrategy(t *testing.T) {
	c := &Client{config: &config.ServerConfig{URL: "https://upstream.example/mcp"}}

	// Non-brokered: the historical full fallback chain is preserved.
	wantFull := []string{"headers", "no-auth", "OAuth"}
	if got := strategyNames(c.httpAuthStrategies()); !reflect.DeepEqual(got, wantFull) {
		t.Fatalf("non-brokered HTTP strategies = %v, want %v", got, wantFull)
	}
	if got := strategyNames(c.sseAuthStrategies()); !reflect.DeepEqual(got, wantFull) {
		t.Fatalf("non-brokered SSE strategies = %v, want %v", got, wantFull)
	}

	// Brokered: ONLY the headers strategy — no no-auth, no OAuth fallback.
	c.SetBrokeredAuth(&transport.BrokeredAuth{Header: "Authorization", Format: "Bearer {token}", Token: "user-1"})
	wantBrokered := []string{"headers"}
	if got := strategyNames(c.httpAuthStrategies()); !reflect.DeepEqual(got, wantBrokered) {
		t.Fatalf("brokered HTTP strategies = %v, want %v (fail-closed: no no-auth/OAuth fallback)", got, wantBrokered)
	}
	if got := strategyNames(c.sseAuthStrategies()); !reflect.DeepEqual(got, wantBrokered) {
		t.Fatalf("brokered SSE strategies = %v, want %v (fail-closed: no no-auth/OAuth fallback)", got, wantBrokered)
	}
}
