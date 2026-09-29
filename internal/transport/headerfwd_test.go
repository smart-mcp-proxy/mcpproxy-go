package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/headerfwd"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

const fwdSentinel = "SENTINEL-tenant-9f3a"

// outboundCtx builds a context carrying key B with one forwarded header, going
// through the public Capture/Outbound path (Snapshot has no value setter).
func outboundCtx(t *testing.T, name, value string) context.Context {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
	r.Header.Set(name, value)
	snap := headerfwd.Capture(r, map[string]struct{}{name: {}})
	out := headerfwd.Outbound(snap, headerfwd.Policy{Enabled: true, Allow: []string{name}, Transport: "http"})
	require.False(t, out.IsEmpty())
	return headerfwd.WithOutbound(context.Background(), out)
}

type recorder struct {
	mu   sync.Mutex
	seen []http.Header
}

func (r *recorder) add(h http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, h.Clone())
}

func (r *recorder) all() []http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]http.Header(nil), r.seen...)
}

// rpcServer answers initialize and notifications so the client is usable, and
// hands every tools/call to onCall (after recording its headers in rec).
func rpcServer(rec *recorder, onCall func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &msg)
		switch msg.Method {
		case "initialize":
			rec.add(r.Header)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(msg.ID) +
				`,"result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"x","version":"1"}}}`))
		case "tools/call":
			rec.add(r.Header)
			onCall(w, r)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
}

func callTool(t *testing.T, c *mcpclient.Client, ctx context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.Start(context.Background()); err != nil {
		t.Logf("start: %v", err)
	}
	if _, err := c.Initialize(context.Background(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = "echo"
	_, err := c.CallTool(ctx, req) // the response is irrelevant; we inspect requests
	t.Logf("calltool: %v", err)
}

type branch struct {
	name    string
	cfg     func(url string) *HTTPTransportConfig
	timeout time.Duration
	oauth   bool
}

func branches() []branch {
	static := map[string]string{"X-Static": "s"}
	return []branch{
		{"plain-no-headers", func(u string) *HTTPTransportConfig { return &HTTPTransportConfig{URL: u} }, 180 * time.Second, false},
		{"plain-static-headers", func(u string) *HTTPTransportConfig { return &HTTPTransportConfig{URL: u, Headers: static} }, 0, false},
		{"plain-trace", func(u string) *HTTPTransportConfig {
			return &HTTPTransportConfig{URL: u, Headers: static, TraceEnabled: true}
		}, 180 * time.Second, false},
		{"plain-retry-after", func(u string) *HTTPTransportConfig {
			return &HTTPTransportConfig{URL: u, Headers: static, RetryAfter: NewRetryAfterRecorder()}
		}, 0, false},
		{"plain-retry-after-no-headers", func(u string) *HTTPTransportConfig {
			return &HTTPTransportConfig{URL: u, RetryAfter: NewRetryAfterRecorder()}
		}, 180 * time.Second, false},
		{"oauth", func(u string) *HTTPTransportConfig {
			return &HTTPTransportConfig{URL: u, OAuthConfig: oauthTestConfig(), UseOAuth: true}
		}, 0, true},
		{"oauth-retry-after", func(u string) *HTTPTransportConfig {
			return &HTTPTransportConfig{URL: u, OAuthConfig: oauthTestConfig(), UseOAuth: true, RetryAfter: NewRetryAfterRecorder()}
		}, 0, true},
	}
}

// T020: the header func forwards on a call that carries key B and stays silent
// otherwise, on every branch of CreateHTTPClient.
func TestCreateHTTPClient_HeaderFuncForwardsOnlyWithKeyB(t *testing.T) {
	for _, b := range branches() {
		t.Run(b.name, func(t *testing.T) {
			rec := &recorder{}
			srv := rpcServer(rec, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
			defer srv.Close()

			c, err := CreateHTTPClient(b.cfg(srv.URL + "/mcp"))
			require.NoError(t, err)
			defer c.Close()
			callTool(t, c, context.Background())
			for _, h := range rec.all() {
				assert.Empty(t, h.Get("X-Tenant-Id"), "no key B: nothing may be forwarded")
			}

			rec2 := &recorder{}
			srv2 := rpcServer(rec2, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
			defer srv2.Close()
			c2, err := CreateHTTPClient(b.cfg(srv2.URL + "/mcp"))
			require.NoError(t, err)
			defer c2.Close()
			callTool(t, c2, outboundCtx(t, "X-Tenant-Id", fwdSentinel))
			var got bool
			for _, h := range rec2.all() {
				if h.Get("X-Tenant-Id") == fwdSentinel {
					got = true
				}
			}
			assert.True(t, got, "key B set: the header must reach the upstream")
		})
	}
}

// A forwarded name that collides with a static header must not override it
// (the transport's filter is the last line of defense even if Outbound was
// handed a policy that did not know the static key), and the OAuth bearer is
// unchanged when a forwarded set is present.
func TestCreateHTTPClient_StaticAndOAuthWinOverForwarded(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  func(url string) *HTTPTransportConfig
		ctx  func(t *testing.T) context.Context
		key  string
		want string
	}{
		{"static", func(u string) *HTTPTransportConfig {
			return &HTTPTransportConfig{URL: u, Headers: map[string]string{"X-Static": "operator"}}
		}, func(t *testing.T) context.Context { return outboundCtx(t, "X-Static", fwdSentinel) }, "X-Static", "operator"},
		{"oauth-bearer", func(u string) *HTTPTransportConfig {
			return &HTTPTransportConfig{URL: u, OAuthConfig: oauthTestConfig(), UseOAuth: true}
		}, func(t *testing.T) context.Context { return outboundCtx(t, "X-Tenant-Id", fwdSentinel) }, "Authorization", "Bearer test-access-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			srv := rpcServer(rec, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
			defer srv.Close()
			c, err := CreateHTTPClient(tc.cfg(srv.URL + "/mcp"))
			require.NoError(t, err)
			defer c.Close()
			callTool(t, c, tc.ctx(t))
			require.NotEmpty(t, rec.all())
			for _, h := range rec.all() {
				assert.Equal(t, tc.want, h.Get(tc.key))
			}
		})
	}
}

// T021: cross-origin redirect strips forwarded names on every branch; the
// same-origin redirect keeps them; per-branch timeouts are unchanged.
func TestCreateHTTPClient_CrossOriginRedirectStripsForwarded(t *testing.T) {
	for _, b := range branches() {
		t.Run(b.name, func(t *testing.T) {
			target := &recorder{}
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target.add(r.Header)
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer other.Close()

			origin := &recorder{}
			self := rpcServer(origin, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/mcp" {
					http.Redirect(w, r, other.URL+"/landing", http.StatusTemporaryRedirect)
					return
				}
				w.WriteHeader(http.StatusTooManyRequests)
			})
			defer self.Close()

			c, err := CreateHTTPClient(b.cfg(self.URL + "/mcp"))
			require.NoError(t, err)
			defer c.Close()
			callTool(t, c, outboundCtx(t, "X-Tenant-Id", fwdSentinel))

			require.NotEmpty(t, target.all(), "the cross-origin redirect must have been followed")
			for _, h := range target.all() {
				assert.Empty(t, h.Get("X-Tenant-Id"), "cross-origin target must never see the forwarded header")
				for _, vs := range h {
					for _, v := range vs {
						assert.NotContains(t, v, fwdSentinel)
					}
				}
			}
			var first bool
			for _, h := range origin.all() {
				if h.Get("X-Tenant-Id") == fwdSentinel {
					first = true
				}
			}
			assert.True(t, first, "the original origin still receives it")

			hc := b.cfg("http://127.0.0.1:1/mcp").streamableHTTPClient(b.oauth, zap.NewNop())
			assert.Equal(t, b.timeout, hc.Timeout, "per-branch timeout must be unchanged")
			assert.NotNil(t, hc.CheckRedirect)
		})
	}
}

func TestForwardedRedirectFilter_SameOriginKeepsAndLimit(t *testing.T) {
	ctx := outboundCtx(t, "X-Tenant-Id", fwdSentinel)
	first, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://a.example/mcp", http.NoBody)
	same, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://a.example:80/x", http.NoBody)
	same.Header.Set("X-Tenant-Id", fwdSentinel)
	require.NoError(t, forwardedRedirectFilter(same, []*http.Request{first}))
	assert.Equal(t, fwdSentinel, same.Header.Get("X-Tenant-Id"))

	cross, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://a.example/x", http.NoBody)
	cross.Header.Set("X-Tenant-Id", fwdSentinel)
	require.NoError(t, forwardedRedirectFilter(cross, []*http.Request{first}))
	assert.Empty(t, cross.Header.Get("X-Tenant-Id"), "scheme change is a different origin")

	via := make([]*http.Request, maxRedirects)
	for i := range via {
		via[i] = first
	}
	assert.Error(t, forwardedRedirectFilter(same, via))

	// Without key B behaviour is unchanged: nothing is stripped.
	plain, _ := http.NewRequest(http.MethodPost, "https://b.example/x", http.NoBody)
	plain.Header.Set("X-Tenant-Id", "keep")
	require.NoError(t, forwardedRedirectFilter(plain, []*http.Request{first}))
	assert.Equal(t, "keep", plain.Header.Get("X-Tenant-Id"))
}

// T022: the trace transport masks key-B and allowlisted names (a name unknown
// to RedactHeaders) in request AND response headers on both sinks.
func TestLoggingTransport_MasksForwardedHeaders(t *testing.T) {
	require.False(t, oauth.RedactHeaders(http.Header{"X-Tenant-Id": {"probe"}})["X-Tenant-Id"] == "[REDACTED]" ||
		oauth.RedactHeaders(http.Header{"X-Tenant-Id": {"probe"}})["X-Tenant-Id"] != "probe",
		"precondition: RedactHeaders must not already know X-Tenant-Id")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tenant-Id", fwdSentinel) // echoed in a response header
		w.Header().Set("X-Region", "eu")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	obsCore, logs := observer.New(zapcore.DebugLevel)
	tr := NewLoggingTransport(srv.Client().Transport, zap.New(obsCore))
	tr.maskNames = func() []string { return []string{"x-tenant-id"} }

	run := func(ctx context.Context) (string, string) {
		logs.TakeAll()
		var zapText string
		stdout := captureStdout(t, func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/mcp", http.NoBody)
			require.NoError(t, err)
			req.Header.Set("X-Tenant-Id", fwdSentinel)
			req.Header.Set("X-Other", "visible-value")
			resp, err := tr.RoundTrip(req)
			require.NoError(t, err)
			_, _ = io.ReadAll(resp.Body)
			require.NoError(t, resp.Body.Close())
			zapText = observedText(logs)
		})
		return stdout, zapText
	}

	// Allowlist-only (no key B on the request), then key B present.
	for name, ctx := range map[string]context.Context{
		"allowlist-only": context.Background(),
		"key-b":          outboundCtx(t, "X-Tenant-Id", fwdSentinel),
	} {
		stdout, zapText := run(ctx)
		assert.NotContains(t, stdout, fwdSentinel, name+": stdout")
		assert.NotContains(t, zapText, fwdSentinel, name+": zap")
		assert.Contains(t, stdout, "X-Tenant-Id", name+": the name stays visible")
		assert.Contains(t, stdout, "[forwarded]", name)
		assert.Contains(t, stdout, "visible-value", name+": other headers are untouched")
	}
}

// Spec 112 FR-015b: an upstream that echoes a forwarded value in a response
// BODY (a result or a 500 page, plain JSON or an SSE frame) must not have it
// written to stdout or zap by the trace transport.
func TestLoggingTransport_ScrubsForwardedValueFromBodies(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
	}{
		{"json-error-body", "application/json", `upstream exploded; X-Tenant-Id: ` + fwdSentinel + ` and "v":"` + fwdSentinel + `"`},
		{"sse-frame", "text/event-stream", "event: message\ndata: {\"text\":\"plain " + fwdSentinel + "\"}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			obsCore, logs := observer.New(zapcore.DebugLevel)
			tr := NewLoggingTransport(srv.Client().Transport, zap.New(obsCore))
			var zapText string
			stdout := captureStdout(t, func() {
				req, err := http.NewRequestWithContext(outboundCtx(t, "X-Tenant-Id", fwdSentinel), http.MethodPost, srv.URL+"/mcp", http.NoBody)
				require.NoError(t, err)
				resp, err := tr.RoundTrip(req)
				require.NoError(t, err)
				got, _ := io.ReadAll(resp.Body)
				require.NoError(t, resp.Body.Close())
				assert.Contains(t, string(got), fwdSentinel, "the caller still receives the body unmodified")
				time.Sleep(100 * time.Millisecond) // the SSE reader logs from a goroutine
				zapText = observedText(logs)
			})
			assert.NotContains(t, stdout, fwdSentinel, "stdout")
			assert.NotContains(t, zapText, fwdSentinel, "zap")
		})
	}
}

type failingRT struct{ err error }

func (f failingRT) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

// Spec 112 FR-015b (review round 2): a transport error that quotes a forwarded
// value must not reach stdout or zap through the failed-request log.
func TestLoggingTransport_ScrubsForwardedValueFromTransportError(t *testing.T) {
	obsCore, logs := observer.New(zapcore.DebugLevel)
	tr := NewLoggingTransport(failingRT{err: fmt.Errorf("dial failed while sending tenant %s to upstream", fwdSentinel)}, zap.New(obsCore))
	var zapText string
	stdout := captureStdout(t, func() {
		req, err := http.NewRequestWithContext(outboundCtx(t, "X-Tenant-Id", fwdSentinel), http.MethodPost, "http://upstream.invalid/mcp", http.NoBody)
		require.NoError(t, err)
		_, err = tr.RoundTrip(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), fwdSentinel, "the returned error is untouched")
		zapText = observedText(logs)
	})
	assert.NotContains(t, stdout, fwdSentinel, "stdout")
	assert.NotContains(t, zapText, fwdSentinel, "zap")
}
