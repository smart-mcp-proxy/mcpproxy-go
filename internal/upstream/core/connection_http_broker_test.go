package core

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

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
