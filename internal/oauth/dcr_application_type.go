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
// Dynamic Client Registration request. mcp-go's body is a few hundred bytes
// plus the configured scopes; the bound leaves room for any real scope list.
const maxRegistrationBodyBytes = 1 << 20

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
// would be registered as "web". The request is recognised by its body: a JSON
// object carrying redirect_uris, application_type, client_name and
// grant_types, which is the RFC 7591 request mcp-go builds (the wrapper's
// client belongs to one OAuth handler, whose only other POSTs are form-encoded
// token requests, never JSON). A POST to the known token endpoint, any other
// request, and any body that cannot be read within maxRegistrationBodyBytes
// is left alone and reaches the server unchanged, read errors included.
// The request is never retried without the field (FR-016).
func (w *OAuthTransportWrapper) rewriteRegistrationApplicationType(req *http.Request) *http.Request {
	if req.Method != http.MethodPost || req.Body == nil || req.ContentLength <= 0 || req.ContentLength > maxRegistrationBodyBytes {
		return req
	}
	if mt, _, err := mime.ParseMediaType(req.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return req
	}
	w.mu.RLock()
	tokenURL := w.tokenURL
	w.mu.RUnlock()
	if tokenURL != nil && urlEndpointMatch(tokenURL, req.URL) {
		return req
	}
	orig := req.Body
	raw, err := io.ReadAll(io.LimitReader(orig, maxRegistrationBodyBytes+1))
	restore := func() *http.Request {
		req.Body = replayRequestBody(raw, err, orig)
		return req
	}
	if err != nil || int64(len(raw)) != req.ContentLength {
		return restore()
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		return restore()
	}
	for _, k := range []string{"application_type", "client_name", "grant_types"} {
		if _, ok := body[k]; !ok {
			return restore()
		}
	}
	var redirectURIs []string
	var current string
	if json.Unmarshal(body["redirect_uris"], &redirectURIs) != nil || len(redirectURIs) == 0 ||
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
	_ = orig.Close()
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(out))
	clone.ContentLength = int64(len(out))
	clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(out)), nil }
	w.logger.Debug("Corrected application_type on the client registration request",
		zap.String("url", logSafeURL(req.URL.String())),
		zap.String("from", current), zap.String("to", want))
	return clone
}

type replayReadCloser struct {
	io.Reader
	io.Closer
}

// errAfterPrefix returns err once its prefix has been read.
type errAfterPrefix struct {
	prefix *bytes.Reader
	err    error
}

func (e *errAfterPrefix) Read(p []byte) (int, error) {
	if e.prefix.Len() > 0 {
		return e.prefix.Read(p)
	}
	return 0, e.err
}

// replayRequestBody rebuilds a body after raw was read from orig: the same
// bytes, then the rest of orig, or, when the read failed, the same bytes and
// then that error rather than a plain EOF.
func replayRequestBody(raw []byte, err error, orig io.ReadCloser) io.ReadCloser {
	if err != nil {
		return replayReadCloser{Reader: &errAfterPrefix{prefix: bytes.NewReader(raw), err: err}, Closer: orig}
	}
	return replayReadCloser{Reader: io.MultiReader(bytes.NewReader(raw), orig), Closer: orig}
}
