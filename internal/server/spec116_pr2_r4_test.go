package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage/purgetest"
)

// Spec 116 PR2 review round 4 (finding 1, FR-014c): a direct tool call whose
// gate read server A's record, and that a remove and same-name re-add (B,
// different configuration) overtakes before the call captured its
// incarnation, must not persist history built from A's record: the
// incarnation the history write is bound to is the one the gate read with
// the record, not B's.
func TestToolGate_R4_IncarnationReadWithRecord(t *testing.T) {
	a := &config.ServerConfig{Name: "acme", Command: "true", Args: []string{"a"}, Protocol: "stdio", Enabled: false}
	srv, _ := newSpec116Server(t, a)
	sm := srv.runtime.StorageManager()
	incA, ok := srv.runtime.ServerIncarnation("acme")
	require.True(t, ok)

	var once sync.Once
	srv.mcpProxy.toolGateAfterRecordRead = func(name string) {
		if name != "acme" {
			return
		}
		once.Do(func() {
			require.NoError(t, srv.RemoveServer(context.Background(), "acme"))
			b := &config.ServerConfig{Name: "acme", Command: "true", Args: []string{"b"}, Protocol: "stdio", Enabled: false}
			require.NoError(t, srv.AddServer(context.Background(), b))
		})
	}
	t.Cleanup(func() { srv.mcpProxy.toolGateAfterRecordRead = nil })

	gate := srv.mcpProxy.evaluateExactToolGate("acme", "echo")
	require.NotNil(t, gate.serverConfig, "the gate read A's record before the re-add")
	assert.Equal(t, []string{"a"}, gate.serverConfig.Args)
	incB, ok := srv.runtime.ServerIncarnation("acme")
	require.True(t, ok)
	require.NotEqual(t, incA, incB, "the re-add starts a new incarnation")
	assert.Equal(t, incA, gate.incarnation, "the gate's incarnation must be the one its record belongs to")

	// The dispatch records history from the gate's record, bound to the
	// gate's incarnation: nothing of A is recreated under B.
	rec := &storage.ToolCallRecord{
		ID: "r4-call", ServerID: storage.GenerateServerID(gate.serverConfig), ServerName: "acme",
		ToolName: "echo", Timestamp: time.Now(),
	}
	require.NoError(t, srv.mcpProxy.recordToolCallAt(rec, gate.incarnation))
	c := purgetest.Counts(t, sm, "acme")
	assert.Zero(t, c[purgetest.ToolCalls], "A's history bucket was recreated after the purge")
	assert.Zero(t, c[purgetest.Identities], "A's identity was recreated after the purge")
}

// Spec 116 PR2 review round 4 (finding 3, FR-017): an unquarantine sent
// without expected_incarnation is bound to the incarnation current when it
// arrives. Parked before its commit while the name is removed and re-added,
// it fails with server_replaced and the replacement stays quarantined.
func TestUnquarantine_R4_UnboundBindsAtArrival(t *testing.T) {
	srv, _ := newSpec116Server(t, pr2Manual("acme", true))
	hook, entered, release := pr2ParkOnce("acme")
	srv.runtime.SetApprovalAfterCaptureHookForTest(hook)
	t.Cleanup(func() { srv.runtime.SetApprovalAfterCaptureHookForTest(nil) })
	done := make(chan error, 1)
	go func() { done <- srv.QuarantineServer("acme", false) }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("the unbound unquarantine never captured an incarnation (returned %v)", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the unbound unquarantine never captured an incarnation")
	}

	require.NoError(t, srv.RemoveServer(context.Background(), "acme"))
	require.NoError(t, srv.AddServer(context.Background(), pr2Manual("acme", true)))
	close(release)
	err := awaitErr(t, done, "QuarantineServer")
	require.ErrorIs(t, err, runtime.ErrServerReplaced)
	assert.True(t, liveServer(srv, "acme").Quarantined, "the replacement stays quarantined")
}

// An unbound unquarantine of a name that is not configured is not found.
func TestUnquarantine_R4_MissingServerNotFound(t *testing.T) {
	srv, _ := newSpec116Server(t, pr2Manual("acme", true))
	err := srv.QuarantineServer("ghost", false)
	require.ErrorIs(t, err, runtime.ErrServerNotFound)
}
