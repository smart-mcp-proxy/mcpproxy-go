package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 116 review round 1: a URL edit probes outside the commit lock. A
// name-only edit of the same source committed while it probes must survive:
// the URL edit carries only the probe-derived fields onto the live entry.
func TestSpec116_RegistryEditProbeKeepsConcurrentEdit(t *testing.T) {
	srv, cfgPath := newSpec116Server(t)
	_, err := srv.runtime.Commit("seed_registry", func(tx *runtime.CommitTx) error {
		return tx.MutateGlobal(func(cfg *config.Config) error {
			cfg.Registries = append(cfg.Registries, config.RegistryEntry{
				ID: "acme", Name: "Acme", URL: "https://acme.example",
				ServersURL: "https://acme.example/v0.1/servers", Provenance: config.RegistryProvenanceCustom,
			})
			return nil
		})
	})
	require.NoError(t, err)

	entered := make(chan struct{})
	release := make(chan struct{})
	stubProbe(t, func(_ context.Context, rawURL string) (*registries.SourceProbe, error) {
		close(entered)
		<-release
		return &registries.SourceProbe{ServersURL: rawURL + "/probed/servers", Protocol: "modelcontextprotocol/registry"}, nil
	})

	urlDone := make(chan error, 1)
	go func() {
		_, err := srv.EditRegistrySource(&EditRegistrySourceRequest{ID: "acme", URL: "https://acme2.example"})
		urlDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the URL edit never started probing")
	}
	_, err = srv.EditRegistrySource(&EditRegistrySourceRequest{ID: "acme", Name: "Acme Renamed"})
	require.NoError(t, err)
	close(release)
	select {
	case err := <-urlDone:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("the URL edit never finished")
	}

	check := func(where string, regs []config.RegistryEntry) {
		t.Helper()
		var got *config.RegistryEntry
		for i := range regs {
			if strings.EqualFold(regs[i].ID, "acme") {
				got = &regs[i]
			}
		}
		require.NotNil(t, got, "%s: acme must exist", where)
		assert.Equal(t, "Acme Renamed", got.Name, "%s: the concurrent name edit must survive", where)
		assert.Equal(t, "https://acme2.example", got.URL, "%s: the URL edit lands", where)
		assert.Equal(t, "https://acme2.example/probed/servers", got.ServersURL, "%s: the probe result lands", where)
		assert.Equal(t, "modelcontextprotocol/registry", got.Protocol, "%s", where)
	}
	check("live", srv.runtime.Config().Registries)
	disk, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	check("disk", disk.Registries)
}
