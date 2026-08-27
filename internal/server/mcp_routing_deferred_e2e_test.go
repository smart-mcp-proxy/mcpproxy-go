//go:build !windows

package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 102 T033 — FR-015 byte-stability of the DEFAULT (deferral-off) direct
// listing.
//
// FORM USED: the LIVE stdio fixture, not the same-tree differential. This file
// compiles and runs unchanged at the merge-base, which is what makes the golden
// a genuine pre-feature capture rather than a restatement of the code under
// test: `testdata/direct_full_prefeature.golden.json` was produced by running
// THIS test against a merge-base binary with
// MCPPROXY_WRITE_DIRECT_PREFEATURE_GOLDEN set. A same-tree differential could
// only ever compare the branch against itself.
//
// It reuses the Spec-098 preflight harness (isolated data dir + config, high
// port, node fixture, never touches ~/.mcpproxy) and drives raw JSON-RPC rather
// than an mcp-go client, because the assertion is on BYTES: decoding into
// mcp.Tool and re-marshalling would launder exactly the differences this test
// exists to catch.
//
// KNOWN LIMIT: every tool the shared fixture declares has an empty input schema,
// so this gate pins the envelope (names, descriptions, annotations, the
// "properties":{},"required":[] full-mode schema shape) but not a populated
// schema. Schema-bearing full-mode rendering is covered by
// TestDirectModes_SetIdentity in mcp_routing_deferred_test.go, which asserts the
// upstream properties and outputSchema survive in full mode. Widening the
// fixture would change the tool set the Spec-098 preflight tests assert on.

const directPrefeatureGoldenEnv = "MCPPROXY_WRITE_DIRECT_PREFEATURE_GOLDEN"

const directPrefeatureGoldenPath = "testdata/direct_full_prefeature.golden.json"

// directSurfaceBuiltins are the tools mcpproxy serves itself on the direct
// surface. They are the FR-010 enumerated delta against the pre-feature
// baseline and are excluded from the byte comparison — their presence is
// asserted separately below, so excluding them cannot hide their removal.
var directSurfaceBuiltins = []string{"describe_tool"}

// mcpJSONRPC posts one JSON-RPC message to an MCP route and returns the decoded
// envelope. Streamable HTTP may answer either as plain JSON or as a single SSE
// event, so both framings are accepted.
func mcpJSONRPC(t *testing.T, e *preflightE2E, route, sessionID, body string) (map[string]json.RawMessage, string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+route, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("X-API-Key", preflightE2EAPIKey)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, err := readMCPPayload(resp)
	require.NoErrorf(t, err, "read %s response", route)
	require.Lessf(t, resp.StatusCode, 300, "%s %s -> %d: %s", route, body, resp.StatusCode, raw)

	if len(bytes.TrimSpace(raw)) == 0 {
		// notifications/initialized answers 202 with no body.
		return nil, resp.Header.Get("Mcp-Session-Id")
	}

	var envelope map[string]json.RawMessage
	require.NoErrorf(t, json.Unmarshal(raw, &envelope), "decode %s response: %s", route, raw)
	require.NotContainsf(t, envelope, "error", "%s returned an error: %s", route, raw)

	return envelope, resp.Header.Get("Mcp-Session-Id")
}

// readMCPPayload unwraps a single SSE event, or returns the body verbatim when
// the response is plain JSON.
func readMCPPayload(resp *http.Response) ([]byte, error) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		var buf bytes.Buffer
		for scanner.Scan() {
			buf.Write(scanner.Bytes())
		}
		return buf.Bytes(), scanner.Err()
	}

	var payload bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "data:"); ok {
			payload.WriteString(strings.TrimPrefix(after, " "))
		}
	}
	return payload.Bytes(), scanner.Err()
}

// directToolsListEntries returns the direct surface's tools/list entries as raw
// JSON, keyed by tool name.
func directToolsListEntries(t *testing.T, e *preflightE2E) map[string]json.RawMessage {
	t.Helper()

	const route = "/mcp/all"

	_, sessionID := mcpJSONRPC(t, e, route, "",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26",`+
			`"capabilities":{},"clientInfo":{"name":"spec102-fr015","version":"0"}}}`)
	require.NotEmpty(t, sessionID, "streamable HTTP must return a session id")

	mcpJSONRPC(t, e, route, sessionID, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	envelope, _ := mcpJSONRPC(t, e, route, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)

	var result struct {
		Tools []json.RawMessage `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(envelope["result"], &result))

	entries := make(map[string]json.RawMessage, len(result.Tools))
	for _, raw := range result.Tools {
		var named struct {
			Name string `json:"name"`
		}
		require.NoError(t, json.Unmarshal(raw, &named))
		entries[named.Name] = raw
	}
	return entries
}

// stripCR normalizes CRLF so the golden compares equal on Windows runners.
// Deliberately local rather than borrowing the toolslist gate's helper: this
// file must compile unchanged at the merge-base.
func stripCR(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func renderDirectGolden(t *testing.T, entries map[string]json.RawMessage) []byte {
	t.Helper()
	// Sorted by name: tools/list order follows the catalog's sorted display
	// names, but a map does not, and a golden must not be iteration-order flaky.
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	ordered := make(map[string]json.RawMessage, len(entries))
	for _, name := range names {
		ordered[name] = entries[name]
	}
	out, err := json.MarshalIndent(ordered, "", "  ")
	require.NoError(t, err)
	return append(out, '\n')
}

// TestDirectFullMode_ByteStableAgainstPreFeatureE2E is the FR-015 gate.
func TestDirectFullMode_ByteStableAgainstPreFeatureE2E(t *testing.T) {
	env := newPreflightE2E(t)
	env.start()

	// Direct mode is served at /mcp/all regardless of routing_mode, and the
	// fixture config sets none — so this is the default (deferral-off) path.
	entries := directToolsListEntries(t, env)
	require.NotEmpty(t, entries, "the fixture must produce a non-empty direct listing")

	if dir := os.Getenv(directPrefeatureGoldenEnv); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		// Capture EVERYTHING, built-ins included: at the merge-base there are
		// none, and on the branch this env var is only ever set deliberately.
		out := filepath.Join(dir, "direct_full_prefeature.golden.json")
		require.NoError(t, os.WriteFile(out, renderDirectGolden(t, entries), 0o600))
		t.Logf("wrote %s (%d entries)", out, len(entries))
		t.Skip("golden written; comparison skipped")
	}

	for _, builtin := range directSurfaceBuiltins {
		assert.Containsf(t, entries, builtin,
			"%q is the FR-010 enumerated delta and must be present, not merely excluded below", builtin)
		delete(entries, builtin)
	}

	want, err := os.ReadFile(directPrefeatureGoldenPath)
	require.NoErrorf(t, err, "missing pre-feature golden; regenerate it at the merge-base with %s=<dir>", directPrefeatureGoldenEnv)

	got := renderDirectGolden(t, entries)
	assert.Equal(t, stripCR(string(want)), stripCR(string(got)),
		"deferral-off direct listings must be byte-identical to pre-feature output modulo the built-in delta (FR-015)")
}
