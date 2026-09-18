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

// TestCreateToken_CapReached pins the personal-edition 409 classification of
// both storage cap sentinels (issue #1177 / #1286).
//
// Every personal-edition token is ownerless, so only the deployment-wide
// storage.ErrAgentTokenLimitReached can fire on this surface today; the
// per-owner storage.ErrAgentTokenOwnerLimitReached is classified as well so a
// future owned-token caller gets a 409 that names the owner quota rather than
// a misleading 500. Each body states the limit it is about and nothing about
// anyone else's tokens.
//
// Oracle discipline: a positive control mints through the same server first,
// so the 409 is the classified sentinel and not an unwired store.
//
// BITES: map either sentinel to any other status, or drop the limit figure.
func TestCreateToken_CapReached(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		limit int
	}{
		{name: "deployment cap", err: storage.ErrAgentTokenLimitReached, limit: auth.MaxTokens},
		{name: "owner quota", err: storage.ErrAgentTokenOwnerLimitReached, limit: auth.MaxTokensPerOwner},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMockTokenStore()
			srv := newTestTokenServer(t, store, []string{"server1"})

			body := createTokenRequest{Name: "ci", Permissions: []string{"read"}}

			ok := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
			require.Equal(t, http.StatusCreated, ok.Code, "positive control: create must work before the cap (%s)", ok.Body.String())

			store.createErr = tc.err
			body.Name = "one-too-many"
			over := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
			require.Equal(t, http.StatusConflict, over.Code, "the cap must answer 409 (%s)", over.Body.String())

			var envelope contracts.APIResponse
			require.NoError(t, json.Unmarshal(over.Body.Bytes(), &envelope))
			require.False(t, envelope.Success)
			msg := envelope.Error
			lower := strings.ToLower(msg)

			assert.Contains(t, msg, fmt.Sprintf("(%d)", tc.limit), "the body must say what the limit is")
			for _, leak := range []string{"other users", "shared by all", "administrator"} {
				assert.NotContains(t, lower, leak, "the body must not describe anyone else's tokens")
			}
		})
	}
}
