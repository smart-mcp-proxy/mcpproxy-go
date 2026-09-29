package transport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/headerfwd"
)

// maxGateBody bounds how much of a request body the gate will buffer. Bodies
// above it cannot be a legitimate small in-memory JSON-RPC message the gate
// needs to inspect, so forwarded headers are stripped (fail closed).
const maxGateBody = 8 << 20

// forwardGateTransport enforces Spec 112 FR-008 below mcp-go. mcp-go reuses the
// tools/call context (which carries the outbound forwarded set, key B) for the
// reply POSTs to server-initiated requests (ping, sampling, elicitation, roots,
// method-not-found) received inside the tools/call SSE stream, so HeaderFunc
// attaches forwarded headers to those too. This layer removes every forwarded
// header name from any request whose ctx carries key B unless the body is a
// single JSON-RPC request object with method exactly "tools/call".
type forwardGateTransport struct {
	base http.RoundTripper
}

func newForwardGateTransport(base http.RoundTripper) http.RoundTripper {
	return &forwardGateTransport{base: base}
}

func (g *forwardGateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	out, ok := headerfwd.OutboundFrom(req.Context())
	if !ok || out.IsEmpty() || !hasAnyHeader(req.Header, out.Names()) {
		return g.base.RoundTrip(req)
	}
	allow := false
	body, ok := readGateBody(req)
	if ok {
		allow = isToolsCallBody(body)
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	if !allow {
		req = req.Clone(req.Context())
		for _, n := range out.Names() {
			req.Header.Del(n)
		}
	}
	return g.base.RoundTrip(req)
}

func hasAnyHeader(h http.Header, names []string) bool {
	for _, n := range names {
		if _, ok := h[http.CanonicalHeaderKey(n)]; ok {
			return true
		}
	}
	return false
}

// readGateBody returns the request body bytes without consuming the original
// (GetBody when available, otherwise buffer and restore req.Body).
func readGateBody(req *http.Request) ([]byte, bool) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, true
	}
	var rc io.ReadCloser
	if req.GetBody != nil {
		var err error
		if rc, err = req.GetBody(); err != nil {
			return nil, false
		}
	} else {
		rc = req.Body
	}
	b, err := io.ReadAll(io.LimitReader(rc, maxGateBody+1))
	_ = rc.Close()
	if err != nil || len(b) > maxGateBody {
		return nil, false
	}
	if req.GetBody == nil {
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	return b, true
}

// isToolsCallBody reports whether body is a single JSON-RPC request object
// (has an id) whose method is exactly "tools/call". Batches, responses,
// notifications and anything unparseable are false.
func isToolsCallBody(body []byte) bool {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return false
	}
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	return m.Method == "tools/call" && len(m.ID) > 0 && string(m.ID) != "null"
}
