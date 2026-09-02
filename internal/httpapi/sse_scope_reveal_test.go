package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
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

// readSSEFramesUntil consumes the live SSE body and returns EVERY frame it saw
// up to and including the first one `stop` accepts.
//
// readSSEUntil skips past frames it is not waiting for, which is fine for
// asserting the shape of an event that DID arrive and useless for asserting
// that one did not. Proving a drop needs the whole run of frames between a
// publish and a sentinel the subscriber is guaranteed to receive.
func readSSEFramesUntil(t *testing.T, body *bufio.Reader, deadline time.Time, stop func(sseEvent) bool) []sseEvent {
	t.Helper()
	var frames []sseEvent
	var name string
	for time.Now().Before(deadline) {
		line, err := body.ReadString('\n')
		if err != nil {
			t.Fatalf("SSE stream ended before the sentinel arrived (frames so far: %d): %v", len(frames), err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var decoded map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &decoded))
			frame := sseEvent{Name: name, Data: decoded}
			frames = append(frames, frame)
			if stop(frame) {
				return frames
			}
		}
	}
	t.Fatalf("timed out waiting for the sentinel frame (frames so far: %d)", len(frames))
	return nil
}

// sseIdentityEventFixtures is THE enumerated class this change closes: one
// event per runtime event type that carries a server identity to an SSE
// subscriber, each naming `server`, each with the payload shape its producer in
// internal/runtime actually builds.
//
// The first entry is the frame observed live on a scoped subscriber's stream
// while an admin watched the same stream and `beta` was disabled — the gap the
// servers.changed-only filter missed.
func sseIdentityEventFixtures(server string) []internalRuntime.Event {
	now := time.Now()
	evt := func(t internalRuntime.EventType, payload map[string]any) internalRuntime.Event {
		return internalRuntime.Event{Type: t, Payload: payload, Timestamp: now}
	}
	return []internalRuntime.Event{
		// affected_entity
		evt(internalRuntime.EventTypeActivityConfigChange, map[string]any{
			"action":          "server_disabled",
			"affected_entity": server,
			"source":          "api",
			"changed_fields":  []string{"enabled"},
			"previous_values": map[string]any{"enabled": true},
			"new_values":      map[string]any{"enabled": false},
		}),
		// target_server
		evt(internalRuntime.EventTypeActivityInternalToolCall, map[string]any{
			"internal_tool_name": "call_tool_read",
			"target_server":      server,
			"target_tool":        "read_file",
			"status":             "ok",
		}),
		// server_name
		evt(internalRuntime.EventTypeActivityToolCallStarted, map[string]any{"server_name": server, "tool_name": "t"}),
		evt(internalRuntime.EventTypeActivityToolCallCompleted, map[string]any{"server_name": server, "tool_name": "t", "status": "ok"}),
		evt(internalRuntime.EventTypeActivityToolCallRejected, map[string]any{"server_name": server, "tool_name": "t", "reason": "queue_full"}),
		evt(internalRuntime.EventTypeActivityPolicyDecision, map[string]any{"server_name": server, "tool_name": "t", "decision": "blocked"}),
		evt(internalRuntime.EventTypeActivityQuarantineChange, map[string]any{"server_name": server, "quarantined": true}),
		evt(internalRuntime.EventTypeActivityToolQuarantineChange, map[string]any{"server_name": server, "tool_name": "t", "action": "pending"}),
		evt(internalRuntime.EventTypeActivityPromptGet, map[string]any{"server_name": server, "prompt_name": "p", "status": "ok"}),
		evt(internalRuntime.EventTypeOAuthTokenRefreshed, map[string]any{"server_name": server, "expires_at": now.Format(time.RFC3339)}),
		evt(internalRuntime.EventTypeOAuthRefreshFailed, map[string]any{"server_name": server, "error": "re-auth required"}),
		evt(internalRuntime.EventTypeSecurityScanSettled, map[string]any{"server_name": server, "status": "completed"}),
		evt(internalRuntime.EventTypeSecurityIntegrityAlert, map[string]any{"server_name": server, "alert_type": "hash_mismatch", "action": "quarantined"}),
	}
}

// sseAdminConfigEventFixtures are the config-document events: no server
// identity, but a live notification about the document GET /api/v1/config now
// answers 403 for an agent token.
func sseAdminConfigEventFixtures() []internalRuntime.Event {
	now := time.Now()
	return []internalRuntime.Event{
		{Type: internalRuntime.EventTypeConfigSaved, Payload: map[string]any{"path": "/home/operator/.mcpproxy/mcp_config.json"}, Timestamp: now},
		{Type: internalRuntime.EventTypeConfigReloaded, Payload: map[string]any{"path": "/home/operator/.mcpproxy/mcp_config.json"}, Timestamp: now},
		{Type: internalRuntime.EventTypeSecretsChanged, Payload: map[string]any{"operation": "set", "secret_name": "keyring:beta/api_key"}, Timestamp: now},
	}
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

	// ------------------------------------------------------------------
	// Part two: the rest of the class.
	//
	// servers.changed was one event type of twenty-odd. Live, with these same
	// two concurrent subscribers, disabling `beta` delivered its name, its
	// state and its mutation history to the scoped subscriber through
	// activity.config_change — a different type, the same door. Every type
	// that names a server is published here, in one burst, and the scoped
	// subscriber must learn nothing about `beta` from any of them while the
	// admin's stream stays complete.
	// ------------------------------------------------------------------
	betaEvents := sseIdentityEventFixtures("beta")
	adminOnly := sseAdminConfigEventFixtures()
	// An event about the server the token MAY see: the filter must be a scope
	// check, not a mute button.
	alphaProbe := internalRuntime.Event{
		Type:      internalRuntime.EventTypeActivityToolCallCompleted,
		Payload:   map[string]any{"server_name": "alpha", "tool_name": "alpha_probe", "status": "ok"},
		Timestamp: time.Now(),
	}
	// The sentinel both subscribers are guaranteed to receive, so "did not
	// arrive" is a decision rather than a race. It carries the coalescer extra
	// that named an out-of-scope server BESIDE a correctly narrowed embed.
	sentinelServers := scopeFixtureServers()
	for i := range sentinelServers {
		oauth.RedactServerSecretFields(&sentinelServers[i])
	}
	sentinelPayload := map[string]any{
		"reason":  "sentinel",
		"server":  "beta",
		"servers": sentinelServers,
		"stats":   &contracts.ServerStats{TotalServers: 2},
	}
	sentinel := internalRuntime.Event{
		Type:      internalRuntime.EventTypeServersChanged,
		Payload:   sentinelPayload,
		Timestamp: time.Now(),
	}

	isSentinel := func(e sseEvent) bool {
		if e.Name != string(internalRuntime.EventTypeServersChanged) {
			return false
		}
		inner, ok := e.Data["payload"].(map[string]interface{})
		return ok && inner["reason"] == "sentinel"
	}

	var adminFrames, agentFrames []sseEvent
	deadline = time.Now().Add(15 * time.Second)
	wg.Add(2)
	go func() { defer wg.Done(); adminFrames = readSSEFramesUntil(t, adminBody, deadline, isSentinel) }()
	go func() { defer wg.Done(); agentFrames = readSSEFramesUntil(t, agentBody, deadline, isSentinel) }()

	for _, e := range betaEvents {
		ctrl.publishToAll(e)
	}
	for _, e := range adminOnly {
		ctrl.publishToAll(e)
	}
	ctrl.publishToAll(alphaProbe)
	ctrl.publishToAll(sentinel)
	wg.Wait()

	frameNames := func(frames []sseEvent) []string {
		out := make([]string, 0, len(frames))
		for _, f := range frames {
			out = append(out, f.Name)
		}
		return out
	}
	adminNames := frameNames(adminFrames)
	agentNames := frameNames(agentFrames)

	// The admin's stream is complete: a per-subscriber drop must not remove
	// the frame from the shared bus.
	for _, e := range betaEvents {
		assert.Contains(t, adminNames, string(e.Type),
			"the admin subscriber must still receive every event about beta")
	}
	for _, e := range adminOnly {
		assert.Contains(t, adminNames, string(e.Type),
			"the admin subscriber must still receive the config-document events")
	}

	// The scoped stream learns nothing about a server it may not enumerate —
	// not the name, not the mutation, not the fact that a frame existed.
	agentRaw, err := json.Marshal(agentFrames)
	require.NoError(t, err)
	assert.NotContains(t, string(agentRaw), "beta",
		"a token scoped to alpha must not learn beta exists from ANY event type: %s", string(agentRaw))
	for _, e := range betaEvents {
		// activity.tool_call.completed is deliberately excluded: the alpha
		// probe below has the same TYPE, so absence-by-type would be an
		// assertion about the wrong thing. The exact-sequence assertion that
		// follows covers it, and covers it harder.
		if e.Type == alphaProbe.Type {
			continue
		}
		assert.NotContains(t, agentNames, string(e.Type),
			"%s named beta and must be dropped for the scoped subscriber, not redacted into an empty-shell frame", e.Type)
	}
	for _, e := range adminOnly {
		assert.NotContains(t, agentNames, string(e.Type),
			"%s announces a mutation of the admin config document, which GET /api/v1/config answers 403 for this caller", e.Type)
	}

	// The scoped subscriber's stream over this burst, exactly: the one event
	// about the server it MAY see, then the sentinel. Nothing dropped that
	// should have arrived, nothing arrived that should have been dropped —
	// including a same-type frame about beta hiding behind the alpha probe.
	assert.Equal(t,
		[]string{string(alphaProbe.Type), string(internalRuntime.EventTypeServersChanged)},
		agentNames,
		"the scoped subscriber must receive exactly the alpha event and the sentinel")
	assert.Contains(t, string(agentRaw), "alpha_probe",
		"the filter must be a scope check, not a mute button")

	// The sentinel: the coalescer extra is removed for the scoped subscriber
	// and intact for the admin. servers.changed is never dropped, because it
	// is coalesced last-write-wins and dropping on the winning marker's name
	// would strand the scoped subscriber's view of alpha.
	sentinelPayloadOf := func(frames []sseEvent) map[string]interface{} {
		last := frames[len(frames)-1]
		require.True(t, isSentinel(last), "the last frame collected must be the sentinel")
		return last.Data["payload"].(map[string]interface{})
	}
	assert.Equal(t, "beta", sentinelPayloadOf(adminFrames)["server"],
		"the admin's servers.changed extras must be untouched by the scoped subscriber's rendering")
	assert.NotContains(t, sentinelPayloadOf(agentFrames), "server",
		"the coalescer extra naming an out-of-scope server must be removed, not merely left beside a narrowed embed")

	// The bus's own map, once more, after the second burst.
	assert.Equal(t, "beta", sentinelPayload["server"],
		"the shared payload must never be edited in place")
	assert.Len(t, sentinelPayload["servers"].([]contracts.Server), 2,
		"the shared payload must never be edited in place")
}

// TestSSE_ScopedSubscriberGetsNotifyOnlyEventWithoutTheName pins the notify-only
// path of servers.changed — the branch where ListServers failed upstream, so
// there is no embed to narrow and the coalescer extra IS the whole payload.
//
// The original filter returned that payload verbatim ("nothing to scope"),
// which is precisely when the name was the only thing in it.
func TestSSE_ScopedSubscriberGetsNotifyOnlyEventWithoutTheName(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()
	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 1 }, 5*time.Second, 20*time.Millisecond)

	payload := map[string]any{"reason": "server_disconnected", "server": "beta"}
	ctrl.publishToAll(internalRuntime.Event{
		Type:      internalRuntime.EventTypeServersChanged,
		Payload:   payload,
		Timestamp: time.Now(),
	})

	got := readSSEUntil(t, agentBody, "servers.changed", time.Now().Add(10*time.Second))
	inner, ok := got.Data["payload"].(map[string]interface{})
	require.True(t, ok, "no payload in %#v", got.Data)
	assert.Equal(t, "server_disconnected", inner["reason"],
		"the notify-only event still reaches the scoped subscriber; only the name is withheld")
	assert.NotContains(t, inner, "server",
		"a notify-only servers.changed must not carry the name of a server the caller cannot enumerate")
	assert.Equal(t, "beta", payload["server"], "the shared payload must never be edited in place")
}

// TestSSE_EveryRuntimeEventTypeIsClassified reads the event-type constants out
// of internal/runtime/events.go and requires each one to be classified here.
//
// The fix scopes on payload KEY, not on event type, so a new producer that
// names a server through server_name / server / target_server / affected_entity
// is covered the day it lands. This test covers the other half: a new event
// type that invents a NEW key name would slip through silently, so adding one
// fails here until someone puts it in a list and, in doing so, looks at whether
// it names a server.
func TestSSE_EveryRuntimeEventTypeIsClassified(t *testing.T) {
	// Types that carry a server identity, exercised end-to-end over the wire
	// by TestSSE_ServersChangedRenderedPerSubscriber.
	carriesServerIdentity := map[string]bool{}
	for _, e := range sseIdentityEventFixtures("beta") {
		carriesServerIdentity[string(e.Type)] = true
	}

	// Types that carry NO server identity. Each is either rendered per
	// subscriber (servers.changed), dropped as an admin config document, or
	// delivered unchanged because there is nothing in it to scope.
	noServerIdentity := map[string]string{
		"servers.changed":          "rendered per subscriber, never dropped: coalesced last-write-wins",
		"config.reloaded":          "admin config document — dropped for a scoped caller",
		"config.saved":             "admin config document — dropped for a scoped caller",
		"secrets.changed":          "admin config document — dropped for a scoped caller",
		"active_profile.changed":   "profile slug only",
		"upstream.prompts_changed": "nil payload",
		"sensitive_data.detected":  "activity id + severity counts, no server name",
		"security.scanner_changed": "scanner plugin id, not a server",
		"security.scan_started":    "never published (no-op producer)",
		"security.scan_progress":   "never published (no-op producer)",
		"security.scan_completed":  "folded into security.scan_settled by the debouncer",
		"security.scan_failed":     "folded into security.scan_settled by the debouncer",
		"activity.system.start":    "version / listen address / config path, no server name",
		"activity.system.stop":     "reason / signal / uptime, no server name",
	}

	src, err := os.ReadFile(filepath.Join("..", "runtime", "events.go"))
	require.NoError(t, err, "the event-type constants must be readable for this guard to mean anything")

	re := regexp.MustCompile(`(?m)^\s*EventType\w+\s+EventType\s*=\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(src), -1)
	require.NotEmpty(t, matches, "no event-type constants parsed — the guard would pass vacuously")

	for _, m := range matches {
		name := m[1]
		_, classifiedAsNoIdentity := noServerIdentity[name]
		assert.True(t, carriesServerIdentity[name] || classifiedAsNoIdentity,
			"runtime event %q is not classified for the /events door: add it to sseIdentityEventFixtures if it names a server "+
				"(server_name / server / target_server / affected_entity), or to noServerIdentity with the reason it cannot", name)
	}
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
