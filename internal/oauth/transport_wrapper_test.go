package oauth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockRoundTripper is a test double that captures requests
type mockRoundTripper struct {
	lastRequest *http.Request
	response    *http.Response
	err         error
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Capture the request for assertion
	m.lastRequest = req
	if m.response != nil {
		return m.response, m.err
	}
	// Default response
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"access_token":"test"}`)),
		Header:     make(http.Header),
	}, nil
}

func TestNewOAuthTransportWrapper(t *testing.T) {
	t.Run("creates wrapper with provided transport", func(t *testing.T) {
		inner := &mockRoundTripper{}
		params := map[string]string{"resource": "https://example.com/mcp"}

		wrapper := NewOAuthTransportWrapper(inner, params, nil)

		assert.NotNil(t, wrapper)
		assert.Equal(t, inner, wrapper.inner)
		assert.Equal(t, params, wrapper.extraParams)
	})

	t.Run("uses default transport if nil provided", func(t *testing.T) {
		params := map[string]string{"resource": "https://example.com/mcp"}

		wrapper := NewOAuthTransportWrapper(nil, params, nil)

		assert.NotNil(t, wrapper)
		assert.Equal(t, http.DefaultTransport, wrapper.inner)
	})

	t.Run("copies extra params to avoid external modifications", func(t *testing.T) {
		params := map[string]string{"resource": "https://example.com/mcp"}

		wrapper := NewOAuthTransportWrapper(nil, params, nil)

		// Modify original map
		params["tenant"] = "changed"

		// Wrapper should have original values only
		assert.Equal(t, map[string]string{"resource": "https://example.com/mcp"}, wrapper.extraParams)
	})

	t.Run("handles nil extra params", func(t *testing.T) {
		wrapper := NewOAuthTransportWrapper(nil, nil, nil)

		assert.NotNil(t, wrapper)
		assert.Empty(t, wrapper.extraParams)
	})
}

func TestRoundTrip_NoExtraParams(t *testing.T) {
	inner := &mockRoundTripper{}
	wrapper := NewOAuthTransportWrapper(inner, nil, zap.NewNop())

	req := httptest.NewRequest("GET", "https://provider.com/authorize?client_id=abc", nil)

	_, err := wrapper.RoundTrip(req)

	require.NoError(t, err)
	// Should delegate to inner transport without modification
	assert.Equal(t, req, inner.lastRequest)
}

func TestInjectQueryParams_Authorization(t *testing.T) {
	tests := []struct {
		name        string
		requestURL  string
		extraParams map[string]string
		wantParams  map[string]string
	}{
		{
			name:       "injects resource parameter into /authorize",
			requestURL: "https://provider.com/authorize?response_type=code&client_id=abc",
			extraParams: map[string]string{
				"resource": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
			},
			wantParams: map[string]string{
				"response_type": "code",
				"client_id":     "abc",
				"resource":      "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
			},
		},
		{
			name:       "injects multiple extra parameters",
			requestURL: "https://provider.com/oauth/authorize?client_id=123",
			extraParams: map[string]string{
				"resource": "https://example.com/mcp",
				"audience": "mcp-api",
				"tenant":   "tenant-456",
			},
			wantParams: map[string]string{
				"client_id": "123",
				"resource":  "https://example.com/mcp",
				"audience":  "mcp-api",
				"tenant":    "tenant-456",
			},
		},
		{
			name:       "overwrites existing param with extra param value",
			requestURL: "https://provider.com/authorize?resource=old-value",
			extraParams: map[string]string{
				"resource": "https://new-value.com/mcp",
			},
			wantParams: map[string]string{
				"resource": "https://new-value.com/mcp",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &mockRoundTripper{}
			wrapper := NewOAuthTransportWrapper(inner, tt.extraParams, zap.NewNop())

			req := httptest.NewRequest("GET", tt.requestURL, nil)

			_, err := wrapper.RoundTrip(req)

			require.NoError(t, err)
			assert.NotNil(t, inner.lastRequest)

			// Verify all expected params are in the modified request
			actualParams := inner.lastRequest.URL.Query()
			for key, wantValue := range tt.wantParams {
				assert.Equal(t, wantValue, actualParams.Get(key),
					"param %s should have value %s", key, wantValue)
			}
		})
	}
}

func TestInjectFormParams_TokenRequest(t *testing.T) {
	tests := []struct {
		name        string
		requestURL  string
		requestBody string
		extraParams map[string]string
		wantParams  map[string]string
	}{
		{
			name:       "injects resource into token exchange",
			requestURL: "https://provider.com/token",
			requestBody: url.Values{
				"grant_type":   {"authorization_code"},
				"code":         {"auth-code-123"},
				"redirect_uri": {"http://localhost:8080/callback"},
			}.Encode(),
			extraParams: map[string]string{
				"resource": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
			},
			wantParams: map[string]string{
				"grant_type":   "authorization_code",
				"code":         "auth-code-123",
				"redirect_uri": "http://localhost:8080/callback",
				"resource":     "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
			},
		},
		{
			name:       "injects resource into token refresh",
			requestURL: "https://provider.com/oauth/token",
			requestBody: url.Values{
				"grant_type":    {"refresh_token"},
				"refresh_token": {"refresh-token-xyz"},
			}.Encode(),
			extraParams: map[string]string{
				"resource": "https://example.com/mcp",
			},
			wantParams: map[string]string{
				"grant_type":    "refresh_token",
				"refresh_token": "refresh-token-xyz",
				"resource":      "https://example.com/mcp",
			},
		},
		{
			name:       "injects multiple extra parameters into token request",
			requestURL: "https://provider.com/token",
			requestBody: url.Values{
				"grant_type": {"authorization_code"},
				"code":       {"code-123"},
			}.Encode(),
			extraParams: map[string]string{
				"resource": "https://example.com/mcp",
				"audience": "mcp-api",
				"tenant":   "tenant-789",
			},
			wantParams: map[string]string{
				"grant_type": "authorization_code",
				"code":       "code-123",
				"resource":   "https://example.com/mcp",
				"audience":   "mcp-api",
				"tenant":     "tenant-789",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &mockRoundTripper{}
			wrapper := NewOAuthTransportWrapper(inner, tt.extraParams, zap.NewNop())

			req := httptest.NewRequest("POST", tt.requestURL, strings.NewReader(tt.requestBody))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			_, err := wrapper.RoundTrip(req)

			require.NoError(t, err)
			assert.NotNil(t, inner.lastRequest)

			// Read and parse the modified body
			bodyBytes, err := io.ReadAll(inner.lastRequest.Body)
			require.NoError(t, err)

			actualParams, err := url.ParseQuery(string(bodyBytes))
			require.NoError(t, err)

			// Verify all expected params are in the modified body
			for key, wantValue := range tt.wantParams {
				assert.Equal(t, wantValue, actualParams.Get(key),
					"param %s should have value %s", key, wantValue)
			}

			// Verify Content-Type header is preserved
			assert.Equal(t, "application/x-www-form-urlencoded", inner.lastRequest.Header.Get("Content-Type"))
		})
	}
}

func TestRoundTrip_NonOAuthRequest(t *testing.T) {
	inner := &mockRoundTripper{}
	extraParams := map[string]string{"resource": "https://example.com/mcp"}
	wrapper := NewOAuthTransportWrapper(inner, extraParams, zap.NewNop())

	// Non-OAuth request (MCP API call)
	req := httptest.NewRequest("POST", "https://provider.com/mcp", strings.NewReader(`{"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")

	originalBody := `{"method":"tools/list"}`

	_, err := wrapper.RoundTrip(req)

	require.NoError(t, err)

	// Body should NOT be modified for non-OAuth requests
	bodyBytes, err := io.ReadAll(inner.lastRequest.Body)
	require.NoError(t, err)
	assert.Equal(t, originalBody, string(bodyBytes))

	// URL should NOT be modified
	assert.Empty(t, inner.lastRequest.URL.Query().Get("resource"))
}

func TestIsAuthorizationRequest(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "/authorize endpoint", url: "https://provider.com/authorize", want: true},
		{name: "/oauth/authorize endpoint", url: "https://provider.com/oauth/authorize", want: true},
		{name: "/authorize with query params", url: "https://provider.com/authorize?client_id=abc", want: true},
		{name: "non-authorization endpoint", url: "https://provider.com/token", want: false},
		{name: "MCP endpoint", url: "https://provider.com/mcp", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			got := isAuthorizationRequest(req)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsTokenRequest(t *testing.T) {
	tests := []struct {
		name   string
		method string
		url    string
		want   bool
	}{
		{name: "POST /token", method: "POST", url: "https://provider.com/token", want: true},
		{name: "POST /oauth/token", method: "POST", url: "https://provider.com/oauth/token", want: true},
		{name: "GET /token (not POST)", method: "GET", url: "https://provider.com/token", want: false},
		{name: "POST /authorize (not token)", method: "POST", url: "https://provider.com/authorize", want: false},
		{name: "POST /mcp (not OAuth)", method: "POST", url: "https://provider.com/mcp", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.url, nil)
			got := isTokenRequest(req)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestInjectFormParams_EmptyBody(t *testing.T) {
	inner := &mockRoundTripper{}
	extraParams := map[string]string{"resource": "https://example.com/mcp"}
	wrapper := NewOAuthTransportWrapper(inner, extraParams, zap.NewNop())

	// Token request with empty body
	req := httptest.NewRequest("POST", "https://provider.com/token", bytes.NewReader([]byte{}))

	_, err := wrapper.RoundTrip(req)

	require.NoError(t, err)

	// Should create new form with extra params only
	bodyBytes, err := io.ReadAll(inner.lastRequest.Body)
	require.NoError(t, err)

	actualParams, err := url.ParseQuery(string(bodyBytes))
	require.NoError(t, err)

	assert.Equal(t, "https://example.com/mcp", actualParams.Get("resource"))
}

func TestRoundTrip_Normalizes201ToOKForTokenRequests(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		url            string
		statusCode     int
		extraParams    map[string]string
		expectedStatus int
	}{
		{
			name:           "201 token response normalized to 200 with extra params",
			method:         "POST",
			url:            "https://provider.com/token",
			statusCode:     http.StatusCreated,
			extraParams:    map[string]string{"resource": "https://example.com"},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "201 token response normalized to 200 without extra params",
			method:         "POST",
			url:            "https://provider.com/token",
			statusCode:     http.StatusCreated,
			extraParams:    nil,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "200 token response unchanged",
			method:         "POST",
			url:            "https://provider.com/token",
			statusCode:     http.StatusOK,
			extraParams:    nil,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "201 non-token response not normalized",
			method:         "GET",
			url:            "https://provider.com/authorize",
			statusCode:     http.StatusCreated,
			extraParams:    nil,
			expectedStatus: http.StatusCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &mockRoundTripper{
				response: &http.Response{
					StatusCode: tt.statusCode,
					Status:     http.StatusText(tt.statusCode),
					Body:       io.NopCloser(strings.NewReader(`{"access_token":"test"}`)),
					Header:     make(http.Header),
				},
			}
			wrapper := NewOAuthTransportWrapper(inner, tt.extraParams, zap.NewNop())
			req := httptest.NewRequest(tt.method, tt.url, strings.NewReader("grant_type=authorization_code"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp, err := wrapper.RoundTrip(req)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
		})
	}
}

func TestRoundTrip_PreservesOriginalRequest(t *testing.T) {
	inner := &mockRoundTripper{}
	extraParams := map[string]string{"resource": "https://example.com/mcp"}
	wrapper := NewOAuthTransportWrapper(inner, extraParams, zap.NewNop())

	originalURL := "https://provider.com/authorize?client_id=abc"
	req := httptest.NewRequest("GET", originalURL, nil)

	// Capture original URL
	origQuery := req.URL.Query()

	_, err := wrapper.RoundTrip(req)

	require.NoError(t, err)

	// Original request should NOT be modified (wrapper clones it)
	assert.Equal(t, origQuery, req.URL.Query(),
		"original request should not be modified")
}

// mcpGoToken mirrors mcp-go v1.0.0 transport.Token (client/transport/oauth.go).
// mcp-go json.Unmarshals the token response straight into it in both
// ProcessAuthorizationResponse (code exchange) and refreshToken. The wrapper
// does not distinguish exchange from refresh, so these tests cover both paths.
type mcpGoToken struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresIn    int64     `json:"expires_in,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
}

func TestNormalizeTokenResponseBody(t *testing.T) {
	now := time.Unix(1_794_300_000, 0)

	decode := func(t *testing.T, b []byte) map[string]json.RawMessage {
		t.Helper()
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(b, &m))
		return m
	}

	t.Run("numeric expires_at dropped, expires_in kept", func(t *testing.T) {
		in := []byte(`{"access_token":"a","token_type":"Bearer","expires_in":3600,"expires_at":1794307530}`)
		out, changed := normalizeTokenResponseBody(in, now)
		require.True(t, changed)
		m := decode(t, out)
		assert.NotContains(t, m, "expires_at")
		assert.JSONEq(t, `3600`, string(m["expires_in"]))
		assert.JSONEq(t, `"a"`, string(m["access_token"]))
		assert.JSONEq(t, `"Bearer"`, string(m["token_type"]))
	})

	t.Run("null expires_at dropped", func(t *testing.T) {
		in := []byte(`{"access_token":"a","token_type":"Bearer","expires_in":3600,"expires_at":null}`)
		out, changed := normalizeTokenResponseBody(in, now)
		require.True(t, changed)
		m := decode(t, out)
		assert.NotContains(t, m, "expires_at")
		assert.JSONEq(t, `3600`, string(m["expires_in"]))
	})

	t.Run("null expires_at does not derive expires_in", func(t *testing.T) {
		in := []byte(`{"access_token":"a","expires_at":null}`)
		out, changed := normalizeTokenResponseBody(in, now)
		require.True(t, changed)
		m := decode(t, out)
		assert.NotContains(t, m, "expires_at")
		assert.NotContains(t, m, "expires_in")
	})

	t.Run("numeric expires_at without expires_in derives it", func(t *testing.T) {
		in := []byte(`{"access_token":"a","expires_at":1794303600}`)
		out, changed := normalizeTokenResponseBody(in, now)
		require.True(t, changed)
		m := decode(t, out)
		assert.NotContains(t, m, "expires_at")
		assert.JSONEq(t, `3600`, string(m["expires_in"]))
	})

	t.Run("numeric expires_at with zero expires_in derives it", func(t *testing.T) {
		in := []byte(`{"access_token":"a","expires_in":0,"expires_at":1794303600}`)
		out, changed := normalizeTokenResponseBody(in, now)
		require.True(t, changed)
		assert.JSONEq(t, `3600`, string(decode(t, out)["expires_in"]))
	})

	t.Run("numeric expires_at in the past derives expires_in 0", func(t *testing.T) {
		in := []byte(`{"access_token":"a","expires_at":1000}`)
		out, changed := normalizeTokenResponseBody(in, now)
		require.True(t, changed)
		assert.JSONEq(t, `0`, string(decode(t, out)["expires_in"]))
	})

	t.Run("non-RFC3339 string expires_at dropped and decodes", func(t *testing.T) {
		for _, in := range []string{
			`{"access_token":"a","expires_at":""}`,
			`{"access_token":"a","expires_at":"2026-11-11 10:00:00"}`,
			`{"access_token":"a","expires_at":"soon"}`,
			`{"access_token":"a","expires_in":3600,"expires_at":"1794307530"}`,
		} {
			var raw mcpGoToken
			require.Error(t, json.Unmarshal([]byte(in), &raw), in)

			out, changed := normalizeTokenResponseBody([]byte(in), now)
			require.True(t, changed, in)
			assert.NotContains(t, decode(t, out), "expires_at", in)
			var tok mcpGoToken
			require.NoError(t, json.Unmarshal(out, &tok), in)
			assert.Equal(t, "a", tok.AccessToken)
		}
	})

	t.Run("quoted epoch string derives expires_in", func(t *testing.T) {
		in := []byte(`{"access_token":"a","expires_at":"1794303600"}`)
		out, changed := normalizeTokenResponseBody(in, now)
		require.True(t, changed)
		assert.JSONEq(t, `3600`, string(decode(t, out)["expires_in"]))
	})

	t.Run("non-RFC3339 string does not derive expires_in", func(t *testing.T) {
		in := []byte(`{"access_token":"a","expires_at":"2026-11-11 10:00:00"}`)
		out, _ := normalizeTokenResponseBody(in, now)
		assert.NotContains(t, decode(t, out), "expires_in")
	})

	t.Run("millisecond epoch is read as milliseconds", func(t *testing.T) {
		in := []byte(`{"access_token":"a","expires_at":1794303600000}`)
		out, changed := normalizeTokenResponseBody(in, now)
		require.True(t, changed)
		assert.JSONEq(t, `3600`, string(decode(t, out)["expires_in"]))
	})

	t.Run("absurd epochs never produce an overflowing expires_in", func(t *testing.T) {
		for _, in := range []string{
			`{"access_token":"a","expires_at":1e300}`,
			`{"access_token":"a","expires_at":-1e300}`,
			`{"access_token":"a","expires_at":9223372036854775807}`,
			`{"access_token":"a","expires_at":1794303600000000}`,
			`{"access_token":"a","expires_at":4102444800}`, // year 2100 seconds
		} {
			out, changed := normalizeTokenResponseBody([]byte(in), now)
			require.True(t, changed, in)
			var tok mcpGoToken
			require.NoError(t, json.Unmarshal(out, &tok), in)
			d := time.Duration(tok.ExpiresIn) * time.Second
			assert.GreaterOrEqual(t, d, time.Duration(0), in)
			assert.LessOrEqual(t, d, 10*366*24*time.Hour, in)
		}
	})

	t.Run("RFC3339 string expires_at left byte-identical", func(t *testing.T) {
		in := []byte(`{"access_token":"a",  "expires_in":3600,"expires_at":"2026-11-11T10:00:00Z"}`)
		out, changed := normalizeTokenResponseBody(in, now)
		assert.False(t, changed)
		assert.Equal(t, in, out)
	})

	t.Run("no expires_at left byte-identical", func(t *testing.T) {
		in := []byte(`{"access_token":"a","token_type":"Bearer"}`)
		out, changed := normalizeTokenResponseBody(in, now)
		assert.False(t, changed)
		assert.Equal(t, in, out)
	})

	t.Run("non-object bodies untouched", func(t *testing.T) {
		for _, in := range []string{`[1,2]`, `null`, `"x"`, `not json`, ``, `{"expires_at":`} {
			out, changed := normalizeTokenResponseBody([]byte(in), now)
			assert.False(t, changed, in)
			assert.Equal(t, []byte(in), out, in)
		}
	})

	t.Run("other fields keep their values", func(t *testing.T) {
		in := []byte(`{"access_token":"a","big":12345678901234567890,"nested":{"expires_at":5},"expires_in":10,"expires_at":7}`)
		out, changed := normalizeTokenResponseBody(in, now)
		require.True(t, changed)
		m := decode(t, out)
		assert.Equal(t, `12345678901234567890`, string(m["big"]))
		assert.JSONEq(t, `{"expires_at":5}`, string(m["nested"]))
	})

	t.Run("normalized body decodes into mcp-go Token", func(t *testing.T) {
		for _, in := range []string{
			`{"access_token":"a","token_type":"Bearer","expires_in":3600,"expires_at":1794307530}`,
			`{"access_token":"a","token_type":"Bearer","expires_at":1794307530}`,
		} {
			// Reproduces the exact issue #1862 failure on the raw body (a null
			// expires_at is tolerated by encoding/json but is dropped too, see above).
			var raw mcpGoToken
			err := json.Unmarshal([]byte(in), &raw)
			require.Error(t, err, in)
			assert.Contains(t, err.Error(), "Time.UnmarshalJSON: input is not a JSON string")

			out, changed := normalizeTokenResponseBody([]byte(in), now)
			require.True(t, changed)
			var tok mcpGoToken
			require.NoError(t, json.Unmarshal(out, &tok), in)
			assert.Equal(t, "a", tok.AccessToken)
			assert.Equal(t, "Bearer", tok.TokenType)
		}
	})
}

func TestRoundTrip_NormalizesTokenResponseBody(t *testing.T) {
	const numericBody = `{"access_token":"a","token_type":"Bearer","expires_in":3600,"expires_at":1794307530}`

	newResp := func(status int, body string) *http.Response {
		return &http.Response{
			StatusCode:    status,
			Status:        http.StatusText(status),
			Body:          io.NopCloser(strings.NewReader(body)),
			Header:        http.Header{"Content-Length": []string{"999"}},
			ContentLength: 999,
		}
	}
	doReq := func(t *testing.T, extra map[string]string, method, target string, resp *http.Response) *http.Response {
		t.Helper()
		wrapper := NewOAuthTransportWrapper(&mockRoundTripper{response: resp}, extra, zap.NewNop())
		req := httptest.NewRequest(method, target, strings.NewReader("grant_type=refresh_token"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		out, err := wrapper.RoundTrip(req)
		require.NoError(t, err)
		return out
	}
	readBody := func(t *testing.T, r *http.Response) string {
		t.Helper()
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		return string(b)
	}

	for name, extra := range map[string]map[string]string{
		"with extra params":    {"resource": "https://example.com"},
		"without extra params": nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Run("numeric expires_at rewritten and Content-Length updated", func(t *testing.T) {
				resp := doReq(t, extra, "POST", "https://provider.com/token", newResp(http.StatusOK, numericBody))
				body := readBody(t, resp)
				var tok mcpGoToken
				require.NoError(t, json.Unmarshal([]byte(body), &tok))
				assert.Equal(t, int64(3600), tok.ExpiresIn)
				assert.NotContains(t, body, "expires_at")
				assert.Equal(t, int64(len(body)), resp.ContentLength)
				assert.Equal(t, strconv.Itoa(len(body)), resp.Header.Get("Content-Length"))
			})

			t.Run("201 token response is both status- and body-normalized", func(t *testing.T) {
				resp := doReq(t, extra, "POST", "https://provider.com/token", newResp(http.StatusCreated, numericBody))
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				var tok mcpGoToken
				require.NoError(t, json.Unmarshal([]byte(readBody(t, resp)), &tok))
			})

			t.Run("non-token request untouched", func(t *testing.T) {
				resp := doReq(t, extra, "GET", "https://provider.com/authorize", newResp(http.StatusOK, numericBody))
				assert.Equal(t, numericBody, readBody(t, resp))
				assert.Equal(t, int64(999), resp.ContentLength)
			})

			t.Run("non-2xx token response untouched", func(t *testing.T) {
				errBody := `{"error":"invalid_grant","expires_at":1}`
				resp := doReq(t, extra, "POST", "https://provider.com/token", newResp(http.StatusBadRequest, errBody))
				assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
				assert.Equal(t, errBody, readBody(t, resp))
				assert.Equal(t, int64(999), resp.ContentLength)
			})

			t.Run("non-JSON body untouched", func(t *testing.T) {
				resp := doReq(t, extra, "POST", "https://provider.com/token", newResp(http.StatusOK, "access_token=a&expires_at=1"))
				assert.Equal(t, "access_token=a&expires_at=1", readBody(t, resp))
				assert.Equal(t, int64(999), resp.ContentLength)
			})

			t.Run("string expires_at untouched", func(t *testing.T) {
				body := `{"access_token":"a","expires_at":"2026-11-11T10:00:00Z"}`
				resp := doReq(t, extra, "POST", "https://provider.com/token", newResp(http.StatusOK, body))
				assert.Equal(t, body, readBody(t, resp))
			})

			t.Run("body over the cap passed through unchanged", func(t *testing.T) {
				big := `{"access_token":"a","expires_at":1,"pad":"` + strings.Repeat("x", maxTokenResponseBodyBytes) + `"}`
				resp := doReq(t, extra, "POST", "https://provider.com/token", newResp(http.StatusOK, big))
				assert.Equal(t, big, readBody(t, resp))
				assert.Equal(t, int64(999), resp.ContentLength)
			})
		})
	}
}
