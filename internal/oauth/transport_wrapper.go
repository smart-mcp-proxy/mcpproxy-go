package oauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// wrapperEndpoints is the Spec 113-b discovery context of one wrapper: the
// per-server endpoint overrides, the AS metadata URL mcp-go will fetch, and the
// cache that serves it.
type wrapperEndpoints struct {
	serverURL   string
	overrides   discoveryOverrides
	metadataURL string
	cache       *discoveryCache
}

// OAuthTransportWrapper wraps an HTTP RoundTripper to inject extra OAuth parameters
// into authorization and token requests without modifying the mcp-go library.
//
// This wrapper intercepts HTTP requests and adds custom parameters to:
// - Authorization requests (query parameters)
// - Token exchange requests (form body parameters)
// - Token refresh requests (form body parameters)
//
// The wrapper is stateless and thread-safe for concurrent use.
type OAuthTransportWrapper struct {
	// inner is the wrapped HTTP RoundTripper (typically http.DefaultTransport)
	inner http.RoundTripper

	// extraParams contains the additional OAuth parameters to inject
	// (e.g., RFC 8707 "resource" parameter for Runlayer integration)
	extraParams map[string]string

	// logger for DEBUG level logging of parameter injection
	logger *zap.Logger

	// endpoints and the learned endpoint URLs drive Spec 113-b metadata rewriting
	// and FR-025 endpoint-URL matching. mu guards the learned URLs, which are
	// written from RoundTrip.
	endpoints wrapperEndpoints
	mu        sync.RWMutex
	tokenURL  *url.URL
	authzURL  *url.URL
}

// SetEndpoints configures discovery overrides for this wrapper. Call before the
// wrapper is used by a client.
func (w *OAuthTransportWrapper) SetEndpoints(ep wrapperEndpoints) {
	if ep.cache == nil {
		ep.cache = globalDiscoveryCache
	}
	w.endpoints = ep
	w.learnEndpoints(ep.overrides.authz, ep.overrides.token)
}

// learnEndpoints records the effective authorization / token endpoint URLs so
// extra_params injection and 201 normalization match on them (FR-025). Empty or
// unparsable values are ignored.
func (w *OAuthTransportWrapper) learnEndpoints(authz, token string) {
	parse := func(raw string) *url.URL {
		if raw == "" {
			return nil
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return nil
		}
		return u
	}
	a, t := parse(authz), parse(token)
	w.mu.Lock()
	if a != nil {
		w.authzURL = a
	}
	if t != nil {
		w.tokenURL = t
	}
	w.mu.Unlock()
}

// urlEndpointMatch compares scheme, host (with the scheme's default port) and path.
func urlEndpointMatch(a, b *url.URL) bool {
	norm := func(u *url.URL) (string, string, string) {
		port := u.Port()
		if port == "" {
			if strings.EqualFold(u.Scheme, "https") {
				port = "443"
			} else {
				port = "80"
			}
		}
		return strings.ToLower(u.Scheme), strings.ToLower(u.Hostname()) + ":" + port, strings.TrimSuffix(u.Path, "/")
	}
	as, ah, ap := norm(a)
	bs, bh, bp := norm(b)
	return as == bs && ah == bh && ap == bp
}

func (w *OAuthTransportWrapper) matchesToken(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	w.mu.RLock()
	known := w.tokenURL
	w.mu.RUnlock()
	if known != nil {
		return urlEndpointMatch(known, req.URL)
	}
	return isTokenRequest(req)
}

func (w *OAuthTransportWrapper) matchesAuthorization(req *http.Request) bool {
	w.mu.RLock()
	known := w.authzURL
	w.mu.RUnlock()
	if known != nil {
		return urlEndpointMatch(known, req.URL)
	}
	return isAuthorizationRequest(req)
}

// NewOAuthTransportWrapper creates a new transport wrapper that injects extra params.
//
// Parameters:
//   - transport: The base HTTP RoundTripper to wrap (use http.DefaultTransport if nil)
//   - extraParams: Map of extra parameters to inject into OAuth requests
//   - logger: Logger for debug output (uses zap.L() if nil)
//
// Returns a wrapper that can be used as http.Client.Transport.
func NewOAuthTransportWrapper(transport http.RoundTripper, extraParams map[string]string, logger *zap.Logger) *OAuthTransportWrapper {
	if transport == nil {
		transport = http.DefaultTransport
	}
	if logger == nil {
		logger = zap.L().Named("oauth-wrapper")
	}

	// Make a copy to avoid external modifications
	params := make(map[string]string, len(extraParams))
	for k, v := range extraParams {
		params[k] = v
	}

	return &OAuthTransportWrapper{
		inner:       transport,
		extraParams: params,
		logger:      logger,
	}
}

// RoundTrip implements http.RoundTripper by intercepting requests and injecting extra params.
//
// This method:
// 1. Detects OAuth authorization and token requests by URL path
// 2. Clones the request to avoid modifying the original
// 3. Injects extra parameters into query string (authorization) or body (token)
// 4. Delegates to the wrapped transport for actual HTTP execution
// 5. Normalizes HTTP 201 responses to 200 for token requests (some providers like Supabase return 201)
// 6. Logs parameter injection at DEBUG level for observability
func (w *OAuthTransportWrapper) RoundTrip(req *http.Request) (*http.Response, error) {
	if w.isMetadataRequest(req) {
		if resp, err, handled := w.roundTripMetadata(req); handled {
			return resp, err
		}
	}

	tokenReq := w.matchesToken(req)

	if len(w.extraParams) > 0 {
		// Clone request to avoid modifying original
		clonedReq := req.Clone(req.Context())

		// Detect OAuth endpoint type and inject params appropriately
		if w.matchesAuthorization(req) {
			w.injectQueryParams(clonedReq)
		} else if tokenReq {
			w.injectFormParams(clonedReq)
		}

		resp, err := w.inner.RoundTrip(clonedReq)
		if err != nil {
			return resp, err
		}

		if tokenReq {
			w.normalizeTokenResponse(req, resp)
		}

		return resp, nil
	}

	resp, err := w.inner.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	if tokenReq {
		w.normalizeTokenResponse(req, resp)
	}

	return resp, nil
}

// maxTokenResponseBodyBytes caps how much of a token response body is buffered
// for normalization. Larger bodies are passed through unchanged.
const maxTokenResponseBodyBytes = 1 << 20

// normalizeTokenResponse post-processes a token endpoint response (code
// exchange or refresh; mcp-go uses the same HTTP client for both) so mcp-go can
// decode it:
//   - 201 Created becomes 200 OK (some providers, e.g. Supabase, return 201
//     but mcp-go only accepts 200);
//   - an "expires_at" that is not an RFC 3339 string (a numeric epoch, null or
//     any other string) is dropped from 2xx JSON bodies, and "expires_in" is
//     derived from a numeric epoch when it is missing (mcp-go decodes
//     "expires_at" into a time.Time and otherwise fails with
//     "Time.UnmarshalJSON: input is not a JSON string"; see
//     normalizeTokenResponseBody).
func (w *OAuthTransportWrapper) normalizeTokenResponse(req *http.Request, resp *http.Response) {
	if resp.StatusCode == http.StatusCreated {
		w.logger.Debug("Normalized token response status 201→200",
			zap.String("url", logSafeURL(req.URL.String())))
		resp.StatusCode = http.StatusOK
		resp.Status = "200 OK"
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 || resp.Body == nil {
		return
	}
	// A still-encoded body (the transport did not transparently decode it) cannot be parsed here.
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		return
	}

	orig := resp.Body
	buf, err := io.ReadAll(io.LimitReader(orig, maxTokenResponseBodyBytes+1))
	if err != nil || len(buf) > maxTokenResponseBodyBytes {
		// Hand mcp-go exactly what it would have seen: what we read so far,
		// then the rest of the stream (including a read error, if any).
		resp.Body = &prefixedReadCloser{
			Reader: io.MultiReader(bytes.NewReader(buf), orig),
			Closer: orig,
		}
		return
	}
	_ = orig.Close()

	out, changed := normalizeTokenResponseBody(buf, time.Now())
	resp.Body = io.NopCloser(bytes.NewReader(out))
	if !changed {
		return
	}
	resp.ContentLength = int64(len(out))
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	w.logger.Debug("Normalized token response body: dropped non-RFC3339 expires_at",
		zap.String("url", logSafeURL(req.URL.String())))
}

// prefixedReadCloser reads from Reader and closes Closer.
type prefixedReadCloser struct {
	io.Reader
	io.Closer
}

// Bounds for an "expires_in" derived from a numeric "expires_at".
const (
	// epochMillisThreshold: a numeric epoch above this (year ~5138 in seconds)
	// is taken to be in milliseconds, as JS-based providers send. A value still
	// above it after that conversion is not a plausible timestamp and derives
	// no expires_in.
	epochMillisThreshold = 1e11
	// maxDerivedExpiresInSecs caps the derived lifetime (10 years) so mcp-go's
	// time.Duration(ExpiresIn)*time.Second cannot overflow or land far in the future.
	maxDerivedExpiresInSecs = 10 * 366 * 24 * 3600
)

// normalizeTokenResponseBody makes a token response JSON body decodable by
// mcp-go's transport.Token, whose "expires_at" is a time.Time decoded with
// time.Time.UnmarshalJSON (RFC 3339 string only). Some providers (e.g. Better
// Auth) send a numeric Unix epoch, null, or another string form alongside the
// RFC 6749 "expires_in".
//
// If "expires_at" is present and is not an RFC 3339 JSON string it is dropped.
// When it was a number (seconds, or milliseconds above 1e11) or a quoted
// all-digit epoch, and "expires_in" is absent or <= 0, "expires_in" is derived
// as clamp(expires_at - now, 0, 10 years) seconds; implausible epochs derive
// nothing. All other values are carried over verbatim as json.RawMessage. It
// returns the input untouched, with changed=false, when the body is not a JSON
// object or needs no change.
func normalizeTokenResponseBody(body []byte, now time.Time) ([]byte, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil || m == nil {
		return body, false
	}
	rawAt, ok := m["expires_at"]
	if !ok {
		return body, false
	}
	at := bytes.TrimSpace(rawAt)
	if len(at) > 0 && at[0] == '"' {
		// Same decoder mcp-go uses: leave only what it can parse.
		var t time.Time
		if t.UnmarshalJSON(at) == nil {
			return body, false
		}
	}
	delete(m, "expires_at")

	var epoch float64
	haveEpoch := false
	switch {
	case bytes.Equal(at, []byte("null")):
	case len(at) > 0 && at[0] == '"':
		var str string
		if json.Unmarshal(at, &str) == nil && isAllDigits(str) {
			if f, err := strconv.ParseFloat(str, 64); err == nil {
				epoch, haveEpoch = f, true
			}
		}
	default:
		haveEpoch = json.Unmarshal(at, &epoch) == nil
	}

	if haveEpoch {
		var expiresIn float64
		rawIn, hasIn := m["expires_in"]
		inOK := !hasIn || json.Unmarshal(rawIn, &expiresIn) == nil // null decodes to 0
		if inOK && expiresIn <= 0 {
			if epoch > epochMillisThreshold {
				epoch /= 1000
			}
			if epoch <= epochMillisThreshold {
				secs := int64(0)
				if epoch > 0 {
					secs = int64(epoch) - now.Unix()
				}
				if secs < 0 {
					secs = 0
				}
				if secs > maxDerivedExpiresInSecs {
					secs = maxDerivedExpiresInSecs
				}
				m["expires_in"] = json.RawMessage(strconv.FormatInt(secs, 10))
			}
		}
	}

	// Encode without HTML escaping so values such as URLs with "&" stay as sent.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return body, false
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isAuthorizationRequest detects if this is an OAuth authorization request
// by checking for common authorization endpoint patterns.
func isAuthorizationRequest(req *http.Request) bool {
	path := req.URL.Path
	// Common OAuth authorization endpoint paths
	return contains(path, "/authorize") || contains(path, "/oauth/authorize")
}

// isTokenRequest detects if this is an OAuth token request (exchange or refresh)
// by checking for token endpoint patterns and POST method.
func isTokenRequest(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	path := req.URL.Path
	// Common OAuth token endpoint paths
	return contains(path, "/token") || contains(path, "/oauth/token")
}

// injectQueryParams adds extra parameters to the authorization URL query string.
//
// This is used for OAuth authorization requests where params are sent as
// URL query parameters (e.g., /authorize?response_type=code&resource=...).
func (w *OAuthTransportWrapper) injectQueryParams(req *http.Request) {
	q := req.URL.Query()

	for k, v := range w.extraParams {
		q.Set(k, v)
	}

	req.URL.RawQuery = q.Encode()

	// Log at DEBUG level with selective masking
	masked := maskExtraParams(w.extraParams)
	w.logger.Debug("Injected extra params into authorization URL",
		zap.String("url", logSafeURL(req.URL.String())),
		zap.Any("extra_params", masked))
}

// injectFormParams adds extra parameters to token request form body.
//
// This is used for OAuth token exchange and refresh requests where params
// are sent as application/x-www-form-urlencoded body parameters.
func (w *OAuthTransportWrapper) injectFormParams(req *http.Request) {
	// Read existing body
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		w.logger.Warn("Failed to read token request body for extra params injection",
			zap.Error(err))
		return
	}
	req.Body.Close()

	// Parse form values
	values, err := url.ParseQuery(string(bodyBytes))
	if err != nil {
		w.logger.Warn("Failed to parse token request form body",
			zap.Error(err))
		// Restore original body
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return
	}

	// Add extra params
	for k, v := range w.extraParams {
		values.Set(k, v)
	}

	// Encode modified form and update request
	newBody := values.Encode()
	req.Body = io.NopCloser(bytes.NewBufferString(newBody))
	req.ContentLength = int64(len(newBody))

	// Ensure correct content type
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	// Log at DEBUG level with selective masking
	masked := maskExtraParams(w.extraParams)
	w.logger.Debug("Injected extra params into token request body",
		zap.String("url", logSafeURL(req.URL.String())),
		zap.Any("extra_params", masked))
}

// contains is a helper to check if a string contains a substring (case-sensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(bytes.Contains([]byte(s), []byte(substr))))
}

// isMetadataRequest reports whether req is the GET mcp-go issues for the
// authorization server metadata document this wrapper was configured with.
func (w *OAuthTransportWrapper) isMetadataRequest(req *http.Request) bool {
	if req.Method != http.MethodGet || w.endpoints.metadataURL == "" {
		return false
	}
	return req.URL.String() == w.endpoints.metadataURL
}

// roundTripMetadata serves the AS metadata GET (Spec 113 FR-024, FR-026): the
// document comes from the discovery cache (one fetch shared with the preflight),
// the override fields are written over it, and the learned endpoints feed
// FR-025 matching. When the document cannot be fetched and the overrides cover
// authorization_endpoint and token_endpoint, a synthetic document is served so
// mcp-go never falls back to its /authorize, /token, /register defaults.
// handled=false means "forward the request untouched".
func (w *OAuthTransportWrapper) roundTripMetadata(req *http.Request) (*http.Response, error, bool) {
	ep := w.endpoints
	doc, err := cachedASMetadataDoc(ep.cache, ep.serverURL, ep.overrides, ep.metadataURL, func() ([]byte, error) {
		// Follow redirects (under the OAuth redirect policy) so a metadata URL
		// that redirects is fetched, cached and rewritten like a direct one.
		hc := &http.Client{Transport: w.inner, CheckRedirect: oauthCheckRedirect, Timeout: 30 * time.Second}
		resp, err := hc.Do(req.Clone(req.Context()))
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, errHTTPStatus(resp.StatusCode)
		}
		return readBoundedDoc(resp.Body)
	})

	var body []byte
	switch {
	case err == nil:
		rewritten, ok := w.rewriteMetadata(doc.raw)
		if !ok {
			return nil, nil, false // not a JSON object: leave it to mcp-go untouched
		}
		body = rewritten
	case ep.overrides.authz != "" && ep.overrides.token != "":
		synth, serr := w.synthesizeMetadata()
		if serr != nil {
			return nil, nil, false
		}
		w.logger.Info("Authorization server metadata unreachable; serving a document built from the endpoint overrides",
			zap.String("metadata_url", logSafeURL(ep.metadataURL)),
			zap.String("authorization_endpoint", logSafeURL(ep.overrides.authz)),
			zap.String("token_endpoint", logSafeURL(ep.overrides.token)))
		body = synth
		var m map[string]any
		if json.Unmarshal(body, &m) == nil {
			w.learnFromDoc(m)
		}
	default:
		// Unreachable and nothing to synthesize from. The failure is cached for
		// 30s by the discovery cache, so answer it from the cache too instead
		// of re-fetching: a status failure replays its status code, a
		// transport failure replays the error.
		var st errHTTPStatus
		if errors.As(err, &st) {
			return &http.Response{
				Status:     strconv.Itoa(int(st)) + " " + http.StatusText(int(st)),
				StatusCode: int(st),
				Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
				Header:  make(http.Header),
				Body:    io.NopCloser(bytes.NewReader(nil)),
				Request: req,
			}, nil, true
		}
		return nil, err, true
	}

	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        h,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil, true
}

type errHTTPStatus int

func (e errHTTPStatus) Error() string {
	return "HTTP " + strconv.Itoa(int(e)) + ": " + http.StatusText(int(e))
}

// rewriteMetadata overwrites the override fields in raw and learns the effective
// endpoints from the result. ok=false when raw is not a JSON object.
func (w *OAuthTransportWrapper) rewriteMetadata(raw []byte) ([]byte, bool) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, false
	}
	ov := w.endpoints.overrides
	changed := false
	for k, v := range map[string]string{
		"authorization_endpoint": ov.authz,
		"token_endpoint":         ov.token,
		"registration_endpoint":  ov.registration,
	} {
		if v != "" {
			m[k] = v
			changed = true
		}
	}
	w.learnFromDoc(m)
	if !changed {
		return raw, true
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, false
	}
	return out, true
}

func (w *OAuthTransportWrapper) learnFromDoc(m map[string]any) {
	authz, _ := m["authorization_endpoint"].(string)
	token, _ := m["token_endpoint"].(string)
	w.learnEndpoints(authz, token)
}

// synthesizeMetadata builds an RFC 8414 document from the endpoint overrides.
// The issuer is the authorization endpoint's origin.
func (w *OAuthTransportWrapper) synthesizeMetadata() ([]byte, error) {
	ov := w.endpoints.overrides
	au, err := url.Parse(ov.authz)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{
		"issuer":                                au.Scheme + "://" + au.Host,
		"authorization_endpoint":                ov.authz,
		"token_endpoint":                        ov.token,
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post", "client_secret_basic"},
	}
	if ov.registration != "" {
		doc["registration_endpoint"] = ov.registration
	}
	return json.Marshal(doc)
}
