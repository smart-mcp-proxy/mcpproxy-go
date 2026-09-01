package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

const round6Token = "ghp_1234567890abcdefghijABCDEFGHIJ123456"

// Issue #1148, round 6 finding 4. The MCP `upstream_servers add` door accepted
// echoed masks while REST `POST /api/v1/servers` refused them. No credential
// leaks either way — there is nothing stored to corrupt — but a client copying
// a masked payload into an add call silently persisted literal text like
// `••••AL (12 chars)` as an argv token or a header value, producing a server
// that fails to connect for a non-obvious reason. Both create doors now refuse.
func TestHandleAddUpstream_RefusesEveryEchoedMask(t *testing.T) {
	p := createTestMCPProxyServer(t)

	maskedArgs, err := json.Marshal(redactedArgs([]string{"mcp-foo", "--api-key", round6Token}, liveRedaction))
	require.NoError(t, err)
	maskedEnv, err := json.Marshal(map[string]string{"BENIGN": oauth.LiveRedaction.EnvValue("BENIGN", round6Token)})
	require.NoError(t, err)
	maskedHeaders, err := json.Marshal(map[string]string{"X-Weird": oauth.LiveRedaction.HeaderValue("X-Weird", round6Token)})
	require.NoError(t, err)

	cases := map[string]map[string]interface{}{
		"args_json":    {"name": "a1", "command": "uvx", "args_json": string(maskedArgs)},
		"env_json":     {"name": "a2", "command": "uvx", "env_json": string(maskedEnv)},
		"headers_json": {"name": "a3", "url": "https://host/mcp", "headers_json": string(maskedHeaders)},
		"url":          {"name": "a4", "url": oauth.LiveRedaction.URLValue("https://host/mcp?opaque=" + round6Token)},
		"oauth_json":   {"name": "a5", "url": "https://host/mcp", "oauth_json": `{"client_secret":"••••ab (12 chars)"}`},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = args

			result, err := p.handleAddUpstream(context.Background(), request)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.True(t, result.IsError, "add must refuse an echoed mask: %+v", result.Content)

			// Nothing may be persisted.
			servers, listErr := p.storage.ListUpstreamServers()
			require.NoError(t, listErr)
			for _, sc := range servers {
				assert.NotEqual(t, args["name"], sc.Name, "the refused server must not be stored")
			}
		})
	}
}

// The fail-closed net on the MCP patch door: a mask in a field that has no
// key-bound revert must be REFUSED, not persisted over whatever is stored.
// isolation.extra_args stands in for "every field nobody has given a binding",
// which is the shape a field ADDED to config.ServerConfig later would have.
func TestBuildPatchConfig_RefusesAMaskInAFieldWithNoRevert(t *testing.T) {
	p := createTestMCPProxyServer(t)
	stored := &config.ServerConfig{Name: "srv", Command: "uvx", Enabled: true}

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"operation":      "patch",
		"name":           "srv",
		"isolation_json": `{"extra_args":["--env","••••ab (12 chars)"]}`,
	}

	patch, _, err := p.buildPatchConfigFromRequest(request, stored)
	require.Error(t, err, "a mask with no binding must be refused")
	assert.Nil(t, patch)
	assert.Contains(t, err.Error(), "redaction placeholder")
}

// A legitimate patch — real values, no masks — must still go through. The net
// is a refusal of masks, not of writes.
func TestBuildPatchConfig_AcceptsRealValues(t *testing.T) {
	p := createTestMCPProxyServer(t)
	stored := &config.ServerConfig{
		Name:    "srv",
		Command: "uvx",
		Env:     map[string]string{"BENIGN": round6Token},
		Enabled: true,
	}

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"operation": "patch",
		"name":      "srv",
		"env_json":  `{"BENIGN":"ghp_ffffffffffffffffffffFFFFFFFFFF999999"}`,
	}

	patch, _, err := p.buildPatchConfigFromRequest(request, stored)
	require.NoError(t, err)
	require.NotNil(t, patch)
	assert.Equal(t, "ghp_ffffffffffffffffffffFFFFFFFFFF999999", patch.Env["BENIGN"])
}

// The MCP patch door must still REVERT what it can bind — the live rendering of
// a credential under a benign env name included. This is the round trip the
// REST door now shares (see internal/httpapi/round6_write_parity_test.go).
func TestBuildPatchConfig_RevertsTheLiveEnvMask(t *testing.T) {
	p := createTestMCPProxyServer(t)
	stored := &config.ServerConfig{
		Name:    "srv",
		Command: "uvx",
		Env:     map[string]string{"BENIGN": round6Token},
		Enabled: true,
	}
	masked := oauth.LiveRedaction.EnvValue("BENIGN", round6Token)
	require.NotEqual(t, round6Token, masked, "precondition: the read door masks it")

	body, err := json.Marshal(map[string]string{"BENIGN": masked})
	require.NoError(t, err)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"operation": "patch",
		"name":      "srv",
		"env_json":  string(body),
	}

	patch, _, err := p.buildPatchConfigFromRequest(request, stored)
	require.NoError(t, err)
	require.NotNil(t, patch)
	assert.Equal(t, round6Token, patch.Env["BENIGN"],
		"the stored credential must not be overwritten by its own mask")
}
