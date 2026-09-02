package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// sseEvent is one decoded `event:`/`data:` pair off the wire.
type sseEvent struct {
	Name string
	Data map[string]interface{}
}

// readSSEUntil consumes the live SSE body until `want` arrives or the deadline
// passes. It drives the real handler over a real http.Client against a real
// httptest.Server, so the flusher, the per-connection subscription and the
// production route table are all genuine.
func readSSEUntil(t *testing.T, body *bufio.Reader, want string, deadline time.Time) sseEvent {
	t.Helper()
	var name string
	for time.Now().Before(deadline) {
		line, err := body.ReadString('\n')
		if err != nil {
			t.Fatalf("SSE stream ended before %q arrived: %v", want, err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if name != want {
				continue
			}
			var decoded map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &decoded))
			return sseEvent{Name: name, Data: decoded}
		}
	}
	t.Fatalf("timed out waiting for SSE event %q", want)
	return sseEvent{}
}

func sseSubscribe(t *testing.T, base, apiKey string) (*bufio.Reader, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", apiKey)
	resp, err := http.DefaultClient.Do(req) //nolint:bodyclose // closed by the returned cleanup
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return bufio.NewReader(resp.Body), func() {
		cancel()
		resp.Body.Close()
	}
}

// TestSSE_ServersChangedRenderedPerSubscriber is the sharpest test in this
// change. It runs an ADMIN and a SCOPED agent as CONCURRENT subscribers to one
// live /events stream and fans the same Event value (same map pointer, same
// slice backing array) out to both, exactly as runtime.publishEvent does.
//
// It pins three things at once:
//
//  1. #1166 — the scoped subscriber's servers.changed embed carries only the
//     server it may enumerate, with `stats` recomputed.
//  2. #1167 — neither subscriber receives a raw credential; the bus masks this
//     payload unconditionally because it has no caller to gate against.
//  3. The admin's view is NOT corrupted by the scoped subscriber's rendering.
//     Under `-race` a shared-map write here would also be an unsynchronised
//     write concurrent with the other goroutine's json.Marshal.
func TestSSE_ServersChangedRenderedPerSubscriber(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	adminBody, adminClose := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer adminClose()
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()

	// Both handlers must have subscribed before the event is published, or one
	// of them silently misses it and the test passes for the wrong reason.
	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 2 }, 5*time.Second, 20*time.Millisecond,
		"precondition: both SSE connections must be subscribed to the event bus")

	// Build the payload exactly as runtime.buildServersChangedPayload does:
	// masked ONCE, ONE map and ONE slice, shared by every subscriber.
	shared := scopeFixtureServers()
	for i := range shared {
		oauth.RedactServerSecretFields(&shared[i])
	}
	stats := &contracts.ServerStats{TotalServers: 2, ConnectedServers: 2, TotalTools: 10}
	payload := map[string]any{"reason": "test", "servers": shared, "stats": stats}
	evt := internalRuntime.Event{
		Type:      internalRuntime.EventTypeServersChanged,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	var wg sync.WaitGroup
	var adminEvt, agentEvt sseEvent
	deadline := time.Now().Add(10 * time.Second)
	wg.Add(2)
	go func() { defer wg.Done(); adminEvt = readSSEUntil(t, adminBody, "servers.changed", deadline) }()
	go func() { defer wg.Done(); agentEvt = readSSEUntil(t, agentBody, "servers.changed", deadline) }()

	ctrl.publishToAll(evt)
	wg.Wait()

	names := func(e sseEvent) []string {
		inner, ok := e.Data["payload"].(map[string]interface{})
		require.True(t, ok, "no payload in %#v", e.Data)
		raw, ok := inner["servers"].([]interface{})
		require.True(t, ok, "no servers embed in %#v", inner)
		out := make([]string, 0, len(raw))
		for _, entry := range raw {
			m, _ := entry.(map[string]interface{})
			n, _ := m["name"].(string)
			out = append(out, n)
		}
		return out
	}

	assert.ElementsMatch(t, []string{"alpha", "beta"}, names(adminEvt),
		"the admin's servers.changed embed must be untouched by the scoped subscriber's rendering")
	assert.Equal(t, []string{"alpha"}, names(agentEvt),
		"a token scoped to alpha must not receive beta over /events")

	agentPayload := agentEvt.Data["payload"].(map[string]interface{})
	agentStats, ok := agentPayload["stats"].(map[string]interface{})
	require.True(t, ok, "no stats in %#v", agentPayload)
	assert.Equal(t, float64(1), agentStats["total_servers"], "the embed's stats must narrow with the array")

	// The event bus's own map must be intact after both renders.
	assert.Len(t, payload["servers"].([]contracts.Server), 2,
		"the shared payload must never be edited in place")
	assert.Equal(t, 2, stats.TotalServers, "the shared stats struct must never be edited in place")
}

// TestSSE_ScopedSubscriberGetsNoRawSecretEvenWithRevealOn pins the #1167 half
// of the /events door: the live reproduction showed a scoped read-only token
// receiving all four credential classes in the servers.changed payload.
//
// It also pins the notify-only DEGRADE for a caller who MAY see raw values.
// The bus masks unconditionally, so handing an admin the masked embed while
// GET /api/v1/servers hands them the real one would make the Web UI's
// authoritative merge flicker; the embed is dropped instead and the client
// re-fetches through the gated REST door.
func TestSSE_ScopedSubscriberGetsNoRawSecretEvenWithRevealOn(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(true), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	adminBody, adminClose := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer adminClose()
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()

	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 2 }, 5*time.Second, 20*time.Millisecond)

	shared := scopeFixtureServers()
	for i := range shared {
		oauth.RedactServerSecretFields(&shared[i])
	}
	evt := internalRuntime.Event{
		Type:      internalRuntime.EventTypeServersChanged,
		Payload:   map[string]any{"reason": "test", "servers": shared, "stats": &contracts.ServerStats{TotalServers: 2}},
		Timestamp: time.Now(),
	}

	var wg sync.WaitGroup
	var adminEvt, agentEvt sseEvent
	deadline := time.Now().Add(10 * time.Second)
	wg.Add(2)
	go func() { defer wg.Done(); adminEvt = readSSEUntil(t, adminBody, "servers.changed", deadline) }()
	go func() { defer wg.Done(); agentEvt = readSSEUntil(t, agentBody, "servers.changed", deadline) }()

	ctrl.publishToAll(evt)
	wg.Wait()

	agentPayload := agentEvt.Data["payload"].(map[string]interface{})
	require.Contains(t, agentPayload, "servers", "precondition: the scoped subscriber still receives an embed")
	agentRaw, err := json.Marshal(agentPayload)
	require.NoError(t, err)
	for _, secret := range []string{alphaHeaderSecret, alphaQuerySecret, betaArgvSecret, betaEnvSecret} {
		assert.NotContains(t, string(agentRaw), secret,
			"reveal_secret_headers must not open the /events door for a scoped token")
	}

	adminPayload := adminEvt.Data["payload"].(map[string]interface{})
	assert.NotContains(t, adminPayload, "servers",
		"a subscriber who MAY see raw values gets the notify-only shape, so it re-fetches through the gated REST door instead of merging a masked embed")
	assert.NotContains(t, adminPayload, "stats")
	assert.Equal(t, "test", adminPayload["reason"], "the notify-only degrade must keep the rest of the payload")
}
