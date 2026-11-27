package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 PR-5 review follow-up: the native probe gate against a bare mcp-go
// transport that serves the modern protocol (mcpproxy pins it off today, so
// these checks are not reachable through the real endpoint yet). The gate's
// answer for every refusal must be the bare transport's own answer to the
// same request, byte for byte, and every read it does not claim must reach
// the transport unchanged.

const gateOrdinaryURI = "file:///ordinary.txt"

// gateFixture is a bare SDK instance with the native options, one tool, one
// ordinary resource and one ordinary template, served with and without the
// gate.
type gateFixture struct {
	bare     http.Handler
	gated    http.Handler
	answered atomic.Int32
	hooks    atomic.Int32 // OnBeforeAny calls: a request the SDK refused early runs none
}

func newGateFixture(t *testing.T, known func(string) bool) *gateFixture {
	t.Helper()
	f := &gateFixture{}
	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeAny(func(context.Context, any, mcp.MCPMethod, any) { f.hooks.Add(1) })
	srv := mcpserver.NewMCPServer("gate-test", "1", append(nativeSkillsServerOptions(), mcpserver.WithHooks(hooks))...)
	srv.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	srv.AddResource(mcp.NewResource(gateOrdinaryURI, "ordinary"), func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{mcp.TextResourceContents{URI: gateOrdinaryURI, Text: "ORDINARY"}}, nil
	})
	srv.AddResourceTemplate(mcp.NewResourceTemplate("ui://widget/{id}", "widget"), func(_ context.Context, r mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{mcp.TextResourceContents{URI: r.Params.URI, Text: "WIDGET"}}, nil
	})
	f.bare = mcpserver.NewStreamableHTTPServer(srv, mcpserver.WithStateLess(true))
	claims := func(req *nativeSkillsRequest) bool { return nativeClaims(req, known) }
	f.gated = nativeProbeGate(f.bare, claims, func(w http.ResponseWriter, _ *http.Request, req *nativeSkillsRequest) {
		f.answered.Add(1)
		writeNativeAnswer(w, req.ID, true, nativeAnswer{result: map[string]any{"native": true}})
	})
	return f
}

type gateReply struct {
	status int
	body   string
	err    *rawRPCError
}

func gateDo(t *testing.T, h http.Handler, method string, params map[string]any, headers map[string]string) gateReply {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": method, "params": params})
	require.NoError(t, err)
	return gateDoRaw(t, h, string(body), headers)
}

func gateDoRaw(t *testing.T, h http.Handler, body string, headers map[string]string) gateReply {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp/s/x", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	raw, err := io.ReadAll(rec.Result().Body)
	require.NoError(t, err)
	out := gateReply{status: rec.Code, body: string(raw)}
	var msg struct {
		Error *rawRPCError `json:"error"`
	}
	if json.Unmarshal(raw, &msg) == nil {
		out.err = msg.Error
	}
	return out
}

// modernParams adds the modern-era _meta to params.
func modernParams(params map[string]any) map[string]any {
	out := map[string]any{"_meta": map[string]any{
		mcp.MetaKeyProtocolVersion:    mcp.ProtocolVersion20260728,
		mcp.MetaKeyClientCapabilities: map[string]any{},
	}}
	for k, v := range params {
		out[k] = v
	}
	return out
}

// modernHeaders are the headers a correct modern client sends for method and
// params (the SDK's own StandardHeaders), with overrides ("" deletes).
func modernHeaders(t *testing.T, method string, params map[string]any, override map[string]string) map[string]string {
	t.Helper()
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	h := map[string]string{mcp.HeaderProtocolVersion: mcp.ProtocolVersion20260728}
	for k, v := range mcp.StandardHeaders(mcp.ProtocolVersion20260728, mcp.MCPMethod(method), raw) {
		h[k] = v
	}
	for k, v := range override {
		h[k] = v
	}
	return h
}

// The original Mcp-Method and Mcp-Name are validated against the original
// method and params, and the refusal is the SDK's own, unchanged.
func TestNativeProbeGate_StandardHeadersMatchTheSDK(t *testing.T) {
	f := newGateFixture(t, nil)
	const skillURI = "skill://pdf/SKILL.md"
	methods := map[string]map[string]any{
		nativeMethodList: {},
		nativeMethodGet:  {"uri": skillURI},
		nativeMethodRead: {"uri": skillURI},
	}
	cases := []struct {
		name     string
		override map[string]string
		only     string // method the case applies to; "" = all
	}{
		{"Mcp-Method names another method", map[string]string{mcp.HeaderMethod: "tools/list"}, ""},
		{"Mcp-Method names ping", map[string]string{mcp.HeaderMethod: string(mcp.MethodPing)}, ""},
		{"Mcp-Method missing", map[string]string{mcp.HeaderMethod: ""}, ""},
		{"Mcp-Name names another resource", map[string]string{mcp.HeaderName: "skill://other/SKILL.md"}, nativeMethodRead},
		{"Mcp-Name missing", map[string]string{mcp.HeaderName: ""}, nativeMethodRead},
		{"Mcp-Name not valid base64", map[string]string{mcp.HeaderName: "=?base64?***?="}, nativeMethodRead},
	}
	for _, tc := range cases {
		for method, p := range methods {
			if tc.only != "" && tc.only != method {
				continue
			}
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				params := modernParams(p)
				h := modernHeaders(t, method, params, tc.override)
				want := gateDo(t, f.bare, method, params, h)
				require.NotNil(t, want.err, "the SDK refuses: %s", want.body)
				require.Equal(t, mcp.HEADER_MISMATCH, want.err.Code, want.body)

				before, hooksBefore := f.answered.Load(), f.hooks.Load()
				got := gateDo(t, f.gated, method, params, h)
				assert.Equal(t, before, f.answered.Load(), "a header mismatch is never answered natively")
				assert.Equal(t, hooksBefore, f.hooks.Load(), "refused before any hook runs, as the SDK refuses it")
				assert.Equal(t, want.status, got.status, "status like the SDK: %s", got.body)
				assert.Equal(t, want.body, got.body, "the SDK's refusal, unchanged")
				assert.NotContains(t, got.body, `"ping"`, "the probe's method never shows")
				assert.NotContains(t, got.body, string(mcp.MethodServerDiscover), "the probe's method never shows")
			})
		}
	}

	// Agreeing headers pass.
	for method, p := range methods {
		params := modernParams(p)
		before := f.answered.Load()
		got := gateDo(t, f.gated, method, params, modernHeaders(t, method, params, nil))
		assert.Equal(t, http.StatusOK, got.status, "%s: %s", method, got.body)
		assert.Nil(t, got.err, got.body)
		assert.Equal(t, before+1, f.answered.Load(), "%s is answered natively", method)
	}
}

// The transport's earlier checks still come first: a request refused for its
// protocol version is refused for that, even when its headers also disagree.
func TestNativeProbeGate_TransportChecksComeBeforeHeaders(t *testing.T) {
	f := newGateFixture(t, nil)
	params := modernParams(map[string]any{"uri": "skill://pdf/SKILL.md"})
	h := modernHeaders(t, nativeMethodRead, params, map[string]string{
		mcp.HeaderProtocolVersion: "2025-06-18", // disagrees with _meta
		mcp.HeaderMethod:          "tools/list",
	})
	want := gateDo(t, f.bare, nativeMethodRead, params, h)
	got := gateDo(t, f.gated, nativeMethodRead, params, h)
	require.NotNil(t, want.err, want.body)
	assert.Equal(t, want.status, got.status)
	assert.Equal(t, want.body, got.body)
	assert.Zero(t, f.answered.Load())
}

// Ordinary resource reads on the same endpoint are the transport's: a
// registered resource, a template match and an unknown non-skill address all
// get exactly the answer the endpoint gives without the native surface.
func TestNativeProbeGate_OrdinaryResourceReadsStayTheSDKs(t *testing.T) {
	const otherSchemeSkill = "file:///skills/pdf/SKILL.md"
	f := newGateFixture(t, func(uri string) bool { return uri == otherSchemeSkill })
	for _, uri := range []string{gateOrdinaryURI, "ui://widget/42", "https://example.com/not-a-skill"} {
		t.Run(uri, func(t *testing.T) {
			params := modernParams(map[string]any{"uri": uri})
			h := modernHeaders(t, nativeMethodRead, params, nil)
			want := gateDo(t, f.bare, nativeMethodRead, params, h)
			got := gateDo(t, f.gated, nativeMethodRead, params, h)
			assert.Equal(t, want.status, got.status)
			assert.Equal(t, want.body, got.body, "the transport's own answer")
			assert.Zero(t, f.answered.Load(), "never answered natively")
		})
	}
	// An address a skill of this server lists under another scheme is still
	// the native surface's.
	params := modernParams(map[string]any{"uri": otherSchemeSkill})
	got := gateDo(t, f.gated, nativeMethodRead, params, modernHeaders(t, nativeMethodRead, params, nil))
	assert.Nil(t, got.err, got.body)
	assert.Equal(t, int32(1), f.answered.Load())
}

// Requests the SDK would refuse before or while parsing them keep the SDK's
// refusal: nothing the interceptor reads differently from the SDK can turn a
// refused request into a native answer.
func TestNativeProbeGate_RequestsTheSDKRefusesStayRefused(t *testing.T) {
	f := newGateFixture(t, nil)
	modernMeta := `"_meta":{"` + mcp.MetaKeyProtocolVersion + `":"` + mcp.ProtocolVersion20260728 + `","` + mcp.MetaKeyClientCapabilities + `":{}}`
	cases := []struct {
		name, body string
		headers    map[string]string
	}{
		// The SDK merges repeated "params" objects, so it sees the modern
		// _meta of the first and refuses the request for the missing
		// Mcp-Protocol-Version header; a last-wins read would see a legacy
		// request and serve it.
		{"repeated params hides the modern era", `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{` + modernMeta + `},"params":{"uri":"skill://pdf/SKILL.md"}}`, nil},
		{"repeated params on skills/get", `{"jsonrpc":"2.0","id":7,"method":"skills/get","params":{` + modernMeta + `},"params":{"uri":"skill://pdf/SKILL.md"}}`, nil},
		{"repeated _meta, malformed first", `{"jsonrpc":"2.0","id":7,"method":"skills/list","params":{"_meta":[],` + modernMeta + `}}`, nil},
		{"repeated uri in params", `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"skill://pdf/SKILL.md","URI":5}}`, nil},
		{"repeated method", `{"jsonrpc":"2.0","id":7,"method":"tools/list","method":"skills/list","params":{}}`, nil},
		// encoding/json matches member names case-insensitively: the SDK
		// reads the last "JSONRPC" and refuses the version.
		{"case-variant jsonrpc", `{"jsonrpc":"2.0","JSONRPC":"1.0","id":7,"method":"resources/read","params":{"uri":"skill://pdf/SKILL.md"}}`, nil},
		{"case-variant params", `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{` + modernMeta + `},"Params":{"uri":"skill://pdf/SKILL.md"}}`, nil},
		// ReadResourceParams.Arguments must be an object.
		{"legacy read with non-object arguments", `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"skill://pdf/SKILL.md","arguments":5}}`, nil},
		{"modern read with non-object arguments", `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{` + modernMeta + `,"uri":"skill://pdf/SKILL.md","arguments":5}}`,
			map[string]string{mcp.HeaderProtocolVersion: mcp.ProtocolVersion20260728, mcp.HeaderMethod: nativeMethodRead, mcp.HeaderName: "skill://pdf/SKILL.md"}},
		{"read with array params", `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":[]}`, nil},
		{"read with uri of the wrong type", `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":5}}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := gateDoRaw(t, f.bare, tc.body, tc.headers)
			require.NotNil(t, want.err, "the SDK refuses: %s", want.body)
			before := f.answered.Load()
			got := gateDoRaw(t, f.gated, tc.body, tc.headers)
			assert.Equal(t, before, f.answered.Load(), "never answered natively")
			assert.Equal(t, want.status, got.status)
			assert.Equal(t, want.body, got.body, "the SDK's refusal, unchanged")
		})
	}

	// A read without a uri is not a skill read: the SDK's answer.
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{}}`,
		`{"jsonrpc":"2.0","id":7,"method":"resources/read"}`,
		`{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":""}}`,
	} {
		want := gateDoRaw(t, f.bare, body, nil)
		got := gateDoRaw(t, f.gated, body, nil)
		assert.Equal(t, want.body, got.body, body)
	}
	assert.Zero(t, f.answered.Load())

	// skills/* with params that are not an object: the probe carries no
	// params, so the request reaches the answer (which refuses them, see
	// TestNativeSkills_NonObjectParamsAreInvalidParams) and the probe's
	// method never shows.
	for _, m := range []string{nativeMethodList, nativeMethodGet} {
		got := gateDoRaw(t, f.gated, `{"jsonrpc":"2.0","id":7,"method":"`+m+`","params":[]}`, nil)
		assert.Equal(t, int32(1), f.answered.Load(), "%s reaches the answer: %s", m, got.body)
		assert.NotContains(t, got.body, "ping", got.body)
		f.answered.Store(0)
	}
}

// The probe carries the request's _meta and nothing else, so its own parser
// never judges the request's params.
func TestProbeBody_CarriesOnlyTheMeta(t *testing.T) {
	meta := `{"` + mcp.MetaKeyProtocolVersion + `":"` + mcp.ProtocolVersion20260728 + `"}`
	for in, want := range map[string]string{
		`{"jsonrpc":"2.0","id":1,"method":"skills/list","params":{"_meta":` + meta + `,"cursor":5}}`: `{"id":1,"jsonrpc":"2.0","method":"ping","params":{"_meta":` + meta + `}}`,
		`{"jsonrpc":"2.0","id":1,"method":"skills/list","params":{"_META":` + meta + `}}`:            `{"id":1,"jsonrpc":"2.0","method":"ping","params":{"_meta":` + meta + `}}`,
		`{"jsonrpc":"2.0","id":1,"method":"skills/list","params":{"_meta":[]}}`:                      `{"id":1,"jsonrpc":"2.0","method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"skills/list","params":{"_meta":null,"uri":"x"}}`:          `{"id":1,"jsonrpc":"2.0","method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"skills/list","params":[]}`:                                `{"id":1,"jsonrpc":"2.0","method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"Method":"skills/list"}`:                                            `{"id":1,"jsonrpc":"2.0","method":"ping"}`,
	} {
		got, err := probeBody([]byte(in), mcp.MethodPing)
		require.NoError(t, err)
		assert.JSONEq(t, want, string(got), in)
	}
}

func TestHasRepeatedTopLevelMember(t *testing.T) {
	for body, want := range map[string]bool{
		`{"a":1,"b":{"a":1,"a":2}}`: false, // nested repeats are the params' own, passed verbatim
		`{"a":1,"a":2}`:             true,
		`{"jsonrpc":1,"JSONRPC":2}`: true,
		`{"k":1,"\u212a":2}`:        true, // KELVIN SIGN folds to k
		`{"ab":1,"ba":2}`:           false,
		`{"a":1,"b":[1,{"c":2}]}`:   false,
		`{"a":`:                     true,
		`[]`:                        true,
	} {
		assert.Equal(t, want, hasRepeatedTopLevelMember([]byte(body)), body)
	}
}

func TestNativeReadIsSkillAddress(t *testing.T) {
	known := func(uri string) bool { return uri == "file:///s/SKILL.md" }
	for uri, want := range map[string]bool{
		"skill://pdf/SKILL.md":      true,
		"skill://pdf/scripts/x.py":  true,
		"skill://pdf/nope.md":       true, // the extension's namespace: not found is ours to say
		"":                          false,
		"file:///s/SKILL.md":        true,
		"file:///ordinary.txt":      false,
		"ui://widget/1":             false,
		"skill:/pdf/SKILL.md":       false,
		"SKILL://pdf/SKILL.md":      false,
		"https://x.test/skill://pd": false,
	} {
		assert.Equal(t, want, nativeReadIsSkillAddress(uri, known), uri)
	}
	assert.False(t, nativeReadIsSkillAddress("file:///s/SKILL.md", nil))
}

// The copy of the SDK's status mapping answers what the SDK's transport sends
// for the same JSON-RPC error code.
func TestNativeProbeGate_AnswerStatusMatchesSDK(t *testing.T) {
	f := newGateFixture(t, nil)
	cases := []struct {
		method string
		params map[string]any
	}{
		{"no/such/method", map[string]any{}},                              // METHOD_NOT_FOUND
		{string(mcp.MethodToolsCall), map[string]any{"name": "missing"}},  // tool not found
		{string(mcp.MethodResourcesRead), map[string]any{"uri": "x://n"}}, // resource not found
		{string(mcp.MethodToolsCall), map[string]any{"name": 5}},          // unparsable
	}
	seen := 0
	for _, tc := range cases {
		params := modernParams(tc.params)
		h := modernHeaders(t, tc.method, params, nil)
		if tc.method == string(mcp.MethodToolsCall) {
			if _, ok := tc.params["name"].(string); !ok {
				h[mcp.HeaderName] = "5"
			}
		}
		r := gateDo(t, f.bare, tc.method, params, h)
		if r.err == nil {
			continue
		}
		seen++
		assert.Equal(t, r.status, nativeAnswerStatus(true, r.err.Code), "%s: code %d", tc.method, r.err.Code)
		assert.Equal(t, http.StatusOK, nativeAnswerStatus(false, r.err.Code))
	}
	require.GreaterOrEqual(t, seen, 3, "the table exercised the SDK's mapping")
	for _, code := range []int{mcp.INVALID_PARAMS, mcp.HEADER_MISMATCH} {
		assert.Equal(t, http.StatusBadRequest, nativeAnswerStatus(true, code))
	}
	assert.Equal(t, http.StatusOK, nativeAnswerStatus(true, nativeNotFoundCode))
	assert.Equal(t, http.StatusOK, nativeAnswerStatus(true, 0))
}

// Through the real endpoint (legacy era): an ordinary resources/read is not a
// skill access. It gets the transport's answer and records no skill row.
func TestNativeSkills_OrdinaryResourceReadIsNotASkillAccess(t *testing.T) {
	g := newNativeSkills(t)
	c := newRawMCP(t, g.singleURL())
	caps, _ := c.initialize()
	require.True(t, hasSkillsExtension(caps))

	for _, params := range []map[string]any{
		{"uri": "file:///etc/hosts"}, {"uri": "https://example.com/x"}, {"uri": "ui://widget/1"}, {},
	} {
		r := c.call(nativeMethodRead, params)
		require.NotNil(t, r.err, string(r.body))
		assert.Contains(t, r.err.Message, "handler not found for resource URI", "the SDK's own answer: %s", r.body)
		assert.NotContains(t, r.err.Message, "resource not found: ", "not the native answer")
	}
	r0 := c.call(nativeMethodRead, map[string]any{"uri": "skill://pdf/SKILL.md", "arguments": 5})
	require.NotNil(t, r0.err, string(r0.body))
	assert.Equal(t, mcp.INVALID_REQUEST, r0.err.Code, "the SDK refuses what it cannot parse: %s", r0.body)
	// A skill read on the same session is still native and recorded.
	r := c.call(nativeMethodRead, map[string]any{"uri": "skill://pdf/SKILL.md"})
	require.Nil(t, r.err, string(r.body))

	var rows []*storage.ActivityRecord
	require.Eventually(t, func() bool { rows = g.nativeRows(t); return len(rows) >= 1 }, 10*time.Second, 50*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	rows = g.nativeRows(t)
	require.Len(t, rows, 1, "only the skill read is a skill access")
	assert.Equal(t, "success", rows[0].Metadata["outcome"])
}

// Through the real endpoint: skills/* with params that are not an object are
// refused by the native answer as invalid params, and a resources/read that
// repeats uri is left to the SDK.
func TestNativeSkills_NonObjectParamsAndRepeatedURI(t *testing.T) {
	g := newNativeSkills(t)
	c := newRawMCP(t, g.singleURL())
	c.initialize()
	for _, m := range []string{nativeMethodList, nativeMethodGet} {
		for _, params := range []any{
			[]any{},
			map[string]any{"_meta": []any{}, "uri": "skill://pdf/SKILL.md"},
			map[string]any{"_meta": "x", "uri": "skill://pdf/SKILL.md"},
		} {
			r := c.sendWith(c.sid, mcp.LATEST_LEGACY_PROTOCOL_VERSION, nil, map[string]any{"jsonrpc": "2.0", "id": c.nextID(), "method": m, "params": params})
			require.NotNil(t, r.err, "%s %v: %s", m, params, r.body)
			assert.Equal(t, mcp.INVALID_PARAMS, r.err.Code, "%s %v: %s", m, params, r.body)
			assert.NotContains(t, string(r.body), "ping")
		}
	}
	// A member another method uses is ignored, as the SDK ignores it.
	rc := c.call(nativeMethodRead, map[string]any{"uri": "skill://pdf/SKILL.md", "cursor": 5})
	require.Nil(t, rc.err, "a read ignores cursor: %s", rc.body)
	rg := c.call(nativeMethodGet, map[string]any{"uri": "skill://pdf/SKILL.md", "cursor": 5})
	require.Nil(t, rg.err, "skills/get ignores cursor: %s", rg.body)
	rl := c.call(nativeMethodList, map[string]any{"uri": 5})
	require.Nil(t, rl.err, "skills/list ignores uri: %s", rl.body)

	// resources/read with a malformed _meta is the SDK's to refuse.
	r := c.call(nativeMethodRead, map[string]any{"_meta": []any{}, "uri": "skill://pdf/SKILL.md"})
	require.NotNil(t, r.err, string(r.body))
	assert.Equal(t, mcp.INVALID_REQUEST, r.err.Code, string(r.body))
	assert.Contains(t, r.err.Message, "unparsable resources/read request", string(r.body))

	body := `{"jsonrpc":"2.0","id":901,"method":"resources/read","params":{"uri":"skill://pdf/SKILL.md","uri":null}}`
	req, err := http.NewRequest(http.MethodPost, c.url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", c.sid)
	req.Header.Set("Mcp-Protocol-Version", mcp.LATEST_LEGACY_PROTOCOL_VERSION)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { assert.NoError(t, resp.Body.Close()) }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	// A repeated member in params is the transport's: the SDK's own answer,
	// never a native one. Since mcp-go v1.6 the SDK refuses a duplicate
	// member outright (Spec 135 research R1a); before, it read the last one.
	assert.Contains(t, string(raw), `duplicate member \"params.uri\"`, "the SDK's answer: %s", raw)
	assert.NotContains(t, string(raw), "missing required parameter")
}
