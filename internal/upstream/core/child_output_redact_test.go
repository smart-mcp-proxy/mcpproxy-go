package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Issue #1158, review round 2, investigation 3.
//
// loggerWriter pumps the launched child's own stdout/stderr into the
// per-server log. That log is not a local-only artifact: `mcpproxy upstream
// logs`, the `upstream_servers tail_log` MCP tool and
// GET /api/v1/servers/{id}/logs all serve it. An upstream that echoes its
// configuration on startup — and plenty do, "connecting with token …" is a
// common startup banner — therefore publishes, on an HTTP surface, a
// credential mcpproxy itself handed it.
func TestLoggerWriter_ScrubsChildOutput(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	w := newLoggerWriter(zap.New(core), nil)

	const secret = "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"
	for _, line := range []string{
		"connecting to https://api.example/mcp?token=" + secret + "\n",
		"WARN using api_key=" + secret + "\n",
		"listening on http://127.0.0.1:9331\n",
	} {
		n, err := w.Write([]byte(line))
		require.NoError(t, err)
		assert.Equal(t, len(line), n, "the writer must report the bytes it consumed, not the bytes it logged")
	}

	var rendered []string
	for _, entry := range logs.All() {
		rendered = append(rendered, entry.Message)
	}
	joined := strings.Join(rendered, "\n")

	assert.NotContains(t, joined, secret, "the child's own output reaches a REST surface")
	assert.Contains(t, joined, "api.example", "the host stays readable")
	assert.Contains(t, joined, "listening on http://127.0.0.1:9331",
		"ordinary child output must round-trip byte-identical — a per-server log "+
			"nobody can read is a different bug")
}

// Issue #1158, review round 2, investigation 2: the token endpoint's non-200
// response BODY was embedded verbatim in the refresh error, which is logged
// with the server name and returned to the REST caller.
func TestCappedScrub_ScrubsAndCapsAnUpstreamBody(t *testing.T) {
	const secret = "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"

	body := `{"error":"invalid_grant","error_description":"bad refresh_token=` + secret + `"}`
	got := cappedScrub(body, 512)
	assert.NotContains(t, got, secret)
	assert.Contains(t, got, "invalid_grant", "the OAuth error CODE is the diagnostic")

	// Unbounded bodies (a proxy's HTML error page) must not go into the log whole.
	huge := strings.Repeat("A", 5000)
	assert.LessOrEqual(t, len(cappedScrub(huge, 512)), 512+len("… (truncated)"))

	// Scrub-then-cap: a credential straddling the cut must not survive as a
	// leading fragment.
	straddling := strings.Repeat("x", 500) + secret + strings.Repeat("y", 500)
	capped := cappedScrub(straddling, 512)
	for i := 0; i+8 <= len(secret); i++ {
		assert.NotContains(t, capped, secret[i:i+8])
	}
}
