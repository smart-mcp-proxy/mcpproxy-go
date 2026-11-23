package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

const oauthLoginTestAPIKey = "test-api-key-oauth-login"

func newOAuthLoginTestServer(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = oauthLoginTestAPIKey
	if mutate != nil {
		mutate(cfg)
	}
	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })
	return srv
}

// oauthLoginTestMuxes builds the mux as startCustomHTTPServer shapes it around
// the browser-login routes: the /mcp catch-all chain (real host validation and
// MCP auth middleware around a sentinel) and the real root handler. withRoutes
// adds the Spec 132 registrations.
func oauthLoginTestMux(s *Server, withRoutes bool) *http.ServeMux {
	sentinel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "mcp-chain %s %s", r.Method, r.URL.Path)
	})
	mcpHandler := s.hostValidationMiddleware(s.mcpAuthMiddleware(sentinel))
	rootHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)
	mux.Handle("/", rootHandler)
	if withRoutes {
		// The OAuth address runs the REAL resource authentication around a
		// sentinel that reports the identity it was handed.
		oauthMCP := s.oauthResourceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ac := auth.AuthContextFromContext(r.Context())
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client": ac.ClientID, "kind": ac.CredentialKind, "grant": ac.GrantID,
				"pin": ac.ProfilePin, "mode": ac.ProfileMode, "agent": ac.AgentName, "path": r.URL.Path,
			})
		}))
		s.registerOAuthLoginRoutes(mux, mcpHandler, rootHandler, s.hostValidationMiddleware(oauthMCP))
	}
	return mux
}

var oauthLoginTestPaths = []string{
	"/mcp/oauth", "/mcp/oauth/", "/mcp/oauth/cursor",
	"/oauth", "/oauth/", "/oauth/x",
	"/oauth/register", "/oauth/authorize", "/oauth/token",
	"/.well-known/oauth-authorization-server",
	"/.well-known/oauth-protected-resource",
	"/.well-known/oauth-protected-resource/mcp/oauth",
	"/.well-known/oauth-protected-resource/mcp/oauth/cursor",
	"/.well-known/x",
}

type oauthLoginResp struct {
	code            int
	body            string
	location        string
	wwwAuthenticate string
}

func doOAuthLoginReq(mux http.Handler, method, path, remote string, hdr map[string]string) oauthLoginResp {
	req := httptest.NewRequest(method, path, http.NoBody)
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return oauthLoginResp{
		code:            rec.Code,
		body:            rec.Body.String(),
		location:        rec.Header().Get("Location"),
		wwwAuthenticate: rec.Header().Get("WWW-Authenticate"),
	}
}

func TestOAuthLogin_DefaultOff_RoutesUnchanged(t *testing.T) {
	creds := map[string]map[string]string{
		"none":    nil,
		"admin":   {"X-API-Key": oauthLoginTestAPIKey},
		"garbage": {"Authorization": "Bearer garbage-token"},
		// An access-token-shaped credential is just an unknown token while the
		// feature is off and never used (Spec 132 FR-002).
		"oat":    {"Authorization": "Bearer mcp_oat_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		"oat-ws": {"Authorization": "Bearer  mcp_oat_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
	}
	for _, requireAuth := range []bool{false, true} {
		srv := newOAuthLoginTestServer(t, func(c *config.Config) { c.RequireMCPAuth = requireAuth })
		full := oauthLoginTestMux(srv, true)
		ref := oauthLoginTestMux(srv, false)
		for _, path := range oauthLoginTestPaths {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				for credName, hdr := range creds {
					for _, remote := range []string{"192.0.2.1:4000", "127.0.0.1:4000"} {
						name := fmt.Sprintf("require=%v %s %s cred=%s peer=%s", requireAuth, method, path, credName, remote)
						want := doOAuthLoginReq(ref, method, path, remote, hdr)
						got := doOAuthLoginReq(full, method, path, remote, hdr)
						assert.Equal(t, want, got, name)
					}
				}
			}
		}
	}
}

func bucketSnapshot(t *testing.T, db *bbolt.DB) (buckets []string, metaKeys []string) {
	t.Helper()
	require.NoError(t, db.View(func(tx *bbolt.Tx) error {
		if err := tx.ForEach(func(name []byte, b *bbolt.Bucket) error {
			buckets = append(buckets, string(name))
			return nil
		}); err != nil {
			return err
		}
		if meta := tx.Bucket([]byte(storage.MetaBucket)); meta != nil {
			return meta.ForEach(func(k, _ []byte) error {
				metaKeys = append(metaKeys, string(k))
				return nil
			})
		}
		return nil
	}))
	sort.Strings(buckets)
	sort.Strings(metaKeys)
	return buckets, metaKeys
}

func TestOAuthLogin_DefaultOff_NoStorage(t *testing.T) {
	srv := newOAuthLoginTestServer(t, nil)
	db := srv.runtime.StorageManager().GetDB()
	require.NotNil(t, db)
	beforeBuckets, _ := bucketSnapshot(t, db)

	mux := oauthLoginTestMux(srv, true)
	for _, path := range oauthLoginTestPaths {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			doOAuthLoginReq(mux, method, path, "127.0.0.1:4000", nil)
			doOAuthLoginReq(mux, method, path, "127.0.0.1:4000", map[string]string{"X-API-Key": oauthLoginTestAPIKey})
			doOAuthLoginReq(mux, method, path, "127.0.0.1:4000", map[string]string{"Authorization": "Bearer garbage-token"})
		}
	}

	// Only browser-login state is asserted: unrelated runtime startup tasks
	// (e.g. the work-session backfill) may write to the database concurrently.
	afterBuckets, afterMeta := bucketSnapshot(t, db)
	before := map[string]bool{}
	for _, name := range beforeBuckets {
		before[name] = true
	}
	for _, name := range afterBuckets {
		if strings.HasPrefix(name, "oauth_") && !before[name] {
			t.Errorf("browser login created bucket %q while off", name)
		}
	}
	for _, name := range []string{"oauth_registrations", "oauth_grants", "oauth_grant_access_index", "oauth_client_bindings"} {
		assert.NotContains(t, afterBuckets, name, "lazy browser-login bucket must not exist")
	}
	for _, key := range afterMeta {
		assert.False(t, strings.HasPrefix(key, "oauth_login"), "MetaBucket key %q must not exist", key)
	}
}

// Patterns the gate guards (the ones registered by registerOAuthLoginRoutes).
var oauthLoginGatedPaths = []string{
	"/mcp/oauth", "/mcp/oauth/", "/mcp/oauth/cursor",
	"/oauth/register", "/oauth/authorize", "/oauth/token",
	"/.well-known/oauth-authorization-server",
	"/.well-known/oauth-protected-resource/mcp/oauth",
	"/.well-known/oauth-protected-resource/mcp/oauth/cursor",
}

func TestOAuthLogin_Gate_OffAndEverUsed_404(t *testing.T) {
	srv := newOAuthLoginTestServer(t, nil)
	srv.oauthLoginEverUsed = func() bool { return true }
	mux := oauthLoginTestMux(srv, true)
	for _, path := range oauthLoginGatedPaths {
		got := doOAuthLoginReq(mux, http.MethodGet, path, "127.0.0.1:4000", nil)
		assert.Equal(t, http.StatusNotFound, got.code, path)
		assert.NotContains(t, got.body, "mcp-chain", path)
	}
}
