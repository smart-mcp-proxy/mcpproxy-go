package httpapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// Issue #1148, round 8 finding 2.
//
// `health.detail` and `diagnostic.cause` are the SAME two strings that
// oauth.RedactServerSecretFields scrubs on GET /api/v1/servers and on the
// /events servers.changed payload, where the rule is the name rule PLUS the
// value-shaped detector. The per-server diagnostics route scrubbed them with
// the name rule ALONE, so a credential under an unrecognised query parameter
// was masked on one door and published on its sibling.

const round8GHPToken = "ghp_1234567890abcdefghijABCDEFGHIJ123456"

func TestRedactHealthDetail_UsesTheSharedFreeTextRule(t *testing.T) {
	detail := `Post "https://h/mcp?opaque=` + round8GHPToken + `": no such host`

	got := redactHealthDetail(&contracts.HealthStatus{Detail: detail}, false)
	hs, ok := got.(*contracts.HealthStatus)
	require.True(t, ok)

	assert.NotContains(t, hs.Detail, round8GHPToken,
		"the diagnostics door published a credential its sibling REST door masks")
	assert.Equal(t, oauth.ScrubUpstreamText(detail), hs.Detail,
		"health.detail must be scrubbed with the one shared free-text rule")
}

func TestRedactDiagnosticCause_UsesTheSharedFreeTextRule(t *testing.T) {
	cause := `Get "https://h/mcp?opaque=` + round8GHPToken + `": connection refused`
	diag := map[string]interface{}{"cause": cause}

	redactDiagnosticCause(diag, false)

	assert.NotContains(t, diag["cause"], round8GHPToken,
		"the diagnostics door published a credential its sibling REST door masks")
	assert.Equal(t, oauth.ScrubUpstreamText(cause), diag["cause"],
		"diagnostic.cause must be scrubbed with the one shared free-text rule")
}

func TestRedactHealthDetail_RevealStillOptsOut(t *testing.T) {
	detail := "raw " + round8GHPToken
	got := redactHealthDetail(&contracts.HealthStatus{Detail: detail}, true)
	hs, ok := got.(*contracts.HealthStatus)
	require.True(t, ok)
	assert.Equal(t, detail, hs.Detail)
}
