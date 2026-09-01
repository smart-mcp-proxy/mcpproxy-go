package oauth

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Issue #1148: the *config.ServerConfig door. RedactServerConfigSecrets is what
// the server edition's `GET /api/v1/user/servers` hands an ordinary user for an
// ADMIN-configured shared upstream, so a leaf that escapes it is a
// cross-tenant credential disclosure.

// The same general form as TestRedactServerSecretFields_MasksEverySecretBearingLeaf,
// derived from the decision table rather than from a list of fields somebody
// remembered: fill EVERY text-carrying leaf of config.ServerConfig with a
// credential, redact, and require that the only places it survives are the ones
// explicitly recorded as not-secret.
func TestRedactServerConfigSecrets_MasksEverySecretBearingLeaf(t *testing.T) {
	sc := &config.ServerConfig{}
	fillTextLeaves(reflect.ValueOf(sc).Elem(), ghpToken)

	masked := RedactServerConfigSecrets(sc)
	require.NotNil(t, masked)

	for _, path := range findTokenPaths(t, masked, ghpToken) {
		decision := decisionForPath(path)
		assert.Equal(t, MaskDecisionNotSecret, decision,
			"config.ServerConfig.%s published the credential verbatim, but its decision is %q — "+
				"a field that can carry a secret must be masked on this door too", path, decision)
	}
}

// The named shapes, spelled out, so a regression reads as the credential it is
// rather than as a reflection walk.
func TestRedactServerConfigSecrets_MasksTheNamedShapes(t *testing.T) {
	sc := secretBearingServerConfig()

	masked := RedactServerConfigSecrets(sc)
	require.NotNil(t, masked)

	assert.NotContains(t, masked.Headers["Authorization"], "hdrsecretvalue")
	assert.NotContains(t, masked.Headers["X-Api-Key"], "apikeysecretvalue")
	assert.NotContains(t, masked.Env["GITHUB_TOKEN"], "envsecretvalue")
	assert.NotContains(t, masked.URL, "urlsecretvalue")
	assert.NotContains(t, masked.OAuth.ClientSecret, "oauthsecretvalue")
	for _, arg := range masked.Args {
		assert.NotContains(t, arg, "argvsecretvalue")
	}
	for _, arg := range masked.Isolation.ExtraArgs {
		assert.NotContains(t, arg, "isosecretvalue")
	}

	// Still readable: the mask says WHICH credential is configured, it does not
	// blank the payload.
	assert.Equal(t, "https", mustParseScheme(t, masked.URL))
	assert.Contains(t, masked.Headers, "Authorization")
	assert.Contains(t, masked.Env, "GITHUB_TOKEN")
}

// The caller's config is typically the LIVE one (`h.sharedServers` is the
// admin's running configuration), so rendering a read response must neither
// mutate it nor hand back anything that still aliases it — writing a mask
// through a shared backing array is the #1142/#1146 corruption with a whole
// deployment as the blast radius.
func TestRedactServerConfigSecrets_LeavesTheInputUntouched(t *testing.T) {
	sc := secretBearingServerConfig()
	pristine := secretBearingServerConfig()

	masked := RedactServerConfigSecrets(sc)
	require.NotNil(t, masked)

	assert.Equal(t, pristine, sc, "redacting a read payload mutated the config it was built from")

	// No shared backing storage: mutating the masked copy must not reach the
	// original.
	masked.Headers["Authorization"] = "rewritten"
	masked.Env["GITHUB_TOKEN"] = "rewritten"
	masked.Args[0] = "rewritten"
	masked.Isolation.ExtraArgs[0] = "rewritten"
	masked.OAuth.ClientSecret = "rewritten"
	assert.Equal(t, pristine, sc, "the masked copy still aliases the live config")
}

// RedactServerConfigSecrets decodes the masked view back into a ServerConfig,
// so a field that does not survive that round trip would silently vanish from
// the response. Pin the structural (non-secret) fields.
func TestRedactServerConfigSecrets_PreservesNonSecretFields(t *testing.T) {
	autoApprove := true
	exposePrompts := false
	maxConcurrent := 7
	interval := config.Duration(90 * time.Second)
	sc := &config.ServerConfig{
		Name:                     "shared-github",
		Protocol:                 "http",
		Command:                  "npx",
		WorkingDir:               "/srv/mcp",
		URL:                      "https://api.github.com/mcp",
		Enabled:                  true,
		Quarantined:              true,
		TrustMode:                string(config.TrustModeScan),
		Shared:                   true,
		Created:                  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Updated:                  time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC),
		ReconnectOnUse:           true,
		AutoApproveToolChanges:   &autoApprove,
		ExposePrompts:            &exposePrompts,
		MaxConcurrentRequests:    &maxConcurrent,
		HealthCheckInterval:      &interval,
		LauncherWaitTimeout:      config.Duration(45 * time.Second),
		ToonOutput:               "adaptive",
		EnabledTools:             []string{"create_issue"},
		SourceRegistryID:         "official",
		SourceRegistryProvenance: "official",
	}

	masked := RedactServerConfigSecrets(sc)
	require.NotNil(t, masked)

	assert.Equal(t, sc.Name, masked.Name)
	assert.Equal(t, sc.Protocol, masked.Protocol)
	assert.Equal(t, sc.Command, masked.Command)
	assert.Equal(t, sc.WorkingDir, masked.WorkingDir)
	assert.Equal(t, sc.URL, masked.URL, "a URL with no credential in it must round-trip verbatim")
	assert.Equal(t, sc.Enabled, masked.Enabled)
	assert.Equal(t, sc.Quarantined, masked.Quarantined)
	assert.Equal(t, sc.TrustMode, masked.TrustMode)
	assert.Equal(t, sc.Shared, masked.Shared)
	assert.Equal(t, sc.Created, masked.Created)
	assert.Equal(t, sc.Updated, masked.Updated)
	assert.Equal(t, sc.ReconnectOnUse, masked.ReconnectOnUse)
	require.NotNil(t, masked.AutoApproveToolChanges)
	assert.True(t, *masked.AutoApproveToolChanges)
	require.NotNil(t, masked.ExposePrompts)
	assert.False(t, *masked.ExposePrompts, "an explicit false must survive as an explicit false")
	require.NotNil(t, masked.MaxConcurrentRequests)
	assert.Equal(t, maxConcurrent, *masked.MaxConcurrentRequests)
	require.NotNil(t, masked.HealthCheckInterval)
	assert.Equal(t, interval, *masked.HealthCheckInterval)
	assert.Equal(t, sc.LauncherWaitTimeout, masked.LauncherWaitTimeout)
	assert.Equal(t, sc.ToonOutput, masked.ToonOutput)
	assert.Equal(t, sc.EnabledTools, masked.EnabledTools)
	assert.Equal(t, sc.SourceRegistryID, masked.SourceRegistryID)
	assert.Equal(t, sc.SourceRegistryProvenance, masked.SourceRegistryProvenance)
}

func TestRedactServerConfigSecrets_NilIsNil(t *testing.T) {
	assert.Nil(t, RedactServerConfigSecrets(nil))
	assert.Nil(t, RedactedServerConfigView(nil, LiveRedaction))
}

// The view and the typed copy are the SAME rule seen through two shapes: the
// MCP doors take the map, the server edition's ServerResponse takes the struct.
// A door that drifted from the other is what this issue keeps producing.
func TestRedactServerConfigSecrets_AgreesWithTheMapView(t *testing.T) {
	sc := secretBearingServerConfig()

	view := RedactedServerConfigView(sc, LiveRedaction)
	typed := RedactServerConfigSecrets(sc)
	require.NotNil(t, typed)

	assert.Equal(t, view, NormalizeForRedaction(typed),
		"the typed copy and the map view must render the same payload")
}

func secretBearingServerConfig() *config.ServerConfig {
	return &config.ServerConfig{
		Name:     "shared-secrets",
		URL:      "https://api.example.com/mcp?access_token=urlsecretvalue1234567890",
		Protocol: "http",
		Command:  "npx",
		Args:     []string{"server", "--api-key", "argvsecretvalue1234567890"},
		Env:      map[string]string{"GITHUB_TOKEN": "envsecretvalue1234567890"},
		Headers: map[string]string{
			"Authorization": "Bearer hdrsecretvalue1234567890",
			"X-Api-Key":     "apikeysecretvalue1234567890",
		},
		OAuth: &config.OAuthConfig{
			ClientID:     "public-client-id",
			ClientSecret: "oauthsecretvalue1234567890",
		},
		Isolation: &config.IsolationConfig{
			ExtraArgs: []string{"-e", "API_KEY=isosecretvalue1234567890"},
		},
		Enabled: true,
		Shared:  true,
		Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func mustParseScheme(t *testing.T, raw string) string {
	t.Helper()
	for i := 0; i < len(raw); i++ {
		if raw[i] == ':' {
			return raw[:i]
		}
	}
	return ""
}

// GH #1145's `retry_stopped_reason` arrived on contracts.Server from main while
// this branch was open, and the coverage canary caught it with no decision.
//
// Its name says "catalog message", but diagnostics.PermanentFailureReason falls
// through to the RAW error whenever the terminal code has no catalog entry —
// and that raw error is ci.LastError.Error(), which
// core.enrichTransportClosedError folds the child process's captured stderr
// into. So it is upstream free text of exactly the last_error kind, and it must
// be scrubbed rather than trusted for its name.
func TestRedactServerSecretFields_ScrubsRetryStoppedReason(t *testing.T) {
	srv := &contracts.Server{
		Name: "parked",
		RetryStoppedReason: "dial https://api.example.com/mcp?access_token=" + ghpToken +
			" failed; recent stderr:\nAPI_KEY=" + ghpToken,
		RetryStoppedCode: "MCPX_STDIO_EXEC_NOT_FOUND",
	}

	RedactServerSecretFields(srv)

	assert.NotContains(t, srv.RetryStoppedReason, ghpToken,
		"retry_stopped_reason falls back to the raw upstream error, so it carries the same "+
			"credentials last_error does and takes the same scrub")
	assert.Equal(t, "MCPX_STDIO_EXEC_NOT_FOUND", srv.RetryStoppedCode,
		"the terminal code is a stable catalog identifier this proxy produced; blanking it "+
			"would cost the operator the one field that says WHY the server parked")
}
