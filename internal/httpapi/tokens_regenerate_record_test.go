package httpapi

import (
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Issue #1698: POST /api/v1/tokens/{name}/regenerate minted a new secret with
// no record. It now goes through the credentials service, which writes the
// `rotate` profile_change row naming the caller.
func TestRegenerateToken_WritesRotateRecord(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	var mu sync.Mutex
	var records []*storage.ActivityRecord
	srv.SetCredentialsService(internalRuntime.NewCredentialsService(internalRuntime.CredentialsServiceDeps{
		Tokens:  store,
		HMACKey: func() ([]byte, error) { return auth.GetOrCreateHMACKey(srv.dataDir) },
		Config:  func() *config.Config { return &config.Config{APIKey: "test-api-key"} },
		Activity: func(r *storage.ActivityRecord) error {
			mu.Lock()
			defer mu.Unlock()
			records = append(records, r)
			return nil
		},
	}))

	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", createTokenRequest{Name: "regen-me", Permissions: []string{"read"}})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created createTokenResponse
	decodeSuccess(t, w, &created)

	w = doRequest(t, srv, http.MethodPost, "/api/v1/tokens/regen-me/regenerate", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var regen regenerateTokenResponse
	decodeSuccess(t, w, &regen)
	assert.Equal(t, "regen-me", regen.Name)
	assert.True(t, auth.ValidateTokenFormat(regen.Token))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, records, 2, "one issue row, one rotate row")
	rot := records[1]
	assert.Equal(t, storage.ActivityTypeProfileChange, rot.Type)
	assert.Equal(t, "rotate", rot.Metadata["change"])
	assert.Equal(t, "regen-me", rot.Metadata["token_name"])
	assert.Equal(t, "api_key", rot.Metadata["actor_kind"])
	assert.Equal(t, "api", rot.Metadata["surface"])
	diff, _ := rot.Metadata["diff"].(map[string]interface{})
	require.NotNil(t, diff)
	assert.Equal(t, "mcp_agt_newt", diff["new_token_prefix"], "the mock store's rotated prefix")
}

// The refusals keep their status codes through the service.
func TestRegenerateToken_RevokedIs409ThroughService(t *testing.T) {
	store := newMockTokenStore()
	store.regenErr = storage.ErrAgentTokenRevoked
	srv := newTestTokenServer(t, store, nil)
	srv.SetCredentialsService(internalRuntime.NewCredentialsService(internalRuntime.CredentialsServiceDeps{
		Tokens:  store,
		HMACKey: func() ([]byte, error) { return auth.GetOrCreateHMACKey(srv.dataDir) },
		Config:  func() *config.Config { return &config.Config{APIKey: "test-api-key"} },
	}))
	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", createTokenRequest{Name: "gone", Permissions: []string{"read"}})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	w = doRequest(t, srv, http.MethodPost, "/api/v1/tokens/gone/regenerate", nil)
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
}
