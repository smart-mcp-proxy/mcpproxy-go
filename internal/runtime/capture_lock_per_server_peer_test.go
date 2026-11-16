package runtime

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// peerToolNames returns the raw tool names collectPeerToolMetadata reports
// for server.
func peerToolNames(peers map[string][]*config.ToolMetadata, server string) []string {
	var names []string
	for _, t := range peers[server] {
		names = append(names, t.RawName)
	}
	return names
}

// With per-server capture locks, two servers' discovery passes overlap. A pass
// publishes its tools to the StateView only at its end, so the peer context of
// another server's scan must also include in-flight inventories, or two
// near-duplicate tools added at once both scan clean.
func TestCollectPeerToolMetadata_IncludesOtherServersInflightInventory(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{scanServer("a"), scanServer("b")})

	require.Nil(t, rt.collectPeerToolMetadata("a"), "no peer has published or in-flight tools yet")

	unregisterB := rt.registerInflightInventory("b", []*config.ToolMetadata{
		{ServerName: "b", Name: "sync_database", RawName: "sync_database", Description: "b desc", ParamsJSON: `{"type":"object"}`},
	})
	unregisterA := rt.registerInflightInventory("a", []*config.ToolMetadata{
		{ServerName: "a", Name: "own_tool", RawName: "own_tool", Description: "a desc"},
	})

	peers := rt.collectPeerToolMetadata("a")
	assert.Equal(t, []string{"sync_database"}, peerToolNames(peers, "b"), "b's in-flight inventory is peer context for a")
	assert.Empty(t, peers["a"], "a's own in-flight inventory is never its own peer")
	require.Len(t, peers["b"], 1)
	got := peers["b"][0]
	assert.Equal(t, "b", got.ServerName)
	assert.Equal(t, "sync_database", got.Name)
	assert.Equal(t, "b desc", got.Description)
	assert.Equal(t, `{"type":"object"}`, got.ParamsJSON)

	peersOfB := rt.collectPeerToolMetadata("b")
	assert.Equal(t, []string{"own_tool"}, peerToolNames(peersOfB, "a"))
	assert.Empty(t, peersOfB["b"])

	unregisterB()
	unregisterB() // idempotent
	peers = rt.collectPeerToolMetadata("a")
	assert.Empty(t, peers["b"], "a finished pass is no longer in flight")

	unregisterA()
	assert.Nil(t, rt.collectPeerToolMetadata("b"))
}

// A stale unregister (from a pass that already finished) must not remove the
// registration of the server's next pass.
func TestRegisterInflightInventory_StaleUnregisterKeepsNewerPass(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{scanServer("a"), scanServer("b")})

	first := rt.registerInflightInventory("b", []*config.ToolMetadata{{ServerName: "b", Name: "old"}})
	second := rt.registerInflightInventory("b", []*config.ToolMetadata{{ServerName: "b", Name: "new"}})
	first()
	assert.Equal(t, []string{"new"}, peerToolNames(rt.collectPeerToolMetadata("a"), "b"))
	second()
	assert.Nil(t, rt.collectPeerToolMetadata("a"))
}

// Two scan-mode servers gain a same-name, near-duplicate tool in overlapping
// discovery passes. Before captureSnapMu became per server the passes were
// serialized, so the second one's scan saw the first's published tool and
// held its clone. With per-server locks, server a's pass is paused after its
// approval check (before it publishes); server b's whole pass runs in that
// window and must still see a's new tool as a peer and hold its own clone.
func TestOverlappingDiscoveryPasses_SecondScanSeesInflightPeer(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{scanServer("a"), scanServer("b")})
	const shared = "sync_database"
	aDescription := "Synchronize production database records to nightly encrypted cold storage backup bucket automatically without manual intervention"
	bDescription := "Synchronize database records to production nightly encrypted cold storage backup bucket automatically without manual intervention"
	schema := `{"type":"object"}`

	seedApprovedBaseline(t, rt, "a", "a_baseline", "Lists the a things", schema)
	seedApprovedBaseline(t, rt, "b", "b_baseline", "Lists the b things", schema)

	paused, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rt.testHooks.discoveryBeforeIndex = func(name string) {
		if name != "a" {
			return
		}
		once.Do(func() {
			close(paused)
			<-release
		})
	}

	ctx := context.Background()
	aDone := make(chan error, 1)
	go func() {
		aDone <- rt.applyDifferentialToolUpdate(ctx, "a", []*config.ToolMetadata{
			{ServerName: "a", Name: "a_baseline", RawName: "a_baseline", Description: "Lists the a things", ParamsJSON: schema},
			{ServerName: "a", Name: shared, RawName: shared, Description: aDescription, ParamsJSON: schema},
		})
	}()
	select {
	case <-paused:
	case <-time.After(10 * time.Second):
		t.Fatal("a's pass never reached its index step")
	}
	released := false
	defer func() {
		if !released {
			close(release)
			<-aDone
		}
	}()

	bErr := make(chan error, 1)
	go func() {
		bErr <- rt.applyDifferentialToolUpdate(ctx, "b", []*config.ToolMetadata{
			{ServerName: "b", Name: "b_baseline", RawName: "b_baseline", Description: "Lists the b things", ParamsJSON: schema},
			{ServerName: "b", Name: shared, RawName: shared, Description: bDescription, ParamsJSON: schema},
		})
	}()
	select {
	case err := <-bErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("b's pass blocked behind a's paused pass")
	}

	rec, err := rt.storageManager.GetToolApproval("b", shared)
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status,
		"b's near-duplicate of a's in-flight tool must be held, as it was when the passes were serialized")

	released = true
	close(release)
	require.NoError(t, <-aDone)
}

// reconcileSignatureCache enumerates the index and then evicts every cached
// hash it did not see. A concurrent pass that indexes and warms a new hash
// between those two steps must not lose it.
func TestReconcileSignatureCache_DoesNotEvictConcurrentlyWarmedHash(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, nil)
	require.NotNil(t, rt.sigCache)
	require.NotNil(t, rt.indexManager)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				rt.reconcileSignatureCache()
			}
		}
	}()

	var warmed []*config.ToolMetadata
	deadline := time.Now().Add(2 * time.Second)
	for i := 0; i < 400 && time.Now().Before(deadline); i++ {
		server := fmt.Sprintf("s%d", i%4)
		tool := &config.ToolMetadata{
			ServerName:  server,
			Name:        fmt.Sprintf("tool_%d", i),
			RawName:     fmt.Sprintf("tool_%d", i),
			Description: fmt.Sprintf("tool number %d", i),
			ParamsJSON:  fmt.Sprintf(`{"type":"object","properties":{"p%d":{"type":"string"}}}`, i),
			Hash:        fmt.Sprintf("hash-%d", i),
		}
		require.NoError(t, rt.indexManager.BatchIndexTools([]*config.ToolMetadata{tool}))
		rt.warmSignatureCache([]*config.ToolMetadata{tool})
		warmed = append(warmed, tool)
	}
	close(stop)
	wg.Wait()

	var lost []string
	for _, tool := range warmed {
		if _, ok := rt.sigCache.Peek(tool.Hash); !ok {
			lost = append(lost, tool.Hash)
		}
	}
	assert.Empty(t, lost, "indexed and warmed hashes were evicted by a concurrent reconcile")
}

// An in-flight tool replaces the peer's published tool of the same raw name;
// published tools the in-flight inventory does not carry stay (the union is
// what the scan would see with the peer's pass ordered before or after it).
func TestOverlayInflightPeers_UnionKeepsPublishedContract(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{scanServer("a"), scanServer("b")})
	unregister := rt.registerInflightInventory("b", []*config.ToolMetadata{
		{ServerName: "b", Name: "changed", RawName: "changed", Description: "new"},
		{ServerName: "b", Name: "added", RawName: "added", Description: "added"},
		{ServerName: "other", Name: "foreign", RawName: "foreign"}, // not b's: dropped like the publish drops it
	})
	defer unregister()

	peers := map[string][]*config.ToolMetadata{
		"b": {
			{ServerName: "b", Name: "changed", RawName: "changed", Description: "old"},
			{ServerName: "b", Name: "kept", RawName: "kept", Description: "kept"},
		},
		"c": {{ServerName: "c", Name: "c_tool", RawName: "c_tool"}},
	}
	overlayInflightPeers(rt.inflightPeerInventories("a"), peers)

	descs := map[string][]string{}
	for _, tool := range peers["b"] {
		descs[tool.RawName] = append(descs[tool.RawName], tool.Description)
	}
	// A replacement is not yet published and its pass may fail: BOTH contracts
	// stay in the peer context, so a clone of either is seen.
	assert.Equal(t, []string{"old", "new"}, descs["changed"], "published contract first: the detector reads a server's first entry per name")
	assert.Equal(t, []string{"added"}, descs["added"])
	assert.Equal(t, []string{"kept"}, descs["kept"])
	assert.Len(t, peers["c"], 1, "servers without an in-flight inventory are untouched")
	assert.Empty(t, rt.inflightPeerInventories("b"), "a server never sees its own in-flight inventory")

	// An unchanged in-flight tool is listed once.
	peers = map[string][]*config.ToolMetadata{
		"b": {{ServerName: "b", Name: "added", RawName: "added", Description: "added"}},
	}
	overlayInflightPeers(rt.inflightPeerInventories("a"), peers)
	n := 0
	for _, tool := range peers["b"] {
		if tool.RawName == "added" {
			n++
		}
	}
	assert.Equal(t, 1, n, "identical published and in-flight contracts are deduplicated")
}
