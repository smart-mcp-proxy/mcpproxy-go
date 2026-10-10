package managed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	proxytransport "github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// UX-01 r9 (finding 1): the HTTP/SSE dial gate must hold until the request is
// on the wire. A connect admitted before retirement that resumes after it must
// put nothing, credentials included, on the old endpoint.
func TestRetire_AfterHTTPAdmission_SendsNothingToOldEndpoint(t *testing.T) {
	for _, protocol := range []string{"http", "sse"} {
		t.Run(protocol, func(t *testing.T) {
			t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
			var hits atomic.Int32
			var credHits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if strings.Contains(r.Header.Get("Authorization"), "old-endpoint-secret") {
					credHits.Add(1)
				}
				w.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(srv.Close)

			cfg := &config.ServerConfig{
				Name: "late-" + protocol, URL: srv.URL + "/mcp", Protocol: protocol, Enabled: true,
				Headers: map[string]string{"Authorization": "Bearer old-endpoint-secret"},
			}
			mc, err := NewClient(cfg.Name, cfg, zap.NewNop(), nil, &config.Config{}, nil, secret.NewResolver())
			require.NoError(t, err)

			reached := make(chan struct{})
			resume := make(chan struct{})
			var once sync.Once
			// Park after admission, before the request reaches the network.
			proxytransport.BeforeUpstreamRequestHook = func(string) {
				first := false
				once.Do(func() { first = true })
				if first {
					close(reached)
					<-resume
				}
			}
			t.Cleanup(func() { proxytransport.BeforeUpstreamRequestHook = nil })

			done := make(chan error, 1)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				done <- mc.Connect(ctx)
			}()
			select {
			case <-reached:
			case <-time.After(10 * time.Second):
				t.Fatal("connect never reached the first request")
			}

			retired := make(chan struct{})
			go func() { mc.Retire(); close(retired) }()
			// Retire must not return while an admitted request is still pre-wire;
			// it aborts it, so it completes once the request is released.
			close(resume)
			select {
			case <-retired:
			case <-time.After(10 * time.Second):
				t.Fatal("Retire never completed")
			}
			_ = mc.Disconnect()

			select {
			case err := <-done:
				require.Error(t, err)
			case <-time.After(15 * time.Second):
				t.Fatal("Connect never returned")
			}
			time.Sleep(300 * time.Millisecond)
			require.Zero(t, hits.Load(), "a retired client sent a request to its old endpoint")
			require.Zero(t, credHits.Load(), "a retired client sent credentials to its old endpoint")
		})
	}
}
