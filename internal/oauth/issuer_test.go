package oauth

import (
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
)

// Spec 135 FR-003/FR-004/FR-004a/FR-014 (plan D2): the RFC 9207 decision on
// an authorization response, for every issuer source, advertised flag, iss
// shape and opt-out setting.
func TestDecideIssuer_Matrix(t *testing.T) {
	const issuer = "https://as.example.com"
	type want int
	const (
		refused want = iota
		unprotected
		protected
	)
	issQuery := func(mode string) url.Values {
		q := url.Values{"code": {"c"}, "state": {"s"}}
		switch mode {
		case "correct":
			q["iss"] = []string{issuer}
		case "absent":
		case "empty":
			q["iss"] = []string{""}
		case "wrong":
			q["iss"] = []string{"https://evil.example.com"}
		case "slash":
			q["iss"] = []string{issuer + "/"} // exact compare: a trailing slash is another issuer
		case "repeated":
			q["iss"] = []string{issuer, issuer}
		}
		return q
	}
	cases := []struct {
		source    IssuerSource
		supported bool
		iss       string
		skip      bool
		want      want
	}{
		// discovered / configured (verified): FR-003.
		{IssuerSourceDiscovered, true, "correct", false, protected},
		{IssuerSourceDiscovered, false, "correct", false, protected},
		{IssuerSourceDiscovered, true, "absent", false, refused},
		{IssuerSourceDiscovered, false, "absent", false, unprotected},
		{IssuerSourceDiscovered, false, "empty", false, refused},
		{IssuerSourceDiscovered, true, "empty", false, refused},
		{IssuerSourceDiscovered, false, "wrong", false, refused},
		{IssuerSourceDiscovered, true, "wrong", false, refused},
		{IssuerSourceDiscovered, true, "slash", false, refused},
		{IssuerSourceDiscovered, true, "repeated", false, refused},
		{IssuerSourceConfigured, true, "correct", false, protected},
		{IssuerSourceConfigured, true, "absent", false, refused},
		{IssuerSourceConfigured, false, "absent", false, unprotected},
		{IssuerSourceConfigured, false, "wrong", false, refused},
		// fallback: a present iss must match; absent is accepted (FR-004a).
		{IssuerSourceFallback, false, "correct", false, unprotected},
		{IssuerSourceFallback, false, "absent", false, unprotected},
		{IssuerSourceFallback, false, "wrong", false, refused},
		{IssuerSourceFallback, false, "empty", false, refused},
		// synthetic: the overrides are the trust anchor (FR-004).
		{IssuerSourceSynthetic, false, "absent", false, unprotected},
		{IssuerSourceSynthetic, false, "wrong", false, unprotected},
		{IssuerSourceSynthetic, false, "correct", false, unprotected},
		{IssuerSourceSynthetic, false, "repeated", false, refused},
		// opt-out: wrong or absent accepted, never protected (FR-014);
		// a repeated iss is still refused (FR-001 is never skipped).
		{IssuerSourceDiscovered, true, "wrong", true, unprotected},
		{IssuerSourceDiscovered, true, "absent", true, unprotected},
		{IssuerSourceDiscovered, true, "empty", true, unprotected},
		{IssuerSourceFallback, false, "wrong", true, unprotected},
		{IssuerSourceDiscovered, true, "repeated", true, refused},
	}
	for _, tc := range cases {
		name := string(tc.source) + "/" + tc.iss
		if tc.supported {
			name += "/advertised"
		}
		if tc.skip {
			name += "/skip"
		}
		t.Run(name, func(t *testing.T) {
			exp := ExpectedIssuer{Issuer: issuer, Source: tc.source, IssSupported: tc.supported, Verified: true}
			d, err := DecideAuthorizationResponseIssuer(issQuery(tc.iss), exp, tc.skip)
			switch tc.want {
			case refused:
				require.Error(t, err)
				var mm *IssuerMismatchError
				require.True(t, errors.As(err, &mm), "typed refusal, got %T", err)
				assert.Equal(t, diagnostics.OAuthIssuerMismatch, diagnostics.Classify(err, diagnostics.ClassifierHints{}))
				assert.False(t, d.Protected)
			case unprotected:
				require.NoError(t, err)
				assert.False(t, d.Protected)
			case protected:
				require.NoError(t, err)
				assert.True(t, d.Protected)
			}
		})
	}
}

// A configured metadata URL that is not one of the declared issuer's own
// candidates, or an insecure issuer, never yields a protected login, even
// when iss matches (FR-008, plan D2).
func TestDecideIssuer_ProtectedNeedsVerifiedAndSecure(t *testing.T) {
	q := url.Values{"code": {"c"}, "state": {"s"}, "iss": {"https://as.example.com"}}
	d, err := DecideAuthorizationResponseIssuer(q, ExpectedIssuer{
		Issuer: "https://as.example.com", Source: IssuerSourceConfigured, Verified: false,
	}, false)
	require.NoError(t, err)
	assert.False(t, d.Protected, "unverified configured URL")

	q.Set("iss", "http://as.example.com")
	d, err = DecideAuthorizationResponseIssuer(q, ExpectedIssuer{
		Issuer: "http://as.example.com", Source: IssuerSourceDiscovered, Insecure: true,
	}, false)
	require.NoError(t, err)
	assert.False(t, d.Protected, "insecure issuer")
}

// A flow with no recorded expectation is refused: nothing can be sent for a
// response whose issuer was never decided.
func TestDecideIssuer_NoExpectationRefused(t *testing.T) {
	q := url.Values{"code": {"c"}, "state": {"s"}}
	_, err := DecideAuthorizationResponseIssuer(q, ExpectedIssuer{}, false)
	var mm *IssuerMismatchError
	require.True(t, errors.As(err, &mm))
}

// The refusal never echoes the received iss (it is attacker-controlled) and
// names the expected issuer as an origin only (contracts/diagnostics-codes.md).
func TestIssuerMismatchError_TextIsOriginOnly(t *testing.T) {
	q := url.Values{"code": {"c"}, "state": {"s"}, "iss": {"https://evil.example/<script>"}}
	_, err := DecideAuthorizationResponseIssuer(q, ExpectedIssuer{
		Issuer: "https://as.example.com/tenant/x", Source: IssuerSourceDiscovered,
	}, false)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "evil")
	assert.NotContains(t, err.Error(), "/tenant/x")
	assert.Contains(t, err.Error(), "https://as.example.com")
	assert.Contains(t, err.Error(), string(diagnostics.OAuthIssuerMismatch))
}

// ExpectedIssuerFromMetadata derives the flow's snapshot from the document
// the handler holds (FR-002).
func TestExpectedIssuerFromMetadata(t *testing.T) {
	exp := ExpectedIssuerFromMetadata(IssuerSourceDiscovered, "https://as.example.com", true, "",
		"https://as.example.com/authorize", "https://as.example.com/token")
	assert.Equal(t, ExpectedIssuer{Issuer: "https://as.example.com", Source: IssuerSourceDiscovered, IssSupported: true}, exp)

	exp = ExpectedIssuerFromMetadata(IssuerSourceDiscovered, "https://as.example.com", false, "",
		"http://as.example.com/authorize", "https://as.example.com/token")
	assert.True(t, exp.Insecure, "an http endpoint on a public host is insecure")

	exp = ExpectedIssuerFromMetadata(IssuerSourceDiscovered, "https://as.example.com", false, "",
		"https://as.example.com/authorize", "https://as.example.com/token", "http://as.example.com/register")
	assert.True(t, exp.Insecure, "an http registration endpoint is insecure too")

	exp = ExpectedIssuerFromMetadata(IssuerSourceDiscovered, "http://127.0.0.1:9000", false, "",
		"http://127.0.0.1:9000/authorize", "http://127.0.0.1:9000/token")
	assert.False(t, exp.Insecure, "loopback http is allowed")

	exp = ExpectedIssuerFromMetadata(IssuerSourceConfigured, "https://as.example.com", false,
		"https://as.example.com/.well-known/oauth-authorization-server",
		"https://as.example.com/authorize", "https://as.example.com/token")
	assert.True(t, exp.Verified, "configured URL is a candidate of the declared issuer")

	exp = ExpectedIssuerFromMetadata(IssuerSourceConfigured, "https://as.example.com", false,
		"https://other.example.com/.well-known/oauth-authorization-server",
		"https://as.example.com/authorize", "https://as.example.com/token")
	assert.False(t, exp.Verified, "configured URL on another origin is not verified")
}
