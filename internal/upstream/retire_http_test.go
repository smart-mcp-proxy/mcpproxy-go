package upstream

import (
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

// Spec 116 PR4, T4.2 (US4 scenario 2): an HTTP or SSE instance parked before
// its first request (the headers-auth strategy's dial). A commit removes the
// server, replaces its endpoint or quarantines it, and returns; the instance
// is released. The old endpoint receives no request and no Authorization
// header. Parent (PR3): the parked instance had passed its connect-level
// check, so it sends `initialize` with the static bearer to the old endpoint.

func competingCommit(t *testing.T, m *Manager, op string, cfg *config.ServerConfig) {
	t.Helper()
	switch op {
	case "remove":
		m.AdvanceGenerations([]GenerationChange{{Name: cfg.Name, Present: false}})
	case "replace":
		moved := config.CopyServerConfig(cfg)
		moved.URL = "http://127.0.0.1:1/elsewhere"
		m.AdvanceGenerations([]GenerationChange{{Name: cfg.Name, Gen: 2, Present: true, Reconnect: true, Config: moved}})
	case "quarantine":
		q := config.CopyServerConfig(cfg)
		q.Quarantined = true
		m.AdvanceGenerations([]GenerationChange{{Name: cfg.Name, Gen: 2, Present: true, Reconnect: true, Config: q}})
	default:
		t.Fatalf("unknown op %s", op)
	}
}

func TestRetire_HTTPDialParked_OldEndpointReceivesNothing(t *testing.T) {
	for _, kind := range []string{"http", "sse"} {
		for _, op := range []string{"remove", "replace", "quarantine"} {
			t.Run(kind+"/"+op, func(t *testing.T) {
				up := newCountingUpstream(t, kind)
				m := newGenTestManager(t)
				cfg := httpServerConfig("acme", up.url, kind)
				m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
				require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
				inst := registered(t, m, cfg.Name)

				parked, release := parkFirstAdmit(inst)
				defer release()
				errc := connectAsync(inst)
				<-parked

				competingCommit(t, m, op, cfg)
				require.True(t, inst.Retired(), "the commit retired the instance before it returned")
				reqBefore, authBefore := up.counts()

				release()
				require.Error(t, waitErr(t, errc))
				time.Sleep(100 * time.Millisecond)
				req, auth := up.counts()
				assert.Equal(t, reqBefore, req, "no request reaches the old endpoint after the commit")
				assert.Equal(t, authBefore, auth, "no Authorization header reaches the old endpoint after the commit")
				assert.Zero(t, auth)
			})
		}
	}
}

// Control: with no competing commit the same instance connects and its
// requests (with the bearer) reach the fixture.
func TestRetire_HTTPControl_ConnectsAndCounts(t *testing.T) {
	for _, kind := range []string{"http", "sse"} {
		t.Run(kind, func(t *testing.T) {
			up := newCountingUpstream(t, kind)
			m := newGenTestManager(t)
			cfg := httpServerConfig("acme", up.url, kind)
			m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
			require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
			inst := registered(t, m, cfg.Name)
			t.Cleanup(func() { _ = inst.Disconnect() })
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			require.NoError(t, inst.Connect(ctx))
			req, auth := up.counts()
			assert.Positive(t, req)
			assert.Positive(t, auth)
		})
	}
}

// The exclusion variant: the instance's dial is held INSIDE its admitted
// section (connected, not yet registered). The commit's Retire waits on the
// barrier; after the release the connection is refused by track and closed,
// and no HTTP request reaches the endpoint.
func TestRetire_HTTPDialInsideSection_ClosedNoRequest(t *testing.T) {
	up := newCountingUpstream(t, "http")
	m := newGenTestManager(t)
	cfg := httpServerConfig("acme", up.url, "http")
	m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
	require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
	inst := registered(t, m, cfg.Name)

	inSection := make(chan struct{})
	resume := make(chan struct{})
	first := true
	inst.Gate().SetHooks(&dialgate.Hooks{BeforeTrack: func() {
		if first {
			first = false
			close(inSection)
			<-resume
		}
	}})
	errc := connectAsync(inst)
	<-inSection

	committed := make(chan struct{})
	go func() {
		competingCommit(t, m, "remove", cfg)
		close(committed)
	}()
	require.Eventually(t, inst.Gate().RetireWaiting, 5*time.Second, time.Millisecond)
	close(resume)
	select {
	case <-committed:
	case <-time.After(2 * time.Second):
		t.Fatal("Retire did not return after the section was released")
	}
	require.Error(t, waitErr(t, errc))
	time.Sleep(100 * time.Millisecond)
	req, auth := up.counts()
	assert.Zero(t, req)
	assert.Zero(t, auth)
}

// Cross-process isolation: `go test ./...` runs packages in parallel, and other
// packages' test clients keep reconnecting to the ports of their own closed
// fixtures (POST / initialize, GET / listen stream, DELETE / close, none with
// credentials). When the OS hands one of those ports to this fixture, that
// traffic arrives here (CI job 135134947938: 3 requests, 0 Authorization). It
// is not traffic to the old endpoint. The fixture counts only requests to its
// own endpoint, so the zero-request invariant stays exact while strangers on
// the same port are recorded apart.
func TestRetire_HTTPDialParked_StrayTrafficOnThePortIsNotTheOldEndpoint(t *testing.T) {
	for _, kind := range []string{"http", "sse"} {
		t.Run(kind, func(t *testing.T) {
			up := newCountingUpstream(t, kind)
			m := newGenTestManager(t)
			cfg := httpServerConfig("acme", up.url, kind)
			m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
			require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
			inst := registered(t, m, cfg.Name)

			parked, release := parkFirstAdmit(inst)
			defer release()
			errc := connectAsync(inst)
			<-parked

			competingCommit(t, m, "remove", cfg)
			require.True(t, inst.Retired())
			reqBefore, authBefore := up.counts()

			// A stranger on the same port, as another package's test client.
			// A real one may add more strays; only these are asserted.
			injected := sendStrayTraffic(t, up.srv.URL)

			release()
			require.Error(t, waitErr(t, errc))
			time.Sleep(100 * time.Millisecond)
			req, auth := up.counts()
			assert.Equal(t, reqBefore, req, "stray traffic on the port is not a request to the old endpoint")
			assert.Equal(t, authBefore, auth)
			assert.Zero(t, auth)
			assert.Subset(t, up.strays(), injected, "the fixture saw the stray requests and kept them apart")
		})
	}
}

// sendStrayTraffic sends what other packages' reconnecting test clients send
// to a reused port (POST / initialize, GET / listen stream, DELETE / close, no
// credentials), each marked with a unique query so the caller can find it
// among any real strays. It returns the strays() entries to expect.
func sendStrayTraffic(t *testing.T, base string) []string {
	t.Helper()
	mark := "qa-stray=" + rand.Text()
	var want []string
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		var body io.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"mcpproxy-go","version":"1.0.0"},"capabilities":{}}}`)
		}
		req, err := http.NewRequest(method, base+"/?"+mark, body)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		want = append(want, method+" /?"+mark)
	}
	return want
}

// The fixture's controls, one request at a time with its own counter delta:
// a request to the real endpoint is counted with or without credentials, this
// fixture's own credential is counted on any path (a misrouted credential is
// still caught), and origin-level OAuth paths are counted whoever sends them.
// A stranger's request to another path, with or without its own credential,
// is kept apart.
func TestCountingUpstream_CountsItsEndpointAndItsCredentialAnywhere(t *testing.T) {
	for _, kind := range []string{"http", "sse"} {
		t.Run(kind, func(t *testing.T) {
			up := newCountingUpstream(t, kind)
			own := fixtureBearer(up.url)
			require.Equal(t, own, httpServerConfig("acme", up.url, kind).Headers["Authorization"],
				"the instance carries the credential the fixture recognises")
			require.NotEqual(t, own, fixtureBearer(newCountingUpstream(t, kind).url), "credentials are unique per fixture")
			mark := "qa-control=" + rand.Text()
			cases := []struct {
				name, method, url, auth string
				counted, withAuth       bool
			}{
				{"endpoint, no credentials", http.MethodDelete, up.url, "", true, false},
				{"endpoint, own credential", http.MethodDelete, up.url, own, true, true},
				{"own credential, another path", http.MethodGet, up.srv.URL + "/elsewhere", own, true, true},
				{"own credential, stranger's path", http.MethodPost, up.srv.URL + "/mcp", own, true, true},
				{"well-known authorization server", http.MethodGet, up.srv.URL + "/.well-known/oauth-authorization-server", "", true, false},
				{"well-known protected resource", http.MethodGet, up.srv.URL + "/.well-known/oauth-protected-resource", "", true, false},
				{"default authorize endpoint", http.MethodGet, up.srv.URL + "/authorize", "", true, false},
				{"default token endpoint", http.MethodPost, up.srv.URL + "/token", "", true, false},
				{"default register endpoint", http.MethodPost, up.srv.URL + "/register", "", true, false},
				{"stranger, root", http.MethodGet, up.srv.URL + "/?" + mark + "-root", "", false, false},
				{"stranger with its own credential", http.MethodGet, up.srv.URL + "/?" + mark + "-cred", "Bearer someone-else", false, false},
				{"stranger, its own endpoint path", http.MethodPost, up.srv.URL + "/mcp?" + mark + "-mcp", "", false, false},
			}
			for _, tc := range cases {
				req0, auth0 := up.counts()
				req, err := http.NewRequest(tc.method, tc.url, nil)
				require.NoError(t, err)
				if tc.auth != "" {
					req.Header.Set("Authorization", tc.auth)
				}
				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				_ = resp.Body.Close()
				req1, auth1 := up.counts()
				wantReq, wantAuth := int64(0), int64(0)
				if tc.counted {
					wantReq = 1
				}
				if tc.withAuth {
					wantAuth = 1
				}
				assert.Equal(t, wantReq, req1-req0, "%s: request counted", tc.name)
				assert.Equal(t, wantAuth, auth1-auth0, "%s: Authorization counted", tc.name)
			}
			assert.Subset(t, up.strays(), []string{"GET /?" + mark + "-root", "GET /?" + mark + "-cred", "POST /mcp?" + mark + "-mcp"},
				"each stranger is recorded apart")
		})
	}
}
