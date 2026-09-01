package oauth

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Issue #1148, round 6. Every round of this review found the same shape: a rule
// applied at one door and not its sibling. These tests pin the invariant the
// branch is now structured around — ONE redaction rule and ONE revert rule,
// shared by every read and every write door.

// ghpToken is a GitHub personal access token in the shape the value-shaped
// detector recognises. It is what a NAME rule can never see: no field name,
// query parameter or header name says "this holds a credential".
const ghpToken = "ghp_1234567890abcdefghijABCDEFGHIJ123456"

// ghpMasked is what the detector renders it as.
const ghpMasked = "ghp_…****"

// ROUND 6, FINDING 1 — the value-shaped detector must run PER COMPONENT of a
// URL, not be gated on whether the whole URL string changed.
//
// The old guard skipped the detector for the entire string as soon as the NAME
// rule rewrote any one component, so a URL holding TWO credentials published
// the second one verbatim.
func TestLiveRedaction_URLDetectorRunsPerComponent(t *testing.T) {
	cases := []struct {
		name        string
		rawURL      string
		mustSurvive []string
	}{
		{
			name:        "no name rule fires",
			rawURL:      "https://host/mcp?opaque=" + ghpToken,
			mustSurvive: []string{"https://host/mcp", "opaque="},
		},
		{
			name:        "name rule masks a SIBLING query parameter",
			rawURL:      "https://host/mcp?token=abc123def456&opaque=" + ghpToken,
			mustSurvive: []string{"https://host/mcp", "opaque="},
		},
		{
			name:        "name rule masks the userinfo password",
			rawURL:      "https://u:supersecretpw@host/mcp?opaque=" + ghpToken,
			mustSurvive: []string{"@host/mcp", "opaque="},
		},
		{
			name:        "credential sits in the path",
			rawURL:      "https://host/" + ghpToken + "/mcp",
			mustSurvive: []string{"https://host/"},
		},
		{
			name:        "credential sits in the fragment",
			rawURL:      "https://host/mcp#" + ghpToken,
			mustSurvive: []string{"https://host/mcp"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LiveRedaction.Leaf("url", tc.rawURL)
			assert.NotContains(t, got, ghpToken, "credential published verbatim: %s", got)
			assert.Contains(t, got, ghpMasked, "credential should carry the detector's mask: %s", got)
			for _, keep := range tc.mustSurvive {
				assert.Contains(t, got, keep,
					"#872 component-wise rendering must survive: %s", got)
			}
		})
	}
}

// The same shape one leaf down: a connection string under a benign env name
// whose userinfo password the NAME rule masks, holding a second credential in
// its query string.
func TestLiveRedaction_EnvURLValueDetectsBesideTheNameMask(t *testing.T) {
	got := LiveRedaction.EnvValue("DATABASE_URL", "postgres://u:supersecretpw@host/db?opaque="+ghpToken)
	assert.NotContains(t, got, ghpToken, "credential published verbatim: %s", got)
	assert.Contains(t, got, "@host/db", "#872 component-wise rendering must survive: %s", got)
	assert.NotContains(t, got, "supersecretpw", "userinfo password must stay masked: %s", got)
}

// And on a header value whose name rule fired on an embedded `token=` pair.
func TestLiveRedaction_HeaderDetectsBesideTheNameMask(t *testing.T) {
	got := LiveRedaction.HeaderValue("X-Weird", "token=abc123def456 "+ghpToken)
	assert.NotContains(t, got, ghpToken, "credential published verbatim: %s", got)
}

// ROUND 6, FINDING 2 — RedactServerSecretFields (the REST + SSE door) must
// apply the SAME rule as the MCP door, value-shaped detector included.
func TestRedactServerSecretFields_AppliesTheSharedLeafRule(t *testing.T) {
	srv := &contracts.Server{
		Name:    "s",
		URL:     "https://host/mcp?opaque=" + ghpToken,
		Env:     map[string]string{"BENIGN": ghpToken},
		Headers: map[string]string{"X-Weird": ghpToken},
	}
	RedactServerSecretFields(srv)

	assert.Equal(t, LiveRedaction.EnvValue("BENIGN", ghpToken), srv.Env["BENIGN"],
		"REST env must render exactly what the MCP door renders")
	assert.Equal(t, LiveRedaction.HeaderValue("X-Weird", ghpToken), srv.Headers["X-Weird"],
		"REST headers must render exactly what the MCP door renders")
	assert.Equal(t, LiveRedaction.Leaf("url", "https://host/mcp?opaque="+ghpToken), srv.URL,
		"REST url must render exactly what the MCP door renders")

	for _, got := range []string{srv.Env["BENIGN"], srv.Headers["X-Weird"], srv.URL} {
		assert.NotContains(t, got, ghpToken, "credential published verbatim: %s", got)
	}
}

// ROUND 6, FINDING 3 — oauth.scopes is masked on the MCP door; the REST/SSE
// door must not answer differently for the same field.
func TestRedactServerSecretFields_MasksOAuthScopes(t *testing.T) {
	srv := &contracts.Server{
		Name: "s",
		OAuth: &contracts.OAuthConfig{
			Scopes:      []string{"read:user", ghpToken},
			ExtraParams: map[string]string{"resource": "https://api/x"},
		},
	}
	RedactServerSecretFields(srv)

	require.Len(t, srv.OAuth.Scopes, 2)
	assert.Equal(t, "read:user", srv.OAuth.Scopes[0], "an ordinary scope stays readable")
	assert.NotContains(t, srv.OAuth.Scopes[1], ghpToken, "credential-shaped scope published verbatim")
	assert.True(t, strings.Contains(srv.OAuth.Scopes[1], ghpMasked),
		"credential-shaped scope should carry the detector's mask: %s", srv.OAuth.Scopes[1])
}

// ROUND 6, GUIDING PRINCIPLE — the per-field decision is made ONCE, and a field
// added to either server struct cannot silently opt out of it.
//
// The guard itself now lives in round7_derived_net_test.go: round 7 finding 4
// found THIS version failing open, because it reflected over TOP-LEVEL fields
// only and every credential-bearing leaf that round found lives one level down.
// See TestServerFieldMaskDecisions_CoverEveryNestedLeaf and
// TestServerFieldMaskDecisions_HaveNoStaleNestedEntries.

func jsonFieldName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	return name
}

// ROUND 6, FINDING 2's OTHER HALF — masking a value on the REST read door is
// only safe if the REST write door can revert exactly that rendering. These
// pin the round trip at the rule level; internal/httpapi's
// round6_write_parity_test.go pins it through the actual handler.
func TestUnmaskLive_RevertsWhatTheLiveDoorRendered(t *testing.T) {
	t.Run("env", func(t *testing.T) {
		stored := map[string]string{"BENIGN": ghpToken}
		echoed := map[string]string{"BENIGN": LiveRedaction.EnvValue("BENIGN", ghpToken)}
		require.NotEqual(t, stored, echoed, "precondition: the read door masks it")
		assert.Equal(t, stored, UnmaskLiveEnvValues(echoed, stored))
	})

	t.Run("headers", func(t *testing.T) {
		stored := map[string]string{"X-Weird": ghpToken}
		echoed := map[string]string{"X-Weird": LiveRedaction.HeaderValue("X-Weird", ghpToken)}
		require.NotEqual(t, stored, echoed, "precondition: the read door masks it")
		assert.Equal(t, stored, UnmaskLiveHeaders(echoed, stored))
	})

	t.Run("url, unedited", func(t *testing.T) {
		stored := "https://host/mcp?opaque=" + ghpToken
		got, err := UnmaskLiveURL(LiveRedaction.URLValue(stored), stored)
		require.NoError(t, err)
		assert.Equal(t, stored, got)
	})

	t.Run("url, edited elsewhere — bound per parameter", func(t *testing.T) {
		stored := "https://host/mcp?opaque=" + ghpToken
		edited := strings.Replace(LiveRedaction.URLValue(stored), "/mcp?", "/mcp/v2?", 1)
		got, err := UnmaskLiveURL(edited, stored)
		require.NoError(t, err)
		assert.Equal(t, "https://host/mcp/v2?opaque="+ghpToken, got)
	})

	t.Run("url, moved to another host — refused, never relocated", func(t *testing.T) {
		stored := "https://host/mcp?opaque=" + ghpToken
		moved := strings.Replace(LiveRedaction.URLValue(stored), "https://host/", "https://evil.example/", 1)
		_, err := UnmaskLiveURL(moved, stored)
		require.Error(t, err, "a mask that cannot be bound must be refused, not written through")
	})

	t.Run("a mask relocated to a key it was never read from stays masked", func(t *testing.T) {
		stored := map[string]string{"BENIGN": ghpToken}
		echoed := map[string]string{"OTHER": LiveRedaction.EnvValue("BENIGN", ghpToken)}
		got := UnmaskLiveEnvValues(echoed, stored)
		assert.NotEqual(t, ghpToken, got["OTHER"], "a secret must never move to another key")
		require.Error(t, CheckServerWriteMasks("server.", map[string]interface{}{"env": got}),
			"and the residual net must then refuse the write")
	})
}
