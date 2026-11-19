package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
)

// Issue #1932: tool_response_limit must bound structuredContent too, not only
// the text blocks. The handlers run withholdOversizeStructured on the upstream
// result before sanitisation, then forwardContentResult; these tests drive
// that same sequence.

// bigStructured returns a structured value whose JSON encoding is well over
// 500 chars and carries a records array the truncator can paginate.
func bigStructured() map[string]interface{} {
	items := make([]interface{}, 0, 60)
	for i := 0; i < 60; i++ {
		items = append(items, map[string]interface{}{"id": float64(i), "title": strings.Repeat("T", 20)})
	}
	return map[string]interface{}{"items": items}
}

func textBlocks(r *mcp.CallToolResult) []string {
	var out []string
	for _, c := range r.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			out = append(out, tc.Text)
		}
	}
	return out
}

func TestWithholdOversizeStructured_ForwardedAsCachedTextBlock(t *testing.T) {
	structured := bigStructured()
	upstream := &mcp.CallToolResult{
		Content:              []mcp.Content{mcp.NewTextContent("Found 60 tasks.")},
		StructuredContent:    structured,
		RawStructuredContent: mustJSON(t, structured),
	}
	store := &captureStore{}
	tr := truncate.NewTruncator(500)

	withheld := withholdOversizeStructured(upstream, tr.Limit(), nil, "vikunja:list_tasks")
	assert.Equal(t, structured, withheld.value)
	assert.Equal(t, 2, withheld.added)
	assert.Nil(t, upstream.StructuredContent)
	assert.Nil(t, upstream.RawStructuredContent, "mcp-go marshals the raw bytes in preference; they must go too")

	forwarded, response, wasTruncated := forwardContentResult(upstream, tr, store, nil, "vikunja:list_tasks", map[string]interface{}{"q": "x"})

	assert.True(t, wasTruncated)
	assert.Nil(t, forwarded.StructuredContent, "oversize structuredContent must not reach the client")
	wire, err := json.Marshal(forwarded)
	require.NoError(t, err)
	assert.NotContains(t, string(wire), "structuredContent\":")

	texts := textBlocks(forwarded)
	require.Len(t, texts, 3, "original text, label, structured JSON")
	assert.Equal(t, "Found 60 tasks.", texts[0])
	assert.Contains(t, texts[1], "structuredContent withheld")
	assert.Contains(t, texts[2], "read_cache")
	for _, txt := range texts {
		assert.LessOrEqual(t, len(txt), 500)
		assert.Contains(t, response, txt, "logs/metrics text must match what the agent sees")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	require.Len(t, store.calls, 1, "the full structured result must be cached for read_cache")
	assert.JSONEq(t, string(mustJSON(t, structured)), store.calls[0].content)
	assert.Contains(t, texts[2], store.calls[0].key, "the banner must name the key that was stored")
	assert.Equal(t, 60, store.calls[0].totalRecords)
}

func TestWithholdOversizeStructured_DuplicateTextNoSecondCopy(t *testing.T) {
	structured := bigStructured()
	// MCP servers SHOULD also return the serialized JSON in a text block. Use
	// pretty-printing so the copy is semantically, not byte-for-byte, equal.
	pretty, err := json.MarshalIndent(structured, "", "  ")
	require.NoError(t, err)
	upstream := &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent(string(pretty))},
		StructuredContent: structured,
	}

	w := withholdOversizeStructured(upstream, 500, nil, "t")
	require.NotNil(t, w.value)
	assert.Zero(t, w.added)
	assert.Nil(t, upstream.StructuredContent)
	assert.Len(t, upstream.Content, 1, "a text copy of the structured result already carries it")
}

func TestWithholdOversizeStructured_DoesNotWriteIntoCallerBackingArray(t *testing.T) {
	backing := make([]mcp.Content, 1, 4)
	backing[0] = mcp.NewTextContent("ok")
	upstream := &mcp.CallToolResult{Content: backing[:1], StructuredContent: bigStructured()}

	require.NotNil(t, withholdOversizeStructured(upstream, 500, nil, "t").value)
	assert.Len(t, upstream.Content, 3)
	assert.Nil(t, backing[:2][1], "the caller's spare capacity must stay untouched")
}

func TestWithholdOversizeStructured_NoOp(t *testing.T) {
	small := map[string]interface{}{"ok": true, "count": float64(3)}
	cases := map[string]struct {
		result interface{}
		limit  int
	}{
		"within limit":    {&mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("x")}, StructuredContent: small}, 500},
		"unlimited":       {&mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("x")}, StructuredContent: bigStructured()}, 0},
		"no structured":   {&mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("x")}}, 500},
		"not a result":    {"plain string", 500},
		"typed nil value": {(*mcp.CallToolResult)(nil), 500},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			before, _ := json.Marshal(tc.result)
			assert.Equal(t, withheldStructured{}, withholdOversizeStructured(tc.result, tc.limit, nil, "t"))
			after, _ := json.Marshal(tc.result)
			assert.Equal(t, string(before), string(after), "nothing to withhold leaves the result unchanged (Spec 056 SC-002)")
		})
	}
}

// Reviewer round 1: the replacement text must go through output sanitisation.
// The handlers withhold before applyOutputSanitisation; drive that order.
func TestWithholdOversizeStructured_ReplacementIsSanitised(t *testing.T) {
	cfg := config.DefaultOutputSanitisationConfig()
	cfg.ResponseAction = "redact"
	p := newSanProxy(t, cfg, true)

	structured := bigStructured()
	structured["token"] = "key=" + awsKeyFixture
	upstream := &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent("Found 60 tasks.")},
		StructuredContent: structured,
	}
	require.NotNil(t, withholdOversizeStructured(upstream, 500, nil, "t").value)
	require.Nil(t, p.applyOutputSanitisation(context.Background(), "srv", "tool", "req", contracts.ContentTrustUntrusted, upstream))

	store := &captureStore{}
	forwarded, _, _ := forwardContentResult(upstream, truncate.NewTruncator(500), store, nil, "t", nil)
	wire, err := json.Marshal(forwarded)
	require.NoError(t, err)
	assert.NotContains(t, string(wire), awsKeyFixture)
	store.mu.Lock()
	defer store.mu.Unlock()
	require.NotEmpty(t, store.calls)
	for _, c := range store.calls {
		assert.NotContains(t, c.content, awsKeyFixture, "the cached copy is the sanitised one")
	}
	assert.Contains(t, store.calls[0].content, "[REDACTED:", "the secret sits past the cut, so only the cache proves the redaction ran")
}

func TestOutputValidationView_RestoresWithheldStructuredContent(t *testing.T) {
	structured := bigStructured()
	upstream := &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent("ok")},
		StructuredContent: structured,
	}
	withheld := withholdOversizeStructured(upstream, 500, nil, "t")
	forwarded, _, _ := forwardContentResult(upstream, truncate.NewTruncator(500), nil, nil, "t", nil)
	require.Nil(t, forwarded.StructuredContent)

	view := outputValidationView(forwarded, withheld)
	require.NotNil(t, view)
	assert.Equal(t, structured, view.StructuredContent, "validation must judge what the upstream returned")
	assert.Nil(t, forwarded.StructuredContent, "building the view must not put the payload back on the forwarded result")

	// Strict mode with missing_structured_content=block must not block a
	// response only because the proxy withheld its structured payload.
	schema := `{"type":"object","required":["items"]}`
	assert.False(t, evaluateOutputValidation(newTestValidator(), "s:t", schema, true, true, view).block,
		"a conforming upstream result passes even though its structured payload was withheld")
	// Control: the forwarded result itself would have been blocked.
	assert.True(t, evaluateOutputValidation(newTestValidator(), "s:t", schema, true, true, forwarded).block)
}

func TestOutputValidationView_PassThrough(t *testing.T) {
	small := &mcp.CallToolResult{StructuredContent: map[string]interface{}{"a": 1.0}}
	assert.Same(t, small, outputValidationView(small, withheldStructured{}))
	assert.Same(t, small, outputValidationView(small, withheldStructured{value: map[string]interface{}{"b": 2.0}}), "a forwarded structured value is never replaced")

	noStructured := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("x")}}
	assert.Same(t, noStructured, outputValidationView(noStructured, withheldStructured{}))

	assert.Nil(t, outputValidationView(nil, withheldStructured{value: map[string]interface{}{}}))
}

// Reviewer round 1: on the direct surface the handler may withhold only when
// the PUBLISHED listing carries no outputSchema. A deferred listing never
// publishes one, even when the upstream declared it.
func TestRenderDirectTools_RecordsWhetherOutputSchemaIsListed(t *testing.T) {
	const schema = `{"type":"object"}`
	tools := []*config.ToolMetadata{
		{ServerName: "srv", Name: "with_schema", Description: "d", ParamsJSON: `{"type":"object"}`, OutputSchemaJSON: schema},
		{ServerName: "srv", Name: "no_schema", Description: "d", ParamsJSON: `{"type":"object"}`},
		{ServerName: "srv", Name: "bad_schema", Description: "d", ParamsJSON: `{"type":"object"}`, OutputSchemaJSON: `{"type":`},
	}
	for _, mode := range []string{config.DirectToolResponseModeFull, config.DirectToolResponseModeDeferred} {
		t.Run(mode, func(t *testing.T) {
			p := createTestMCPProxyServer(t)
			cat := buildDirectCatalog(tools, nil)
			cat.mode = mode
			listed := map[string]bool{}
			for _, st := range p.renderDirectTools(cat) {
				entry, ok := cat.Lookup(st.Tool.Name)
				require.True(t, ok)
				assert.Equal(t, len(st.Tool.RawOutputSchema) > 0, entry.OutputSchemaListed, "the flag must mirror the published listing")
				listed[entry.ToolName] = entry.OutputSchemaListed
			}
			assert.Equal(t, map[string]bool{
				"with_schema": mode == config.DirectToolResponseModeFull,
				"no_schema":   false,
				"bad_schema":  false,
			}, listed)
		})
	}
}

// Reviewer round 2: Spec 069 response_bytes must keep counting what the
// upstream returned, not the withheld form.
func TestUpstreamSizeView_MeasuresTheUpstreamResult(t *testing.T) {
	for name, text := range map[string]string{
		"separate text":  "Found 60 tasks.",
		"duplicate text": string(mustJSON(t, bigStructured())),
	} {
		t.Run(name, func(t *testing.T) {
			structured := bigStructured()
			upstream := &mcp.CallToolResult{
				Content:           []mcp.Content{mcp.NewTextContent(text)},
				StructuredContent: structured,
				// As decoded off the wire: original bytes, not re-marshalled.
				RawStructuredContent: json.RawMessage(" " + string(mustJSON(t, structured)) + " "),
			}
			before := rawByteSize(upstream)

			w := withholdOversizeStructured(upstream, 500, nil, "t")
			require.NotNil(t, w.value)
			assert.NotEqual(t, before, rawByteSize(upstream), "control: the withheld form has a different size")
			assert.Equal(t, before, rawByteSize(upstreamSizeView(upstream, w)))
			assert.Nil(t, upstream.StructuredContent, "the view must not modify the result")
		})
	}
	plain := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("x")}}
	assert.Same(t, plain, upstreamSizeView(plain, withheldStructured{}))
}
