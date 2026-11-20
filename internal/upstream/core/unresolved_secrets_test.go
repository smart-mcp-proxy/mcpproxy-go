package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

type mapKeyring map[string]string

func (k mapKeyring) CanResolve(t string) bool { return t == "keyring" }
func (k mapKeyring) IsAvailable() bool        { return true }
func (k mapKeyring) Resolve(_ context.Context, ref secret.Ref) (string, error) {
	if v, ok := k[ref.Name]; ok {
		return v, nil
	}
	return "", errors.New("secret not found in keyring")
}
func (k mapKeyring) Store(context.Context, secret.Ref, string) error { return nil }
func (k mapKeyring) Delete(context.Context, secret.Ref) error        { return nil }
func (k mapKeyring) List(context.Context) ([]secret.Ref, error)      { return nil, nil }

func resolverWith(k mapKeyring) *secret.Resolver {
	r := secret.NewResolver()
	r.RegisterProvider("keyring", k)
	return r
}

// Release QA F9, at the client: every way the client talks to an HTTP upstream
// or its authorization server is refused before a request, the error names only
// the references that failed, and no value is logged.
func TestUnresolvedWireSecret_RefusesConnectLoginAndRefresh(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	const present = "present-value-7c41"
	const literal = "literal-credential-55d0"
	cfg := &config.ServerConfig{
		Name: "f9", URL: srv.URL + "/mcp", Protocol: "http", Enabled: true,
		Headers: map[string]string{
			// one header mixes a resolvable reference, a missing one and literal text
			"Authorization": literal + " ${keyring:present} ${keyring:missing}",
		},
	}
	coreLog, logs := observer.New(zapcore.DebugLevel)
	c, err := NewClient(cfg.Name, cfg, zap.New(coreLog), nil, &config.Config{}, nil, resolverWith(mapKeyring{"present": present}))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = c.Connect(ctx)
	require.Error(t, err)
	assert.True(t, IsUnresolvedSecretError(err))
	assert.Equal(t, int32(0), hits.Load(), "nothing is sent")

	msg := err.Error()
	assert.Contains(t, msg, "unresolved secret reference ${keyring:missing} in Headers[Authorization]")
	assert.NotContains(t, msg, "${keyring:present}", "a reference that resolves is not named")
	assert.Contains(t, msg, "mcpproxy secrets set missing")
	assert.Contains(t, msg, "mcpproxy upstream restart f9")
	assert.NotContains(t, msg, present)
	assert.NotContains(t, msg, literal)
	assert.Equal(t, diagnostics.ConfigMissingSecret, diagnostics.Classify(err, diagnostics.ClassifierHints{Transport: "http"}))
	assert.Equal(t, "missing", health.ExtractMissingSecret(msg))

	assert.Error(t, c.CheckManualLoginAllowed(), "no sign-in with unresolved settings")
	_, err = c.StartOAuthFlowQuick(ctx)
	assert.Error(t, err, "quick sign-in refused") // OAuth is off in this test, which refuses first
	assert.True(t, IsUnresolvedSecretError(c.RefreshOAuthTokenDirect(ctx)), "token refresh refused")
	assert.Equal(t, int32(0), hits.Load(), "still nothing sent")

	assertNoValueLogged(t, logs, present, literal)
}

// assertNoValueLogged checks every logged message and field (strings, errors,
// arrays: through ContextMap) for any of the values.
func assertNoValueLogged(t *testing.T, logs *observer.ObservedLogs, values ...string) {
	t.Helper()
	for _, e := range logs.All() {
		line := e.Message + " " + fmt.Sprint(e.ContextMap())
		for _, v := range values {
			assert.NotContains(t, line, v, "a value was logged in %q", e.Message)
		}
	}
}

// Only fields an HTTP upstream receives block the connection; a stdio
// server's env keeps the existing behaviour (logged, used as written), and a
// server whose references all resolve is untouched.
func TestUnresolvedSecret_OnlyWireFieldsBlock(t *testing.T) {
	r := resolverWith(mapKeyring{"ok": "v"})
	stdio := &config.ServerConfig{Name: "local", Command: "true", Protocol: "stdio", Enabled: true,
		Env: map[string]string{"TOKEN": "${keyring:missing}"}}
	c, err := NewClient(stdio.Name, stdio, zap.NewNop(), nil, &config.Config{}, nil, r)
	require.NoError(t, err)
	assert.NoError(t, c.UnresolvedSecrets())

	// A stdio server never sends its headers, URL or OAuth settings, so a
	// stale reference there does not stop it (review r1).
	stdioHeaders := &config.ServerConfig{Name: "local-h", Command: "true", URL: "https://x.example/mcp", Protocol: "stdio", Enabled: true,
		Headers: map[string]string{"Authorization": "${keyring:missing}"},
		OAuth:   &config.OAuthConfig{ClientSecret: "${keyring:missing}"}}
	c, err = NewClient(stdioHeaders.Name, stdioHeaders, zap.NewNop(), nil, &config.Config{}, nil, r)
	require.NoError(t, err)
	assert.NoError(t, c.UnresolvedSecrets(), "a stdio server's headers never reach a wire")

	good := &config.ServerConfig{Name: "good", URL: "http://127.0.0.1:1/mcp", Protocol: "http", Enabled: true,
		Headers: map[string]string{"Authorization": "Bearer ${keyring:ok}"}}
	c, err = NewClient(good.Name, good, zap.NewNop(), nil, &config.Config{}, nil, r)
	require.NoError(t, err)
	assert.NoError(t, c.UnresolvedSecrets())
	assert.Equal(t, "Bearer v", c.config.Headers["Authorization"])

	for _, tc := range []struct {
		name string
		cfg  *config.ServerConfig
		want string
	}{
		{"url", &config.ServerConfig{Name: "u", URL: "https://x.example/${keyring:missing}/mcp", Protocol: "http"}, "in URL"},
		{"oauth", &config.ServerConfig{Name: "o", URL: "https://x.example/mcp", Protocol: "http",
			OAuth: &config.OAuthConfig{ClientID: "id", ClientSecret: "${keyring:missing}"}}, "in OAuth.ClientSecret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewClient(tc.cfg.Name, tc.cfg, zap.NewNop(), nil, &config.Config{}, nil, r)
			require.NoError(t, err)
			require.Error(t, c.UnresolvedSecrets())
			assert.Contains(t, c.UnresolvedSecrets().Error(), tc.want)
		})
	}
}

// With OAuth applicable, both manual sign-in flows stop at the guard: they
// would send the unresolved settings to the authorization server.
func TestUnresolvedWireSecret_SignInFlowsRefused(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	cfg := &config.ServerConfig{Name: "f9-oauth", URL: srv.URL + "/mcp", Protocol: "http", Enabled: true,
		OAuth: &config.OAuthConfig{ClientID: "id", ClientSecret: "${keyring:missing}"}}
	c, err := NewClient(cfg.Name, cfg, zap.NewNop(), nil, &config.Config{}, nil, resolverWith(mapKeyring{}))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = c.StartOAuthFlowQuick(ctx)
	assert.True(t, IsUnresolvedSecretError(err), "quick sign-in: %v", err)
	_, err = c.ForceOAuthFlowWithResult(ctx)
	assert.True(t, IsUnresolvedSecretError(err), "forced sign-in: %v", err)
	assert.Contains(t, err.Error(), "in OAuth.ClientSecret")
	assert.Equal(t, int32(0), hits.Load(), "nothing sent to the upstream or its authorization server")
}

// The fix hint matches what recovers: a keyring or file secret is picked up by
// a server restart; an environment variable only by restarting mcpproxy.
func TestUnresolvedSecretError_FixHints(t *testing.T) {
	keyring := &UnresolvedSecretError{Server: "s", Refs: []UnresolvedSecretRef{{Field: "Headers[A]", Reference: "${keyring:k}", Type: "keyring", Name: "k"}}}
	assert.Contains(t, keyring.Error(), "mcpproxy secrets set k, then restart the server (mcpproxy upstream restart s)")
	env := &UnresolvedSecretError{Server: "s", Refs: []UnresolvedSecretRef{{Field: "Headers[A]", Reference: "${env:TOKEN}", Type: "env", Name: "TOKEN"}}}
	assert.Contains(t, env.Error(), "Set the environment variable TOKEN and restart mcpproxy")
	assert.NotContains(t, env.Error(), "upstream restart")
	assert.Equal(t, "TOKEN", health.ExtractMissingSecret(env.Error()))
}
