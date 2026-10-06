package headerfwd

import (
	"strings"
	"testing"
)

func TestDenied(t *testing.T) {
	denied := []string{
		"Authorization", "Proxy-Authorization", "X-Api-Key", "Cookie", "Set-Cookie", "Forwarded", "X-Real-Ip",
		"X-Forwarded-For", "X-Forwarded-Host", "Host", "Connection", "Keep-Alive", "Proxy-Connection", "Te",
		"Trailer", "Transfer-Encoding", "Upgrade", "Expect", "Proxy-Foo", "Content-Type", "Content-Length",
		"Content-Encoding", "Accept", "Accept-Encoding", "Range", "If-Match", "If-None-Match", "Last-Event-Id",
		"Mcp-Session-Id", "Mcp-Protocol-Version", "Mcp-Method", "Mcp-Param-X", "Traceparent", "Tracestate",
		"Baggage", "X-Request-Id", "X-Mcpproxy-Foo", "X_Forwarded_For", "X_Real_Ip", "X_Api_Key", "Mcp_Session_Id", "Content_Type", "Sec-Fetch-Mode", "User-Agent", "Origin", "Referer",
	}
	for _, n := range denied {
		for _, v := range []string{n, strings.ToLower(n), strings.ToUpper(n)} {
			if !Denied(v) {
				t.Errorf("Denied(%q) = false, want true", v)
			}
		}
	}
	for _, n := range []string{"X-User-Id", "X-Tenant-Id", "Accept-Language", "x-org"} {
		if Denied(n) {
			t.Errorf("Denied(%q) = true, want false", n)
		}
	}
}
