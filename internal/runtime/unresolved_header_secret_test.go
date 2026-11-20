package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// fakeKeyring is an in-memory keyring provider; a missing name is an error,
// like the OS keychain's "secret not found".
type fakeKeyring struct {
	mu     sync.Mutex
	values map[string]string
}

func (k *fakeKeyring) CanResolve(t string) bool { return t == "keyring" }
func (k *fakeKeyring) IsAvailable() bool        { return true }
func (k *fakeKeyring) Resolve(_ context.Context, ref secret.Ref) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.values[ref.Name]
	if !ok {
		return "", errors.New("secret not found in keyring")
	}
	return v, nil
}
func (k *fakeKeyring) Store(_ context.Context, ref secret.Ref, v string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.values[ref.Name] = v
	return nil
}
func (k *fakeKeyring) Delete(context.Context, secret.Ref) error   { return nil }
func (k *fakeKeyring) List(context.Context) ([]secret.Ref, error) { return nil, nil }

// headerRecorder is a streamable-HTTP MCP upstream that records every request
// it receives and the headers that came with it.
type headerRecorder struct {
	next http.Handler
	mu   sync.Mutex
	seen []http.Header
}

func (h *headerRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.seen = append(h.seen, r.Header.Clone())
	h.mu.Unlock()
	h.next.ServeHTTP(w, r)
}

func (h *headerRecorder) requests() []http.Header {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]http.Header(nil), h.seen...)
}

func newHeaderRecordingUpstream(t *testing.T) (*headerRecorder, string) {
	t.Helper()
	srv := mcpserver.NewMCPServer("f9", "0.0.1", mcpserver.WithToolCapabilities(true))
	srv.AddTool(mcp.NewTool("echo", mcp.WithDescription("Echoes.")), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	rec := &headerRecorder{next: mcpserver.NewStreamableHTTPServer(srv)}
	ts := httptest.NewServer(rec)
	t.Cleanup(ts.Close)
	return rec, ts.URL
}

// Release QA F9: an unavailable ${keyring:...} in a configured upstream HTTP
// header was sent to the upstream verbatim, and health stayed green. A header,
// URL or OAuth field whose secret reference cannot be resolved must stop that
// server before any HTTP request, with an actionable missing-secret error;
// valid substitutions on the same server and unrelated servers are unaffected.
// After the secret is added, a restart of that server connects it with the
// resolved value. No secret value may reach the logs.
func TestUnresolvedHeaderSecret_RefusedBeforeAnyRequestThenRecoversOnRestart(t *testing.T) {
	t.Run("upstream restart", func(t *testing.T) {
		testUnresolvedHeaderSecret(t, func(rt *Runtime) error { return rt.RestartServer("f9-secret") })
	})
	// Storing the secret through REST / the Web UI restarts the servers that
	// reference it; a header reference was not found by that scan (F9).
	t.Run("secret stored through the API", func(t *testing.T) {
		testUnresolvedHeaderSecret(t, func(rt *Runtime) error {
			return rt.NotifySecretsChanged(context.Background(), "store", "f9-missing")
		})
	})
}

func testUnresolvedHeaderSecret(t *testing.T, restart func(*Runtime) error) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	const presentValue = "f9-present-value-6b1e"
	const addedValue = "f9-added-later-value-93ac"

	secretUp, secretURL := newHeaderRecordingUpstream(t)
	otherUp, otherURL := newHeaderRecordingUpstream(t)

	secretCfg := &config.ServerConfig{
		Name: "f9-secret", URL: secretURL, Protocol: "streamable-http", Enabled: true,
		Headers: map[string]string{
			"Authorization": "Bearer ${keyring:f9-missing}",
			"X-Present":     "${keyring:f9-present}",
		},
	}
	otherCfg := &config.ServerConfig{Name: "f9-other", URL: otherURL, Protocol: "streamable-http", Enabled: true}

	core, logs := observer.New(zapcore.DebugLevel)
	quarantineOff := false // admission is not under test here
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", QuarantineEnabled: &quarantineOff,
		Servers: []*config.ServerConfig{secretCfg, otherCfg}}, "", zap.New(core))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	keyring := &fakeKeyring{values: map[string]string{"f9-present": presentValue}}
	rt.secretResolver.RegisterProvider("keyring", keyring)
	for _, c := range []*config.ServerConfig{secretCfg, otherCfg} {
		require.NoError(t, rt.storageManager.SaveUpstreamServer(c))
	}

	rt.StartBackgroundInitialization()

	// The unrelated server connects as usual.
	require.Eventually(t, func() bool {
		c, ok := rt.UpstreamManager().GetClient("f9-other")
		return ok && c.IsConnected()
	}, 15*time.Second, 50*time.Millisecond, "an unrelated server must connect")
	assert.NotEmpty(t, otherUp.requests())

	// The server with the missing secret reports it, and sent nothing.
	var status map[string]interface{}
	require.Eventually(t, func() bool {
		servers, err := rt.GetAllServers()
		if err != nil {
			return false
		}
		for _, s := range servers {
			if s["name"] == "f9-secret" {
				status = s
				le, _ := s["last_error"].(string)
				return strings.Contains(le, "f9-missing")
			}
		}
		return false
	}, 15*time.Second, 50*time.Millisecond, "the missing secret must be reported on the server (status %v; upstream saw %d requests, first Authorization %q)",
		status, len(secretUp.requests()), firstAuthorization(secretUp))
	c, ok := rt.UpstreamManager().GetClient("f9-secret")
	require.True(t, ok)
	assert.False(t, c.IsConnected())
	assert.Empty(t, secretUp.requests(), "no HTTP request may be sent with an unresolved secret reference")

	lastErr, _ := status["last_error"].(string)
	assert.Contains(t, lastErr, "${keyring:f9-missing}", "the error names the reference")
	assert.Contains(t, lastErr, "Headers[Authorization]", "the error names the field")
	assert.Contains(t, lastErr, "mcpproxy secrets set f9-missing", "the error says how to fix it")
	assert.NotContains(t, lastErr, presentValue, "a resolved value never appears in the error")
	h, ok := status["health"].(*contracts.HealthStatus)
	require.True(t, ok, "no health on the server status: %#v", status["health"])
	assert.Equal(t, "unhealthy", h.Level, "health is not green")
	assert.Equal(t, "Missing secret", h.Summary)
	assert.Equal(t, "f9-missing", h.Detail)
	assert.Equal(t, "set_secret", h.Action)

	// The secret is added later; a restart of that server picks it up.
	require.NoError(t, keyring.Store(context.Background(), secret.Ref{Type: "keyring", Name: "f9-missing"}, addedValue))
	require.NoError(t, restart(rt))
	require.Eventually(t, func() bool {
		c, ok := rt.UpstreamManager().GetClient("f9-secret")
		return ok && c.IsConnected()
	}, 15*time.Second, 50*time.Millisecond, "after the secret is added, a restart connects the server")

	reqs := secretUp.requests()
	require.NotEmpty(t, reqs)
	for _, h := range reqs {
		assert.Equal(t, "Bearer "+addedValue, h.Get("Authorization"), "the resolved header is sent")
		assert.Equal(t, presentValue, h.Get("X-Present"), "a valid substitution is preserved")
		assert.NotContains(t, h.Get("Authorization"), "${keyring:", "never the raw reference")
	}

	// No secret value reaches the logs.
	for _, e := range logs.All() {
		line := e.Message + " " + fmt.Sprint(e.ContextMap())
		for _, v := range []string{presentValue, addedValue} {
			assert.NotContains(t, line, v, "a secret value was logged: %q", e.Message)
		}
	}
}

func firstAuthorization(h *headerRecorder) string {
	if r := h.requests(); len(r) > 0 {
		return r[0].Get("Authorization")
	}
	return ""
}

// Review r1: a secret change restarts the servers that reference it, and a
// restart enables a disabled server, so a disabled server is skipped.
func TestNotifySecretsChanged_LeavesDisabledServersDisabled(t *testing.T) {
	quarantineOff := false
	disabled := &config.ServerConfig{Name: "off", URL: "http://127.0.0.1:1/mcp", Protocol: "streamable-http", Enabled: false,
		Headers: map[string]string{"Authorization": "Bearer ${keyring:tok}"}}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", QuarantineEnabled: &quarantineOff,
		Servers: []*config.ServerConfig{disabled}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	require.NoError(t, rt.storageManager.SaveUpstreamServer(disabled))

	require.NoError(t, rt.NotifySecretsChanged(context.Background(), "store", "tok"))
	// Restarts run in the background: the server must stay disabled.
	assert.Never(t, func() bool { return rt.Config().Servers[0].Enabled }, time.Second, 20*time.Millisecond,
		"storing a secret must not enable a disabled server")
}

func TestServerReferencesSecret_Fields(t *testing.T) {
	ref := "${keyring:tok}"
	for name, cfg := range map[string]*config.ServerConfig{
		"header": {Headers: map[string]string{"A": "Bearer " + ref}},
		"url":    {URL: "https://x/" + ref},
		"oauth":  {OAuth: &config.OAuthConfig{ClientSecret: ref}},
		"extra":  {OAuth: &config.OAuthConfig{ExtraParams: map[string]string{"k": ref}}},
		"env":    {Env: map[string]string{"T": ref}},
		"args":   {Args: []string{"--t=" + ref}},
	} {
		assert.True(t, serverReferencesSecret(cfg, ref), name)
	}
	assert.False(t, serverReferencesSecret(&config.ServerConfig{Headers: map[string]string{"A": "${keyring:other}"}}, ref))
	assert.False(t, serverReferencesSecret(nil, ref))
}
