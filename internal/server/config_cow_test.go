package server

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestConfigWithAppendedServer_DoesNotMutateThePublishedSnapshot pins the
// copy-on-write contract that handleAddUpstream violated.
//
// handleAddUpstream did:
//
//	currentConfig := p.mainServer.runtime.Config()
//	currentConfig.Servers = append(currentConfig.Servers, serverConfig)
//
// where runtime.Config() returns the PUBLISHED snapshot every other goroutine
// reads lock-free. Both the field write and the backing-array write land in
// shared state.
//
// BITES: replace configWithAppendedServer's body with the in-place form
//
//	current.Servers = append(current.Servers, sc); return current
//
// and both assertions below fail — the input's length grows and the caller's
// slice header is the same one that was published.
func TestConfigWithAppendedServer_DoesNotMutateThePublishedSnapshot(t *testing.T) {
	published := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "alpha", URL: "https://alpha.example", Protocol: "http"},
		},
	}
	// Give the slice spare capacity: with cap to grow into, an in-place append
	// writes the SHARED backing array rather than reallocating, which is the
	// worse half of the race and the half a len-only check would miss.
	published.Servers = append(make([]*config.ServerConfig, 0, 8), published.Servers...)

	before := len(published.Servers)
	beforeFirst := published.Servers[0]

	added := &config.ServerConfig{Name: "beta", URL: "https://beta.example", Protocol: "http"}
	updated := configWithAppendedServer(published, added)

	require.NotNil(t, updated)
	assert.Len(t, updated.Servers, before+1, "the returned config must carry the new server")
	assert.Equal(t, "beta", updated.Servers[before].Name)

	assert.Len(t, published.Servers, before,
		"the published snapshot must not have grown: readers hold this pointer")
	assert.Same(t, beforeFirst, published.Servers[0],
		"the published snapshot's existing entries must be untouched")

	// The clone must not share a backing array with the snapshot, or a LATER
	// append through the clone would still write memory the snapshot spans.
	updated.Servers[0] = &config.ServerConfig{Name: "rewritten"}
	assert.Same(t, beforeFirst, published.Servers[0],
		"writing through the clone must not reach the published snapshot")

	// Fields other than Servers are carried over.
	assert.Equal(t, "127.0.0.1:8080", updated.Listen)
}

// TestConfigWithAppendedServer_NilConfig keeps the caller's nil guard honest.
func TestConfigWithAppendedServer_NilConfig(t *testing.T) {
	assert.Nil(t, configWithAppendedServer(nil, &config.ServerConfig{Name: "x"}))
}

// TestConfigWithAppendedServer_RaceWithSnapshotReaders is the -race half of the
// proof: it reconstructs the interleaving the fix exists to prevent — a reader
// ranging over the published snapshot's Servers while an add appends.
//
// BITES (under `go test -race`): with the in-place body described above, the
// race detector reports a write to the config's Servers field and to the
// backing array concurrent with these reads.
func TestConfigWithAppendedServer_RaceWithSnapshotReaders(t *testing.T) {
	published := &config.Config{
		Servers: append(make([]*config.ServerConfig, 0, 64), &config.ServerConfig{Name: "alpha"}),
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Readers, standing in for AdminServersProvider / LoadConfiguredServers.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				names := 0
				for _, s := range published.Servers {
					if s != nil && s.Name != "" {
						names++
					}
				}
				_ = names
			}
		}()
	}

	for i := 0; i < 64; i++ {
		// The production call site publishes the RESULT; the snapshot the
		// readers hold must be left alone by the append itself.
		_ = configWithAppendedServer(published, &config.ServerConfig{Name: fmt.Sprintf("s%d", i)})
	}

	close(stop)
	wg.Wait()

	assert.Len(t, published.Servers, 1, "no append may have landed in the published snapshot")
}
