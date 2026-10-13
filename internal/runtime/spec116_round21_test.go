package runtime

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// Review rounds 19-20 (High): the telemetry notification runs after the commit
// lock is released. A commit parked in its follow-up work (here on
// profileIndexMu, rebuilding the profiles of a server it disabled) must not
// install its older configuration over a newer commit's opt-out.
func TestCommit_R21_TelemetryOptOutFollowsCommitOrder(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")
	// The opt-out beacon goes to a local endpoint, never the real one.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	rt := newFunnelRuntime(t)
	enabled := true
	start := nextConfig(t, rt)
	start.Telemetry = &config.TelemetryConfig{Enabled: &enabled, Endpoint: srv.URL}
	_, err := rt.ApplyConfig(start, rt.ConfigPath())
	require.NoError(t, err)
	rt.telemetryService = telemetry.New(rt.Config(), rt.ConfigPath(), "v0.0.0-test", "personal", zap.NewNop())
	require.True(t, rt.telemetryService.Active(), "fixture starts with telemetry enabled")

	rt.profileIndexMu.Lock()
	locked := true
	defer func() {
		if locked {
			rt.profileIndexMu.Unlock()
		}
	}()

	// Commit A keeps telemetry enabled and disables profiled server a, so its
	// follow-up work parks on the profile rebuild before notifying telemetry.
	older := nextConfig(t, rt)
	for _, sc := range older.Servers {
		if sc.Name == "a" {
			sc.Enabled = false
		}
	}
	olderDone := make(chan error, 1)
	go func() {
		_, err := rt.ApplyConfig(older, rt.ConfigPath())
		olderDone <- err
	}()
	require.Eventually(t, func() bool {
		sc := spec116Live(rt, "a")
		return sc != nil && !sc.Enabled
	}, 10*time.Second, 2*time.Millisecond, "commit A publishes")

	// Commit B opts out of telemetry and completes.
	disabled := false
	newer := nextConfig(t, rt)
	newer.Telemetry.Enabled = &disabled
	_, err = rt.ApplyConfig(newer, rt.ConfigPath())
	require.NoError(t, err)
	require.False(t, rt.Config().IsTelemetryEnabled())
	require.False(t, rt.telemetryService.Active(), "commit B's opt-out is applied")

	rt.profileIndexMu.Unlock()
	locked = false
	require.NoError(t, spec116AwaitErr(t, olderDone, "commit A"))

	assert.False(t, rt.Config().IsTelemetryEnabled())
	assert.False(t, rt.telemetryService.Active(),
		"a stale notification from an older commit must not re-enable telemetry")
}

// tasks.md T029a / FR-009a: an UnderApprovalLock hook runs while the commit
// holds the server's approval lock, and the lock is held through the row
// write: a competing approval operation waits, then sees the committed row.
func TestCommit_R21_UnderApprovalLockHoldsLockThroughRowWrite(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, unreachableHTTP("hooked"))
	const newURL = "http://127.0.0.1:9/mcp-hooked"

	entered := make(chan struct{})
	release := make(chan struct{})
	commitDone := make(chan error, 1)
	go func() {
		_, err := rt.Commit("hook_test", func(tx *CommitTx) error {
			if err := tx.UpdateServer("hooked", func(cur *config.ServerConfig) error {
				cur.URL = newURL
				return nil
			}); err != nil {
				return err
			}
			return tx.UnderApprovalLock("hooked", func() error {
				close(entered)
				<-release
				return nil
			})
		})
		commitDone <- err
	}()
	spec116Await(t, entered, "the hook to run")

	var sawURL atomic.Value
	competitorDone := make(chan error, 1)
	go func() {
		competitorDone <- rt.WithToolApprovalLock("hooked", func() error {
			if sc := spec116Stored(t, rt, "hooked"); sc != nil {
				sawURL.Store(sc.URL)
			}
			return nil
		})
	}()
	returned, _ := spec116AwaitReturnOrBlocked(t, competitorDone, func() bool { return rt.approvalLockWaiters("hooked") > 0 })
	require.False(t, returned, "a competing approval operation must wait while the hook runs")

	close(release)
	require.NoError(t, spec116AwaitErr(t, commitDone, "commit"))
	require.NoError(t, spec116AwaitErr(t, competitorDone, "competitor"))
	assert.Equal(t, newURL, sawURL.Load(), "the lock is held until the commit's row write is done")
}

// tasks.md T029a: a hook error aborts the commit with nothing written — disk
// bytes, stored rows, the published config and its generation are unchanged —
// and every lock the commit took is released.
func TestCommit_R21_UnderApprovalLockErrorAbortsWithoutWrites(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, nil, unreachableHTTP("hooked"), unreachableHTTP("other"))
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	snapBefore := rt.configSvc.Current()
	storedBefore := spec116Stored(t, rt, "hooked")
	require.NotNil(t, storedBefore)
	otherBefore := spec116Stored(t, rt, "other")
	require.NotNil(t, otherBefore)

	hookErr := errors.New("review is stale")
	_, err = rt.Commit("hook_error", func(tx *CommitTx) error {
		if err := tx.UpdateServer("hooked", func(cur *config.ServerConfig) error {
			cur.URL = "http://127.0.0.1:9/changed"
			return nil
		}); err != nil {
			return err
		}
		if err := tx.SetQuarantined("other", !otherBefore.Quarantined); err != nil {
			return err
		}
		return tx.UnderApprovalLock("hooked", func() error { return hookErr })
	})
	require.ErrorIs(t, err, hookErr)

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "disk bytes unchanged")
	assert.Equal(t, snapBefore.Version, rt.configSvc.Current().Version, "generation unchanged")
	assert.Same(t, snapBefore, rt.configSvc.Current(), "published snapshot unchanged")
	assert.Equal(t, storedBefore.URL, spec116Stored(t, rt, "hooked").URL, "row unchanged")
	assert.Equal(t, otherBefore.Quarantined, spec116Stored(t, rt, "other").Quarantined, "other row unchanged")
	assert.Equal(t, storedBefore.URL, spec116Live(rt, "hooked").URL, "live config unchanged")

	// Every lock was released: the approval locks and the commit lock.
	lockFree := make(chan error, 1)
	go func() {
		lockFree <- rt.WithToolApprovalLock("hooked", func() error {
			return rt.WithToolApprovalLock("other", func() error { return nil })
		})
	}()
	require.NoError(t, spec116AwaitErr(t, lockFree, "approval locks to be free"))
	_, err = rt.Commit("after_hook_error", func(tx *CommitTx) error {
		return tx.UpdateServer("hooked", func(cur *config.ServerConfig) error {
			cur.URL = "http://127.0.0.1:9/after"
			return nil
		})
	})
	require.NoError(t, err, "the commit lock is free after a hook error")
	assert.Equal(t, "http://127.0.0.1:9/after", spec116Stored(t, rt, "hooked").URL)
}
