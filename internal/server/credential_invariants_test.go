package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// malformedRecord is one hand-written agent_tokens record the FR-021
// invariants must refuse at authentication (T027a).
type malformedRecord struct {
	name   string
	prefix string // secret prefix presented: mcp_cli_ or mcp_agt_
	record func() auth.AgentToken
}

// validClientRecord is a well-formed kind=client record; every client row
// below breaks exactly one thing.
func validClientRecord(id string) auth.AgentToken {
	now := time.Now().UTC()
	return auth.AgentToken{
		Name: auth.ClientTokenName(id), Kind: auth.KindClient, ClientID: id,
		ProfileMode:    auth.ProfileModeSwitchable,
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
		CreatedAt:      now, ExpiresAt: now.Add(24 * time.Hour),
	}
}

// validAgentRecord is a well-formed regular agent token record.
func validAgentRecord() auth.AgentToken {
	now := time.Now().UTC()
	return auth.AgentToken{
		Name: "bot", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
		CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
}

func mutClient(mutate func(*auth.AgentToken)) func() auth.AgentToken {
	return func() auth.AgentToken { rec := validClientRecord("cursor"); mutate(&rec); return rec }
}

func mutAgent(mutate func(*auth.AgentToken)) func() auth.AgentToken {
	return func() auth.AgentToken { rec := validAgentRecord(); mutate(&rec); return rec }
}

var malformedRecordTable = []malformedRecord{
	{"mcp_cli_ secret whose record lost kind", auth.ClientTokenPrefixStr, mutClient(func(t *auth.AgentToken) { t.Kind = "" })},
	{"kind=client without client_id", auth.ClientTokenPrefixStr, mutClient(func(t *auth.AgentToken) { t.ClientID = "" })},
	{"client_id not matching the name suffix", auth.ClientTokenPrefixStr, mutClient(func(t *auth.AgentToken) { t.Name = "client-other" })},
	{"locked with an empty pin", auth.ClientTokenPrefixStr, mutClient(func(t *auth.AgentToken) { t.ProfileMode = auth.ProfileModeLocked })},
	{"empty profile_mode", auth.ClientTokenPrefixStr, mutClient(func(t *auth.AgentToken) { t.ProfileMode = "" })},
	{"unknown profile_mode", auth.ClientTokenPrefixStr, mutClient(func(t *auth.AgentToken) { t.ProfileMode = "sticky" })},
	{"unknown kind", auth.ClientTokenPrefixStr, mutClient(func(t *auth.AgentToken) { t.Kind = "robot" })},
	{"allowed_servers narrower than *", auth.ClientTokenPrefixStr, mutClient(func(t *auth.AgentToken) { t.AllowedServers = []string{"github"} })},
	{"permissions narrower than all three", auth.ClientTokenPrefixStr, mutClient(func(t *auth.AgentToken) { t.Permissions = []string{auth.PermRead} })},
	{"expiry beyond 365 days", auth.ClientTokenPrefixStr, mutClient(func(t *auth.AgentToken) { t.ExpiresAt = t.CreatedAt.Add(400 * 24 * time.Hour) })},
	{"mcp_agt_ secret on a kind=client record", auth.TokenPrefixStr, mutClient(func(t *auth.AgentToken) {})},
	{"mcp_agt_ secret on a record with client_id", auth.TokenPrefixStr, mutAgent(func(t *auth.AgentToken) { t.ClientID = "cursor" })},
	{"mcp_agt_ secret on a record with profile_mode", auth.TokenPrefixStr, mutAgent(func(t *auth.AgentToken) { t.ProfileMode = auth.ProfileModeLocked })},
}

// rowSecret is a valid-format, per-row unique secret.
func rowSecret(prefix string, n int) string { return prefix + fmt.Sprintf("%064x", n+1) }

// writeRawRecord stores rec under the hash of secret exactly as a hand edit or
// a buggy writer would have, bypassing every mint-time check.
func writeRawRecord(t *testing.T, sm *storage.Manager, key []byte, secret string, rec auth.AgentToken) {
	t.Helper()
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
}

func invariantsRuntime(t *testing.T, logger *zap.Logger) (*runtime.Runtime, []byte) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.RequireMCPAuth = true
	rt, err := runtime.New(cfg, "", logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	key, err := auth.GetOrCreateHMACKey(cfg.DataDir)
	require.NoError(t, err)
	return rt, key
}

// TestCredentialInvariants_MalformedRecordsAre401OnMCP pins FR-021 fail-closed
// at every request: each malformed record is refused with the exact body, never
// reaches the MCP handler (zero tool calls possible) and warns naming the token.
func TestCredentialInvariants_MalformedRecordsAre401OnMCP(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	rt, key := invariantsRuntime(t, zap.New(core))
	srv := &Server{runtime: rt, logger: zap.NewNop()}

	for i, tc := range malformedRecordTable {
		t.Run(tc.name, func(t *testing.T) {
			secret := rowSecret(tc.prefix, i)
			rec := tc.record()
			writeRawRecord(t, rt.StorageManager(), key, secret, rec)
			logs.TakeAll()

			reached := false
			handler := srv.mcpAuthMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { reached = true }))
			for _, body := range []string{
				`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
				`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"github:create_issue","arguments":{}}}`,
			} {
				req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+secret)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
				require.JSONEq(t, `{"error":"Agent token invalid: malformed credential record"}`, w.Body.String())
			}
			require.False(t, reached, "a malformed record must never reach a tool handler")

			var named bool
			for _, e := range logs.All() {
				for _, f := range e.Context {
					if f.Key == "name" && f.String == rec.Name {
						named = true
					}
				}
			}
			require.True(t, named, "the warning names the token %q, got %v", rec.Name, logs.All())
		})
	}
}

// A well-formed record authenticates: the table above fails closed because of
// the invariant, not because the harness is broken.
func TestCredentialInvariants_WellFormedRecordAuthenticates(t *testing.T) {
	rt, key := invariantsRuntime(t, zap.NewNop())
	srv := &Server{runtime: rt, logger: zap.NewNop()}

	secret := rowSecret(auth.ClientTokenPrefixStr, 1000)
	writeRawRecord(t, rt.StorageManager(), key, secret, validClientRecord("cursor"))

	var got *auth.AuthContext
	handler := srv.mcpAuthMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = auth.AuthContextFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+secret)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.NotNil(t, got, w.Body.String())
	require.True(t, got.IsClientCredential())
	require.Equal(t, "cursor", got.ClientID)
}
