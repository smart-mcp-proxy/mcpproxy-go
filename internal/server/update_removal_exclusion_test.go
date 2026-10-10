package server

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// UX-01 r8 (finding 1): MCP update, MCP patch and REST UpdateServer take part
// in the config commit protocol. An update that has read server "a" must hold
// the commit lock until it has written, so a removal cannot slip in between
// the read and the write and then be undone by the update's upsert.

type updatePath struct {
	name string
	run  func(srv *Server) error // error = refused (tool error or Go error)
}

func toolErr(res *mcp.CallToolResult, err error) error {
	if err != nil {
		return err
	}
	if res != nil && res.IsError {
		return errors.New(fwdTextOf(res))
	}
	return nil
}

func fwdTextOf(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func updatePaths() []updatePath {
	return []updatePath{
		{"rest-update", func(srv *Server) error {
			return srv.UpdateServer(context.Background(), "a", &config.ServerConfig{Command: "true", Protocol: "stdio"})
		}},
		{"mcp-update", func(srv *Server) error {
			res, _, err := srv.mcpProxy.handleUpdateUpstream(context.Background(), fwdRequest(map[string]interface{}{
				"name": "a", "command": "true", "protocol": "stdio",
			}))
			return toolErr(res, err)
		}},
		{"mcp-patch", func(srv *Server) error {
			res, _, err := srv.mcpProxy.handlePatchUpstream(context.Background(), fwdRequest(map[string]interface{}{
				"name": "a", "command": "true", "protocol": "stdio",
			}))
			return toolErr(res, err)
		}},
	}
}

func seedServerA(t *testing.T, srv *Server) {
	t.Helper()
	for _, s := range srv.runtime.Config().Servers {
		if s.Name == "a" {
			seed := *s
			require.NoError(t, srv.runtime.StorageManager().SaveUpstreamServer(&seed))
		}
	}
}

func assertServerAGone(t *testing.T, srv *Server, cfgPath string) {
	t.Helper()
	got, _ := srv.runtime.StorageManager().GetUpstreamServer("a")
	require.Nil(t, got, "storage holds the removed server")
	for _, s := range srv.runtime.Config().Servers {
		require.NotEqual(t, "a", s.Name, "live config holds the removed server")
	}
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(raw), `"name": "a"`), "disk config holds the removed server")
	require.NotContains(t, srv.runtime.UpstreamManager().GetAllServerNames(), "a")
	require.NoError(t, srv.SaveConfiguration())
	for _, s := range srv.runtime.Config().Servers {
		require.NotEqual(t, "a", s.Name, "a later save republished the removed server")
	}
}

func TestUpdatePaths_RemovalCannotInterleaveBetweenReadAndWrite(t *testing.T) {
	for _, path := range updatePaths() {
		t.Run(path.name, func(t *testing.T) {
			srv, cfgPath := guardedApplyServer(t, nil)
			seedServerA(t, srv)

			reached := make(chan struct{})
			release := make(chan struct{})
			runtime.CommitServerUpdateAfterReadHook = func(string) {
				close(reached)
				<-release
			}
			t.Cleanup(func() { runtime.CommitServerUpdateAfterReadHook = nil })

			updateDone := make(chan error, 1)
			go func() { updateDone <- path.run(srv) }()
			<-reached // the update has read "a" and is about to write

			removeDone := make(chan error, 1)
			go func() { removeDone <- srv.RemoveServer(context.Background(), "a") }()
			select {
			case <-removeDone:
				t.Fatal("removal completed while an update that had read the server was in flight")
			case <-time.After(400 * time.Millisecond):
			}

			close(release)
			require.NoError(t, <-updateDone)
			require.NoError(t, <-removeDone)
			assertServerAGone(t, srv, cfgPath)
		})
	}
}

func TestUpdatePaths_AfterRemoval_NotFoundAndNothingRecreated(t *testing.T) {
	for _, path := range updatePaths() {
		t.Run(path.name, func(t *testing.T) {
			srv, cfgPath := guardedApplyServer(t, nil)
			seedServerA(t, srv)
			require.NoError(t, srv.RemoveServer(context.Background(), "a"))

			err := path.run(srv)
			require.Error(t, err, "an update of a removed server must be refused")
			require.Contains(t, err.Error(), "not found")
			assertServerAGone(t, srv, cfgPath)
		})
	}
}
