package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 116 PR1, review round 3.

func oauthHTTP(name string, scopes ...string) *config.ServerConfig {
	sc := &config.ServerConfig{
		Name: name, URL: "http://127.0.0.1:9/mcp", Protocol: "http", Enabled: false,
		OAuth: &config.OAuthConfig{Scopes: scopes},
	}
	sc.MarkQuarantineExplicitlySet(true)
	return sc
}

// seedOAuth stores a token record and a DCR client registration under both
// key forms ClearOAuthState covers: the bare name and name_<hash>.
func seedOAuth(t *testing.T, rt *Runtime, name string) {
	t.Helper()
	db := rt.storageManager.GetBoltDB()
	for _, key := range []string{name, name + "_0123456789abcdef"} {
		require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{
			ServerName: key, DisplayName: name, AccessToken: "at-" + key, RefreshToken: "rt-" + key,
			TokenType: "Bearer", ClientID: "dcr-" + key, ClientSecret: "secret-" + key,
		}))
	}
}

func oauthKeysPresent(t *testing.T, rt *Runtime, name string) []string {
	t.Helper()
	var present []string
	for _, key := range []string{name, name + "_0123456789abcdef"} {
		if rec, err := rt.storageManager.GetBoltDB().GetOAuthToken(key); err == nil && rec != nil {
			present = append(present, key)
		}
	}
	return present
}

// Finding 1 (High): an OAuth-only file edit made while the daemon was stopped
// clears the cached tokens and DCR client of that server at boot, as main's
// reconcile did, and keeps the credentials of an unchanged server.
func TestCommit_R3_BootClearsOAuthStateEditedWhileStopped(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	initial := config.DefaultConfig()
	initial.DataDir = dir
	initial.Listen = "127.0.0.1:0"
	initial.Servers = []*config.ServerConfig{oauthHTTP("edited", "read"), oauthHTTP("kept", "read")}
	require.NoError(t, config.SaveConfig(initial, cfgPath))
	boot := func() *Runtime {
		cfg, err := config.LoadFromFile(cfgPath)
		require.NoError(t, err)
		rt, err := New(cfg, cfgPath, zap.NewNop())
		require.NoError(t, err)
		rt.gateInitialConfig()
		require.NoError(t, rt.bootReconcile())
		return rt
	}
	rt := boot()
	seedOAuth(t, rt, "edited")
	seedOAuth(t, rt, "kept")
	require.NoError(t, rt.Close())

	onDisk, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	for _, sc := range onDisk.Servers {
		if sc.Name == "edited" {
			sc.OAuth.Scopes = []string{"read", "write"}
			sc.OAuth.TokenEndpoint = "https://auth.example.com/token"
		}
	}
	require.NoError(t, config.SaveConfig(onDisk, cfgPath))

	rt2 := boot()
	t.Cleanup(func() { _ = rt2.Close() })

	row := spec116Stored(t, rt2, "edited")
	require.NotNil(t, row)
	require.NotNil(t, row.OAuth)
	assert.Equal(t, []string{"read", "write"}, row.OAuth.Scopes, "the row carries the edited OAuth config")
	assert.Equal(t, "https://auth.example.com/token", row.OAuth.TokenEndpoint)
	assert.Empty(t, oauthKeysPresent(t, rt2, "edited"), "the old token and client registration must be gone")
	assert.Len(t, oauthKeysPresent(t, rt2, "kept"), 2, "an unchanged OAuth config keeps its credentials")
}

// Finding 4: the OAuth clear is part of the row transaction. A storage
// failure leaves both the old row and the old credentials, and the file is
// restored; a successful edit clears only the edited server.
func TestCommit_R3_OAuthClearIsInTheRowTransaction(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, nil, oauthHTTP("o", "read"), oauthHTTP("p", "read"))
	seedOAuth(t, rt, "o")
	seedOAuth(t, rt, "p")
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	edit := func() error {
		_, err := rt.Commit("update", func(tx *CommitTx) error {
			return tx.UpdateServer("o", func(cur *config.ServerConfig) error {
				cur.OAuth.Scopes = []string{"admin"}
				return nil
			})
		})
		return err
	}

	rt.testHooks.storageFault = func(string) error { return errors.New("injected storage failure") }
	require.Error(t, edit())
	rt.testHooks.storageFault = nil
	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "the file is restored")
	assert.Equal(t, []string{"read"}, spec116Stored(t, rt, "o").OAuth.Scopes, "the row keeps the old OAuth config")
	assert.Len(t, oauthKeysPresent(t, rt, "o"), 2, "a failed commit keeps the credentials of the old config")

	require.NoError(t, edit())
	assert.Equal(t, []string{"admin"}, spec116Stored(t, rt, "o").OAuth.Scopes)
	assert.Empty(t, oauthKeysPresent(t, rt, "o"))
	assert.Len(t, oauthKeysPresent(t, rt, "p"), 2)
}

// Finding 2: a failed UpdateConfig does not publish its new path; a
// successful one moves both path stores together.
func TestCommit_R3_FailedUpdateConfigKeepsPath(t *testing.T) {
	for _, fault := range []string{"disk", "storage"} {
		t.Run(fault, func(t *testing.T) {
			rt, pathA := newSpec116Runtime(t, nil, disabledStdio("a"))
			pathB := filepath.Join(filepath.Dir(pathA), "other.json")
			liveBefore := rt.Config()

			next := cloneRuntimeConfig(t, rt.Config())
			next.Servers = append(next.Servers, disabledStdio("b"))
			inject := func(string) error { return errors.New("injected " + fault + " failure") }
			if fault == "disk" {
				rt.testHooks.diskWriteFault = inject
			} else {
				rt.testHooks.storageFault = inject
			}
			rt.UpdateConfig(next, pathB)
			rt.testHooks.diskWriteFault = nil
			rt.testHooks.storageFault = nil

			assert.Equal(t, pathA, rt.ConfigPath(), "the published path is unchanged")
			rt.mu.RLock()
			legacy := rt.cfgPath
			rt.mu.RUnlock()
			assert.Equal(t, pathA, legacy, "the legacy path is unchanged")
			assert.Same(t, liveBefore, rt.Config(), "the published config is unchanged")
			_, statErr := os.Stat(pathB)
			assert.True(t, errors.Is(statErr, os.ErrNotExist), "nothing is left at the new path")

			require.NoError(t, os.Remove(pathA))
			require.NoError(t, rt.SaveConfiguration())
			_, err := os.Stat(pathA)
			assert.NoError(t, err, "SaveConfiguration still writes A")
			_, statErr = os.Stat(pathB)
			assert.True(t, errors.Is(statErr, os.ErrNotExist))

			// Control: the same call without a fault moves both stores.
			rt.UpdateConfig(cloneRuntimeConfig(t, next), pathB)
			assert.Equal(t, pathB, rt.ConfigPath())
			rt.mu.RLock()
			legacy = rt.cfgPath
			rt.mu.RUnlock()
			assert.Equal(t, pathB, legacy)
			assert.NotNil(t, spec116Live(rt, "b"))
			_, err = os.Stat(pathB)
			assert.NoError(t, err, "the successful commit writes the new path")
		})
	}
}

// Finding 2, unchanged config: a path-only UpdateConfig still moves the path.
func TestCommit_R3_PathOnlyUpdateConfigMovesPath(t *testing.T) {
	rt, pathA := newSpec116Runtime(t, nil, disabledStdio("a"))
	pathB := filepath.Join(filepath.Dir(pathA), "other.json")
	rt.UpdateConfig(cloneRuntimeConfig(t, rt.Config()), pathB)
	assert.Equal(t, pathB, rt.ConfigPath())
	rt.mu.RLock()
	assert.Equal(t, pathB, rt.cfgPath)
	rt.mu.RUnlock()
}

// Finding 3, T1.18 variant (c): compensation coverage on a path that writes
// disk. The file holds an external edit A→A+B that no reload applied; a
// toggle of A fails in storage → the file is byte-identical to A+B.
// Then the documented outcomes: a successful disk-writing commit writes the
// live config (B is gone from disk), and only a reload applies B.
func TestCommit_R3_CompensationKeepsUnappliedEdit(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, nil, disabledStdio("a"))
	edited := cloneRuntimeConfig(t, rt.Config())
	edited.Servers = append(edited.Servers, disabledStdio("b"))
	require.NoError(t, config.SaveConfig(edited, cfgPath))
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	rt.testHooks.storageFault = func(string) error { return errors.New("injected storage failure") }
	require.Error(t, rt.EnableServer("a", true))
	rt.testHooks.storageFault = nil
	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "the compensation restores the unapplied edit byte for byte")
	assert.False(t, spec116Live(rt, "a").Enabled)
	assert.Nil(t, spec116Live(rt, "b"))
	assert.Nil(t, spec116Stored(t, rt, "b"))

	// A failed reload writes nothing either.
	rt.testHooks.storageFault = func(reason string) error {
		if reason == commitReasonFileReload {
			return errors.New("injected storage failure")
		}
		return nil
	}
	require.Error(t, rt.ReloadConfiguration())
	rt.testHooks.storageFault = nil
	after, err = os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))

	// The next disk-writing commit builds from live (A) and erases B, as on
	// main; it is not "applied by the next commit".
	require.NoError(t, rt.SaveConfiguration())
	onDisk, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	for _, sc := range onDisk.Servers {
		assert.NotEqual(t, "b", sc.Name, "a disk-writing commit after a failed reload writes the live config")
	}
	assert.Nil(t, spec116Live(rt, "b"))
}
