package transport

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// Issue #1158, review round 2 (confirmed minor).
//
// The truncated-response-body log line scrubbed AFTER truncating:
//
//	oauth.ScrubUpstreamText(body[:1000])
//
// so a credential straddling byte 1000 reached the detectors as a FRAGMENT.
// Every vendor-shaped matcher is anchored on a complete token — a `ghp_` of the
// right length, a JWT with three segments — so the fragment matched nothing and
// the secret's leading bytes were published on a line that claims to be
// redacted.
//
// This test places a real credential across the cut and asserts no run of it
// survives.
func TestScrubbedPreview_ScrubsBeforeItTruncates(t *testing.T) {
	const secret = "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"
	const limit = 1000

	// Start the token 12 bytes before the cut, so ~28 bytes of it sit past it.
	filler := strings.Repeat("x", limit-12)
	body := filler + secret + strings.Repeat("y", 4000)

	require.Contains(t, oauth.ScrubUpstreamText(body[:limit]), secret[:12],
		"sanity: truncate-then-scrub really does publish the leading fragment; "+
			"if this stops holding the test below proves nothing")

	got := scrubbedPreview(body, limit)
	for i := 0; i+8 <= len(secret); i++ {
		assert.NotContains(t, got, secret[i:i+8],
			"an 8-byte run of the credential survived the preview")
	}
	assert.LessOrEqual(t, len(got), limit, "the preview must still be capped")
	assert.True(t, utf8.ValidString(got),
		"the mask rendering is multi-byte; the cut must land on a rune boundary")
}

// A body shorter than the cap round-trips through the scrub only.
func TestScrubbedPreview_ShortBodyIsNotTruncated(t *testing.T) {
	assert.Equal(t, "hello world", scrubbedPreview("hello world", 1000))
}

// The cut must not split a U+2022 bullet, which is what the mask is made of.
func TestScrubbedPreview_CutsOnARuneBoundary(t *testing.T) {
	body := strings.Repeat("z", 20) + "api_key=" + strings.Repeat("s3cr3tv4lu3", 200)
	for limit := 24; limit < 60; limit++ {
		got := scrubbedPreview(body, limit)
		assert.True(t, utf8.ValidString(got), "limit %d produced invalid UTF-8: %q", limit, got)
	}
}

// The transport's logSafeErrorField had drifted from its twin in
// internal/upstream/core: it still ran the NAME rule only, while the twin had
// moved to ScrubUpstreamText. Same rule, same class of text, one answer.
func TestTransportLogSafeErrorField_RunsTheValueShapedDetector(t *testing.T) {
	const secret = "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"
	err := errorString(`Post "https://host.example/mcp?opaque=` + secret + `": dial tcp: timeout`)

	require.Contains(t, oauth.RedactSensitiveData(err.Error()), secret,
		"sanity: the name rule cannot see a credential under an unrecognised parameter name")

	assert.NotContains(t, logSafeErrorField(err).String, secret)
	assert.Contains(t, logSafeErrorField(err).String, "host.example")
}

type errorString string

func (e errorString) Error() string { return string(e) }
