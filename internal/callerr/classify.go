// Package callerr classifies the outcome of one upstream tool call into the
// call-error taxonomy of Spec 113-c: an error_class (what failed), a
// fault_domain (whose fault it is) and, when known, the upstream HTTP status.
//
// The classifier is pure: it reads typed errors and a small set of Facts the
// dispatch site collected (a per-call transport recorder, the cancellation
// state of the caller's context). It never inspects error message text,
// except for a short, documented last-resort list (see fallbackNetwork /
// fallbackTimeout) that applies only when the typed chain is known to have
// been lost.
package callerr

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	proxytransport "github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Class is the error_class vocabulary (FR-040).
type Class string

const (
	ClassNetwork           Class = "network"
	ClassTimeout           Class = "timeout"
	ClassHTTP              Class = "http"
	ClassJSONRPC           Class = "jsonrpc"
	ClassToolError         Class = "tool_error"
	ClassSessionTerminated Class = "session_terminated"
	ClassAuth              Class = "auth"
	ClassProxyPolicy       Class = "proxy_policy"
	ClassProxyInternal     Class = "proxy_internal"
	ClassCancelled         Class = "cancelled"
)

// Domain is the fault_domain vocabulary.
type Domain string

const (
	DomainUpstream Domain = "upstream"
	DomainProxy    Domain = "proxy"
	DomainClient   Domain = "client"
)

// Facts are the observations a dispatch site makes about one call.
type Facts struct {
	// Dispatched is true once the request was handed to the mcp-go client
	// (set by core.Client for every transport, not inferred from HTTP).
	Dispatched bool
	// HTTPRequests is how many HTTP requests the call produced (0 for stdio).
	HTTPRequests int
	// ResponseReceived is true when any HTTP response was observed.
	ResponseReceived bool
	// HTTPStatus is the status of the last HTTP response, 0 if none.
	HTTPStatus int
	// CallerCancelled is true when the caller's own context was cancelled.
	CallerCancelled bool
}

// FactsFromRecorder builds Facts from a transport recorder snapshot.
func FactsFromRecorder(s proxytransport.CallRecorderSnapshot, callerCancelled bool) Facts {
	return Facts{
		Dispatched:       s.Dispatched,
		HTTPRequests:     s.Requests,
		ResponseReceived: s.Responses > 0,
		HTTPStatus:       s.LastStatus,
		CallerCancelled:  callerCancelled,
	}
}

// Outcome is the classification of one failed call.
type Outcome struct {
	Class      Class
	Domain     Domain
	HTTPStatus int // upstream HTTP status when known, else 0

	// audit pins the Spec 107 audit class where the frozen vocabulary already
	// distinguishes a case the (class, domain) pair cannot express: a limiter
	// shed or stale-generation refusal is `upstream_unavailable`, a
	// sanitisation failure is `sanitisation` (FR-047). Zero means "derive".
	audit audit.ErrorClass
}

// ValidationOutcome is the outcome of mcpproxy rejecting a call's arguments
// before dispatch (Spec 085): the client's fault.
func ValidationOutcome() Outcome {
	return Outcome{Class: ClassProxyPolicy, Domain: DomainClient, audit: audit.ErrorClassValidation}
}

// UnavailableOutcome is the outcome of a call that could not be made because
// the upstream is not connected.
func UnavailableOutcome() Outcome {
	return Outcome{Class: ClassNetwork, Domain: DomainUpstream, audit: audit.ErrorClassUpstreamUnavailable}
}

// InternalOutcome is the catch-all for a proxy-side failure.
func InternalOutcome() Outcome {
	return Outcome{Class: ClassProxyInternal, Domain: DomainProxy}
}

// Classify maps a dispatch result onto an Outcome. The bool is false when the
// call succeeded (nil error and not an isError result).
//
// Rules, first match wins (FR-041).
func Classify(result *mcp.CallToolResult, err error, f Facts) (Outcome, bool) {
	if err == nil {
		if result != nil && result.IsError {
			return Outcome{Class: ClassToolError, Domain: DomainUpstream}, true
		}
		return Outcome{}, false
	}

	status := f.HTTPStatus
	var httpErr *proxytransport.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode != 0 {
		status = httpErr.StatusCode
	}
	nonOK := status != 0 && (status < 200 || status >= 300)
	upstreamStatus := 0
	if nonOK {
		upstreamStatus = status
	}

	// Session terminated.
	if errors.Is(err, transport.ErrSessionTerminated) {
		return Outcome{Class: ClassSessionTerminated, Domain: DomainUpstream, HTTPStatus: upstreamStatus}, true
	}

	// Auth.
	var oauthReq *transport.OAuthAuthorizationRequiredError
	var authReq *transport.AuthorizationRequiredError
	if errors.As(err, &oauthReq) || errors.As(err, &authReq) ||
		errors.Is(err, transport.ErrUnauthorized) ||
		errors.Is(err, transport.ErrOAuthAuthorizationRequired) ||
		errors.Is(err, transport.ErrAuthorizationRequired) ||
		status == 401 || status == 403 {
		return Outcome{Class: ClassAuth, Domain: DomainUpstream, HTTPStatus: upstreamStatus}, true
	}

	// mcpproxy's own refusals.
	var limitErr *limiter.LimitError
	if errors.As(err, &limitErr) {
		return Outcome{Class: ClassProxyPolicy, Domain: DomainProxy, audit: audit.ErrorClassUpstreamUnavailable}, true
	}
	if errors.Is(err, managed.ErrConnectionGenerationChanged) {
		return Outcome{Class: ClassProxyPolicy, Domain: DomainProxy, audit: audit.ErrorClassUpstreamUnavailable}, true
	}
	var blocked *profile.ToolBlockedError
	if errors.As(err, &blocked) ||
		errors.Is(err, profile.ErrToolOutsideProfile) ||
		errors.Is(err, profile.ErrCodeExecutionBlocked) {
		return Outcome{Class: ClassProxyPolicy, Domain: DomainProxy}, true
	}
	var verr *jsonschema.ValidationError
	if errors.As(err, &verr) {
		return ValidationOutcome(), true
	}
	if errors.Is(err, audit.ErrSanitisationFailed) {
		return Outcome{Class: ClassProxyInternal, Domain: DomainProxy, audit: audit.ErrorClassSanitisation}, true
	}

	// Cancellation: the caller walked away; not an upstream fault.
	if errors.Is(err, context.Canceled) || f.CallerCancelled {
		return Outcome{Class: ClassCancelled, Domain: DomainClient}, true
	}

	// Timeouts: upstream's if the request left the proxy, else the proxy's.
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return timeoutOutcome(f), true
	}

	// Network, on typed evidence of a dial / read / write / EOF failure.
	if isNetworkError(err) {
		return Outcome{Class: ClassNetwork, Domain: DomainUpstream}, true
	}

	// A JSON-RPC error response, recognised structurally (mcp-go maps the
	// standard codes onto sentinels via JSONRPCErrorDetails.AsError). It is a
	// JSON-RPC error even if the HTTP status was non-2xx.
	if isJSONRPCError(err) {
		return Outcome{Class: ClassJSONRPC, Domain: DomainUpstream, HTTPStatus: upstreamStatus}, true
	}

	// A recorded non-2xx HTTP status: mcp-go returns these as an untyped error,
	// usually wrapped in its generic *transport.Error — which is why that
	// wrapper counts as network only AFTER this rule.
	if nonOK {
		return Outcome{Class: ClassHTTP, Domain: DomainUpstream, HTTPStatus: status}, true
	}

	// mcp-go's generic transport wrapper with no more specific evidence.
	var trErr *transport.Error
	if errors.As(err, &trErr) {
		return Outcome{Class: ClassNetwork, Domain: DomainUpstream}, true
	}

	// Last resort, only when the typed chain is known to be lost: the call
	// never left the proxy, or HTTP requests went out and no response came
	// back. Never for stdio/SSE, where an upstream message may legitimately
	// contain any of these words.
	if !f.Dispatched || (f.HTTPRequests > 0 && !f.ResponseReceived) {
		if c, ok := fallbackClass(err); ok {
			if c == ClassTimeout {
				return timeoutOutcome(f), true
			}
			return Outcome{Class: ClassNetwork, Domain: DomainUpstream}, true
		}
	}

	// Any other error returned after the response was received: HTTP, the
	// recorder saw a 2xx; stdio, no HTTP request exists to observe. Both
	// surface every received JSON-RPC error through the same untyped path.
	if f.Dispatched && ((f.ResponseReceived && !nonOK) || f.HTTPRequests == 0) {
		return Outcome{Class: ClassJSONRPC, Domain: DomainUpstream}, true
	}

	return InternalOutcome(), true
}

func timeoutOutcome(f Facts) Outcome {
	d := DomainProxy
	if f.Dispatched {
		d = DomainUpstream
	}
	return Outcome{Class: ClassTimeout, Domain: d}
}

func isNetworkError(err error) bool {
	var (
		opErr  *net.OpError
		dnsErr *net.DNSError
		errno  syscall.Errno
		urlErr *url.Error
	)
	return errors.As(err, &opErr) || errors.As(err, &dnsErr) || errors.As(err, &errno) ||
		errors.As(err, &urlErr) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, transport.ErrTransportClosed)
}

func isJSONRPCError(err error) bool {
	var (
		rpc    *proxytransport.JSONRPCError
		elicit mcp.URLElicitationRequiredError
		proto  mcp.UnsupportedProtocolVersionError
		hdr    mcp.HeaderMismatchError
		capErr mcp.MissingRequiredClientCapabilityError
	)
	if errors.As(err, &rpc) || errors.As(err, &elicit) || errors.As(err, &proto) ||
		errors.As(err, &hdr) || errors.As(err, &capErr) {
		return true
	}
	for _, s := range []error{
		mcp.ErrParseError, mcp.ErrInvalidRequest, mcp.ErrMethodNotFound, mcp.ErrInvalidParams,
		mcp.ErrInternalError, mcp.ErrRequestInterrupted, mcp.ErrResourceNotFound,
	} {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

// Documented last-resort fallback (FR-049). These substrings are the stable
// wording of Go's net / net/http / syscall errors. They are consulted only
// when the error carried no typed network information AND the call is known
// not to have received a response, so they can never relabel an upstream's own
// message. Covered by TestClassifyFallbackListIsNarrow.
var (
	fallbackNetwork = []string{"connection refused", "connection reset", "no such host", "broken pipe", "unexpected eof"}
	fallbackTimeout = []string{"i/o timeout", "client.timeout exceeded", "context deadline exceeded"}
)

func fallbackClass(err error) (Class, bool) {
	msg := strings.ToLower(err.Error())
	for _, s := range fallbackTimeout {
		if strings.Contains(msg, s) {
			return ClassTimeout, true
		}
	}
	for _, s := range fallbackNetwork {
		if strings.Contains(msg, s) {
			return ClassNetwork, true
		}
	}
	return "", false
}
