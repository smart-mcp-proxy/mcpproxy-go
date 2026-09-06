package hints_test

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics/hints"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// TestFor_CanonicalizesTransport wires the REAL resolver to the REAL hint
// builder, which is where the defect lived: transport.DetermineTransportType
// returns config.Protocol verbatim (and "streamable-http" when it is unset), so
// four different strings described one transport, and the classifier's HTTP
// arms only recognised one of them.
//
// Every row below is a server shape that exists in the field: config validation
// accepts http / sse / streamable-http / auto (internal/config/config.go), the
// registry add path and the Claude Code and Gemini importers write "http", and
// a URL server added with no protocol resolves to "streamable-http".
func TestFor_CanonicalizesTransport(t *testing.T) {
	cases := []struct {
		name string
		srv  config.ServerConfig
		want string
	}{
		{"url server, no explicit protocol", config.ServerConfig{Name: "a", URL: "https://example.invalid/mcp"}, diagnostics.TransportHTTP},
		{"explicit http", config.ServerConfig{Name: "b", URL: "https://example.invalid/mcp", Protocol: "http"}, diagnostics.TransportHTTP},
		{"explicit sse", config.ServerConfig{Name: "c", URL: "https://example.invalid/sse", Protocol: "sse"}, diagnostics.TransportHTTP},
		{"explicit streamable-http", config.ServerConfig{Name: "d", URL: "https://example.invalid/mcp", Protocol: "streamable-http"}, diagnostics.TransportHTTP},
		{"auto with url", config.ServerConfig{Name: "e", URL: "https://example.invalid/mcp", Protocol: "auto"}, diagnostics.TransportHTTP},
		// The stdio side must NOT be folded in: its classifier arms are gated
		// on this exact value and read a child process's stderr.
		{"stdio command", config.ServerConfig{Name: "f", Command: "npx"}, diagnostics.TransportStdio},
		{"explicit stdio", config.ServerConfig{Name: "g", Command: "npx", Protocol: "stdio"}, diagnostics.TransportStdio},
		{"auto with command", config.ServerConfig{Name: "h", Command: "docker", Protocol: "auto"}, diagnostics.TransportStdio},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := tc.srv
			resolved := transport.DetermineTransportType(&srv)
			got := hints.For(nil, &srv, resolved).Transport
			if got != tc.want {
				t.Errorf("For(..., DetermineTransportType=%q).Transport = %q, want %q", resolved, got, tc.want)
			}
		})
	}
}

// TestFor_CanonicalizesTransportWithoutServer covers the supervisor's
// config-unavailable path (supervisor.go passes transport=="" when
// state.Config is nil). It must stay EMPTY, not become "http": three of the
// four classifier arms the HTTP family unlocks — context.DeadlineExceeded, its
// stringified form, and context.Canceled — carry no HTTP-specific evidence, and
// the fourth reads a stdio child's stderr tail, which can quote a status the
// CHILD saw. Calling an unknown transport "http" therefore turned stdio
// failures into MCPX_HTTP_* codes.
func TestFor_CanonicalizesTransportWithoutServer(t *testing.T) {
	if got := hints.For(nil, nil, "").Transport; got != "" {
		t.Errorf("For(nil, nil, \"\").Transport = %q, want %q (unknown must not be spelled HTTP)", got, "")
	}
}
