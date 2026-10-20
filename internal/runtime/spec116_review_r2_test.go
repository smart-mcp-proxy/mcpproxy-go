package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/supervisor"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Spec 116 PR1, review round 2 (Sol) regressions.

func r2CaptureStamped(rt *Runtime, name string) bool {
	rt.lastGoodToolsMu.RLock()
	defer rt.lastGoodToolsMu.RUnlock()
	_, ok := rt.lastCaptureAt[name]
	return ok
}

// R2.2 (FR-008a, D15): a discovery result for a server that is NOT eligible
// for indexing (it goes to the review capture, not the index) is generation
// checked too. A tools/list in flight at generation G whose server is then
// quarantined by a commit at G+1 must not become the review snapshot: no
// last-good set, no capture stamp, no review records. A capture from the
// current generation is still recorded.
func TestCommit_R2_StaleIneligibleDiscoveryWritesNoReviewCapture(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil,
		explicitServer(&config.ServerConfig{Name: "omega", Command: "true", Protocol: "stdio", Enabled: true}),
	)
	oldGen := storedStamp(t, rt, "omega").Stamp.Gen
	_, err := rt.Commit("quarantine_toggle", func(tx *CommitTx) error {
		if err := tx.UpdateServer("omega", func(cur *config.ServerConfig) error {
			cur.Args = []string{"new-endpoint"}
			return nil
		}); err != nil {
			return err
		}
		return tx.SetQuarantined("omega", true)
	})
	require.NoError(t, err)
	newGen := storedStamp(t, rt, "omega").Stamp.Gen
	require.NotEqual(t, oldGen, newGen)

	tools := []*config.ToolMetadata{
		{ServerName: "omega", Name: "old_tool", Description: "from the old endpoint", ParamsJSON: `{"type":"object"}`, Hash: "h-old"},
	}
	capture := rt.discoveryGeneration("omega")
	eligible, published, err := rt.commitDiscoveredToolsAt(context.Background(), "omega", tools, oldGen, &capture)
	require.NoError(t, err)
	assert.False(t, eligible)
	assert.False(t, published)
	assert.Nil(t, rt.lastGoodToolsSnapshot("omega"), "a stale pass must not become the review snapshot")
	assert.False(t, r2CaptureStamped(rt, "omega"), "a stale pass must not stamp a capture")
	records, err := rt.storageManager.ListToolApprovals("omega")
	require.NoError(t, err)
	assert.Empty(t, records, "a stale pass must not write review records")

	// The current generation's capture is recorded for review (not indexed).
	cur := []*config.ToolMetadata{
		{ServerName: "omega", Name: "new_tool", Description: "from the new endpoint", ParamsJSON: `{"type":"object"}`, Hash: "h-new"},
	}
	eligible, _, err = rt.commitDiscoveredToolsAt(context.Background(), "omega", cur, newGen, &capture)
	require.NoError(t, err)
	assert.False(t, eligible)
	snap := rt.lastGoodToolsSnapshot("omega")
	require.Len(t, snap, 1)
	assert.Equal(t, "new_tool", snap[0].Name)
	assert.True(t, r2CaptureStamped(rt, "omega"))
	indexed, err := rt.indexManager.GetToolsByServer("omega")
	require.NoError(t, err)
	assert.Empty(t, indexed, "a quarantined server is never indexed")
}

// R2.3 (FR-008a): the empty-result discovery stamp (ToolsDiscovered /
// DiscoveryEpoch) is generation checked under the approval lock, on both the
// sweep and the lenient single-server path: a stale empty tools/list captured
// at G cannot mark discovery complete after a connection-changing commit at
// G+1.
func TestCommit_R2_StaleEmptyDiscoveryDoesNotMarkDiscovered(t *testing.T) {
	for _, path := range []string{"sweep", "single"} {
		t.Run(path, func(t *testing.T) {
			rt, _ := newSpec116Runtime(t, nil,
				explicitServer(&config.ServerConfig{Name: "omega", Command: "true", Protocol: "stdio", Enabled: true}),
			)
			// Give the StateView an entry for the server, as a connect does.
			rt.supervisor.StateView().UpdateServer("omega", func(st *stateview.ServerStatus) {
				st.Name, st.Enabled, st.Connected = "omega", true, true
				st.Tools, st.ToolsDiscovered = nil, false
			})
			oldGen := storedStamp(t, rt, "omega").Stamp.Gen
			gen := rt.discoveryGeneration("omega")
			_, err := rt.Commit("update_server", func(tx *CommitTx) error {
				return tx.UpdateServer("omega", func(cur *config.ServerConfig) error {
					cur.Args = []string{"new-endpoint"}
					return nil
				})
			})
			require.NoError(t, err)
			newGen := storedStamp(t, rt, "omega").Stamp.Gen
			require.NotEqual(t, oldGen, newGen)

			mark := func(instanceGen int64) {
				if path == "sweep" {
					rt.markZeroToolServersDiscovered([]string{"omega"}, nil,
						map[string]supervisor.DiscoveryCapture{"omega": gen}, map[string]int64{"omega": instanceGen}, nil)
				} else {
					rt.markServerZeroToolsDiscovered("omega", gen, instanceGen, managed.InventoryCapture{})
				}
			}
			discovered := func() bool {
				st, ok := rt.supervisor.StateView().GetServer("omega")
				require.True(t, ok)
				return st.ToolsDiscovered
			}

			mark(oldGen)
			assert.False(t, discovered(), "a stale empty result must not mark discovery complete")
			mark(newGen)
			assert.True(t, discovered(), "the current generation's empty result marks discovery complete")
		})
	}
}

// r2EventRecorder counts servers.changed submissions (before the coalescer
// merges them) and review.changed publishes for one server.
type r2EventRecorder struct {
	mu      sync.Mutex
	servers []map[string]any // extra of each servers.changed with the watched reason
	reviews int
}

func r2RecordEvents(t *testing.T, rt *Runtime, server, reason string) (*r2EventRecorder, func()) {
	t.Helper()
	rec := &r2EventRecorder{}
	fn := func(r string, extra map[string]any) {
		if r != reason {
			return
		}
		rec.mu.Lock()
		rec.servers = append(rec.servers, extra)
		rec.mu.Unlock()
	}
	rt.serversChangedHook.Store(&fn)
	t.Cleanup(func() { rt.serversChangedHook.Store(nil) })
	sub := rt.SubscribeEvents()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case evt := <-sub:
				if evt.Type == EventTypeReviewChanged && evt.Payload["server"] == server {
					rec.mu.Lock()
					rec.reviews++
					rec.mu.Unlock()
				}
			}
		}
	}()
	return rec, func() {
		// Let the after-unlock work and any goroutine it spawned settle.
		time.Sleep(400 * time.Millisecond)
		close(stop)
		<-done
	}
}

// R2.5 (FR-028, contracts/surfaces.md "SSE events"): one quarantine, enable
// or bulk-enable transition sends servers.changed and review.changed at most
// once per commit and reason — the commit's own emission, carrying the
// caller's payload fields — and never a second, caller-side copy.
func TestCommit_R2_EventsOncePerCommit(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	t.Run("quarantine", func(t *testing.T) {
		rt, _ := newSpec116Runtime(t, nil,
			explicitServer(&config.ServerConfig{Name: "omega", Command: "true", Protocol: "stdio", Enabled: false}),
		)
		rec, wait := r2RecordEvents(t, rt, "omega", "quarantine_toggle")
		require.NoError(t, rt.QuarantineServer("omega", true))
		wait()
		rec.mu.Lock()
		defer rec.mu.Unlock()
		assert.Equal(t, 1, rec.reviews, "exactly one review.changed per quarantine transition")
		require.Len(t, rec.servers, 1, "exactly one servers.changed per quarantine commit")
		assert.Equal(t, "omega", rec.servers[0]["server"])
		assert.Equal(t, true, rec.servers[0]["quarantined"])
	})
	t.Run("enable", func(t *testing.T) {
		rt, _ := newSpec116Runtime(t, nil,
			explicitServer(&config.ServerConfig{Name: "omega", Command: "true", Protocol: "stdio", Enabled: true}),
		)
		rec, wait := r2RecordEvents(t, rt, "omega", "enable_toggle")
		require.NoError(t, rt.EnableServer("omega", false))
		wait()
		rec.mu.Lock()
		defer rec.mu.Unlock()
		require.Len(t, rec.servers, 1, "exactly one servers.changed per enable commit")
		assert.Equal(t, "omega", rec.servers[0]["server"])
		assert.Equal(t, false, rec.servers[0]["enabled"])
	})
	t.Run("bulk_enable", func(t *testing.T) {
		rt, _ := newSpec116Runtime(t, nil,
			explicitServer(&config.ServerConfig{Name: "omega", Command: "true", Protocol: "stdio", Enabled: true}),
			explicitServer(&config.ServerConfig{Name: "sigma", Command: "true", Protocol: "stdio", Enabled: true}),
		)
		rec, wait := r2RecordEvents(t, rt, "omega", "bulk_enable_toggle")
		errs, err := rt.BulkEnableServers([]string{"omega", "sigma"}, false)
		require.NoError(t, err)
		require.Empty(t, errs)
		wait()
		rec.mu.Lock()
		defer rec.mu.Unlock()
		require.Len(t, rec.servers, 1, "exactly one servers.changed per bulk commit")
		assert.Equal(t, false, rec.servers[0]["enabled"])
		assert.Equal(t, 2, rec.servers[0]["count"])
	})
}
