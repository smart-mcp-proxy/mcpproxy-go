package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// Official-registry server ids are namespace/name; the core decodes a
// %2F-encoded {id} on /api/v1/servers/{id}/* (MCP-1118), so the tray has to
// send one for the menu actions to reach the right server.
func TestClientServerActions_EscapeSlashServerName(t *testing.T) {
	const (
		name = "io.github.owner/repo"
		enc  = "/api/v1/servers/io.github.owner%2Frepo"
	)

	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer server.Close()

	client := &Client{baseURL: server.URL, httpClient: server.Client(), logger: zap.NewNop().Sugar()}

	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"enable", func() error { return client.EnableServer(name, true) }, enc + "/enable"},
		{"disable", func() error { return client.EnableServer(name, false) }, enc + "/disable"},
		{"restart", func() error { return client.RestartServer(name) }, enc + "/restart"},
		{"login", func() error { return client.TriggerOAuthLogin(name) }, enc + "/login"},
		{"tools", func() error { _, err := client.GetServerTools(name); return err }, enc + "/tools"},
		{"quarantine", func() error { return client.QuarantineServer(name) }, enc + "/quarantine"},
	} {
		got = ""
		_ = tc.call()
		if got != tc.want {
			t.Errorf("%s: request path = %q, want %q", tc.name, got, tc.want)
		}
	}
}
