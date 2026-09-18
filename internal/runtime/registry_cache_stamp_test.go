package runtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 105 FR-002 (gap FR001-G2, task T029): the registry search path is one
// of the two writers of INTERNAL cache entries
// (`registry-servers:<id>:<tag>:<query>:<limit>`). It must stamp every entry
// it writes with the internal caller kind so read_cache refuses the entry for
// every caller — administrators included (SC-005 names this exception) —
// without evicting it; and its own reader (Peek, which serves a cached list
// while flagging its age) must keep finding the stamped entry.
//
// The kind is spelled as its wire value so this test compiles against the
// pre-feature cache package; the constant lives in internal/cache.
const internalCallerKindWire = "internal"

func TestSearchRegistryServers_CacheEntryIsStampedInternal(t *testing.T) {
	// A single-page official-protocol listing on loopback; the SSRF guard is
	// relaxed through the same config flag an operator running a private
	// mirror would set.
	fetches := 0
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"servers": []map[string]interface{}{{
				"server": map[string]interface{}{
					"name":        "io.example/sentinel-registry-server",
					"description": "SENTINEL-REGISTRY-ENTRY",
					"remotes": []interface{}{
						map[string]interface{}{"type": "streamable-http", "url": "https://example.com/mcp"},
					},
				},
			}},
			"metadata": map[string]interface{}{},
		})
	}))
	defer registry.Close()

	cfg := &config.Config{
		DataDir: t.TempDir(),
		Listen:  "127.0.0.1:0",
		Registries: []config.RegistryEntry{{
			ID: "stamp-test", Name: "Stamp Test", URL: registry.URL, ServersURL: registry.URL,
			Protocol: "modelcontextprotocol/registry",
		}},
		AllowPrivateRegistryFetch: true,
	}
	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	servers, info, err := rt.SearchRegistryServers("stamp-test", "", "", 5)
	require.NoError(t, err)
	require.Len(t, servers, 1, "premise: the fake registry lists one server")
	require.NotNil(t, info)
	require.Zero(t, info.AgeSeconds, "premise: a fresh fetch reports age zero")
	require.Equal(t, 1, fetches)

	key := fmt.Sprintf("registry-servers:%s:%s:%s:%d", "stamp-test", "", "", 5)
	rec, ok := rt.CacheManager().Peek(key)
	require.True(t, ok, "the registry writer must persist its list under %q", key)
	require.NotNil(t, rec.Producer, "a registry entry must carry a producer stamp: an unstamped entry is legacy provenance and would be evicted on the first read_cache probe")
	assert.Equal(t, internalCallerKindWire, rec.Producer.CallerKind, "registry entries are internal entries")

	// The internal reader is unaffected by the stamp: the second search is
	// served from the stamped entry without a second fetch.
	servers, info, err = rt.SearchRegistryServers("stamp-test", "", "", 5)
	require.NoError(t, err)
	require.Len(t, servers, 1)
	require.NotNil(t, info, "the cached list must be served with its freshness info")
	assert.False(t, info.Stale)
	assert.Equal(t, 1, fetches, "the stamped entry must still be served to the registry's own read path")
}
