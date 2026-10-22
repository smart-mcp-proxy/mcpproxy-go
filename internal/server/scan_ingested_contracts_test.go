package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestRT06_IngestedContracts_CarryOutputSchemas (Sol r1 P1): the real,
// storage-backed provider must hand the scanner the output schemas discovery
// ingested — the current one and, for a changed record, the approved one —
// or an output-schema-only TOCTOU payload is never scanned.
func TestRT06_IngestedContracts_CarryOutputSchemas(t *testing.T) {
	s := newAdmissionTestServer(t, newFakeSecurityScanner(), scanModeServer("srv", false))
	sm := s.runtime.StorageManager()
	require.NotNil(t, sm)
	require.NoError(t, sm.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "srv", ToolName: "summarize", Status: storage.ToolApprovalStatusChanged,
		ApprovedHash: "a", CurrentHash: "b",
		CurrentDescription:   "Summarizes text.",
		CurrentSchema:        `{"type":"object"}`,
		CurrentOutputSchema:  `{"properties":{"summary":{"description":"POISON-CURRENT","type":"string"}},"type":"object"}`,
		PreviousDescription:  "Summarizes text.",
		PreviousSchema:       `{"type":"object"}`,
		PreviousOutputSchema: `{"properties":{"summary":{"description":"POISON-PREVIOUS","type":"string"}},"type":"object"}`,
		IdentityKeyed:        true,
	}))

	p := &configServerInfoProvider{cfg: s.runtime.Config(), liveConfig: s.runtime.Config, server: s}
	contracts, err := p.GetIngestedToolContracts("srv")
	require.NoError(t, err)
	require.Len(t, contracts, 2)
	seen := map[string]bool{}
	for _, c := range contracts {
		out, ok := c["outputSchema"].(map[string]interface{})
		require.True(t, ok, "every ingested contract carries its output schema")
		props := out["properties"].(map[string]interface{})
		seen[props["summary"].(map[string]interface{})["description"].(string)] = true
	}
	assert.True(t, seen["POISON-CURRENT"])
	assert.True(t, seen["POISON-PREVIOUS"])
}

// TestRT06_LiveScanExport_CarriesOutputSchema: the scanner-facing live export
// includes the tool's output schema so the detector can see it.
func TestRT06_LiveScanExport_CarriesOutputSchema(t *testing.T) {
	s := newAdmissionTestServer(t, newFakeSecurityScanner(), scanModeServer("q-srv", true))
	tool := benignTool()
	tool.OutputSchemaJSON = `{"type":"object","properties":{"sum":{"type":"number"}}}`
	lister := &fakeLiveToolLister{connected: true, tools: []*config.ToolMetadata{tool}}
	p := &configServerInfoProvider{
		cfg: s.runtime.Config(), liveConfig: s.runtime.Config, server: s,
		liveTools: listerProviderHook("q-srv", lister),
	}
	tools, err := p.GetServerTools("q-srv")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	out, ok := tools[0]["outputSchema"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "object", out["type"])
}

// TestReprotectContracts_DescribeEveryCurrentDefinition (US3 judge finding 1):
// the post-activation cached scan is stored as the server's newest Pass-1 job,
// so it must describe the same tool set as a live scan - approved AND held
// (pending / changed) tools at their current definition - or it would replace
// a "dangerous" live verdict that rests on a held tool with a clean one.
func TestReprotectContracts_DescribeEveryCurrentDefinition(t *testing.T) {
	s := newAdmissionTestServer(t, newFakeSecurityScanner(), scanModeServer("srv", false))
	sm := s.runtime.StorageManager()
	require.NotNil(t, sm)
	for tool, status := range map[string]string{
		"approved_tool": storage.ToolApprovalStatusApproved,
		"changed_tool":  storage.ToolApprovalStatusChanged,
		"pending_tool":  storage.ToolApprovalStatusPending,
	} {
		require.NoError(t, sm.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "srv", ToolName: tool, Status: status,
			ApprovedHash: "a", CurrentHash: "b",
			CurrentDescription: "Current " + tool + ".", CurrentSchema: `{"type":"object"}`,
			PreviousDescription: "Previous " + tool + ".",
		}))
	}

	p := &configServerInfoProvider{cfg: s.runtime.Config(), liveConfig: s.runtime.Config, server: s}
	contracts, err := p.GetCurrentToolContracts("srv")
	require.NoError(t, err)
	got := map[string]string{}
	for _, c := range contracts {
		name, _ := c["name"].(string)
		desc, _ := c["description"].(string)
		got[name] = desc
	}
	assert.Equal(t, map[string]string{
		"approved_tool": "Current approved_tool.",
		"changed_tool":  "Current changed_tool.",
		"pending_tool":  "Current pending_tool.",
	}, got, "the cached scan must cover held tools at their CURRENT definition, never the previous one")
}
