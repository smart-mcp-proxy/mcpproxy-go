package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// Issue #1698: config_change and quarantine_change records name who acted.
// The caller identity travels in the request context (REST stamps it after
// authentication, the MCP tools derive it from the AuthContext) and the
// runtime copies it onto the activity event, so the stored record carries the
// same actor_kind / actor_name / surface keys a profile_change row has.

func TestRequestActor_ExplicitActorWins(t *testing.T) {
	want := Actor{Kind: "agent_token", Name: "ci-bot", Surface: profile.SurfaceCLI}
	ctx := ContextWithActor(context.Background(), want)
	got, ok := RequestActor(ctx)
	require.True(t, ok)
	assert.Equal(t, want, got)
}

func TestRequestActor_DerivedFromAuthContext(t *testing.T) {
	ctx := auth.WithAuthContext(context.Background(), auth.AdminContext())
	got, ok := RequestActor(ctx)
	require.True(t, ok)
	assert.Equal(t, string(auth.CredentialKindAPIKey), got.Kind)
	assert.Equal(t, profile.SurfaceAPI, got.Surface)

	// The tray / CLI socket bypasses the API key: the record says so.
	sock := transport.TagConnectionContext(ctx, transport.ConnectionSourceTray)
	got, ok = RequestActor(sock)
	require.True(t, ok)
	assert.Equal(t, string(auth.CredentialKindSocket), got.Kind)
}

func TestRequestActor_NoCallerIsNoActor(t *testing.T) {
	_, ok := RequestActor(context.Background())
	assert.False(t, ok, "a background context (file reload, startup) names no actor")
}

func TestEmitActivityConfigChangeCtx_CarriesCallerIdentity(t *testing.T) {
	rt := &Runtime{logger: zap.NewNop(), eventSubs: make(map[chan Event]struct{})}
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	ctx := ContextWithActor(context.Background(), Actor{Kind: "agent_token", Name: "ci-bot", Surface: profile.SurfaceCLI})
	ctx = reqcontext.WithRequestID(ctx, "req-42")
	rt.EmitActivityConfigChangeCtx(ctx, "server_added", "github", nil, nil, map[string]interface{}{"protocol": "http"})

	select {
	case evt := <-ch:
		require.Equal(t, EventTypeActivityConfigChange, evt.Type)
		assert.Equal(t, "server_added", evt.Payload["action"])
		assert.Equal(t, "github", evt.Payload["affected_entity"])
		assert.Equal(t, "cli", evt.Payload["source"], "source follows the actor's surface")
		assert.Equal(t, "agent_token", evt.Payload["actor_kind"])
		assert.Equal(t, "ci-bot", evt.Payload["actor_name"])
		assert.Equal(t, "cli", evt.Payload["surface"])
		assert.Equal(t, "req-42", evt.Payload["request_id"])
	case <-time.After(2 * time.Second):
		t.Fatal("no activity.config_change event")
	}
}

func TestEmitActivityConfigChangeCtx_NoActorStaysSilentAboutIt(t *testing.T) {
	rt := &Runtime{logger: zap.NewNop(), eventSubs: make(map[chan Event]struct{})}
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	rt.EmitActivityConfigChangeCtx(context.Background(), "server_updated", "github", []string{"enabled"}, nil, nil)

	select {
	case evt := <-ch:
		assert.Equal(t, "api", evt.Payload["source"], "no actor: the pre-#1698 default")
		_, has := evt.Payload["actor_kind"]
		assert.False(t, has, "an unknown caller is absent, never invented")
	case <-time.After(2 * time.Second):
		t.Fatal("no activity.config_change event")
	}
}

func TestEmitActivityQuarantineChangeCtx_CarriesCallerIdentity(t *testing.T) {
	rt := &Runtime{logger: zap.NewNop(), eventSubs: make(map[chan Event]struct{})}
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	ctx := auth.WithAuthContext(context.Background(), auth.AdminContext())
	ctx = reqcontext.WithRequestID(ctx, "req-7")
	rt.EmitActivityQuarantineChangeCtx(ctx, "sketchy", false, "Server unquarantined by administrator")

	select {
	case evt := <-ch:
		require.Equal(t, EventTypeActivityQuarantineChange, evt.Type)
		assert.Equal(t, "sketchy", evt.Payload["server_name"])
		assert.Equal(t, false, evt.Payload["quarantined"])
		assert.Equal(t, "api_key", evt.Payload["actor_kind"])
		assert.Equal(t, "api", evt.Payload["surface"])
		assert.Equal(t, "req-7", evt.Payload["request_id"])
	case <-time.After(2 * time.Second):
		t.Fatal("no activity.quarantine_change event")
	}
}

func TestActivityService_ConfigChangeRecordCarriesActor(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.handleEvent(Event{
		Type:      EventTypeActivityConfigChange,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"action": "server_removed", "affected_entity": "github", "source": "cli",
			"actor_kind": "socket", "actor_name": "", "surface": "cli", "request_id": "req-9",
		},
	})

	rec := onlyRecord(t, store)
	assert.Equal(t, storage.ActivityTypeConfigChange, rec.Type)
	assert.Equal(t, storage.ActivitySourceCLI, rec.Source)
	assert.Equal(t, "req-9", rec.RequestID)
	assert.Equal(t, "socket", rec.Metadata["actor_kind"])
	assert.Equal(t, "cli", rec.Metadata["surface"])
	_, hasName := rec.Metadata["actor_name"]
	assert.False(t, hasName, "an empty actor_name is omitted, as on profile_change rows")
}

func TestActivityService_QuarantineChangeRecordCarriesActor(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.handleEvent(Event{
		Type:      EventTypeActivityQuarantineChange,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "sketchy", "quarantined": false, "reason": "Server unquarantined by administrator",
			"actor_kind": "agent_token", "actor_name": "ops-bot", "surface": "api", "request_id": "req-3",
		},
	})

	rec := onlyRecord(t, store)
	assert.Equal(t, storage.ActivityTypeQuarantineChange, rec.Type)
	assert.Equal(t, "req-3", rec.RequestID)
	assert.Equal(t, "agent_token", rec.Metadata["actor_kind"])
	assert.Equal(t, "ops-bot", rec.Metadata["actor_name"])
	assert.Equal(t, "api", rec.Metadata["surface"])
}

func TestActivityService_LegacyEventsWithoutActorStillRecord(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.handleEvent(Event{
		Type:      EventTypeActivityQuarantineChange,
		Timestamp: time.Now().UTC(),
		Payload:   map[string]any{"server_name": "s", "quarantined": true, "reason": "r"},
	})

	rec := onlyRecord(t, store)
	_, has := rec.Metadata["actor_kind"]
	assert.False(t, has)
	assert.Empty(t, rec.RequestID)
}
