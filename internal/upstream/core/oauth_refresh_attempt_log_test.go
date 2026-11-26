package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// GH #1827: the stored-DCR refresh does not go through the OAuth transport
// wrapper, so it logged no token-request line at all. It now logs the same
// one, to the server's OAuth logger.
func TestRefreshWithStoredCredentials_LogsTokenRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	}))
	t.Cleanup(srv.Close)
	obs, logs := observer.New(zapcore.DebugLevel)
	c := &Client{config: &config.ServerConfig{Name: "s"}, logger: zap.New(obs)}
	_, err := c.refreshTokenWithStoredCredentials(context.Background(), srv.URL+"/token",
		&storage.OAuthTokenRecord{RefreshToken: "rt-secret-9", ClientID: "cid"})
	require.Error(t, err)

	entries := logs.FilterMessage("OAuth token request finished").All()
	require.Len(t, entries, 1)
	m := entries[0].ContextMap()
	assert.Equal(t, "refresh_token", m["grant_type"])
	assert.Equal(t, int64(http.StatusBadRequest), m["http_status"])
	for k, v := range m {
		if s, ok := v.(string); ok {
			assert.NotContains(t, s, "rt-secret", "field %s", k)
		}
	}
}

// The coordinator request carries the server's OAuth logger, so flight
// start/finish lines reach the server's own log file.
func TestOAuthRefreshRequest_CarriesServerLogger(t *testing.T) {
	c := &Client{config: &config.ServerConfig{Name: "s"}, logger: zap.NewNop()}
	req := c.oauthRefreshRequest("k", nil)
	assert.NotNil(t, req.Logger)
}

// Review round 5 on #2063: a refresh flight can finish after its instance was
// retired and its log file closed. Writing to the server file then would
// reopen it with nobody left to close it, so the server-file half of the
// OAuth logger drops writes once the instance is retired; the main log keeps
// them.
func TestOAuthLogger_DropsServerFileWritesAfterRetire(t *testing.T) {
	mainCore, mainLogs := observer.New(zapcore.InfoLevel)
	fileCore, fileLogs := observer.New(zapcore.InfoLevel)
	c := &Client{config: &config.ServerConfig{Name: "s"}, logger: zap.New(mainCore), upstreamLogger: zap.New(fileCore)}
	l := c.oauthLogger()
	l.Info("before")
	c.Retire()
	l.Info("after")
	assert.Equal(t, 2, mainLogs.Len())
	assert.Equal(t, 1, fileLogs.Len())
	assert.Equal(t, 0, fileLogs.FilterMessage("after").Len())
}
