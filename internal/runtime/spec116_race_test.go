package runtime

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func explicitServer(sc *config.ServerConfig) *config.ServerConfig {
	sc.MarkQuarantineExplicitlySet(true)
	return sc
}

// T1.2 (#1558): ApplyConfig quarantines and disables A; a toggle of B before
// any asynchronous work runs must not revert A in any store. On main the
// toggle's SaveConfiguration rebuilds the server list from storage rows the
// apply reconcile has not written yet; after Spec 116 the reconcile is part of
// the apply commit and the toggle waits for it.
func TestSpec116_T1_2_ApplyThenToggleKeepsQuarantine(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, nil,
		explicitServer(&config.ServerConfig{Name: "alpha", URL: "http://127.0.0.1:9/mcp", Protocol: "http", Enabled: true}),
		explicitServer(&config.ServerConfig{Name: "bravo", Command: "true", Protocol: "stdio", Enabled: false}),
	)
	probe := installSpec116ReconcileProbe(rt)
	defer probe.releaseParked()

	next := cloneRuntimeConfig(t, rt.Config())
	for _, sc := range next.Servers {
		if sc.Name == "alpha" {
			sc.Quarantined = true
			sc.Enabled = false
			sc.MarkQuarantineExplicitlySet(true)
		}
	}
	applyDone := make(chan error, 1)
	go func() {
		_, err := rt.ApplyConfig(next, cfgPath)
		applyDone <- err
	}()
	spec116Await(t, probe.entered, "the apply reconcile to park")

	toggleDone := make(chan error, 1)
	go func() { toggleDone <- rt.EnableServer("bravo", true) }()
	returned, toggleErr := spec116AwaitReturnOrBlocked(t, toggleDone, func() bool { return rt.commitWaiters() > 0 })
	if returned {
		require.NoError(t, toggleErr)
	}
	probe.releaseParked()
	require.NoError(t, spec116AwaitErr(t, applyDone, "ApplyConfig"))
	if !returned {
		require.NoError(t, spec116AwaitErr(t, toggleDone, "EnableServer"))
	}
	probe.settle(t)

	for label, sc := range map[string]*config.ServerConfig{
		"live":    spec116Live(rt, "alpha"),
		"storage": spec116Stored(t, rt, "alpha"),
		"disk":    spec116Disk(t, cfgPath, "alpha"),
	} {
		require.NotNil(t, sc, label)
		assert.True(t, sc.Quarantined, "%s: alpha must stay quarantined", label)
		assert.False(t, sc.Enabled, "%s: alpha must stay disabled", label)
	}
	if c, ok := rt.upstreamManager.GetClient("alpha"); ok {
		cfg := c.GetConfig()
		assert.True(t, cfg.Quarantined && !cfg.Enabled, "manager: alpha's client must carry the quarantined, disabled config")
	}
	for label, sc := range map[string]*config.ServerConfig{
		"live":    spec116Live(rt, "bravo"),
		"storage": spec116Stored(t, rt, "bravo"),
		"disk":    spec116Disk(t, cfgPath, "bravo"),
	} {
		require.NotNil(t, sc, label)
		assert.True(t, sc.Enabled, "%s: bravo's toggle must land", label)
	}
}

// T1.4 (#1558, #1560): a reconcile that captured an explicit quarantined:false
// must not lower a quarantine that QuarantineServer(true) committed after it.
func TestSpec116_T1_4_StaleReconcileCannotLowerQuarantine(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, nil,
		explicitServer(&config.ServerConfig{Name: "sigma", Command: "true", Protocol: "stdio", Enabled: false}),
		explicitServer(&config.ServerConfig{Name: "tau", Command: "true", Protocol: "stdio", Enabled: false}),
	)
	require.NoError(t, rt.indexManager.BatchIndexTools([]*config.ToolMetadata{
		{ServerName: "sigma", Name: "list", Description: "list things", ParamsJSON: `{"type":"object"}`, Hash: "h1"},
	}))
	probe := installSpec116ReconcileProbe(rt)
	defer probe.releaseParked()

	next := cloneRuntimeConfig(t, rt.Config())
	for _, sc := range next.Servers {
		if sc.Name == "tau" {
			sc.Args = []string{"edited"}
		}
	}
	applyDone := make(chan error, 1)
	go func() {
		_, err := rt.ApplyConfig(next, cfgPath)
		applyDone <- err
	}()
	spec116Await(t, probe.entered, "the apply reconcile to park")

	qDone := make(chan error, 1)
	go func() { qDone <- rt.QuarantineServer("sigma", true) }()
	returned, qErr := spec116AwaitReturnOrBlocked(t, qDone, func() bool { return rt.commitWaiters() > 0 })
	if returned {
		require.NoError(t, qErr)
	}
	probe.releaseParked()
	require.NoError(t, spec116AwaitErr(t, applyDone, "ApplyConfig"))
	if !returned {
		require.NoError(t, spec116AwaitErr(t, qDone, "QuarantineServer"))
	}
	probe.settle(t)

	for label, sc := range map[string]*config.ServerConfig{
		"live":    spec116Live(rt, "sigma"),
		"storage": spec116Stored(t, rt, "sigma"),
		"disk":    spec116Disk(t, cfgPath, "sigma"),
	} {
		require.NotNil(t, sc, label)
		assert.True(t, sc.Quarantined, "%s: sigma must stay quarantined", label)
	}
	tools, err := rt.indexManager.GetToolsByServer("sigma")
	require.NoError(t, err)
	assert.Empty(t, tools, "a quarantined server's tools must not be indexed")
}

// T1.12 (FR-008a, review R2.2): a discovery pass that checked approvals before
// a quarantine committed must not re-index the server after the commit
// purged it.
func TestSpec116_T1_12_DiscoveryCannotReindexAfterQuarantine(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil,
		explicitServer(&config.ServerConfig{Name: "omega", Command: "true", Protocol: "stdio", Enabled: true}),
	)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	rt.testHooks.discoveryBeforeIndex = func(name string) {
		if name != "omega" {
			return
		}
		first := false
		once.Do(func() { first = true })
		if first {
			close(entered)
			<-release
		}
	}
	var relOnce sync.Once
	releaseFn := func() { relOnce.Do(func() { close(release) }) }
	defer releaseFn()

	tools := []*config.ToolMetadata{
		{ServerName: "omega", Name: "fetch", Description: "fetch a page", ParamsJSON: `{"type":"object"}`, Hash: "hf"},
	}
	passDone := make(chan error, 1)
	go func() { passDone <- rt.applyDifferentialToolUpdate(context.Background(), "omega", tools) }()
	spec116Await(t, entered, "the discovery pass to park")

	qDone := make(chan error, 1)
	go func() { qDone <- rt.QuarantineServer("omega", true) }()
	returned, qErr := spec116AwaitReturnOrBlocked(t, qDone, func() bool {
		return rt.approvalLockWaiters("omega") > 0 || rt.commitWaiters() > 0
	})
	if returned {
		require.NoError(t, qErr)
	}
	releaseFn()
	_ = spec116AwaitErr(t, passDone, "discovery pass")
	if !returned {
		require.NoError(t, spec116AwaitErr(t, qDone, "QuarantineServer"))
	}

	indexed, err := rt.indexManager.GetToolsByServer("omega")
	require.NoError(t, err)
	assert.Empty(t, indexed, "a pass that started before the quarantine must not re-index the server")
}
