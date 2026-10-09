package core

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"syscall"

	"github.com/mark3labs/mcp-go/client"
	uptransport "github.com/mark3labs/mcp-go/client/transport"
)

// IsNetworkError reports whether err is (or wraps) a transport-level failure
// that says nothing about authentication: a DNS lookup, dial, reset or timeout.
//
// GH #1537: the auth ladder and the managed client classify failures by
// substring, and both match a bare "401"/"403". Go's net errors quote
// addresses and ephemeral ports ("read udp 192.168.1.20:54012->…:53: i/o
// timeout", "dial tcp 127.0.0.1:14010"), so a DNS failure during a network
// outage could read as an OAuth challenge and push a no-auth server into the
// OAuth strategy — where an empty token store parks it in Pending Auth for
// good. A typed check runs first so the address text is never consulted.
//
// A *url.Error in the chain means http.Client.Do failed before any response
// arrived (TLS verification, a connection closed with EOF, an HTTP/2 GOAWAY):
// whatever it is, no server asked for credentials, and the URL it quotes is
// exactly the text the substring classifiers must not see.
//
// A typed HTTP 401 from mcp-go always wins: it is a real challenge, even when
// it arrives wrapped together with other error text.
func IsNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if hasHTTPAuthChallenge(err) || client.IsOAuthAuthorizationRequiredError(err) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF)
}

// hasHTTPAuthChallenge reports whether err carries mcp-go's typed HTTP 401
// (AuthorizationRequiredError, or its OAuth-handler variant). Only a real 401
// response produces these, so this is the one piece of evidence that an
// upstream asked for credentials — unlike the substring classifiers, which
// also fire on "unauthorized" in an error page or a JSON-RPC message.
func hasHTTPAuthChallenge(err error) bool {
	var authErr *uptransport.AuthorizationRequiredError
	if errors.As(err, &authErr) {
		return true
	}
	var oauthErr *uptransport.OAuthAuthorizationRequiredError
	return errors.As(err, &oauthErr)
}

// authEvidence records what the anonymous strategies (headers, no-auth) saw
// before the ladder fell through to OAuth, so the OAuth strategy can tell an
// upstream that answered with a real 401 from one that merely produced
// auth-sounding error text.
type authEvidence struct {
	// challenged is true once an anonymous attempt received a typed HTTP 401.
	challenged bool
}

type authEvidenceKey struct{}

func withAuthEvidence(ctx context.Context, ev *authEvidence) context.Context {
	return context.WithValue(ctx, authEvidenceKey{}, ev)
}

func authEvidenceFrom(ctx context.Context) *authEvidence {
	ev, _ := ctx.Value(authEvidenceKey{}).(*authEvidence)
	return ev
}

// speculativeOAuthPark reports whether a Pending Auth park about to happen is
// a guess rather than a confirmed requirement (GH #1537): the operator
// declared no oauth block, no token was ever stored for the server, and the
// anonymous attempt never received an HTTP 401. Such a park is re-probed
// periodically (types.ConnectionInfo.PendingAuthProbe) instead of waiting
// forever for a sign-in the server may not support.
//
// A nil evidence pointer means the OAuth strategy was called outside the
// ladder (manual login, tests); that keeps the historical permanent park.
func (c *Client) speculativeOAuthPark(ctx context.Context, hasPersistedToken bool) bool {
	if c.oauthRequiredByConfig() || hasPersistedToken {
		return false
	}
	ev := authEvidenceFrom(ctx)
	return ev != nil && !ev.challenged
}
