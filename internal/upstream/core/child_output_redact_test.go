package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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

// The stdio stderr PUMP is a different code path from the launcher's
// loggerWriter and feeds three sinks at once: main.log, the per-server log
// (served by GET /api/v1/servers/{id}/logs) and the recent-stderr ring buffer
// that connectStdio splices into the "did not respond to MCP initialize"
// error. A live run proved all three published a token a child printed on
// startup.
func TestMonitorStderr_ScrubsAllThreeSinks(t *testing.T) {
	const secret = "sk-live-ARGVSECRET0123456789"

	mainCore, mainLogs := observer.New(zap.DebugLevel)
	perServerCore, perServerLogs := observer.New(zap.DebugLevel)
	c := &Client{
		config:         &config.ServerConfig{Name: "alpha"},
		logger:         zap.New(mainCore),
		upstreamLogger: zap.New(perServerCore),
	}

	c.monitorStderr(context.Background(),
		strings.NewReader("starting with token "+secret+"\nlistening on 127.0.0.1:9331\n"))

	for name, logs := range map[string]*observer.ObservedLogs{"main": mainLogs, "per-server": perServerLogs} {
		var joined string
		for _, entry := range logs.All() {
			joined += entry.Message
			for k, v := range entry.ContextMap() {
				joined += " " + k + "=" + toStr(v)
			}
		}
		assert.NotContains(t, joined, secret, "%s log published the child's own credential", name)
		assert.Contains(t, joined, "listening on 127.0.0.1:9331", "%s log lost an ordinary line", name)
	}

	snapshot := strings.Join(c.RecentStderrSnapshot(), "\n")
	assert.NotContains(t, snapshot, secret,
		"the recent-stderr buffer is spliced into a connect error that is logged AND returned over REST")
	assert.Contains(t, snapshot, "listening on 127.0.0.1:9331")
}

// redactURLCredentialsInError is the ONE seam every downstream log site
// inherits — managed.Client, upstream.Manager, the supervisor and the runtime
// all zap.Error the value it returns. It ran the NAME rule only, so a
// credential under an unrecognised query-parameter name reached main.log
// fifteen times in a live run.
func TestRedactURLCredentialsInError_RunsTheValueShapedDetector(t *testing.T) {
	const secret = "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"
	err := errors.New(`Post "https://127.0.0.1:19999/mcp?opaque=` + secret + `": connection refused`)

	got := redactURLCredentialsInError(err)
	assert.NotContains(t, got.Error(), secret)
	assert.Contains(t, got.Error(), "connection refused",
		"the connect paths classify on this substring; masking must not eat it")
	assert.ErrorIs(t, got, err, "the original must stay reachable through Unwrap")
}
