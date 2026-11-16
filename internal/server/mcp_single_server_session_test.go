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
)

// A session id minted on /mcp/s/<a> is refused on /mcp/s/<b>, so a session
// (and the client identity attributed to it) cannot be carried across
// single-server surfaces.
func TestSingleServerSessionIDs_BoundToTheirServer(t *testing.T) {
	a, b := newSingleServerSessionIDs("alpha"), newSingleServerSessionIDs("beta")
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
		return httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv, singleServerStreamableOptions(nil, name)...))
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
