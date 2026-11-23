package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// A session id minted on /mcp/s/<a> is refused on /mcp/s/<b>, so a session
// (and the client identity attributed to it) cannot be carried across
// single-server surfaces.
func TestSingleServerSessionIDs_BoundToTheirServer(t *testing.T) {
	a, b := newSingleServerSessionIDs("alpha", 0), newSingleServerSessionIDs("beta", 0)
	id := a.Generate()
	_, err := a.Validate(id)
	require.NoError(t, err)
	_, err = b.Validate(id)
	assert.Error(t, err, "beta must refuse alpha's session id")
	_, err = a.Validate("mcp-session-980d2b73-e933-46a0-8d2f-0b9090f6bb79")
	assert.Error(t, err, "an id minted by another surface (no server tag) is refused")
	_, err = a.Validate(strings.Replace(id, "mcp-session-", "x-", 1))
	assert.Error(t, err)
	assert.NotEqual(t, a.Generate(), a.Generate())
}

func TestSingleServerStreamable_RefusesAnotherServersSession(t *testing.T) {
	newSurface := func(name string) *httptest.Server {
		srv := mcpserver.NewMCPServer("t", "1")
		srv.AddTool(mcp.NewTool("echo"), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		})
		return httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv, singleServerStreamableOptions(nil, name, 0)...))
	}
	alpha, beta := newSurface("alpha"), newSurface("beta")
	defer alpha.Close()
	defer beta.Close()

	post := func(url, sid string, msg map[string]any) *http.Response {
		body, _ := json.Marshal(msg)
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
			req.Header.Set("Mcp-Protocol-Version", mcp.LATEST_LEGACY_PROTOCOL_VERSION)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}
	resp := post(alpha.URL, "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": mcp.LATEST_LEGACY_PROTOCOL_VERSION, "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "c", "version": "1"},
	}})
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	sid := resp.Header.Get("Mcp-Session-Id")
	require.NotEmpty(t, sid)

	list := map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}
	own := post(alpha.URL, sid, list)
	own.Body.Close()
	assert.Equal(t, http.StatusOK, own.StatusCode, "the surface that minted the session accepts it")

	other := post(beta.URL, sid, list)
	other.Body.Close()
	assert.GreaterOrEqual(t, other.StatusCode, 400, "another surface refuses the session id")
}

// describe_tool on /mcp/s/<we> never resolves a server whose name merely
// starts with "we__" (here "we__ird"), and did_you_mean names only this
// server's tools.
func TestSingleServerSurface_DescribeToolPrefixCollision(t *testing.T) {
	p := newDirectDescribeProxy(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"tool_ids": []any{"we__ird:do_thing", "we__ird__do_thing"}}
	result, err := p.singleServerDescribeHandler("we")(singleServerAdminCtx(), req)
	require.NoError(t, err)
	text := result.Content[0].(mcp.TextContent).Text
	var resp describeToolResponse
	require.NoError(t, json.Unmarshal([]byte(text), &resp), text)
	assert.Empty(t, resp.Definitions, "a server named we__ird is not described on /mcp/s/we: %s", text)
	assert.NotContains(t, text, "Do a thing")
}

func TestStripServerPrefixInJSON_DidYouMeanKeepsOnlyThisServer(t *testing.T) {
	in := `{"errors":[{"id":"github:raed_file","did_you_mean":["github__read_file","github:read_file","we__ird:do_thing","we__ird__do_thing","github__b:x"]}]}`
	out := stripServerPrefixInJSON(in, "github")
	var doc struct {
		Errors []struct {
			DidYouMean []string `json:"did_you_mean"`
		} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &doc), out)
	require.Len(t, doc.Errors, 1)
	assert.Equal(t, []string{"read_file"}, doc.Errors[0].DidYouMean)
}

// mcp-go checks the session id only on POST. The guard in front of every
// /mcp/s/<server> transport refuses a foreign id on GET and DELETE too, so
// another endpoint's session record cannot be registered or closed here.
func TestSingleServerSessionGuard_RefusesForeignIDOnEveryMethod(t *testing.T) {
	var reached int
	guarded := httptest.NewServer(singleServerSessionGuard("beta", 0, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached++
		w.WriteHeader(http.StatusNoContent)
	})))
	defer guarded.Close()

	do := func(method, sid string) int {
		req, _ := http.NewRequest(method, guarded.URL, nil)
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}
	foreign := newSingleServerSessionIDs("alpha", 0).Generate()
	for _, m := range []string{http.MethodGet, http.MethodDelete, http.MethodPost} {
		assert.Equal(t, http.StatusNotFound, do(m, foreign), "%s with alpha's session on beta", m)
		assert.Equal(t, http.StatusNotFound, do(m, "mcp-session-980d2b73-e933-46a0-8d2f-0b9090f6bb79"), "%s with an untagged id", m)
	}
	assert.Zero(t, reached, "a refused request never reaches the transport")

	own := newSingleServerSessionIDs("beta", 0).Generate()
	assert.Equal(t, http.StatusNoContent, do(http.MethodGet, own))
	assert.Equal(t, http.StatusNoContent, do(http.MethodDelete, own))
	assert.Equal(t, http.StatusNoContent, do(http.MethodPost, ""), "initialize carries no session id")
	assert.Equal(t, 3, reached)
}

// On /mcp/s/x__y a bare "z" is "x__y:z", which is also the DISPLAY name of
// server x's tool "y:z". describe_tool must describe x__y's own z and never
// x's tool, even for a caller who may see both servers.
func TestSingleServerSurface_DescribeToolDisplayNameCollision(t *testing.T) {
	p := directCanonicalOverlapFixture(t, true)
	for _, ctx := range []context.Context{scopedCtx("x", "x__y"), singleServerAdminCtx()} {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{"tool_ids": []any{"z"}}
		result, err := p.singleServerDescribeHandler("x__y")(ctx, req)
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.NotContains(t, text, "HIDDEN_SENTINEL", "server x's tool must not be described on /mcp/s/x__y")
	}
	// Confined, every caller sees one side of the collision: x__y's own tool,
	// administrators included.
	for _, ctx := range []context.Context{scopedCtx("x", "x__y"), singleServerAdminCtx()} {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{"tool_ids": []any{"z"}}
		result, err := p.singleServerDescribeHandler("x__y")(ctx, req)
		require.NoError(t, err)
		assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "authorized canonical-id owner")
	}
}

// An upstream tool whose own name starts with "<server>__" is described by that
// listed name, not by the name with the prefix removed.
func TestSingleServerSurface_DescribeToolNameStartingWithServerPrefix(t *testing.T) {
	p := createTestMCPProxyServer(t)
	tools := []*config.ToolMetadata{
		{ServerName: "gh", Name: "gh__x", Description: "PREFIXED_TOOL", ParamsJSON: `{"type":"object"}`, Hash: "h1"},
		{ServerName: "gh", Name: "x", Description: "PLAIN_TOOL", ParamsJSON: `{"type":"object"}`, Hash: "h2"},
	}
	require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{Name: "gh", Enabled: true}))
	for _, tool := range tools {
		require.NoError(t, p.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: tool.ServerName, ToolName: tool.Name, Status: storage.ToolApprovalStatusApproved,
		}))
	}
	p.publishDirectCatalog(buildDirectCatalog(tools, nil))

	describe := func(id string) string {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{"tool_ids": []any{id}}
		result, err := p.singleServerDescribeHandler("gh")(singleServerAdminCtx(), req)
		require.NoError(t, err)
		return result.Content[0].(mcp.TextContent).Text
	}
	assert.Contains(t, describe("gh__x"), "PREFIXED_TOOL")
	assert.NotContains(t, describe("gh__x"), "PLAIN_TOOL")
	assert.Contains(t, describe("x"), "PLAIN_TOOL")
	assert.Contains(t, describe("gh:x"), "PLAIN_TOOL", "this server's colon qualifier still works")
}
