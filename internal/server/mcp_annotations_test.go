package server

import (
	"fmt"
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

// --- Spec 094 T001: excludeReason parity against a FROZEN oracle ---
//
// shouldExclude now delegates to excludeReason, so comparing the two live
// functions would be circular. legacyShouldExcludeOracle is the verbatim body
// of shouldExclude as it stood before the spec-094 attribution refactor
// (internal/server/mcp_annotations.go:153-181 at 8ed4e4689) — an independent
// oracle that catches semantic drift the delegation cannot.
//
// Filter semantics are FROZEN by the spec: this file must never be "fixed" to
// match a changed implementation.
func legacyShouldExcludeOracle(annotations *config.ToolAnnotations, readOnlyOnly, excludeDestructive, excludeOpenWorld bool) bool {
	if readOnlyOnly {
		// Must have explicit readOnlyHint=true to pass
		if annotations == nil || annotations.ReadOnlyHint == nil || !*annotations.ReadOnlyHint {
			return true
		}
	}

	if excludeDestructive {
		// Exclude if destructiveHint is true or nil (default is true per spec).
		// However, a tool with readOnlyHint=true is inherently non-destructive,
		// so treat destructiveHint as false when readOnlyHint is explicitly true.
		isReadOnly := annotations != nil && annotations.ReadOnlyHint != nil && *annotations.ReadOnlyHint
		if !isReadOnly {
			if annotations == nil || annotations.DestructiveHint == nil || *annotations.DestructiveHint {
				return true
			}
		}
	}

	if excludeOpenWorld {
		// Exclude if openWorldHint is true or nil (default is true per spec)
		if annotations == nil || annotations.OpenWorldHint == nil || *annotations.OpenWorldHint {
			return true
		}
	}

	return false
}

// expectedAttribution derives the first-failure filter key and reason class
// straight from spec 094 FR-004 / data-model.md, independently of both the
// implementation and the oracle above.
func expectedAttribution(a *config.ToolAnnotations, readOnlyOnly, excludeDestructive, excludeOpenWorld bool) (filterKey string, explicit, excluded bool) {
	hint := func(p *bool) (set, val bool) {
		if a == nil || p == nil {
			return false, false
		}
		return true, *p
	}
	var roPtr, destPtr, owPtr *bool
	if a != nil {
		roPtr, destPtr, owPtr = a.ReadOnlyHint, a.DestructiveHint, a.OpenWorldHint
	}
	roSet, roVal := hint(roPtr)
	destSet, destVal := hint(destPtr)
	owSet, owVal := hint(owPtr)

	if readOnlyOnly {
		switch {
		case !roSet:
			return "read_only_only", false, true
		case !roVal:
			return "read_only_only", true, true
		}
	}

	explicitlyReadOnly := roSet && roVal
	if excludeDestructive && !explicitlyReadOnly {
		switch {
		case !destSet:
			return "exclude_destructive", false, true
		case destVal:
			return "exclude_destructive", true, true
		}
	}

	if excludeOpenWorld {
		switch {
		case !owSet:
			return "exclude_open_world", false, true
		case owVal:
			return "exclude_open_world", true, true
		}
	}

	return "", false, false
}

// hintStates enumerates the three possible states of one annotation hint.
var hintStates = []struct {
	label string
	value *bool
}{
	{"unset", nil},
	{"true", boolPtr(true)},
	{"false", boolPtr(false)},
}

// TestExcludeReason_ParityWithFrozenOracle exhausts the full domain: 27
// non-nil hint combinations + the nil-annotations state, times the 8 filter
// combinations = 224 cases. Every case asserts all three outputs.
func TestExcludeReason_ParityWithFrozenOracle(t *testing.T) {
	type annState struct {
		label       string
		annotations *config.ToolAnnotations
	}
	states := []annState{{"nil-annotations", nil}}
	for _, ro := range hintStates {
		for _, dest := range hintStates {
			for _, ow := range hintStates {
				states = append(states, annState{
					label: "readOnly=" + ro.label + "/destructive=" + dest.label + "/openWorld=" + ow.label,
					annotations: &config.ToolAnnotations{
						ReadOnlyHint:    ro.value,
						DestructiveHint: dest.value,
						OpenWorldHint:   ow.value,
					},
				})
			}
		}
	}
	require.Len(t, states, 28, "28 annotation states: 3^3 hint combos + nil annotations")

	filterCombos := []struct {
		readOnlyOnly, excludeDestructive, excludeOpenWorld bool
	}{
		{false, false, false},
		{true, false, false},
		{false, true, false},
		{false, false, true},
		{true, true, false},
		{true, false, true},
		{false, true, true},
		{true, true, true},
	}

	cases := 0
	for _, st := range states {
		for _, fc := range filterCombos {
			cases++
			name := fmt.Sprintf("%s|ro=%t,xd=%t,xow=%t", st.label, fc.readOnlyOnly, fc.excludeDestructive, fc.excludeOpenWorld)
			t.Run(name, func(t *testing.T) {
				gotKey, gotExplicit, gotExcluded := excludeReason(st.annotations, fc.readOnlyOnly, fc.excludeDestructive, fc.excludeOpenWorld)

				wantExcluded := legacyShouldExcludeOracle(st.annotations, fc.readOnlyOnly, fc.excludeDestructive, fc.excludeOpenWorld)
				assert.Equal(t, wantExcluded, gotExcluded, "excluded must match the frozen pre-refactor oracle")

				// shouldExclude delegates, so it must agree with the oracle too.
				assert.Equal(t, wantExcluded, shouldExclude(st.annotations, fc.readOnlyOnly, fc.excludeDestructive, fc.excludeOpenWorld),
					"shouldExclude must stay semantically frozen")

				wantKey, wantExplicit, wantExcludedAttr := expectedAttribution(st.annotations, fc.readOnlyOnly, fc.excludeDestructive, fc.excludeOpenWorld)
				require.Equal(t, wantExcludedAttr, wantExcluded, "test oracles disagree — fix the test, not the code")
				assert.Equal(t, wantKey, gotKey, "first-failure filter key (read-only -> destructive -> open-world)")
				assert.Equal(t, wantExplicit, gotExplicit, "reason class (missing vs explicit)")

				if !gotExcluded {
					assert.Empty(t, gotKey, "a kept tool has no responsible filter")
					assert.False(t, gotExplicit, "a kept tool has no reason class")
				}
			})
		}
	}
	assert.Equal(t, 224, cases, "exhaustive domain is 28 annotation states x 8 filter combos")
}

// The read-only shortcut is the subtlest frozen semantic: an explicitly
// read-only tool passes exclude_destructive even with destructiveHint=true,
// so it can only ever be attributed to read_only_only or exclude_open_world.
func TestExcludeReason_ReadOnlyShortcutAttribution(t *testing.T) {
	readOnlyDestructive := &config.ToolAnnotations{
		ReadOnlyHint:    boolPtr(true),
		DestructiveHint: boolPtr(true),
	}

	key, explicit, excluded := excludeReason(readOnlyDestructive, false, true, false)
	assert.False(t, excluded, "explicit readOnlyHint=true passes exclude_destructive (frozen shortcut)")
	assert.Empty(t, key)
	assert.False(t, explicit)

	// With open-world unset it falls through to the open-world filter, never
	// to exclude_destructive.
	key, explicit, excluded = excludeReason(readOnlyDestructive, false, true, true)
	assert.True(t, excluded)
	assert.Equal(t, "exclude_open_world", key)
	assert.False(t, explicit, "openWorldHint unset is a missing-annotation omission")
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
