package runtime

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Review round 6 (finding 1): the config funnel resolves its default
// destination INSIDE the commit. A MutateConfig that queued behind an
// ApplyConfig moving the config file from A to B must land on B: reading the
// path before the commit lock passed a stale A as an explicit destination,
// and the commit then republished A, silently reverting the path change.
func TestCommit_R6_FunnelResolvesPathInsideCommit(t *testing.T) {
	rt, pathA := newSpec116Runtime(t, nil, disabledStdio("a"))
	pathB := filepath.Join(t.TempDir(), "moved.json")
	aBefore, err := os.ReadFile(pathA)
	require.NoError(t, err)

	parked := make(chan struct{})
	release := make(chan struct{})
	var first atomic.Bool
	rt.testHooks.afterPrepare = func(string) {
		if first.CompareAndSwap(false, true) {
			close(parked)
			<-release
		}
	}

	moveDone := make(chan error, 1)
	go func() {
		_, err := rt.ApplyConfig(cloneRuntimeConfig(t, rt.Config()), pathB)
		moveDone <- err
	}()
	<-parked

	mutateDone := make(chan error, 1)
	go func() {
		_, _, err := rt.MutateConfig(context.Background(), Actor{}, func(desired *config.Config) (ChangeHint, error) {
			desired.ToolsLimit = 424
			return ChangeHint{}, nil
		}, TokenRewrite{})
		mutateDone <- err
	}()
	require.Eventually(t, func() bool { return rt.commitWaiters() > 0 }, 10*time.Second, time.Millisecond,
		"the mutation must block on the commit lock behind the path move")
	close(release)
	require.NoError(t, <-moveDone)
	require.NoError(t, <-mutateDone)
	rt.testHooks.afterPrepare = nil

	assert.Equal(t, pathB, rt.ConfigPath(), "published path after the mutation")
	rt.mu.RLock()
	assert.Equal(t, pathB, rt.cfgPath, "r.cfgPath after the mutation")
	rt.mu.RUnlock()

	onB, err := config.LoadFromFile(pathB)
	require.NoError(t, err)
	assert.Equal(t, 424, onB.ToolsLimit, "the mutation lands on B")
	aAfter, err := os.ReadFile(pathA)
	require.NoError(t, err)
	assert.Equal(t, string(aBefore), string(aAfter), "A never receives the mutation")

	// Later save and reload use B.
	require.NoError(t, os.Remove(pathB))
	require.NoError(t, rt.SaveConfiguration())
	_, err = os.Stat(pathB)
	require.NoError(t, err, "save writes B")
	onB, err = config.LoadFromFile(pathB)
	require.NoError(t, err)
	onB.ToolsLimit = 999
	require.NoError(t, config.SaveConfig(onB, pathB))
	require.NoError(t, rt.ReloadConfiguration())
	assert.Equal(t, 999, rt.Config().ToolsLimit, "reload reads B")
}
