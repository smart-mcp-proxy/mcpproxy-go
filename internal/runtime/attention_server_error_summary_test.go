package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
)

// A server_error item names the reason the server's health already gives, so
// the attention list and the server list say the same thing about one server.
func TestAttentionServerErrorSummaryUsesHealthSummary(t *testing.T) {
	now := time.Now()
	stale := now.Add(-time.Hour)
	cases := []struct{ name, healthSummary, want string }{
		{"reason from health", "Server exited during startup", "srv: server exited during startup"},
		{"acronym keeps its case", "OAuth discovery failed", "srv: OAuth discovery failed"},
		{"single capital word then digits", "HTTP 502 from upstream", "srv: HTTP 502 from upstream"},
		{"product name keeps its case", "GitHub rejected the token", "srv: GitHub rejected the token"},
		{"one-letter word", "A timeout occurred", "srv: A timeout occurred"},
		{"still connecting", "Connecting...", "srv: connection error"},
		{"no health summary", "", "srv: connection error"},
		{"blank health summary", "   ", "srv: connection error"},
		{"generic health summary", "Connection error", "srv: connection error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := Compute(AttentionInput{Now: now, Servers: []AttentionServer{
				attnServer("srv", func(s *AttentionServer) {
					s.Health.Status = health.StatusError
					s.Health.Summary = tc.healthSummary
					s.StateSince = stale
				}),
			}})
			require.Len(t, items, 1)
			require.Equal(t, AttentionKindServerError, items[0].Kind)
			require.Equal(t, tc.want, items[0].Summary)
		})
	}
}
