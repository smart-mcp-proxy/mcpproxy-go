package httpapi

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// T082 (Spec 107 US4): the /events door re-resolves a SESSION principal
// (cookie or bearer-JWT, never an agent token) before every identity-bearing
// frame, exactly as newSSECallerContextRefresher already does for an agent
// token (sse_scope.go:17-43). Contract (entitlement-predicate.md §4): "The
// SSE refresher ... calls the same resolver with the original (kind, value)
// before each frame; (nil, nil) or an error ends the stream."
//
// This file does not compile until T083 lands SetSessionPrincipalResolver on
// *Server (session_principal.go) — referenced below — and is behaviour-red
// (compiles, fails) until T085 wires that resolver into the SSE refresher.

// carolSessionResolverState is the mutable fixture a test drives to simulate
// live changes to Carol's entitlement between SSE frames (an admin un-sharing
// a server, or disabling her account) without reconnecting.
type carolSessionResolverState struct {
	mu       sync.Mutex
	allowed  []string // nil = resolver call not yet configured to a value
	disabled bool     // true => resolver reports the session invalid
	calls    int
}

func (s *carolSessionResolverState) setAllowed(names ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allowed = append([]string(nil), names...)
}

func (s *carolSessionResolverState) disable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disabled = true
}

func (s *carolSessionResolverState) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

const carolSessionToken = "carol-session-jwt"

// newCarolSessionResolver builds a SessionPrincipalResolver that answers only
// for the (bearer_jwt, carolSessionToken) credential the tests authenticate
// with, reading the live entitlement off state on every call — one call per
// SSE frame is exactly the assertion TestSSE_SessionPrincipalReResolvedPerFrame
// makes via callCount().
func newCarolSessionResolver(state *carolSessionResolverState) SessionPrincipalResolver {
	return func(_ *http.Request, kind auth.CredentialKind, value string) (*auth.AuthContext, error) {
		if kind != auth.CredentialKindBearerJWT || value != carolSessionToken {
			return nil, nil
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		state.calls++
		if state.disabled {
			return nil, fmt.Errorf("session principal disabled")
		}
		return &auth.AuthContext{
			Type:           auth.AuthTypeUser,
			UserID:         "carol",
			Email:          "carol@example.com",
			DisplayName:    "Carol",
			Provider:       "google",
			AllowedServers: append([]string(nil), state.allowed...),
			CredentialKind: auth.CredentialKindBearerJWT,
		}, nil
	}
}

// sseSubscribeBearer is sseSubscribe (sse_scope_reveal_test.go) with a bearer
// Authorization header instead of X-API-Key, for a session-principal caller.
func sseSubscribeBearer(t *testing.T, base, bearer string) (*bufio.Reader, func()) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/events", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req) //nolint:bodyclose // closed by the returned cleanup
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return bufio.NewReader(resp.Body), func() { resp.Body.Close() }
}

// TestSSE_SessionPrincipalReResolvedPerFrame proves the resolver is called
// again for each identity-bearing frame (not just once at connect time), and
// that a server un-shared mid-stream narrows the very next frame — the same
// live-narrowing guarantee #1166 gives an agent token, extended to a session
// principal.
func TestSSE_SessionPrincipalReResolvedPerFrame(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	state := &carolSessionResolverState{}
	state.setAllowed("alpha", "beta")
	srv.SetSessionPrincipalResolver(newCarolSessionResolver(state))

	ts := httptest.NewServer(srv)
	defer ts.Close()

	body, closeConn := sseSubscribeBearer(t, ts.URL, carolSessionToken)
	defer closeConn()

	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 1 }, 5*time.Second, 20*time.Millisecond,
		"precondition: the session-principal SSE connection must be subscribed")

	callsBeforeFirstFrame := state.callCount()
	require.GreaterOrEqual(t, callsBeforeFirstFrame, 1, "the resolver must run at least once before the initial status frame")

	// First frame: both alpha and beta are still allowed, so an alpha event
	// arrives.
	deadline := time.Now().Add(10 * time.Second)
	ctrl.publishToAll(internalRuntime.Event{
		Type:      internalRuntime.EventTypeActivityToolCallStarted,
		Payload:   map[string]any{"server_name": "alpha", "tool_name": "t"},
		Timestamp: time.Now(),
	})
	readSSEUntil(t, body, string(internalRuntime.EventTypeActivityToolCallStarted), deadline)
	callsAfterFirstFrame := state.callCount()
	require.Greater(t, callsAfterFirstFrame, callsBeforeFirstFrame,
		"the resolver must be called again for the second identity-bearing frame, not cached from connect time")

	// An admin un-shares beta: the next resolver call narrows Carol's
	// entitlement to alpha only. A beta-identified frame published afterwards
	// must be DROPPED, not delivered — proven by a guaranteed-received
	// sentinel (an alpha frame) arriving without the beta frame ever showing
	// up in between.
	state.setAllowed("alpha")

	deadline = time.Now().Add(10 * time.Second)
	ctrl.publishToAll(internalRuntime.Event{
		Type:      internalRuntime.EventTypeActivityToolCallStarted,
		Payload:   map[string]any{"server_name": "beta", "tool_name": "t"},
		Timestamp: time.Now(),
	})
	ctrl.publishToAll(internalRuntime.Event{
		Type:      internalRuntime.EventTypeActivityPolicyDecision,
		Payload:   map[string]any{"server_name": "alpha", "tool_name": "t", "decision": "allowed"},
		Timestamp: time.Now(),
	})
	frames := readSSEFramesUntil(t, body, deadline, func(e sseEvent) bool {
		return e.Name == string(internalRuntime.EventTypeActivityPolicyDecision)
	})
	for _, f := range frames {
		if f.Name == string(internalRuntime.EventTypeActivityToolCallStarted) {
			payload, _ := f.Data["payload"].(map[string]interface{})
			t.Fatalf("beta was un-shared before this frame was published and must not reach Carol: %#v", payload)
		}
	}
}

// TestSSE_SessionPrincipalDisabledEndsStream proves that a resolver reporting
// the session principal invalid (account disabled, token revoked) closes the
// stream rather than delivering another frame silently degraded.
func TestSSE_SessionPrincipalDisabledEndsStream(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	state := &carolSessionResolverState{}
	state.setAllowed("alpha", "beta")
	srv.SetSessionPrincipalResolver(newCarolSessionResolver(state))

	ts := httptest.NewServer(srv)
	defer ts.Close()

	body, closeConn := sseSubscribeBearer(t, ts.URL, carolSessionToken)
	defer closeConn()

	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 1 }, 5*time.Second, 20*time.Millisecond,
		"precondition: the session-principal SSE connection must be subscribed")

	// Consume the initial status frame so the connection is past its opening
	// handshake before disabling Carol.
	readSSEUntil(t, body, "status", time.Now().Add(5*time.Second))

	state.disable()

	// Any subsequent event, or the heartbeat, forces the next refresh, which
	// now fails and must end the stream: the next read hits EOF rather than
	// ever decoding another frame.
	ctrl.publishToAll(internalRuntime.Event{
		Type:      internalRuntime.EventTypeActivityToolCallStarted,
		Payload:   map[string]any{"server_name": "alpha", "tool_name": "t"},
		Timestamp: time.Now(),
	})

	deadline := time.Now().Add(10 * time.Second)
	sawEOF := false
	for time.Now().Before(deadline) {
		if _, err := body.ReadString('\n'); err != nil {
			sawEOF = true
			break
		}
	}
	require.True(t, sawEOF, "the SSE stream must close once the session principal's resolver reports it disabled")
}

// TestSSE_HeartbeatReResolvesSessionPrincipal pins cross-review round 2,
// chunk 3's P2 finding: the heartbeat ("ping") branch of the /events stream
// loop (server.go) never called refreshCallerContext(), unlike the status
// and runtime-event branches right next to it. FR-005 requires the session
// principal to be re-resolved "before each frame" specifically so "a
// disabled user's stream ends" without the caller having to reconnect — a
// heartbeat IS a frame, and on an otherwise-idle connection (no status
// update, no runtime event) it is the ONLY frame the server sends, so a
// disabled tenant's stream stayed open indefinitely, silently, as long as
// nothing else happened to trigger a refresh.
//
// The test shrinks sseHeartbeatInterval so a tick fires quickly, disables
// the session principal, and asserts the stream closes on the next
// heartbeat alone — no status update or runtime event is published.
//
// BITES: reverting the heartbeat case to skip refreshCallerContext() makes
// this test time out waiting for EOF (the stream stays open forever on
// heartbeats only).
func TestSSE_HeartbeatReResolvesSessionPrincipal(t *testing.T) {
	orig := sseHeartbeatInterval
	sseHeartbeatInterval = 50 * time.Millisecond
	t.Cleanup(func() { sseHeartbeatInterval = orig })

	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	state := &carolSessionResolverState{}
	state.setAllowed("alpha", "beta")
	srv.SetSessionPrincipalResolver(newCarolSessionResolver(state))

	ts := httptest.NewServer(srv)
	defer ts.Close()

	body, closeConn := sseSubscribeBearer(t, ts.URL, carolSessionToken)
	defer closeConn()

	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 1 }, 5*time.Second, 20*time.Millisecond,
		"precondition: the session-principal SSE connection must be subscribed")

	// Consume the initial status frame so the connection is past its opening
	// handshake before disabling Carol.
	readSSEUntil(t, body, "status", time.Now().Add(5*time.Second))

	state.disable()

	// No status update and no runtime event is published from here on: only
	// the (now fast) heartbeat ticks. The stream must still close.
	deadline := time.Now().Add(10 * time.Second)
	sawEOF := false
	for time.Now().Before(deadline) {
		if _, err := body.ReadString('\n'); err != nil {
			sawEOF = true
			break
		}
	}
	require.True(t, sawEOF, "the SSE stream must close on a heartbeat tick alone once the session "+
		"principal's resolver reports it disabled — the heartbeat branch must re-resolve the caller "+
		"context exactly like the status and runtime-event branches")
}
