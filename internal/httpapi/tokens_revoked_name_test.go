package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 109-l P12 / issue #1437 item 5: revocation is a soft delete, so a revoked
// token keeps its name (activity.token_name references it). The 409 says so
// instead of claiming a live token holds the name.
func TestCreateToken_RevokedHolderConflictText(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, []string{"server1"})
	create := createTokenRequest{Name: "ro-bot", AllowedServers: []string{"server1"}, Permissions: []string{"read"}, ExpiresIn: "30d"}

	require.Equal(t, http.StatusCreated, doRequest(t, srv, http.MethodPost, "/api/v1/tokens", create).Code)

	// A live duplicate keeps the original text.
	live := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", create)
	require.Equal(t, http.StatusConflict, live.Code)
	assert.Equal(t, `A token named "ro-bot" already exists`, errorMessage(t, live.Body.Bytes()))

	require.Equal(t, http.StatusNoContent, doRequest(t, srv, http.MethodDelete, "/api/v1/tokens/ro-bot", nil).Code)

	revoked := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", create)
	require.Equal(t, http.StatusConflict, revoked.Code)
	assert.Equal(t, `A revoked token named "ro-bot" still holds this name for activity history; choose another name`, errorMessage(t, revoked.Body.Bytes()))
}

func errorMessage(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &env))
	return env.Error
}
