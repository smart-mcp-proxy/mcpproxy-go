package server

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// UX-01 review r1: create-only add must read the config snapshot, modify it and
// publish it under the runtime config-commit lock, so a config commit landing
// mid-add is neither lost nor reverted.
func TestServerAddServer_DoesNotRevertConcurrentConfigCommit(t *testing.T) {
	srv, cfgPath := guardedApplyServer(t, nil)

	// Seed "a" in storage as well as runtime config: SaveConfiguration rebuilds
	// the server list from storage, so a runtime-only "a" is dropped whenever the
	// add's save wins the race, which made this test scheduling-dependent.
	for _, s := range srv.runtime.Config().Servers {
		if s.Name == "a" {
			seed := *s
			require.NoError(t, srv.runtime.StorageManager().SaveUpstreamServer(&seed))
		}
	}

	reached := make(chan struct{})
	release := make(chan struct{})
	createServerAfterSnapshotHook = func() {
		close(reached)
		<-release
	}
	t.Cleanup(func() { createServerAfterSnapshotHook = nil })

	addDone := make(chan error, 1)
	go func() {
		addDone <- srv.AddServer(context.Background(), &config.ServerConfig{
			Name: "fresh", Command: "true", Protocol: "stdio", Quarantined: true,
		})
	}()
	<-reached

	commitDone := make(chan error, 1)
	go func() {
		_, err := srv.runtime.SetServerShared("a", true)
		commitDone <- err
	}()
	// Give an unserialized commit time to land inside the add's window.
	select {
	case <-commitDone:
		commitDone <- nil
	case <-time.After(300 * time.Millisecond):
	}
	close(release)

	require.NoError(t, <-addDone)
	require.NoError(t, <-commitDone)

	cfg := srv.runtime.Config()
	found, shared := false, false
	for _, s := range cfg.Servers {
		found = found || s.Name == "fresh"
		shared = shared || (s.Name == "a" && s.Shared)
	}
	assert.True(t, shared, "runtime lost the concurrent commit")
	assert.True(t, found, "runtime lost the new server")

	// Persist whatever the stores now hold; a reverted commit would show here.
	require.NoError(t, srv.SaveConfiguration())
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var disk config.Config
	require.NoError(t, json.Unmarshal(raw, &disk))
	onDisk, sharedOnDisk := false, false
	for _, s := range disk.Servers {
		onDisk = onDisk || s.Name == "fresh"
		sharedOnDisk = sharedOnDisk || (s.Name == "a" && s.Shared)
	}
	assert.True(t, sharedOnDisk, "disk lost the concurrent commit")
	assert.True(t, onDisk, "disk lost the new server")
}
