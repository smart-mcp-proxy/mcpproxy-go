package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

type invariantsController struct{ baseController }

func (c *invariantsController) GetCurrentConfig() *config.Config {
	return &config.Config{APIKey: "invariants-admin-key"}
}

// TestCredentialInvariants_MalformedRecordsOnREST is T027a's REST half: the
// same malformed records that are 401 on /mcp are refused on REST too, and
// never install an agent context. A mcp_cli_ secret is refused by PREFIX
// (403) before any store lookup; a mcp_agt_ secret whose record is
// kind=client, or carries client_id/profile_mode, is a 401.
func TestCredentialInvariants_MalformedRecordsOnREST(t *testing.T) {
	dir := t.TempDir()
	sm, err := storage.NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sm.Close() })
	key, err := auth.GetOrCreateHMACKey(dir)
	require.NoError(t, err)

	srv := NewServer(&invariantsController{}, zap.NewNop().Sugar(), nil)
	srv.SetTokenStore(sm, dir)

	now := time.Now().UTC()
	client := func() auth.AgentToken {
		return auth.AgentToken{
			Name: "client-cursor", Kind: auth.KindClient, ClientID: "cursor", ProfileMode: auth.ProfileModeSwitchable,
			AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
			CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		}
	}
	agent := func() auth.AgentToken {
		return auth.AgentToken{Name: "bot", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	}
	rows := []struct {
		name   string
		prefix string
		status int
		record auth.AgentToken
	}{
		{"cli secret, record lost kind", auth.ClientTokenPrefixStr, http.StatusForbidden, func() auth.AgentToken { r := client(); r.Kind = ""; return r }()},
		{"cli secret, no client_id", auth.ClientTokenPrefixStr, http.StatusForbidden, func() auth.AgentToken { r := client(); r.ClientID = ""; return r }()},
		{"cli secret, locked with empty pin", auth.ClientTokenPrefixStr, http.StatusForbidden, func() auth.AgentToken { r := client(); r.ProfileMode = auth.ProfileModeLocked; return r }()},
		{"cli secret, well-formed record", auth.ClientTokenPrefixStr, http.StatusForbidden, client()},
		{"agt secret on a kind=client record", auth.TokenPrefixStr, http.StatusUnauthorized, client()},
		{"agt secret on a record with client_id", auth.TokenPrefixStr, http.StatusUnauthorized, func() auth.AgentToken { r := agent(); r.ClientID = "cursor"; return r }()},
		{"agt secret on a record with profile_mode", auth.TokenPrefixStr, http.StatusUnauthorized, func() auth.AgentToken { r := agent(); r.ProfileMode = auth.ProfileModeLocked; return r }()},
	}
	for i, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			secret := tc.prefix + fmt.Sprintf("%064x", i+1)
			rec := tc.record
			rec.TokenHash = auth.HashToken(secret, key)
			rec.TokenPrefix = auth.TokenPrefix(secret)
			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			require.NoError(t, sm.GetDB().Update(func(tx *bbolt.Tx) error {
				b, err := tx.CreateBucketIfNotExists([]byte(storage.AgentTokensBucket))
				if err != nil {
					return err
				}
				return b.Put([]byte(rec.TokenHash), raw)
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/status", http.NoBody)
			req.Header.Set("X-API-Key", secret)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == http.StatusForbidden {
				require.Contains(t, w.Body.String(), "client credentials are valid on MCP endpoints only")
			}
			require.NotContains(t, w.Body.String(), `"success":true`, "no handler ran, so no agent context was ever attached")
		})
	}
}
