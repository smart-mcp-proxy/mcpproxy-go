package oauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"syscall"

	transport "github.com/mark3labs/mcp-go/client/transport"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/stringutil"
)

// RefreshErrorClass is the structured class of an OAuth refresh failure
// (Spec 113 FR-007/FR-008). It drives both retry handling and the
// Prometheus result label.
type RefreshErrorClass string

const (
	// RefreshClassSuccess is the class of a nil error.
	RefreshClassSuccess RefreshErrorClass = "success"
	// RefreshClassInvalidGrant: RFC 6749 §5.2 invalid_grant. Terminal; the user must log in again.
	RefreshClassInvalidGrant RefreshErrorClass = "invalid_grant"
	// RefreshClassInvalidClient: RFC 6749 §5.2 invalid_client. Terminal; see FR-009 for DCR handling.
	RefreshClassInvalidClient RefreshErrorClass = "invalid_client"
	// RefreshClassTerminalOther: unauthorized_client, unsupported_grant_type,
	// invalid_scope, or no usable client credentials. Terminal.
	RefreshClassTerminalOther RefreshErrorClass = "terminal_other"
	// RefreshClassServerError: OAuth server_error/temporarily_unavailable, HTTP 5xx or 429. Transient.
	RefreshClassServerError RefreshErrorClass = "server_error"
	// RefreshClassNetwork: connection/timeout failures. Transient.
	RefreshClassNetwork RefreshErrorClass = "network"
	// RefreshClassServerGone: the server was removed or no longer uses OAuth. Terminal.
	RefreshClassServerGone RefreshErrorClass = "server_gone"
	// RefreshClassOther: anything else. Existing backoff, then the existing give-up.
	RefreshClassOther RefreshErrorClass = "other"
)

// IsTerminal reports whether retrying the refresh cannot succeed without user action.
func (c RefreshErrorClass) IsTerminal() bool {
	switch c {
	case RefreshClassInvalidGrant, RefreshClassInvalidClient, RefreshClassTerminalOther, RefreshClassServerGone:
		return true
	default:
		return false
	}
}

// MetricLabel maps the class onto the oauth refresh metric result label. The
// pre-Spec-113 values are kept; failed_invalid_client and failed_server_error are new.
func (c RefreshErrorClass) MetricLabel() string {
	switch c {
	case RefreshClassSuccess:
		return "success"
	case RefreshClassInvalidGrant:
		return "failed_invalid_grant"
	case RefreshClassInvalidClient:
		return "failed_invalid_client"
	case RefreshClassServerError:
		return "failed_server_error"
	case RefreshClassNetwork:
		return "failed_network"
	case RefreshClassServerGone:
		return "failed_server_gone"
	default:
		return "failed_other"
	}
}

var (
	// ErrTokenRefreshTransient is returned by a refresher-bound token store when
	// a refresh failed transiently (or is cooling down after such a failure).
	// It is returned instead of the expired token so mcp-go never starts its
	// own, unserialized refresh (Spec 113 FR-005).
	ErrTokenRefreshTransient = errors.New("oauth: token refresh failed transiently")

	// ErrNoClientCredentials means neither the live handler nor the stored
	// record has a client id to refresh with (e.g. the DCR registration was
	// cleared after invalid_client). A refresh is never sent with an empty
	// client_id; the user has to sign in again.
	ErrNoClientCredentials = errors.New("oauth: no client credentials available for token refresh; sign in again")
)

// RefreshHTTPError is the typed error of mcpproxy's own refresh request (the
// stored-DCR-credentials path). Detail is already scrubbed and capped by the
// producer; the raw body is never kept.
type RefreshHTTPError struct {
	Status    int
	OAuthCode string
	Detail    string
}

func (e *RefreshHTTPError) Error() string {
	msg := fmt.Sprintf("token refresh failed with status %d", e.Status)
	if e.OAuthCode != "" {
		msg += " (error=" + e.OAuthCode + ")"
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

// RefreshFailure is a refresh error after coordinator handling: it carries the
// final class (which may differ from the raw error's, e.g. FR-009 messages)
// and an actionable message for the user.
type RefreshFailure struct {
	Class   RefreshErrorClass
	Message string
	Err     error
}

func (e *RefreshFailure) Error() string {
	if e.Err == nil {
		return e.Message
	}
	if e.Message == "" {
		return e.Err.Error()
	}
	return e.Message + ": " + e.Err.Error()
}

func (e *RefreshFailure) Unwrap() error { return e.Err }

// mcpGoStatusRe matches mcp-go's fixed non-JSON refresh failure text
// ("refresh token request failed with status %d: %s") and mcpproxy's own
// manual-path text.
var mcpGoStatusRe = regexp.MustCompile(`(?:refresh token request|token refresh) failed with status (\d{3})`)

// ClassifyRefreshError classifies a refresh error structurally (Spec 113
// FR-007): the RFC 6749 §5.2 error code of a transport.OAuthError anywhere in
// the chain, the HTTP status of a RefreshHTTPError or of mcp-go's fixed
// non-JSON failure text, then network error types. Substring matching is only
// the last fallback for non-compliant bodies. The returned status is 0 when it
// is not recoverable (mcp-go JSON error bodies drop it).
func ClassifyRefreshError(err error) (RefreshErrorClass, int) {
	if err == nil {
		return RefreshClassSuccess, 0
	}

	var failure *RefreshFailure
	if errors.As(err, &failure) && failure.Class != "" {
		return failure.Class, statusOf(err)
	}

	if errors.Is(err, ErrTokenRefreshTransient) {
		return RefreshClassServerError, statusOf(err)
	}
	if errors.Is(err, ErrServerNotOAuth) {
		return RefreshClassServerGone, 0
	}
	if errors.Is(err, ErrNoClientCredentials) {
		return RefreshClassTerminalOther, 0
	}

	status := statusOf(err)

	if code := oauthCodeOf(err); code != "" {
		if cls, ok := classifyOAuthCode(code); ok {
			return cls, status
		}
		// Unknown code: let the status decide, else other.
		if cls, ok := classifyStatus(status); ok {
			return cls, status
		}
		return RefreshClassOther, status
	}

	if cls, ok := classifyStatus(status); ok {
		return cls, status
	}

	if isNetworkError(err) {
		return RefreshClassNetwork, status
	}

	return classifyBySubstring(err.Error()), status
}

func classifyOAuthCode(code string) (RefreshErrorClass, bool) {
	switch code {
	case "invalid_grant":
		return RefreshClassInvalidGrant, true
	case "invalid_client":
		return RefreshClassInvalidClient, true
	case "unauthorized_client", "unsupported_grant_type", "invalid_scope":
		return RefreshClassTerminalOther, true
	case "server_error", "temporarily_unavailable":
		return RefreshClassServerError, true
	default:
		return "", false
	}
}

func classifyStatus(status int) (RefreshErrorClass, bool) {
	if status == 429 || (status >= 500 && status <= 599) {
		return RefreshClassServerError, true
	}
	return "", false
}

func oauthCodeOf(err error) string {
	var httpErr *RefreshHTTPError
	if errors.As(err, &httpErr) && httpErr.OAuthCode != "" {
		return httpErr.OAuthCode
	}
	// mcp-go defines Error() on the value type and wraps the value with %w.
	var oe transport.OAuthError
	if errors.As(err, &oe) && oe.ErrorCode != "" {
		return oe.ErrorCode
	}
	var oep *transport.OAuthError
	if errors.As(err, &oep) && oep != nil && oep.ErrorCode != "" {
		return oep.ErrorCode
	}
	return ""
}

func statusOf(err error) int {
	var httpErr *RefreshHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status
	}
	if m := mcpGoStatusRe.FindStringSubmatch(err.Error()); m != nil {
		if n, convErr := strconv.Atoi(m[1]); convErr == nil {
			return n
		}
	}
	return 0
}

func isNetworkError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ECONNABORTED,
			syscall.ETIMEDOUT, syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.EPIPE:
			return true
		}
	}
	return false
}

// classifyBySubstring is the last-resort fallback for non-compliant bodies
// and for errors that crossed a string boundary. Every pattern is covered by
// TestClassifyRefreshError_Structured.
func classifyBySubstring(errStr string) RefreshErrorClass {
	for _, pattern := range []string{"server not found", "server does not use OAuth"} {
		if stringutil.ContainsIgnoreCase(errStr, pattern) {
			return RefreshClassServerGone
		}
	}
	for _, pattern := range []string{"invalid_grant", "refresh token expired", "refresh token revoked", "refresh token invalid"} {
		if stringutil.ContainsIgnoreCase(errStr, pattern) {
			return RefreshClassInvalidGrant
		}
	}
	if stringutil.ContainsIgnoreCase(errStr, "invalid_client") {
		return RefreshClassInvalidClient
	}
	for _, pattern := range []string{"timeout", "connection refused", "connection reset", "no such host", "dial tcp", "network", "EOF", "context deadline exceeded"} {
		if stringutil.ContainsIgnoreCase(errStr, pattern) {
			return RefreshClassNetwork
		}
	}
	return RefreshClassOther
}
