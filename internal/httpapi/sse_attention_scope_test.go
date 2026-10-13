package httpapi

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// TestSSE_AttentionChangedRenderedPerSubscriber pins T055a (FR-006): an
// attention.changed whose items name alpha, beta and a client delivers every
// id to an admin subscriber, and only the alpha id (count 1) to a subscriber
// scoped to alpha — never the client id, never the beta id.
func TestSSE_AttentionChangedRenderedPerSubscriber(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	adminBody, adminClose := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer adminClose()
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()

	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 2 }, 5*time.Second, 20*time.Millisecond,
		"precondition: both SSE connections must be subscribed to the event bus")

	evt := internalRuntime.Event{
		Type: internalRuntime.EventTypeAttentionChanged,
		Payload: map[string]any{
			"count": 3,
			"items": []internalRuntime.AttentionEventItem{
				{ID: "sign_in_required:server:alpha", SubjectType: "server", SubjectID: "alpha"},
				{ID: "server_error:server:beta", SubjectType: "server", SubjectID: "beta"},
				{ID: "client_never_seen:client:codex", SubjectType: "client", SubjectID: "codex"},
			},
		},
		Timestamp: time.Now(),
	}

	var wg sync.WaitGroup
	var adminEvt, agentEvt sseEvent
	deadline := time.Now().Add(10 * time.Second)
	wg.Add(2)
	go func() { defer wg.Done(); adminEvt = readSSEUntil(t, adminBody, "attention.changed", deadline) }()
	go func() { defer wg.Done(); agentEvt = readSSEUntil(t, agentBody, "attention.changed", deadline) }()

	ctrl.publishToAll(evt)
	wg.Wait()

	adminPayload := adminEvt.Data["payload"].(map[string]interface{})
	adminIDs, _ := adminPayload["ids"].([]interface{})
	assert.ElementsMatch(t, []interface{}{
		"sign_in_required:server:alpha", "server_error:server:beta", "client_never_seen:client:codex",
	}, adminIDs, "the admin frame must carry every id")
	assert.Equal(t, float64(3), adminPayload["count"])

	agentPayload := agentEvt.Data["payload"].(map[string]interface{})
	agentIDs, _ := agentPayload["ids"].([]interface{})
	assert.Equal(t, []interface{}{"sign_in_required:server:alpha"}, agentIDs,
		"a token scoped to alpha must see only the alpha id, no client id")
	assert.Equal(t, float64(1), agentPayload["count"])
}

// TestSSE_AttentionChangedSuppressedWhenScopedSetUnchanged pins T055a's other
// half: a change that touches only a server the scoped subscriber cannot see
// must send that subscriber NO attention.changed frame — its narrowed id set
// (empty, then empty again) has not changed.
func TestSSE_AttentionChangedSuppressedWhenScopedSetUnchanged(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()

	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 1 }, 5*time.Second, 20*time.Millisecond)

	betaOnly := func(count int, id string) internalRuntime.Event {
		return internalRuntime.Event{
			Type: internalRuntime.EventTypeAttentionChanged,
			Payload: map[string]any{
				"count": count,
				"items": []internalRuntime.AttentionEventItem{
					{ID: id, SubjectType: "server", SubjectID: "beta"},
				},
			},
			Timestamp: time.Now(),
		}
	}

	// First beta-only change: the scoped subscriber's narrowed set goes from
	// nil (never sent) to empty — that IS a change (the first frame), so it
	// must still be delivered once, with an empty id list.
	ctrl.publishToAll(betaOnly(1, "server_error:server:beta"))
	first := readSSEUntil(t, agentBody, "attention.changed", time.Now().Add(5*time.Second))
	firstPayload := first.Data["payload"].(map[string]interface{})
	assert.Equal(t, float64(0), firstPayload["count"])
	ids, _ := firstPayload["ids"].([]interface{})
	assert.Empty(t, ids)

	// A second beta-only change (still narrows to empty for this subscriber)
	// must be suppressed: use a sentinel event guaranteed to arrive to prove
	// no attention.changed frame appeared in between.
	ctrl.publishToAll(betaOnly(1, "server_error:server:beta:again"))
	ctrl.publishToAll(internalRuntime.Event{
		Type:      internalRuntime.EventTypeOAuthTokenRefreshed,
		Payload:   map[string]any{"server_name": "alpha", "expires_at": time.Now().Format(time.RFC3339)},
		Timestamp: time.Now(),
	})

	frames := readSSEFramesUntil(t, agentBody, time.Now().Add(5*time.Second), func(e sseEvent) bool {
		return e.Name == "oauth.token_refreshed"
	})
	for _, f := range frames {
		assert.NotEqual(t, "attention.changed", f.Name,
			"an unchanged (still empty) narrowed set must not re-send a frame")
	}
}
