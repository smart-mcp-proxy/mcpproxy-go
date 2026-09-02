package core

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

const (
	credURL   = "https://host.example/mcp?opaque=ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"
	credToken = "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"
)

// Issue #1158, review round 2, finding B7.
//
// emptyClientIDFlowError scrubbed ONE leaf of contracts.OAuthFlowError and left
// its siblings raw on the same struct: `Details.DCRStatus.Error` took
// `dcrErr.Error()` verbatim, and the log statement four lines above wrote the
// same error through zap.NamedError. The whole struct is JSON-encoded to the
// REST caller by handleServerLogin, so a DCR failure that quotes the
// registration endpoint published the credential twice.
//
// Per-field scrubbing at N construction sites is what allowed that, so the
// assertion is on the SERIALIZED struct — every leaf the caller can see —
// rather than on the one field the fix happened to touch.
func TestEmptyClientIDFlowError_ScrubsEveryLeafItPublishes(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	c := &Client{
		config: &config.ServerConfig{Name: "alpha", URL: credURL},
		logger: zap.New(core),
	}

	dcrErr := errors.New(`registration failed: Post "` + credURL + `": 403 Forbidden`)
	flowErr := c.emptyClientIDFlowError("https://auth.example/authorize?scope=read", "corr-1", dcrErr)
	require.NotNil(t, flowErr)

	encoded, err := json.Marshal(flowErr)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), credToken,
		"every leaf of this struct reaches the REST caller: %s", encoded)

	require.NotNil(t, flowErr.Details)
	require.NotNil(t, flowErr.Details.DCRStatus)
	assert.Equal(t, 403, flowErr.Details.DCRStatus.StatusCode,
		"the DCR outcome is the diagnostic and must survive the scrub")
	assert.Contains(t, flowErr.Details.DCRStatus.Error, "403",
		"the provider's status is what distinguishes a rejection from no DCR at all")
	assert.Contains(t, flowErr.Details.ServerURL, "host.example",
		"the host survives so the panel still names the server")

	var rendered string
	for _, entry := range logs.All() {
		rendered += entry.Message
		for k, v := range entry.ContextMap() {
			rendered += " " + k + "=" + toStr(v)
		}
	}
	assert.NotContains(t, rendered, credToken,
		"the log statement beside the scrubbed field must not re-publish it")
	assert.Contains(t, rendered, "dcr_error",
		"the DCR failure is still reported; only its credential is gone")
}

func toStr(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// scrubbedFlowError is the ONE place the rule now lives, so assert it covers a
// leaf that no current construction site sets — that is the property that
// makes it survive the next field somebody adds.
func TestScrubbedFlowError_CoversEveryFreeTextLeaf(t *testing.T) {
	got := scrubbedFlowError(&contracts.OAuthFlowError{
		Message:    "failed for " + credURL,
		Suggestion: "retry against " + credURL,
		DebugHint:  "see " + credURL,
		Details: &contracts.OAuthErrorDetails{
			ServerURL: credURL,
			ProtectedResourceMetadata: &contracts.MetadataStatus{
				URLChecked:           credURL,
				Error:                "fetch failed for " + credURL,
				AuthorizationServers: []string{credURL},
			},
			AuthorizationServerMetadata: &contracts.MetadataStatus{
				URLChecked: credURL,
				Error:      "fetch failed for " + credURL,
			},
			DCRStatus: &contracts.DCRStatus{Error: "register failed for " + credURL},
		},
	})

	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), credToken, "encoded: %s", encoded)
	assert.Contains(t, string(encoded), "host.example", "hosts and paths stay readable")

	assert.Nil(t, scrubbedFlowError(nil))
}

// Finding B6 at the call site: Client.logSafeURL must use the DEEP renderer.
// The name-rule-only one it used before could not see a credential under an
// unrecognised parameter name, and this value is served to REST callers inside
// OAuthErrorDetails.ServerURL.
func TestClientLogSafeURL_UsesTheDeepRenderer(t *testing.T) {
	c := &Client{config: &config.ServerConfig{Name: "alpha", URL: credURL}}
	got := c.logSafeURL()
	assert.NotContains(t, got, credToken)
	assert.Contains(t, got, "host.example/mcp")
}
