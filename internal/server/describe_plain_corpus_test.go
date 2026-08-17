package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Spec 099 FR-011 / SC-002 — plain-mode byte-identity with ONE enumerated delta.
//
// `describe_tool` without `check` sits on the hot path of the compact router:
// it is how an agent recovers a full schema after a lossy signature, so a
// regression here degrades every compact-mode session. This replays a fixed
// corpus of plain-mode calls and compares each response BYTE FOR BYTE against a
// capture taken from the pre-099 handler.
//
// Exactly one difference is permitted, and it is named rather than tolerated:
// an out-of-scope id's per-id `error` moves from the retired `invisible` to
// `not_found` (the `remediation` was already the shared not-found string, so
// nothing else on the entry moves). Any second difference — a reordered key, a
// reworded remediation, a changed cap message — fails.
//
// The golden was captured with this exact file copied into a throwaway
// `git worktree` of the pre-099 commit and run with
// MCPPROXY_WRITE_DESCRIBE_PLAIN_CORPUS set, so the capture and the comparison
// share one serializer and cannot drift.

const (
	describePlainCorpusGolden   = "testdata/describe_plain_corpus/pre099.json"
	describePlainCorpusWriteEnv = "MCPPROXY_WRITE_DESCRIBE_PLAIN_CORPUS"
)

// describePlainDelta lists the corpus scenarios spec 099 is allowed to change,
// with the exact substitution that must account for the whole difference.
// A MISCASED out-of-scope id is deliberately absent: it never reached the scope
// gate — an id that is not in the index at all resolves to not_found first — so
// it answered not_found before this change too, and must still.
var describePlainDelta = map[string]struct{ from, to string }{
	"out_of_scope_id":             {`"error":"invisible"`, `"error":"not_found"`},
	"out_of_scope_id_among_valid": {`"error":"invisible"`, `"error":"not_found"`},
}

// scopedSessionContext is the agent session the scope cases are observed under:
// scoped to github + quarry, so gitlab is out of scope.
func scopedSessionContext() context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "corpus-bot",
		AllowedServers: []string{"github", "quarry"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
	})
}

// describePlainCorpus is the replay corpus: every plain-mode shape an existing
// integration could depend on — resolved definitions, each per-id error code,
// the did-you-mean paths, duplicates, whitespace, and both request errors.
func describePlainCorpus() []struct {
	name  string
	agent bool
	args  map[string]interface{}
} {
	return []struct {
		name  string
		agent bool
		args  map[string]interface{}
	}{
		{"single_definition", false, map[string]interface{}{"tool_ids": []interface{}{"github:visible_tool"}}},
		{"two_definitions", false, map[string]interface{}{"tool_ids": []interface{}{"github:visible_tool", "quarry:lingering_tool"}}},
		{"unknown_id", false, map[string]interface{}{"tool_ids": []interface{}{"github:no_such_tool"}}},
		{"malformed_id", false, map[string]interface{}{"tool_ids": []interface{}{"not-an-id"}}},
		{"case_mismatch_did_you_mean", false, map[string]interface{}{"tool_ids": []interface{}{"GITHUB:visible_tool"}}},
		{"quarantined_id", true, map[string]interface{}{"tool_ids": []interface{}{"quarry:lingering_tool"}}},
		{"pending_id", true, map[string]interface{}{"tool_ids": []interface{}{"github:pending_tool"}}},
		{"changed_id", true, map[string]interface{}{"tool_ids": []interface{}{"github:changed_tool"}}},
		{"disabled_id", true, map[string]interface{}{"tool_ids": []interface{}{"github:disabled_tool"}}},
		{"out_of_scope_id", true, map[string]interface{}{"tool_ids": []interface{}{"gitlab:scoped_tool"}}},
		{"out_of_scope_id_among_valid", true, map[string]interface{}{"tool_ids": []interface{}{"github:visible_tool", "gitlab:scoped_tool"}}},
		{"out_of_scope_case_mismatch_id", true, map[string]interface{}{"tool_ids": []interface{}{"GITLAB:scoped_tool"}}},
		{"duplicate_ids", false, map[string]interface{}{"tool_ids": []interface{}{"github:visible_tool", "github:visible_tool"}}},
		{"padded_ids", false, map[string]interface{}{"tool_ids": []interface{}{" github:visible_tool ", "github: visible_tool"}}},
		{"mixed_valid_and_errors", true, map[string]interface{}{"tool_ids": []interface{}{"github:visible_tool", "github:pending_tool", "nope"}}},
		{"empty_ids", false, map[string]interface{}{"tool_ids": []interface{}{}}},
		{"missing_ids", false, map[string]interface{}{}},
		{"over_cap_six_ids", false, map[string]interface{}{"tool_ids": []interface{}{
			"github:visible_tool", "github:pending_tool", "github:changed_tool",
			"github:disabled_tool", "quarry:lingering_tool", "github:no_such_tool",
		}}},
	}
}

// captureDescribePlainCorpus replays the corpus and returns scenario -> response
// text (the error text for a tool-error result, prefixed so the two shapes can
// never be confused).
func captureDescribePlainCorpus(t *testing.T) map[string]string {
	t.Helper()

	proxy := createTestMCPProxyServer(t)
	seedVisibilityFixture(t, proxy)

	captured := make(map[string]string)
	for _, entry := range describePlainCorpus() {
		ctx := context.Background()
		if entry.agent {
			ctx = scopedSessionContext()
		}
		req := mcp.CallToolRequest{}
		req.Params.Arguments = entry.args

		result, err := proxy.handleDescribeTool(ctx, req)
		require.NoErrorf(t, err, "scenario %s", entry.name)
		require.NotNilf(t, result, "scenario %s", entry.name)
		require.NotEmptyf(t, result.Content, "scenario %s", entry.name)

		text := result.Content[0].(mcp.TextContent).Text
		if result.IsError {
			text = "ERROR: " + text
		}
		captured[entry.name] = text
	}
	return captured
}

func TestDescribeToolPlainCorpus_ByteIdenticalWithOneEnumeratedDelta(t *testing.T) {
	captured := captureDescribePlainCorpus(t)

	if outPath := os.Getenv(describePlainCorpusWriteEnv); outPath != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(outPath), 0o755))
		raw, err := json.MarshalIndent(captured, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(outPath, append(raw, '\n'), 0o644))
		t.Skipf("corpus written to %s (%s set); comparison skipped", outPath, describePlainCorpusWriteEnv)
	}

	raw, err := os.ReadFile(describePlainCorpusGolden)
	require.NoError(t, err, "missing pre-099 corpus capture")
	var want map[string]string
	require.NoError(t, json.Unmarshal(raw, &want))
	require.NotEmpty(t, want)

	// The corpus itself must not shrink: dropping a scenario would make
	// "byte-identical" true by omission.
	assert.Equal(t, sortedKeys(want), sortedKeys(captured), "the replay corpus must not gain or lose scenarios")

	var changed []string
	for name, wantText := range want {
		gotText, ok := captured[name]
		if !ok {
			continue // already reported by the key comparison
		}
		if gotText == wantText {
			continue
		}
		changed = append(changed, name)

		delta, allowed := describePlainDelta[name]
		if !assert.Truef(t, allowed, "scenario %s changed, and spec 099 permits no delta there:\nwant %s\ngot  %s", name, wantText, gotText) {
			continue
		}
		assert.Equalf(t, strings.ReplaceAll(wantText, delta.from, delta.to), gotText,
			"scenario %s may differ ONLY by %s -> %s; anything else is a second, unenumerated delta", name, delta.from, delta.to)
		assert.Containsf(t, wantText, delta.from, "scenario %s: the pre-099 capture must actually contain the retired code", name)
	}

	sort.Strings(changed)
	assert.Equal(t, sortedKeys(describePlainDelta), changed,
		"the enumerated delta must be exactly the out-of-scope scenarios — no more (a regression) and no fewer (a stale enumeration)")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
