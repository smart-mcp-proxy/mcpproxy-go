package upstream

import (
	"context"
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
			sendStrayTraffic(t, up.srv.URL)

			release()
			require.Error(t, waitErr(t, errc))
			time.Sleep(100 * time.Millisecond)
			req, auth := up.counts()
			assert.Equal(t, reqBefore, req, "stray traffic on the port is not a request to the old endpoint")
			assert.Equal(t, authBefore, auth)
			assert.Zero(t, auth)
			assert.Len(t, up.strays(), 3, "the fixture saw the stray requests and kept them apart")
		})
	}
}

func sendStrayTraffic(t *testing.T, base string) {
	t.Helper()
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		var body io.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"mcpproxy-go","version":"1.0.0"},"capabilities":{}}}`)
		}
		req, err := http.NewRequest(method, base+"/", body)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}
}

// The fixture's controls: a request to the real endpoint is counted with or
// without credentials, this fixture's own credential is counted on any path
// (a misrouted credential is still caught), and origin-level OAuth paths are
// counted whoever sends them. A stranger's request to another path, with or
// without its own credential, is kept apart.
func TestCountingUpstream_CountsItsEndpointAndItsCredentialAnywhere(t *testing.T) {
	for _, kind := range []string{"http", "sse"} {
		t.Run(kind, func(t *testing.T) {
			up := newCountingUpstream(t, kind)
			do := func(method, url, auth string) {
				t.Helper()
				req, err := http.NewRequest(method, url, nil)
				require.NoError(t, err)
				if auth != "" {
					req.Header.Set("Authorization", auth)
				}
				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				_ = resp.Body.Close()
			}

			do(http.MethodDelete, up.url, "") // the endpoint, no credentials
			req, auth := up.counts()
			assert.Equal(t, int64(1), req, "a request to the endpoint without credentials is counted")
			assert.Zero(t, auth)

			do(http.MethodGet, up.srv.URL+"/elsewhere", fixtureBearer(up.url)) // own credential, wrong path
			req, auth = up.counts()
			assert.Equal(t, int64(2), req, "this fixture's credential on another path is counted")
			assert.Equal(t, int64(1), auth)

			do(http.MethodGet, up.srv.URL+"/.well-known/oauth-authorization-server", "") // origin-level OAuth discovery
			do(http.MethodGet, up.srv.URL+"/.well-known/oauth-protected-resource", "")
			do(http.MethodPost, up.srv.URL+"/token", "") // mcp-go's default endpoint without metadata
			req, auth = up.counts()
			assert.Equal(t, int64(5), req, "origin-level OAuth paths are counted whoever sends them (fail closed)")
			assert.Equal(t, int64(1), auth)

			do(http.MethodGet, up.srv.URL+"/", "Bearer someone-else") // a stranger with its own credential
			do(http.MethodPost, up.srv.URL+"/mcp", "")                // a stranger's endpoint path
			req, auth = up.counts()
			assert.Equal(t, int64(5), req, "strangers on the port are not counted")
			assert.Equal(t, int64(1), auth)
			assert.Equal(t, []string{"GET /", "POST /mcp"}, up.strays())
		})
	}
}
