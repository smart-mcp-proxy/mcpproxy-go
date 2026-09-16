package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 105 FR-012 (PR H0, spec.md:116 "Operator-published content"): custom
// initialization `instructions` are operator-authored text published to EVERY
// caller by design, and sit OUTSIDE the semantic-disclosure guarantee. A
// scoped token therefore receives them verbatim — even when they mention a
// server it cannot reach — which is why the agent-token documentation warns
// operators not to put server names or secrets in them. This pins that
// documented behaviour so a later "scrub instructions per caller" change is
// a deliberate spec decision, not drift. It holds on the merge base.

// newCustomInstructionsProxy builds a proxy whose config carries the
// operator's `instructions` at CONSTRUCTION time, on the shared stored-script
// fixture (mcp-go fixes WithInstructions on the server instance, so a
// post-construction edit would not reach initialize).
func newCustomInstructionsProxy(t *testing.T, instructions string) *MCPProxyServer {
	t.Helper()
	proxy, _ := newStoredScriptProxyCfg(t, func(cfg *config.Config) { cfg.Instructions = instructions })
	return proxy
}

// initializeInstructions performs the JSON-RPC initialize handshake on srv
// under ctx and returns the `instructions` the caller is handed.
func initializeInstructions(t *testing.T, ctx context.Context, srv jsonRPCHandler) string {
	t.Helper()
	encoded, err := json.Marshal(srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)))
	require.NoError(t, err)
	var envelope struct {
		Error  *json.RawMessage `json:"error"`
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(encoded, &envelope))
	require.Nil(t, envelope.Error, "initialize must succeed: %s", encoded)
	return envelope.Result.Instructions
}

// TestScopedInitialize_PublishesCustomInstructions (T062 positive control):
// a scoped initialization publishes the operator's custom instructions
// verbatim, including a mention of `b:private_search` on a server the token
// cannot reach — published, documented content (spec.md:116).
func TestScopedInitialize_PublishesCustomInstructions(t *testing.T) {
	const custom = "Team conventions: run b:private_search before answering; never paste raw output."
	proxy := newCustomInstructionsProxy(t, custom)
	require.NotNil(t, proxy.directServer, "fixture: the direct server must exist")

	aOnly := agentCtx([]string{"a"}, []string{auth.PermRead}, "")
	require.False(t, auth.AuthContextFromContext(aOnly).CanAccessServer("b"), "precondition: b is outside the token's scope")

	// The two surfaces that carry instructions today: the default /mcp server
	// (resolveInstructions) and the direct server (resolveDirectInstructions,
	// which appends its deferral legend to the operator's text).
	for label, srv := range map[string]jsonRPCHandler{
		"default": proxy.server,
		"direct":  proxy.directServer,
	} {
		label, srv := label, srv
		t.Run(label, func(t *testing.T) {
			scoped := initializeInstructions(t, aOnly, srv)
			assert.Contains(t, scoped, custom,
				"%s: a scoped initialization must publish the operator's custom instructions verbatim (spec.md:116)", label)
			assert.Contains(t, scoped, "b:private_search",
				"%s: the mention of an out-of-scope server in operator-authored instructions is published by design — the docs warn operators, the proxy does not scrub", label)

			admin := initializeInstructions(t, adminCtx(), srv)
			assert.Equal(t, admin, scoped,
				"%s: instructions are the same text for every caller kind (SC-005)", label)
		})
	}
}

// jsonRPCHandler is the seam every routing-mode server exposes: the raw
// JSON-RPC message handler an HTTP transport feeds.
type jsonRPCHandler interface {
	HandleMessage(context.Context, json.RawMessage) mcp.JSONRPCMessage
}
