package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 116 PR1 runtime fixtures (quickstart.md §T1).

// newSpec116Runtime writes servers to a config file, builds a runtime on it
// and boots its storage projection.
func newSpec116Runtime(t *testing.T, mutate func(*config.Config), servers ...*config.ServerConfig) (*Runtime, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	cfg := config.DefaultConfig()
	cfg.DataDir = dir
	cfg.Listen = "127.0.0.1:0"
	cfg.Servers = servers
	if mutate != nil {
		mutate(cfg)
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, raw, 0o600))
	loaded, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	loaded.DataDir = dir
	rt, err := New(loaded, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	bootSpec116Runtime(t, rt)
	return rt, cfgPath
}

// bootSpec116Runtime projects the configured servers into storage, as the
// daemon's boot reconcile does.
func bootSpec116Runtime(t *testing.T, rt *Runtime) {
	t.Helper()
	require.NoError(t, rt.LoadConfiguredServers(nil))
}

func cloneRuntimeConfig(t *testing.T, cfg *config.Config) *config.Config {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var out config.Config
	require.NoError(t, json.Unmarshal(raw, &out))
	return &out
}

func spec116Live(rt *Runtime, name string) *config.ServerConfig {
	for _, sc := range rt.Config().Servers {
		if sc != nil && sc.Name == name {
			return sc
		}
	}
	return nil
}

func spec116Stored(t *testing.T, rt *Runtime, name string) *config.ServerConfig {
	t.Helper()
	servers, err := rt.storageManager.ListUpstreamServers()
	require.NoError(t, err)
	for _, sc := range servers {
		if sc.Name == name {
			return sc
		}
	}
	return nil
}

func spec116Disk(t *testing.T, cfgPath, name string) *config.ServerConfig {
	t.Helper()
	cfg, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	for _, sc := range cfg.Servers {
		if sc != nil && sc.Name == name {
			return sc
		}
	}
	return nil
}

// spec116ReconcileProbe parks the first reconcile that enters and counts
// entries and exits.
type spec116ReconcileProbe struct {
	entered  chan struct{}
	release  chan struct{}
	relOnce  sync.Once
	parkOnce sync.Once
	entries  atomic.Int32
	exits    atomic.Int32
}

func installSpec116ReconcileProbe(rt *Runtime) *spec116ReconcileProbe {
	p := &spec116ReconcileProbe{entered: make(chan struct{}), release: make(chan struct{})}
	rt.testHooks.reconcileEntry = func() {
		p.entries.Add(1)
		parked := false
		p.parkOnce.Do(func() { parked = true })
		if parked {
			close(p.entered)
			<-p.release
		}
	}
	rt.testHooks.reconcileExit = func() { p.exits.Add(1) }
	return p
}

func (p *spec116ReconcileProbe) releaseParked() { p.relOnce.Do(func() { close(p.release) }) }

func (p *spec116ReconcileProbe) settle(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return p.exits.Load() >= p.entries.Load() },
		20*time.Second, time.Millisecond, "a reconcile that started never finished")
}

func spec116AwaitReturnOrBlocked(t *testing.T, done <-chan error, blocked func() bool) (bool, error) {
	t.Helper()
	deadline := time.After(20 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return true, err
		case <-tick.C:
			if blocked() {
				return false, nil
			}
		case <-deadline:
			t.Fatal("competitor neither returned nor blocked within 20s")
			return false, nil
		}
	}
}

func spec116Await(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func spec116AwaitErr(t *testing.T, ch <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}

func runtimeStack(buf []byte) int { return goruntime.Stack(buf, true) }
