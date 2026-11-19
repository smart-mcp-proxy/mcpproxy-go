package server

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// tool_response_limit and structuredContent (issue #1932).
//
// The limit used to bound only TextContent blocks, so an upstream that put a
// large payload in structuredContent reached the agent's context in full.
//
// withholdOversizeStructured now runs on the upstream result BEFORE output
// sanitisation: when the structured result's JSON exceeds the limit it is
// taken out of structuredContent and, unless a text block already carries the
// same JSON (the MCP spec says servers SHOULD send one), appended as a text
// block. From there it is an ordinary text block: sanitisation redacts or
// blocks it, the TOON seam and sensitive-data detection see it, and
// forwardContentResult truncates it with a read_cache handle like any other
// oversize block. A structured result within the limit is forwarded unchanged.
//
// Output-schema validation still judges the upstream's structured result
// (outputValidationView), so Spec 056 decisions do not change.
//
// The direct surface lists a tool with its upstream outputSchema in full mode,
// and the MCP spec requires such a tool to return conforming structured
// content (MCP clients reject a result without it). There the structured
// result is withheld only when the published listing carries no outputSchema
// (directCatalogEntry.OutputSchemaListed).

// structuredWithheldLabel heads the text block that replaces a withheld
// structured result: actual size, then the limit.
const structuredWithheldLabel = "[mcpproxy] structuredContent withheld: %d chars exceeds tool_response_limit (%d). The structured result follows as JSON text."

// oversizeStructuredJSON returns the JSON encoding of structured when it
// exceeds limit, or nil when there is nothing to withhold (no structured
// result, no limit, within the limit, or not encodable).
func oversizeStructuredJSON(structured any, limit int, logger *zap.Logger, toolName string) []byte {
	if structured == nil || limit <= 0 {
		return nil
	}
	b, err := json.Marshal(structured)
	if err != nil {
		// Decoded from JSON upstream, so this does not happen in practice.
		// Without a size there is nothing to bound; forward as before.
		if logger != nil {
			logger.Warn("Cannot measure structuredContent against tool_response_limit; forwarding it unchanged",
				zap.String("tool", toolName), zap.Error(err))
		}
		return nil
	}
	if len(b) <= limit {
		return nil
	}
	return b
}

// textDuplicatesStructured reports whether one of the text blocks is a JSON
// rendering of the structured result (any formatting).
func textDuplicatesStructured(content []mcp.Content, structuredJSON []byte) bool {
	var want any
	if err := json.Unmarshal(structuredJSON, &want); err != nil {
		return false
	}
	for _, c := range content {
		tc, ok := c.(mcp.TextContent)
		if !ok {
			continue
		}
		s := strings.TrimSpace(tc.Text)
		if s == "" || (s[0] != '{' && s[0] != '[') {
			continue
		}
		var got any
		if err := json.Unmarshal([]byte(s), &got); err != nil {
			continue
		}
		if reflect.DeepEqual(got, want) {
			return true
		}
	}
	return false
}

// withheldStructured records what withholdOversizeStructured took out of a
// result. The zero value means nothing was withheld.
type withheldStructured struct {
	// value is the upstream's structured result; raw its original bytes.
	value any
	raw   json.RawMessage
	// added is how many text blocks were appended at the end of Content.
	added int
}

// withholdOversizeStructured applies tool_response_limit to the structured
// result of an upstream CallToolResult, in place. It must run before output
// sanitisation so the replacement text is sanitised like every other block.
// The returned record serves output-schema validation (outputValidationView)
// and response-size accounting (upstreamSizeView).
func withholdOversizeStructured(result interface{}, limit int, logger *zap.Logger, toolName string) withheldStructured {
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil {
		return withheldStructured{}
	}
	sj := oversizeStructuredJSON(ctr.StructuredContent, limit, logger, toolName)
	if sj == nil {
		return withheldStructured{}
	}
	w := withheldStructured{value: ctr.StructuredContent, raw: ctr.RawStructuredContent}
	duplicate := textDuplicatesStructured(ctr.Content, sj)
	ctr.StructuredContent = nil
	// mcp-go marshals RawStructuredContent in preference to StructuredContent.
	ctr.RawStructuredContent = nil
	if !duplicate {
		// Clip so the append never writes into a backing array shared with
		// the caller's slice.
		ctr.Content = append(slices.Clip(ctr.Content),
			mcp.NewTextContent(fmt.Sprintf(structuredWithheldLabel, len(sj), limit)),
			mcp.NewTextContent(string(sj)),
		)
		w.added = 2
	}
	return w
}

// upstreamSizeView returns result as the upstream returned it, for Spec 069
// response-size accounting: the withheld structured value restored and the
// appended text blocks dropped. Sanitisation rewrites blocks in place and
// never changes their number, so the leading blocks are the upstream's
// (sanitised, as before this change). result itself is not modified.
func upstreamSizeView(result interface{}, w withheldStructured) interface{} {
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil || w.value == nil {
		return result
	}
	view := *ctr
	view.StructuredContent = w.value
	view.RawStructuredContent = w.raw
	if w.added > 0 && w.added <= len(view.Content) {
		n := len(view.Content) - w.added
		view.Content = view.Content[:n:n]
	}
	return &view
}

// outputValidationView returns the result output-schema validation should
// judge: forwarded, or a copy of it carrying the structured value that
// tool_response_limit withheld. Validating the forwarded result would see no
// structured content at all, and strict mode with
// missing_structured_content=block would refuse every large response.
func outputValidationView(forwarded *mcp.CallToolResult, w withheldStructured) *mcp.CallToolResult {
	if forwarded == nil || w.value == nil || forwarded.StructuredContent != nil {
		return forwarded
	}
	view := *forwarded
	view.StructuredContent = w.value
	return &view
}

// outputSchemaAdvertised reports whether a tool's output schema can be
// published on its listing (the rule applyToolOutputSchemaJSON applies).
func outputSchemaAdvertised(outputSchemaJSON string) bool {
	return outputSchemaJSON != "" && json.Valid([]byte(outputSchemaJSON))
}
