package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 112 x Spec 054: when a call forwards headers the recording sinks get a
// scrubbed copy of the result. That copy must be taken AFTER output
// sanitisation, or redact mode never reaches tool-call history / activity.
func TestForwardHeaders_RedactModeStillRedactsRecords(t *testing.T) {
	up := newFwdUpstream(t)
	servers := []*config.ServerConfig{fwdServerCfg("echoer", up.URL, withForward("X-Tenant-Id"))}
	env := startFwdEnv(t, servers, func(cfg *config.Config, _ string) {
		cfg.OutputSanitisation = config.DefaultOutputSanitisationConfig()
		cfg.OutputSanitisation.ResponseAction = "redact"
	})
	c := env.fwdClient("/mcp", map[string]string{"X-Tenant-Id": echoSentinel})

	clientText := fwdCallRead(t, c, "echoer", "leak")
	assert.NotContains(t, clientText, "AKIA1234567890ABCDEF", "client copy is redacted")

	rt := env.proxyServer.runtime
	rec := awaitToolCallActivity(t, rt, "echoer", "leak")
	assert.NotContains(t, rec.Response, "AKIA1234567890ABCDEF", "activity Response")
	assert.NotContains(t, rec.Response, echoSentinel)

	sc, err := rt.StorageManager().GetUpstreamServer("echoer")
	require.NoError(t, err)
	calls, err := rt.StorageManager().GetServerToolCalls(storage.GenerateServerID(sc), 20)
	require.NoError(t, err)
	require.NotEmpty(t, calls)
	for _, call := range calls {
		b, _ := json.Marshal(call)
		assert.False(t, strings.Contains(string(b), "AKIA1234567890ABCDEF"), "tool-call history must be redacted")
	}
}
