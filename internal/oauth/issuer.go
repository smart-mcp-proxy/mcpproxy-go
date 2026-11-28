package oauth

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
)

// IssuerSource says where a flow's expected issuer came from (Spec 135 FR-002).
type IssuerSource string

const (
	// IssuerSourceDiscovered: a metadata document found through discovery.
	IssuerSourceDiscovered IssuerSource = "discovered"
	// IssuerSourceConfigured: the operator's oauth.auth_server_metadata_url.
	IssuerSourceConfigured IssuerSource = "configured"
	// IssuerSourceFallback: no document and no override; mcp-go's default
	// endpoints derived from the origin (FR-004a).
	IssuerSourceFallback IssuerSource = "fallback"
	// IssuerSourceSynthetic: a document built from operator endpoint
	// overrides because the real one could not be fetched (FR-004).
	IssuerSourceSynthetic IssuerSource = "synthetic"
)

// ExpectedIssuer is the issuer snapshot a flow records when its
// authorization request is built (FR-002). The callback decision and the code
// exchange compare the authorization response against it.
type ExpectedIssuer struct {
	Issuer       string
	Source       IssuerSource
	IssSupported bool // authorization_response_iss_parameter_supported
	// Insecure: the issuer or an endpoint is http on a non-loopback host.
	Insecure bool
	// Verified (source configured only): the configured metadata URL is one
	// of the declared issuer's own well-known candidates (FR-008).
	Verified bool
}

// IssuerExpectation is what a pending flow carries: the snapshot and the
// server's oauth.skip_issuer_validation setting at the time the flow started.
type IssuerExpectation struct {
	Expected ExpectedIssuer
	Skip     bool
}

// Armed reports whether the expectation was recorded (a zero value was not).
func (e IssuerExpectation) Armed() bool { return e.Expected.Source != "" }

// IssuerDecision is the outcome of an accepted authorization response.
type IssuerDecision struct {
	// Protected: iss was present and equal, from an eligible snapshot, with
	// no check waived. Only such a login is mix-up protected.
	Protected bool
	// Waived names the check that was not applied ("" when none): a
	// synthetic source or the opt-out accepting a wrong or absent iss.
	Waived string
}

// IssuerMismatchError refuses an authorization response whose iss does not
// confirm the flow's expected issuer (MCPX_OAUTH_ISSUER_MISMATCH). Its text
// names the expected issuer as an origin only and never the received iss,
// which is attacker-controlled (contracts/diagnostics-codes.md).
type IssuerMismatchError struct {
	ExpectedOrigin string
	Reason         string
}

func (e *IssuerMismatchError) Error() string {
	expected := e.ExpectedOrigin
	if expected == "" {
		expected = "unknown"
	}
	return fmt.Sprintf("%s: the sign-in response could not be confirmed as coming from the expected authorization server (%s): %s",
		diagnostics.OAuthIssuerMismatch, expected, e.Reason)
}

// Code makes the refusal classify as MCPX_OAUTH_ISSUER_MISMATCH.
func (e *IssuerMismatchError) Code() diagnostics.Code { return diagnostics.OAuthIssuerMismatch }

// DecideAuthorizationResponseIssuer applies RFC 9207 to an authorization
// response query against the flow's expected issuer (Spec 135 FR-003,
// FR-004, FR-004a, FR-005, FR-014). It runs before the response's error is
// reported and before its code is exchanged.
//
//   - iss is present when len(q["iss"]) == 1, even with an empty value; an
//     empty value is a mismatch (stricter than mcp-go, research R6). A
//     repeated iss is always refused (FR-001 is never skipped).
//   - discovered, configured, fallback: a present iss must equal the expected
//     issuer exactly; an absent iss is refused when support was advertised
//     and accepted (not protected) otherwise.
//   - synthetic: the operator's overrides are the trust anchor; any iss is
//     accepted, never protected.
//   - skip: a wrong iss, or an absent one that was advertised, is accepted
//     and not protected; a match that waived nothing is decided as strict.
//
// A zero expectation (Source == "") is refused: nothing may be exchanged
// for a response whose issuer was never decided.
func DecideAuthorizationResponseIssuer(q url.Values, exp ExpectedIssuer, skip bool) (IssuerDecision, error) {
	refuse := func(reason string) (IssuerDecision, error) {
		return IssuerDecision{}, &IssuerMismatchError{ExpectedOrigin: issuerOrigin(exp.Issuer), Reason: reason}
	}
	values := q["iss"]
	if len(values) > 1 {
		return refuse("the response repeats the iss parameter")
	}
	if exp.Source == "" {
		return refuse("the flow recorded no expected issuer")
	}
	present := len(values) == 1
	iss := ""
	if present {
		iss = values[0]
	}

	if exp.Source == IssuerSourceSynthetic {
		// FR-004: accepted; the caller logs a present iss (WARN when Waived).
		if present && iss != exp.Issuer {
			return IssuerDecision{Waived: "synthetic_issuer"}, nil
		}
		return IssuerDecision{}, nil
	}

	var problem string
	switch {
	case present && iss != exp.Issuer:
		problem = "the iss parameter names another issuer"
	case !present && exp.IssSupported:
		problem = "the authorization server advertises iss, but the response has none"
	}
	if problem != "" {
		if skip {
			return IssuerDecision{Waived: "issuer_validation_disabled"}, nil
		}
		return refuse(problem)
	}
	if !present {
		return IssuerDecision{}, nil
	}
	// Under skip, a match whose checks all passed is protected as in strict
	// mode: only a check actually waived downgrades the login (contract
	// config-skip-issuer-validation.md "Protection result").
	eligible := exp.Source == IssuerSourceDiscovered || (exp.Source == IssuerSourceConfigured && exp.Verified)
	return IssuerDecision{Protected: eligible && !exp.Insecure}, nil
}

// ExpectedIssuerFromMetadata builds a flow's snapshot from the metadata
// document its handler holds. configuredURL is the operator's metadata URL
// (source configured only), checked against the declared issuer's own
// candidates for Verified. endpoints are the document's endpoints
// (authorization, token, registration); any http one on a non-loopback host
// makes the snapshot Insecure.
func ExpectedIssuerFromMetadata(source IssuerSource, issuer string, issSupported bool, configuredURL string, endpoints ...string) ExpectedIssuer {
	exp := ExpectedIssuer{Issuer: issuer, Source: source, IssSupported: issSupported}
	for _, u := range append([]string{issuer}, endpoints...) {
		if insecureURL(u) {
			exp.Insecure = true
		}
	}
	if source == IssuerSourceConfigured && configuredURL != "" {
		for _, c := range issuerMetadataCandidates(issuer) {
			if c == configuredURL {
				exp.Verified = true
				break
			}
		}
	}
	return exp
}

// issuerMetadataCandidates are the well-known URLs an issuer's own metadata
// may be published at (RFC 8414 §3.1 and OIDC Discovery), built from the
// escaped path. A path issuer has only path-derived forms.
func issuerMetadataCandidates(issuer string) []string {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme == "" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil
	}
	origin := u.Scheme + "://" + u.Host
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	if path == "" {
		return []string{
			origin + "/.well-known/oauth-authorization-server",
			origin + "/.well-known/openid-configuration",
		}
	}
	return []string{
		origin + "/.well-known/oauth-authorization-server" + path,
		origin + "/.well-known/openid-configuration" + path,
		origin + path + "/.well-known/openid-configuration",
	}
}

// insecureURL: an http URL whose host is not loopback.
func insecureURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "http") {
		return false
	}
	return !config.IsLoopbackHost(u.Hostname())
}

// NewIssuerMismatchError builds the refusal for a flow expecting issuer.
func NewIssuerMismatchError(issuer, reason string) *IssuerMismatchError {
	return &IssuerMismatchError{ExpectedOrigin: issuerOrigin(issuer), Reason: reason}
}

// issuerOriginOrUnknown is issuerOrigin for page text.
func issuerOriginOrUnknown(issuer string) string {
	if o := issuerOrigin(issuer); o != "" {
		return o
	}
	return "unknown"
}

// issuerOrigin reduces an issuer to scheme://host[:port] for user-facing
// text: a path or tenant is never shown (contracts/diagnostics-codes.md).
func issuerOrigin(issuer string) string {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	host := u.Hostname()
	if port := u.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host
}
