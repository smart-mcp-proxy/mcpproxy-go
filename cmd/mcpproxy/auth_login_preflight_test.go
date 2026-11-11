package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 123 PR-3 (US2, FR-031): the login pre-flight. `mcpproxy auth login
// --server=<name>` reads the stored row first; an undecryptable one gets the
// MCPX_OAUTH_TOKEN_UNDECRYPTABLE explanation with the reason, key ids and the
// exact next commands, a non-zero exit and no browser (no request to the
// authorization server, discovery or DCR endpoint).

// captureStderrText runs f and returns what it wrote to os.Stderr (the login
// explanation goes to stderr, like the other login error displays).
func captureStderrText(f func()) string {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	f()
	_ = w.Close()
	os.Stderr = old
	return <-done
}

func TestPrintOAuthTokenUndecryptable_Content(t *testing.T) {
	var buf bytes.Buffer
	printOAuthTokenUndecryptable(&buf, oauthUndecryptableInfo{
		Server: "alpha", Reason: "key_mismatch", RowKeyID: "fedcba9876543210", KeyID: "0123456789abcdef",
	})
	out := buf.String()
	assert.Contains(t, out, "MCPX_OAUTH_TOKEN_UNDECRYPTABLE")
	assert.Contains(t, out, `"alpha"`)
	assert.Contains(t, out, "key_mismatch")
	assert.Contains(t, out, "sealed with a different key")
	assert.Contains(t, out, "fedcba9876543210")
	assert.Contains(t, out, "0123456789abcdef")
	assert.Contains(t, out, "mcpproxy auth logout --server=alpha")
	assert.Contains(t, out, "mcpproxy auth login --server=alpha")
	assert.Contains(t, out, "No browser was opened")
	assert.Contains(t, out, "mcpproxy doctor")
}

func TestPrintOAuthTokenUndecryptable_NoKeyIds(t *testing.T) {
	var buf bytes.Buffer
	printOAuthTokenUndecryptable(&buf, oauthUndecryptableInfo{Server: "alpha", Reason: "no_key"})
	out := buf.String()
	assert.Contains(t, out, "no encryption key configured")
	assert.NotContains(t, out, "Key id", "no id is printed when none is known")
}

// loginPreflightFixture is a data dir with a row sealed under key 1 for a
// server whose URL points at a counting HTTP server standing in for the
// authorization server, discovery and DCR endpoints.
type loginPreflightFixture struct {
	cfg  *config.Config
	hits *int32
}

func newLoginPreflightFixture(t *testing.T, configKey string) loginPreflightFixture {
	t.Helper()
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.NotFound(w, r)
	}))
	t.Cleanup(upstream.Close)

	f := newTokensFixture(t, configKey)
	srv := &config.ServerConfig{Name: "alpha", URL: upstream.URL + "/mcp", Protocol: "http", Enabled: true}
	f.cfg.Servers = []*config.ServerConfig{srv}

	m, err := storage.NewManager(f.dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(tokensKey(t, 1)))
	require.NoError(t, err)
	require.NoError(t, m.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName: oauth.GenerateServerKey(srv.Name, srv.URL), DisplayName: "alpha",
		AccessToken: tokensAccessMarker, RefreshToken: tokensRefreshMarker,
	}))
	require.NoError(t, m.Close())

	cfgPath := filepath.Join(f.dataDir, "mcp_config.json")
	require.NoError(t, config.SaveConfig(f.cfg, cfgPath))
	prevCfg, prevDataDir := authConfigPath, dataDir
	authConfigPath, dataDir = cfgPath, ""
	t.Cleanup(func() { authConfigPath, dataDir = prevCfg, prevDataDir })
	return loginPreflightFixture{cfg: f.cfg, hits: &hits}
}

func TestAuthLoginStandalone_UndecryptableRowStopsBeforeAnyRequest(t *testing.T) {
	cases := map[string]struct {
		configKey string
		reason    string
	}{
		"other key": {tokensKeyB64(9), "key_mismatch"},
		"no key":    {"", "no_key"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLoginPreflightFixture(t, tc.configKey)

			var err error
			out := captureStderrText(func() {
				err = runAuthLoginStandalone(context.Background(), "alpha")
			})
			require.Error(t, err)
			assert.Equal(t, ExitCodeGeneralError, classifyError(err))
			assert.Contains(t, out, "MCPX_OAUTH_TOKEN_UNDECRYPTABLE")
			assert.Contains(t, out, tc.reason)
			assert.Contains(t, out, "mcpproxy auth logout --server=alpha")
			assert.NotContains(t, out, tokensAccessMarker)
			assert.NotContains(t, out, tokensKeyB64(1))
			assert.NotContains(t, out, tokensKeyB64(9))
			assert.EqualValues(t, 0, atomic.LoadInt32(f.hits), "no request may reach the authorization server, discovery or DCR endpoint")
		})
	}
}

func TestPreflightStandalone_PassesWhenTheRowIsUsable(t *testing.T) {
	f := newLoginPreflightFixture(t, tokensKeyB64(1)) // the right key
	assert.NoError(t, preflightStandaloneOAuthRow(f.cfg, f.cfg.Servers[0]))

	// no row at all is a first login
	other := &config.ServerConfig{Name: "fresh", URL: "https://fresh.example.invalid/mcp", Protocol: "http", Enabled: true}
	assert.NoError(t, preflightStandaloneOAuthRow(f.cfg, other))
}

func TestPreflightStandalone_UnresolvableKeyKeepsExit4(t *testing.T) {
	f := newLoginPreflightFixture(t, "${file:"+filepath.Join(t.TempDir(), "missing", "key")+"}")
	err := preflightStandaloneOAuthRow(f.cfg, f.cfg.Servers[0])
	require.Error(t, err)
	assert.Equal(t, ExitCodeConfigError, classifyError(err))
}

// fakeDaemonAlphaURL is the URL the fake daemon reports for server "alpha".
const fakeDaemonAlphaURL = "https://alpha.example.invalid/mcp"

// fakeDaemon answers the endpoints the daemon-mode pre-flight touches.
type fakeDaemon struct {
	srv       *httptest.Server
	loginHits int32
}

func newFakeDaemon(t *testing.T, diag map[string]interface{}, loginStatus int, loginBody string) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/status":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{}})
		case r.URL.Path == "/api/v1/servers":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{
				"servers": []interface{}{map[string]interface{}{"name": "alpha", "url": fakeDaemonAlphaURL}},
			}})
		case r.URL.Path == "/api/v1/diagnostics":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": diag})
		case strings.HasSuffix(r.URL.Path, "/login"):
			atomic.AddInt32(&d.loginHits, 1)
			w.WriteHeader(loginStatus)
			_, _ = w.Write([]byte(loginBody))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func TestAuthLoginDaemon_PreflightFromDiagnosticsOpensNothing(t *testing.T) {
	d := newFakeDaemon(t, map[string]interface{}{
		"oauth_token_storage": map[string]interface{}{
			"encryption_enabled": true, "key_id": "0123456789abcdef", "undecryptable": 1,
			"servers": []interface{}{map[string]interface{}{
				"bucket_key": oauth.GenerateServerKey("alpha", fakeDaemonAlphaURL), "server": "alpha", "reason": "key_mismatch", "row_key_id": "fedcba9876543210",
			}},
		},
	}, http.StatusOK, `{"success":true,"data":{}}`)
	client := cliclient.NewClient(d.srv.URL, zap.NewNop().Sugar())

	var err error
	out := captureStderrText(func() {
		err = runAuthLoginClientMode(context.Background(), client, "alpha")
	})
	require.Error(t, err)
	assert.Equal(t, ExitCodeGeneralError, classifyError(err))
	assert.Contains(t, out, "MCPX_OAUTH_TOKEN_UNDECRYPTABLE")
	assert.Contains(t, out, "fedcba9876543210")
	assert.Contains(t, out, "0123456789abcdef")
	assert.EqualValues(t, 0, atomic.LoadInt32(&d.loginHits), "the daemon is never asked to log in")
}

func TestAuthLoginDaemon_ConflictFromTheDaemonGetsTheSameExplanation(t *testing.T) {
	// An older diagnostics payload (no section): the daemon's own refusal
	// (REST 409 with the code) is rendered the same way.
	d := newFakeDaemon(t, map[string]interface{}{}, http.StatusConflict,
		`{"success":false,"error":"MCPX_OAUTH_TOKEN_UNDECRYPTABLE: stored OAuth token cannot be decrypted: sealed with a different key; restore the encryption key"}`)
	client := cliclient.NewClient(d.srv.URL, zap.NewNop().Sugar())

	var err error
	out := captureStderrText(func() {
		err = runAuthLoginClientMode(context.Background(), client, "alpha")
	})
	require.Error(t, err)
	assert.Equal(t, ExitCodeGeneralError, classifyError(err))
	assert.Contains(t, out, "MCPX_OAUTH_TOKEN_UNDECRYPTABLE")
	assert.Contains(t, out, "mcpproxy auth logout --server=alpha")
	assert.Contains(t, out, "No browser was opened")
}

func TestAuthLoginDaemon_UsableRowStillTriggersLogin(t *testing.T) {
	d := newFakeDaemon(t, map[string]interface{}{
		"oauth_token_storage": map[string]interface{}{"encryption_enabled": true, "servers": []interface{}{}},
	}, http.StatusOK, `{"success":true,"data":{"success":true}}`)
	client := cliclient.NewClient(d.srv.URL, zap.NewNop().Sugar())
	_ = captureStderrText(func() {
		_ = runAuthLoginClientMode(context.Background(), client, "alpha")
	})
	assert.EqualValues(t, 1, atomic.LoadInt32(&d.loginHits))
}

func TestAuthStatusPretty_ShowsTheUndecryptableStateDistinctly(t *testing.T) {
	servers := []map[string]interface{}{
		{
			"name": "alpha", "enabled": true,
			"last_error": "stored OAuth token cannot be decrypted: sealed with a different key",
			"error_code": "MCPX_OAUTH_TOKEN_UNDECRYPTABLE",
			"health": map[string]interface{}{
				"level": "unhealthy", "admin_state": "enabled", "action": "configure",
				"summary": "Stored OAuth token cannot be decrypted",
				"detail":  "stored OAuth token cannot be decrypted: sealed with a different key",
			},
		},
		{
			"name": "beta", "enabled": true, "last_error": "",
			"health": map[string]interface{}{"level": "unhealthy", "admin_state": "enabled", "action": "login", "summary": "Login required"},
			"oauth":  map[string]interface{}{},
		},
	}
	out := captureOutput(func() { _ = displayAuthStatusPretty(servers) })
	assert.Contains(t, out, "cannot be decrypted")
	assert.Contains(t, out, "MCPX_OAUTH_TOKEN_UNDECRYPTABLE")
	assert.Contains(t, out, "mcpproxy auth logout --server=alpha")
	// the sign-in action belongs to beta only
	alphaBlock := out[strings.Index(out, "Server: alpha"):strings.Index(out, "Server: beta")]
	assert.NotContains(t, alphaBlock, "Action: mcpproxy auth login --server=alpha", "an undecryptable row is never offered as 'log in'")
	assert.Contains(t, out, "Action: mcpproxy auth login --server=beta")
}

// A database the pre-flight cannot open (a running core holds it) stops the
// login with exit 3: continuing would fall back to in-memory storage, where
// nothing can see an undecryptable row.
func TestPreflightStandalone_LockedDatabaseStopsTheLogin(t *testing.T) {
	f := newLoginPreflightFixture(t, tokensKeyB64(2)) // the row is undecryptable
	holder, err := storage.NewManager(f.cfg.DataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(tokensKey(t, 1)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Close() })

	err = preflightStandaloneOAuthRow(f.cfg, f.cfg.Servers[0])
	require.Error(t, err)
	assert.Equal(t, ExitCodeDBLocked, classifyError(err))
	assert.Zero(t, atomic.LoadInt32(f.hits))
}

// A row is keyed by name and URL: an undecryptable row left by an earlier URL
// of the same server does not block a login for the current one.
func TestAuthLoginDaemon_UndecryptableRowOfAnEarlierURLDoesNotBlock(t *testing.T) {
	d := newFakeDaemon(t, map[string]interface{}{
		"oauth_token_storage": map[string]interface{}{
			"encryption_enabled": true, "undecryptable": 1,
			"servers": []interface{}{map[string]interface{}{
				"bucket_key": oauth.GenerateServerKey("alpha", "https://old.example.invalid/mcp"), "server": "alpha", "reason": "key_mismatch",
			}},
		},
	}, http.StatusOK, `{"success":true,"data":{"success":true}}`)
	client := cliclient.NewClient(d.srv.URL, zap.NewNop().Sugar())
	_ = captureStderrText(func() {
		_ = runAuthLoginClientMode(context.Background(), client, "alpha")
	})
	assert.EqualValues(t, 1, atomic.LoadInt32(&d.loginHits))
}
