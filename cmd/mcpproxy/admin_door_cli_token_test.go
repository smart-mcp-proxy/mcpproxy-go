package main

// Spec 133 FR-023 for the CLI token doors: each `mcpproxy token …` command
// the door registry marks done drives its real Cobra handler against a fake
// daemon, and must send exactly the REST request whose door row is done
// with the same action — the daemon's line for that door is pinned by
// internal/server/admin_action_token_doors_test.go. A CLI that sent revoke's
// soft route for `token delete` would fail here.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

func TestAdminDoors_CLITokenCommandsHitTheirDoneRESTDoors(t *testing.T) {
	var mu sync.Mutex
	var got []string
	handler := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tokens":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(contracts.NewSuccessResponse(map[string]interface{}{
				"name": "ci", "token": "mcp_agt_deadbeefcafe0123456789", "allowed_servers": []string{"*"},
				"permissions": []string{"read"}, "expires_at": "2027-01-01T00:00:00Z",
			}))
		case r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(contracts.NewSuccessResponse(map[string]interface{}{"name": "ci", "token": "mcp_agt_feedfacecafe0123456789"}))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
	// newTokenTestDaemon writes the CLI config and routes /api/v1/tokens;
	// the per-name doors live below it, so point the CLI at a daemon whose
	// mux serves the whole subtree.
	newTokenTestDaemon(t, handler)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"running": true}})
	})
	mux.HandleFunc("/api/v1/tokens", handler)
	mux.HandleFunc("/api/v1/tokens/", handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("MCPPROXY_TRAY_ENDPOINT", srv.URL)

	oldName, oldServers, oldPerms, oldExpires := tokenName, tokenServers, tokenPermissions, tokenExpires
	tokenName, tokenServers, tokenPermissions, tokenExpires = "ci", "*", "read", "30d"
	defer func() {
		tokenName, tokenServers, tokenPermissions, tokenExpires = oldName, oldServers, oldPerms, oldExpires
	}()

	cases := []struct {
		cli, restDoor, request string
		run                    func() error
	}{
		{"CLI mcpproxy token create", "REST POST /api/v1/tokens/", "POST /api/v1/tokens", func() error { return runTokenCreate(nil, nil) }},
		{"CLI mcpproxy token regenerate", "REST POST /api/v1/tokens/{name}/regenerate", "POST /api/v1/tokens/ci/regenerate", func() error { return runTokenRegenerate(nil, []string{"ci"}) }},
		{"CLI mcpproxy token revoke", "REST DELETE /api/v1/tokens/{name}/", "DELETE /api/v1/tokens/ci", func() error { return runTokenRevoke(nil, []string{"ci"}) }},
		{"CLI mcpproxy token delete", "REST DELETE /api/v1/tokens/{name}/permanent", "DELETE /api/v1/tokens/ci/permanent", func() error { return runTokenDelete(nil, []string{"ci"}) }},
	}
	for _, tc := range cases {
		t.Run(tc.cli, func(t *testing.T) {
			mu.Lock()
			got = nil
			mu.Unlock()
			captureTokenStdout(t, tc.run)
			mu.Lock()
			sent := append([]string(nil), got...)
			mu.Unlock()
			var mutations []string
			for _, s := range sent {
				if s != "GET /api/v1/status" {
					mutations = append(mutations, s)
				}
			}
			require.Equal(t, []string{tc.request}, mutations, "the command sends exactly its door's request")

			cliActions := doneActions(tc.cli, "")
			if len(cliActions) == 0 {
				cliActions = doneActions(tc.cli, "agent token")
			}
			require.NotEmpty(t, cliActions, "%s is a done registry row", tc.cli)
			restActions := doneActions(tc.restDoor, "")
			if len(restActions) == 0 {
				restActions = doneActions(tc.restDoor, "agent token")
			}
			require.Equal(t, cliActions, restActions, "the CLI row and the REST door it reaches record the same action")
		})
	}
}

func doneActions(door, branch string) []string {
	for _, d := range audit.AdminDoors {
		if d.Door == door && d.Branch == branch && d.Status == audit.DoorDone {
			return d.Actions
		}
	}
	return nil
}
