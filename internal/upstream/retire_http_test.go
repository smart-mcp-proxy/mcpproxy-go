package upstream

import (
	"context"
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
