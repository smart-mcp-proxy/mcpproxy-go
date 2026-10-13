package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 116 PR2 review round 5 runtime tests.

// A replay that read the original call of the removed server A must not
// record its result under A's identity once A was removed and re-added as B
// under the same name: the write would recreate A's purged history bucket
// and identity (FR-014c). The control case proves the replay records when no
// removal intervenes, so the assertion is not vacuous.
func TestReplay_R5_OriginalReadBeforeReplacementRecreatesNothing(t *testing.T) {
	// Review round 6: "replaced_same_config" re-adds the server with the
	// IDENTICAL configuration and registers its identity, so the replacement's
	// identity hash equals the removed server's (GenerateServerID excludes the
	// incarnation). The identity check alone then passes; the replay must still
	// be refused because the original call record itself was purged.
	for _, mode := range []string{"control", "replaced_after_read", "replaced_same_config"} {
		replaced := mode != "control"
		sameConfig := mode == "replaced_same_config"
		t.Run(mode, func(t *testing.T) {
			cfgA := explicitServer(&config.ServerConfig{Name: "acme", URL: "http://127.0.0.1:9/a", Protocol: "http", Enabled: true})
			rt, _ := newSpec116Runtime(t, nil, cfgA)
			idA := storage.GenerateServerID(cfgA)
			_, err := rt.storageManager.RegisterServerIdentity(cfgA, "")
			require.NoError(t, err)
			require.NoError(t, rt.storageManager.RecordToolCall(&storage.ToolCallRecord{
				ID: "orig-1", ServerID: idA, ServerName: "acme", ToolName: "t",
				Arguments: map[string]interface{}{"x": 1}, Timestamp: time.Now(),
			}))

			require.NoError(t, rt.upstreamManager.AddServerConfigAt("acme", spec116Live(rt, "acme"), storedStamp(t, rt, "acme").Stamp.Gen))

			hooked := false
			if replaced {
				rt.replayAfterOriginalRead = func() {
					url := "http://127.0.0.1:9/b"
					if sameConfig {
						url = "http://127.0.0.1:9/a"
					}
					cfgB := explicitServer(&config.ServerConfig{
						Name: "acme", URL: url, Protocol: "http", Enabled: true})
					removeAndReAdd(t, rt, cfgB)
					if sameConfig {
						require.Equal(t, idA, storage.GenerateServerID(cfgB), "the replacement reuses the identity hash")
						// B's connect worker registers B's identity.
						_, err := rt.storageManager.RegisterServerIdentity(cfgB, "")
						require.NoError(t, err)
					}
					// B's client, as B's post-commit connect worker installs it.
					require.NoError(t, rt.upstreamManager.AddServerConfigAt("acme", spec116Live(rt, "acme"), storedStamp(t, rt, "acme").Stamp.Gen))
					hooked = true
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, rerr := rt.ReplayToolCall(ctx, "orig-1", nil)
			if replaced {
				assert.Error(t, rerr, "the replay of a removed server's call is refused before dispatch")
			} else {
				assert.NoError(t, rerr)
			}
			rt.replayAfterOriginalRead = nil

			calls, err := rt.storageManager.GetServerToolCalls(idA, 100)
			require.NoError(t, err)
			ids, err := rt.storageManager.ListServerIdentities()
			require.NoError(t, err)
			hasA := false
			for _, id := range ids {
				if id.ID == idA {
					hasA = true
				}
			}
			if !replaced {
				assert.Len(t, calls, 2, "without a removal the replay records its call next to the original")
				assert.True(t, hasA)
				return
			}
			require.True(t, hooked, "the replay read the original call")
			assert.Empty(t, calls, "the removed server's history bucket must not be recreated")
			if sameConfig {
				assert.True(t, hasA, "the replacement registered its own identity under the same hash")
				return
			}
			assert.False(t, hasA, "the removed server's identity must not be recreated")
		})
	}
}
