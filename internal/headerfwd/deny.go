package headerfwd

import (
	"net/http"
	"strings"
)

// Size limits (FR-012).
const (
	MaxValueBytes = 4 << 10
	MaxTotalBytes = 16 << 10
	MaxAllowNames = 32
)

var deniedExact = map[string]struct{}{
	// Credentials and identity.
	"authorization": {}, "proxy-authorization": {}, "x-api-key": {}, "cookie": {},
	"set-cookie": {}, "forwarded": {}, "x-real-ip": {},
	// Host and hop-by-hop.
	"host": {}, "connection": {}, "keep-alive": {}, "proxy-connection": {}, "te": {},
	"trailer": {}, "transfer-encoding": {}, "upgrade": {}, "expect": {},
	// Transport-managed.
	"content-type": {}, "content-length": {}, "accept": {}, "accept-encoding": {},
	"range": {}, "last-event-id": {},
	// Tracing and proxy-managed.
	"traceparent": {}, "tracestate": {}, "baggage": {}, "x-request-id": {},
	// Logged by mcpproxy's access log or host validation.
	"user-agent": {}, "origin": {}, "referer": {},
}

var deniedPrefixes = []string{
	"x-forwarded-", "proxy-", "content-", "if-", "mcp-", "x-mcpproxy-", "sec-",
}

// Denied reports whether name must never be forwarded (FR-004). The comparison
// is case-insensitive.
func Denied(name string) bool {
	// CGI-style upstreams fold "_" and "-" together, so X_Real_Ip is X-Real-Ip.
	l := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "_", "-")
	if l == "" {
		return true
	}
	if _, ok := deniedExact[l]; ok {
		return true
	}
	for _, p := range deniedPrefixes {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

func canon(name string) string { return http.CanonicalHeaderKey(strings.TrimSpace(name)) }
