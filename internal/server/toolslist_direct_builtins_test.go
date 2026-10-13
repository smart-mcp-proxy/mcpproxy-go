package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 102 T073/T074 — the direct surface's own built-in gate.
//
// STANDALONE, deliberately not an entry in toolsListGoldenSurfaces: that list
// is driven by _DeltaIsEnumerated, which reads a frozen pre099/<surface>.json
// per surface to measure an allowed delta against. Direct mode has no
// pre-feature baseline — nothing was registered on it before this spec — so
// adding it there would demand a file that cannot honestly exist.
//
// What it pins: with ZERO upstream servers the direct listing is exactly the
// built-ins, byte for byte, in BOTH serialization modes, together with the
// instructions string the surface now carries. The listing is captured two
// ways — the registration map and a real tools/list — because they can
// genuinely diverge here (T074).

const directBuiltinsGolden = "direct_mode_builtins"

// newDirectBuiltinsProxy is a proxy with no upstreams and NO custom
// instructions, so the captured bytes are deterministic.
func newDirectBuiltinsProxy(t *testing.T, mode string) *MCPProxyServer {
	t.Helper()
	p := &MCPProxyServer{
		config: &config.Config{
			RoutingMode:            config.RoutingModeDirect,
			DirectToolResponseMode: mode,
			// Instructions deliberately empty: a custom value would make the
			// golden depend on an operator setting.
		},
		logger: zap.NewNop(),
	}
	p.initRoutingModeServers()
	return p
}

// registeredDirectTools is the registration map — what SetTools landed.
func registeredDirectTools(t *testing.T, p *MCPProxyServer) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	for name, st := range p.directServer.ListTools() {
		raw, err := json.Marshal(st.Tool)
		require.NoErrorf(t, err, "marshal registered tool %q", name)
		out[name] = raw
	}
	return out
}

// servedDirectTools is what a real tools/list actually returns, through
// handleListTools and therefore through the WithToolFilters.
//
// T074's point: directServer is the ONE routing-mode server carrying tool
// filters, so "registered" and "served" are different questions on exactly this
// surface. A gate that only read the registration map would miss a filter that
// wrongly dropped a built-in.
func servedDirectTools(t *testing.T, p *MCPProxyServer) map[string]json.RawMessage {
	t.Helper()

	init := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{` +
		`"protocolVersion":"2025-03-26","capabilities":{},` +
		`"clientInfo":{"name":"direct-builtins-gate","version":"0"}}}`)
	require.NotNil(t, p.directServer.HandleMessage(context.Background(), init))

	msg := p.directServer.HandleMessage(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	encoded, err := json.Marshal(msg)
	require.NoError(t, err)

	var envelope struct {
		Error  *json.RawMessage `json:"error"`
		Result struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(encoded, &envelope))
	require.Nil(t, envelope.Error, "tools/list must succeed: %s", encoded)

	out := map[string]json.RawMessage{}
	for _, raw := range envelope.Result.Tools {
		var named struct {
			Name string `json:"name"`
		}
		require.NoError(t, json.Unmarshal(raw, &named))
		out[named.Name] = raw
	}
	return out
}

// ONE golden, not one per mode. Built-ins are rendered by their own builder and
// never by renderDirectTools, so the two modes produce identical bytes — a fact
// the test below asserts outright rather than obscuring behind two files that
// would have to be kept in sync while always being equal.
func directBuiltinsGoldenPath() string {
	return filepath.Join("testdata", toolsListGoldenDir, directBuiltinsGolden+".json")
}

// TestToolsListSnapshot_DirectModeBuiltins is the gate.
//
// The golden is captured from the SERVED payload, not the registration map:
// those are different questions on this surface (directServer is the one
// routing-mode server carrying WithToolFilters), and it is the served bytes a
// client actually receives. Registered-vs-served is then asserted BYTE-exactly —
// an earlier version used assert.JSONEq here, which ignores key ordering and
// formatting and so could not have caught a serialization difference between the
// two, which is precisely what it was there to catch.
func TestToolsListSnapshot_DirectModeBuiltins(t *testing.T) {
	perMode := map[string]string{}

	for _, mode := range []string{config.DirectToolResponseModeFull, config.DirectToolResponseModeDeferred} {
		t.Run(mode, func(t *testing.T) {
			p := newDirectBuiltinsProxy(t, mode)

			registered := registeredDirectTools(t, p)
			served := servedDirectTools(t, p)

			// T074: the registration map and a real tools/list must agree, as a
			// SET and byte for byte. A filter that dropped or reshaped a built-in
			// shows up here and nowhere else.
			assert.Equal(t, sortedToolNames(registered), sortedToolNames(served),
				"the registration map and a real tools/list must serve the same set")
			for name, raw := range registered {
				assert.Equalf(t, string(raw), string(served[name]),
					"registered and served bytes differ for %q", name)
			}

			// Zero upstreams: the listing IS the built-in set.
			assert.Equal(t, []string{"describe_tool"}, sortedToolNames(served),
				"with no upstream servers the direct listing is exactly the built-ins")

			payload := map[string]interface{}{
				"tools":        served,
				"instructions": resolveDirectInstructions(""),
			}
			raw, err := json.MarshalIndent(payload, "", "  ")
			require.NoError(t, err)
			got := string(append(raw, '\n'))
			perMode[mode] = got

			path := directBuiltinsGoldenPath()
			if dir := os.Getenv(toolsListGoldenWriteEnv); dir != "" {
				require.NoError(t, os.MkdirAll(dir, 0o755))
				out := filepath.Join(dir, filepath.Base(path))
				require.NoError(t, os.WriteFile(out, []byte(got), 0o600))
				t.Skipf("golden written to %s; comparison skipped", out)
			}

			want, err := os.ReadFile(path)
			require.NoErrorf(t, err, "missing golden %s", path)
			assert.Equal(t, string(normalizeGoldenEOL(want)), string(normalizeGoldenEOL([]byte(got))),
				"the direct built-in surface changed; regenerate deliberately, never to fix a red run")
		})
	}

	// The reason one golden is enough, asserted rather than assumed. If deferral
	// ever DID reshape a built-in, this fails and the single-golden design has to
	// be revisited — the two subtests above would otherwise silently pin the same
	// bytes twice and neither would notice.
	if len(perMode) == 2 {
		assert.Equal(t, perMode[config.DirectToolResponseModeFull], perMode[config.DirectToolResponseModeDeferred],
			"built-ins are not upstream projections; serialization must not touch them")
	}
}

// The instructions the golden pins are the ones a client actually receives on
// initialize, not just what the helper returns.
func TestToolsListSnapshot_DirectModeBuiltinsInstructionsAreServed(t *testing.T) {
	p := newDirectBuiltinsProxy(t, config.DirectToolResponseModeDeferred)
	assert.Equal(t, resolveDirectInstructions(""), directInitializeInstructions(t, p),
		"the golden's instructions must be the bytes initialize serves")
}
