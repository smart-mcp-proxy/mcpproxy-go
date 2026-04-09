package server

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
)

// TestForwardContentResult_PreservesImageContent verifies that an ImageContent
// block from upstream is forwarded unchanged to the downstream client.
// Regression test for issue #368.
func TestForwardContentResult_PreservesImageContent(t *testing.T) {
	upstream := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent("Here is your image:"),
			mcp.NewImageContent("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwC", "image/png"),
		},
	}
	truncator := truncate.NewTruncator(0) // disabled

	forwarded, text, truncated := forwardContentResult(upstream, truncator, "test:tool", nil)

	require.NotNil(t, forwarded)
	require.Equal(t, 2, len(forwarded.Content), "both content blocks must be forwarded")
	assert.False(t, truncated)

	// First block: text preserved
	tc, ok := forwarded.Content[0].(mcp.TextContent)
	require.True(t, ok, "block 0 should remain TextContent")
	assert.Equal(t, "Here is your image:", tc.Text)

	// Second block: image preserved as native type
	ic, ok := forwarded.Content[1].(mcp.ImageContent)
	require.True(t, ok, "block 1 should remain ImageContent (not serialized to text)")
	assert.Equal(t, "image/png", ic.MIMEType)
	assert.Equal(t, "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwC", ic.Data)

	// Text representation used for logging should reference both blocks
	assert.Contains(t, text, "Here is your image:")
	assert.Contains(t, text, "[image:image/png")
}

// TestForwardContentResult_TruncatesOnlyText verifies that truncation applies
// to TextContent but leaves ImageContent and AudioContent untouched regardless
// of their size.
func TestForwardContentResult_TruncatesOnlyText(t *testing.T) {
	// Build a very large base64 payload to show it survives truncation
	bigData := strings.Repeat("A", 10000)
	bigText := strings.Repeat("x", 2000)

	upstream := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(bigText),
			mcp.NewImageContent(bigData, "image/png"),
			mcp.NewAudioContent(bigData, "audio/wav"),
		},
	}
	// Truncator with a 500-char limit
	truncator := truncate.NewTruncator(500)

	forwarded, _, truncated := forwardContentResult(upstream, truncator, "test:tool", nil)
	require.NotNil(t, forwarded)
	require.Equal(t, 3, len(forwarded.Content))
	assert.True(t, truncated, "text block should be marked as truncated")

	// Text was truncated
	tc, ok := forwarded.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Less(t, len(tc.Text), len(bigText), "text block should be shorter after truncation")

	// Image unchanged
	ic, ok := forwarded.Content[1].(mcp.ImageContent)
	require.True(t, ok)
	assert.Equal(t, bigData, ic.Data, "image data must be forwarded byte-for-byte")

	// Audio unchanged
	ac, ok := forwarded.Content[2].(mcp.AudioContent)
	require.True(t, ok)
	assert.Equal(t, bigData, ac.Data, "audio data must be forwarded byte-for-byte")
}

// TestForwardContentResult_TextOnlyNoTruncation exercises the common case of a
// small text-only response. Verifies the result is forwarded unchanged.
func TestForwardContentResult_TextOnlyNoTruncation(t *testing.T) {
	upstream := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent("small result"),
		},
	}
	truncator := truncate.NewTruncator(0)

	forwarded, text, truncated := forwardContentResult(upstream, truncator, "test:tool", nil)
	require.NotNil(t, forwarded)
	require.Equal(t, 1, len(forwarded.Content))
	assert.False(t, truncated)
	assert.Equal(t, "small result", text)

	tc, ok := forwarded.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "small result", tc.Text)
}

// TestForwardContentResult_Fallback verifies that if result is not a
// *mcp.CallToolResult (e.g., nil or some other interface value), the function
// falls back to legacy JSON-wrapping behavior without panicking.
func TestForwardContentResult_Fallback(t *testing.T) {
	// Case 1: nil — should not panic, returns a JSON "null" text wrapper
	forwarded, _, _ := forwardContentResult(nil, truncate.NewTruncator(0), "t", nil)
	require.NotNil(t, forwarded)
	require.Equal(t, 1, len(forwarded.Content))

	// Case 2: a plain map — legacy JSON marshal path
	forwarded, text, _ := forwardContentResult(map[string]string{"key": "value"}, truncate.NewTruncator(0), "t", nil)
	require.NotNil(t, forwarded)
	require.Equal(t, 1, len(forwarded.Content))
	assert.Contains(t, text, "key")
	assert.Contains(t, text, "value")
}
