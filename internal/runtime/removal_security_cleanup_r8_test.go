package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/supervisor"
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
	for _, st := range []string{"approved", "changed"} {
		require.NoError(t, rt.StorageManager().SavePromptApproval(&storage.PromptApprovalRecord{
			ServerName: sc.Name, PromptName: "p-" + st, ApprovedHash: "ph", CurrentHash: "ph", Status: st, ApprovedAt: time.Now(),
		}))
	}
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

	prompts, err := rt.StorageManager().ListPromptApprovals(sc.Name)
	require.NoError(t, err)
	require.Empty(t, prompts, "removed server's prompt approvals survived (UX-01 r9)")

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
	// An unrelated save must NOT republish srv-a (still in storage, pending
	// removal) into the live config (UX-01 r10).
	require.NoError(t, rt.SaveConfiguration())
	require.False(t, serverNames(rt.Config())["srv-a"], "save republished a server pending removal")
	close(gate)

	waitServerGone(t, rt, "srv-a")
	time.Sleep(200 * time.Millisecond)
	assertSecurityStateGone(t, rt, a)
}

func TestRemoveServerCommitted_PurgesApprovalsPromptsAndOAuth(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r6Server("srv-a", "http://127.0.0.1:1/mcp", false)
	seedSecurityState(t, rt, a)

	require.NoError(t, rt.RemoveServerCommitted("srv-a"))
	assertSecurityStateGone(t, rt, a)
}

// UX-01 r10: a server pending removal must not be accepted by the supervisor,
// even from a snapshot that still lists it, and an unrelated save must not
// republish it.
func TestPendingRemoval_SupervisorActionRefused_AndSaveDoesNotRepublish(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	keep := r6Server("keep", "http://127.0.0.1:2/mcp", false)
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(keep))
	a := r6Server("srv-a", "http://127.0.0.1:1/mcp", false)
	a.Enabled = true
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
	require.NoError(t, rt.SaveConfiguration())
	require.False(t, serverNames(rt.Config())["srv-a"])

	applied := false
	err = rt.guardSupervisorAction("srv-a", a, func(*config.ServerConfig) error { applied = true; return nil })
	require.ErrorIs(t, err, supervisor.ErrStaleAction)
	require.False(t, applied, "supervisor action ran for a server pending removal")

	close(gate)
	waitServerGone(t, rt, "srv-a")
	time.Sleep(200 * time.Millisecond)
	require.False(t, serverNames(rt.Config())["srv-a"])
	assertSecurityStateGone(t, rt, a)
}

// UX-01 r10: an approval writer is exclusive with a removal. While a removal
// holds the commit lock, ApproveTools waits; once the removal purged the
// records, it cannot write the removed server's baseline back.
func TestApproveTools_ExclusiveWithRemoval(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r6Server("srv-a", "http://127.0.0.1:1/mcp", false)
	seedSecurityState(t, rt, a)

	parked := make(chan struct{})
	gate := make(chan struct{})
	removeServerAfterStorageDeleteHook = func(string) { close(parked); <-gate }
	t.Cleanup(func() { removeServerAfterStorageDeleteHook = nil })

	removed := make(chan error, 1)
	go func() { removed <- rt.RemoveServerCommitted("srv-a") }()
	<-parked

	approveDone := make(chan error, 1)
	go func() { approveDone <- rt.ApproveTools("srv-a", []string{"t1"}, "test") }()
	select {
	case <-approveDone:
		t.Fatal("ApproveTools ran inside a removal's commit")
	case <-time.After(300 * time.Millisecond):
	}
	close(gate)
	require.NoError(t, <-removed)
	require.NoError(t, <-approveDone)
	assertSecurityStateGone(t, rt, a)
}

// UX-01 r10: an approval started against one incarnation of a server must not
// unquarantine (or write a baseline for) a same-name replacement.
func TestQuarantineServerAtEpoch_RefusesReplacement(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r6Server("srv-a", "http://127.0.0.1:1/mcp", false)
	a.Quarantined = true
	seedSecurityState(t, rt, a)
	epoch := rt.ServerRemovalEpoch("srv-a")

	require.NoError(t, rt.RemoveServerCommitted("srv-a"))
	re := r6Server("srv-a", "http://127.0.0.1:9/mcp", false)
	re.Quarantined = true
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(re))

	ran := false
	err := rt.QuarantineServerAtEpoch("srv-a", false, epoch, func() error { ran = true; return nil })
	require.ErrorIs(t, err, ErrServerGenerationChanged)
	require.False(t, ran, "baseline write ran for a replacement server")
	got, err := rt.StorageManager().GetUpstreamServer("srv-a")
	require.NoError(t, err)
	require.True(t, got.Quarantined, "replacement was unquarantined by the old server's approval")

	// A fresh epoch for the replacement is accepted.
	require.NoError(t, rt.QuarantineServerAtEpoch("srv-a", false, rt.ServerRemovalEpoch("srv-a"), func() error { ran = true; return nil }))
	require.True(t, ran)
}
