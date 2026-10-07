package transport

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const toolsCallBody = `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"x"}}`

func doGet(t *testing.T, c *http.Client, ctx context.Context, url string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(toolsCallBody))
	require.NoError(t, err)
	resp, err := c.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

func statusServer(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
}

// FR-042: the recorder is filled by a RoundTripper layer installed in every
// client branch CreateHTTPClient builds.
func TestCallRecorderInstalledInEveryClientBranch(t *testing.T) {
	srv := statusServer(http.StatusBadGateway)
	defer srv.Close()

	cases := map[string]struct {
		cfg   *HTTPTransportConfig
		oauth bool
	}{
		"plain":          {cfg: &HTTPTransportConfig{URL: srv.URL}},
		"static headers": {cfg: &HTTPTransportConfig{URL: srv.URL, Headers: map[string]string{"X-A": "b"}}},
		"trace":          {cfg: &HTTPTransportConfig{URL: srv.URL, TraceEnabled: true}},
		"retry-after":    {cfg: &HTTPTransportConfig{URL: srv.URL, RetryAfter: NewRetryAfterRecorder()}},
		"oauth":          {cfg: &HTTPTransportConfig{URL: srv.URL}, oauth: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := tc.cfg.streamableHTTPClient(tc.oauth, zap.NewNop())
			ctx, rec := WithCallRecorder(context.Background())
			doGet(t, c, ctx, srv.URL)
			snap := rec.Snapshot()
			assert.Equal(t, 1, snap.Requests)
			assert.Equal(t, 1, snap.Responses)
			assert.Equal(t, http.StatusBadGateway, snap.LastStatus)
		})
	}
}

func TestCallRecorderSSEClientBranch(t *testing.T) {
	srv := statusServer(http.StatusServiceUnavailable)
	defer srv.Close()
	cfg := &HTTPTransportConfig{URL: srv.URL}
	rt := cfg.upstreamRoundTripper(http.DefaultTransport, zap.NewNop())
	ctx, rec := WithCallRecorder(context.Background())
	doGet(t, &http.Client{Transport: rt}, ctx, srv.URL)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Snapshot().LastStatus)
}

func TestCallRecorderIgnoresRequestsWithoutRecorder(t *testing.T) {
	srv := statusServer(http.StatusOK)
	defer srv.Close()
	cfg := &HTTPTransportConfig{URL: srv.URL}
	c := cfg.streamableHTTPClient(false, zap.NewNop())

	_, rec := WithCallRecorder(context.Background())
	// A different request (no recorder in its ctx) must not touch rec.
	doGet(t, c, context.Background(), srv.URL)
	assert.Equal(t, 0, rec.Snapshot().Requests)
}

func TestCallRecorderTransportErrorHasNoResponse(t *testing.T) {
	srv := statusServer(http.StatusOK)
	url := srv.URL
	srv.Close() // connection refused
	cfg := &HTTPTransportConfig{URL: url}
	c := cfg.streamableHTTPClient(false, zap.NewNop())
	ctx, rec := WithCallRecorder(context.Background())
	doGet(t, c, ctx, url)
	snap := rec.Snapshot()
	assert.Equal(t, 1, snap.Requests)
	assert.Equal(t, 0, snap.Responses)
	assert.Equal(t, 0, snap.LastStatus)
}

func TestCallRecorderMarkDispatched(t *testing.T) {
	ctx, rec := WithCallRecorder(context.Background())
	assert.False(t, rec.Snapshot().Dispatched)
	MarkDispatched(ctx)
	assert.True(t, rec.Snapshot().Dispatched)
	// No recorder: a no-op, never a panic.
	MarkDispatched(context.Background())
}

// Concurrent calls each carry their own recorder and must not cross-contaminate.
func TestCallRecorderConcurrentCallsIsolated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var code int
		_, _ = fmt.Sscanf(r.URL.Query().Get("code"), "%d", &code)
		w.WriteHeader(code)
	}))
	defer srv.Close()
	cfg := &HTTPTransportConfig{URL: srv.URL}
	c := cfg.streamableHTTPClient(false, zap.NewNop())

	codes := []int{200, 401, 404, 500, 502, 503, 504, 429}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		code := codes[i%len(codes)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, rec := WithCallRecorder(context.Background())
			doGet(t, c, ctx, fmt.Sprintf("%s/?code=%d", srv.URL, code))
			assert.Equal(t, code, rec.Snapshot().LastStatus)
		}()
	}
	wg.Wait()
}

// mcp-go reuses the tools/call ctx for reply POSTs to server-initiated
// requests; those must not overwrite the call's own recorded status.
func TestCallRecorderIgnoresNonToolsCallPosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "reply") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cfg := &HTTPTransportConfig{URL: srv.URL}
	c := cfg.streamableHTTPClient(false, zap.NewNop())
	ctx, rec := WithCallRecorder(context.Background())
	doGet(t, c, ctx, srv.URL)
	for _, body := range []string{`{"jsonrpc":"2.0","id":1,"result":{}}`, `{"jsonrpc":"2.0","method":"notifications/x"}`} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/reply", strings.NewReader(body))
		require.NoError(t, err)
		resp, err := c.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}
	snap := rec.Snapshot()
	assert.Equal(t, 1, snap.Requests)
	assert.Equal(t, http.StatusOK, snap.LastStatus)
}
