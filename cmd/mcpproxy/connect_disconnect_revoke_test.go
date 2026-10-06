package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func runDisconnectArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var runErr error
	out := captureStdout(t, func() {
		cmd := GetDisconnectCommand()
		cmd.SetArgs(args)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		runErr = cmd.Execute()
	})
	return out, runErr
}

func clientCredentialRevoked(t *testing.T, dataDir string) bool {
	t.Helper()
	sm, err := storage.NewManager(dataDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer func() { _ = sm.Close() }()
	toks, err := sm.ListAgentTokens()
	require.NoError(t, err)
	for _, tk := range toks {
		if tk.Name == "client-cursor" {
			return tk.Revoked
		}
	}
	return true // no record at all: nothing live
}

// Offline (no daemon) disconnect removes the entry and revokes the client
// credential over config.db (#1435).
func TestDisconnect_OfflineRevokesTheClientCredential(t *testing.T) {
	home, cfg := connectTestEnv(t, true)
	seedCursor(t, home)
	out, err := runConnectArgs(t, "cursor", "--profile", "ro")
	require.NoError(t, err, out)
	resetConnectFlagValues()
	require.False(t, clientCredentialRevoked(t, cfg.DataDir), "connect minted a live credential")

	out, err = runDisconnectArgs(t, "cursor")
	require.NoError(t, err, out)
	resetConnectFlagValues()
	assert.Contains(t, out, "Credential: revoked (token client-cursor)")
	assert.True(t, clientCredentialRevoked(t, cfg.DataDir))

	// Disconnecting again: nothing to remove, exit 0, no credential line.
	out, err = runDisconnectArgs(t, "cursor")
	require.NoError(t, err, out)
	resetConnectFlagValues()
	assert.NotContains(t, out, "Credential:")
}

// With a daemon, disconnect goes through DELETE /api/v1/connect/{client} so the
// daemon revokes and records presence itself.
func TestDisconnect_DaemonBackendIssuesDelete(t *testing.T) {
	var method, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.Path, string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"success":true,"client":"cursor","action":"removed","message":"ok","credential_revoked":"client-cursor"}}`))
	}))
	defer srv.Close()

	b := &daemonConnectBackend{client: cliclient.NewClientWithAPIKey(srv.URL, "k", zap.NewNop().Sugar())}
	res, err := b.disconnect("cursor", "my-proxy")
	require.NoError(t, err)
	assert.Equal(t, http.MethodDelete, method)
	assert.Equal(t, "/api/v1/connect/cursor", path)
	assert.JSONEq(t, `{"server_name":"my-proxy"}`, body)
	assert.Equal(t, "client-cursor", res.CredentialRevoked)
	assert.Equal(t, "Credential: revoked (token client-cursor)", disconnectCredentialLine(res))
}

func TestParseDisconnectResponse_NotFoundIsAResultForAKnownClient(t *testing.T) {
	res, err := parseDisconnectResponse(http.StatusNotFound, []byte(`{"error":"no entry"}`), "cursor")
	require.NoError(t, err)
	assert.Equal(t, "not_found", res.Action)
	_, err = parseDisconnectResponse(http.StatusNotFound, []byte(`{"error":"unknown client"}`), "nope")
	require.Error(t, err)
}
