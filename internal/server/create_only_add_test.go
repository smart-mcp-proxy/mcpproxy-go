package server

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func addRequest(name string, args ...string) mcp.CallToolRequest {
	argsJSON, _ := json.Marshal(args)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"operation": "add", "name": name, "command": "python3", "args_json": string(argsJSON),
	}
	return req
}

// UX-01: MCP `upstream_servers add` over an existing, approved server must fail
// without touching the stored record.
func TestHandleAddUpstream_DuplicateNameIsRefusedWithoutSideEffects(t *testing.T) {
	p := createTestMCPProxyServer(t)
	created := time.Now().Add(-time.Hour).Truncate(time.Second)
	require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "large-review", Command: "python3", Args: []string{"fixture.py", "180"},
		Protocol: "stdio", Enabled: true, Quarantined: false, Created: created,
	}))

	res, err := p.handleAddUpstream(context.Background(), addRequest("large-review", "fixture.py", "12"))
	require.NoError(t, err)
	require.True(t, res.IsError)
	text := res.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "server 'large-review' already exists")
	assert.Contains(t, text, "update")
	assert.NotContains(t, text, "180", "must not echo the existing config")

	got, err := p.storage.GetUpstreamServer("large-review")
	require.NoError(t, err)
	assert.Equal(t, []string{"fixture.py", "180"}, got.Args)
	assert.False(t, got.Quarantined)
	assert.True(t, got.Created.Equal(created))
}

func TestHandleAddUpstream_ConcurrentSameNameExactlyOneSucceeds(t *testing.T) {
	p := createTestMCPProxyServer(t)
	var ok int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// enabled=false skips the 2s connection wait
			req := addRequest("racer", "a")
			req.Params.Arguments.(map[string]interface{})["enabled"] = false
			res, err := p.handleAddUpstream(context.Background(), req)
			if err == nil && res != nil && !res.IsError {
				atomic.AddInt32(&ok, 1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), atomic.LoadInt32(&ok))
}

func TestConfigWithAppendedServer_ReplacesSameName(t *testing.T) {
	old := &config.ServerConfig{Name: "a", Command: "old"}
	cur := &config.Config{Servers: []*config.ServerConfig{old, {Name: "b"}}}
	updated := configWithAppendedServer(cur, &config.ServerConfig{Name: "a", Command: "new"})
	require.Len(t, updated.Servers, 2)
	assert.Equal(t, "new", updated.Servers[0].Command)
	assert.Equal(t, "old", cur.Servers[0].Command, "published snapshot untouched")
}

func TestServerAddServer_CreateOnly(t *testing.T) {
	srv, _ := guardedApplyServer(t, nil)
	ctx := context.Background()

	// Name present only in runtime config ("a" from the fixture) is refused.
	err := srv.AddServer(ctx, &config.ServerConfig{Name: "a", Command: "true", Protocol: "stdio"})
	var exists *ServerExistsError
	require.ErrorAs(t, err, &exists)
	assert.Contains(t, err.Error(), "server 'a' already exists")

	var wins, dups int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := srv.AddServer(ctx, &config.ServerConfig{Name: "fresh", Command: "true", Protocol: "stdio", Quarantined: true})
			if e == nil {
				atomic.AddInt32(&wins, 1)
			} else if _, isDup := e.(*ServerExistsError); isDup {
				atomic.AddInt32(&dups, 1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), wins)
	assert.Equal(t, int32(39), dups)

	n := 0
	for _, s := range srv.runtime.Config().Servers {
		if s.Name == "fresh" {
			n++
		}
	}
	assert.Equal(t, 1, n, "runtime config must hold exactly one entry")
}
