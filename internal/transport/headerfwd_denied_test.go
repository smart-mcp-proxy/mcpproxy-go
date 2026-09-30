package transport

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/headerfwd"
)

// Spec 112 SC-002 / US3: for every name in FR-004, in three casings, listed in
// an allowlist and present on the inbound request, nothing reaches the upstream
// on any CreateHTTPClient branch (plain, static-header, trace, OAuth), and the
// OAuth upstream always receives the proxy-obtained token, never the inbound
// Authorization.
func TestCreateHTTPClient_DeniedNamesNeverReachUpstream(t *testing.T) {
	denied := []string{
		"Authorization", "Proxy-Authorization", "X-Api-Key", "Cookie", "Set-Cookie", "Forwarded", "X-Real-Ip",
		"Host", "Connection", "Keep-Alive", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Expect",
		"Content-Type", "Content-Length", "Accept", "Accept-Encoding", "Range", "Last-Event-Id",
		"Traceparent", "Tracestate", "Baggage", "X-Request-Id", "User-Agent", "Origin", "Referer",
		"X-Forwarded-For", "X-Forwarded-Host", "If-Match", "Mcp-Session-Id", "Mcp-Protocol-Version",
		"X-Mcpproxy-Debug", "Sec-Fetch-Site",
	}
	for _, b := range branches() {
		t.Run(b.name, func(t *testing.T) {
			for _, name := range denied {
				for _, casing := range []string{name, strings.ToLower(name), strings.ToUpper(name)} {
					sentinel := fmt.Sprintf("DENIED-%s-%s", b.name, strings.ToLower(name))

					// The full pipeline as the proxy runs it: capture at the edge,
					// derive the per-server outbound set, hand it to the transport.
					r := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
					r.Header[casing] = []string{sentinel}
					r.Header.Set("X-Tenant-Id", "tenant-ok")
					union := map[string]struct{}{casing: {}, "X-Tenant-Id": {}}
					snap := headerfwd.Capture(r, union)
					out := headerfwd.Outbound(snap, headerfwd.Policy{
						Enabled: true, Allow: []string{casing, "X-Tenant-Id"}, Transport: "http",
					})
					ctx := headerfwd.WithOutbound(t.Context(), out)

					rec := &recorder{}
					srv := rpcServer(rec, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
					c, err := CreateHTTPClient(b.cfg(srv.URL + "/mcp"))
					require.NoError(t, err)
					callTool(t, c, ctx)
					_ = c.Close()
					srv.Close()

					var call http.Header
					for _, h := range rec.all() {
						for k, vs := range h {
							for _, v := range vs {
								assert.NotContains(t, v, sentinel, "%s: denied %q (%s) leaked in %s", b.name, name, casing, k)
							}
						}
						if h.Get("X-Tenant-Id") == "tenant-ok" {
							call = h
						}
					}
					require.NotNil(t, call, "%s/%s: the legitimate allowlisted name must still be forwarded", b.name, casing)
					if b.oauth {
						assert.Equal(t, "Bearer test-access-token", call.Get("Authorization"),
							"%s: the upstream receives the proxy's token, never the inbound one", b.name)
					}
				}
			}
		})
	}
}
