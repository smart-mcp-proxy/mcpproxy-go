package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func TestInspectQuarantinedTools_UsesComposerWhenDefinitionsCaptured(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{
		Name: "github", Enabled: true, Quarantined: true,
	}})
	require.NoError(t, rt.StorageManager().SaveUpstreamServer(&config.ServerConfig{
		Name: "github", Enabled: true, Quarantined: true,
	}))
	destructive := true
	annotations := &config.ToolAnnotations{DestructiveHint: &destructive}
	require.NoError(t, rt.StorageManager().SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "delete_issue", Status: storage.ToolApprovalStatusPending,
		CurrentHash: "hash", CurrentDescription: "Deletes an issue", CurrentSchema: `{"type":"object"}`,
		CurrentAnnotations: annotations,
	}))

	review, err := rt.GetServerReview(context.Background(), "github")
	require.NoError(t, err)

	result, err := proxy.handleInspectQuarantinedTools(context.Background(), quarantineRequest(map[string]interface{}{"name": "github"}))
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	var payload struct {
		DefinitionsSource string                       `json:"definitions_source"`
		ServerSummary     internalRuntime.ReviewServer `json:"server_summary"`
		Tools             []internalRuntime.ReviewTool `json:"tools"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &payload))
	require.Equal(t, "captured", payload.DefinitionsSource)
	wantServer, err := json.Marshal(review.Server)
	require.NoError(t, err)
	gotServer, err := json.Marshal(payload.ServerSummary)
	require.NoError(t, err)
	require.JSONEq(t, string(wantServer), string(gotServer))
	wantTools, err := json.Marshal(review.Tools)
	require.NoError(t, err)
	gotTools, err := json.Marshal(payload.Tools)
	require.NoError(t, err)
	require.JSONEq(t, string(wantTools), string(gotTools))
}

// default_allowed is the review screens' fail-closed selection hint (D41). The
// captured inspection serialises the composer, so it must equal the REST value
// for a mix of tiers; inspect_tools is an approval-state listing and does not
// carry it.
func TestInspectQuarantinedCarriesDefaultAllowed(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{
		Name: "github", Enabled: true, Quarantined: true,
	}})
	require.NoError(t, rt.StorageManager().SaveUpstreamServer(&config.ServerConfig{
		Name: "github", Enabled: true, Quarantined: true,
	}))
	readOnly, writes := true, false
	for name, hint := range map[string]*bool{"list_issues": &readOnly, "create_issue": &writes} {
		require.NoError(t, rt.StorageManager().SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "github", ToolName: name, Status: storage.ToolApprovalStatusPending,
			CurrentHash: "h-" + name, CurrentDescription: name,
			CurrentAnnotations: &config.ToolAnnotations{ReadOnlyHint: hint},
		}))
	}
	review, err := rt.GetServerReview(context.Background(), "github")
	require.NoError(t, err)
	want := map[string]bool{}
	for _, tool := range review.Tools {
		want[tool.Name] = tool.DefaultAllowed
	}
	require.Len(t, want, 2)

	result, err := proxy.handleInspectQuarantinedTools(context.Background(), quarantineRequest(map[string]interface{}{"name": "github"}))
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	var payload struct {
		Tools []map[string]json.RawMessage `json:"tools"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &payload))
	require.Len(t, payload.Tools, 2)
	for _, tool := range payload.Tools {
		var name string
		require.NoError(t, json.Unmarshal(tool["name"], &name))
		raw, ok := tool["default_allowed"]
		require.Truef(t, ok, "captured inspection must carry default_allowed for %s", name)
		var got bool
		require.NoError(t, json.Unmarshal(raw, &got))
		require.Equal(t, want[name], got, name)
	}

	listed, err := proxy.handleInspectToolApprovals(context.Background(), quarantineRequest(map[string]interface{}{"name": "github"}))
	require.NoError(t, err)
	require.False(t, listed.IsError, "%v", listed.Content)
	var approvals struct {
		Tools []map[string]json.RawMessage `json:"tools"`
	}
	require.NoError(t, json.Unmarshal([]byte(listed.Content[0].(mcp.TextContent).Text), &approvals))
	require.NotEmpty(t, approvals.Tools)
	for _, tool := range approvals.Tools {
		require.NotContains(t, tool, "default_allowed", "inspect_tools is an approval listing, not a selection hint")
	}
}

func TestInspectToolsIncludesCanonicalReviewFields(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{
		Name: "github", Enabled: true, Quarantined: true,
	}})
	require.NoError(t, rt.StorageManager().SaveUpstreamServer(&config.ServerConfig{
		Name: "github", Enabled: true, Quarantined: true,
	}))
	readOnly := false
	previousReadOnly := true
	require.NoError(t, rt.StorageManager().SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "delete_issue", Status: storage.ToolApprovalStatusChanged,
		CurrentHash:        "current",
		CurrentDescription: "Deletes an issue", PreviousDescription: "Lists issues",
		CurrentSchema:       `{"type":"object","properties":{"issue":{"type":"string"}}}`,
		PreviousSchema:      `{"type":"object"}`,
		CurrentAnnotations:  &config.ToolAnnotations{ReadOnlyHint: &readOnly},
		PreviousAnnotations: &config.ToolAnnotations{ReadOnlyHint: &previousReadOnly},
	}))

	review, err := rt.GetServerReview(context.Background(), "github")
	require.NoError(t, err)
	require.Len(t, review.Tools, 1)

	result, err := proxy.handleInspectToolApprovals(context.Background(), quarantineRequest(map[string]interface{}{"name": "github"}))
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	var payload struct {
		Tools []map[string]json.RawMessage `json:"tools"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &payload))
	require.Len(t, payload.Tools, 1)

	canonical, err := json.Marshal(review.Tools[0])
	require.NoError(t, err)
	var want map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(canonical, &want))
	for _, field := range []string{"input_schema", "output_schema", "annotations", "tier", "approval_status", "disabled", "scan_verdict", "held_signals", "previous", "diff"} {
		gotValue, ok := payload.Tools[0][field]
		require.Truef(t, ok, "inspect_tools must expose canonical review field %q", field)
		require.JSONEqf(t, string(want[field]), string(gotValue), "inspect_tools field %q differs from the review composer", field)
	}
}

// inspect_quarantined serializes the review composer directly. Pin the
// credential-bearing configuration classes here as well as REST: this MCP
// operation is available to the unauthenticated/default MCP surface, while a
// scoped agent is intentionally denied quarantine operations before any review
// payload can be produced.
func TestInspectQuarantinedNeverLeaksReviewSecrets(t *testing.T) {
	const secret = "secret123"
	proxy, rt := createTestProxyWithRuntimeCfg(t, []*config.ServerConfig{{
		Name: "github", Enabled: true, Quarantined: true, Protocol: "stdio",
		Command: "server --token " + secret, Args: []string{"--token", secret},
		URL: "https://example.test/mcp?api_key=" + secret,
		Env: map[string]string{"TOKEN": secret}, Headers: map[string]string{"Authorization": "Bearer " + secret},
	}}, func(cfg *config.Config) { cfg.RevealSecretHeaders = true })
	require.NoError(t, rt.StorageManager().SaveUpstreamServer(&config.ServerConfig{
		Name: "github", Enabled: true, Quarantined: true, Protocol: "stdio",
		Command: "server --token " + secret, Args: []string{"--token", secret},
		URL: "https://example.test/mcp?api_key=" + secret,
		Env: map[string]string{"TOKEN": secret}, Headers: map[string]string{"Authorization": "Bearer " + secret},
	}))
	require.NoError(t, rt.StorageManager().SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "read", Status: storage.ToolApprovalStatusPending,
		CurrentDescription: "Read safely",
	}))

	admin, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "inspect_quarantined", "name": "github",
	}))
	require.NoError(t, err)
	require.False(t, admin.IsError)
	adminText := admin.Content[0].(mcp.TextContent).Text
	require.NotContains(t, adminText, secret)
	require.Contains(t, adminText, "••••23")

	// Agent tokens cannot use quarantine_security at all. The denial is the
	// scoped caller result, and must not accidentally serialize the server
	// summary while reporting it.
	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AllowedServers: []string{"github"},
	})
	agent, err := proxy.handleQuarantineSecurity(scoped, quarantineRequest(map[string]interface{}{
		"operation": "inspect_quarantined", "name": "github",
	}))
	require.NoError(t, err)
	require.True(t, agent.IsError)
	agentText := agent.Content[0].(mcp.TextContent).Text
	require.Contains(t, agentText, "cannot perform quarantine security operations")
	require.NotContains(t, agentText, secret)
}
