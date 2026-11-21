package httpapi

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
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
	readSSEUntil(t, adminBody, "attention.changed", time.Now().Add(5*time.Second))
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()
	readSSEUntil(t, agentBody, "attention.changed", time.Now().Add(5*time.Second))

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
	// Nothing needs attention when the subscriber connects: its narrowed set
	// at connect is empty (Spec 125 review round 1 seeds the suppression with it).
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true,
		attentionItemsOverride: []contracts.AttentionItem{}}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()
	readSSEUntil(t, agentBody, "attention.changed", time.Now().Add(5*time.Second))

	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 1 }, 5*time.Second, 20*time.Millisecond)

	sentinel := func() {
		ctrl.publishToAll(internalRuntime.Event{
			Type:      internalRuntime.EventTypeOAuthTokenRefreshed,
			Payload:   map[string]any{"server_name": "alpha", "expires_at": time.Now().Format(time.RFC3339)},
			Timestamp: time.Now(),
		})
	}
	attentionFrames := func() int {
		frames := readSSEFramesUntil(t, agentBody, time.Now().Add(5*time.Second), func(e sseEvent) bool {
			return e.Name == "oauth.token_refreshed"
		})
		n := 0
		for _, f := range frames {
			if f.Name == "attention.changed" {
				n++
			}
		}
		return n
	}

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

	// A beta-only change narrows to the empty set the subscriber had at
	// connect: no frame (a sentinel guaranteed to arrive bounds the wait).
	ctrl.publishToAll(betaOnly(1, "server_error:server:beta"))
	sentinel()
	assert.Zero(t, attentionFrames(), "a narrowed set equal to the one at connect must not send a frame")

	// A change the subscriber can see is delivered.
	ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeAttentionChanged, Timestamp: time.Now(),
		Payload: map[string]any{"count": 1, "items": []internalRuntime.AttentionEventItem{
			{ID: "server_error:server:alpha", SubjectType: "server", SubjectID: "alpha"},
		}}})
	evt := readSSEUntil(t, agentBody, "attention.changed", time.Now().Add(5*time.Second))
	assert.Equal(t, []interface{}{"server_error:server:alpha"}, evt.Data["payload"].(map[string]interface{})["ids"])

	// A second beta-only change (alpha's item gone again: the set moves to
	// empty) is delivered once, then the same empty set again is suppressed.
	ctrl.publishToAll(betaOnly(1, "server_error:server:beta"))
	sentinel()
	assert.Equal(t, 1, attentionFrames())
	ctrl.publishToAll(betaOnly(1, "server_error:server:beta:again"))
	sentinel()
	assert.Zero(t, attentionFrames(), "an unchanged (still empty) narrowed set must not re-send a frame")
}
