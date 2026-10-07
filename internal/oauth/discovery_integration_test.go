package oauth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// resetDiscoveryStateForTest isolates a test from the process-wide discovery
// cache and the once-per-server warning registry.
func resetDiscoveryStateForTest(t *testing.T) {
	t.Helper()
	globalDiscoveryCache = newDiscoveryCache(nil)
	refreshGrantWarned = sync.Map{}
}

// discoveryFixture is a combined MCP server + protected resource + authorization
// server on one httptest listener, counting hits per path.
type discoveryFixture struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits map[string]int

	prmScopes   string // JSON array literal
	asScopes    string // JSON array literal
	grantTypes  string // "" = field absent, else JSON array literal
	asPath      string // path the AS metadata is served on
	asAvailable bool
}

func newDiscoveryFixture(t *testing.T) *discoveryFixture {
	t.Helper()
	f := &discoveryFixture{
		hits:        map[string]int{},
		prmScopes:   `["read"]`,
		asScopes:    `["read","write","offline_access"]`,
		asPath:      "/.well-known/oauth-authorization-server/as",
		asAvailable: true,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits[r.Method+" "+r.URL.Path]++
		f.mu.Unlock()
		base := "http://" + r.Host
		switch {
		case r.URL.Path == "/mcp":
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, base))
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/.well-known/oauth-protected-resource/mcp":
			_, _ = fmt.Fprintf(w, `{"resource":"%s/mcp","authorization_servers":["%s/as"],"scopes_supported":%s}`, base, base, f.prmScopes)
		case (r.URL.Path == f.asPath || r.URL.Path == "/.well-known/oauth-authorization-server") && f.asAvailable:
			grant := ""
			if f.grantTypes != "" {
				grant = fmt.Sprintf(`,"grant_types_supported":%s`, f.grantTypes)
			}
			_, _ = fmt.Fprintf(w, `{"issuer":"%s/as","authorization_endpoint":"%s/as/authorize","token_endpoint":"%s/as/token","registration_endpoint":"%s/as/register","response_types_supported":["code"],"scopes_supported":%s%s}`,
				base, base, base, base, f.asScopes, grant)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *discoveryFixture) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[key]
}

func (f *discoveryFixture) serverConfig(name string, o *config.OAuthConfig) *config.ServerConfig {
	return &config.ServerConfig{Name: name, URL: f.srv.URL + "/mcp", OAuth: o}
}

// T139 / SC-003: repeated config creation hits each discovery URL once.
func TestCreateOAuthConfig_DiscoveryIsCached(t *testing.T) {
	resetDiscoveryStateForTest(t)
	f := newDiscoveryFixture(t)
	name := "cache-srv"
	stopCallbackServer(t, name)
	store := setupTestStorage(t)

	for i := 0; i < 5; i++ {
		cfg, err := createOAuthConfigInternal(f.serverConfig(name, nil), store, nil, zap.NewNop())
		require.NoError(t, err)
		require.NotNil(t, cfg)
	}
	assert.Equal(t, 1, f.count("GET /.well-known/oauth-protected-resource/mcp"), "PRM fetched once")
	assert.Equal(t, 1, f.count("GET "+f.asPath), "AS metadata fetched once")
	assert.Equal(t, 1, f.count("HEAD /mcp"), "preflight HEAD once")
	assert.Equal(t, 1, f.count("POST /mcp"), "preflight POST once")
}

func TestCreateOAuthConfig_DifferentOverridesUseSeparateCacheEntries(t *testing.T) {
	resetDiscoveryStateForTest(t)
	f := newDiscoveryFixture(t)
	name := "cache-ov-srv"
	stopCallbackServer(t, name)
	store := setupTestStorage(t)

	_, err := createOAuthConfigInternal(f.serverConfig(name, nil), store, nil, zap.NewNop())
	require.NoError(t, err)
	_, err = createOAuthConfigInternal(f.serverConfig(name, &config.OAuthConfig{
		TokenEndpoint: "https://login.example.com/oauth2/token", AuthorizationEndpoint: "https://login.example.com/oauth2/authorize",
	}), store, nil, zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, 2, f.count("HEAD /mcp"), "a different override set is a different cache key")

	// other oauth fields do not affect discovery and keep the entry (FR-027)
	_, err = createOAuthConfigInternal(f.serverConfig(name, &config.OAuthConfig{
		TokenEndpoint: "https://login.example.com/oauth2/token", AuthorizationEndpoint: "https://login.example.com/oauth2/authorize",
		ExtraParams: map[string]string{"audience": "x"}, ClientID: "c",
	}), store, nil, zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, 2, f.count("HEAD /mcp"))
}

func TestCreateOAuthConfig_FailedDiscoveryIsCachedBriefly(t *testing.T) {
	resetDiscoveryStateForTest(t)
	f := newDiscoveryFixture(t)
	f.asAvailable = false
	name := "cache-fail-srv"
	stopCallbackServer(t, name)
	store := setupTestStorage(t)
	for i := 0; i < 3; i++ {
		_, err := createOAuthConfigInternal(f.serverConfig(name, nil), store, nil, zap.NewNop())
		require.NoError(t, err)
	}
	assert.Equal(t, 1, f.count("GET "+f.asPath), "a failing metadata URL is not hammered")
}

// T141 / FR-020
func TestCreateOAuthConfig_OfflineAccessWaterfall(t *testing.T) {
	cases := []struct {
		name       string
		oauth      *config.OAuthConfig
		prmScopes  string
		asScopes   string
		wantScopes []string
	}{
		{"PRM scopes + AS advertises offline_access -> appended once", nil, `["read"]`, `["read","offline_access"]`, []string{"read", "offline_access"}},
		{"PRM already lists offline_access -> not duplicated", nil, `["read","offline_access"]`, `["offline_access"]`, []string{"read", "offline_access"}},
		{"AS does not advertise it -> unchanged", nil, `["read"]`, `["read"]`, []string{"read"}},
		{"explicit oauth.scopes -> unchanged", &config.OAuthConfig{Scopes: []string{"custom"}}, `["read"]`, `["read","offline_access"]`, []string{"custom"}},
		{"AS-derived scopes -> unchanged", nil, `[]`, `["read","offline_access"]`, []string{"read", "offline_access"}},
		{"nothing advertised -> empty", nil, `[]`, `[]`, []string{}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetDiscoveryStateForTest(t)
			f := newDiscoveryFixture(t)
			f.prmScopes, f.asScopes = c.prmScopes, c.asScopes
			name := fmt.Sprintf("offline-srv-%d", i)
			stopCallbackServer(t, name)
			cfg, err := createOAuthConfigInternal(f.serverConfig(name, c.oauth), setupTestStorage(t), nil, zap.NewNop())
			require.NoError(t, err)
			assert.Equal(t, c.wantScopes, cfg.Scopes)
		})
	}
}

// T143 / FR-021
func TestWarnIfNoRefreshGrant(t *testing.T) {
	resetDiscoveryStateForTest(t)
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	warnIfNoRefreshGrant(logger, "srv-a", &OAuthServerMetadata{GrantTypesSupported: []string{"authorization_code"}})
	warnIfNoRefreshGrant(logger, "srv-a", &OAuthServerMetadata{GrantTypesSupported: []string{"authorization_code"}})
	require.Equal(t, 1, logs.Len(), "one Warn per server per process")
	assert.Contains(t, logs.All()[0].Message, "refresh_token")

	warnIfNoRefreshGrant(logger, "srv-b", &OAuthServerMetadata{GrantTypesSupported: []string{"authorization_code"}})
	assert.Equal(t, 2, logs.Len(), "a different server warns again")

	warnIfNoRefreshGrant(logger, "srv-c", &OAuthServerMetadata{GrantTypesSupported: []string{"authorization_code", "refresh_token"}})
	warnIfNoRefreshGrant(logger, "srv-d", &OAuthServerMetadata{})
	warnIfNoRefreshGrant(logger, "srv-e", nil)
	assert.Equal(t, 2, logs.Len(), "present refresh_token or absent field is not a warning")

	// An explicitly empty list is present, and lacks refresh_token.
	doc, err := parseASMetadataDoc([]byte(`{"issuer":"https://x","grant_types_supported":[]}`))
	require.NoError(t, err)
	warnIfNoRefreshGrant(logger, "srv-f", doc.meta)
	assert.Equal(t, 3, logs.Len())
	doc, err = parseASMetadataDoc([]byte(`{"issuer":"https://x"}`))
	require.NoError(t, err)
	warnIfNoRefreshGrant(logger, "srv-g", doc.meta)
	assert.Equal(t, 3, logs.Len(), "an absent field stays silent")
}

// A 401 advertising a different resource_metadata URL invalidates discovery that
// was learned through the POST preflight (explicit oauth.scopes skips the HEAD one).
func TestCreateOAuthConfig_ExplicitScopesStillInvalidatedByNewPRMURL(t *testing.T) {
	resetDiscoveryStateForTest(t)
	f := newDiscoveryFixture(t)
	name := "prm-inval-srv"
	stopCallbackServer(t, name)
	store := setupTestStorage(t)
	sc := f.serverConfig(name, &config.OAuthConfig{Scopes: []string{"custom"}})
	_, err := createOAuthConfigInternal(sc, store, nil, zap.NewNop())
	require.NoError(t, err)
	_, err = createOAuthConfigInternal(sc, store, nil, zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, 1, f.count("POST /mcp"))

	assert.True(t, globalDiscoveryCache.noteResourceMetadataURL(sc.URL, "https://elsewhere.example/prm"),
		"the PRM URL the POST preflight used was recorded, so a different advertised one invalidates")
	_, err = createOAuthConfigInternal(sc, store, nil, zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, 2, f.count("POST /mcp"))
}

func TestCreateOAuthConfig_WarnsOnMissingRefreshGrant(t *testing.T) {
	resetDiscoveryStateForTest(t)
	f := newDiscoveryFixture(t)
	f.grantTypes = `["authorization_code"]`
	core, logs := observer.New(zap.WarnLevel)
	name := "refresh-warn-srv"
	stopCallbackServer(t, name)
	for i := 0; i < 2; i++ {
		_, err := createOAuthConfigInternal(f.serverConfig(name, nil), setupTestStorage(t), nil, zap.New(core))
		require.NoError(t, err)
	}
	n := 0
	for _, e := range logs.All() {
		if strings.Contains(e.Message, "refresh_token") {
			n++
		}
	}
	assert.Equal(t, 1, n)
}

// FR-024: with endpoint overrides mcp-go is always handed a metadata URL, and the
// preflight is skipped when auth_server_metadata_url is set.
func TestCreateOAuthConfig_EndpointOverridesSetMetadataURL(t *testing.T) {
	resetDiscoveryStateForTest(t)
	f := newDiscoveryFixture(t)
	f.asAvailable = false // standard discovery finds nothing
	name := "ov-meta-srv"
	stopCallbackServer(t, name)

	cfg, err := createOAuthConfigInternal(f.serverConfig(name, &config.OAuthConfig{
		AuthorizationEndpoint: "https://login.example.com/oauth2/authorize",
		TokenEndpoint:         "https://login.example.com/oauth2/token",
	}), setupTestStorage(t), nil, zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, "https://login.example.com/.well-known/oauth-authorization-server", cfg.AuthServerMetadataURL,
		"RFC 8414 URL derived from the authorization endpoint origin")

	resetDiscoveryStateForTest(t)
	name2 := "ov-meta-srv2"
	stopCallbackServer(t, name2)
	f.asAvailable = true
	explicit := f.srv.URL + "/custom/meta"
	before := f.count("HEAD /mcp") + f.count("POST /mcp")
	cfg, err = createOAuthConfigInternal(f.serverConfig(name2, &config.OAuthConfig{AuthServerMetadataURL: explicit}), setupTestStorage(t), nil, zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, explicit, cfg.AuthServerMetadataURL)
	assert.Equal(t, before+1, f.count("HEAD /mcp")+f.count("POST /mcp"),
		"auth_server_metadata_url replaces the auth-server/metadata discovery requests (only the PRM scope preflight HEAD remains)")
}

// The live read view masks leaves named *auth*/*token*; echoing such a view back
// on a write must restore the stored override, not persist the mask.
func TestUnmaskLiveOAuth_RestoresMaskedEndpointOverrides(t *testing.T) {
	stored := &config.OAuthConfig{
		AuthorizationEndpoint: "https://idp.example.com/oauth2/v1/authorize",
		TokenEndpoint:         "https://idp.example.com/oauth2/v1/exchange",
		RegistrationEndpoint:  "https://idp.example.com/oauth2/v1/clients",
		AuthServerMetadataURL: "https://idp.example.com/.well-known/custom",
	}
	incoming := &config.OAuthConfig{
		AuthorizationEndpoint: LiveRedaction.Leaf("authorization_endpoint", stored.AuthorizationEndpoint),
		TokenEndpoint:         LiveRedaction.Leaf("token_endpoint", stored.TokenEndpoint),
		RegistrationEndpoint:  LiveRedaction.Leaf("registration_endpoint", stored.RegistrationEndpoint),
		AuthServerMetadataURL: LiveRedaction.Leaf("auth_server_metadata_url", stored.AuthServerMetadataURL),
	}
	require.NoError(t, UnmaskLiveOAuth(incoming, stored))
	assert.Equal(t, stored, incoming)
}
