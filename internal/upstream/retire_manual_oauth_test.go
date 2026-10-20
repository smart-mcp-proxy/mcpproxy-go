package upstream

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

// Spec 116 PR4, Sol review round 3 (FR-027) and T4.4: a manual OAuth login
// or a refresh on an instance retired while it was on its way returns the
// mapped "server not found", sends nothing to the upstream, opens no
// browser and does not fall back to a reconnect.

// oauthUpstreamCounter answers every request with an OAuth challenge and
// counts them.
func oauthUpstreamCounter(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="http://`+r.Host+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func newManualOAuthManager(t *testing.T, url string) *Manager {
	t.Helper()
	t.Setenv("CI", "")
	t.Setenv("HEADLESS", "")
	m := NewManager(zap.NewNop(), &config.Config{}, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })
	require.NoError(t, m.AddServerConfig("srv", &config.ServerConfig{
		Name: "srv", URL: url, Protocol: "http", Enabled: true,
		OAuth: &config.OAuthConfig{ClientID: "cid", ClientSecret: "cs-secret"},
	}))
	return m
}

// countBrowserLaunches counts browser launches for srv and starts none.
func countBrowserLaunches(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	restoreStart := core.SetBrowserStartForTest(func(*exec.Cmd) error { return nil })
	restoreObs := core.SetLaunchObserverForTest(func(server, kind string) {
		if server == "srv" && kind == "browser" {
			n.Add(1)
		}
	})
	t.Cleanup(func() { restoreObs(); restoreStart() })
	return &n
}

// parkGateOnce parks the instance gate's first admitted section.
func parkGateOnce(g *dialgate.Gate) (parked <-chan struct{}, release func()) {
	p := make(chan struct{})
	r := make(chan struct{})
	var once atomic.Bool
	g.SetHooks(&dialgate.Hooks{BeforeAdmit: func() {
		if once.CompareAndSwap(false, true) {
			close(p)
			<-r
		}
	}})
	var rel atomic.Bool
	return p, func() {
		if rel.CompareAndSwap(false, true) {
			close(r)
		}
	}
}

func TestManualOAuth_RetiredDuringLogin_MappedNotFound(t *testing.T) {
	entries := map[string]func(m *Manager) error{
		"StartManualOAuthQuick": func(m *Manager) error {
			_, err := m.StartManualOAuthQuick("srv")
			return err
		},
		"StartManualOAuthWithInfo": func(m *Manager) error {
			_, err := m.StartManualOAuthWithInfo("srv", false)
			return err
		},
		"StartManualOAuthWithInfo(force)": func(m *Manager) error {
			_, err := m.StartManualOAuthWithInfo("srv", true)
			return err
		},
	}
	for name, run := range entries {
		t.Run(name, func(t *testing.T) {
			srv, hits := oauthUpstreamCounter(t)
			browsers := countBrowserLaunches(t)
			m := newManualOAuthManager(t, srv.URL+"/mcp")
			old, ok := m.GetClient("srv")
			require.True(t, ok)
			parked, release := parkGateOnce(old.Gate())
			defer release()

			done := make(chan error, 1)
			go func() { done <- run(m) }()
			select {
			case <-parked:
			case <-time.After(10 * time.Second):
				t.Fatal("the login never reached the instance gate")
			}
			m.RemoveServer("srv") // retires the instance
			require.True(t, old.Retired())
			release()

			var err error
			select {
			case err = <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("the login did not return")
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "server not found")
			assert.NotContains(t, strings.ToLower(err.Error()), "retired", "the internal retirement error never reaches a surface")
			time.Sleep(200 * time.Millisecond)
			assert.Zero(t, hits.Load(), "a retired login sends nothing upstream")
			assert.Zero(t, browsers.Load(), "a retired login opens no browser")
		})
	}
}

// T4.4: a refresh whose instance is retired after the manager looked it up
// returns "server not found", sends no token request and does not fall
// back to a reconnect.
func TestRefreshOAuthToken_RetiredAfterLookup_NotFoundNoFallback(t *testing.T) {
	srv, hits := oauthUpstreamCounter(t)
	m := newManualOAuthManager(t, srv.URL+"/mcp")
	old, ok := m.GetClient("srv")
	require.True(t, ok)

	prev := refreshOAuthAfterLookupHook
	refreshOAuthAfterLookupHook = func() { m.RemoveServer("srv") }
	t.Cleanup(func() { refreshOAuthAfterLookupHook = prev })

	err := m.RefreshOAuthToken("srv")
	require.Error(t, err)
	assert.Equal(t, "server not found: srv", err.Error())
	require.True(t, old.Retired())
	time.Sleep(300 * time.Millisecond)
	assert.Zero(t, hits.Load(), "no token request and no reconnect reach the upstream")
	assert.False(t, old.IsConnecting(), "no reconnect fallback on the retired instance")
}
