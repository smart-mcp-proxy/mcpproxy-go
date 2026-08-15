package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 098 (required-tools preflight) T002/T024 — FR-015: the preflight
// feature adds a REST endpoint and a CLI command; it MUST NOT move the MCP
// surface. `tools/list` payloads have to stay byte-identical to the merge-base
// release across every routing mode an agent can be served.
//
// The goldens in testdata/toolslist_goldens/ were captured from the merge-base
// commit (bfd43e7ce, origin/main, pre-098) with this exact test file — copied
// into a throwaway `git worktree` of origin/main and run with
// MCPPROXY_WRITE_TOOLSLIST_GOLDENS set — so the capture and the comparison
// share one serializer and cannot drift.
//
// Unlike the spec-085/094 delta test in mcp_menu_surface_test.go (which allows
// an enumerated, intentional delta), this test allows NO delta at all. A
// failure here is a spec-098 regression, not a golden to refresh: goldens are
// only regenerated when a DIFFERENT, deliberate spec changes the MCP surface.
//
// Surfaces covered (the three routing modes that expose a static, built-in
// tool set):
//
//   - default_server      — the default /mcp server (`proxy.server`)
//   - retrieve_tools_mode — buildCallToolModeTools() (/mcp/call, /mcp/p/<slug>)
//   - code_execution_mode — buildCodeExecModeTools()
//
// Direct mode is deliberately excluded: its tools/list is a projection of live
// upstream catalogs (buildDirectModeTools → upstreamManager.DiscoverTools), so
// it has no static payload to snapshot. Direct-mode non-regression is covered
// by the dispatch-parity tests instead.

const (
	toolsListGoldenDir = "toolslist_goldens"

	// toolsListGoldenWriteEnv, when set to a directory, makes this test WRITE
	// the goldens instead of comparing them. Used once to capture the
	// merge-base surface from a detached worktree of origin/main. Never set it
	// to "fix" a failing run — see the doc comment above.
	toolsListGoldenWriteEnv = "MCPPROXY_WRITE_TOOLSLIST_GOLDENS"
)

// toolsListGoldenSurfaces is the surface name -> golden file basename map.
var toolsListGoldenSurfaces = []string{
	"default_server",
	"retrieve_tools_mode",
	"code_execution_mode",
}

// captureToolsListSurfaces serializes each routing mode's registered tool
// schemas exactly as an agent receives them from tools/list: name -> marshaled
// mcp.Tool (description, annotations, inputSchema, outputSchema, …).
func captureToolsListSurfaces(t *testing.T, proxy *MCPProxyServer) map[string]map[string]json.RawMessage {
	t.Helper()

	surfaces := map[string]map[string]json.RawMessage{
		"default_server":      {},
		"retrieve_tools_mode": {},
		"code_execution_mode": {},
	}

	for name, st := range proxy.server.ListTools() {
		raw, err := json.Marshal(st.Tool)
		require.NoError(t, err, "marshal default-server tool %q", name)
		surfaces["default_server"][name] = raw
	}
	for _, st := range proxy.buildCallToolModeTools() {
		raw, err := json.Marshal(st.Tool)
		require.NoError(t, err, "marshal retrieve_tools-mode tool %q", st.Tool.Name)
		surfaces["retrieve_tools_mode"][st.Tool.Name] = raw
	}
	for _, st := range proxy.buildCodeExecModeTools() {
		raw, err := json.Marshal(st.Tool)
		require.NoError(t, err, "marshal code_execution-mode tool %q", st.Tool.Name)
		surfaces["code_execution_mode"][st.Tool.Name] = raw
	}

	for _, surface := range toolsListGoldenSurfaces {
		require.NotEmpty(t, surfaces[surface], "surface %s registered no tools — the snapshot would be vacuous", surface)
	}
	return surfaces
}

// renderToolsListGolden produces the canonical golden bytes for one surface:
// MarshalIndent over a map (encoding/json sorts map keys and re-indents the
// embedded RawMessages), plus a trailing newline so the files are diffable.
func renderToolsListGolden(t *testing.T, tools map[string]json.RawMessage) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(tools, "", "  ")
	require.NoError(t, err)
	return append(raw, '\n')
}

func toolsListGoldenPath(surface string) string {
	return filepath.Join("testdata", toolsListGoldenDir, surface+".json")
}

// normalizeGoldenEOL strips CR from CRLF line endings. The goldens are pinned
// to LF in .gitattributes, but Windows runners default to core.autocrlf=true,
// so a checkout that predates (or ignores) that pin would otherwise fail the
// byte comparison on \r alone — a spurious FR-015 "regression".
func normalizeGoldenEOL(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// TestToolsListSnapshot_MatchesMergeBaseGoldens is the FR-015 gate.
func TestToolsListSnapshot_MatchesMergeBaseGoldens(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	surfaces := captureToolsListSurfaces(t, proxy)

	if outDir := os.Getenv(toolsListGoldenWriteEnv); outDir != "" {
		require.NoError(t, os.MkdirAll(outDir, 0o755))
		for _, surface := range toolsListGoldenSurfaces {
			path := filepath.Join(outDir, surface+".json")
			require.NoError(t, os.WriteFile(path, renderToolsListGolden(t, surfaces[surface]), 0o644))
			t.Logf("wrote golden %s (%d tools)", path, len(surfaces[surface]))
		}
		t.Skipf("goldens written to %s (%s set); comparison skipped", outDir, toolsListGoldenWriteEnv)
	}

	for _, surface := range toolsListGoldenSurfaces {
		surface := surface
		t.Run(surface, func(t *testing.T) {
			raw, err := os.ReadFile(toolsListGoldenPath(surface))
			require.NoError(t, err, "missing golden for surface %s", surface)
			want := normalizeGoldenEOL(raw)

			got := renderToolsListGolden(t, surfaces[surface])
			if bytes.Equal(got, want) {
				return
			}
			// Byte comparison failed: report the per-tool diff so the
			// regression is readable instead of a wall of JSON.
			reportToolsListDiff(t, surface, want, got)
			t.Errorf("surface %s: tools/list is not byte-identical to the merge-base golden (spec 098 FR-015)", surface)
		})
	}
}

// reportToolsListDiff decodes both sides and reports added/removed/changed
// tools individually.
func reportToolsListDiff(t *testing.T, surface string, want, got []byte) {
	t.Helper()

	var wantTools, gotTools map[string]json.RawMessage
	if err := json.Unmarshal(want, &wantTools); err != nil {
		t.Errorf("surface %s: golden is not valid JSON: %v", surface, err)
		return
	}
	if err := json.Unmarshal(got, &gotTools); err != nil {
		t.Errorf("surface %s: current surface is not valid JSON: %v", surface, err)
		return
	}

	var added, removed []string
	for name := range gotTools {
		if _, ok := wantTools[name]; !ok {
			added = append(added, name)
		}
	}
	for name := range wantTools {
		if _, ok := gotTools[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	assert.Empty(t, added, "surface %s: tools added to the MCP surface (FR-015 forbids any change)", surface)
	assert.Empty(t, removed, "surface %s: tools removed from the MCP surface (FR-015 forbids any change)", surface)

	names := make([]string, 0, len(wantTools))
	for name := range wantTools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		gotTool, ok := gotTools[name]
		if !ok {
			continue // already reported as removed
		}
		assert.JSONEq(t, string(wantTools[name]), string(gotTool),
			"surface %s: tool %q schema changed (FR-015)", surface, name)
	}
}
