package cliclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// Official-registry server ids are namespace/name (io.github.owner/repo). The
// daemon decodes a %2F-encoded {id} (MCP-1118), but only if the client sends
// one: an unescaped slash splits the name across path segments and the request
// lands on a different route.
func TestPerServerRequests_EscapeServerAndToolNames(t *testing.T) {
	const (
		server = "io.github.owner/repo"
		srvEnc = "io.github.owner%2Frepo"
		tool   = "ns/erase"
		tlEnc  = "ns%2Ferase"
	)

	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, zap.NewNop().Sugar())
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		call func()
		want string
	}{
		{"enable", func() { _ = c.ServerAction(ctx, server, "enable") }, "/api/v1/servers/" + srvEnc + "/enable"},
		{"restart", func() { _ = c.ServerAction(ctx, server, "restart") }, "/api/v1/servers/" + srvEnc + "/restart"},
		{"tools", func() { _, _ = c.GetServerTools(ctx, server) }, "/api/v1/servers/" + srvEnc + "/tools"},
		{"tool enabled", func() { _ = c.SetToolEnabled(ctx, server, tool, false) }, "/api/v1/servers/" + srvEnc + "/tools/" + tlEnc + "/enabled"},
		{"all tools", func() { _, _ = c.SetAllToolsEnabled(ctx, server, true) }, "/api/v1/servers/" + srvEnc + "/tools/enable_all"},
		{"logout", func() { _ = c.TriggerOAuthLogout(ctx, server) }, "/api/v1/servers/" + srvEnc + "/logout"},
		{"patch", func() { _ = c.PatchServer(ctx, server, []byte(`{}`)) }, "/api/v1/servers/" + srvEnc},
		{"remove", func() { _ = c.RemoveServer(ctx, server) }, "/api/v1/servers/" + srvEnc},
		{"approvals", func() { _, _ = c.GetToolApprovals(ctx, server) }, "/api/v1/servers/" + srvEnc + "/tools/export"},
		{"diff", func() { _, _ = c.GetToolDiff(ctx, server, tool) }, "/api/v1/servers/" + srvEnc + "/tools/" + tlEnc + "/diff"},
		{"approve", func() { _, _ = c.ApproveTools(ctx, server, nil, true) }, "/api/v1/servers/" + srvEnc + "/tools/approve"},
		{"block", func() { _, _ = c.BlockTools(ctx, server, nil, true) }, "/api/v1/servers/" + srvEnc + "/tools/block"},
	} {
		got = ""
		tc.call()
		if got != tc.want {
			t.Errorf("%s: request path = %q, want %q", tc.name, got, tc.want)
		}
	}
}
