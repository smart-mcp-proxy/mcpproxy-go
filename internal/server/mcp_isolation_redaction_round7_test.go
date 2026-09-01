package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Issue #1148, round 7 finding 3. `upstream_servers list` masked env, url,
// headers and args and then republished the isolation configuration RAW in the
// SAME payload:
//
//	servers[].docker_isolation.server_isolation.extra_args — the per-server override
//	docker_status.isolation_config                         — the WHOLE global block
//
// `extra_args` is free text an operator puts `-e API_KEY=<token>` into, and the
// global block is where one credential is configured for every isolated server
// at once. Both were built by reading the config struct field by field, which
// is why they were never covered by the walk that masks everything else.
const round7IsolationToken = "ghp_1234567890abcdefghijABCDEFGHIJ123456"

func TestUpstreamServersList_MasksIsolationConfig(t *testing.T) {
	p := createTestMCPProxyServer(t)
	p.config.DockerIsolation = &config.DockerIsolationConfig{
		Enabled:   true,
		ExtraArgs: []string{"-e", "GLOBAL_API_KEY=" + round7IsolationToken},
	}
	require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:     "srv",
		Protocol: "stdio",
		Command:  "uvx",
		Enabled:  true,
		Isolation: &config.IsolationConfig{
			ExtraArgs: []string{"-e", "SERVER_API_KEY=" + round7IsolationToken},
		},
	}))

	result, err := p.handleListUpstreams(auth.WithAuthContext(context.Background(), auth.AnonymousContext()))
	require.NoError(t, err)
	body := toolResultText(t, result)

	assert.NotContains(t, body, round7IsolationToken,
		"the isolation configuration published a credential in the clear")
	// The field NAMES stay: masked-but-present is the whole point — an operator
	// must still see WHICH variable is configured.
	assert.Contains(t, body, "extra_args")
	assert.Contains(t, body, "SERVER_API_KEY")
	assert.Contains(t, body, "GLOBAL_API_KEY")
}

// The reveal opt-in still works for an authenticated admin, exactly as it does
// for env/headers/url — the gate is the same one, not a new special case.
func TestUpstreamServersList_RevealSecretHeadersStillRevealsIsolation(t *testing.T) {
	p := createTestMCPProxyServer(t)
	p.config.RevealSecretHeaders = true
	p.config.DockerIsolation = &config.DockerIsolationConfig{
		Enabled:   true,
		ExtraArgs: []string{"-e", "GLOBAL_API_KEY=" + round7IsolationToken},
	}
	require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:     "srv",
		Protocol: "stdio",
		Command:  "uvx",
		Enabled:  true,
		Isolation: &config.IsolationConfig{
			ExtraArgs: []string{"-e", "SERVER_API_KEY=" + round7IsolationToken},
		},
	}))

	result, err := p.handleListUpstreams(auth.WithAuthContext(context.Background(), auth.AdminContext()))
	require.NoError(t, err)
	assert.Contains(t, toolResultText(t, result), round7IsolationToken,
		"reveal_secret_headers must keep working for an authenticated admin")
}

// Round 7 finding 1, through the real doors: read the redacted isolation block
// off the MCP read surface and echo it straight back at the MCP write door.
//
// The read door renders `-e API_KEY=<token>` as `-e API_KEY=***REDACTED***`
// (RedactSensitiveData), and the fail-closed net did not recognise that
// rendering — so the echo was ACCEPTED and the mask persisted over the live
// credential. The net is now derived from the renderers, so it refuses.
func TestBuildPatchConfig_RefusesAnEchoedRedactedMarker(t *testing.T) {
	p := createTestMCPProxyServer(t)
	stored := &config.ServerConfig{
		Name:      "srv",
		Command:   "uvx",
		Enabled:   true,
		Isolation: &config.IsolationConfig{ExtraArgs: []string{"-e", "API_KEY=" + round7IsolationToken}},
	}

	view := redactedServerView(stored, liveRedaction)
	isolationEcho, err := json.Marshal(view["isolation"])
	require.NoError(t, err)
	require.NotContains(t, string(isolationEcho), round7IsolationToken,
		"precondition: the read door masks isolation.extra_args")

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"operation":      "patch",
		"name":           "srv",
		"isolation_json": string(isolationEcho),
	}

	patch, _, err := p.buildPatchConfigFromRequest(request, stored)
	require.Error(t, err, "an echoed read-door mask must be refused, not persisted over the credential")
	assert.Nil(t, patch)
	assert.Contains(t, err.Error(), "redaction placeholder")
}
