package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 135 FR-019 / research R1a: on the legacy-pinned client-facing surface a
// server/discover probe at an unserved modern version gets -32601, as from a
// legacy-only server; every other request reaches mcp-go untouched.
func TestClientFacingEraGuard(t *testing.T) {
	var reached []string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reached = append(reached, string(b))
		w.WriteHeader(http.StatusTeapot)
	})
	h := clientFacingEraGuard(next)
	send := func(version, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		if version != "" {
			req.Header.Set(mcp.HeaderProtocolVersion, version)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	modern := mcp.ProtocolVersion20260728
	discover := `{"jsonrpc":"2.0","id":7,"method":"server/discover","params":{}}`

	w := send(modern, `{"jsonrpc":"2.0","id":1e400,"method":"server/discover"}`)
	assert.Contains(t, w.Body.String(), `"id":1e400`, "a large numeric id is still an id, echoed as sent")
	assert.Contains(t, w.Body.String(), `-32601`)

	w = send(modern, `{"jsonrpc":"2\u002e0","id":2,"method":"server\u002fdiscover"}`)
	assert.Contains(t, w.Body.String(), `-32601`, "escaped spellings are the same probe")

	w = send(modern, "{\"jsonrpc\":\"2.0\",\"id\":3,\"\u0131d\":4,\"method\":\"server/discover\"}")
	assert.Contains(t, w.Body.String(), `-32601`, "dotless i does not fold with i, so this is not ambiguous")

	w = send(modern, discover)
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"jsonrpc":"2.0","id":7,"error":{"code":-32601,"message":"Method not found: server/discover"}}`, w.Body.String())
	assert.Empty(t, reached, "the probe is answered here")

	for _, tc := range []struct{ version, body string }{
		{mcp.LATEST_LEGACY_PROTOCOL_VERSION, discover}, // a served version
		{"", discover}, // no header
		{modern, `{"jsonrpc":"2.0","id":8,"method":"tools/list","params":{}}`}, // not the probe
		{modern, `{"jsonrpc":"2.0","method":"server/discover"}`},               // a notification
		{modern, `[{"jsonrpc":"2.0","id":9,"method":"server/discover"}]`},      // a batch
		{modern, `not json`},
		{modern, `{"id":10,"method":"server/discover"}`},                                                   // no jsonrpc member
		{modern, `{"jsonrpc":"2.0","id":{"x":1},"method":"server/discover"}`},                              // object id
		{modern, `{"jsonrpc":"2.0","id":1,"method":"tools/list","method":"server/discover"}`},              // repeated member
		{modern, `{"jsonrpc":"2.0","id":1,"Method":"tools/list","method":"server/discover"}`},              // case variant
		{modern, `{"jsonrpc":"2.0","id":1,"method":"server/discover"} {}`},                                 // trailing data
		{modern, "{\"j\u017fonrpc\":\"1.0\",\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"server/discover\"}"}, // folds like encoding/json // garbage
	} {
		reached = nil
		w := send(tc.version, tc.body)
		assert.Equal(t, http.StatusTeapot, w.Code, "%s %s", tc.version, tc.body)
		require.Len(t, reached, 1)
		assert.Equal(t, tc.body, reached[0], "the body reaches mcp-go byte for byte")
	}

	reached = nil
	big := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"x":"` + strings.Repeat("a", maxDiscoverProbeBytes) + `"}}`
	w = send(modern, big)
	assert.Equal(t, http.StatusTeapot, w.Code)
	require.Len(t, reached, 1)
	assert.Equal(t, big, reached[0], "an oversized body is passed on whole")

	reached = nil
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(mcp.HeaderProtocolVersion, modern)
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Len(t, reached, 1, "GET is untouched")
}

type failAfter struct {
	data []byte
	done bool
}

func (f *failAfter) Read(p []byte) (int, error) {
	if !f.done {
		f.done = true
		return copy(p, f.data), nil
	}
	return 0, io.ErrUnexpectedEOF
}

// A body whose read fails reaches mcp-go with the same bytes and the same
// error, not a clean EOF.
func TestClientFacingEraGuard_ReadErrorPassedOn(t *testing.T) {
	var got error
	h := clientFacingEraGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, got = io.ReadAll(r.Body)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", &failAfter{data: []byte(`{"jsonrpc":"2.0"`)})
	req.Header.Set(mcp.HeaderProtocolVersion, mcp.ProtocolVersion20260728)
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.ErrorIs(t, got, io.ErrUnexpectedEOF)
}

// A repeated member anywhere (here inside params) is mcp-go's to refuse.
func TestClientFacingEraGuard_NestedDuplicatePassedOn(t *testing.T) {
	reached := false
	h := clientFacingEraGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{},"_meta":{}}}`))
	req.Header.Set(mcp.HeaderProtocolVersion, mcp.ProtocolVersion20260728)
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.True(t, reached)
	assert.False(t, hasAmbiguousMembers([]byte(`{"a":{"b":1,"c":[{"b":2},{"b":3}]},"d":[1,{"e":{}}]}`)), "same names in sibling objects are fine")
	assert.True(t, hasAmbiguousMembers([]byte(`{"a":[{"x":1,"X":2}]}`)))
}
