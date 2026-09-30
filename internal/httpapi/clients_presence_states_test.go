//go:build !server

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	proxyRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 109 T124: presence is implemented in this package (clients.go
// clientPresence, connect.go applyClientConnected), so its pure-logic tests
// live here. They cover behaviour that already shipped; they are coverage,
// not red-first.

// TestClientsPresence_FiveStates pins the five presence states of FR-034.
func TestClientsPresence_FiveStates(t *testing.T) {
	home := t.TempDir()
	// cursor has a config file that does not contain the mcpproxy entry: installed.
	cursorCfg := connect.ConfigPath("cursor", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cursorCfg), 0o755))
	require.NoError(t, os.WriteFile(cursorCfg, []byte(`{"mcpServers":{}}`), 0o600))

	connectedAt := time.Date(2026, 9, 29, 11, 0, 0, 0, time.UTC)
	seenAt := connectedAt.Add(time.Minute)
	otherSeen := connectedAt.Add(2 * time.Minute)
	ctrl := &clientPresenceController{state: &storage.OnboardingState{
		ClientConnectedAt: map[string]time.Time{
			"claude-code": connectedAt, // connected and seen
			"windsurf":    connectedAt, // connected, never initialised
		},
		ClientLastSeen: map[string]time.Time{
			"claude-code":          seenAt,
			"local experimental x": otherSeen,
		},
	}}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	srv.SetConnectService(connect.NewServiceWithHome("127.0.0.1:8080", "", home))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil)
	req.Header.Set("X-API-Key", "clients-admin-key")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rows := map[string]map[string]any{}
	for _, raw := range successData(t, rec)["clients"].([]any) {
		row := raw.(map[string]any)
		rows[row["id"].(string)] = row
	}

	require.Equal(t, "connected_seen", rows["claude-code"]["state"])
	require.Equal(t, true, rows["claude-code"]["connected"])
	require.NotNil(t, rows["claude-code"]["last_seen"])

	require.Equal(t, "connected_never_seen", rows["windsurf"]["state"])
	require.Equal(t, true, rows["windsurf"]["connected"])
	require.Nil(t, rows["windsurf"]["last_seen"])

	require.Equal(t, "installed", rows["cursor"]["state"])
	require.Equal(t, true, rows["cursor"]["connection_unverified"])
	require.NotEqual(t, true, rows["cursor"]["connected"])

	require.Equal(t, "not_installed", rows["codex"]["state"])
	require.NotEqual(t, true, rows["codex"]["connection_unverified"])

	other, ok := rows["other:local experimental x"]
	require.True(t, ok, "an observed unknown clientInfo.name must surface as an other:<name> row")
	require.Equal(t, "other", other["kind"])
	require.Equal(t, "other", other["state"])
}

// TestApplyClientConnected_ClearsEveryAliasForEveryClient pins that the key
// spelling written by the runtime (clientidentity-normalised) and the one
// cleared by httpapi (strings.ToLower) agree for every known alias. A silent
// divergence would leave a stale connected_seen after a reconnect.
func TestApplyClientConnected_ClearsEveryAliasForEveryClient(t *testing.T) {
	for _, def := range connect.GetAllClients() {
		def := def
		t.Run(def.ID, func(t *testing.T) {
			rt, err := proxyRuntime.New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0"}, "", zap.NewNop())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, rt.Close()) })

			for _, alias := range def.ClientInfoNames {
				rt.RecordClientSeen(alias)
			}
			// Every other client's aliases are recorded too and must survive.
			var otherKeys []string
			for _, other := range connect.GetAllClients() {
				if other.ID == def.ID {
					continue
				}
				for _, alias := range other.ClientInfoNames {
					rt.RecordClientSeen(alias)
					otherKeys = append(otherKeys, strings.ToLower(alias))
				}
			}

			state, err := rt.GetOnboardingState()
			require.NoError(t, err)
			for _, alias := range def.ClientInfoNames {
				require.Contains(t, state.ClientLastSeen, strings.ToLower(alias), "runtime must store alias %q under its lower-cased spelling", alias)
			}

			now := time.Now()
			applyClientConnected(state, def.ID, now)

			for _, alias := range def.ClientInfoNames {
				require.NotContains(t, state.ClientLastSeen, strings.ToLower(alias), "alias %q must be cleared on reconnect", alias)
			}
			for _, key := range otherKeys {
				if isAliasOf(def, key) {
					continue
				}
				require.Contains(t, state.ClientLastSeen, key, "another client's key %q must be untouched", key)
			}
			require.Equal(t, now, state.ClientConnectedAt[def.ID])
		})
	}
}

func isAliasOf(def connect.ClientDef, key string) bool {
	for _, alias := range def.ClientInfoNames {
		if strings.ToLower(alias) == key {
			return true
		}
	}
	return false
}

func TestApplyClientConnected_ClearsDisconnectedAt(t *testing.T) {
	disconnected := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	state := &storage.OnboardingState{
		ClientDisconnectedAt: map[string]time.Time{"cursor": disconnected, "codex": disconnected},
	}
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	applyClientConnected(state, "cursor", now)

	require.NotContains(t, state.ClientDisconnectedAt, "cursor")
	require.Contains(t, state.ClientDisconnectedAt, "codex", "another client's disconnect stays")
	require.Equal(t, now, state.ClientConnectedAt["cursor"])
}
