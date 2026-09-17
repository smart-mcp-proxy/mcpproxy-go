//go:build server

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/multiuser"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// stubServerController implements httpapi.ServerController for the 403 test
// below by embedding the (nil) interface: every method the test's request
// path never reaches promotes straight through to a nil-pointer panic it
// will never trigger, and GetCurrentConfig is the only one apiKeyAuthMiddleware
// needs to run — tenantSessionAllowlist must refuse the tenant BEFORE any
// handler that would call something else on this controller ever runs
// (contracts/rest-endpoints.md: "the fixed 403 ... before any body or path
// param is even parsed").
type stubServerController struct {
	httpapi.ServerController
	cfg *config.Config
}

func (c *stubServerController) GetCurrentConfig() interface{} { return c.cfg }

// T082 (Spec 107 US4): GET /user/activity wired through
// storage.ActivityFilter with BOTH the UserID and the AllowedServers terms
// evaluated inside ActivityFilter.Matches — never a read-everything-then-
// post-filter loop (multiuser/activity.go:107-150 today) — plus the same
// masking core GET /activity applies (T086: contracts/entitlement-predicate.md
// §"user/activity", tasks.md T086).
//
// This file does not compile until:
//   - T086 adds storage.ActivityFilter.UserID (activity_models.go:283-299)
//   - T086 adds httpapi.(*Server).ActivityProjector() and a matching setter on
//     UserActivityHandlers to receive it (named here SetActivityProjector,
//     the repo's existing convention — SetEntitlement, SetAdminServersProvider
//     — for injecting a collaborator built elsewhere; the production name is
//     T086's to fix, and a rename there is a discrepancy against this test,
//     not a bug in it)
//   - T083 adds httpapi.SessionPrincipalResolver / SetSessionPrincipalResolver
//     (used below to prove the core /activity family refuses a tenant)

const (
	wiredAliceID  = "01HTEST0000000000000ALICE"
	wiredBobID    = "01HTEST00000000000000BOB1"
	wiredSentinel = "AKIAIOSFODNN7EXAMPLE" // recognised by security.Detector (activity_mask_test.go)
)

// capturingActivityProvider is multiuser.ActivityStorageProvider, but unlike
// the plain mockActivityProvider in user_activity_test.go it applies the
// filter's OWN Matches predicate to every record — exactly what BBolt storage
// does — rather than returning every record and trusting a caller to
// post-filter. It also records the filter it was called with, so a test can
// assert the query itself carried UserID/AllowedServers/limit/offset and
// nothing more, catching a regression back to the fetch-100-then-post-filter
// shape T086 replaces.
type capturingActivityProvider struct {
	records    []*storage.ActivityRecord
	lastFilter storage.ActivityFilter
	calls      int
}

func (p *capturingActivityProvider) ListActivities(filter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	p.calls++
	p.lastFilter = filter

	var matched []*storage.ActivityRecord
	for _, r := range p.records {
		if filter.Matches(r) {
			matched = append(matched, r)
		}
	}
	total := len(matched)

	start := filter.Offset
	if start > total {
		start = total
	}
	end := total
	if filter.Limit > 0 && start+filter.Limit < end {
		end = start + filter.Limit
	}
	return matched[start:end], total, nil
}

func (p *capturingActivityProvider) GetActivity(id string) (*storage.ActivityRecord, error) {
	for _, r := range p.records {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, nil
}

// wiredSentinelRecord is one call to server "a" carrying a flagged secret,
// attributed to ownerID ("" for an operator/system-plane record that belongs
// to no tenant).
func wiredSentinelRecord(id, ownerID, ownerEmail string) *storage.ActivityRecord {
	return &storage.ActivityRecord{
		ID:         id,
		Type:       storage.ActivityTypeToolCall,
		ServerName: "a",
		ToolName:   "call",
		Status:     "success",
		Timestamp:  time.Now().UTC(),
		UserID:     ownerID,
		UserEmail:  ownerEmail,
		Arguments:  map[string]interface{}{"token": wiredSentinel + " aws key"},
		Metadata: map[string]interface{}{
			"sensitive_data_detection": map[string]interface{}{
				"detected": true,
				"detections": []interface{}{
					map[string]interface{}{"type": "aws_access_key", "severity": "critical", "location": "arguments"},
				},
			},
		},
	}
}

// wiredActivityTestSetup builds a UserActivityHandlers backed by
// capturingActivityProvider, wired to entitle every tenant to server "a" (the
// records all three sentinel calls above use), plus a real
// httpapi.(*Server).ActivityProjector() for masking parity with core
// /activity.
func wiredActivityTestSetup(t *testing.T, records []*storage.ActivityRecord) (*UserActivityHandlers, *capturingActivityProvider, *users.UserStore) {
	t.Helper()

	tmpFile := filepath.Join(t.TempDir(), "test.db")
	db, err := bbolt.Open(tmpFile, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())

	provider := &capturingActivityProvider{records: records}
	activityFilter := multiuser.NewActivityFilter(provider)
	logger := zap.NewNop().Sugar()

	handlers := NewUserActivityHandlers(activityFilter, store, nil, logger)

	// The masking parity core GET /activity applies (T086): build an
	// httpapi.Server purely to reuse its projector — no routes on it are
	// exercised here, only the conversion + mask composition.
	maskingServer := httpapi.NewServer(&stubServerController{cfg: &config.Config{APIKey: "unused"}}, logger, nil)
	maskingServer.SetSensitiveMasker(security.NewDetector(nil))
	handlers.SetActivityProjector(maskingServer.ActivityProjector())

	return handlers, provider, store
}

func wiredActivityRouter(handlers *UserActivityHandlers, ac *auth.AuthContext) http.Handler {
	ensureUserRecord(handlers.userStore, ac)
	r := chi.NewRouter()
	if ac != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := auth.WithAuthContext(r.Context(), ac)
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		})
	}
	r.Route("/api/v1", func(r chi.Router) { handlers.RegisterRoutes(r) })
	return r
}

func aliceContext() *auth.AuthContext {
	return &auth.AuthContext{
		Type: auth.AuthTypeUser, UserID: wiredAliceID, Email: "alice@example.com", DisplayName: "Alice",
		Provider: "google", AllowedServers: []string{"a"}, CredentialKind: auth.CredentialKindCookie,
	}
}

func bobContext() *auth.AuthContext {
	return &auth.AuthContext{
		Type: auth.AuthTypeUser, UserID: wiredBobID, Email: "bob@example.com", DisplayName: "Bob",
		Provider: "google", AllowedServers: []string{"a"}, CredentialKind: auth.CredentialKindCookie,
	}
}

// danaContext is Dana, the administrator (admin_user), whose /user/activity
// must stay the merge-base {items:[],total:0} — SC-006, and this door's
// deliberate carve-out: the filter's admin branch is never reached from here.
func danaContext() *auth.AuthContext {
	return &auth.AuthContext{
		Type: auth.AuthTypeAdminUser, UserID: "dana", Email: "dana@example.com", DisplayName: "Dana",
		Provider: "google", CredentialKind: auth.CredentialKindCookie,
	}
}

func TestUserActivityWired_AliceSeesOnlyHerRecordOnServerA(t *testing.T) {
	records := []*storage.ActivityRecord{
		wiredSentinelRecord("rec-alice", wiredAliceID, "alice@example.com"),
		wiredSentinelRecord("rec-bob", wiredBobID, "bob@example.com"),
		wiredSentinelRecord("rec-operator", "", ""), // operator/system-plane: no owner
	}
	handlers, provider, _ := wiredActivityTestSetup(t, records)
	router := wiredActivityRouter(handlers, aliceContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/activity?limit=10&offset=0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp ActivityListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Total, "total must count only Alice's record, not Bob's or the operator's")

	body := w.Body.String()
	assert.NotContains(t, body, wiredSentinel, "the flagged secret must be masked, same as core GET /activity")

	// The query itself carried the authorization terms and the requested
	// pagination — never the fetch-100-then-post-filter shape.
	assert.Equal(t, wiredAliceID, provider.lastFilter.UserID)
	assert.NotNil(t, provider.lastFilter.AllowedServers)
	assert.Equal(t, 10, provider.lastFilter.Limit)
	assert.Equal(t, 0, provider.lastFilter.Offset)
}

func TestUserActivityWired_BobSeesOnlyHisRecord(t *testing.T) {
	records := []*storage.ActivityRecord{
		wiredSentinelRecord("rec-alice", wiredAliceID, "alice@example.com"),
		wiredSentinelRecord("rec-bob", wiredBobID, "bob@example.com"),
		wiredSentinelRecord("rec-operator", "", ""),
	}
	handlers, _, _ := wiredActivityTestSetup(t, records)
	router := wiredActivityRouter(handlers, bobContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/activity", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp ActivityListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Total, "total must count only Bob's record")
}

// TestUserActivityWired_AdminUnchangedMeansEmpty pins the merge-base carve-out
// explicitly: wiring multiuser.ActivityFilter must not hand an admin_user
// principal the fleet's history through this door (that stays the whole-
// config /activity surface, refused to tenants below) — the handler answers
// IsAdmin() principals with today's shape unconditionally.
func TestUserActivityWired_AdminUnchangedMeansEmpty(t *testing.T) {
	records := []*storage.ActivityRecord{
		wiredSentinelRecord("rec-alice", wiredAliceID, "alice@example.com"),
		wiredSentinelRecord("rec-bob", wiredBobID, "bob@example.com"),
	}
	handlers, _, _ := wiredActivityTestSetup(t, records)
	router := wiredActivityRouter(handlers, danaContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/activity", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp ActivityListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Total)

	items, _ := resp.Items.([]interface{})
	assert.Empty(t, items, `admin_user must still receive {"items":[],"total":0}`)
}

// TestCoreActivityDoorsRefuseTenant proves the FR-043(k) refusal list: a
// session-principal (tenant) hitting the CORE, whole-configuration activity
// surface gets the fixed 403 tenantSessionAllowlist writes, before any body
// or path param is even parsed — never the tenant's own (nor anyone else's)
// records through the admin door.
func TestCoreActivityDoorsRefuseTenant(t *testing.T) {
	logger := zap.NewNop().Sugar()
	srv := httpapi.NewServer(&stubServerController{cfg: &config.Config{APIKey: "admin-key-unused-by-tenant"}}, logger, nil)

	const tenantToken = "alice-session-jwt"
	srv.SetSessionPrincipalResolver(func(_ *http.Request, kind auth.CredentialKind, value string) (*auth.AuthContext, error) {
		if kind != auth.CredentialKindBearerJWT || value != tenantToken {
			return nil, nil
		}
		return aliceContext(), nil
	})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	for _, path := range []string{
		"/api/v1/activity",
		"/api/v1/tool-calls",
		"/api/v1/servers/a/tool-calls",
	} {
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+tenantToken)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "tenant must be refused %s non-disclosingly (FR-043(k))", path)
	}
}
