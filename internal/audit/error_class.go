package audit

// error_class.go: the one helper that maps a dispatch error to the closed
// `error_class` vocabulary of contracts/audit-line-events.md (tool_call,
// outcome:error). A class, never message text: the line carries the class
// and the activity record keeps the prose (FR-015).

import (
	"context"
	"errors"
	"net/http"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// ErrorClass is one member of the schema's `error_class` enum.
type ErrorClass string

const (
	ErrorClassUpstreamError       ErrorClass = "upstream_error"
	ErrorClassUpstreamTimeout     ErrorClass = "upstream_timeout"
	ErrorClassUpstreamUnavailable ErrorClass = "upstream_unavailable"
	ErrorClassValidation          ErrorClass = "validation"
	ErrorClassSanitisation        ErrorClass = "sanitisation"
	ErrorClassInternal            ErrorClass = "internal"
	ErrorClassCancelled           ErrorClass = "cancelled"
)

// ErrSanitisationFailed is the sentinel a dispatch path wraps when the
// sanitisation step itself fails (as opposed to a post-dispatch output
// sanitisation BLOCK, which is a tool_call outcome:blocked and never reaches
// ErrorClassOf).
var ErrSanitisationFailed = errors.New("audit: sanitisation failed")

// ErrorClassOf classifies err. Wrapped chains are unwrapped with errors.Is /
// errors.As; anything unrecognised (including nil) is `internal`.
func ErrorClassOf(err error) ErrorClass {
	if err == nil {
		return ErrorClassInternal
	}
	if errors.Is(err, context.Canceled) {
		return ErrorClassCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorClassUpstreamTimeout
	}
	if errors.Is(err, ErrSanitisationFailed) {
		return ErrorClassSanitisation
	}
	var limitErr *limiter.LimitError
	if errors.As(err, &limitErr) {
		return ErrorClassUpstreamUnavailable
	}
	var verr *jsonschema.ValidationError
	if errors.As(err, &verr) {
		return ErrorClassValidation
	}
	var rpcErr *transport.JSONRPCError
	if errors.As(err, &rpcErr) {
		return ErrorClassUpstreamError
	}
	var httpErr *transport.HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return ErrorClassUpstreamUnavailable
		default:
			return ErrorClassUpstreamError
		}
	}
	return ErrorClassInternal
}
