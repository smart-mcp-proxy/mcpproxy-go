package core

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// A client's roots/list_changed can arrive while the upstream session is being
// re-initialized, which rewrites mcp-go's client state. Telling the upstream
// must wait for the re-initialize instead of reading that state concurrently
// (run with -race).
func TestRelay_RootsChangedDuringSessionReinit(t *testing.T) {
	disableOAuthForTest(t)
	srv := httptest.NewServer(mcpserver.NewStreamableHTTPServer(relayUpstream()))
	t.Cleanup(srv.Close)
	cfg := &config.ServerConfig{Name: "relay-reinit", Protocol: "streamable-http", URL: srv.URL, Enabled: true, ClientFeatures: []string{config.ClientFeatureRoots}}
	c, err := NewClient("relay-reinit", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() { defer wg.Done(); c.notifyRootsChanged() }()
		go func() { defer wg.Done(); _ = c.ReinitializeSession(ctx) }()
	}
	wg.Wait()
	// The notifications are sent in the background; give them time to run.
	time.Sleep(500 * time.Millisecond)
}
