package transport

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Locks the behavior the supervisor classifier hint relies on (#599 / PR #606):
// an auto-detected stdio server (empty/auto protocol + a command) must resolve to
// stdio, so the stdio-gated diagnostic rules fire for it — using the raw
// Config.Protocol would leave such servers unclassified.
func TestDetermineTransportType(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.ServerConfig
		want string
	}{
		{"explicit stdio", config.ServerConfig{Protocol: "stdio"}, TransportStdio},
		{"auto-detected stdio (empty protocol + command)", config.ServerConfig{Command: "npx"}, TransportStdio},
		{"auto protocol + command", config.ServerConfig{Protocol: "auto", Command: "docker"}, TransportStdio},
		{"empty protocol + url", config.ServerConfig{URL: "https://example.com/mcp"}, TransportStreamableHTTP},
		{"explicit http", config.ServerConfig{Protocol: "http", URL: "https://example.com/mcp"}, "http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			if got := DetermineTransportType(&cfg); got != tc.want {
				t.Errorf("DetermineTransportType(%+v) = %q, want %q", tc.cfg, got, tc.want)
			}
		})
	}
}
