package callstats

import (
	"context"
	"errors"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Classify decides how one dispatched tool call affects the failure-rate
// window. It is a denylist over typed proxy-side outcomes (Spec 113 FR-061):
//
//   - isError:true results are tool-level answers, not transport failures:
//     uncounted.
//   - limiter rejections, connection-generation refusals, caller cancellation
//     and auth-required errors are not the upstream's fault: uncounted.
//   - every other error from the dispatch is a counted failure.
//   - a nil error with a normal result is a counted success.
//
// Profile/quarantine refusals and argument validation happen before dispatch
// and never reach this predicate.
func Classify(result *mcp.CallToolResult, err error) (counted, failed bool, kind Kind) {
	if err == nil {
		if result != nil && result.IsError {
			return false, false, KindNone
		}
		return true, false, KindNone
	}
	var limitErr *limiter.LimitError
	if errors.As(err, &limitErr) ||
		errors.Is(err, managed.ErrConnectionGenerationChanged) ||
		errors.Is(err, context.Canceled) {
		return false, false, KindNone
	}
	msg := strings.ToLower(err.Error())
	// Some transports (mcp-go SSE) surface a caller cancellation as plain text
	// with the sentinel stripped; it is still the caller going away.
	if containsAny(msg, "context canceled", "context cancelled") {
		return false, false, KindNone
	}
	if isAuthRequired(msg) {
		return false, false, KindNone
	}
	return true, true, failureKind(err, msg)
}

func isAuthRequired(msg string) bool {
	for _, s := range []string{
		"authorization required",
		"unauthorized",
		"invalid_token",
		"oauth authentication failed",
		"insufficient_scope",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func failureKind(err error, msg string) Kind {
	if errors.Is(err, context.DeadlineExceeded) || containsAny(msg, "timeout", "timed out", "deadline exceeded") {
		return KindTimeout
	}
	if containsAny(msg, "session terminated") {
		return KindSession
	}
	if containsAny(msg, "connection refused", "connection reset", "no such host", "broken pipe",
		"transport closed", "eof", "dial tcp", "i/o timeout", "network is unreachable", "not connected") {
		return KindNetwork
	}
	if containsAny(msg, "status 4", "status 5", "status code", "http 4", "http 5") {
		return KindHTTP
	}
	if containsAny(msg, "jsonrpc", "json-rpc", "json rpc") {
		return KindJSONRPC
	}
	return KindOther
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
