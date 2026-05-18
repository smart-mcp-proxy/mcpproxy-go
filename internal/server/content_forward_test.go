package server

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
)

// captureStore is a CacheStore test double that records every Store call.
type captureStore struct {
	mu      sync.Mutex
	calls   []captureCall
	failErr error
}

type captureCall struct {
	key          string
	toolName     string
	args         map[string]interface{}
	content      string
	recordPath   string
	totalRecords int
}

func (c *captureStore) Store(key, toolName string, args map[string]interface{}, content, recordPath string, totalRecords int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, captureCall{
		key:          key,
		toolName:     toolName,
		args:         args,
		content:      content,
		recordPath:   recordPath,
		totalRecords: totalRecords,
	})
	return c.failErr
}

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

	forwarded, text, truncated := forwardContentResult(upstream, truncator, nil, "test:tool", nil)

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

	forwarded, _, truncated := forwardContentResult(upstream, truncator, nil, "test:tool", nil)
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

	forwarded, text, truncated := forwardContentResult(upstream, truncator, nil, "test:tool", nil)
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
	forwarded, _, _ := forwardContentResult(nil, truncate.NewTruncator(0), nil, "t", nil)
	require.NotNil(t, forwarded)
	require.Equal(t, 1, len(forwarded.Content))

	// Case 2: a plain map — legacy JSON marshal path
	forwarded, text, _ := forwardContentResult(map[string]string{"key": "value"}, truncate.NewTruncator(0), nil, "t", nil)
	require.NotNil(t, forwarded)
	require.Equal(t, 1, len(forwarded.Content))
	assert.Contains(t, text, "key")
	assert.Contains(t, text, "value")
}

// TestForwardContentResult_StoresFullContentInCache verifies the bug fix:
// when a TextContent block exceeds the truncator limit AND a cache store is
// provided, the FULL pre-truncation text is persisted under the embedded
// cache key. Prior to this fix the truncator only emitted a "use read_cache"
// instruction but never actually stored the full payload, so every read_cache
// call returned "cache key not found".
func TestForwardContentResult_StoresFullContentInCache(t *testing.T) {
	// Build a JSON array large enough to trip the 500-char truncator AND
	// have an inner array of records the truncator can paginate.
	records := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		records = append(records, `{"id":"`+strings.Repeat("X", 20)+`"}`)
	}
	bigJSON := `{"items":[` + strings.Join(records, ",") + `]}`

	upstream := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(bigJSON)},
	}
	store := &captureStore{}
	tr := truncate.NewTruncator(500)

	args := map[string]interface{}{"q": "demo"}
	_, _, wasTruncated := forwardContentResult(upstream, tr, store, "github:pull_request_read", args)
	require.True(t, wasTruncated)

	store.mu.Lock()
	defer store.mu.Unlock()
	require.Len(t, store.calls, 1, "exactly one Store call expected for one truncated TextContent block")
	c := store.calls[0]
	assert.NotEmpty(t, c.key, "cache key must be non-empty for read_cache to resolve it")
	assert.Equal(t, "github:pull_request_read", c.toolName)
	assert.Equal(t, args, c.args)
	assert.Equal(t, bigJSON, c.content, "full pre-truncation content must be persisted")
	assert.Greater(t, c.totalRecords, 0, "totalRecords should reflect the records array length")
}

// TestForwardContentResult_NoCacheStoreNoPanic guards the cache-disabled path:
// callers that don't have a cache plumbed through must not crash, and the
// truncated output must still flow through unchanged.
func TestForwardContentResult_NoCacheStoreNoPanic(t *testing.T) {
	bigText := strings.Repeat("y", 3000)
	upstream := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(bigText)},
	}
	tr := truncate.NewTruncator(500)

	forwarded, _, wasTruncated := forwardContentResult(upstream, tr, nil, "t", nil)
	require.NotNil(t, forwarded)
	require.True(t, wasTruncated)
}

// TestForwardContentResult_RoundTripViaRealCacheManager wires
// forwardContentResult against the real cache.Manager (BBolt-backed) and
// exercises the full pagination contract end-to-end: an oversize payload is
// truncated → the full payload lands in the cache → cache.Manager.GetRecords
// hands back paginated records by offset/limit. This is the contract that
// the read_cache MCP tool relies on; the original bug broke it because the
// Store call was missing from the truncation path.
func TestForwardContentResult_RoundTripViaRealCacheManager(t *testing.T) {
	// Stand up a tmp BBolt + real cache.Manager.
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	db, err := bbolt.Open(dbPath, 0o600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mgr, err := cache.NewManager(db, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(mgr.Close)

	// Build a payload shaped like the github MCP review-list response: a top
	// level array of N review objects, each non-trivial in size. ~50 records
	// * ~80 bytes each = ~4 KB; with a 600-byte truncator limit this trips
	// the cache path.
	records := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		records = append(records, fmt.Sprintf(`{"id":%d,"body":"%s"}`, i, strings.Repeat("z", 40)))
	}
	bigJSON := `[` + strings.Join(records, ",") + `]`

	upstream := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(bigJSON)},
	}
	args := map[string]interface{}{"perPage": 100}

	_, response, wasTruncated := forwardContentResult(
		upstream,
		truncate.NewTruncator(600),
		mgr,
		"github:pull_request_read",
		args,
	)
	require.True(t, wasTruncated)

	// Truncation banner must embed a usable cache key.
	idx := strings.Index(response, `key="`)
	require.NotEqual(t, -1, idx, "truncation banner missing cache key")
	keyStart := idx + len(`key="`)
	keyEnd := strings.Index(response[keyStart:], `"`)
	require.NotEqual(t, -1, keyEnd)
	key := response[keyStart : keyStart+keyEnd]
	require.NotEmpty(t, key)

	// Page 1: records 0..9
	page1, err := mgr.GetRecords(key, 0, 10)
	require.NoError(t, err, "read_cache (GetRecords) must succeed for the embedded key")
	require.Equal(t, 50, page1.Meta.TotalRecords)
	require.Len(t, page1.Records, 10)

	// Page 2: records 10..19, distinct from page 1 with monotonically
	// increasing ids — proves offset is honored, not silently 0.
	page2, err := mgr.GetRecords(key, 10, 10)
	require.NoError(t, err)
	require.Len(t, page2.Records, 10)

	idOf := func(rec interface{}) float64 {
		m, _ := rec.(map[string]interface{})
		id, _ := m["id"].(float64)
		return id
	}
	assert.Equal(t, float64(0), idOf(page1.Records[0]), "page 1 should start at id 0")
	assert.Equal(t, float64(10), idOf(page2.Records[0]), "page 2 should start at id 10")
	assert.Equal(t, float64(19), idOf(page2.Records[9]), "page 2 should end at id 19")
}

// TestForwardContentResult_FallbackPathStoresCache covers the legacy fallback
// where `result` is not a *mcp.CallToolResult — the JSON-wrapped fallback path
// must also persist its truncated payload so read_cache works there too.
func TestForwardContentResult_FallbackPathStoresCache(t *testing.T) {
	rows := make([]map[string]int, 80)
	for i := range rows {
		rows[i] = map[string]int{"n": i}
	}
	payload := map[string]interface{}{"rows": rows}

	store := &captureStore{}
	tr := truncate.NewTruncator(400)

	_, _, wasTruncated := forwardContentResult(payload, tr, store, "fallback:tool", nil)
	require.True(t, wasTruncated)

	store.mu.Lock()
	defer store.mu.Unlock()
	require.Len(t, store.calls, 1)
	assert.NotEmpty(t, store.calls[0].key)
	assert.Equal(t, "fallback:tool", store.calls[0].toolName)
	assert.Contains(t, store.calls[0].content, `"rows"`, "fallback path should persist the JSON-serialized full result")
}
