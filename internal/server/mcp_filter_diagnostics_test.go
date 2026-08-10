package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
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
// view, not in the index.
func newFilterDiagnosticsProxy(t *testing.T) *MCPProxyServer {
	t.Helper()
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{
		{Name: "annot", Enabled: true},
		{Name: "plain", Enabled: true},
	})
	seedFilterDiagnosticsFixture(t, proxy, rt)
	return proxy
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

// diagModes maps a serialization mode to the `detail` argument that selects it.
var diagModes = map[string]string{"full": "full", "compact": "compact"}

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
				end := skipJSONValue(t, raw, i+len(needle))
				start := i
				switch {
				case end < len(raw) && raw[end] == ',':
					end++ // drop the separator that followed the member
				case start > 0 && raw[start-1] == ',':
					start-- // last member: drop the separator that preceded it
				}
				return raw[:start] + raw[end:]
			}
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	return raw
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
	proxy := newFilterDiagnosticsProxy(t)

	for _, cond := range spec094Conditions() {
		cond := cond
		for _, mode := range []string{"full", "compact"} {
			mode := mode
			t.Run(cond.name+"/"+mode, func(t *testing.T) {
				got := callRetrieveRaw(t, proxy, withDetail(cond.args, diagModes[mode]))

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
