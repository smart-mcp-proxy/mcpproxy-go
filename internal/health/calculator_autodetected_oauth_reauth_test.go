package health

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// An enabled, non-quarantined server with autodetected OAuth (OAuthRequired
// is false by design — see the comment above quarantinedAwaitingSignIn)
// whose stored token breaks must surface the same Login CTA as one with
// configured OAuth. Before this fix, the "error"/"disconnected" branches
// gated the reauth/login markers behind `input.OAuthRequired`, so this exact
// production message — the connection_oauth.go server-5xx path, "... server
// error with stored token ... re-login available ..." — fell through to the
// generic Restart action while diagnostics.classifyOAuth (which has no
// OAuthRequired hint at all) still assigned MCPX_OAUTH_REAUTH_REQUIRED. The
// Web UI's ServerCard then rendered BOTH a Restart button (from
// health.action) and a Login button (from the OAuth diagnostic code,
// independent of health.action), with no explanation, because showLogin also
// suppresses the error alert.
func TestCalculateHealth_EnabledAutodetectedOAuthReauthIsLogin(t *testing.T) {
	reauthErr := "OAuth authentication required for github: server error with stored token - re-login available via Web UI, system tray menu, or 'mcpproxy auth login' CLI command"
	cases := []struct {
		name  string
		state string
	}{
		{"error state", "error"},
		{"disconnected state", "disconnected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := CalculateHealth(HealthCalculatorInput{
				Name:          "github",
				Enabled:       true,
				Quarantined:   false,
				State:         tc.state,
				OAuthRequired: false,
				LastError:     reauthErr,
			}, nil)
			assert.Equal(t, ActionLogin, result.Action, "the reauth marker is specific enough to trust without OAuthRequired")
			assert.Equal(t, LevelUnhealthy, result.Level, "a broken stored token is not healthy even for autodetected OAuth")
			assert.Equal(t, StateEnabled, result.AdminState)
			assert.Equal(t, reauthErr, result.Detail)
		})
	}
}

// Same marker, but OAuthRequired=true (explicitly configured OAuth) must
// behave identically — this is the control the fix must not regress.
func TestCalculateHealth_EnabledConfiguredOAuthReauthIsLogin(t *testing.T) {
	reauthErr := "OAuth token refresh failed: invalid_grant, re-login available via Web UI"
	result := CalculateHealth(HealthCalculatorInput{
		Name:          "github",
		Enabled:       true,
		Quarantined:   false,
		State:         "error",
		OAuthRequired: true,
		LastError:     reauthErr,
	}, nil)
	assert.Equal(t, ActionLogin, result.Action)
	assert.Equal(t, LevelUnhealthy, result.Level)
}

// A first-time login-required marker on an autodetected-OAuth, enabled,
// non-quarantined server must read amber Login, mirroring the reauth case
// above and the already-fixed quarantined path.
func TestCalculateHealth_EnabledAutodetectedOAuthLoginRequiredIsLogin(t *testing.T) {
	loginErr := "OAuth authentication required for server 'github' - login available via Web UI or 'mcpproxy auth login --server=github'"
	result := CalculateHealth(HealthCalculatorInput{
		Name:          "github",
		Enabled:       true,
		Quarantined:   false,
		State:         "disconnected",
		OAuthRequired: false,
		LastError:     loginErr,
	}, nil)
	assert.Equal(t, ActionLogin, result.Action)
	assert.Equal(t, LevelDegraded, result.Level, "first-time sign-in is amber, not red")
}

// Control: mcp-go's generic "authentication strategies failed" transport-fault
// wrapper matches isOAuthRelatedError but is neither a login-required nor a
// reauth marker. Without OAuthRequired, it must stay a plain Restart — the
// looser generic-OAuth-error branch is not trustworthy enough to promote on
// its own (this mirrors why quarantinedAwaitingSignIn only special-cases the
// two specific markers, not isOAuthRelatedError as a whole).
func TestCalculateHealth_EnabledAutodetectedOAuthGenericTransportFaultStaysRestart(t *testing.T) {
	result := CalculateHealth(HealthCalculatorInput{
		Name:          "github",
		Enabled:       true,
		Quarantined:   false,
		State:         "error",
		OAuthRequired: false,
		LastError:     "failed to connect: all authentication strategies failed: EOF",
	}, nil)
	assert.Equal(t, ActionRestart, result.Action)
}
