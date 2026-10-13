package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// This file collects the round-4 review findings on issue #1148.
//
// Findings 1 and 2 are the same class as the argv one: #1148 started MASKING a
// field on a live read surface without giving the write path a matching,
// key-bound revert (or a refusal), so a read-modify-write client persists the
// MASK STRING over the real credential.

// ---------------------------------------------------------------------------
// Finding 1: oauth_json REPLACES the whole oauth block, and the live view masks
// extra_params values (and can mask a scope) — but unmaskLiveOAuth reverted
// ONLY client_secret.
// ---------------------------------------------------------------------------

const round4GitHubToken = "ghp_16Cabcdefghijklmnopqrstuvwxyz0123456789"

func TestOAuthJSON_ExtraParamMask_IsRevertedNotPersisted(t *testing.T) {
	stored := &config.OAuthConfig{
		ClientID:     "client-abc",
		ClientSecret: leakySecrets["oauth"],
		ExtraParams: map[string]string{
			// RFC 8707 resource indicator carrying a signed URL: the live view
			// masks `sig` via the URL rule.
			"resource": "https://api.example.com/mcp?sig=" + leakySecrets["url"] + "&region=eu",
			"audience": "tenant-a",
		},
	}

	view := redactedServerView(&config.ServerConfig{Name: "srv", OAuth: stored}, liveRedaction)
	oauthView, ok := view["oauth"].(map[string]interface{})
	require.True(t, ok, "the live view renders the oauth block")
	params, ok := oauthView["extra_params"].(map[string]interface{})
	require.True(t, ok, "the live view renders extra_params")
	require.NotContains(t, params["resource"], leakySecrets["url"],
		"precondition: the read path masks the signed resource URL")

	// A read-modify-write client echoes the rendered block straight back.
	echoed := roundTripOAuthView(t, oauthView)
	require.NoError(t, unmaskLiveOAuth(echoed, stored))

	assert.Equal(t, stored.ExtraParams["resource"], echoed.ExtraParams["resource"],
		"an echoed extra_params mask must be reverted, never persisted over the credential")
	assert.Equal(t, stored.ClientSecret, echoed.ClientSecret)
	assert.Equal(t, "tenant-a", echoed.ExtraParams["audience"], "non-secret params round-trip verbatim")
}

func TestOAuthJSON_ResidualMask_IsRefused(t *testing.T) {
	// A scope has no key to bind a revert to — its only context is its position
	// in a caller-supplied slice — so a mask that survives the key-bound
	// reverts must be REFUSED, exactly like an argv token.
	stored := &config.OAuthConfig{
		ClientID: "client-abc",
		Scopes:   []string{"read:user", round4GitHubToken},
	}

	view := redactedServerView(&config.ServerConfig{Name: "srv", OAuth: stored}, liveRedaction)
	oauthView := view["oauth"].(map[string]interface{})
	scopes, ok := oauthView["scopes"].([]interface{})
	require.True(t, ok)
	require.NotContains(t, scopes[1], round4GitHubToken, "precondition: the read path masks the token-shaped scope")

	echoed := roundTripOAuthView(t, oauthView)
	err := unmaskLiveOAuth(echoed, stored)
	require.Error(t, err, "a mask with no key to bind to must be refused, not written through")
	assert.NotContains(t, err.Error(), round4GitHubToken)
}

func TestOAuthJSON_UnmaskedBlock_IsAccepted(t *testing.T) {
	stored := &config.OAuthConfig{ClientID: "client-abc", ClientSecret: leakySecrets["oauth"]}
	incoming := &config.OAuthConfig{ClientID: "client-abc", ClientSecret: "brand-new-secret"}
	require.NoError(t, unmaskLiveOAuth(incoming, stored))
	assert.Equal(t, "brand-new-secret", incoming.ClientSecret, "a rotated secret is written through")
}

func roundTripOAuthView(t *testing.T, oauthView map[string]interface{}) *config.OAuthConfig {
	t.Helper()
	raw, err := json.Marshal(oauthView)
	require.NoError(t, err)
	var out config.OAuthConfig
	require.NoError(t, json.Unmarshal(raw, &out))
	return &out
}

// ---------------------------------------------------------------------------
// Finding 2: editing any OTHER part of a URL defeats the whole-URL echo check,
// and oauth.UnmaskURL only knows the SENSITIVE query params — so a mask the
// value-shaped detector produced under an unrecognised parameter name was
// written through as the credential.
// ---------------------------------------------------------------------------

func TestUnmaskLiveURL_RevertsPerParameterAcrossAnEdit(t *testing.T) {
	stored := "https://host/old?opaque=" + round4GitHubToken + "&region=eu"
	rendered := redactStringWith("url", stored, liveRedaction)
	require.NotContains(t, rendered, round4GitHubToken, "precondition: the live detector masks the token")

	// The client edits the PATH and echoes everything else back.
	incoming := strings.Replace(rendered, "/old", "/new", 1)
	out, err := unmaskLiveURL(incoming, stored)
	require.NoError(t, err)
	assert.Contains(t, out, round4GitHubToken, "the real credential must survive an unrelated edit")
	assert.Contains(t, out, "/new", "the edit must be applied")
	assert.Contains(t, out, "region=eu")
}

func TestUnmaskLiveURL_RefusesAMaskItCannotBind(t *testing.T) {
	stored := "https://host/old?opaque=" + round4GitHubToken
	rendered := redactStringWith("url", stored, liveRedaction)

	t.Run("a moved authority is refused, never restored", func(t *testing.T) {
		moved := strings.Replace(rendered, "host", "evil.example", 1)
		out, err := unmaskLiveURL(moved, stored)
		require.Error(t, err, "a mask must never be persisted, and never follow the URL to a new host")
		assert.NotContains(t, out, round4GitHubToken)
	})

	t.Run("an unedited echo still reverts", func(t *testing.T) {
		out, err := unmaskLiveURL(rendered, stored)
		require.NoError(t, err)
		assert.Equal(t, stored, out)
	})

	t.Run("a genuinely new URL is written through", func(t *testing.T) {
		out, err := unmaskLiveURL("https://host/new?opaque=fresh", stored)
		require.NoError(t, err)
		assert.Equal(t, "https://host/new?opaque=fresh", out)
	})
}

// ---------------------------------------------------------------------------
// Finding 4: /api/v1/servers/{id}/logs is the REST twin of `tail_log` — same
// stored child-process and connection log lines, no scrubbing.
// ---------------------------------------------------------------------------

func TestParseLogLine_ScrubsCredentials(t *testing.T) {
	lines := []string{
		`2025-01-20 15:04:05 [INFO] Starting connection attempt url=https://host/mcp?token=` + leakySecrets["url"],
		`child stdout: using ` + round4GitHubToken,
	}

	var entries []contracts.LogEntry
	for _, line := range lines {
		entries = append(entries, parseLogLine(line, "leaky"))
	}

	assert.NotContains(t, entries[0].Message, leakySecrets["url"],
		"the REST logs endpoint republishes the URL credential mcpproxy itself logged")
	assert.Contains(t, entries[0].Message, "Starting connection attempt", "the diagnostic content must survive")
	assert.Equal(t, "INFO", entries[0].Level)
	assert.NotContains(t, entries[1].Message, round4GitHubToken,
		"a credential the child process printed must not leave through the REST door either")
}
