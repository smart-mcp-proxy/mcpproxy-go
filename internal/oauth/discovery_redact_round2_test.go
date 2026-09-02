package oauth

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const opaqueCredURL = "https://host.example/mcp?opaque=ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"

// Issue #1158, review round 2, finding B2.
//
// FindWorkingMetadataURL wraps the configured server URL into its failure
// message verbatim. That error is logged and handed to REST callers, and the
// configured URL is precisely the string this issue exists for.
func TestFindWorkingMetadataURL_ErrorCarriesNoCredential(t *testing.T) {
	_, err := FindWorkingMetadataURL(opaqueCredURL, 50*time.Millisecond)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789",
		"the configured URL is spliced into this message on every discovery failure")
	assert.Contains(t, err.Error(), "host.example",
		"the host is what makes the message diagnosable and must survive")
}

// Issue #1158, review round 2, finding B3.
//
// A *url.Error renders the request URL inside its own text, so an HTTP failure
// path publishes the credential through zap.Error even when the sibling `url`
// field is masked. logSafeErrorField is the answer; assert it on a REAL
// *url.Error rather than a hand-written string.
func TestLogSafeErrorField_ScrubsAUrlErrorFromARealRequest(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Millisecond}
	//nolint:noctx // the point is the *url.Error this produces
	_, err := client.Get("http://127.0.0.1:1/mcp?opaque=ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789",
		"sanity: net/http really does quote the request URL in its error")

	field := logSafeErrorField(err)
	assert.NotContains(t, field.String, "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789")
	assert.Contains(t, field.String, "127.0.0.1",
		"the host and the failure reason are the diagnostic")

	assert.Equal(t, "", LogSafeErrorText(nil))
	assert.NotContains(t, LogSafeErrorText(fmt.Errorf("wrapped: %w", err)),
		"ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789")
	assert.NotContains(t, logSafeNamedErrorField("dcr_error", errors.New(opaqueCredURL)).String,
		"ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789")
}

// Issue #1158, review round 2, third investigation item: the PRM's own
// authorization_servers list left createMetadataError unscrubbed while every
// sibling URL leaf on the same struct was scrubbed.
func TestCreateMetadataError_ScrubsThePRMAuthorizationServers(t *testing.T) {
	err := createMetadataError("alpha", "https://host.example/mcp", &OAuthMetadataValidationResult{
		ProtectedResourceMetadataURL: "https://host.example/.well-known/oauth-protected-resource",
		ProtectedResourceMetadata: &ProtectedResourceMetadata{
			AuthorizationServers: []string{opaqueCredURL},
		},
	})
	require.Error(t, err)

	metaErr, ok := err.(*OAuthMetadataError)
	require.True(t, ok)
	require.NotNil(t, metaErr.Details.ProtectedResourceMetadata)
	servers := metaErr.Details.ProtectedResourceMetadata.AuthorizationServers
	require.Len(t, servers, 1)

	assert.NotContains(t, servers[0], "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789",
		"this list is JSON-encoded to the REST caller alongside leaves that ARE scrubbed")
	assert.Contains(t, servers[0], "host.example",
		"the panel exists to say WHICH authorization server was advertised")
}

// Issue #1158, review round 2, finding B6: the exported renderer every package
// now shares must be the DEEP one. RedactURLQueryParams — the rule the other
// packages used — is name-rule only, so a credential under an unrecognised
// parameter name walked straight through it.
func TestLogSafeURL_IsDeeperThanTheNameRule(t *testing.T) {
	assert.Contains(t, RedactURLQueryParams(opaqueCredURL), "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789",
		"sanity: the name rule is what missed it; if this stops holding the contrast means nothing")

	got := LogSafeURL(opaqueCredURL)
	assert.NotContains(t, got, "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789")
	assert.Contains(t, got, "host.example/mcp", "scheme, host and path survive")

	// And it still decodes one level of nesting, which is where the authorize
	// URL hides the configured upstream URL.
	nested := "https://auth.example/authorize?client_id=x&resource=" +
		"https%3A%2F%2Fhost.example%2Fmcp%3Ftoken%3Dsk-live-11aa22bb33cc"
	assert.NotContains(t, LogSafeURL(nested), "sk-live-11aa22bb33cc")

	// Idempotent: the structured-error scrubber applies it to values that were
	// already rendered safely.
	assert.Equal(t, got, LogSafeURL(got))
}
