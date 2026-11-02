package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

func newArgumentPrivacyUnitProxy(t *testing.T) *MCPProxyServer {
	t.Helper()
	p := createTestMCPProxyServer(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, p.upstreamManager.ShutdownAll(ctx))
		require.NoError(t, p.Close())
	})
	return p
}

// ARG-PXY-02's typed boundary is owned by MCPProxy. The installed upstream
// SDK does not preserve this type, code or data over Streamable HTTP.
func TestArgumentPrivacyTypedErrors(t *testing.T) {
	p := newArgumentPrivacyUnitProxy(t)
	marker := "typedM_雪/~"
	for _, indexed := range []bool{false, true} {
		if indexed {
			require.NoError(t, p.index.IndexTool(&config.ToolMetadata{
				Name: "private:check", ServerName: "private", ParamsJSON: argumentPrivacySchema,
			}))
		}
		for _, nestedHTTP := range []bool{false, true} {
			t.Run(fmt.Sprintf("indexed=%t/http=%t", indexed, nestedHTTP), func(t *testing.T) {
				original := &transport.JSONRPCError{
					Code: -32602, Message: "Invalid params: " + marker,
					Data: map[string]interface{}{marker: []interface{}{map[string]string{"path": "/" + marker, "value": marker}}},
				}
				if nestedHTTP {
					original.HTTPError = &transport.HTTPError{
						StatusCode: 400, Body: `{"rejected":"` + marker + `"}`,
						URL: "https://fixture.invalid/mcp", Method: "POST",
						Err: errors.New(marker),
					}
				}
				before, err := json.Marshal(original)
				require.NoError(t, err)
				wrapped := fmt.Errorf("outer request detail %s: %w", marker, original)
				result := p.createToolErrorResponse(wrapped, "private", "check", config.ToolArgumentErrorModeRedacted)
				body := decodeErrorBody(t, result)
				require.Equal(t, "Invalid tool arguments.", body["error"])
				privacyAssertNoMarkers(t, result, marker)
				if indexed {
					privacyAssertIndexedError(t, result, "private:check", true)
				} else {
					require.NotContains(t, body, "input_schema")
					require.NotContains(t, body, "hint")
					require.Equal(t, float64(-32602), body["error_code"])
					require.Contains(t, body, "error_data")
					require.Nil(t, body["error_data"])
					if nestedHTTP {
						require.Len(t, body, 5)
						httpDetails := body["http_details"].(map[string]interface{})
						require.Equal(t, float64(400), httpDetails["status_code"])
						require.Equal(t, "", httpDetails["response_body"])
						require.Equal(t, "https://fixture.invalid/mcp", httpDetails["server_url"])
					} else {
						require.Len(t, body, 3)
					}
				}
				for _, mode := range []string{"", config.ToolArgumentErrorModeDetailed} {
					detailed := p.createToolErrorResponse(wrapped, "private", "check", mode)
					require.Contains(t, fwdText(detailed), marker, "default/detailed retain original diagnosis")
					require.Equal(t, p.createDetailedErrorResponse(wrapped, "private", "check"), detailed)
				}
				after, err := json.Marshal(original)
				require.NoError(t, err)
				require.Equal(t, before, after, "redaction must not mutate shared message/data/nested HTTP errors")
				if nestedHTTP {
					require.Equal(t, marker, original.HTTPError.Err.Error())
				}
			})
		}
	}
}

func TestArgumentPrivacyClassificationControls(t *testing.T) {
	p := newArgumentPrivacyUnitProxy(t)
	marker := "controlM_雪/~"
	positive := errors.New("validation failed for argument: " + marker)
	for _, indexed := range []bool{false, true} {
		if indexed {
			require.NoError(t, p.index.IndexTool(&config.ToolMetadata{Name: "private:check", ServerName: "private", ParamsJSON: argumentPrivacySchema}))
		}
		t.Run(fmt.Sprintf("narrow_untyped/indexed=%t", indexed), func(t *testing.T) {
			result := p.createToolErrorResponse(positive, "private", "check", config.ToolArgumentErrorModeRedacted)
			body := decodeErrorBody(t, result)
			require.Equal(t, "Invalid tool arguments.", body["error"])
			privacyAssertNoMarkers(t, result, marker)
			if indexed {
				privacyAssertIndexedError(t, result, "private:check", true)
			} else {
				require.Len(t, body, 4)
				require.NotContains(t, body, "input_schema")
				require.NotContains(t, body, "hint")
				require.NotContains(t, body, "error_code", "an untyped error has no numeric code to invent")
			}
		})
	}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"http_auth", &transport.HTTPError{StatusCode: 401, Body: "validation failed for argument: " + marker}},
		{"http_bad_request", &transport.HTTPError{StatusCode: 400, Body: "missing required property " + marker}},
		{"rpc_internal", &transport.JSONRPCError{Code: -32603, Message: "missing required field " + marker, Data: marker}},
		{"auth", errors.New("authentication failed; missing required field " + marker)},
		{"network", errors.New("connection refused; validation failed for argument " + marker)},
		{"timeout", errors.New("request timed out; validation failed for argument " + marker)},
		{"deadline", fmt.Errorf("%s: %w", marker, context.DeadlineExceeded)},
		{"internal", errors.New("internal failure: " + marker)},
		{"generic_invalid", errors.New("Invalid params: " + marker)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, eligible := classifyUpstreamInvalidParams(tc.err)
			require.False(t, eligible)
			detailed := p.createDetailedErrorResponse(tc.err, "private", "check")
			redacted := p.createToolErrorResponse(tc.err, "private", "check", config.ToolArgumentErrorModeRedacted)
			require.Equal(t, detailed, redacted, "noneligible errors retain their complete established response")
			require.NotContains(t, decodeErrorBody(t, redacted), "input_schema")
		})
	}
}

// CI-PLAN-02: independently check the existing credential rules at each typed
// legacy publishing sink. Nonargument diagnostics survive every policy.
func TestArgumentPrivacyLegacyCredentialRedaction(t *testing.T) {
	p := newArgumentPrivacyUnitProxy(t)
	const urlSecret = "fixture-url-credential-123456"
	const textSecret = "fixture-text-credential-123456"
	const authSecret = "fixture-authorization-123456"
	httpError := &transport.HTTPError{
		StatusCode: 401, URL: "https://fixture.invalid/mcp?token=" + urlSecret + "&region=west",
		Method: "POST", Body: "safe body diagnostic token=" + textSecret,
	}
	data := map[string]interface{}{
		"headers":    map[string]interface{}{"Authorization": "Bearer " + authSecret},
		"diagnostic": "safe structured diagnostic",
	}
	for _, tc := range []struct {
		name    string
		err     error
		rpc     bool
		hasHTTP bool
	}{
		{"http401", httpError, false, true},
		{"rpc_internal", &transport.JSONRPCError{Code: -32603, Message: "safe message diagnostic token=" + textSecret, Data: data}, true, false},
		{"rpc_internal_http401", &transport.JSONRPCError{Code: -32603, Message: "safe message diagnostic token=" + textSecret, Data: data, HTTPError: httpError}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, eligible := classifyUpstreamInvalidParams(tc.err)
			require.False(t, eligible)
			for _, mode := range []string{"legacy", "", "detailed", "redacted"} {
				t.Run("mode="+mode, func(t *testing.T) {
					var result *mcp.CallToolResult
					if mode == "legacy" {
						result = p.createDetailedErrorResponse(tc.err, "renderer", "probe")
					} else {
						result = p.createToolErrorResponse(tc.err, "renderer", "probe", mode)
					}
					require.True(t, result.IsError)
					serialized, err := json.Marshal(result)
					require.NoError(t, err)
					for _, secret := range []string{urlSecret, textSecret, authSecret} {
						require.NotContains(t, string(serialized), secret)
					}
					body := decodeErrorBody(t, result)
					require.NotContains(t, body, "input_schema")
					require.NotContains(t, body, "hint")
					message, ok := body["error"].(string)
					require.True(t, ok)
					require.NotContains(t, message, textSecret)
					if tc.rpc {
						require.Contains(t, message, "safe message diagnostic")
						require.Equal(t, float64(-32603), body["error_code"])
						details, ok := body["error_data"].(map[string]interface{})
						require.True(t, ok)
						require.Len(t, details, 2)
						require.Equal(t, "safe structured diagnostic", details["diagnostic"])
						headers, ok := details["headers"].(map[string]interface{})
						require.True(t, ok)
						require.Len(t, headers, 1)
						require.NotEmpty(t, headers["Authorization"])
						require.NotContains(t, headers["Authorization"], authSecret)
					} else {
						require.Contains(t, message, "HTTP 401")
						require.Contains(t, message, "safe body diagnostic")
						require.NotContains(t, body, "error_code")
						require.NotContains(t, body, "error_data")
					}
					if tc.hasHTTP {
						details, ok := body["http_details"].(map[string]interface{})
						require.True(t, ok)
						require.Equal(t, float64(401), details["status_code"])
						require.Contains(t, details["response_body"], "safe body diagnostic")
						require.NotContains(t, details["response_body"], textSecret)
						redactedURL, ok := details["server_url"].(string)
						require.True(t, ok)
						u, err := url.Parse(redactedURL)
						require.NoError(t, err)
						require.Equal(t, "https", u.Scheme)
						require.Equal(t, "fixture.invalid", u.Host)
						require.Equal(t, "/mcp", u.Path)
						require.Equal(t, "west", u.Query().Get("region"))
						require.NotEmpty(t, u.Query().Get("token"))
						require.NotEqual(t, urlSecret, u.Query().Get("token"))
						require.Contains(t, body["troubleshooting"], "Authentication required")
						if !tc.rpc {
							require.Equal(t, "POST", details["method"])
						}
					} else {
						require.NotContains(t, body, "http_details")
						require.NotContains(t, body, "troubleshooting")
					}
				})
			}
		})
	}
}
