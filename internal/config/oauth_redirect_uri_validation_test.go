package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLoopbackRedirectURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantHost string
		wantPort int
		wantErr  string
	}{
		{name: "loopback ipv4", uri: "http://127.0.0.1:54108/oauth/callback", wantHost: LoopbackIPv4Host, wantPort: 54108},
		{name: "localhost binds ipv4", uri: "http://localhost:8123/oauth/callback", wantHost: LoopbackIPv4Host, wantPort: 8123},
		{name: "ipv6 loopback binds ipv6", uri: "http://[::1]:8123/oauth/callback", wantHost: LoopbackIPv6Host, wantPort: 8123},
		{name: "whitespace tolerated", uri: "  http://127.0.0.1:54108/oauth/callback  ", wantHost: LoopbackIPv4Host, wantPort: 54108},
		{name: "empty", uri: "", wantErr: "empty"},
		{name: "not a url", uri: "://nope", wantErr: "not a valid URL"},
		{name: "https scheme", uri: "https://127.0.0.1:54108/oauth/callback", wantErr: "http scheme"},
		{name: "ftp scheme", uri: "ftp://nope/oauth/callback", wantErr: "http scheme"},
		{name: "non-loopback host", uri: "https://evil.example.com/nope", wantErr: "http scheme"},
		{name: "non-loopback http host", uri: "http://evil.example.com/oauth/callback", wantErr: "loopback host"},
		{name: "gemini callback path", uri: "http://localhost:7777/oauth2callback", wantErr: "callback path"},
		{name: "no port", uri: "http://127.0.0.1/oauth/callback", wantErr: "explicit port"},
		{name: "port zero", uri: "http://127.0.0.1:0/oauth/callback", wantErr: "invalid port"},
		{name: "query string", uri: "http://127.0.0.1:54108/oauth/callback?x=1", wantErr: "query string or fragment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := ParseLoopbackRedirectURI(tt.uri)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if host != tt.wantHost || port != tt.wantPort {
				t.Fatalf("got (%q, %d), want (%q, %d)", host, port, tt.wantHost, tt.wantPort)
			}
		})
	}
}

func TestOAuthConfigValidate_RejectsMalformedRedirectURI(t *testing.T) {
	o := &OAuthConfig{RedirectURI: "https://evil.example.com/nope"}
	err := o.Validate()
	if err == nil {
		t.Fatal("expected a malformed redirect_uri to be rejected")
	}
	if !strings.Contains(err.Error(), "oauth.redirect_uri") {
		t.Fatalf("error must name the field, got %q", err.Error())
	}

	if err := (&OAuthConfig{RedirectURI: "http://127.0.0.1:54108/oauth/callback"}).Validate(); err != nil {
		t.Fatalf("a valid pin must pass: %v", err)
	}
	if err := (&OAuthConfig{}).Validate(); err != nil {
		t.Fatalf("an absent redirect_uri must pass: %v", err)
	}
}

// TestValidateDetailed_RejectsMalformedRedirectURI covers the REST write
// surfaces: POST /api/v1/config/validate, POST /api/v1/config/apply and
// PATCH /api/v1/config all funnel through Runtime.ValidateConfig /
// Runtime.ApplyConfig, which call ValidateDetailed. Before this the same value
// that MCP's upstream_servers rejected was accepted and persisted via REST.
func TestValidateDetailed_RejectsMalformedRedirectURI(t *testing.T) {
	cfg := &Config{
		Servers: []*ServerConfig{
			{Name: "ok", URL: "https://example.com/mcp", Protocol: "http"},
			{
				Name:     "bad-pin",
				URL:      "https://example.com/mcp",
				Protocol: "http",
				OAuth:    &OAuthConfig{RedirectURI: "https://evil.example.com/nope"},
			},
		},
	}

	var found bool
	for _, e := range cfg.ValidateDetailed() {
		if e.Field == "mcpServers[1].oauth.redirect_uri" {
			found = true
			if !strings.Contains(e.Message, "http scheme") {
				t.Fatalf("unexpected message: %q", e.Message)
			}
		}
	}
	if !found {
		t.Fatalf("ValidateDetailed must flag the malformed pin, got %+v", cfg.ValidateDetailed())
	}

	// A valid pin must not be flagged.
	cfg.Servers[1].OAuth.RedirectURI = "http://127.0.0.1:54108/oauth/callback"
	for _, e := range cfg.ValidateDetailed() {
		if strings.Contains(e.Field, "redirect_uri") {
			t.Fatalf("a valid pin must not be flagged: %+v", e)
		}
	}
}

// TestLoadStillBootsWithMalformedRedirectURI is the other half of the contract:
// a bad value already on disk must NOT brick the boot. Failing the load would
// take every other server down over a field one server uses, and the value is
// already surfaced loudly at connect time.
func TestLoadStillBootsWithMalformedRedirectURI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	body := `{
	  "listen": "127.0.0.1:8080",
	  "mcpServers": [
	    {
	      "name": "bad-pin",
	      "url": "https://example.com/mcp",
	      "protocol": "http",
	      "enabled": true,
	      "oauth": {"redirect_uri": "https://evil.example.com/nope"}
	    }
	  ]
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("a pre-existing malformed oauth.redirect_uri must not fail the load: %v", err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("expected the server to survive the load, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].OAuth == nil || cfg.Servers[0].OAuth.RedirectURI != "https://evil.example.com/nope" {
		t.Fatalf("the value must be preserved verbatim for the connect-time diagnostic")
	}

	// The split is load-bearing: the same config that Validate() (boot) tolerates
	// must be rejected by ValidateDetailed() (every write surface).
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate (boot path) must tolerate the value: %v", err)
	}
	var flagged bool
	for _, e := range cfg.ValidateDetailed() {
		if strings.Contains(e.Field, "redirect_uri") {
			flagged = true
		}
	}
	if !flagged {
		t.Fatal("ValidateDetailed (write surfaces) must still reject the value")
	}
}
