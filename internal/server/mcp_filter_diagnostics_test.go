package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 094 — retrieve_tools filter diagnostics.
//
// The fixture below is shared by every test in this file. It deliberately
// spans all four attribution outcomes so a single query exercises both reason
// classes across two filters:
//
//	annot:safe_read   readOnly=true  destructive=false openWorld=false -> kept
//	annot:write_item  readOnly=false destructive=false openWorld=false -> read_only_only / explicit
//	annot:purge_item  readOnly=unset destructive=true  openWorld=false -> read_only_only / missing
//	annot:reach_web   readOnly=true  destructive=false openWorld=true  -> exclude_open_world / explicit
//	plain:mystery     no annotations at all (absent from the state view)  -> read_only_only / missing
//
// "widgetquery" matches all five; "safeonlyterm" matches only annot:safe_read,
// which passes every filter — the zero-omission condition.

const (
	diagQueryAll      = "widgetquery"
	diagQuerySafeOnly = "safeonlyterm"
)

// seedFilterDiagnosticsFixture indexes the corpus above and publishes the
// annotations into the runtime state view, which is where
// lookupToolAnnotations resolves them from.
func seedFilterDiagnosticsFixture(t *testing.T, proxy *MCPProxyServer, rt *runtime.Runtime) {
	t.Helper()

	for _, name := range []string{"annot", "plain"} {
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
			Name: name, Enabled: true,
		}))
	}

	indexed := []struct{ name, server, desc string }{
		{"annot:safe_read", "annot", diagQueryAll + " " + diagQuerySafeOnly + " listing"},
		{"annot:write_item", "annot", diagQueryAll + " write item"},
		{"annot:purge_item", "annot", diagQueryAll + " purge item"},
		{"annot:reach_web", "annot", diagQueryAll + " reach web"},
		{"plain:mystery_tool", "plain", diagQueryAll + " mystery tool"},
	}
	for _, tool := range indexed {
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name: tool.name, ServerName: tool.server,
			Description: tool.desc,
			ParamsJSON:  `{"type":"object"}`,
			Hash:        "hash-" + tool.name,
		}))
	}

	supervisor := rt.Supervisor()
	require.NotNil(t, supervisor, "runtime must expose a supervisor")
	supervisor.StateView().UpdateServer("annot", func(s *stateview.ServerStatus) {
		s.Name = "annot"
		s.Enabled = true
		s.Connected = true
		s.Tools = []stateview.ToolInfo{
			{Name: "safe_read", Annotations: &config.ToolAnnotations{
				ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
			}},
			{Name: "write_item", Annotations: &config.ToolAnnotations{
				ReadOnlyHint: boolPtr(false), DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
			}},
			{Name: "purge_item", Annotations: &config.ToolAnnotations{
				DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
			}},
			{Name: "reach_web", Annotations: &config.ToolAnnotations{
				ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true),
			}},
		}
	})
	// "plain" is deliberately absent from the state view: its tool resolves to
	// nil annotations, the field-report scenario.
}

// newFilterDiagnosticsProxy builds a runtime-backed proxy with the shared
// fixture seeded. A runtime is required because annotations live in the state
// view, not in the index; it is returned so individual tests can extend the
// fixture.
func newFilterDiagnosticsProxy(t *testing.T) (*MCPProxyServer, *runtime.Runtime) {
	t.Helper()
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{
		{Name: "annot", Enabled: true},
		{Name: "plain", Enabled: true},
	})
	seedFilterDiagnosticsFixture(t, proxy, rt)
	return proxy, rt
}

// addAnnotatedTool extends the fixture with one more annotated tool on the
// "annot" server: indexed, and appended to the state view so its annotations
// resolve.
func addAnnotatedTool(t *testing.T, proxy *MCPProxyServer, rt *runtime.Runtime, toolName, description string, annotations *config.ToolAnnotations) {
	t.Helper()
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "annot:" + toolName, ServerName: "annot",
		Description: description,
		ParamsJSON:  `{"type":"object"}`,
		Hash:        "hash-annot:" + toolName,
	}))
	rt.Supervisor().StateView().UpdateServer("annot", func(s *stateview.ServerStatus) {
		s.Tools = append(s.Tools, stateview.ToolInfo{Name: toolName, Annotations: annotations})
	})
}

// --- T003: frozen pre-feature response goldens (SC-002) ---
//
// Captured while the branch still had pre-feature behavior, these six files
// pin the EXACT bytes of the three response conditions in both serialization
// modes. Post-feature:
//
//	(a) no filters            -> byte-identical to the golden
//	(b) filters, no omissions -> byte-identical to the golden
//	(c) filters with omissions-> identical to the golden once the
//	    filter_diagnostics key alone is spliced back out
//
// Normal runs only read the committed fixtures. Regenerate deliberately with
// UPDATE_SPEC094_GOLDENS=1 go test ./internal/server -run TestSpec094Goldens

// diagCondition is one golden-backed response condition.
type diagCondition struct {
	name   string
	args   map[string]interface{}
	omits  bool // (c): the response is expected to carry filter_diagnostics post-feature
	golden string
}

func spec094Conditions() []diagCondition {
	return []diagCondition{
		{
			name:   "a_no_filters",
			args:   map[string]interface{}{"query": diagQueryAll, "limit": float64(20)},
			golden: "a_no_filters",
		},
		{
			name: "b_zero_omissions",
			args: map[string]interface{}{
				"query": diagQuerySafeOnly, "limit": float64(20),
				"read_only_only": true, "exclude_destructive": true, "exclude_open_world": true,
			},
			golden: "b_zero_omissions",
		},
		{
			name: "c_with_omissions",
			args: map[string]interface{}{
				"query": diagQueryAll, "limit": float64(20),
				"read_only_only": true, "exclude_open_world": true,
			},
			omits:  true,
			golden: "c_with_omissions",
		},
	}
}

// diagSerializationModes are the two `detail` values a response can be
// rendered under; the diagnostics block must be identical in both.
var diagSerializationModes = []string{"full", "compact"}

// withDetail copies args with an explicit `detail` override, so a table entry
// can be reused across serialization modes without mutation.
func withDetail(args map[string]interface{}, detail string) map[string]interface{} {
	out := make(map[string]interface{}, len(args)+1)
	for k, v := range args {
		out[k] = v
	}
	out["detail"] = detail
	return out
}

func spec094GoldenPath(condition, mode string) string {
	return filepath.Join("testdata", "spec094", condition+"_"+mode+".golden.json")
}

// compareSpec094Golden is compareGolden with the spec-094 update guard.
func compareSpec094Golden(t *testing.T, goldenPath, got string) {
	t.Helper()
	if os.Getenv("UPDATE_SPEC094_GOLDENS") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, []byte(got+"\n"), 0o644))
		t.Logf("golden updated: %s", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden missing — run with UPDATE_SPEC094_GOLDENS=1 to capture")
	assert.Equal(t,
		strings.TrimRight(string(want), "\n"),
		strings.TrimRight(got, "\n"),
		"response must be byte-identical to the frozen pre-feature capture (SC-002)")
}

// stripTopLevelJSONKey removes one top-level `"key": <value>` member from a
// JSON object, byte-surgically — no decode/re-encode round trip, so what
// remains is the untouched original bytes. Returns the input unchanged when
// the key is absent (the pre-feature case).
func stripTopLevelJSONKey(t *testing.T, raw, key string) string {
	t.Helper()

	start, _, end, ok := findTopLevelJSONMember(t, raw, key)
	if !ok {
		return raw
	}
	switch {
	case end < len(raw) && raw[end] == ',':
		end++ // drop the separator that followed the member
	case start > 0 && raw[start-1] == ',':
		start-- // last member: drop the separator that preceded it
	}
	return raw[:start] + raw[end:]
}

// extractTopLevelJSONValue returns the raw bytes of one top-level member's
// value, unparsed, so two responses can be compared on that member alone.
func extractTopLevelJSONValue(t *testing.T, raw, key string) (string, bool) {
	t.Helper()
	_, valStart, valEnd, ok := findTopLevelJSONMember(t, raw, key)
	if !ok {
		return "", false
	}
	return raw[valStart:valEnd], true
}

// findTopLevelJSONMember locates `"key": <value>` at depth 1 of a JSON object
// and returns the offsets of the key's opening quote, the value's first byte,
// and one past the value's last byte.
func findTopLevelJSONMember(t *testing.T, raw, key string) (keyStart, valStart, valEnd int, ok bool) {
	t.Helper()

	needle := `"` + key + `":`
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			if depth == 1 && strings.HasPrefix(raw[i:], needle) {
				start := i + len(needle)
				return i, start, skipJSONValue(t, raw, start), true
			}
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	return 0, 0, 0, false
}

// skipJSONValue returns the index just past the JSON value starting at pos.
func skipJSONValue(t *testing.T, raw string, pos int) int {
	t.Helper()
	depth := 0
	inString := false
	escaped := false
	for i := pos; i < len(raw); i++ {
		c := raw[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
				if depth == 0 {
					return i + 1
				}
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return i // a scalar value ended at the parent's closing brace
			}
			depth--
			if depth == 0 {
				return i + 1
			}
		case ',':
			if depth == 0 {
				return i
			}
		}
	}
	t.Fatalf("unterminated JSON value at offset %d", pos)
	return 0
}

func TestSpec094Goldens(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)

	for _, cond := range spec094Conditions() {
		cond := cond
		for _, mode := range diagSerializationModes {
			mode := mode
			t.Run(cond.name+"/"+mode, func(t *testing.T) {
				got := callRetrieveRaw(t, proxy, withDetail(cond.args, mode))

				if cond.omits {
					// (c): everything OUTSIDE the diagnostics block is frozen.
					compareSpec094Golden(t, spec094GoldenPath(cond.golden, mode),
						stripTopLevelJSONKey(t, got, "filter_diagnostics"))
					return
				}
				compareSpec094Golden(t, spec094GoldenPath(cond.golden, mode), got)
			})
		}
	}
}

// The stripping helper is load-bearing for the SC-002 (c) assertion, so it
// gets its own coverage: a no-op on the pre-feature shape, exact removal of a
// nested object member, and immunity to braces inside string values.
func TestStripTopLevelJSONKey(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"absent", `{"a":1,"b":2}`, `{"a":1,"b":2}`},
		{"middle member", `{"a":1,"filter_diagnostics":{"x":1},"b":2}`, `{"a":1,"b":2}`},
		{"last member", `{"a":1,"filter_diagnostics":{"x":1}}`, `{"a":1}`},
		{"only member", `{"filter_diagnostics":{"x":1}}`, `{}`},
		{"nested braces in strings", `{"a":"{not:a}brace","filter_diagnostics":{"s":"a,b}"},"b":2}`, `{"a":"{not:a}brace","b":2}`},
		{"same key nested deeper is untouched", `{"a":{"filter_diagnostics":1},"b":2}`, `{"a":{"filter_diagnostics":1},"b":2}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, stripTopLevelJSONKey(t, tc.in, "filter_diagnostics"))
		})
	}
}

// --- T004 [US1]: presence/absence + normative shape (FR-001, FR-003) ---

// diagReasonCounts / diagBlock decode the response block independently of the
// production structs, so a rename or a lost JSON tag shows up as a test
// failure rather than being silently absorbed.
type diagReasonCounts struct {
	MissingAnnotation int `json:"missing_annotation"`
	Explicit          int `json:"explicit"`
}

type diagBlock struct {
	MatchedBeforeFilters int                         `json:"matched_before_filters"`
	OmittedTotal         int                         `json:"omitted_total"`
	OmittedByFilter      map[string]diagReasonCounts `json:"omitted_by_filter"`
	Suggestion           string                      `json:"suggestion"`
}

// diagResponse is the subset of the response the diagnostics tests read.
type diagResponse struct {
	Tools             []map[string]interface{} `json:"tools"`
	Total             int                      `json:"total"`
	Notice            string                   `json:"notice"`
	Disabled          []map[string]interface{} `json:"disabled"`
	Remediation       map[string]string        `json:"remediation"`
	FilterDiagnostics *diagBlock               `json:"filter_diagnostics"`
}

func decodeDiagResponse(t *testing.T, raw string) diagResponse {
	t.Helper()
	var resp diagResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &resp))
	return resp
}

// requireDiag decodes the response and asserts the block is present.
func requireDiag(t *testing.T, raw string) *diagBlock {
	t.Helper()
	resp := decodeDiagResponse(t, raw)
	require.NotNil(t, resp.FilterDiagnostics, "filter_diagnostics must be present (FR-001)")
	return resp.FilterDiagnostics
}

// FR-001: the block is absent whenever no filter is active, and whenever
// active filters omitted nothing — in BOTH serialization modes.
func TestFilterDiagnostics_AbsentOnHappyPath(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)

	happy := []struct {
		name string
		args map[string]interface{}
	}{
		{"no filters at all", map[string]interface{}{"query": diagQueryAll, "limit": float64(20)}},
		{"filters active, zero omissions", map[string]interface{}{
			"query": diagQuerySafeOnly, "limit": float64(20),
			"read_only_only": true, "exclude_destructive": true, "exclude_open_world": true,
		}},
	}
	for _, hc := range happy {
		hc := hc
		for _, mode := range diagSerializationModes {
			mode := mode
			t.Run(hc.name+"/"+mode, func(t *testing.T) {
				raw := callRetrieveRaw(t, proxy, withDetail(hc.args, mode))
				assert.NotContains(t, raw, "filter_diagnostics",
					"no key, no whitespace, nothing: the response must be byte-identical to pre-feature (FR-001)")
				require.NotEmpty(t, decodeDiagResponse(t, raw).Tools,
					"fixture sanity: the happy-path query must return tools")
			})
		}
	}
}

// FR-001 + FR-007: present when filters omitted something, with content
// identical in full and compact mode.
func TestFilterDiagnostics_PresentAndModeIdentical(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)
	args := map[string]interface{}{
		"query": diagQueryAll, "limit": float64(20),
		"read_only_only": true, "exclude_open_world": true,
	}

	blocks := map[string]string{}
	for _, mode := range diagSerializationModes {
		raw := callRetrieveRaw(t, proxy, withDetail(args, mode))
		block, ok := extractTopLevelJSONValue(t, raw, "filter_diagnostics")
		require.True(t, ok, "mode %s: filter_diagnostics must be present (FR-001)", mode)
		blocks[mode] = block
	}
	assert.Equal(t, blocks["full"], blocks["compact"],
		"the diagnostics block must be byte-identical across serialization modes (FR-007)")
}

// FR-003: the exact serialized shape — alphabetical map ordering (the
// encoder's native behavior), both reason fields always emitted (no
// omitempty), field order fixed by the struct.
func TestFilterDiagnostics_NormativeJSONShape(t *testing.T) {
	diag := filterDiagnostics{
		MatchedBeforeFilters: 12,
		OmittedTotal:         8,
		OmittedByFilter: map[string]reasonCounts{
			"read_only_only":      {MissingAnnotation: 5, Explicit: 1},
			"exclude_open_world":  {MissingAnnotation: 0, Explicit: 1},
			"exclude_destructive": {MissingAnnotation: 1, Explicit: 0},
		},
		Suggestion: "one string",
	}

	raw, err := json.Marshal(diag)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"matched_before_filters": 12,
		"omitted_total": 8,
		"omitted_by_filter": {
			"exclude_destructive": {"missing_annotation": 1, "explicit": 0},
			"exclude_open_world":  {"missing_annotation": 0, "explicit": 1},
			"read_only_only":      {"missing_annotation": 5, "explicit": 1}
		},
		"suggestion": "one string"
	}`, string(raw))
	assert.Equal(t,
		`{"matched_before_filters":12,"omitted_total":8,"omitted_by_filter":`+
			`{"exclude_destructive":{"missing_annotation":1,"explicit":0},`+
			`"exclude_open_world":{"missing_annotation":0,"explicit":1},`+
			`"read_only_only":{"missing_annotation":5,"explicit":1}},`+
			`"suggestion":"one string"}`,
		string(raw),
		"serialized bytes must match the FR-003 normative shape exactly")
}

// FR-003: an ACTIVE filter that omitted nothing must not appear in the map.
// exclude_destructive is active here but can never fire: read_only_only
// evaluates first and claims every non-read-only tool, and the read-only ones
// pass the destructive filter through the frozen shortcut.
func TestFilterDiagnostics_ZeroCountActiveFilterAbsent(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)

	raw := callRetrieveRaw(t, proxy, map[string]interface{}{
		"query": diagQueryAll, "limit": float64(20),
		"read_only_only": true, "exclude_destructive": true, "exclude_open_world": true,
	})
	diag := requireDiag(t, raw)

	assert.NotContains(t, diag.OmittedByFilter, "exclude_destructive",
		"an active filter with zero omissions must be absent from omitted_by_filter (FR-003)")
	assert.Contains(t, diag.OmittedByFilter, "read_only_only")
	assert.Contains(t, diag.OmittedByFilter, "exclude_open_world")
	assert.NotContains(t, diag.Suggestion, "exclude_destructive",
		"the suggestion must name only the filters that actually omitted tools (FR-006)")
}

// FR-003 arithmetic: omitted_total is the sum of every reason count, and
// matched_before_filters is omitted_total plus the response's own total.
func TestFilterDiagnostics_CountInvariants(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)

	for _, mode := range diagSerializationModes {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			raw := callRetrieveRaw(t, proxy, map[string]interface{}{
				"query": diagQueryAll, "limit": float64(20), "detail": mode,
				"read_only_only": true, "exclude_open_world": true,
			})
			resp := decodeDiagResponse(t, raw)
			require.NotNil(t, resp.FilterDiagnostics)
			diag := resp.FilterDiagnostics

			sum := 0
			for _, counts := range diag.OmittedByFilter {
				sum += counts.MissingAnnotation + counts.Explicit
			}
			assert.Equal(t, diag.OmittedTotal, sum, "omitted_total must equal the sum of all reason counts")
			assert.Equal(t, diag.MatchedBeforeFilters, diag.OmittedTotal+resp.Total,
				"matched_before_filters must equal omitted_total + the response total")

			// The fixture is fully determined: 5 candidates, 1 survivor.
			assert.Equal(t, 5, diag.MatchedBeforeFilters)
			assert.Equal(t, 4, diag.OmittedTotal)
			assert.Equal(t, 1, resp.Total)
			assert.Len(t, resp.Tools, resp.Total)
		})
	}
}

// --- T005 [US1]: candidate-window semantics (FR-002, spec Edge Cases) ---

// The window is what the SEARCH layer returned for this call, after
// normalization — not the caller's raw limit. internal/index/manager.go:109
// turns any non-positive limit into 20, so a caller passing 0 or a negative
// value still gets (and is told about) a full window. An implementation that
// derived matched_before_filters from the caller's argument would report 0
// (or a negative number) here.
func TestFilterDiagnostics_WindowUsesNormalizedLimit(t *testing.T) {
	for _, limit := range []float64{0, -5} {
		limit := limit
		t.Run(fmt.Sprintf("limit=%v", limit), func(t *testing.T) {
			proxy, _ := newFilterDiagnosticsProxy(t)

			raw := callRetrieveRaw(t, proxy, map[string]interface{}{
				"query": diagQueryAll, "limit": limit, "read_only_only": true,
			})
			diag := requireDiag(t, raw)

			assert.Equal(t, 5, diag.MatchedBeforeFilters,
				"matched_before_filters is the post-normalization window (20), not the caller's %v", limit)
			// read_only_only alone keeps the two explicitly read-only tools.
			assert.Equal(t, 3, diag.OmittedTotal)
		})
	}
}

// Hits removed by the visibility step never enter the window, and nothing is
// pulled up to replace them (no backfill). A user-disabled tool that matches
// the query must therefore leave matched_before_filters untouched — it belongs
// to the existing locked-tools flow, not to these diagnostics.
func TestFilterDiagnostics_NoBackfillForVisibilityDrops(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)

	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "annot", ToolName: "locked_item",
		Status: storage.ToolApprovalStatusApproved, Disabled: true,
	}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "annot:locked_item", ServerName: "annot",
		Description: diagQueryAll + " locked item",
		ParamsJSON:  `{"type":"object"}`,
		Hash:        "hash-annot:locked_item",
	}))

	raw := callRetrieveRaw(t, proxy, map[string]interface{}{
		"query": diagQueryAll, "limit": float64(20), "read_only_only": true,
	})
	resp := decodeDiagResponse(t, raw)
	require.NotNil(t, resp.FilterDiagnostics)

	assert.Equal(t, 5, resp.FilterDiagnostics.MatchedBeforeFilters,
		"the locked hit is dropped before annotation filtering and must not be counted")
	assert.Equal(t, 3, resp.FilterDiagnostics.OmittedTotal)
	assert.Equal(t, 2, resp.Total)
}

// FR-004 / Edge Cases: a tool whose annotations cannot be resolved is treated
// as unannotated by the filter, so its omission is a missing_annotation — the
// exact field-report signal that tells the operator to fix upstream metadata.
func TestFilterDiagnostics_UnresolvableAnnotationsAreMissing(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)

	// "plain" is absent from the state view, so lookupToolAnnotations returns
	// nil for its tool. Query only that tool.
	require.Nil(t, proxy.lookupToolAnnotations("plain", "mystery_tool"),
		"fixture sanity: the plain server's annotations must be unresolvable")

	raw := callRetrieveRaw(t, proxy, map[string]interface{}{
		"query": "mystery", "limit": float64(20), "read_only_only": true,
	})
	resp := decodeDiagResponse(t, raw)
	require.NotNil(t, resp.FilterDiagnostics)
	diag := resp.FilterDiagnostics

	assert.Equal(t, 1, diag.MatchedBeforeFilters)
	assert.Equal(t, 1, diag.OmittedTotal)
	assert.Equal(t, diagReasonCounts{MissingAnnotation: 1, Explicit: 0},
		diag.OmittedByFilter["read_only_only"],
		"an unresolvable annotation lookup classifies as missing_annotation, never explicit")
	assert.Equal(t, 0, resp.Total)
}

// --- T006 [US2]: reason split + suggestion selection (FR-004, FR-006) ---

// allFilterKeys is every filter that can appear in omitted_by_filter, in the
// alphabetical order the encoder emits and the suggestion joins.
var allFilterKeys = []string{"exclude_destructive", "exclude_open_world", "read_only_only"}

// Mixed causes split per filter: read_only_only claims one explicitly
// non-read-only tool and two unannotated ones, while exclude_open_world claims
// the explicitly open-world tool that survived the first filter.
func TestFilterDiagnostics_ReasonSplitPerFilter(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)

	raw := callRetrieveRaw(t, proxy, map[string]interface{}{
		"query": diagQueryAll, "limit": float64(20),
		"read_only_only": true, "exclude_open_world": true,
	})
	diag := requireDiag(t, raw)

	assert.Equal(t, map[string]diagReasonCounts{
		"read_only_only":     {MissingAnnotation: 2, Explicit: 1},
		"exclude_open_world": {MissingAnnotation: 0, Explicit: 1},
	}, diag.OmittedByFilter, "each omission lands under its first failing filter, split by cause")
}

// The frozen read-only shortcut must be visible in the counts: a tool with
// explicit readOnlyHint=true passes exclude_destructive even when
// destructiveHint=true, so it can never be attributed to that filter.
// Diagnostics describe what the filter did, not a simplified model of it.
func TestFilterDiagnostics_ReadOnlyShortcutNeverAttributedToDestructive(t *testing.T) {
	proxy, rt := newFilterDiagnosticsProxy(t)
	addAnnotatedTool(t, proxy, rt, "ro_destructive", diagQueryAll+" read only destructive",
		&config.ToolAnnotations{ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(true)})

	raw := callRetrieveRaw(t, proxy, map[string]interface{}{
		"query": diagQueryAll, "limit": float64(20), "exclude_destructive": true,
	})
	resp := decodeDiagResponse(t, raw)
	require.NotNil(t, resp.FilterDiagnostics)
	diag := resp.FilterDiagnostics

	assert.Equal(t, 6, diag.MatchedBeforeFilters)
	assert.Equal(t, 2, diag.OmittedTotal)
	assert.Equal(t, map[string]diagReasonCounts{
		"exclude_destructive": {MissingAnnotation: 1, Explicit: 1},
	}, diag.OmittedByFilter)

	names := map[string]bool{}
	for _, entry := range resp.Tools {
		names[entry["name"].(string)] = true
	}
	assert.True(t, names["annot:ro_destructive"],
		"explicit readOnlyHint=true passes exclude_destructive (frozen shortcut) — it must be RETURNED, not counted")
}

// FR-006 precedence: one missing_annotation anywhere selects the
// fix-your-annotations advice; only when every omission is explicit does the
// suggestion say the filter is working as intended.
func TestFilterDiagnostics_SuggestionPrecedence(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)

	t.Run("any missing annotation", func(t *testing.T) {
		raw := callRetrieveRaw(t, proxy, map[string]interface{}{
			"query": diagQueryAll, "limit": float64(20), "read_only_only": true,
		})
		diag := requireDiag(t, raw)
		require.NotZero(t, diag.OmittedByFilter["read_only_only"].MissingAnnotation)
		assert.Equal(t, filterSuggestion([]string{"read_only_only"}, true), diag.Suggestion)
		assert.Contains(t, strings.ToLower(diag.Suggestion), "annotations")
	})

	t.Run("all explicit", func(t *testing.T) {
		// "write" matches only annot:write_item, whose readOnlyHint is an
		// explicit false — a filter working exactly as intended.
		raw := callRetrieveRaw(t, proxy, map[string]interface{}{
			"query": "write", "limit": float64(20), "read_only_only": true,
		})
		diag := requireDiag(t, raw)
		require.Equal(t, diagReasonCounts{MissingAnnotation: 0, Explicit: 1}, diag.OmittedByFilter["read_only_only"])
		assert.Equal(t, filterSuggestion([]string{"read_only_only"}, false), diag.Suggestion)
		assert.NotContains(t, strings.ToLower(diag.Suggestion), "publish annotations",
			"an all-explicit omission must NOT tell the operator to fix upstream metadata (US2 AS2)")
	})
}

// FR-006 / SC-003: every reachable suggestion — all 7 non-empty filter subsets
// against both templates — stays within 200 characters and the JSON-safe
// character set, names every responsible filter literally, and names nothing
// else.
func TestFilterSuggestion_AllSubsetsConform(t *testing.T) {
	safe := regexp.MustCompile(`^[a-zA-Z0-9 .,:;()'_-]+$`)

	subsets := 0
	for mask := 1; mask < 1<<len(allFilterKeys); mask++ {
		var responsible []string
		for i, key := range allFilterKeys {
			if mask&(1<<i) != 0 {
				responsible = append(responsible, key)
			}
		}
		subsets++

		for _, anyMissing := range []bool{true, false} {
			anyMissing := anyMissing
			responsible := responsible
			t.Run(fmt.Sprintf("%s/missing=%t", strings.Join(responsible, "+"), anyMissing), func(t *testing.T) {
				got := filterSuggestion(responsible, anyMissing)

				assert.LessOrEqual(t, len(got), 200, "suggestion must stay under the 200-character bound")
				assert.Regexp(t, safe, got, "suggestion must use the JSON-safe ASCII subset (FR-006)")

				named := map[string]bool{}
				for _, key := range responsible {
					assert.Contains(t, got, key, "every responsible filter must be named literally")
					named[key] = true
				}
				for _, key := range allFilterKeys {
					if !named[key] {
						assert.NotContains(t, got, key, "an unrelated filter must never be named")
					}
				}
				for _, key := range responsible {
					assert.Equal(t, 1, strings.Count(got, key), "each filter is named exactly once")
				}
			})
		}
	}
	assert.Equal(t, 7, subsets, "all 7 non-empty subsets of the three filters")
}

// --- T007 [US3]: zero results, coexistence with the locked-tool flow, size ---

// seedLockedTool adds a user-disabled tool to the "annot" server. It is
// dropped by the visibility step, so it belongs to the locked-tool flow
// (notice / include_disabled) and never to the annotation diagnostics.
func seedLockedTool(t *testing.T, proxy *MCPProxyServer, toolName, description string) {
	t.Helper()
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "annot", ToolName: toolName,
		Status: storage.ToolApprovalStatusApproved, Disabled: true,
	}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "annot:" + toolName, ServerName: "annot",
		Description: description,
		ParamsJSON:  `{"type":"object"}`,
		Hash:        "hash-annot:" + toolName,
	}))
}

// US3 AS1: when the filters take everything, zero results stop reading as an
// outage — matched_before_filters accounts for every candidate.
func TestFilterDiagnostics_EveryMatchOmitted(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)

	// "item" matches write_item (explicit) and purge_item (missing) only.
	raw := callRetrieveRaw(t, proxy, map[string]interface{}{
		"query": "item", "limit": float64(20), "read_only_only": true,
	})
	resp := decodeDiagResponse(t, raw)
	require.NotNil(t, resp.FilterDiagnostics)

	assert.Equal(t, 0, resp.Total)
	assert.Empty(t, resp.Tools)
	assert.Equal(t, resp.FilterDiagnostics.OmittedTotal, resp.FilterDiagnostics.MatchedBeforeFilters,
		"every candidate was withheld, so the two counts coincide (US3 AS1)")
	assert.Equal(t, 2, resp.FilterDiagnostics.OmittedTotal)
	assert.Empty(t, resp.Notice, "no locked tools in this scenario, so no locked-tool notice")
}

// A query whose only hits are locked produces the existing notice and NO
// diagnostics: locked tools are dropped before annotation filtering, so the
// annotation filters omitted nothing.
func TestFilterDiagnostics_LockedOnlyHitsProduceNoDiagnostics(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)
	seedLockedTool(t, proxy, "locked_item", "lockedonlyterm locked item")

	for _, mode := range diagSerializationModes {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			raw := callRetrieveRaw(t, proxy, map[string]interface{}{
				"query": "lockedonlyterm", "limit": float64(20), "detail": mode,
				"read_only_only": true,
			})
			resp := decodeDiagResponse(t, raw)

			assert.NotContains(t, raw, "filter_diagnostics",
				"the annotation filters omitted nothing — the block must be absent (FR-001)")
			assert.Equal(t, 0, resp.Total)
			assert.Contains(t, resp.Notice, "locked", "the existing locked-tool notice is unchanged (US3 AS2)")
		})
	}
}

// Both signals at once: a locked hit and callable hits that the filters
// withhold. The two mechanisms are additive and must agree — the locked tool
// is counted by the notice only, the filtered ones by the diagnostics only.
func TestFilterDiagnostics_CoexistsWithLockedToolFlow(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)
	seedLockedTool(t, proxy, "locked_item", diagQueryAll+" locked item")

	base := map[string]interface{}{
		"query": "item", "limit": float64(20), "read_only_only": true,
	}

	t.Run("notice and diagnostics coexist", func(t *testing.T) {
		resp := decodeDiagResponse(t, callRetrieveRaw(t, proxy, base))
		require.NotNil(t, resp.FilterDiagnostics)

		assert.Equal(t, 0, resp.Total)
		assert.Contains(t, resp.Notice, "1 relevant tool(s) exist but are locked",
			"the locked hit is reported by the notice, exactly once")
		assert.Equal(t, 2, resp.FilterDiagnostics.MatchedBeforeFilters,
			"the locked hit never enters the annotation candidate window")
		assert.Equal(t, 2, resp.FilterDiagnostics.OmittedTotal)
	})

	t.Run("include_disabled coexists too", func(t *testing.T) {
		args := withDetail(base, "full")
		args["include_disabled"] = true
		resp := decodeDiagResponse(t, callRetrieveRaw(t, proxy, args))
		require.NotNil(t, resp.FilterDiagnostics)

		assert.NotEmpty(t, resp.Disabled, "the locked entry is still surfaced by include_disabled")
		assert.NotEmpty(t, resp.Remediation, "and so is its remediation map")
		assert.Empty(t, resp.Notice, "the nudge yields to the explicit opt-in, as before")
		assert.Equal(t, 2, resp.FilterDiagnostics.MatchedBeforeFilters,
			"opting into locked tools does not change the annotation candidate window")
		assert.Equal(t, 2, resp.FilterDiagnostics.OmittedTotal)
	})
}

// SC-003: the worst reachable block — every count maximal, all three filters
// present, a 200-character suggestion — must serialize under 500 bytes. The
// bound is exact arithmetic over a fixed shape, which is only sound while the
// real templates stay inside the JSON-safe character set (one byte per
// character in every mainstream encoder), asserted alongside.
func TestFilterDiagnostics_MaximalBlockSize(t *testing.T) {
	maximal := filterDiagnostics{
		MatchedBeforeFilters: 100,
		OmittedTotal:         100,
		OmittedByFilter: map[string]reasonCounts{
			"read_only_only":      {MissingAnnotation: 98, Explicit: 0},
			"exclude_destructive": {MissingAnnotation: 1, Explicit: 0},
			"exclude_open_world":  {MissingAnnotation: 1, Explicit: 0},
		},
		Suggestion: strings.Repeat("a", 200),
	}

	raw, err := json.Marshal(maximal)
	require.NoError(t, err)
	t.Logf("maximal serialized block: %d bytes", len(raw))
	assert.LessOrEqual(t, len(raw), 500, "the maximal reachable diagnostics block must fit in 500 bytes (SC-003)")

	// Sum invariant holds for the fixture the bound is derived from.
	sum := 0
	for _, counts := range maximal.OmittedByFilter {
		sum += counts.MissingAnnotation + counts.Explicit
	}
	assert.Equal(t, maximal.OmittedTotal, sum)

	safe := regexp.MustCompile(`^[a-zA-Z0-9 .,:;()'_-]+$`)
	for name, constant := range map[string]string{
		"suggestMissingPrefix":  suggestMissingPrefix,
		"suggestMissingSuffix":  suggestMissingSuffix,
		"suggestExplicitPrefix": suggestExplicitPrefix,
		"suggestExplicitSuffix": suggestExplicitSuffix,
	} {
		assert.Regexp(t, safe, constant,
			"%s must stay in the JSON-safe subset — no quotes, backslashes or HTML-significant characters (FR-006)", name)
	}
}

// --- T008: cross-surface parity (FR-007, FR-010) ---

// callRetrieveRawForMode invokes retrieve_tools through a routing-mode handler
// and returns the raw serialized response.
func callRetrieveRawForMode(t *testing.T, proxy *MCPProxyServer, routingMode string, args map[string]interface{}) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := proxy.handleRetrieveToolsForMode(routingMode)(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError, "retrieve_tools returned an error result")
	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected text content")
	return text.Text
}

// All three surfaces share one handler, so the diagnostics reach every one of
// them — including the code-execution surface, which always serializes full
// and has no `detail` parameter (FR-010).
func TestFilterDiagnostics_AllSurfacesCarryTheSameBlock(t *testing.T) {
	proxy, _ := newFilterDiagnosticsProxy(t)
	args := map[string]interface{}{
		"query": diagQueryAll, "limit": float64(20),
		"read_only_only": true, "exclude_open_world": true,
	}

	blockOf := func(t *testing.T, raw string) string {
		t.Helper()
		block, ok := extractTopLevelJSONValue(t, raw, "filter_diagnostics")
		require.True(t, ok, "filter_diagnostics must be present on this surface")
		return block
	}

	defaultBlock := blockOf(t, callRetrieveRaw(t, proxy, args))

	t.Run("code execution surface", func(t *testing.T) {
		raw := callRetrieveRawForMode(t, proxy, config.RoutingModeCodeExecution, args)
		assert.NotContains(t, raw, `"hint"`, "fixture sanity: this surface is always full-mode")
		assert.Equal(t, defaultBlock, blockOf(t, raw),
			"the code-execution surface must carry the identical block (FR-010)")
	})

	t.Run("call-tool surface, full vs compact", func(t *testing.T) {
		full := blockOf(t, callRetrieveRawForMode(t, proxy, config.RoutingModeRetrieveTools, withDetail(args, "full")))
		compact := blockOf(t, callRetrieveRawForMode(t, proxy, config.RoutingModeRetrieveTools, withDetail(args, "compact")))
		assert.Equal(t, full, compact, "the block is mode-independent on the call-tool surface (FR-007)")
		assert.Equal(t, defaultBlock, full, "and identical to the default surface's block")
	})
}
