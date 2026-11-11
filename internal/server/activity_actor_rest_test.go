package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Issue #1698: the REST door's server writers (add, patch, remove, quarantine)
// wrote no config_change / quarantine_change row at all, while the MCP tool
// did. They now record one row each, naming the caller the request context
// carries.

func webActorCtx(reqID string) context.Context {
	ctx := runtime.ContextWithActor(context.Background(), runtime.Actor{Kind: "api_key", Surface: profile.SurfaceWeb})
	return reqcontext.WithRequestID(ctx, reqID)
}

// awaitActivity drains ch until an event of typ whose payload matches want
// (every want key equal) arrives, or fails after two seconds.
func awaitActivity(t *testing.T, ch chan runtime.Event, typ runtime.EventType, want map[string]any) runtime.Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type != typ {
				continue
			}
			ok := true
			for k, v := range want {
				if evt.Payload[k] != v {
					ok = false
					break
				}
			}
			if ok {
				return evt
			}
		case <-deadline:
			t.Fatalf("no %s event matching %v", typ, want)
		}
	}
}

func TestRESTDoor_AddServerRecordsConfigChangeWithActor(t *testing.T) {
	srv, _ := newSpec116Server(t, tenantServer())
	ch := srv.runtime.SubscribeEvents()
	defer srv.runtime.UnsubscribeEvents(ch)

	sc := &config.ServerConfig{Name: "newbie", URL: "http://127.0.0.1:9/mcp?token=sekret123456", Protocol: "http", Enabled: false}
	sc.MarkQuarantineExplicitlySet(true)
	require.NoError(t, srv.AddServer(webActorCtx("req-add"), sc))

	evt := awaitActivity(t, ch, runtime.EventTypeActivityConfigChange, map[string]any{"action": "server_added", "affected_entity": "newbie"})
	assert.Equal(t, "api_key", evt.Payload["actor_kind"])
	assert.Equal(t, "web", evt.Payload["surface"])
	assert.Equal(t, "api", evt.Payload["source"])
	assert.Equal(t, "req-add", evt.Payload["request_id"])
	nv, _ := evt.Payload["new_values"].(map[string]interface{})
	require.NotNil(t, nv)
	assert.Equal(t, "http", nv["protocol"])
	assert.NotContains(t, nv["url"], "sekret123456", "the url credential is redacted as on the MCP door")
}

func TestRESTDoor_UpdateServerRecordsChangedFields(t *testing.T) {
	srv, _ := newSpec116Server(t, tenantServer())
	ch := srv.runtime.SubscribeEvents()
	defer srv.runtime.UnsubscribeEvents(ch)

	require.NoError(t, srv.UpdateServer(webActorCtx("req-upd"), "tenant", &config.ServerConfig{Args: []string{"new"}, Headers: map[string]string{"H1": "v2"}, Enabled: true}))

	evt := awaitActivity(t, ch, runtime.EventTypeActivityConfigChange, map[string]any{"action": "server_updated", "affected_entity": "tenant"})
	assert.Equal(t, "api_key", evt.Payload["actor_kind"])
	assert.Equal(t, "req-upd", evt.Payload["request_id"])
	fields, _ := evt.Payload["changed_fields"].([]string)
	assert.ElementsMatch(t, []string{"args", "headers"}, fields)
	nv, _ := evt.Payload["new_values"].(map[string]interface{})
	require.NotNil(t, nv)
	assert.Contains(t, nv, "args")
	_, hasURL := nv["url"]
	assert.False(t, hasURL, "only the changed fields are recorded")
}

func TestRESTDoor_UpdateServerNoOpRecordsNothing(t *testing.T) {
	srv, _ := newSpec116Server(t, tenantServer())
	ch := srv.runtime.SubscribeEvents()
	defer srv.runtime.UnsubscribeEvents(ch)

	require.NoError(t, srv.UpdateServer(webActorCtx("req-noop"), "tenant", &config.ServerConfig{Args: []string{"old"}, Enabled: true}))

	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case evt := <-ch:
			if evt.Type == runtime.EventTypeActivityConfigChange && evt.Payload["action"] == "server_updated" {
				t.Fatalf("a patch that changed nothing recorded %v", evt.Payload)
			}
		case <-deadline:
			return
		}
	}
}

func TestRESTDoor_RemoveServerRecordsConfigChangeWithActor(t *testing.T) {
	srv, _ := newSpec116Server(t, tenantServer())
	ch := srv.runtime.SubscribeEvents()
	defer srv.runtime.UnsubscribeEvents(ch)

	require.NoError(t, srv.RemoveServer(webActorCtx("req-rm"), "tenant"))

	evt := awaitActivity(t, ch, runtime.EventTypeActivityConfigChange, map[string]any{"action": "server_removed", "affected_entity": "tenant"})
	assert.Equal(t, "api_key", evt.Payload["actor_kind"])
	assert.Equal(t, "web", evt.Payload["surface"])
	assert.Equal(t, "req-rm", evt.Payload["request_id"])
}

func TestRESTDoor_QuarantineServerCtxRecordsActor(t *testing.T) {
	srv, _ := newSpec116Server(t, tenantServer())
	ch := srv.runtime.SubscribeEvents()
	defer srv.runtime.UnsubscribeEvents(ch)

	require.NoError(t, srv.QuarantineServerCtx(webActorCtx("req-q"), "tenant", true))

	evt := awaitActivity(t, ch, runtime.EventTypeActivityQuarantineChange, map[string]any{"server_name": "tenant", "quarantined": true})
	assert.Equal(t, "api_key", evt.Payload["actor_kind"])
	assert.Equal(t, "web", evt.Payload["surface"])
	assert.Equal(t, "req-q", evt.Payload["request_id"])

	require.NoError(t, srv.QuarantineServerAtCtx(webActorCtx("req-uq"), "tenant", false, 0))
	evt = awaitActivity(t, ch, runtime.EventTypeActivityQuarantineChange, map[string]any{"server_name": "tenant", "quarantined": false})
	assert.Equal(t, "req-uq", evt.Payload["request_id"])
}

// Review round 1 (Sol 6.1) findings.

// F2: a security approval (POST /security/approve) finishes through
// FinishServerApproval; its quarantine_change row names the approver.
func TestRESTDoor_FinishServerApprovalCtxRecordsActor(t *testing.T) {
	srv, _ := newSpec116Server(t, tenantServer())
	ch := srv.runtime.SubscribeEvents()
	defer srv.runtime.UnsubscribeEvents(ch)

	require.NoError(t, srv.runtime.FinishServerApprovalCtx(webActorCtx("req-fin"), "tenant"))

	evt := awaitActivity(t, ch, runtime.EventTypeActivityQuarantineChange, map[string]any{"server_name": "tenant", "quarantined": false})
	assert.Equal(t, "api_key", evt.Payload["actor_kind"])
	assert.Equal(t, "req-fin", evt.Payload["request_id"])
}

// F3: restarting a disabled server enables it; that server_enabled row names
// the operator who asked for the restart.
func TestRESTDoor_RestartServerCtxEnablesWithActor(t *testing.T) {
	sc := tenantServer()
	sc.Enabled = false
	srv, _ := newSpec116Server(t, sc)
	ch := srv.runtime.SubscribeEvents()
	defer srv.runtime.UnsubscribeEvents(ch)

	require.NoError(t, srv.RestartServerCtx(webActorCtx("req-rs"), "tenant"))

	evt := awaitActivity(t, ch, runtime.EventTypeActivityConfigChange, map[string]any{"action": "server_enabled", "affected_entity": "tenant"})
	assert.Equal(t, "api_key", evt.Payload["actor_kind"])
	assert.Equal(t, "req-rs", evt.Payload["request_id"])
}

// F4: the add row records the COMMITTED entry, not the request: a server the
// admission gate quarantines on create is recorded as quarantined.
func TestRESTDoor_AddServerRecordsCommittedQuarantineState(t *testing.T) {
	srv, _ := newSpec116Server(t, tenantServer())
	ch := srv.runtime.SubscribeEvents()
	defer srv.runtime.UnsubscribeEvents(ch)

	// No explicit quarantine decision and the default (manual) trust tier:
	// admission quarantines it whatever the request said.
	sc := &config.ServerConfig{Name: "fresh", URL: "http://127.0.0.1:9/mcp", Protocol: "http", Enabled: true, Quarantined: false}
	require.NoError(t, srv.AddServer(webActorCtx("req-add2"), sc))

	committed := srv.runtime.Config()
	var got *config.ServerConfig
	for _, s := range committed.Servers {
		if s.Name == "fresh" {
			got = s
		}
	}
	require.NotNil(t, got)
	require.True(t, got.Quarantined, "precondition: admission quarantined the new server")

	evt := awaitActivity(t, ch, runtime.EventTypeActivityConfigChange, map[string]any{"action": "server_added", "affected_entity": "fresh"})
	nv, _ := evt.Payload["new_values"].(map[string]interface{})
	require.NotNil(t, nv)
	assert.Equal(t, true, nv["quarantined"], "the row says what was persisted")
}

// F1 helper: the MCP door stamps the mcp surface onto the caller it derives.
func TestMCPActorContext_StampsMCPSurface(t *testing.T) {
	ctx := mcpActorContext(auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, AgentName: "ops-bot"}))
	a, ok := runtime.RequestActor(ctx)
	require.True(t, ok)
	assert.Equal(t, "agent_token", a.Kind)
	assert.Equal(t, "ops-bot", a.Name)
	assert.Equal(t, profile.SurfaceMCP, a.Surface)
}
