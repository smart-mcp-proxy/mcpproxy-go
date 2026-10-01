package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

var funnelKey = []byte("config-funnel-test-key-012345678")

// newFunnelRuntime builds a real runtime over a temp dir with two profiles and
// a guard that never refuses (individual tests install their own).
func newFunnelRuntime(t *testing.T) *Runtime {
	t.Helper()
	// Not t.TempDir: applying a profile change starts a background profile-index
	// build that can still be writing under the data dir when the test's own
	// cleanup would remove it.
	dir, err := os.MkdirTemp("", "config-funnel-*")
	require.NoError(t, err)
	cfg := config.DefaultConfig()
	cfg.DataDir = dir
	cfg.Listen = "127.0.0.1:9000"
	cfg.Servers = []*config.ServerConfig{{Name: "a", Enabled: true}, {Name: "b", Enabled: true}}
	cfg.Profiles = []config.ProfileConfig{
		{Name: "ro", Servers: []string{"a"}},
		{Name: "full", Servers: []string{"a", "b"}},
	}
	cfg.RequireMCPAuth = true
	rt, err := New(cfg, filepath.Join(dir, "mcp_config.json"), zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Close()
		time.Sleep(100 * time.Millisecond)
		_ = os.RemoveAll(dir)
	})
	rt.SetBindingGuard(stubGuard{})
	return rt
}

func funnelActor() Actor { return Actor{Kind: "api_key", Surface: profile.SurfaceAPI} }

func profileChanges(t *testing.T, rt *Runtime) []map[string]interface{} {
	t.Helper()
	recs, _, err := rt.StorageManager().ListActivities(storage.ActivityFilter{Types: []string{string(storage.ActivityTypeProfileChange)}, Limit: 100})
	require.NoError(t, err)
	var out []map[string]interface{}
	for _, r := range recs {
		out = append(out, r.Metadata)
	}
	return out
}

func changesOf(recs []map[string]interface{}) map[string]string {
	out := map[string]string{}
	for _, m := range recs {
		out[m["change"].(string)] += m["profile"].(string) + ","
	}
	return out
}

func noop(*config.Config) (ChangeHint, error) { return ChangeHint{}, nil }

func TestMutateConfig_ReadMutateGuardApplyUnderOneLock(t *testing.T) {
	rt := newFunnelRuntime(t)
	_, err := rt.StorageManager().MintClientCredential("cursor", "mcp_cli_funnel_lock_test_00000000", funnelKey, auth.ProfileModeLocked, "ro", time.Now().Add(time.Hour))
	require.NoError(t, err)

	inMutate := make(chan struct{})
	release := make(chan struct{})
	var bindingDone atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, _ = rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
			close(inMutate)
			<-release
			d.Profiles = append(d.Profiles, config.ProfileConfig{Name: "extra", Servers: []string{"a"}})
			return ChangeHint{}, nil
		}, TokenRewrite{})
	}()
	<-inMutate

	done := make(chan struct{})
	go func() {
		_, _ = rt.ClientsService().SetBinding(context.Background(), funnelActor(), "cursor", "full", nil)
		bindingDone.Store(true)
		close(done)
	}()

	time.Sleep(150 * time.Millisecond)
	assert.False(t, bindingDone.Load(), "a binding write must wait for the config write's lock")
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("binding write never ran after the config write released the lock")
	}
	wg.Wait()
	cfg, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	names := []string{}
	for _, p := range cfg.Profiles {
		names = append(names, p.Name)
	}
	assert.Contains(t, names, "extra")
}

// recordingGuard captures the candidate state the guard was asked about.
type recordingGuard struct {
	mu        sync.Mutex
	candidate GuardState
	calls     int
}

func (g *recordingGuard) BindingGuardDelta(_, candidate GuardState) []BindingRef {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.candidate = candidate
	g.calls++
	return nil
}
func (g *recordingGuard) BindingGuardFixes(GuardState, []BindingRef) []GuardFix { return nil }
func (g *recordingGuard) BindingGuardActiveBindings() []BindingRef              { return nil }

func TestMutateConfig_GuardUsesMutatedConfigAndRewrittenTokens(t *testing.T) {
	rt := newFunnelRuntime(t)
	g := &recordingGuard{}
	rt.SetBindingGuard(g)
	_, err := rt.StorageManager().MintClientCredential("cursor", "mcp_cli_funnel_guard_test_0000000", funnelKey, auth.ProfileModeLocked, "ro", time.Now().Add(time.Hour))
	require.NoError(t, err)

	_, _, err = rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
		d.Profiles[0].Name = "ro2"
		return ChangeHint{Kind: profile.ChangeRename, Profile: "ro2", PreviousProfile: "ro"}, nil
	}, TokenRewrite{From: "ro", To: "ro2"})
	require.NoError(t, err)

	require.Equal(t, 1, g.calls)
	assert.Equal(t, "ro2", g.candidate.Config.Profiles[0].Name, "the guard sees the mutated config")
	require.Len(t, g.candidate.Tokens, 1)
	assert.Equal(t, "ro2", g.candidate.Tokens[0].ProfilePin, "and the tokens as they will be after the re-pin")

	// The write reached storage too.
	tok, err := rt.StorageManager().GetAgentTokenByName("client-cursor")
	require.NoError(t, err)
	assert.Equal(t, "ro2", tok.ProfilePin)
}

func TestMutateConfig_GuardRefusalWritesNothing(t *testing.T) {
	rt := newFunnelRuntime(t)
	rt.SetBindingGuard(stubGuard{refuse: &BindingGuardError{Bindings: []BindingRef{{ClientID: "cursor", Profile: "ro"}}}})
	before, err := rt.GetDesiredConfig()
	require.NoError(t, err)

	_, _, err = rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
		d.Profiles = append(d.Profiles, config.ProfileConfig{Name: "new", Servers: []string{"a"}})
		return ChangeHint{}, nil
	}, TokenRewrite{})
	var refusal *BindingGuardError
	require.True(t, errors.As(err, &refusal))

	after, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Len(t, after.Profiles, len(before.Profiles))
	assert.Empty(t, profileChanges(t, rt))
}

func TestMutateConfig_NoRecordWhenProfilesUnchanged(t *testing.T) {
	rt := newFunnelRuntime(t)
	_, diff, err := rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
		d.Listen = "127.0.0.1:9001"
		return ChangeHint{}, nil
	}, TokenRewrite{})
	require.NoError(t, err)
	assert.Empty(t, diff.Changes)
	assert.Empty(t, profileChanges(t, rt))
}

func TestMutateConfig_ApplyDocWritesCreateUpdateAnonymous(t *testing.T) {
	rt := newFunnelRuntime(t)
	actor := Actor{Kind: "api_key", Name: "", Surface: profile.SurfaceCLI}

	_, diff, err := rt.MutateConfig(context.Background(), actor, func(d *config.Config) (ChangeHint, error) {
		d.AnonymousProfile = "ro"
		d.Profiles = append(d.Profiles, config.ProfileConfig{Name: "fresh", Servers: []string{"a"}})
		d.Profiles[1].MaxTier = "read" // full -> read-capped
		return ChangeHint{}, nil
	}, TokenRewrite{})
	require.NoError(t, err)
	require.Len(t, diff.Changes, 3)

	got := changesOf(profileChanges(t, rt))
	assert.Equal(t, "fresh,", got["create"])
	assert.Equal(t, "full,", got["update"])
	assert.Equal(t, "ro,", got["anonymous"])
	for _, m := range profileChanges(t, rt) {
		assert.Equal(t, "api_key", m["actor_kind"])
		assert.Equal(t, "cli", m["surface"])
	}

	// An unchanged document writes nothing further.
	_, diff, err = rt.MutateConfig(context.Background(), actor, noop, TokenRewrite{})
	require.NoError(t, err)
	assert.Empty(t, diff.Changes)
	assert.Len(t, profileChanges(t, rt), 3)
}

func TestMutateConfig_UpdateDiffNamesFieldsNotSecrets(t *testing.T) {
	rt := newFunnelRuntime(t)
	_, _, err := rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
		d.Profiles[0].MaxTier = "read"
		d.Profiles[0].Servers = []string{"a", "b"}
		return ChangeHint{}, nil
	}, TokenRewrite{})
	require.NoError(t, err)
	recs := profileChanges(t, rt)
	require.Len(t, recs, 1)
	diff := recs[0]["diff"].(map[string]interface{})
	assert.Equal(t, map[string]interface{}{"from": "", "to": "read"}, diff["max_tier"])
	assert.Equal(t, map[string]interface{}{"added": []interface{}{"b"}, "removed": []interface{}{}}, diff["servers"])
}

func TestMutateConfig_ChangedAnonymousUnknownIs400(t *testing.T) {
	rt := newFunnelRuntime(t)
	_, _, err := rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
		d.AnonymousProfile = "ghost"
		return ChangeHint{}, nil
	}, TokenRewrite{})
	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Equal(t, "anonymous_profile", ve.Field)
	assert.Equal(t, `unknown profile "ghost"`, ve.Message)
	assert.Empty(t, profileChanges(t, rt))

	// A dangling value that is NOT changed by the write stays a warning.
	rt2 := newFunnelRuntime(t)
	_, _, err = rt2.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
		d.AnonymousProfile = "ro"
		return ChangeHint{}, nil
	}, TokenRewrite{})
	require.NoError(t, err)
}

func TestMutateConfig_ConcurrentWritesKeepBoth(t *testing.T) {
	rt := newFunnelRuntime(t)
	var wg sync.WaitGroup
	for _, n := range []string{"p1", "p2", "p3", "p4"} {
		n := n
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
				d.Profiles = append(d.Profiles, config.ProfileConfig{Name: n, Servers: []string{"a"}})
				return ChangeHint{}, nil
			}, TokenRewrite{})
			assert.NoError(t, err)
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: concurrent MutateConfig calls did not finish")
	}
	cfg, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Len(t, cfg.Profiles, 6, "no concurrent profile write is lost")
}

// A profile that is created or renamed through the funnel gets its per-profile
// search index NOW (found in live QA of Spec 108-f). The funnel gets this
// through ApplyConfig, which owns the reconcile for every apply (#1458).
func TestMutateConfig_ReconcilesPerProfileIndexes(t *testing.T) {
	rt := newFunnelRuntime(t)
	dirs := func() []string {
		names, err := rt.IndexManager().ExistingProfileDirs()
		require.NoError(t, err)
		return names
	}
	ctx := context.Background()

	_, _, err := rt.MutateConfig(ctx, funnelActor(), func(d *config.Config) (ChangeHint, error) {
		d.Profiles = append(d.Profiles, config.ProfileConfig{Name: "fresh", Servers: []string{"a"}})
		return ChangeHint{}, nil
	}, TokenRewrite{})
	require.NoError(t, err)
	assert.Contains(t, dirs(), "fresh", "a created profile is indexed immediately")

	_, _, err = rt.MutateConfig(ctx, funnelActor(), func(d *config.Config) (ChangeHint, error) {
		for i := range d.Profiles {
			if d.Profiles[i].Name == "fresh" {
				d.Profiles[i].Name = "renamed"
			}
		}
		return ChangeHint{Kind: profile.ChangeRename, Profile: "renamed", PreviousProfile: "fresh"}, nil
	}, TokenRewrite{})
	require.NoError(t, err)
	assert.Contains(t, dirs(), "renamed")
	assert.NotContains(t, dirs(), "fresh", "the old name's index is dropped")
}

// flakyPins fails RestorePins (and optionally RepinProfile) to prove the
// rollback ordering never leaves a token wider than it was.
type flakyPins struct {
	inner       ProfilePinStore
	failRestore bool
}

func (f *flakyPins) RepinProfile(from, to string) ([]auth.AgentToken, error) {
	return f.inner.RepinProfile(from, to)
}
func (f *flakyPins) RestorePins(b []auth.AgentToken) error {
	if f.failRestore {
		return errors.New("restore failed")
	}
	return f.inner.RestorePins(b)
}

func TestMutateConfig_FailedApplyRestoresPins(t *testing.T) {
	rt := newFunnelRuntime(t)
	_, err := rt.StorageManager().MintClientCredential("cursor", "mcp_cli_funnel_restore_test_00000", funnelKey, auth.ProfileModeLocked, "ro", time.Now().Add(time.Hour))
	require.NoError(t, err)

	// Make the config write fail: the target path's parent is a file.
	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	rt.configSvc.UpdatePath(filepath.Join(blocker, "mcp_config.json"))

	_, _, err = rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
		d.Profiles[0].Name = "ro2"
		return ChangeHint{Kind: profile.ChangeRename, Profile: "ro2", PreviousProfile: "ro"}, nil
	}, TokenRewrite{From: "ro", To: "ro2"})
	require.Error(t, err)

	tok, err := rt.StorageManager().GetAgentTokenByName("client-cursor")
	require.NoError(t, err)
	assert.Equal(t, "ro", tok.ProfilePin, "the rollback puts the pin back")
	assert.Empty(t, profileChanges(t, rt), "a failed write leaves no record")

	// If the rollback itself fails the pin stays on the NEW name: the running
	// config has no such profile, so the client is denied everything - never
	// wider than before.
	rt.pinStoreOverride = &flakyPins{inner: rt.StorageManager(), failRestore: true}
	_, _, err = rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
		d.Profiles[0].Name = "ro2"
		return ChangeHint{Kind: profile.ChangeRename, Profile: "ro2", PreviousProfile: "ro"}, nil
	}, TokenRewrite{From: "ro", To: "ro2"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be restored")
	tok, err = rt.StorageManager().GetAgentTokenByName("client-cursor")
	require.NoError(t, err)
	assert.Equal(t, "ro2", tok.ProfilePin)
	running := rt.Config()
	for _, p := range running.Profiles {
		assert.NotEqual(t, "ro2", p.Name, "the running config never adopted the failed write")
	}
}
