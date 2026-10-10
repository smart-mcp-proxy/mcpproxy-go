package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// UX-01 r8 (finding 3): a config-driven removal must forget the removed
// server's tool approvals and OAuth state, like an explicit removal does, so a
// same-name create-only add starts from a fresh review.

func seedSecurityState(t *testing.T, rt *Runtime, sc *config.ServerConfig) {
	t.Helper()
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(sc))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{sc}
		return &n, nil
	}))
	require.NoError(t, rt.StorageManager().SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: sc.Name, ToolName: "t1", ApprovedHash: "h1", CurrentHash: "h1", Status: "approved", ApprovedAt: time.Now(),
	}))
	require.NoError(t, rt.storageManager.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName: oauth.GenerateServerKey(sc.Name, sc.URL), DisplayName: sc.Name,
		AccessToken: "tok", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour),
		Created: time.Now(), Updated: time.Now(),
	}))
}

func assertSecurityStateGone(t *testing.T, rt *Runtime, sc *config.ServerConfig) {
	t.Helper()
	rec, _ := rt.StorageManager().GetToolApproval(sc.Name, "t1") // "not found" error when purged
	require.Nil(t, rec, "removed server's approval record survived")
	tok, _ := rt.StorageManager().GetOAuthToken(oauth.GenerateServerKey(sc.Name, sc.URL))
	require.Nil(t, tok, "removed server's OAuth token survived")

	// Same-name create-only add must not inherit an approved baseline.
	re := r6Server(sc.Name, sc.URL, false)
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(re))
	rec, _ = rt.StorageManager().GetToolApproval(sc.Name, "t1")
	require.Nil(t, rec)
}

func TestConfigDrivenRemoval_PurgesApprovalsAndOAuth_EmptyRemainingSet(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r6Server("srv-a", "http://127.0.0.1:1/mcp", false)
	seedSecurityState(t, rt, a)

	_, err := rt.ApplyConfig(validConfigWith(rt), "")
	require.NoError(t, err)
	waitServerGone(t, rt, "srv-a")
	time.Sleep(200 * time.Millisecond)
	assertSecurityStateGone(t, rt, a)
}

func TestConfigDrivenRemoval_PurgesApprovalsAndOAuth_SaveResurrection(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	keep := r6Server("keep", "http://127.0.0.1:2/mcp", false)
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(keep))
	a := r6Server("srv-a", "http://127.0.0.1:1/mcp", false)
	seedSecurityState(t, rt, a)

	parked := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	serverRemovalCleanupHook = func(n string) {
		if n != "srv-a" {
			return
		}
		once.Do(func() { close(parked); <-gate })
	}
	t.Cleanup(func() { serverRemovalCleanupHook = nil })

	_, err := rt.ApplyConfig(validConfigWith(rt, keep), "")
	require.NoError(t, err)
	select {
	case <-parked:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup never started")
	}
	// An unrelated save resurrects srv-a (still in storage) into the live config.
	require.NoError(t, rt.SaveConfiguration())
	require.True(t, serverNames(rt.Config())["srv-a"])
	close(gate)

	waitServerGone(t, rt, "srv-a")
	time.Sleep(200 * time.Millisecond)
	assertSecurityStateGone(t, rt, a)
}
