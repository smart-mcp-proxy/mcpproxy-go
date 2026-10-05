package callerr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	proxytransport "github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timed out" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return false }

var _ net.Error = timeoutErr{}

func TestClassify(t *testing.T) {
	dispatched := Facts{Dispatched: true, HTTPRequests: 1}
	http2xx := Facts{Dispatched: true, HTTPRequests: 1, ResponseReceived: true, HTTPStatus: 200}
	httpStatus := func(code int) Facts {
		return Facts{Dispatched: true, HTTPRequests: 1, ResponseReceived: true, HTTPStatus: code}
	}
	stdio := Facts{Dispatched: true} // dispatched, no HTTP request observed

	tests := []struct {
		name       string
		result     *mcp.CallToolResult
		err        error
		facts      Facts
		wantOK     bool
		wantClass  Class
		wantDomain Domain
		wantStatus int
		wantAudit  audit.ErrorClass
	}{
		{name: "success", result: &mcp.CallToolResult{}, facts: http2xx, wantOK: false},
		{name: "nil result nil err", facts: http2xx, wantOK: false},
		{name: "isError result", result: &mcp.CallToolResult{IsError: true}, facts: http2xx,
			wantOK: true, wantClass: ClassToolError, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamError},
		{name: "session terminated", err: fmt.Errorf("call: %w", transport.ErrSessionTerminated), facts: httpStatus(404),
			wantOK: true, wantClass: ClassSessionTerminated, wantDomain: DomainUpstream, wantStatus: 404, wantAudit: audit.ErrorClassUpstreamError},
		{name: "oauth authorization required", err: &transport.OAuthAuthorizationRequiredError{}, facts: dispatched,
			wantOK: true, wantClass: ClassAuth, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamError},
		{name: "authorization required", err: &transport.AuthorizationRequiredError{}, facts: dispatched,
			wantOK: true, wantClass: ClassAuth, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamError},
		{name: "ErrUnauthorized", err: fmt.Errorf("x: %w", transport.ErrUnauthorized), facts: dispatched,
			wantOK: true, wantClass: ClassAuth, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamError},
		{name: "recorded 401 untyped", err: errors.New("request failed with status 401: nope"), facts: httpStatus(401),
			wantOK: true, wantClass: ClassAuth, wantDomain: DomainUpstream, wantStatus: 401, wantAudit: audit.ErrorClassUpstreamError},
		{name: "recorded 403 untyped", err: errors.New("request failed with status 403: nope"), facts: httpStatus(403),
			wantOK: true, wantClass: ClassAuth, wantDomain: DomainUpstream, wantStatus: 403, wantAudit: audit.ErrorClassUpstreamError},
		{name: "limiter shed", err: fmt.Errorf("w: %w", &limiter.LimitError{Reason: limiter.ReasonQueueFull}),
			wantOK: true, wantClass: ClassProxyPolicy, wantDomain: DomainProxy, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "generation changed", err: fmt.Errorf("srv: %w", managed.ErrConnectionGenerationChanged),
			wantOK: true, wantClass: ClassProxyPolicy, wantDomain: DomainProxy, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "profile refusal", err: &profile.ToolBlockedError{Message: "blocked"},
			wantOK: true, wantClass: ClassProxyPolicy, wantDomain: DomainProxy, wantAudit: audit.ErrorClassInternal},
		{name: "outside profile", err: profile.ErrToolOutsideProfile,
			wantOK: true, wantClass: ClassProxyPolicy, wantDomain: DomainProxy, wantAudit: audit.ErrorClassInternal},
		{name: "schema validation", err: &jsonschema.ValidationError{},
			wantOK: true, wantClass: ClassProxyPolicy, wantDomain: DomainClient, wantAudit: audit.ErrorClassValidation},
		{name: "sanitisation failure", err: fmt.Errorf("s: %w", audit.ErrSanitisationFailed),
			wantOK: true, wantClass: ClassProxyInternal, wantDomain: DomainProxy, wantAudit: audit.ErrorClassSanitisation},
		{name: "caller cancelled", err: context.Canceled, facts: dispatched,
			wantOK: true, wantClass: ClassCancelled, wantDomain: DomainClient, wantAudit: audit.ErrorClassCancelled},
		{name: "caller cancelled wrapped by net", err: &net.OpError{Op: "read", Err: context.Canceled}, facts: dispatched,
			wantOK: true, wantClass: ClassCancelled, wantDomain: DomainClient, wantAudit: audit.ErrorClassCancelled},
		{name: "deadline dispatched", err: context.DeadlineExceeded, facts: dispatched,
			wantOK: true, wantClass: ClassTimeout, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamTimeout},
		{name: "deadline not dispatched", err: context.DeadlineExceeded, facts: Facts{},
			wantOK: true, wantClass: ClassTimeout, wantDomain: DomainProxy, wantAudit: audit.ErrorClassUpstreamTimeout},
		{name: "net timeout", err: timeoutErr{}, facts: dispatched,
			wantOK: true, wantClass: ClassTimeout, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamTimeout},
		{name: "net.OpError dial", err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, facts: dispatched,
			wantOK: true, wantClass: ClassNetwork, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "connection reset syscall", err: fmt.Errorf("x: %w", syscall.ECONNRESET), facts: dispatched,
			wantOK: true, wantClass: ClassNetwork, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "EOF", err: io.EOF, facts: dispatched,
			wantOK: true, wantClass: ClassNetwork, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "unexpected EOF", err: io.ErrUnexpectedEOF, facts: stdio,
			wantOK: true, wantClass: ClassNetwork, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "dns", err: &net.DNSError{Err: "no such host", Name: "x"}, facts: dispatched,
			wantOK: true, wantClass: ClassNetwork, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "mcp-go transport.Error", err: transport.NewError(errors.New("boom")), facts: dispatched,
			wantOK: true, wantClass: ClassNetwork, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "transport closed", err: transport.ErrTransportClosed, facts: stdio,
			wantOK: true, wantClass: ClassNetwork, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "recorded 502 untyped", err: errors.New("request failed with status 502: <html>"), facts: httpStatus(502),
			wantOK: true, wantClass: ClassHTTP, wantDomain: DomainUpstream, wantStatus: 502, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "mcp-go transport.Error wrapping a recorded 502", err: transport.NewError(errors.New("request failed with status 502: <html>")), facts: httpStatus(502),
			wantOK: true, wantClass: ClassHTTP, wantDomain: DomainUpstream, wantStatus: 502, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "recorded 500 untyped", err: errors.New("request failed with status 500: x"), facts: httpStatus(500),
			wantOK: true, wantClass: ClassHTTP, wantDomain: DomainUpstream, wantStatus: 500, wantAudit: audit.ErrorClassUpstreamError},
		{name: "recorded 504", err: errors.New("x"), facts: httpStatus(504),
			wantOK: true, wantClass: ClassHTTP, wantDomain: DomainUpstream, wantStatus: 504, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "recorded 429", err: errors.New("x"), facts: httpStatus(429),
			wantOK: true, wantClass: ClassHTTP, wantDomain: DomainUpstream, wantStatus: 429, wantAudit: audit.ErrorClassUpstreamError},
		{name: "mcp-go ErrInvalidParams", err: fmt.Errorf("w: %w", mcp.ErrInvalidParams), facts: http2xx,
			wantOK: true, wantClass: ClassJSONRPC, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamError},
		{name: "mcp-go ErrInternalError", err: mcp.ErrInternalError, facts: http2xx,
			wantOK: true, wantClass: ClassJSONRPC, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamError},
		{name: "custom -32001 after recorded 200", err: errors.New("custom -32001 message"), facts: http2xx,
			wantOK: true, wantClass: ClassJSONRPC, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamError},
		{name: "stdio untyped error is jsonrpc", err: errors.New("tool exploded"), facts: stdio,
			wantOK: true, wantClass: ClassJSONRPC, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamError},
		{name: "proxy JSONRPCError type", err: &proxytransport.JSONRPCError{Code: -32001, Message: "m"}, facts: dispatched,
			wantOK: true, wantClass: ClassJSONRPC, wantDomain: DomainUpstream, wantAudit: audit.ErrorClassUpstreamError},
		{name: "proxy HTTPError type", err: &proxytransport.HTTPError{StatusCode: 503}, facts: dispatched,
			wantOK: true, wantClass: ClassHTTP, wantDomain: DomainUpstream, wantStatus: 503, wantAudit: audit.ErrorClassUpstreamUnavailable},
		{name: "unknown not dispatched", err: errors.New("client not connected"), facts: Facts{},
			wantOK: true, wantClass: ClassProxyInternal, wantDomain: DomainProxy, wantAudit: audit.ErrorClassInternal},
		{name: "unknown dispatched http no response", err: errors.New("weird"), facts: dispatched,
			wantOK: true, wantClass: ClassProxyInternal, wantDomain: DomainProxy, wantAudit: audit.ErrorClassInternal},
		{name: "caller cancelled fact", err: errors.New("aborted"), facts: Facts{Dispatched: true, CallerCancelled: true},
			wantOK: true, wantClass: ClassCancelled, wantDomain: DomainClient, wantAudit: audit.ErrorClassCancelled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Classify(tc.result, tc.err, tc.facts)
			assert.Equal(t, tc.wantOK, ok)
			if !tc.wantOK {
				return
			}
			assert.Equal(t, tc.wantClass, got.Class)
			assert.Equal(t, tc.wantDomain, got.Domain)
			assert.Equal(t, tc.wantStatus, got.HTTPStatus)
			assert.Equal(t, tc.wantAudit, got.AuditClass(), "audit class must agree with the funnel")
		})
	}
}

// FR-049: message text is read only by a documented last-resort list, and only
// when the call is known to have produced no HTTP response (the typed chain
// was lost) or never left the proxy. A tool/JSON-RPC message that merely
// mentions a network word must NOT be reclassified.
func TestClassifyFallbackListIsNarrow(t *testing.T) {
	noResp := Facts{Dispatched: true, HTTPRequests: 1}
	tests := []struct {
		name  string
		err   string
		facts Facts
		want  Class
	}{
		{"refused no response", "Post \"http://x\": dial tcp 1.2.3.4:80: connect: connection refused", noResp, ClassNetwork},
		{"reset no response", "read: connection reset by peer", noResp, ClassNetwork},
		{"no such host", "lookup x: no such host", noResp, ClassNetwork},
		{"io timeout", "net/http: request canceled (Client.Timeout exceeded) i/o timeout", noResp, ClassTimeout},
		{"stdio message mentioning refused stays jsonrpc", "upstream said: connection refused", Facts{Dispatched: true}, ClassJSONRPC},
		{"response received mentioning refused stays jsonrpc", "connection refused by db", Facts{Dispatched: true, HTTPRequests: 1, ResponseReceived: true, HTTPStatus: 200}, ClassJSONRPC},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Classify(nil, errors.New(tc.err), tc.facts)
			assert.True(t, ok)
			assert.Equal(t, tc.want, got.Class)
		})
	}
}

func TestClassifyIsErrorWinsOverFacts(t *testing.T) {
	// An isError result with a stale non-2xx recorded status is still a tool error.
	got, ok := Classify(&mcp.CallToolResult{IsError: true}, nil, Facts{Dispatched: true, HTTPRequests: 1, ResponseReceived: true, HTTPStatus: 200})
	assert.True(t, ok)
	assert.Equal(t, ClassToolError, got.Class)
	assert.Zero(t, got.HTTPStatus)
}
