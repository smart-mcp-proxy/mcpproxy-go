package oauth

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
