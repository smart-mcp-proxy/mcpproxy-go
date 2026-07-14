package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 085 US4 T036 — FR-014 / SC-003 / SC-007: the built-in tool surface may
// differ from the pre-feature snapshot by EXACTLY:
//
//  1. one added tool: describe_tool (retrieve_tools-mode surfaces only);
//  2. the added optional `detail` parameter on every retrieve_tools
//     registration (all pre-feature parameters preserved byte-equal);
//  3. the FR-014 description updates on retrieve_tools and the call_tool_*
//     variants (referencing compact signatures + describe_tool instead of
//     instructing agents to read inputSchema from retrieve_tools).
//
// Everything else — tool names, counts, schemas, annotations — must be
// byte-identical to the pre-feature snapshot. No renames, no removals.
//
// The snapshot (testdata/tools_list_prefeature.golden.json) was captured from
// the merge-base commit (95cfcfed, pre-Spec-085) by serializing the same three
// surfaces this test rebuilds: the default server's tools/list, and the
// buildCallToolModeTools / buildCodeExecModeTools routing-mode toolsets.

// surfaceSnapshot is surface name -> tool name -> marshaled mcp.Tool.
type surfaceSnapshot map[string]map[string]json.RawMessage

func loadPreFeatureSurface(t *testing.T) surfaceSnapshot {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "tools_list_prefeature.golden.json"))
	require.NoError(t, err)
	var snap surfaceSnapshot
	// Trailing-newline tolerant by construction: json.Unmarshal ignores
	// surrounding whitespace, so pre-commit newline normalization is harmless.
	require.NoError(t, json.Unmarshal(data, &snap))
	require.NotEmpty(t, snap["default_server"])
	require.NotEmpty(t, snap["call_tool_mode"])
	require.NotEmpty(t, snap["code_execution_mode"])
	return snap
}

func currentSurface(t *testing.T, proxy *MCPProxyServer) surfaceSnapshot {
	t.Helper()
	snap := surfaceSnapshot{
		"default_server":      {},
		"call_tool_mode":      {},
		"code_execution_mode": {},
	}
	for name, st := range proxy.server.ListTools() {
		raw, err := json.Marshal(st.Tool)
		require.NoError(t, err)
		snap["default_server"][name] = raw
	}
	for _, st := range proxy.buildCallToolModeTools() {
		raw, err := json.Marshal(st.Tool)
		require.NoError(t, err)
		snap["call_tool_mode"][st.Tool.Name] = raw
	}
	for _, st := range proxy.buildCodeExecModeTools() {
		raw, err := json.Marshal(st.Tool)
		require.NoError(t, err)
		snap["code_execution_mode"][st.Tool.Name] = raw
	}
	return snap
}

// asMap decodes a marshaled tool into a generic map for structural diffing.
func asMap(t *testing.T, raw json.RawMessage) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

func sortedNames(m map[string]json.RawMessage) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// schemaProps returns inputSchema.properties of a decoded tool (may be nil).
func schemaProps(tool map[string]interface{}) map[string]interface{} {
	schema, _ := tool["inputSchema"].(map[string]interface{})
	if schema == nil {
		return nil
	}
	props, _ := schema["properties"].(map[string]interface{})
	return props
}

var callToolVariants = map[string]bool{
	"call_tool_read":        true,
	"call_tool_write":       true,
	"call_tool_destructive": true,
}

func TestMenuSurface_ExactDeltaFromPreFeature(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	pre := loadPreFeatureSurface(t)
	cur := currentSurface(t, proxy)

	// describe_tool is the ONLY addition, and only on the retrieve_tools-mode
	// surfaces (FR-011: not code_execution, not direct).
	wantAdded := map[string][]string{
		"default_server":      {"describe_tool"},
		"call_tool_mode":      {"describe_tool"},
		"code_execution_mode": {},
	}

	for surface, preTools := range pre {
		surface, preTools := surface, preTools
		t.Run(surface, func(t *testing.T) {
			curTools := cur[surface]

			// --- Name-set delta: exactly the expected additions, no removals.
			var added []string
			for n := range curTools {
				if _, ok := preTools[n]; !ok {
					added = append(added, n)
				}
			}
			sort.Strings(added)
			assert.Equal(t, wantAdded[surface], func() []string {
				if added == nil {
					return []string{}
				}
				return added
			}(), "surface %s: only describe_tool may be added (FR-014/SC-007)", surface)
			for n := range preTools {
				assert.Contains(t, curTools, n, "surface %s: pre-feature tool %q must not be removed or renamed (FR-014)", surface, n)
			}

			for _, name := range sortedNames(preTools) {
				name := name
				rawPre, rawCur := preTools[name], curTools[name]
				if rawCur == nil {
					continue // removal already reported above
				}
				preM, curM := asMap(t, rawPre), asMap(t, rawCur)

				switch {
				case name == "retrieve_tools":
					assertRetrieveToolsDelta(t, surface, preM, curM)
				case callToolVariants[name]:
					assertCallToolVariantDelta(t, surface, name, preM, curM)
				default:
					assert.Equal(t, preM, curM,
						"surface %s: tool %q must be byte-identical to the pre-feature snapshot (SC-003)", surface, name)
				}
			}
		})
	}
}

// assertRetrieveToolsDelta: params delta is exactly {+detail}; every
// pre-feature parameter is preserved unchanged; the description on the
// retrieve_tools-mode surfaces is updated per FR-014 to reference compact
// signatures + describe_tool.
func assertRetrieveToolsDelta(t *testing.T, surface string, preM, curM map[string]interface{}) {
	t.Helper()

	preProps, curProps := schemaProps(preM), schemaProps(curM)
	require.NotNil(t, curProps, "surface %s: retrieve_tools lost its inputSchema", surface)

	// Exactly one added param: detail (enum compact|full, optional — FR-005).
	for p := range curProps {
		if _, ok := preProps[p]; !ok {
			assert.Equal(t, "detail", p,
				"surface %s: the only added retrieve_tools parameter is 'detail' (SC-003)", surface)
		}
	}
	detail, ok := curProps["detail"].(map[string]interface{})
	require.True(t, ok, "surface %s: retrieve_tools must gain the 'detail' parameter (FR-005)", surface)
	assert.ElementsMatch(t, []interface{}{"compact", "full"}, detail["enum"],
		"surface %s: detail enum is {compact, full}", surface)

	// All pre-feature params preserved byte-equal.
	for p, preSchema := range preProps {
		assert.Equal(t, preSchema, curProps[p],
			"surface %s: pre-feature retrieve_tools parameter %q must be preserved unchanged (SC-003)", surface, p)
	}

	// 'detail' must stay optional: the required list is unchanged.
	preSchema, _ := preM["inputSchema"].(map[string]interface{})
	curSchema, _ := curM["inputSchema"].(map[string]interface{})
	assert.Equal(t, preSchema["required"], curSchema["required"],
		"surface %s: retrieve_tools required params unchanged (detail is optional)", surface)

	// Annotations unchanged.
	assert.Equal(t, preM["annotations"], curM["annotations"],
		"surface %s: retrieve_tools annotations unchanged", surface)

	preDesc, _ := preM["description"].(string)
	curDesc, _ := curM["description"].(string)
	if surface == "code_execution_mode" {
		// describe_tool is not exposed there (v1), so its description must not
		// point at it; the pre-feature text stays.
		assert.Equal(t, preDesc, curDesc,
			"surface %s: code_execution retrieve_tools description unchanged (describe_tool not exposed there in v1)", surface)
		return
	}
	// FR-014: updated description referencing signatures + describe_tool.
	assert.NotEqual(t, preDesc, curDesc,
		"surface %s: retrieve_tools description must be updated (FR-014)", surface)
	assert.Contains(t, curDesc, "describe_tool",
		"surface %s: retrieve_tools description must reference describe_tool (FR-014)", surface)
	assert.Contains(t, strings.ToLower(curDesc), "signature",
		"surface %s: retrieve_tools description must reference compact signatures (FR-014)", surface)
}

// assertCallToolVariantDelta: only the tool description and the 'args'
// parameter description may change (FR-014); the new text references
// signatures + describe_tool and no longer instructs reading inputSchema from
// retrieve_tools. Everything else is byte-equal.
func assertCallToolVariantDelta(t *testing.T, surface, name string, preM, curM map[string]interface{}) {
	t.Helper()

	preDesc, _ := preM["description"].(string)
	curDesc, _ := curM["description"].(string)
	preProps, curProps := schemaProps(preM), schemaProps(curM)
	require.NotNil(t, curProps, "surface %s: %s lost its inputSchema", surface, name)

	preArgs, _ := preProps["args"].(map[string]interface{})
	curArgs, _ := curProps["args"].(map[string]interface{})
	require.NotNil(t, preArgs, "golden %s has no args param", name)
	require.NotNil(t, curArgs, "surface %s: %s lost its args param", surface, name)
	preArgsDesc, _ := preArgs["description"].(string)
	curArgsDesc, _ := curArgs["description"].(string)

	// FR-014: the combined agent-facing text must now route schema needs
	// through signatures/describe_tool, not "inputSchema from retrieve_tools".
	combined := curDesc + " " + curArgsDesc
	assert.NotEqual(t, preDesc+" "+preArgsDesc, combined,
		"surface %s: %s descriptions must be updated (FR-014)", surface, name)
	assert.Contains(t, combined, "describe_tool",
		"surface %s: %s must reference describe_tool (FR-014)", surface, name)
	assert.Contains(t, strings.ToLower(combined), "sig",
		"surface %s: %s must reference the compact signature (FR-014)", surface, name)
	assert.NotContains(t, combined, "inputSchema from retrieve_tools",
		"surface %s: %s must no longer instruct reading inputSchema from retrieve_tools (FR-014)", surface, name)

	// Everything except the two description strings is byte-equal: normalize
	// the descriptions on deep copies, then compare whole maps.
	preNorm := asMap(t, mustRemarshal(t, preM))
	curNorm := asMap(t, mustRemarshal(t, curM))
	preNorm["description"], curNorm["description"] = "", ""
	schemaOf(preNorm)["args"].(map[string]interface{})["description"] = ""
	schemaOf(curNorm)["args"].(map[string]interface{})["description"] = ""
	assert.Equal(t, preNorm, curNorm,
		"surface %s: %s may differ from pre-feature ONLY in description texts (SC-003)", surface, name)
}

func schemaOf(tool map[string]interface{}) map[string]interface{} {
	return tool["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
}

func mustRemarshal(t *testing.T, m map[string]interface{}) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	return raw
}
