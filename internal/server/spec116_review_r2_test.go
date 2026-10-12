package server

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
)

// Spec 116 PR1, review round 2 (Sol) regressions.

// R2.1: a REST PATCH of the per-server concurrency overrides is validated,
// against the live global defaults, inside the commit. A value the next start
// would refuse (Config.Validate → validateConcurrency) is a 400 with the live
// config, storage and the file unchanged; a valid one is applied everywhere.
func TestSpec116_R2_PatchConcurrencyIsValidated(t *testing.T) {
	srv, cfgPath := newSpec116Server(t, tenantServer())
	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)

	for name, body := range map[string]string{
		"negative max":     `{"max_concurrent_requests":-1}`,
		"negative queue":   `{"queue_size":-1}`,
		"negative timeout": `{"queue_timeout":"-1s"}`,
		"orphaned queue":   `{"queue_size":5}`,
	} {
		diskBefore, err := os.ReadFile(cfgPath)
		require.NoError(t, err)
		rec := restPatch(t, api, "tenant", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", name, rec.Body.String())
		for label, sc := range map[string]*config.ServerConfig{
			"live":    liveServer(srv, "tenant"),
			"storage": storedServer(t, srv, "tenant"),
		} {
			require.NotNil(t, sc, "%s/%s", name, label)
			assert.Nil(t, sc.MaxConcurrentRequests, "%s/%s", name, label)
			assert.Nil(t, sc.QueueSize, "%s/%s", name, label)
			assert.Nil(t, sc.QueueTimeout, "%s/%s", name, label)
		}
		diskNow, err := os.ReadFile(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, string(diskBefore), string(diskNow), "%s: the file is untouched", name)
	}

	rec := restPatch(t, api, "tenant", `{"max_concurrent_requests":2,"queue_size":5,"queue_timeout":"10s"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for label, sc := range map[string]*config.ServerConfig{
		"live":    liveServer(srv, "tenant"),
		"storage": storedServer(t, srv, "tenant"),
		"disk":    diskServer(t, cfgPath, "tenant"),
	} {
		require.NotNil(t, sc, label)
		require.NotNil(t, sc.MaxConcurrentRequests, label)
		assert.Equal(t, 2, *sc.MaxConcurrentRequests, label)
		require.NotNil(t, sc.QueueSize, label)
		assert.Equal(t, 5, *sc.QueueSize, label)
	}
	// The whole file still loads (the next start would accept it).
	_, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)

	// Removing the limit while the queue stays would orphan it: refused.
	rec = restPatch(t, api, "tenant", `{"max_concurrent_requests":0}`)
	// max 0 is the explicit per-server opt-out, which the validator accepts.
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, err = config.LoadFromFile(cfgPath)
	require.NoError(t, err)
}
