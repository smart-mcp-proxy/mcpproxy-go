package server

import (
	"encoding/json"
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

// TestMaybeTruncateAndCacheText_HappyPathStores asserts that an oversized text
// with more than one paginable unit gets truncated AND stored under the
// embedded cache key — the contract required for read_cache pagination to keep
// working as the recursion depth grows.
func TestMaybeTruncateAndCacheText_HappyPathStores(t *testing.T) {
	records := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		records = append(records, fmt.Sprintf(`{"i":%d,"v":"%s"}`, i, strings.Repeat("p", 30)))
	}
	body := `{"records":[` + strings.Join(records, ",") + `],"meta":{"total":30}}`

	store := &captureStore{}
	out, wasTruncated := maybeTruncateAndCacheText(
		body,
		"read_cache",
		map[string]interface{}{"key": "AAA", "offset": 0, "limit": 30},
		30, // paginableUnits > 1 → recursion allowed
		truncate.NewTruncator(500),
		store,
	)
	require.True(t, wasTruncated)
	assert.Contains(t, out, `key="`, "truncated output must carry a usable cache key")

	store.mu.Lock()
	defer store.mu.Unlock()
	require.Len(t, store.calls, 1)
	assert.Equal(t, "read_cache", store.calls[0].toolName)
	assert.Equal(t, body, store.calls[0].content, "full pre-truncation body must be persisted")
}

// TestMaybeTruncateAndCacheText_NoRecurseOnSingleRecord covers the
// single-huge-record edge case. When read_cache returns one record bigger than
// the truncator limit, recursively caching it would produce a new key that
// resolves to the exact same oversized payload — an infinite loop the agent
// can never escape. paginableUnits=1 must short-circuit that.
func TestMaybeTruncateAndCacheText_NoRecurseOnSingleRecord(t *testing.T) {
	// A response shaped like a single huge record (e.g. one CodeRabbit review
	// body of ~70 KB) wrapped in a 1-element records array.
	body := `{"records":[{"body":"` + strings.Repeat("z", 5000) + `"}],"meta":{"total":1}}`
	store := &captureStore{}

	out, wasTruncated := maybeTruncateAndCacheText(
		body,
		"read_cache",
		map[string]interface{}{"key": "AAA", "offset": 0, "limit": 1},
		1, // single record — no further pagination axis available
		truncate.NewTruncator(500),
		store,
	)
	assert.False(t, wasTruncated, "single-record path must NOT recurse")
	assert.Equal(t, body, out, "single-record body must pass through unchanged")

	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Empty(t, store.calls, "no Store call should happen for the single-record short-circuit")
}

// TestMaybeTruncateAndCacheText_NoOpUnderLimit asserts the helper is a no-op
// when the input is already within the limit — no cache churn, no banner
// pollution, no wrapper allocation.
func TestMaybeTruncateAndCacheText_NoOpUnderLimit(t *testing.T) {
	body := `{"records":[{"id":1}],"meta":{"total":1}}`
	store := &captureStore{}

	out, wasTruncated := maybeTruncateAndCacheText(
		body,
		"read_cache",
		nil,
		1,
		truncate.NewTruncator(10_000),
		store,
	)
	assert.False(t, wasTruncated)
	assert.Equal(t, body, out)

	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Empty(t, store.calls)
}

// TestMaybeTruncateAndCacheText_RecursiveRoundTripViaRealCacheManager is the
// hardening contract spelled out: a giant read_cache response gets truncated,
// the full body is stored under a fresh key K2, and a follow-up call against
// K2 returns the same records sliced at the requested offset/limit. The
// recursion is bounded (each level uses a new key) and idempotent.
func TestMaybeTruncateAndCacheText_RecursiveRoundTripViaRealCacheManager(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	db, err := bbolt.Open(dbPath, 0o600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mgr, err := cache.NewManager(db, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(mgr.Close)

	// Build a read-cache-shaped response with 40 records.
	type rec struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}
	recs := make([]rec, 40)
	for i := range recs {
		recs[i] = rec{ID: i, Body: strings.Repeat("x", 60)}
	}
	wrapper := map[string]interface{}{
		"records": recs,
		"meta":    map[string]interface{}{"total": len(recs)},
	}
	jsonBytes, err := json.Marshal(wrapper)
	require.NoError(t, err)

	out, wasTruncated := maybeTruncateAndCacheText(
		string(jsonBytes),
		"read_cache",
		map[string]interface{}{"key": "ORIG", "offset": 0, "limit": 40},
		len(recs),
		truncate.NewTruncator(800),
		mgr,
	)
	require.True(t, wasTruncated)

	// Pull the new cache key out of the truncation banner.
	idx := strings.Index(out, `key="`)
	require.NotEqual(t, -1, idx)
	keyStart := idx + len(`key="`)
	keyEnd := strings.Index(out[keyStart:], `"`)
	require.NotEqual(t, -1, keyEnd)
	newKey := out[keyStart : keyStart+keyEnd]
	require.NotEmpty(t, newKey)

	// Walk the new key page by page and verify each requested slice resolves
	// to the same records the truncated response was hiding.
	page, err := mgr.GetRecords(newKey, 0, 5)
	require.NoError(t, err, "follow-up read_cache against the recursively-cached key must succeed")
	require.Equal(t, 40, page.Meta.TotalRecords)
	require.Len(t, page.Records, 5)

	idOf := func(r interface{}) float64 {
		m, _ := r.(map[string]interface{})
		v, _ := m["id"].(float64)
		return v
	}
	for i, r := range page.Records {
		assert.Equal(t, float64(i), idOf(r), "page 1 must hand back ids 0..4 in order")
	}
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
