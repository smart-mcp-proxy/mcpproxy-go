package oauth

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"go.uber.org/zap"
)

// maxRegistrationBodyBytes bounds the request body the wrapper inspects for a
// Dynamic Client Registration request. mcp-go's body is a few hundred bytes.
const maxRegistrationBodyBytes = 64 << 10

// applicationTypeFor is the OpenID Connect DCR application_type for one
// redirect URI (Spec 135 FR-016, research R4): "native" for an http redirect
// URI whose host config.IsLoopbackHost accepts (all of 127.0.0.0/8, ::1,
// localhost), "web" for everything else. OIDC DCR §2 lets a native client
// register only loopback http (or custom-scheme) redirect URIs, so an https
// loopback URI is "web", as mcp-go also decides.
func applicationTypeFor(redirectURI string) string {
	u, err := url.Parse(redirectURI)
	if err != nil || !strings.EqualFold(u.Scheme, "http") {
		return "web"
	}
	if config.IsLoopbackHost(u.Hostname()) {
		return "native"
	}
	return "web"
}

// rewriteRegistrationApplicationType corrects application_type on mcp-go's
// Dynamic Client Registration request. mcp-go (v1.6) maps only 127.0.0.1,
// ::1 and localhost to "native", so a redirect URI on another 127/8 address
// would be registered as "web". The request is recognised by its body, not
// its URL: a JSON object carrying both redirect_uris and application_type,
// which is the RFC 7591 request mcp-go builds. Any other request, and any
// body that cannot be read within maxRegistrationBodyBytes, is left alone.
// The request is never retried without the field (FR-016).
func (w *OAuthTransportWrapper) rewriteRegistrationApplicationType(req *http.Request) *http.Request {
	if req.Method != http.MethodPost || req.Body == nil || req.ContentLength <= 0 || req.ContentLength > maxRegistrationBodyBytes {
		return req
	}
	if mt, _, err := mime.ParseMediaType(req.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return req
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, maxRegistrationBodyBytes+1))
	_ = req.Body.Close()
	restore := func() *http.Request {
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return req
	}
	if err != nil || int64(len(raw)) != req.ContentLength {
		return restore()
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		return restore()
	}
	var redirectURIs []string
	var current string
	if _, ok := body["application_type"]; !ok ||
		json.Unmarshal(body["redirect_uris"], &redirectURIs) != nil || len(redirectURIs) == 0 ||
		json.Unmarshal(body["application_type"], &current) != nil {
		return restore()
	}
	want := "native"
	for _, ru := range redirectURIs {
		if applicationTypeFor(ru) != "native" {
			want = "web"
			break
		}
	}
	if want == current {
		return restore()
	}
	body["application_type"], _ = json.Marshal(want)
	out, err := json.Marshal(body)
	if err != nil {
		return restore()
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(out))
	clone.ContentLength = int64(len(out))
	clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(out)), nil }
	w.logger.Debug("Corrected application_type on the client registration request",
		zap.String("url", logSafeURL(req.URL.String())),
		zap.String("from", current), zap.String("to", want))
	return clone
}
