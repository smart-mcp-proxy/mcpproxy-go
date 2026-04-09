package server

import (
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
)

// forwardContentResult preserves non-text content blocks (ImageContent, AudioContent,
// EmbeddedResource) from an upstream CallToolResult while applying truncation only to
// TextContent blocks. This fixes issue #368 where all content types were being
// serialized to JSON and wrapped in a single TextContent, destroying the ability of
// vision-capable LLMs to process images efficiently.
//
// Parameters:
//   - result: the upstream CallToolResult (passed as interface{} from Manager.CallTool)
//   - truncator: applies size limits to text blocks only
//   - toolName, args: passed to truncator for caching
//
// Returns:
//   - forwarded: the CallToolResult to return downstream (with original content types)
//   - textRepresentation: a text rendering of the full result for logging/metrics
//   - wasTruncated: whether any TextContent block was truncated
//   - cacheStored: whether the full response was stored in the cache (for audit)
//
// If result is not a *mcp.CallToolResult, it falls back to JSON-serializing the whole
// thing into a TextContent block (legacy behavior).
func forwardContentResult(result interface{}, truncator *truncate.Truncator, toolName string, args map[string]interface{}) (forwarded *mcp.CallToolResult, textRepresentation string, wasTruncated bool) {
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil {
		// Fallback: not a CallToolResult (should not happen with current upstream chain,
		// but guard against future changes). Use legacy JSON-wrap behavior.
		jsonBytes, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize result: %v", err)), "", false
		}
		text := string(jsonBytes)
		if truncator != nil && truncator.ShouldTruncate(text) {
			tr := truncator.Truncate(text, toolName, args)
			text = tr.TruncatedContent
			wasTruncated = true
		}
		return mcp.NewToolResultText(text), text, wasTruncated
	}

	// Walk the Content slice, truncating only TextContent blocks.
	// Build a parallel text representation for logging/metrics.
	newContent := make([]mcp.Content, 0, len(ctr.Content))
	var textBuilder []string
	for _, c := range ctr.Content {
		switch tc := c.(type) {
		case mcp.TextContent:
			txt := tc.Text
			if truncator != nil && truncator.ShouldTruncate(txt) {
				tr := truncator.Truncate(txt, toolName, args)
				txt = tr.TruncatedContent
				wasTruncated = true
			}
			tc.Text = txt
			newContent = append(newContent, tc)
			textBuilder = append(textBuilder, txt)
		case mcp.ImageContent:
			// Preserve image blocks unchanged. For logging, emit a placeholder.
			newContent = append(newContent, tc)
			textBuilder = append(textBuilder, fmt.Sprintf("[image:%s len=%d]", tc.MIMEType, len(tc.Data)))
		case mcp.AudioContent:
			// Preserve audio blocks unchanged. For logging, emit a placeholder.
			newContent = append(newContent, tc)
			textBuilder = append(textBuilder, fmt.Sprintf("[audio:%s len=%d]", tc.MIMEType, len(tc.Data)))
		default:
			// Unknown content type (e.g., EmbeddedResource). Forward as-is and
			// best-effort JSON encode for logging.
			newContent = append(newContent, c)
			if b, err := json.Marshal(c); err == nil {
				textBuilder = append(textBuilder, string(b))
			}
		}
	}

	forwarded = &mcp.CallToolResult{
		Result:            ctr.Result,
		Content:           newContent,
		StructuredContent: ctr.StructuredContent,
		IsError:           ctr.IsError,
	}
	textRepresentation = joinTextParts(textBuilder)
	return forwarded, textRepresentation, wasTruncated
}

// joinTextParts concatenates text parts with a newline separator.
// Equivalent to strings.Join but avoids importing strings just for this.
func joinTextParts(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	total := len(parts) - 1 // separators
	for _, p := range parts {
		total += len(p)
	}
	out := make([]byte, 0, total)
	for i, p := range parts {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, p...)
	}
	return string(out)
}
