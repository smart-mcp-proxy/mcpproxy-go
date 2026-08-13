package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

func newClientIDGuardTestClient(t *testing.T) *Client {
	t.Helper()
	return &Client{
		config: &config.ServerConfig{
			Name: "github",
			URL:  "https://api.githubcopilot.com/mcp/",
		},
		logger: zap.NewNop(),
	}
}

// Issue #975: providers without Dynamic Client Registration (e.g. GitHub) must
// produce a structured oauth_client_id_required error instead of an
// authorization URL with an empty client_id, which the provider 404s.
func TestEmptyClientIDFlowError_EmptyClientID(t *testing.T) {
	c := newClientIDGuardTestClient(t)

	authURL := "https://github.com/login/oauth/authorize?client_id=&code_challenge=abc&code_challenge_method=S256&redirect_uri=http%3A%2F%2F127.0.0.1%3A62876%2Foauth%2Fcallback"
	flowErr := c.emptyClientIDFlowError(authURL, "corr-123", errors.New("server does not support dynamic client registration"), nil)

	require.NotNil(t, flowErr, "empty client_id in authorization URL must be rejected")
	assert.Equal(t, contracts.OAuthErrorClientIDRequired, flowErr.ErrorType)
	assert.Equal(t, contracts.OAuthCodeNoClientID, flowErr.ErrorCode)
	assert.Equal(t, "github", flowErr.ServerName)
	assert.Equal(t, "corr-123", flowErr.CorrelationID)
	assert.Contains(t, flowErr.Suggestion, "oauth.client_id")
	assert.False(t, flowErr.Success)
}

func TestEmptyClientIDFlowError_MissingClientIDParam(t *testing.T) {
	c := newClientIDGuardTestClient(t)

	authURL := "https://github.com/login/oauth/authorize?code_challenge=abc&code_challenge_method=S256"
	flowErr := c.emptyClientIDFlowError(authURL, "", nil, nil)

	require.NotNil(t, flowErr, "authorization URL without client_id param must be rejected")
	assert.Equal(t, contracts.OAuthErrorClientIDRequired, flowErr.ErrorType)
}

func TestEmptyClientIDFlowError_PresentClientID(t *testing.T) {
	c := newClientIDGuardTestClient(t)

	authURL := "https://github.com/login/oauth/authorize?client_id=Iv1.abc123&code_challenge=abc"
	flowErr := c.emptyClientIDFlowError(authURL, "corr-123", nil, nil)

	assert.Nil(t, flowErr, "a URL carrying a client_id must pass the guard")
}

func TestEmptyClientIDFlowError_ClientIDViaExtraParams(t *testing.T) {
	c := newClientIDGuardTestClient(t)

	// A client_id injected through oauth.extra_params must also satisfy the
	// guard — the check is on the final URL, not on the handler state.
	authURL := "https://example.com/authorize?client_id=from-extra-params&resource=https%3A%2F%2Fexample.com"
	flowErr := c.emptyClientIDFlowError(authURL, "", nil, nil)

	assert.Nil(t, flowErr)
}

func TestEmptyClientIDFlowError_UnparseableURL(t *testing.T) {
	c := newClientIDGuardTestClient(t)

	// An unparseable URL is not this guard's concern — never block on it.
	flowErr := c.emptyClientIDFlowError("://not-a-url", "", nil, nil)

	assert.Nil(t, flowErr)
}

// The structured error must preserve the real DCR outcome: a 403 rejection
// (e.g. Figma) keeps its status code, a provider without a registration
// endpoint (e.g. GitHub) keeps the "not supported" message — they are
// distinguishable in the error details.
func TestEmptyClientIDFlowError_DCR403Details(t *testing.T) {
	c := newClientIDGuardTestClient(t)

	authURL := "https://example.com/authorize?client_id="
	flowErr := c.emptyClientIDFlowError(authURL, "", errors.New("registration failed: HTTP 403 Forbidden"), nil)

	require.NotNil(t, flowErr)
	require.NotNil(t, flowErr.Details)
	require.NotNil(t, flowErr.Details.DCRStatus)
	assert.True(t, flowErr.Details.DCRStatus.Attempted)
	assert.False(t, flowErr.Details.DCRStatus.Success)
	assert.Equal(t, 403, flowErr.Details.DCRStatus.StatusCode)
	assert.Contains(t, flowErr.Details.DCRStatus.Error, "403")
}

func TestEmptyClientIDFlowError_DCRUnsupportedDetails(t *testing.T) {
	c := newClientIDGuardTestClient(t)

	authURL := "https://github.com/login/oauth/authorize?client_id="
	flowErr := c.emptyClientIDFlowError(authURL, "", errors.New("server does not support dynamic client registration"), nil)

	require.NotNil(t, flowErr)
	require.NotNil(t, flowErr.Details)
	require.NotNil(t, flowErr.Details.DCRStatus)
	assert.True(t, flowErr.Details.DCRStatus.Attempted)
	assert.False(t, flowErr.Details.DCRStatus.Success)
	assert.Zero(t, flowErr.Details.DCRStatus.StatusCode)
	assert.Contains(t, flowErr.Details.DCRStatus.Error, "does not support dynamic client registration")
}

func TestEmptyClientIDFlowError_NoDCRAttempt(t *testing.T) {
	c := newClientIDGuardTestClient(t)

	// nil dcrErr (DCR not attempted or succeeded): no DCRStatus fabricated.
	flowErr := c.emptyClientIDFlowError("https://example.com/authorize?client_id=", "", nil, nil)

	require.NotNil(t, flowErr)
	require.NotNil(t, flowErr.Details)
	assert.Nil(t, flowErr.Details.DCRStatus)
}

type stubClientIDProvider struct{ id string }

func (s stubClientIDProvider) GetClientID() string { return s.id }

// Codex r2 finding 1: a client_id present only via oauth.extra_params passes
// the guard (escape hatch for lenient providers) but must not be treated as a
// fully-configured client — the helper only warns; behavior is pass-through.
func TestEmptyClientIDFlowError_ExtraParamsClientIDStillPasses(t *testing.T) {
	c := newClientIDGuardTestClient(t)

	authURL := "https://example.com/authorize?client_id=from-extra-params"
	flowErr := c.emptyClientIDFlowError(authURL, "", nil, stubClientIDProvider{id: ""})

	assert.Nil(t, flowErr)
}

// Codex r2 finding 2: "Forbidden" without a literal "403" still maps to
// status_code 403 (best-effort; mcp-go errors are untyped).
func TestEmptyClientIDFlowError_ForbiddenMapsTo403(t *testing.T) {
	c := newClientIDGuardTestClient(t)

	flowErr := c.emptyClientIDFlowError("https://example.com/authorize?client_id=", "", errors.New("registration request failed: Forbidden"), nil)

	require.NotNil(t, flowErr)
	require.NotNil(t, flowErr.Details.DCRStatus)
	assert.Equal(t, 403, flowErr.Details.DCRStatus.StatusCode)
}
