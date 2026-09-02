package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// read_cache is the truncating emitter the typed-stamp conversion missed.
//
// It applies the same truncate-and-cache contract as retrieve_tools — a
// paginated page that overflows tool_response_limit is cut and re-cached — and
// then hands the agent the CUT text while recording the FULL page. That is
// contracts.CutShortenedAgentOnly, the same direction every other built-in
// carries. It was emitting through the whole-response wrapper, which passes
// contracts.CutNone, so a genuine cut was persisted as no cut at all: a record
// holding more text than the agent ever received, claiming to hold exactly it.
//
// Why that is not cosmetic: the Spec 103 replay loader recomputes what a
// workload cost by tokenizing stored responses. An unflagged over-long record
// is indistinguishable from a complete one, so the loader tokenizes text the
// agent never paid for and OVERSTATES what mcpproxy cost — the one direction of
// error the benchmark exists to prevent, and one that cannot be detected after
// the fact. The mirror-image failure (flagging an untruncated page) would make
// the loader discard every read_cache row instead, so the second test pins that
// the stamp still means something.
//
// The proxy is wired to a REAL Runtime with its ActivityService running, for the
// same reason TestRetrieveTools_TruncatedResponseIsFlaggedOnTheActivityRecord
// is: what matters is the record an operator would later export, and only the
// real service builds that record.

// seedCacheEntry stores a paginable JSON payload under key and returns the
// number of records in it. The records are deliberately fat and numerous so the
// serialized read_cache page overflows a small tool_response_limit, and so
// paginableUnits (len(response.Records)) is well above 1 — below that,
// maybeTruncateAndCacheText has nothing to subdivide and passes the text
// through uncut, which would make the assertion below vacuous.
func seedCacheEntry(t *testing.T, p *MCPProxyServer, key string, count int) {
	t.Helper()

	records := make([]map[string]interface{}, 0, count)
	for i := 0; i < count; i++ {
		records = append(records, map[string]interface{}{
			"id":    i,
			"title": fmt.Sprintf("record %d", i),
			"body":  fmt.Sprintf("a body long enough that a page of these cannot fit in a small response limit (%d)", i),
		})
	}
	content, err := json.Marshal(map[string]interface{}{"records": records})
	require.NoError(t, err)

	require.NoError(t, p.cacheManager.Store(key, "github:list_issues", nil, string(content), "records", count))
}

// awaitInternalToolCallActivity polls until the named internal_tool_call record
// lands. Emission publishes onto the event bus synchronously but the activity
// service drains it on its own goroutine, so a bare read after the handler
// returns is a race.
func awaitInternalToolCallActivity(t *testing.T, sm *storage.Manager, toolName string) *storage.ActivityRecord {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		records, _, err := sm.ListActivities(storage.ActivityFilter{Limit: 50})
		require.NoError(t, err)
		for _, rec := range records {
			if rec.Type == storage.ActivityTypeInternalToolCall && rec.ToolName == toolName {
				return rec
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s activity record was persisted within 5s", toolName)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReadCache_TruncatedResponseIsFlaggedOnTheActivityRecord(t *testing.T) {
	// Small enough that a 40-record page cannot fit, large enough that the
	// truncator's own banner still has room.
	proxy, rt := newTruncatingRetrieveToolsProxy(t, 600)
	seedCacheEntry(t, proxy, "cache-key-1", 40)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"key": "cache-key-1", "offset": float64(0), "limit": float64(40),
	}
	result, err := proxy.handleReadCache(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "the fixture must produce a real read_cache page, not an error")

	rec := awaitInternalToolCallActivity(t, rt.StorageManager(), "read_cache")

	// Premise check. If the page were NOT actually cut, everything below would
	// be asserting nothing — this test's value is that it fails when the stamp
	// is missing, not when the fixture stops truncating.
	require.Less(t, len(resultText(t, result)), len(rec.Response),
		"fixture must actually truncate: the agent's text has to be shorter than the stored full page")

	assert.True(t, rec.ResponseTruncated,
		"a cut read_cache page must be flagged on the activity record; without the flag a "+
			"cost recomputation tokenizes the stored FULL page and overstates agent cost")
	assert.Equal(t, contracts.CutShortenedAgentOnly, rec.ResponseTruncationCut,
		"the flag alone says nothing about direction. This record holds MORE than was "+
			"delivered, and only the emitter knows that — read_cache is an internal_tool_call "+
			"like retrieve_tools, and a consumer reading the TYPE cannot tell them from a "+
			"code-execution sub-call, which is flagged too and points the other way")

	// The whole point of the stamp, exercised end to end: the resolved sentence
	// has to say the agent got LESS, which is what makes a cost recomputation
	// exclude the row instead of overstating mcpproxy's cost.
	resolved := contracts.ResolveResponseTruncation(
		rec.ResponseTruncationCut, rec.ResponseTruncated, rec.ResponseStorageTruncated)
	assert.Equal(t, contracts.StoredLargerThanDelivered, resolved.Relation)
	assert.Contains(t, resolved.Notice, "LESS")
}

func TestReadCache_UntruncatedResponseIsNotFlagged(t *testing.T) {
	proxy, rt := newTruncatingRetrieveToolsProxy(t, 0) // 0 = unlimited
	seedCacheEntry(t, proxy, "cache-key-2", 3)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"key": "cache-key-2", "offset": float64(0), "limit": float64(50),
	}
	result, err := proxy.handleReadCache(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	rec := awaitInternalToolCallActivity(t, rt.StorageManager(), "read_cache")
	assert.False(t, rec.ResponseTruncated,
		"a complete page must not be flagged truncated, or a loader excludes every read_cache row")
	assert.Equal(t, contracts.CutNone, rec.ResponseTruncationCut,
		"no cut, no stamp: a blanket stamp would be as wrong as a blanket direction")
}
