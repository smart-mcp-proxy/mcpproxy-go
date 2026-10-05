package oauth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const testMetaPath = "/.well-known/oauth-authorization-server"

func newEndpointWrapper(t *testing.T, inner http.RoundTripper, extra map[string]string, ep wrapperEndpoints) *OAuthTransportWrapper {
	t.Helper()
	if ep.cache == nil {
		ep.cache = newDiscoveryCache(time.Now)
	}
	w := NewOAuthTransportWrapper(inner, extra, zap.NewNop())
	w.SetEndpoints(ep)
	return w
}

func getJSON(t *testing.T, w http.RoundTripper, rawURL string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	require.NoError(t, err)
	resp, err := w.RoundTrip(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	_ = resp.Body.Close()
	var doc map[string]any
	_ = json.Unmarshal(body, &doc)
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		assert.Equal(t, strconv.Itoa(len(body)), cl, "Content-Length must match the rewritten body")
		assert.EqualValues(t, len(body), resp.ContentLength)
	}
	return resp, doc
}

// T134: metadata rewrite from overrides; unrelated fields preserved.
func TestWrapper_RewritesASMetadataFromOverrides(t *testing.T) {
	var hits int32
	as := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"issuer":"https://idp.example.com","authorization_endpoint":"https://idp.example.com/authorize","token_endpoint":"https://idp.example.com/token","registration_endpoint":"https://idp.example.com/register","code_challenge_methods_supported":["S256"],"scopes_supported":["a","offline_access"]}`))
	}))
	defer as.Close()
	metaURL := as.URL + testMetaPath

	ov := discoveryOverrides{
		authz:        "https://login.example.com/oauth2/v1/authorize",
		token:        "https://login.example.com/oauth2/v1/exchange",
		registration: "https://login.example.com/oauth2/v1/clients",
	}
	w := newEndpointWrapper(t, http.DefaultTransport, nil, wrapperEndpoints{serverURL: "https://mcp.example.com/mcp", overrides: ov, metadataURL: metaURL})

	_, doc := getJSON(t, w, metaURL)
	assert.Equal(t, ov.authz, doc["authorization_endpoint"])
	assert.Equal(t, ov.token, doc["token_endpoint"])
	assert.Equal(t, ov.registration, doc["registration_endpoint"])
	assert.Equal(t, "https://idp.example.com", doc["issuer"], "unrelated fields preserved")
	assert.Equal(t, []any{"S256"}, doc["code_challenge_methods_supported"])

	// A second handler (new wrapper, same cache key) is served from the cache.
	cache := w.endpoints.cache
	w2 := newEndpointWrapper(t, http.DefaultTransport, nil, wrapperEndpoints{serverURL: "https://mcp.example.com/mcp", overrides: ov, metadataURL: metaURL, cache: cache})
	_, doc2 := getJSON(t, w2, metaURL)
	assert.Equal(t, ov.token, doc2["token_endpoint"])
	assert.EqualValues(t, 1, atomic.LoadInt32(&hits), "metadata fetched once, then served from cache")
}

func TestWrapper_SynthesizesMetadataWhenUnreachable(t *testing.T) {
	as := httptest.NewServer(http.NotFoundHandler())
	defer as.Close()
	metaURL := as.URL + testMetaPath

	ov := discoveryOverrides{authz: "https://login.example.com/oauth2/authorize", token: "https://login.example.com/oauth2/token", registration: "https://login.example.com/oauth2/register"}
	w := newEndpointWrapper(t, http.DefaultTransport, nil, wrapperEndpoints{serverURL: "https://mcp.example.com/mcp", overrides: ov, metadataURL: metaURL})
	resp, doc := getJSON(t, w, metaURL)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "https://login.example.com", doc["issuer"])
	assert.Equal(t, ov.authz, doc["authorization_endpoint"])
	assert.Equal(t, ov.token, doc["token_endpoint"])
	assert.Equal(t, ov.registration, doc["registration_endpoint"])
	assert.Equal(t, []any{"code"}, doc["response_types_supported"])

	// Without both authz+token overrides nothing is synthesized: the 404 passes through.
	w2 := newEndpointWrapper(t, http.DefaultTransport, nil, wrapperEndpoints{serverURL: "https://mcp.example.com/mcp", overrides: discoveryOverrides{token: ov.token}, metadataURL: metaURL})
	req, _ := http.NewRequest(http.MethodGet, metaURL, nil)
	resp2, err := w2.RoundTrip(req)
	require.NoError(t, err)
	_ = resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestWrapper_LeavesNonMetadataResponsesUntouched(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_, _ = rw.Write([]byte(`{"token_endpoint":"https://evil.example/token","x":1}`))
	}))
	defer srv.Close()
	ov := discoveryOverrides{token: "https://login.example.com/token"}
	w := newEndpointWrapper(t, http.DefaultTransport, nil, wrapperEndpoints{serverURL: "https://mcp.example.com/mcp", overrides: ov, metadataURL: srv.URL + testMetaPath})
	_, doc := getJSON(t, w, srv.URL+"/some/other/doc")
	assert.Equal(t, "https://evil.example/token", doc["token_endpoint"])
}

// T136 / FR-025: effective-endpoint matching.
func TestWrapper_ExtraParamsFollowDiscoveredTokenEndpoint(t *testing.T) {
	var gotBodies []string
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == testMetaPath {
			_, _ = rw.Write([]byte(`{"issuer":"` + "http://" + r.Host + `","authorization_endpoint":"http://` + r.Host + `/login/start","token_endpoint":"http://` + r.Host + `/oauth2/v1/exchange"}`))
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotPaths = append(gotPaths, r.URL.Path)
		gotBodies = append(gotBodies, string(b))
		rw.WriteHeader(http.StatusCreated)
		_, _ = rw.Write([]byte(`{"access_token":"x","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	w := newEndpointWrapper(t, http.DefaultTransport, map[string]string{"resource": "https://mcp.example.com"},
		wrapperEndpoints{serverURL: "https://mcp.example.com/mcp", metadataURL: srv.URL + testMetaPath})
	_, _ = getJSON(t, w, srv.URL+testMetaPath) // learns the token endpoint

	post := func(path string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader("grant_type=refresh_token"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := w.RoundTrip(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp
	}
	r1 := post("/oauth2/v1/exchange")
	r2 := post("/api/tokens/list")
	require.Len(t, gotBodies, 2)
	assert.Contains(t, gotBodies[0], "resource=")
	assert.NotContains(t, gotBodies[1], "resource=", "/api/tokens/list is not the token endpoint")
	assert.Equal(t, http.StatusOK, r1.StatusCode, "201 -> 200 follows the learned endpoint")
	assert.Equal(t, http.StatusCreated, r2.StatusCode, "no normalization off the token endpoint")
}

func TestWrapper_HeuristicWhenNoEndpointKnown(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		rw.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	w := NewOAuthTransportWrapper(http.DefaultTransport, map[string]string{"resource": "r"}, zap.NewNop())
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/token", strings.NewReader("a=b"))
	resp, err := w.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Contains(t, body, "resource=r")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestWrapper_OverrideTokenEndpointIsMatchedWithoutMetadata(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}))
	defer srv.Close()
	w := newEndpointWrapper(t, http.DefaultTransport, map[string]string{"resource": "r"},
		wrapperEndpoints{serverURL: "https://m.example/mcp", overrides: discoveryOverrides{token: srv.URL + "/custom/exchange"}})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/custom/exchange", strings.NewReader("a=b"))
	resp, err := w.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Contains(t, body, "resource=r")
}

// T150 / FR-027a: redirect policy.
func TestOAuthCheckRedirect(t *testing.T) {
	mk := func(method, from, to string) (*http.Request, []*http.Request) {
		fu, _ := url.Parse(from)
		tu, _ := url.Parse(to)
		return &http.Request{Method: method, URL: tu}, []*http.Request{{Method: method, URL: fu}}
	}
	cases := []struct {
		name         string
		method, a, b string
		wantErr      bool
	}{
		{"https->http public refused", "GET", "https://idp.example.com/x", "http://idp.example.com/y", true},
		{"https->http loopback ok", "GET", "https://idp.example.com/x", "http://127.0.0.1:9/y", false},
		{"cross-origin token POST refused", "POST", "https://idp.example.com/token", "https://evil.example/token", true},
		{"same-origin POST ok", "POST", "https://idp.example.com/token", "https://idp.example.com/oauth2/token", false},
		{"cross-origin GET ok", "GET", "https://idp.example.com/meta", "https://other.example.com/meta", false},
		{"scheme change on a POST is a different origin", "POST", "http://idp.example.com/token", "https://idp.example.com/token", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, via := mk(c.method, c.a, c.b)
			err := oauthCheckRedirect(req, via)
			if c.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
	req, via := mk("GET", "https://a.example/", "https://a.example/b")
	via = append(via, via[0], via[0], via[0], via[0], via[0], via[0], via[0], via[0], via[0], via[0])
	assert.Error(t, oauthCheckRedirect(req, via), "stops after 10 redirects")
}

func TestOAuthHTTPClient_SameOriginRedirectOfTokenPostWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			http.Redirect(rw, r, "/token2", http.StatusTemporaryRedirect)
			return
		}
		b, _ := io.ReadAll(r.Body)
		_, _ = rw.Write(b)
	}))
	defer srv.Close()
	c := &http.Client{Transport: http.DefaultTransport, CheckRedirect: oauthCheckRedirect}
	resp, err := c.Post(srv.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader("a=b"))
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, "a=b", string(b))
}

// T146 / FR-030: URLs the wrapper logs never carry a query string.
func TestWrapper_LogsNeverContainQueryStrings(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	as := httptest.NewServer(http.NotFoundHandler())
	defer as.Close()
	metaURL := as.URL + testMetaPath + "?token=SECRETVALUE123"
	ov := discoveryOverrides{authz: "https://login.example.com/authorize", token: "https://login.example.com/token"}
	w := NewOAuthTransportWrapper(http.DefaultTransport, nil, zap.New(core))
	w.SetEndpoints(wrapperEndpoints{serverURL: "https://mcp.example.com/mcp?apikey=SECRETVALUE123", overrides: ov, metadataURL: metaURL, cache: newDiscoveryCache(time.Now)})
	_, _ = getJSON(t, w, metaURL)
	require.NotZero(t, logs.Len())
	for _, e := range logs.All() {
		assert.NotContains(t, e.Message, "SECRETVALUE123")
		for _, f := range e.Context {
			assert.NotContains(t, f.String, "SECRETVALUE123")
		}
	}
}
