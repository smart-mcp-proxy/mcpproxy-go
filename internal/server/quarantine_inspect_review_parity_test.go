package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

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
