package bench

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// retrieveToolsResponseFixture builds a compact JSON body shaped exactly like
// the retrieve_tools response marshaled by internal/server/mcp.go
// handleRetrieveToolsWithMode: a top-level object with "tools" (each entry
// carrying name/description/inputSchema/score/server/call_with/annotations),
// "query", "total", "usage_instructions", and "session_risk". Built with
// json.Marshal over maps so key ordering (sorted) matches the server's output.
func retrieveToolsResponseFixture(t *testing.T, nTools int) string {
	t.Helper()
	tools := make([]map[string]interface{}, 0, nTools)
	for i := 0; i < nTools; i++ {
		tools = append(tools, map[string]interface{}{
			"name": fmt.Sprintf("do_thing_%d", i),
			"description": fmt.Sprintf(
				"Creates thing %d in the remote system — supports labels, assignees, milestones. Ищет инструменты 🚀", i),
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title":  map[string]interface{}{"type": "string", "description": "The title of the thing"},
					"body":   map[string]interface{}{"type": "string", "description": "Long-form markdown body of the thing"},
					"labels": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				},
				"required": []string{"title"},
			},
			"score":       3.5 - float64(i)*0.25,
			"server":      fmt.Sprintf("srv%d", i%3),
			"call_with":   "call_tool_read",
			"annotations": map[string]interface{}{"readOnlyHint": true},
		})
	}
	resp := map[string]interface{}{
		"tools":              tools,
		"query":              "create github issue",
		"total":              nTools,
		"usage_instructions": "TOOL SELECTION GUIDE: Check the 'call_with' field for each tool, then use the matching tool variant.",
		"session_risk":       map[string]interface{}{"level": "low", "lethal_trifecta": false},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(data)
}

// componentText concatenates the raw bytes of every span carrying label.
func componentText(raw string, spans []Span, label string) string {
	var b strings.Builder
	for _, s := range spans {
		if s.Label == label {
			b.WriteString(raw[s.Start:s.End])
		}
	}
	return b.String()
}

func TestPartitionRetrieveToolsResponse_ExactPartition(t *testing.T) {
	raw := retrieveToolsResponseFixture(t, 4)
	spans, count, err := PartitionRetrieveToolsResponse(raw)
	if err != nil {
		t.Fatalf("PartitionRetrieveToolsResponse: %v", err)
	}
	if count != 4 {
		t.Errorf("result count = %d, want 4", count)
	}
	if len(spans) == 0 {
		t.Fatal("no spans produced")
	}

	// Spans must be a contiguous partition of [0, len(raw)) — no gaps, no
	// overlaps — otherwise the sum==total invariant cannot hold by construction.
	if spans[0].Start != 0 {
		t.Errorf("first span starts at %d, want 0", spans[0].Start)
	}
	if spans[len(spans)-1].End != len(raw) {
		t.Errorf("last span ends at %d, want %d", spans[len(spans)-1].End, len(raw))
	}
	valid := map[string]bool{
		ComponentInputSchemas:      true,
		ComponentDescriptions:      true,
		ComponentUsageInstructions: true,
		ComponentMetadata:          true,
		ComponentOther:             true,
	}
	for i, s := range spans {
		if s.Start >= s.End {
			t.Errorf("span %d is empty or inverted: %+v", i, s)
		}
		if i > 0 && s.Start != spans[i-1].End {
			t.Errorf("span %d not contiguous: prev end %d, start %d", i, spans[i-1].End, s.Start)
		}
		if !valid[s.Label] {
			t.Errorf("span %d has unknown label %q", i, s.Label)
		}
	}
}

func TestPartitionRetrieveToolsResponse_BucketsHoldTheRightBytes(t *testing.T) {
	raw := retrieveToolsResponseFixture(t, 2)
	spans, _, err := PartitionRetrieveToolsResponse(raw)
	if err != nil {
		t.Fatalf("PartitionRetrieveToolsResponse: %v", err)
	}

	desc := componentText(raw, spans, ComponentDescriptions)
	schemas := componentText(raw, spans, ComponentInputSchemas)
	usage := componentText(raw, spans, ComponentUsageInstructions)
	meta := componentText(raw, spans, ComponentMetadata)
	other := componentText(raw, spans, ComponentOther)

	// descriptions: the tool-level description pairs, nothing else.
	if !strings.Contains(desc, "Creates thing 0") || !strings.Contains(desc, "Creates thing 1") {
		t.Errorf("descriptions bucket missing tool descriptions: %q", desc)
	}
	if strings.Contains(desc, "inputSchema") {
		t.Errorf("descriptions bucket leaked schema bytes: %q", desc)
	}

	// input_schemas: the full inputSchema pair, including nested property
	// descriptions (they are schema cost, not tool-description cost).
	for _, want := range []string{`"inputSchema"`, `"properties"`, "The title of the thing"} {
		if !strings.Contains(schemas, want) {
			t.Errorf("input_schemas bucket missing %q", want)
		}
	}
	if strings.Contains(schemas, "TOOL SELECTION GUIDE") {
		t.Error("input_schemas bucket leaked usage instructions")
	}

	// usage_instructions: the top-level pair.
	if !strings.Contains(usage, "TOOL SELECTION GUIDE") {
		t.Errorf("usage_instructions bucket missing guide text: %q", usage)
	}

	// metadata: every other tool-level pair (name/score/server/call_with/annotations).
	for _, want := range []string{`"name"`, `"score"`, `"server"`, `"call_with"`, `"readOnlyHint"`} {
		if !strings.Contains(meta, want) {
			t.Errorf("metadata bucket missing %q", want)
		}
	}
	if strings.Contains(meta, "inputSchema") {
		t.Errorf("metadata bucket leaked schema bytes: %q", meta)
	}

	// other: the response envelope (query/total/session_risk + structure).
	for _, want := range []string{`"query"`, `"session_risk"`, `"total"`} {
		if !strings.Contains(other, want) {
			t.Errorf("other bucket missing envelope key %q", want)
		}
	}
	if strings.Contains(other, "do_thing_0") {
		t.Errorf("other bucket leaked tool metadata: %q", other)
	}
}

func TestPartitionRetrieveToolsResponse_Deterministic(t *testing.T) {
	raw := retrieveToolsResponseFixture(t, 3)
	spans1, n1, err1 := PartitionRetrieveToolsResponse(raw)
	spans2, n2, err2 := PartitionRetrieveToolsResponse(raw)
	if err1 != nil || err2 != nil {
		t.Fatalf("partition errors: %v / %v", err1, err2)
	}
	if n1 != n2 || !reflect.DeepEqual(spans1, spans2) {
		t.Errorf("partition not deterministic:\nrun1: %v (%d)\nrun2: %v (%d)", spans1, n1, spans2, n2)
	}
}

func TestPartitionRetrieveToolsResponse_EmptyTools(t *testing.T) {
	raw := `{"query":"x","tools":[],"total":0,"usage_instructions":"U"}`
	spans, count, err := PartitionRetrieveToolsResponse(raw)
	if err != nil {
		t.Fatalf("PartitionRetrieveToolsResponse: %v", err)
	}
	if count != 0 {
		t.Errorf("result count = %d, want 0", count)
	}
	if spans[len(spans)-1].End != len(raw) {
		t.Errorf("partition does not cover the whole text")
	}
	if got := componentText(raw, spans, ComponentUsageInstructions); !strings.Contains(got, `"U"`) {
		t.Errorf("usage_instructions bucket = %q, want to contain %q", got, `"U"`)
	}
}

func TestPartitionRetrieveToolsResponse_Errors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"invalid json", `{"tools":`},
		{"top-level array", `[1,2]`},
		{"top-level scalar", `42`},
		{"tools not an array", `{"tools":42}`},
		{"tools element not an object", `{"tools":[42]}`},
		{"trailing garbage", `{"tools":[]}x`},
		{"empty input", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := PartitionRetrieveToolsResponse(tc.raw); err == nil {
				t.Errorf("PartitionRetrieveToolsResponse(%q) expected error, got nil", tc.raw)
			}
		})
	}
}

// liveCapturedResponse is a trimmed excerpt of a REAL retrieve_tools response
// captured 2026-07-14 from a booted snapshot proxy (127.0.0.1:8094, the 065
// reference-server config) via bench.InvokeRetrieveTools — the T015 smoke run.
// Descriptions and usage_instructions are truncated and the tool list cut to
// two entries, but the key set, key order (json.Marshal sorted), and value
// shapes are verbatim wire bytes: this pins the parser to reality, not to the
// synthetic fixture's assumptions. Note session_risk's real inner fields
// (has_* flags) differ from the synthetic fixture — both belong to "other".
const liveCapturedResponse = `{"query":"read a file from disk","session_risk":{"has_destructive_tools":true,"has_open_world_tools":true,"has_write_tools":true,"lethal_trifecta":true,"level":"high"},"tools":[{"annotations":{"openWorldHint":false,"readOnlyHint":true},"call_with":"call_tool_read","description":"Read the complete contents of a file from the file system as text.","inputSchema":{"properties":{"head":{"description":"If provided, returns only the first N lines of the file","type":"number"},"path":{"type":"string"},"tail":{"description":"If provided, returns only the last N lines of the file","type":"number"}},"required":["path"],"type":"object"},"name":"read_text_file","score":0.0715792545451794,"server":"filesystem"},{"annotations":{"openWorldHint":false,"readOnlyHint":true},"call_with":"call_tool_read","description":"Read the complete contents of a file as text.","inputSchema":{"properties":{"head":{"description":"If provided, returns only the first N lines of the file","type":"number"},"path":{"type":"string"},"tail":{"description":"If provided, returns only the last N lines of the file","type":"number"}},"required":["path"],"type":"object"},"name":"read_file","score":0.053632441228788234,"server":"filesystem"}],"total":2,"usage_instructions":"TOOL SELECTION GUIDE: Check the 'call_with' field for each tool, then use the matching tool variant. DECISION RULES BY T"}`

func TestPartitionRetrieveToolsResponse_LiveCapturedShape(t *testing.T) {
	tk := newTestTokenizer(t)
	raw := liveCapturedResponse

	spans, count, err := PartitionRetrieveToolsResponse(raw)
	if err != nil {
		t.Fatalf("PartitionRetrieveToolsResponse on live-captured shape: %v", err)
	}
	if count != 2 {
		t.Errorf("result count = %d, want 2", count)
	}

	total, comps, err := AttributeTokens(tk, raw, spans)
	if err != nil {
		t.Fatalf("AttributeTokens on live-captured shape: %v", err)
	}
	sum := 0
	for _, v := range comps {
		sum += v
	}
	if sum != total {
		t.Errorf("sum(components) = %d, want total %d", sum, total)
	}
	if want := tk.Count(raw); total != want {
		t.Errorf("total = %d, want whole-text count %d", total, want)
	}

	// Bucket sanity on real wire bytes: schemas in input_schemas, tool
	// descriptions in descriptions, the has_* session_risk flags in other,
	// score/server/annotations in metadata.
	if got := componentText(raw, spans, ComponentInputSchemas); !strings.Contains(got, `"required":["path"]`) {
		t.Errorf("input_schemas bucket missing real schema bytes: %q", got)
	}
	if got := componentText(raw, spans, ComponentDescriptions); !strings.Contains(got, "Read the complete contents") {
		t.Errorf("descriptions bucket missing real description: %q", got)
	}
	if got := componentText(raw, spans, ComponentOther); !strings.Contains(got, "has_destructive_tools") {
		t.Errorf("other bucket missing real session_risk fields: %q", got)
	}
	for _, want := range []string{`"score"`, `"server"`, `"call_with"`, `"openWorldHint"`} {
		if got := componentText(raw, spans, ComponentMetadata); !strings.Contains(got, want) {
			t.Errorf("metadata bucket missing %q", want)
		}
	}
}

func TestAttributeTokens_SumEqualsTotal(t *testing.T) {
	tk := newTestTokenizer(t)
	raw := retrieveToolsResponseFixture(t, 5)
	spans, _, err := PartitionRetrieveToolsResponse(raw)
	if err != nil {
		t.Fatalf("PartitionRetrieveToolsResponse: %v", err)
	}

	total, comps, err := AttributeTokens(tk, raw, spans)
	if err != nil {
		t.Fatalf("AttributeTokens: %v", err)
	}

	// The FR-002 invariant, exactly: components sum to the total.
	sum := 0
	for _, v := range comps {
		sum += v
	}
	if sum != total {
		t.Errorf("sum(components) = %d, want total %d", sum, total)
	}

	// The total is the whole-text tokenization — the same number the live run
	// gets by counting the full MCP text content once.
	if want := tk.Count(raw); total != want {
		t.Errorf("total = %d, want whole-text count %d", total, want)
	}

	// All five canonical components are present (zero-valued allowed), and on a
	// schema-bearing fixture each real bucket is non-empty with input_schemas
	// the heaviest (the pattern the profiler exists to expose).
	for _, label := range []string{
		ComponentInputSchemas, ComponentDescriptions, ComponentUsageInstructions,
		ComponentMetadata, ComponentOther,
	} {
		if _, ok := comps[label]; !ok {
			t.Errorf("components missing canonical key %q", label)
		}
	}
	for _, label := range []string{
		ComponentInputSchemas, ComponentDescriptions, ComponentUsageInstructions, ComponentMetadata,
	} {
		if comps[label] <= 0 {
			t.Errorf("component %q = %d, want > 0", label, comps[label])
		}
	}
	for _, label := range []string{ComponentDescriptions, ComponentUsageInstructions, ComponentMetadata, ComponentOther} {
		if comps[ComponentInputSchemas] <= comps[label] {
			t.Errorf("input_schemas (%d) should dominate %q (%d) on a schema-bearing fixture",
				comps[ComponentInputSchemas], label, comps[label])
		}
	}
}

func TestAttributeTokens_SingleSpanEqualsWholeCount(t *testing.T) {
	tk := newTestTokenizer(t)
	text := `{"tools":[],"query":"nothing to see"}`
	spans := []Span{{Label: ComponentOther, Start: 0, End: len(text)}}
	total, comps, err := AttributeTokens(tk, text, spans)
	if err != nil {
		t.Fatalf("AttributeTokens: %v", err)
	}
	if want := tk.Count(text); total != want || comps[ComponentOther] != want {
		t.Errorf("total=%d other=%d, want both %d", total, comps[ComponentOther], want)
	}
}

// TestAttributeTokens_StartByteAttribution pins the attribution rule: a token
// is attributed to the span containing its STARTING byte. "hello" is a single
// cl100k token; splitting the text mid-word must not split its attribution.
func TestAttributeTokens_StartByteAttribution(t *testing.T) {
	tk := newTestTokenizer(t)
	text := "hello,world"
	if got := tk.Count("hello"); got != 1 {
		t.Fatalf("precondition: %q must be a single cl100k token, got %d", "hello", got)
	}
	total := tk.Count(text)

	// Boundary on a pre-tokenization boundary: exact per-span counts.
	aligned := []Span{
		{Label: ComponentDescriptions, Start: 0, End: 5},  // "hello"
		{Label: ComponentOther, Start: 5, End: len(text)}, // ",world"
	}
	gotTotal, comps, err := AttributeTokens(tk, text, aligned)
	if err != nil {
		t.Fatalf("AttributeTokens (aligned): %v", err)
	}
	if gotTotal != total {
		t.Errorf("total = %d, want %d", gotTotal, total)
	}
	if comps[ComponentDescriptions] != 1 || comps[ComponentOther] != total-1 {
		t.Errorf("aligned split: descriptions=%d other=%d, want 1 and %d",
			comps[ComponentDescriptions], comps[ComponentOther], total-1)
	}

	// Boundary in the middle of the "hello" token: the whole token goes to the
	// span owning byte 0 — attribution is by starting byte, never proportional.
	straddle := []Span{
		{Label: ComponentDescriptions, Start: 0, End: 3}, // "hel"
		{Label: ComponentOther, Start: 3, End: len(text)},
	}
	_, comps, err = AttributeTokens(tk, text, straddle)
	if err != nil {
		t.Fatalf("AttributeTokens (straddle): %v", err)
	}
	if comps[ComponentDescriptions] != 1 || comps[ComponentOther] != total-1 {
		t.Errorf("straddle split: descriptions=%d other=%d, want 1 and %d",
			comps[ComponentDescriptions], comps[ComponentOther], total-1)
	}
}

func TestAttributeTokens_RejectsBadPartitions(t *testing.T) {
	tk := newTestTokenizer(t)
	text := "hello,world"
	cases := []struct {
		name  string
		spans []Span
	}{
		{"nil spans on non-empty text", nil},
		{"gap", []Span{{ComponentOther, 0, 4}, {ComponentOther, 6, len(text)}}},
		{"overlap", []Span{{ComponentOther, 0, 6}, {ComponentOther, 5, len(text)}}},
		{"short cover", []Span{{ComponentOther, 0, len(text) - 1}}},
		{"over cover", []Span{{ComponentOther, 0, len(text) + 1}}},
		{"not starting at zero", []Span{{ComponentOther, 1, len(text)}}},
		{"empty span", []Span{{ComponentOther, 0, 0}, {ComponentOther, 0, len(text)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := AttributeTokens(tk, text, tc.spans); err == nil {
				t.Errorf("expected error for %s partition", tc.name)
			}
		})
	}
}

func TestMeasureRetrieveToolsResponse(t *testing.T) {
	tk := newTestTokenizer(t)
	raw := retrieveToolsResponseFixture(t, 3)

	m, err := MeasureRetrieveToolsResponse(tk, "q-001", raw, 12.5)
	if err != nil {
		t.Fatalf("MeasureRetrieveToolsResponse: %v", err)
	}
	if m.QueryID != "q-001" {
		t.Errorf("QueryID = %q, want q-001", m.QueryID)
	}
	if m.ResultCount != 3 {
		t.Errorf("ResultCount = %d, want 3", m.ResultCount)
	}
	if m.LatencyMs != 12.5 {
		t.Errorf("LatencyMs = %v, want 12.5", m.LatencyMs)
	}
	if want := tk.Count(raw); m.TotalTokens != want {
		t.Errorf("TotalTokens = %d, want %d", m.TotalTokens, want)
	}
	sum := 0
	for _, v := range m.Components {
		sum += v
	}
	if sum != m.TotalTokens {
		t.Errorf("sum(components) = %d, want %d", sum, m.TotalTokens)
	}

	if _, err := MeasureRetrieveToolsResponse(tk, "q-bad", `not json`, 0); err == nil {
		t.Error("expected error for non-JSON response text")
	}
}

func TestSummarizeResponseCost(t *testing.T) {
	// Ten totals 10..100, shuffled: nearest-rank p50 = 50, p95 = 100.
	totals := []int{70, 10, 100, 40, 90, 20, 60, 30, 80, 50}
	perQuery := make([]DiscoveryResponseMeasurement, len(totals))
	for i, tot := range totals {
		perQuery[i] = DiscoveryResponseMeasurement{
			QueryID:     fmt.Sprintf("q-%02d", i),
			TotalTokens: tot,
			Components:  map[string]int{ComponentOther: tot},
		}
	}

	s := SummarizeResponseCost(perQuery)
	if s.P50 != 50 {
		t.Errorf("P50 = %d, want 50", s.P50)
	}
	if s.P95 != 100 {
		t.Errorf("P95 = %d, want 100", s.P95)
	}
	if s.Max != 100 {
		t.Errorf("Max = %d, want 100", s.Max)
	}
	if s.Mean != 55.0 {
		t.Errorf("Mean = %v, want 55.0", s.Mean)
	}
	if len(s.PerQuery) != len(perQuery) {
		t.Errorf("PerQuery rows = %d, want %d", len(s.PerQuery), len(perQuery))
	}
	// Input order preserved (per-query rows keyed by golden-set order, FR-010).
	if s.PerQuery[0].QueryID != "q-00" || s.PerQuery[0].TotalTokens != 70 {
		t.Errorf("PerQuery[0] reordered: %+v", s.PerQuery[0])
	}
}

func TestSummarizeResponseCost_SingleAndEmpty(t *testing.T) {
	one := SummarizeResponseCost([]DiscoveryResponseMeasurement{{QueryID: "q", TotalTokens: 42}})
	if one.P50 != 42 || one.P95 != 42 || one.Max != 42 || one.Mean != 42.0 {
		t.Errorf("single measurement: got %+v, want all 42", one)
	}

	empty := SummarizeResponseCost(nil)
	if empty.P50 != 0 || empty.P95 != 0 || empty.Max != 0 || empty.Mean != 0 {
		t.Errorf("empty input: got %+v, want zeros", empty)
	}
	if len(empty.PerQuery) != 0 {
		t.Errorf("empty input: PerQuery = %v, want empty", empty.PerQuery)
	}
}
