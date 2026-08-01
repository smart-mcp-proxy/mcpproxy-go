//go:build (linux || darwin) && !windows

package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
)

// SC-006: an integration test proving a request over the private local socket
// reaches the gated connect-write route as an ADMINISTRATIVE caller, end to end.
//
// The existing coverage is all unit-level and each piece assumes the next: the
// listener tests check that a Unix connection is tagged, the middleware tests
// check that a tray-tagged context becomes an admin auth context, and the gating
// tests check that an admin context passes requireServerOp. httptest cannot join
// them — it never creates a connection, so ConnContext never runs and the tag
// never exists. Only a real socket, accepted through the production multiplex
// listener and served with the production ConnContext, exercises
// listener → tagging → auth conversion → gate → handler as one path.
//
// The native Connect Client form depends on exactly this chain: it sends its
// mutating requests over the socket with NO credential, so if any link breaks
// the form silently loses the ability to write client configs (or, worse, only
// works because something else authenticated it).

// socketE2EServer starts an httpapi server on a real Unix socket AND a real TCP
// port, through the same ListenerManager, multiplexListener and ConnContext the
// production server uses. It returns the socket path, the TCP address, and the
// isolated home the connect service writes into.
func socketE2EServer(t *testing.T, api *httpapi.Server) (socketPath, tcpAddr string) {
	t.Helper()

	// Socket paths are capped at ~104 bytes; t.TempDir() under macOS's long
	// per-test path blows that limit, so anchor the data dir in /tmp.
	dataDir, err := os.MkdirTemp("/tmp", "mcpx-sock")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	require.NoError(t, os.Chmod(dataDir, 0o700))

	logger := zap.NewNop()
	manager := NewListenerManager(&ListenerConfig{
		DataDir:    dataDir,
		TCPAddress: "127.0.0.1:0",
		Logger:     logger,
	})
	tcpListener, err := manager.CreateTCPListener()
	require.NoError(t, err)
	require.NotNil(t, tcpListener)

	trayListener, err := manager.CreateTrayListener()
	require.NoError(t, err)
	require.NotNil(t, trayListener, "the tray socket listener is the subject of this test")
	require.Equal(t, ConnectionSourceTray, trayListener.Source)

	mux := &multiplexListener{
		listeners: []*Listener{tcpListener, trayListener},
		logger:    logger,
	}

	httpSrv := &http.Server{
		Handler:           api,
		ReadHeaderTimeout: 10 * time.Second,
		// The production tagging hook, shared verbatim — a copy here would
		// prove only that the copy works.
		ConnContext: taggedConnContext,
	}
	go func() { _ = httpSrv.Serve(mux) }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		_ = mux.Close()
		_ = manager.CloseAll()
	})

	return filepath.Join(dataDir, "mcpproxy.sock"), tcpListener.Address
}

// socketHTTPClient dials the Unix socket for every request, the way the tray's
// administrative transport does.
func socketHTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

// TestConnectSocketE2E_SocketCallerIsAdminOnGatedWriteRoute drives
// POST /api/v1/connect/{client} over a real Unix socket with NO credential of
// any kind and requires the write to happen: the admin context can only have
// come from the transport.
func TestConnectSocketE2E_SocketCallerIsAdminOnGatedWriteRoute(t *testing.T) {
	srv := newConsistencyServer(t)
	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)

	home := t.TempDir()
	api.SetConnectService(connect.NewServiceWithHome("127.0.0.1:8080", "", home))

	socketPath, _ := socketE2EServer(t, api)
	client := socketHTTPClient(socketPath)

	// No X-API-Key, no bearer token, no query credential.
	resp, err := client.Post("http://mcpproxy/api/v1/connect/claude-code",
		"application/json", strings.NewReader(`{"server_name":"mcpproxy"}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"an unauthenticated socket caller must reach the gated write route as admin")

	// Reaching the route is not enough — the write itself must have happened,
	// which proves the gate passed rather than something short-circuiting.
	cfgPath := connect.ConfigPath("claude-code", home)
	written, err := os.ReadFile(cfgPath)
	require.NoError(t, err, "the connect write must have created %s", cfgPath)
	assert.Contains(t, string(written), "mcpproxy")
}

// TestConnectSocketE2E_AgentTokenOverTCPIsRejected is the negative control on
// the same route and the same running server: the socket's admin treatment is a
// property of the transport, not a hole in the gate.
func TestConnectSocketE2E_AgentTokenOverTCPIsRejected(t *testing.T) {
	srv := newConsistencyServer(t)
	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)

	home := t.TempDir()
	api.SetConnectService(connect.NewServiceWithHome("127.0.0.1:8080", "", home))

	tokenDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tokenDir)
	require.NoError(t, err)
	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)
	api.SetTokenStore(&socketE2ETokenStore{
		raw: rawToken,
		token: &auth.AgentToken{
			Name:           "socket-e2e-agent",
			TokenPrefix:    auth.TokenPrefix(rawToken),
			AllowedServers: []string{"*"},
			// Deliberately generous: read+write on every server, so a rejection
			// can only come from the admin-operation policy.
			Permissions: []string{auth.PermRead, auth.PermWrite},
			ExpiresAt:   time.Now().Add(time.Hour),
			CreatedAt:   time.Now(),
		},
	}, tokenDir)

	_, tcpAddr := socketE2EServer(t, api)

	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://%s/api/v1/connect/claude-code", tcpAddr),
		strings.NewReader(`{"server_name":"mcpproxy"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", rawToken)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a restricted agent token must not reach the connect write route over TCP")

	if _, statErr := os.Stat(connect.ConfigPath("claude-code", home)); statErr == nil {
		t.Fatal("a rejected request must not have written the client config")
	}
}

// socketE2ETokenStore is a minimal httpapi.TokenStore that validates exactly one
// agent token; the management endpoints are unused by this test.
type socketE2ETokenStore struct {
	raw   string
	token *auth.AgentToken
}

func (s *socketE2ETokenStore) ValidateAgentToken(rawToken string, _ []byte) (*auth.AgentToken, error) {
	if rawToken == s.raw {
		return s.token, nil
	}
	return nil, fmt.Errorf("token not found")
}

func (s *socketE2ETokenStore) CreateAgentToken(auth.AgentToken, string, []byte) error { return nil }
func (s *socketE2ETokenStore) ListAgentTokens() ([]auth.AgentToken, error)            { return nil, nil }
func (s *socketE2ETokenStore) GetAgentTokenByName(string) (*auth.AgentToken, error) {
	return nil, fmt.Errorf("not found")
}
func (s *socketE2ETokenStore) RevokeAgentToken(string) error { return nil }
func (s *socketE2ETokenStore) DeleteAgentToken(string) error { return nil }
func (s *socketE2ETokenStore) RegenerateAgentToken(string, string, []byte) (*auth.AgentToken, error) {
	return nil, fmt.Errorf("not supported")
}
func (s *socketE2ETokenStore) UpdateAgentTokenLastUsed(string) error { return nil }
