package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// Spec 058 FR-027 pins the UPSTREAM-facing handshake to the legacy protocol era,
// and this asserts it on the wire rather than by reading the constant back.
//
// The distinction matters because the risk here is a constant that changes
// underneath us: mcp-go v1.0.0 redefined mcp.LATEST_PROTOCOL_VERSION from
// 2025-11-25 to 2026-07-28, so the code that used to request the legacy era kept
// compiling and silently began requesting the modern one. A test asserting
// "the request carries mcp.LATEST_LEGACY_PROTOCOL_VERSION" would have gone on
// passing through exactly that change. Capturing the literal string a real
// upstream receives is the only form that cannot.
//
// When the pin is deliberately lifted, this test should be updated in the same
// change, with the negotiated era asserted per-hop instead.
const expectedUpstreamProtocolVersion = "2025-11-25"

// captureInitialize records the protocolVersion of the first initialize request
// it receives, then answers with a minimal but valid legacy initialize result so
// the client can finish connecting.
type initializeCapture struct {
	mu       sync.Mutex
	versions []string
}

func (c *initializeCapture) recorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.versions...)
}

func (c *initializeCapture) handler(t *testing.T) http.Handler {
	t.Helper()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "initialize":
			c.mu.Lock()
			c.versions = append(c.versions, req.Params.ProtocolVersion)
			c.mu.Unlock()

			// Echo the era the client asked for, which is what a legacy-only
			// upstream does when it supports the requested version.
			writeJSONRPC(w, req.ID, map[string]any{
				"protocolVersion": req.Params.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "pin-probe", "version": "1.0.0"},
			})
		case "tools/list":
			writeJSONRPC(w, req.ID, map[string]any{"tools": []any{}})
		default:
			// Notifications carry no id and need no response.
			if len(req.ID) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	})
}

func writeJSONRPC(w http.ResponseWriter, id json.RawMessage, result map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{"jsonrpc": "2.0", "result": result}
	if len(id) > 0 {
		payload["id"] = json.RawMessage(id)
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// The load-bearing assertion: an upstream sees the legacy era, not 2026-07-28.
func TestUpstreamHandshakeRequestsLegacyProtocolVersion(t *testing.T) {
	disableOAuthForTest(t)

	capture := &initializeCapture{}
	upstream := httptest.NewServer(capture.handler(t))
	defer upstream.Close()

	cfg := &config.ServerConfig{
		Name:     "pin-probe",
		Protocol: "streamable-http",
		URL:      upstream.URL,
		Enabled:  true,
	}
	client, err := NewClient("pin-probe", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))
	t.Cleanup(func() { _ = client.Disconnect() })

	recorded := capture.recorded()
	require.NotEmpty(t, recorded, "the upstream never received an initialize request, so this test proves nothing about the pin")

	for _, version := range recorded {
		assert.Equal(t, expectedUpstreamProtocolVersion, version,
			"the upstream hop must stay on the legacy era until the pin is deliberately lifted (Spec 058 FR-027); "+
				"a bare mcp.LATEST_PROTOCOL_VERSION here would have switched it to 2026-07-28 as a side effect of the library bump")
	}
}

// Guards the constant this file asserts against: if a future library release
// redefines the legacy constant too, the pin's meaning changes and the failure
// should point here rather than showing up as a puzzling wire mismatch.
func TestLegacyProtocolConstantStillNamesTheExpectedEra(t *testing.T) {
	assert.Equal(t, expectedUpstreamProtocolVersion, mcp.LATEST_LEGACY_PROTOCOL_VERSION,
		"mcp.LATEST_LEGACY_PROTOCOL_VERSION changed; re-read Spec 058 FR-027 before updating this expectation")
}
