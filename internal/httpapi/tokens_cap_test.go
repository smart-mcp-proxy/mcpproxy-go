package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestCreateToken_CapReached pins the personal-edition 409 for
// storage.ErrAgentTokenLimitReached (Spec 107 FR-037, issue #1177).
//
// The cap is now enforced per owner in storage. Every personal-edition token is
// ownerless, so on this surface the caller owns every token that counts: the
// body's MEANING is unchanged — "you are at the maximum, free one of yours" —
// and it must reference only the caller's own count, never a deployment total.
//
// Oracle discipline: a positive control mints through the same server first,
// so the 409 is the classified sentinel and not an unwired store.
//
// BITES: map the sentinel to any other status, drop the limit figure, or
// describe the deployment instead of the caller.
func TestCreateToken_CapReached(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, []string{"server1"})

	body := createTokenRequest{Name: "ci", Permissions: []string{"read"}}

	ok := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
	require.Equal(t, http.StatusCreated, ok.Code, "positive control: create must work before the cap (%s)", ok.Body.String())

	store.createErr = storage.ErrAgentTokenLimitReached
	body.Name = "one-too-many"
	over := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
	require.Equal(t, http.StatusConflict, over.Code, "the cap must answer 409 (%s)", over.Body.String())

	var envelope contracts.APIResponse
	require.NoError(t, json.Unmarshal(over.Body.Bytes(), &envelope))
	require.False(t, envelope.Success)
	msg := envelope.Error
	lower := strings.ToLower(msg)

	assert.Contains(t, msg, fmt.Sprintf("%d", auth.MaxTokens), "the body must say what the limit is")
	assert.Contains(t, lower, "your", "the body must address the caller's own tokens")
	for _, leak := range []string{"deployment", "shared by all", "all users", "administrator"} {
		assert.NotContains(t, lower, leak, "the body must reference only the caller's own count")
	}
}
