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

// Spec 094 FR-009 widens the controlled delta by exactly two things: the
// default registration gains the three annotation-filter parameters it never
// exposed (the discoverability gap the field report started from), and all
// three descriptions gain the diagnostics mention plus one fixed caveat
// sentence. Nothing else about the surface may move.

// spec094FilterParams are the annotation-filter parameters every
// retrieve_tools registration must now expose, from one shared helper.
var spec094FilterParams = []string{"exclude_destructive", "exclude_open_world", "read_only_only"}

// spec094WindowCaveat is the verbatim sentence every retrieve_tools
// description must carry: diagnostics describe ONE call's candidate window,
// not the whole catalog, so an agent never reads the counts as index-wide.
const spec094WindowCaveat = "Filter diagnostics describe this call's candidate window, not the whole catalog."

// addedRetrieveToolsParams is the exact allowed parameter delta per surface.
// code_execution keeps no additions: describe_tool is absent there in v1 (so
// no `detail`, spec 085 FR-011), and it already declared the filters.
var addedRetrieveToolsParams = map[string][]string{
	"default_server":      {"detail", "exclude_destructive", "exclude_open_world", "read_only_only"},
	"call_tool_mode":      {"detail"},
	"code_execution_mode": {},
}

// assertRetrieveToolsDelta: the parameter delta is exactly the surface's
// entry in addedRetrieveToolsParams, every pre-feature parameter is preserved
// unchanged, and the description carries the spec-094 diagnostics mention (all
// surfaces) plus the spec-085 signatures/describe_tool text (retrieve_tools
// surfaces only).
func assertRetrieveToolsDelta(t *testing.T, surface string, preM, curM map[string]interface{}) {
	t.Helper()

	preProps, curProps := schemaProps(preM), schemaProps(curM)
	require.NotNil(t, curProps, "surface %s: retrieve_tools lost its inputSchema", surface)

	var added []string
	for p := range curProps {
		if _, ok := preProps[p]; !ok {
			added = append(added, p)
		}
	}
	sort.Strings(added)
	if added == nil {
		added = []string{}
	}
	assert.Equal(t, addedRetrieveToolsParams[surface], added,
		"surface %s: exact retrieve_tools parameter delta (SC-003 / FR-009)", surface)

	// All pre-feature params preserved byte-equal — including the filter
	// params the routing surfaces already had, which must survive the move to
	// the shared helper untouched.
	for p, preSchema := range preProps {
		assert.Equal(t, preSchema, curProps[p],
			"surface %s: pre-feature retrieve_tools parameter %q must be preserved unchanged (SC-003)", surface, p)
	}

	// Added params stay optional: the required list is unchanged.
	preSchema, _ := preM["inputSchema"].(map[string]interface{})
	curSchema, _ := curM["inputSchema"].(map[string]interface{})
	assert.Equal(t, preSchema["required"], curSchema["required"],
		"surface %s: retrieve_tools required params unchanged", surface)

	// Annotations unchanged.
	assert.Equal(t, preM["annotations"], curM["annotations"],
		"surface %s: retrieve_tools annotations unchanged", surface)

	preDesc, _ := preM["description"].(string)
	curDesc, _ := curM["description"].(string)
	assert.NotEqual(t, preDesc, curDesc,
		"surface %s: retrieve_tools description must be updated", surface)
	assert.Contains(t, curDesc, "filter_diagnostics",
		"surface %s: retrieve_tools description must name the diagnostics block (FR-009)", surface)
	assert.Contains(t, curDesc, spec094WindowCaveat,
		"surface %s: retrieve_tools description must carry the candidate-window caveat verbatim (FR-009)", surface)

	if surface == "code_execution_mode" {
		// Spec 085 §Out-of-scope / FR-011: no describe_tool on this surface, so
		// no detail param and no compact-signature prose.
		assert.NotContains(t, curProps, "detail",
			"surface %s: code_execution retrieve_tools must not expose detail (spec 085 FR-011)", surface)
		return
	}

	detail, ok := curProps["detail"].(map[string]interface{})
	require.True(t, ok, "surface %s: retrieve_tools must carry the 'detail' parameter (spec 085 FR-005)", surface)
	assert.ElementsMatch(t, []interface{}{"compact", "full"}, detail["enum"],
		"surface %s: detail enum is {compact, full}", surface)

	// Spec 085 FR-014: description references signatures + describe_tool.
	assert.Contains(t, curDesc, "describe_tool",
		"surface %s: retrieve_tools description must reference describe_tool (spec 085 FR-014)", surface)
	assert.Contains(t, strings.ToLower(curDesc), "signature",
		"surface %s: retrieve_tools description must reference compact signatures (spec 085 FR-014)", surface)
}

// FR-009: the three registrations are built independently, which is exactly
// how the default one drifted into omitting the filter parameters. They must
// now come from one shared helper — asserted by deep-comparing the produced
// schemas rather than by trusting the call sites.
func TestMenuSurface_AnnotationFilterParamsShared(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	cur := currentSurface(t, proxy)

	surfaces := []string{"default_server", "call_tool_mode", "code_execution_mode"}
	var reference map[string]interface{}
	referenceSurface := ""

	for _, surface := range surfaces {
		raw, ok := cur[surface]["retrieve_tools"]
		require.True(t, ok, "surface %s: retrieve_tools must be registered", surface)
		tool := asMap(t, raw)
		props := schemaProps(tool)
		require.NotNil(t, props, "surface %s: retrieve_tools lost its inputSchema", surface)

		got := map[string]interface{}{}
		for _, name := range spec094FilterParams {
			schema, present := props[name]
			require.True(t, present,
				"surface %s: retrieve_tools must expose the %q filter (FR-009)", surface, name)
			got[name] = schema
		}
		if reference == nil {
			reference, referenceSurface = got, surface
			continue
		}
		assert.Equal(t, reference, got,
			"surface %s: annotation-filter schemas must be identical to %s — they come from one shared helper",
			surface, referenceSurface)

		desc, _ := tool["description"].(string)
		assert.Contains(t, desc, "filter_diagnostics",
			"surface %s: description must name the diagnostics block (FR-009)", surface)
		assert.Contains(t, desc, spec094WindowCaveat,
			"surface %s: description must carry the candidate-window caveat verbatim (FR-009)", surface)
	}
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
