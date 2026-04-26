package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

func boolPtr(b bool) *bool {
	return &b
}

func TestAnalyzeSessionRisk_LethalTrifecta(t *testing.T) {
	// All three risk categories present across different servers
	snapshot := &stateview.ServerStatusSnapshot{
		Servers: map[string]*stateview.ServerStatus{
			"github": {
				Name:      "github",
				Connected: true,
				Tools: []stateview.ToolInfo{
					{
						Name: "delete_repo",
						Annotations: &config.ToolAnnotations{
							DestructiveHint: boolPtr(true),
							OpenWorldHint:   boolPtr(false),
						},
					},
					{
						Name: "search_repos",
						Annotations: &config.ToolAnnotations{
							ReadOnlyHint:  boolPtr(true),
							OpenWorldHint: boolPtr(true), // open world
						},
					},
				},
			},
			"filesystem": {
				Name:      "filesystem",
				Connected: true,
				Tools: []stateview.ToolInfo{
					{
						Name: "write_file",
						Annotations: &config.ToolAnnotations{
							ReadOnlyHint: boolPtr(false), // write tool
						},
					},
				},
			},
		},
	}

	risk := analyzeSessionRisk(snapshot)

	assert.Equal(t, "high", risk.Level)
	assert.True(t, risk.HasOpenWorld)
	assert.True(t, risk.HasDestructive)
	assert.True(t, risk.HasWrite)
	assert.True(t, risk.LethalTrifecta)
	assert.NotEmpty(t, risk.Warning)
}

func TestAnalyzeSessionRisk_LowRisk(t *testing.T) {
	// Only read-only tools present
	snapshot := &stateview.ServerStatusSnapshot{
		Servers: map[string]*stateview.ServerStatus{
			"readonly-server": {
				Name:      "readonly-server",
				Connected: true,
				Tools: []stateview.ToolInfo{
					{
						Name: "list_items",
						Annotations: &config.ToolAnnotations{
							ReadOnlyHint:    boolPtr(true),
							DestructiveHint: boolPtr(false),
							OpenWorldHint:   boolPtr(false),
						},
					},
					{
						Name: "get_item",
						Annotations: &config.ToolAnnotations{
							ReadOnlyHint:    boolPtr(true),
							DestructiveHint: boolPtr(false),
							OpenWorldHint:   boolPtr(false),
						},
					},
				},
			},
		},
	}

	risk := analyzeSessionRisk(snapshot)

	assert.Equal(t, "low", risk.Level)
	assert.False(t, risk.HasOpenWorld)
	assert.False(t, risk.HasDestructive)
	assert.False(t, risk.HasWrite)
	assert.False(t, risk.LethalTrifecta)
	assert.Empty(t, risk.Warning)
}

func TestAnalyzeSessionRisk_MediumRisk(t *testing.T) {
	// Two of three categories present: destructive + open world but all read-only
	snapshot := &stateview.ServerStatusSnapshot{
		Servers: map[string]*stateview.ServerStatus{
			"server": {
				Name:      "server",
				Connected: true,
				Tools: []stateview.ToolInfo{
					{
						Name: "delete_thing",
						Annotations: &config.ToolAnnotations{
							DestructiveHint: boolPtr(true),
							ReadOnlyHint:    boolPtr(true),
							OpenWorldHint:   boolPtr(false),
						},
					},
					{
						Name: "search_web",
						Annotations: &config.ToolAnnotations{
							ReadOnlyHint:  boolPtr(true),
							OpenWorldHint: boolPtr(true),
						},
					},
				},
			},
		},
	}

	risk := analyzeSessionRisk(snapshot)

	assert.Equal(t, "medium", risk.Level)
	assert.True(t, risk.HasOpenWorld)
	assert.True(t, risk.HasDestructive)
	assert.False(t, risk.HasWrite)
	assert.False(t, risk.LethalTrifecta)
	assert.Empty(t, risk.Warning)
}

func TestAnalyzeSessionRisk_NilAnnotationsDefaultRisk(t *testing.T) {
	// Per MCP spec, nil annotations mean defaults:
	// openWorldHint defaults to true, destructiveHint defaults to true,
	// readOnlyHint defaults to false (not read-only)
	// So nil annotations should trigger all three risk categories
	snapshot := &stateview.ServerStatusSnapshot{
		Servers: map[string]*stateview.ServerStatus{
			"unknown-server": {
				Name:      "unknown-server",
				Connected: true,
				Tools: []stateview.ToolInfo{
					{
						Name:        "mysterious_tool",
						Annotations: nil, // No annotations at all
					},
				},
			},
		},
	}

	risk := analyzeSessionRisk(snapshot)

	assert.Equal(t, "high", risk.Level)
	assert.True(t, risk.HasOpenWorld, "nil openWorldHint should default to true")
	assert.True(t, risk.HasDestructive, "nil destructiveHint should default to true")
	assert.True(t, risk.HasWrite, "nil readOnlyHint should mean not read-only")
	assert.True(t, risk.LethalTrifecta)
}

func TestAnalyzeSessionRisk_DisconnectedServersIgnored(t *testing.T) {
	// Disconnected servers should not contribute to risk analysis
	snapshot := &stateview.ServerStatusSnapshot{
		Servers: map[string]*stateview.ServerStatus{
			"dangerous-server": {
				Name:      "dangerous-server",
				Connected: false, // Not connected
				Tools: []stateview.ToolInfo{
					{
						Name: "nuke_everything",
						Annotations: &config.ToolAnnotations{
							DestructiveHint: boolPtr(true),
							OpenWorldHint:   boolPtr(true),
						},
					},
				},
			},
		},
	}

	risk := analyzeSessionRisk(snapshot)

	assert.Equal(t, "low", risk.Level)
	assert.False(t, risk.HasOpenWorld)
	assert.False(t, risk.HasDestructive)
	assert.False(t, risk.HasWrite)
	assert.False(t, risk.LethalTrifecta)
}

func TestAnalyzeSessionRisk_EmptySnapshot(t *testing.T) {
	snapshot := &stateview.ServerStatusSnapshot{
		Servers: map[string]*stateview.ServerStatus{},
	}

	risk := analyzeSessionRisk(snapshot)

	assert.Equal(t, "low", risk.Level)
	assert.False(t, risk.LethalTrifecta)
}

func TestAnnotationFiltering_ReadOnlyOnly(t *testing.T) {
	tools := []annotatedSearchResult{
		{
			serverName: "s1",
			toolName:   "list_items",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint: boolPtr(true),
			},
		},
		{
			serverName: "s1",
			toolName:   "create_item",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint: boolPtr(false),
			},
		},
		{
			serverName:  "s1",
			toolName:    "unknown_tool",
			annotations: nil, // nil readOnlyHint defaults to not read-only
		},
	}

	filtered := filterByAnnotations(tools, true, false, false)

	assert.Len(t, filtered, 1)
	assert.Equal(t, "list_items", filtered[0].toolName)
}

func TestAnnotationFiltering_ExcludeDestructive(t *testing.T) {
	tools := []annotatedSearchResult{
		{
			serverName: "s1",
			toolName:   "list_items",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint:    boolPtr(true),
				DestructiveHint: boolPtr(false),
			},
		},
		{
			serverName: "s1",
			toolName:   "delete_item",
			annotations: &config.ToolAnnotations{
				DestructiveHint: boolPtr(true),
			},
		},
		{
			serverName:  "s1",
			toolName:    "unknown_tool",
			annotations: nil, // nil destructiveHint defaults to true
		},
	}

	filtered := filterByAnnotations(tools, false, true, false)

	assert.Len(t, filtered, 1)
	assert.Equal(t, "list_items", filtered[0].toolName)
}

func TestAnnotationFiltering_ExcludeDestructive_ReadOnlyNotExcluded(t *testing.T) {
	// Bug fix: tools with readOnlyHint=true but missing destructiveHint should NOT
	// be excluded by exclude_destructive. A read-only tool is inherently non-destructive.
	tools := []annotatedSearchResult{
		{
			serverName: "s1",
			toolName:   "read_only_tool",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint: boolPtr(true),
				// destructiveHint is nil — per MCP spec defaults to true,
				// but readOnlyHint=true overrides this.
			},
		},
		{
			serverName:  "s1",
			toolName:    "write_tool_no_annotations",
			annotations: &config.ToolAnnotations{
				// Both nil — defaults to destructive=true, readOnly=false
			},
		},
		{
			serverName:  "s1",
			toolName:    "nil_annotations",
			annotations: nil, // No annotations at all — defaults to destructive
		},
		{
			serverName: "s1",
			toolName:   "safe_write_tool",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint:    boolPtr(false),
				DestructiveHint: boolPtr(false),
			},
		},
	}

	filtered := filterByAnnotations(tools, false, true, false)

	assert.Len(t, filtered, 2)
	assert.Equal(t, "read_only_tool", filtered[0].toolName)
	assert.Equal(t, "safe_write_tool", filtered[1].toolName)
}

func TestAnnotationFiltering_ExcludeOpenWorld(t *testing.T) {
	tools := []annotatedSearchResult{
		{
			serverName: "s1",
			toolName:   "local_tool",
			annotations: &config.ToolAnnotations{
				OpenWorldHint: boolPtr(false),
			},
		},
		{
			serverName: "s1",
			toolName:   "web_search",
			annotations: &config.ToolAnnotations{
				OpenWorldHint: boolPtr(true),
			},
		},
		{
			serverName:  "s1",
			toolName:    "unknown_scope",
			annotations: nil, // nil openWorldHint defaults to true
		},
	}

	filtered := filterByAnnotations(tools, false, false, true)

	assert.Len(t, filtered, 1)
	assert.Equal(t, "local_tool", filtered[0].toolName)
}

func TestAnnotationFiltering_CombinedFilters(t *testing.T) {
	tools := []annotatedSearchResult{
		{
			serverName: "s1",
			toolName:   "safe_local_read",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint:    boolPtr(true),
				DestructiveHint: boolPtr(false),
				OpenWorldHint:   boolPtr(false),
			},
		},
		{
			serverName: "s1",
			toolName:   "safe_open_read",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint:    boolPtr(true),
				DestructiveHint: boolPtr(false),
				OpenWorldHint:   boolPtr(true),
			},
		},
		{
			serverName: "s1",
			toolName:   "destructive_local",
			annotations: &config.ToolAnnotations{
				DestructiveHint: boolPtr(true),
				OpenWorldHint:   boolPtr(false),
			},
		},
	}

	// read_only_only + exclude_open_world
	filtered := filterByAnnotations(tools, true, false, true)

	assert.Len(t, filtered, 1)
	assert.Equal(t, "safe_local_read", filtered[0].toolName)
}

func TestAnnotationFiltering_NoFiltersPassAll(t *testing.T) {
	tools := []annotatedSearchResult{
		{serverName: "s1", toolName: "tool1", annotations: nil},
		{serverName: "s1", toolName: "tool2", annotations: nil},
		{serverName: "s1", toolName: "tool3", annotations: nil},
	}

	filtered := filterByAnnotations(tools, false, false, false)

	assert.Len(t, filtered, 3)
}

// TestBuildSessionRiskResponse_WarningOmittedByDefault verifies that the prose
// `warning` field is excluded from the session_risk map when the include flag
// is false, even when the trifecta is detected. This is the issue #406 fix:
// default-off behavior to reduce token overhead and LLM distraction.
func TestBuildSessionRiskResponse_WarningOmittedByDefault(t *testing.T) {
	risk := SessionRisk{
		Level:          "high",
		HasOpenWorld:   true,
		HasDestructive: true,
		HasWrite:       true,
		LethalTrifecta: true,
		Warning:        "LETHAL TRIFECTA DETECTED: ...",
	}

	out := buildSessionRiskResponse(risk, false)

	// Structured fields are always present
	assert.Equal(t, "high", out["level"])
	assert.Equal(t, true, out["has_open_world_tools"])
	assert.Equal(t, true, out["has_destructive_tools"])
	assert.Equal(t, true, out["has_write_tools"])
	assert.Equal(t, true, out["lethal_trifecta"])

	// Warning prose must NOT appear when include flag is false
	_, hasWarning := out["warning"]
	assert.False(t, hasWarning, "warning prose must be omitted when includeWarning=false")
}

// TestBuildSessionRiskResponse_WarningIncludedWhenOptedIn verifies that the
// prose `warning` field is present when the caller opts in (config flag or
// per-call argument).
func TestBuildSessionRiskResponse_WarningIncludedWhenOptedIn(t *testing.T) {
	risk := SessionRisk{
		Level:          "high",
		HasOpenWorld:   true,
		HasDestructive: true,
		HasWrite:       true,
		LethalTrifecta: true,
		Warning:        "LETHAL TRIFECTA DETECTED: prose warning",
	}

	out := buildSessionRiskResponse(risk, true)

	// Structured fields are always present
	assert.Equal(t, "high", out["level"])
	assert.Equal(t, true, out["lethal_trifecta"])

	// Warning prose IS present when opted in
	warning, hasWarning := out["warning"].(string)
	require.True(t, hasWarning, "warning prose must be present when includeWarning=true")
	assert.Contains(t, warning, "LETHAL TRIFECTA")
}

// TestBuildSessionRiskResponse_NoWarningWhenLowRisk verifies that the warning
// field stays absent for low-risk sessions even when the include flag is true,
// because analyzeSessionRisk only sets Warning for the trifecta case.
func TestBuildSessionRiskResponse_NoWarningWhenLowRisk(t *testing.T) {
	risk := SessionRisk{
		Level:          "low",
		HasOpenWorld:   false,
		HasDestructive: false,
		HasWrite:       false,
		LethalTrifecta: false,
		Warning:        "",
	}

	out := buildSessionRiskResponse(risk, true)

	assert.Equal(t, "low", out["level"])
	_, hasWarning := out["warning"]
	assert.False(t, hasWarning, "warning must not be present when no trifecta")
}
