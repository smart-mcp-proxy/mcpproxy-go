package runtime

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// UX-02 (QA 0.70.0): several discovery triggers (reactive connect, boot sweep,
// post-reload sweep, HandleUpstreamServerChange, approval reindex) can run
// checkToolApprovals for the same server at once, and operator approvals
// write the same records. Without per-server serialization of that
// read-modify-write, a stale pass overwrites a fresher record and tools are
// stranded pending for good. These tests drive the interleavings
// deterministically through toolApprovalReadHook.

const ux02HookWait = 300 * time.Millisecond

func ux02Tools(server string, n int) []*config.ToolMetadata {
	out := make([]*config.ToolMetadata, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, &config.ToolMetadata{
			ServerName:  server,
			Name:        fmt.Sprintf("read_%03d", i),
			Description: fmt.Sprintf("Read records %d", i),
			ParamsJSON:  `{"type":"object"}`,
		})
	}
	return out
}

func ux02ToolNames(tools []*config.ToolMetadata) []string {
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	return names
}

func ux02NotApproved(t *testing.T, rt *Runtime, server string) []string {
	t.Helper()
	recs, err := rt.storageManager.ListToolApprovals(server)
	require.NoError(t, err)
	var out []string
	for _, r := range recs {
		if r.Status != storage.ToolApprovalStatusApproved {
			out = append(out, r.ToolName+"="+r.Status)
		}
	}
	return out
}

// waitOrTimeout reports whether ch was closed within d.
func waitOrTimeout(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// A baseline pass (A) and a later non-baseline pass (B) overlap: A has saved a
// few approved records when B snapshots, so B decides "no baseline" and files
// the rest as new pending tools, overwriting A's approvals. The test pauses A
// mid-pass, lets B read a tool A has not reached yet, lets A finish, then
// releases B. Unserialized, B's check-then-insert clobbers A's approval.
func TestCheckToolApprovals_OverlappingBaselinePasses_NoStrandedPending(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "lib", Enabled: true}})
	tools := ux02Tools("lib", 10)

	var phase atomic.Int32 // 0: pause A at read_005; 1: pause B at read_006; 2: no pauses
	aPaused, releaseA := make(chan struct{}), make(chan struct{})
	bPaused, releaseB := make(chan struct{}), make(chan struct{})
	rt.toolApprovalReadHook = func(_, tool string) {
		switch {
		case phase.Load() == 0 && tool == "read_005":
			close(aPaused)
			<-releaseA
		case phase.Load() == 1 && tool == "read_006":
			close(bPaused)
			<-releaseB
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := rt.checkToolApprovals("lib", tools)
		require.NoError(t, err)
	}()
	require.True(t, waitOrTimeout(aPaused, 5*time.Second), "pass A never reached read_005")

	phase.Store(1)
	go func() {
		defer wg.Done()
		_, err := rt.checkToolApprovals("lib", tools)
		require.NoError(t, err)
	}()
	bReachedRead := waitOrTimeout(bPaused, ux02HookWait)

	phase.Store(2)
	close(releaseA)
	if bReachedRead {
		// Unserialized: give A time to finish its writes before B's stale write.
		time.Sleep(100 * time.Millisecond)
	}
	close(releaseB)
	wg.Wait()

	require.Empty(t, ux02NotApproved(t, rt, "lib"), "a fresh trusted baseline must approve the whole captured toolset")
	require.False(t, bReachedRead, "a second discovery pass must not read approval records while another pass for the same server is in flight")
}

// An operator approval lands while a discovery pass on a quarantined server
// holds a stale copy of a pending record. Unserialized, the pass's
// "stays pending" write-back reverts the approval.
func TestApproveTools_DuringDiscoveryPass_ApprovalNotLost(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "srv", Enabled: true, Quarantined: true}})
	tools := ux02Tools("srv", 10)
	_, err := rt.checkToolApprovals("srv", tools) // every tool filed pending
	require.NoError(t, err)

	paused, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rt.toolApprovalReadHook = func(_, tool string) {
		if tool == "read_003" {
			once.Do(func() {
				close(paused)
				<-release
			})
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, passErr := rt.checkToolApprovals("srv", tools)
		require.NoError(t, passErr)
	}()
	require.True(t, waitOrTimeout(paused, 5*time.Second), "discovery pass never reached read_003")

	approveDone := make(chan struct{})
	go func() {
		defer close(approveDone)
		require.NoError(t, rt.ApproveTools("srv", ux02ToolNames(tools), "test"))
	}()
	approveFinishedEarly := waitOrTimeout(approveDone, ux02HookWait)

	close(release)
	wg.Wait()
	<-approveDone

	require.Empty(t, ux02NotApproved(t, rt, "srv"), "an approval must not be reverted by a concurrent discovery write-back")
	require.False(t, approveFinishedEarly, "ApproveTools must wait for an in-flight discovery pass on the same server")
}

// Concurrent passes with no hook: the realistic shape seen live (three
// passes at boot). Probabilistic on its own; the deterministic tests above pin
// the interleavings.
func TestCheckToolApprovals_ConcurrentBaselinePasses_Stress(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "lib", Enabled: true}})
	tools := ux02Tools("lib", 60)

	var wg sync.WaitGroup
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := rt.checkToolApprovals("lib", tools)
			require.NoError(t, err)
		}()
	}
	wg.Wait()

	recs, err := rt.storageManager.ListToolApprovals("lib")
	require.NoError(t, err)
	require.Len(t, recs, len(tools))
	require.Empty(t, ux02NotApproved(t, rt, "lib"))
}

// The quarantine decision a pass acts on must be read under the same
// serialization as its writes. QuarantineServer flips storage first and the
// runtime config only later, so a storage-level quarantine must stop a pass
// from auto-baselining the server.
func TestCheckToolApprovals_StorageQuarantineBlocksBaseline(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "lib", Enabled: true}})
	require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{Name: "lib", Enabled: true, Quarantined: true}))

	res, err := rt.checkToolApprovals("lib", ux02Tools("lib", 3))
	require.NoError(t, err)
	require.Equal(t, 3, res.PendingCount, "a server quarantined in storage must never auto-baseline")
	require.Len(t, ux02NotApproved(t, rt, "lib"), 3)
}

// QuarantineServer's storage flip waits for an in-flight pass, so a pass can
// never act on a quarantine decision older than its own writes.
func TestQuarantineServer_FlipWaitsForInFlightPass(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "lib", Enabled: true}})
	require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{Name: "lib", Enabled: true}))

	paused, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rt.toolApprovalReadHook = func(_, _ string) {
		once.Do(func() {
			close(paused)
			<-release
		})
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = rt.checkToolApprovals("lib", ux02Tools("lib", 3))
	}()
	require.True(t, waitOrTimeout(paused, 5*time.Second))

	flipDone := make(chan struct{})
	go func() {
		defer close(flipDone)
		_ = rt.QuarantineServer("lib", true) // config save may fail in this harness; the storage flip is what matters
	}()
	time.Sleep(ux02HookWait)
	srv, err := rt.storageManager.GetUpstreamServer("lib")
	require.NoError(t, err)
	flippedEarly := srv.Quarantined

	close(release)
	wg.Wait()
	<-flipDone
	require.False(t, flippedEarly, "the quarantine flip must wait for the in-flight approval pass")
	srv, err = rt.storageManager.GetUpstreamServer("lib")
	require.NoError(t, err)
	require.True(t, srv.Quarantined)
}

// Baseline promotion on server approval re-reads each record under the lock
// and never promotes a record that is now "changed" (rug-pull guarantee),
// even if it was listed as pending.
func TestApproveToolsLocked_PendingOnlySkipsChanged(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "srv", Enabled: true, Quarantined: true}})
	tools := ux02Tools("srv", 2)
	_, err := rt.checkToolApprovals("srv", tools)
	require.NoError(t, err)
	rec, err := rt.storageManager.GetToolApproval("srv", "read_001")
	require.NoError(t, err)
	rec.Status = storage.ToolApprovalStatusChanged
	require.NoError(t, rt.storageManager.SaveToolApproval(rec))

	unlock := rt.lockToolApprovals("srv")
	n, err := rt.approveToolsLocked("srv", ux02ToolNames(tools), "system:server-approval-baseline", true)
	unlock()
	require.NoError(t, err)
	require.Equal(t, 1, n)
	got, err := rt.storageManager.GetToolApproval("srv", "read_001")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusChanged, got.Status)
	got, err = rt.storageManager.GetToolApproval("srv", "read_000")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusApproved, got.Status)
}

// The scanner's atomic baseline+blocks write is serialized through
// WithToolApprovalLock: it waits for an in-flight pass on that server, and
// one server's lock never stalls another server.
func TestWithToolApprovalLock_WaitsForInFlightPass(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "srv", Enabled: true, Quarantined: true}})
	paused, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rt.toolApprovalReadHook = func(_, _ string) {
		once.Do(func() {
			close(paused)
			<-release
		})
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = rt.checkToolApprovals("srv", ux02Tools("srv", 2))
	}()
	require.True(t, waitOrTimeout(paused, 5*time.Second))

	ran := make(chan struct{})
	go func() {
		_ = rt.WithToolApprovalLock("srv", func() error {
			close(ran)
			return nil
		})
	}()
	ranEarly := waitOrTimeout(ran, ux02HookWait)
	close(release)
	wg.Wait()
	require.True(t, waitOrTimeout(ran, 5*time.Second))
	require.False(t, ranEarly, "WithToolApprovalLock must wait for an in-flight pass")

	unlock := rt.lockToolApprovals("srv")
	defer unlock()
	done := make(chan struct{})
	go func() {
		_ = rt.WithToolApprovalLock("other", func() error { return nil })
		close(done)
	}()
	require.True(t, waitOrTimeout(done, 2*time.Second), "locks are per server")
}
