package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	proxytransport "github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/launcher"
)

// Spec 116 PR4 core tests: T4.4 (refresh), T4.8 (stdio writes), T4.13
// (browser), T4.14 (launcher probe), T4.17 (wrapped transport consumers).

func newRetireCoreClient(t *testing.T, cfg *config.ServerConfig, st *storage.BoltDB) *Client {
	t.Helper()
	c, err := NewClient(cfg.Name, cfg, zap.NewNop(), nil, &config.Config{}, st, secret.NewResolver())
	require.NoError(t, err)
	return c
}

// parkFirst parks g's first admitted section before it takes the read lock.
func parkFirst(g *dialgate.Gate) (parked <-chan struct{}, release func()) {
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

// T4.4: a refresh with stored DCR credentials parked before it sends; the
// instance is retired; released, the token endpoint receives no refresh
// token and no client secret.
func TestRetire_StoredCredentialRefresh_NoTokenRequest(t *testing.T) {
	var tokenHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"a","token_type":"Bearer","expires_in":60}`)
	}))
	defer srv.Close()

	c := newRetireCoreClient(t, &config.ServerConfig{Name: "oauth-retire", URL: srv.URL + "/mcp", Protocol: "http"}, nil)
	parked, release := parkFirst(c.Gate())
	defer release()
	errc := make(chan error, 1)
	go func() {
		_, err := c.refreshTokenWithStoredCredentials(context.Background(), srv.URL+"/token",
			&storage.OAuthTokenRecord{RefreshToken: "rt-secret", ClientID: "cid", ClientSecret: "cs-secret"})
		errc <- err
	}()
	<-parked
	c.Retire()
	release()
	err := <-errc
	require.Error(t, err)
	assert.True(t, errors.Is(err, dialgate.ErrRetired), "got %v", err)
	assert.Zero(t, tokenHits.Load(), "no refresh token or client secret reaches /token")

	// The refresh function the coordinator runs refuses at once too.
	_, err = c.oauthRefreshFunc(nil)(context.Background(), &storage.OAuthTokenRecord{RefreshToken: "rt"})
	assert.ErrorIs(t, err, dialgate.ErrRetired)
}

// T4.13 (FR-025b): the OAuth browser launch is an admitted section.
func TestRetire_BrowserLaunchRefused(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("unsupported OS for openBrowser")
	}
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("xdg-open"); err != nil {
			t.Skip("xdg-open not available")
		}
	}
	var started atomic.Int64
	prevStart := browserStart
	browserStart = func(*exec.Cmd) error { started.Add(1); return nil }
	defer func() { browserStart = prevStart }()

	c := newRetireCoreClient(t, &config.ServerConfig{Name: "browser-retire", URL: "http://127.0.0.1:1/mcp", Protocol: "http"}, nil)
	require.NoError(t, c.openBrowser("http://127.0.0.1:1/authorize"))
	require.Equal(t, int64(1), started.Load(), "control: an open gate launches the browser")

	parked, release := parkFirst(c.Gate())
	defer release()
	errc := make(chan error, 1)
	go func() { errc <- c.openBrowser("http://127.0.0.1:1/authorize") }()
	<-parked
	c.Retire()
	release()
	err := <-errc
	require.Error(t, err)
	assert.ErrorIs(t, err, dialgate.ErrRetired)
	assert.Equal(t, int64(1), started.Load(), "no browser opens for a retired instance")
}

// T4.14: the launcher's reachability probe dials through the gate.
func TestRetire_LauncherProbeDialsNothing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	var accepts atomic.Int64
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			conn.Close()
		}
	}()
	url := "http://" + ln.Addr().String() + "/mcp"
	c := newRetireCoreClient(t, &config.ServerConfig{Name: "probe-retire", URL: url, Protocol: "http"}, nil)
	require.NoError(t, launcher.WaitForURLWithDial(context.Background(), url, time.Second, c.Gate().DialContext(launcherProbeDialer.DialContext)))
	require.Eventually(t, func() bool { return accepts.Load() == 1 }, time.Second, 10*time.Millisecond, "control: the probe dials")

	c.Retire()
	err = launcher.WaitForURLWithDial(context.Background(), url, 500*time.Millisecond, c.Gate().DialContext(launcherProbeDialer.DialContext))
	require.Error(t, err)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int64(1), accepts.Load(), "a retired instance's probe dials nothing")
}

// T4.8 (review R1.3): a launched stdio instance; Retire returns; a request
// or notification afterwards fails with ErrRetired, and the child's process
// group is dead.
func TestRetire_StdioWritesRefusedAndGroupKilled(t *testing.T) {
	if testing.Short() || runtime.GOOS == "windows" {
		t.Skip("builds and spawns the mcpfixture stdio binary (unix)")
	}
	bin := filepath.Join(t.TempDir(), "mcpfixture-retire")
	out, err := exec.Command("go", "build", "-o", bin, "github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpfixture").CombinedOutput()
	require.NoError(t, err, "build mcpfixture: %s", out)

	c := newRetireCoreClient(t, &config.ServerConfig{Name: "stdio-retire", Command: bin, Args: []string{"--transport", "stdio"}, Protocol: "stdio", Enabled: true}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })
	_, err = c.ListTools(ctx)
	require.NoError(t, err, "control: the instance works before retirement")
	c.mu.RLock()
	pgid := c.processGroupID
	cl := c.client
	c.mu.RUnlock()
	require.Positive(t, pgid)

	start := time.Now()
	c.Retire()
	assert.Less(t, time.Since(start), time.Second)

	_, err = cl.ListTools(ctx, mcp.ListToolsRequest{})
	assert.ErrorIs(t, err, dialgate.ErrRetired)
	err = cl.GetTransport().SendNotification(ctx, mcp.JSONRPCNotification{JSONRPC: mcp.JSONRPC_VERSION, Notification: mcp.Notification{Method: "notifications/initialized"}})
	assert.ErrorIs(t, err, dialgate.ErrRetired)
	require.Eventually(t, func() bool { return !groupAlive(pgid) }, 2*time.Second, 20*time.Millisecond,
		"the process group is killed by Retire")
}

// groupAlive reports whether a non-zombie process is in process group pgid.
func groupAlive(pgid int) bool {
	out, err := exec.Command("ps", "-axo", "pgid=,stat=").Output()
	if err != nil {
		return false
	}
	want := fmt.Sprint(pgid)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == want && !strings.HasPrefix(f[1], "Z") {
			return true
		}
	}
	return false
}

// T4.17 (review R8.1): the production wrapped transports keep the
// capabilities core and mcp-go assert on.
func TestWrappedTransportConsumers(t *testing.T) {
	var protoHeader atomic.Value
	mcpSrv := server.NewMCPServer("consumers", "1.0.0", server.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	inner := server.NewStreamableHTTPServer(mcpSrv)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("Mcp-Protocol-Version"); v != "" {
			protoHeader.Store(v)
		}
		inner.ServeHTTP(w, r)
	}))
	defer srv.Close()

	t.Run("oauth handler extraction (streamable HTTP and SSE)", func(t *testing.T) {
		c := newRetireCoreClient(t, &config.ServerConfig{Name: "consumers-oauth", URL: srv.URL + "/mcp", Protocol: "http"}, nil)
		oc := &client.OAuthConfig{ClientID: "x", TokenStore: client.NewMemoryTokenStore()}
		for _, mk := range []func() (*client.Client, error){
			func() (*client.Client, error) { return createHTTP(c, oc) },
			func() (*client.Client, error) { return createSSE(c, oc) },
		} {
			cl, err := mk()
			require.NoError(t, err)
			_, wrapped := cl.GetTransport().(dialgate.Unwrapper)
			require.True(t, wrapped, "core installs the gated wrapper")
			assert.NotNil(t, extractOAuthHandler(cl), "the OAuth handler is found through the wrapper")
		}
	})

	t.Run("session snapshot and protocol header over the wrapped streamable transport", func(t *testing.T) {
		c := newRetireCoreClient(t, &config.ServerConfig{Name: "consumers-session", URL: srv.URL + "/mcp", Protocol: "http"}, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, c.Connect(ctx))
		t.Cleanup(func() { _ = c.Disconnect() })
		id, _ := c.SessionSnapshot()
		assert.NotEmpty(t, id, "SessionSnapshot reads the session through the wrapper")
		_, err := c.ListTools(ctx)
		require.NoError(t, err)
		v, _ := protoHeader.Load().(string)
		assert.NotEmpty(t, v, "Mcp-Protocol-Version is sent after negotiation (HTTPConnection forwarded)")
	})
}

func createHTTP(c *Client, oc *client.OAuthConfig) (*client.Client, error) {
	return createWith(c, oc, false)
}

func createSSE(c *Client, oc *client.OAuthConfig) (*client.Client, error) {
	return createWith(c, oc, true)
}

func createWith(c *Client, oc *client.OAuthConfig, sse bool) (*client.Client, error) {
	cfg := c.httpTransportConfig(c.config, oc)
	if sse {
		cfg.URL = strings.TrimSuffix(cfg.URL, "/mcp") + "/sse"
		return proxytransport.CreateSSEClient(cfg)
	}
	return proxytransport.CreateHTTPClient(cfg)
}
