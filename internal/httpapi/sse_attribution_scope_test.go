package httpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

func attributionEvent(tokenName, prefix string) internalRuntime.Event {
	return internalRuntime.Event{
		Type:      internalRuntime.EventTypeActivityToolCallCompleted,
		Timestamp: time.Now(),
		Payload: map[string]any{
			"server_name": "alpha",
			"tool_name":   "alpha_tool",
			"status":      "success",
			"attribution": map[string]any{
				"profile":        "work-readonly",
				"profile_source": "pin",
				"client_id":      "cursor",
				"client_name":    "Cursor",
				"token_name":     tokenName,
				"_token_prefix":  prefix,
			},
		},
	}
}

func agentCtxFor(name, prefix string) context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: name, TokenPrefix: prefix,
		AllowedServers: []string{"alpha"}, Permissions: []string{auth.PermRead},
	})
}

func renderedAttribution(t *testing.T, srv *Server, ctx context.Context, evt internalRuntime.Event) map[string]any {
	t.Helper()
	out := srv.renderEventPayloadForCaller(ctx, evt)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	attr, _ := decoded["attribution"].(map[string]any)
	return attr
}

// FR-029/FR-031: a scoped subscriber sees `attribution` only on the events its
// own token made; an admin sees it on every event; the internal token prefix
// never reaches anyone's wire.
func TestSSE_AttributionOnlyOnOwnEventsForScopedSubscriber(t *testing.T) {
	srv := NewServer(&scopeParamsController{}, zap.NewNop().Sugar(), nil)

	own := attributionEvent("scoped-ci", "mcp_agt_aaaa")
	foreignSameName := attributionEvent("scoped-ci", "mcp_agt_zzzz") // same name, different token
	foreign := attributionEvent("client-zed", "mcp_cli_bbbb")

	scoped := agentCtxFor("scoped-ci", "mcp_agt_aaaa")
	assert.NotNil(t, renderedAttribution(t, srv, scoped, own), "own events keep the attribution")
	assert.Equal(t, "work-readonly", renderedAttribution(t, srv, scoped, own)["profile"])
	assert.Nil(t, renderedAttribution(t, srv, scoped, foreign), "another token's attribution is withheld")
	assert.Nil(t, renderedAttribution(t, srv, scoped, foreignSameName), "prefix AND name must both match")

	admin := auth.WithAuthContext(context.Background(), auth.AdminContext())
	got := renderedAttribution(t, srv, admin, foreign)
	require.NotNil(t, got)
	assert.Equal(t, "cursor", got["client_id"])
	assert.Equal(t, "client-zed", got["token_name"])
}

func TestSSE_TokenPrefixNeverOnWire(t *testing.T) {
	srv := NewServer(&scopeParamsController{}, zap.NewNop().Sugar(), nil)
	evt := attributionEvent("scoped-ci", "mcp_agt_aaaa")

	for name, ctx := range map[string]context.Context{
		"admin":       auth.WithAuthContext(context.Background(), auth.AdminContext()),
		"no auth ctx": context.Background(),
		"scoped own":  agentCtxFor("scoped-ci", "mcp_agt_aaaa"),
	} {
		out := srv.renderEventPayloadForCaller(ctx, evt)
		raw, err := json.Marshal(out)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "_token_prefix", name)
		assert.NotContains(t, string(raw), "mcp_agt_aaaa", name)
	}

	// The shared event payload is never edited in place (other subscribers are
	// marshalling it concurrently).
	attr := evt.Payload["attribution"].(map[string]any)
	assert.Equal(t, "mcp_agt_aaaa", attr["_token_prefix"])
}

// An event with no attribution passes through untouched.
func TestSSE_EventWithoutAttributionIsUnchanged(t *testing.T) {
	srv := NewServer(&scopeParamsController{}, zap.NewNop().Sugar(), nil)
	evt := internalRuntime.Event{
		Type:    internalRuntime.EventTypeActivityToolCallCompleted,
		Payload: map[string]any{"server_name": "alpha", "status": "success"},
	}
	out := srv.renderEventPayloadForCaller(agentCtxFor("scoped-ci", "p"), evt)
	assert.Equal(t, evt.Payload, out)
}
