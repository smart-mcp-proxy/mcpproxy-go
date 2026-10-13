package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
)

// Spec 107 FR-027 (T047/T050): the swagger server URL and the request-metadata
// tag honour X-Forwarded-* only from a trusted proxy, and both read the
// trusted list through a LIVE provider — mutating the provider's list after
// construction changes the next request's answer without a rebuild.

type liveTrusted struct {
	mu   sync.Mutex
	list []string
}

func (l *liveTrusted) set(list []string) { l.mu.Lock(); l.list = list; l.mu.Unlock() }
func (l *liveTrusted) get() []string     { l.mu.Lock(); defer l.mu.Unlock(); return l.list }

func swaggerServerURL(t *testing.T, h http.Handler, headers map[string]string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://origin.example:8080/swagger/doc.json", http.NoBody)
	req.RemoteAddr = "127.0.0.1:5555"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var spec struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &spec))
	require.NotEmpty(t, spec.Servers)
	return spec.Servers[0].URL
}

func TestSwaggerServerURL_ForwardedOnlyFromTrustedProxyAndLive(t *testing.T) {
	live := &liveTrusted{}
	h := SetupSwaggerHandler(zap.NewNop().Sugar(), live.get)
	forwarded := map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "api.example.com"}

	assert.Equal(t, "http://origin.example:8080", swaggerServerURL(t, h, forwarded), "untrusted peer: headers ignored")

	live.set([]string{"127.0.0.1/32"})
	assert.Equal(t, "https://api.example.com", swaggerServerURL(t, h, forwarded), "trusted peer: headers honoured, read live")

	live.set(nil)
	assert.Equal(t, "http://origin.example:8080", swaggerServerURL(t, h, forwarded), "back to untrusted without a rebuild")

	nilProvider := SetupSwaggerHandler(zap.NewNop().Sugar(), nil)
	assert.Equal(t, "http://origin.example:8080", swaggerServerURL(t, nilProvider, forwarded), "nil provider trusts nobody")
}

func TestTagRequestMeta_MountFixedAndClientIPFromTrustedPeerOnly(t *testing.T) {
	live := &liveTrusted{}
	var seen reqcontext.RequestMeta
	var ok bool
	capture := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, ok = reqcontext.GetRequestMeta(r.Context())
	})

	do := func(h http.Handler, xff string) {
		req := httptest.NewRequest(http.MethodPost, "/x", http.NoBody)
		req.RemoteAddr = "127.0.0.1:4444"
		req.Header.Set("X-Forwarded-For", xff)
		req.Header.Set("X-Mount", "api") // a header must never pick the mount
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	mcp := TagRequestMeta(reqcontext.MountMCP, live.get)(capture)
	do(mcp, "1.2.3.4")
	require.True(t, ok)
	assert.Equal(t, reqcontext.MountMCP, seen.Mount)
	assert.Equal(t, "127.0.0.1", seen.ClientIP, "untrusted peer: RemoteAddr")

	live.set([]string{"127.0.0.1/32"})
	do(mcp, "1.2.3.4")
	assert.Equal(t, "1.2.3.4", seen.ClientIP, "trusted peer, read live")

	api := TagRequestMeta(reqcontext.MountAPI, live.get)(capture)
	do(api, "5.6.7.8, 127.0.0.1")
	assert.Equal(t, reqcontext.MountAPI, seen.Mount)
	assert.Equal(t, "5.6.7.8", seen.ClientIP, "right-most untrusted hop")

	var _ config.TrustedProxiesProvider = live.get
}
