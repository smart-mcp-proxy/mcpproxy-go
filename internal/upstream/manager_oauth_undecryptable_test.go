package upstream

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Spec 123 T047 (FR-031, plan D8): the upstream manager's token reads
// distinguish an undecryptable row from "no token".

func undecryptableManager(t *testing.T, sc *config.ServerConfig) (*Manager, *storage.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	key := oauth.GenerateServerKey(sc.Name, sc.URL)
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	k, err := tokenseal.ParseKey(base64.StdEncoding.EncodeToString(raw))
	require.NoError(t, err)
	sealer, err := storage.NewManager(dir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(k))
	require.NoError(t, err)
	require.NoError(t, sealer.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName: key, DisplayName: sc.Name, AccessToken: "SPEC123-MGR-ACCESS", RefreshToken: "SPEC123-MGR-REFRESH",
		ExpiresAt: time.Now().Add(time.Hour),
	}))
	require.NoError(t, sealer.Close())

	sm, err := storage.NewManager(dir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sm.Close() })

	m := &Manager{
		clients:        make(map[string]*managed.Client),
		logger:         zap.NewNop(),
		storage:        sm.GetBoltDB(),
		secretResolver: secret.NewResolver(),
	}
	c, err := managed.NewClient(sc.Name, sc, zap.NewNop(), nil, &config.Config{}, sm.GetBoltDB(), secret.NewResolver())
	require.NoError(t, err)
	m.clients[sc.Name] = c
	return m, sm, key
}

func managerRaw(t *testing.T, sm *storage.Manager, key string) []byte {
	t.Helper()
	var out []byte
	require.NoError(t, sm.GetDB().View(func(tx *bbolt.Tx) error {
		out = append([]byte(nil), tx.Bucket([]byte(storage.OAuthTokenBucket)).Get([]byte(key))...)
		return nil
	}))
	return out
}

// manager.go RefreshOAuthToken (dynamic OAuth, no oauth block): the stored
// row is the only evidence the server uses OAuth. An undecryptable row is
// present-but-unusable: the refresh reports the coded error instead of
// "server does not use OAuth", and does not fall back to a reconnect that
// would start a login.
func TestRefreshOAuthToken_UndecryptableRow(t *testing.T) {
	sc := &config.ServerConfig{Name: "sealed-dynamic", URL: "https://sealed.example.invalid/mcp", Protocol: "http", Enabled: true}
	m, sm, key := undecryptableManager(t, sc)
	before := managerRaw(t, sm, key)

	err := m.RefreshOAuthToken(sc.Name)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "server does not use OAuth")
	assert.True(t, errors.Is(err, storage.ErrOAuthTokenUndecryptableSentinel), "got %v", err)
	assert.Equal(t, diagnostics.Code("MCPX_OAUTH_TOKEN_UNDECRYPTABLE"), diagnostics.Classify(err, diagnostics.ClassifierHints{}))
	assert.Equal(t, before, managerRaw(t, sm, key))
}

// manager.go storedAccessToken (the header injector's read): "" and no
// panic; it never triggers a login.
func TestStoredAccessToken_UndecryptableRow(t *testing.T) {
	sc := &config.ServerConfig{Name: "sealed-header", URL: "https://sealed.example.invalid/mcp", Protocol: "http", Enabled: true}
	m, _, _ := undecryptableManager(t, sc)
	assert.Equal(t, "", m.storedAccessToken(sc, commitview.CredentialBinding{}))
}

// oauthReadPathInventory is plan D8: every production caller (file and
// enclosing function) of a stored-OAuth-token read outside internal/storage.
// Each one was reviewed to distinguish the undecryptable error from "no
// token"; a new caller must be reviewed and added here.
var oauthReadPathInventory = []string{
	"internal/oauth/config.go:GetPersistedRefreshToken",
	"internal/oauth/config.go:HasPersistedToken",
	"internal/oauth/config.go:HasPersistedTokenBound",
	"internal/oauth/persistent_token_store.go:PersistentTokenStore.GetToken",
	"internal/oauth/persistent_token_store.go:PersistentTokenStore.loadRecord",
	"internal/oauth/refresh_manager.go:RefreshManager.lookupToken",
	"internal/runtime/runtime.go:Runtime.GetAllServers",
	"internal/upstream/core/client.go:Client.RefreshOAuthTokenDirect",
	"internal/upstream/core/connection_oauth.go:Client.storedOAuthTokenUndecryptable",
	"internal/upstream/core/connection_oauth.go:Client.trySSEOAuthAuth",
	"internal/upstream/core/connection_oauth.go:Client.tryOAuthAuth",
	"internal/upstream/core/connection_oauth.go:resolveCallbackPortForPersistence",
	"internal/upstream/core/connection_oauth.go:resolveCallbackRedirectURIForPersistence",
	"internal/upstream/core/oauth_refresh.go:Client.oauthRefreshRequest",
	"internal/upstream/core/oauth_refresh_replay.go:Client.withRefreshReplay",
	"internal/upstream/manager.go:Manager.RefreshOAuthToken",
	"internal/upstream/manager.go:Manager.scanForNewTokens",
	"internal/upstream/manager.go:Manager.storedAccessToken",
}

var oauthReadFuncs = map[string]bool{
	"GetOAuthToken":            true,
	"GetOAuthTokenBound":       true,
	"HasPersistedToken":        true,
	"HasPersistedTokenBound":   true,
	"GetPersistedRefreshToken": true,
}

func TestOAuthTokenReadPathInventory(t *testing.T) {
	if testing.Short() {
		t.Skip("walks the module source")
	}
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	found := map[string]bool{}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			base := d.Name()
			if rel != "." && (strings.HasPrefix(base, ".") || base == "node_modules" || base == "vendor" ||
				base == "testdata" || base == "frontend" || base == "website" || base == "native") {
				return filepath.SkipDir
			}
			if rel == "internal/storage" {
				return filepath.SkipDir // the codec and its own callers
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) == 1 {
				rt := fn.Recv.List[0].Type
				if st, ok := rt.(*ast.StarExpr); ok {
					rt = st.X
				}
				if id, ok := rt.(*ast.Ident); ok {
					name = id.Name + "." + name
				}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var callee string
				switch fx := call.Fun.(type) {
				case *ast.SelectorExpr:
					callee = fx.Sel.Name
				case *ast.Ident:
					callee = fx.Name
				}
				if oauthReadFuncs[callee] {
					found[rel+":"+name] = true
				}
				return true
			})
		}
		return nil
	})
	require.NoError(t, err)

	var got []string
	for k := range found {
		got = append(got, k)
	}
	sort.Strings(got)
	want := append([]string(nil), oauthReadPathInventory...)
	sort.Strings(want)
	assert.Equal(t, want, got,
		"a production caller of a stored-OAuth-token read was added or removed: review how it treats "+
			"storage.ErrOAuthTokenUndecryptable (plan D8) and update oauthReadPathInventory")
}
