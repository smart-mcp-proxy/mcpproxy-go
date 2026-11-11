package health

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Spec 123 FR-031: a connected server whose later need for sign-in meets an
// undecryptable stored row is never offered `login`; it reports the same
// unhealthy/configure outcome as a disconnected one. A connected server that
// needs no sign-in stays healthy.

const spec123ConnectedDetail = "stored OAuth token cannot be decrypted: no encryption key configured"

func TestCalculateHealth_OAuthTokenUndecryptable_Connected(t *testing.T) {
	base := HealthCalculatorInput{
		Name: "gh", Enabled: true, Connected: true, State: "ready", HasEndpointURL: true,
		ToolCount:               3,
		OAuthTokenUndecryptable: spec123ConnectedDetail,
	}
	expiry := time.Now().Add(-time.Hour)
	needsLogin := map[string]func(in *HealthCalculatorInput){
		"call-time oauth required": func(in *HealthCalculatorInput) {
			in.CallTimeOAuthRequired = true
		},
		"oauth required, status none": func(in *HealthCalculatorInput) {
			in.OAuthRequired, in.OAuthStatus = true, "none"
		},
		"oauth required, status error": func(in *HealthCalculatorInput) {
			in.OAuthRequired, in.OAuthStatus = true, "error"
		},
		"oauth required, status expired": func(in *HealthCalculatorInput) {
			in.OAuthRequired, in.OAuthStatus, in.TokenExpiresAt = true, "expired", &expiry
		},
		"oauth required, user logged out": func(in *HealthCalculatorInput) {
			in.OAuthRequired, in.UserLoggedOut = true, true
		},
		"oauth required, refresh failed": func(in *HealthCalculatorInput) {
			in.OAuthRequired, in.OAuthStatus, in.RefreshState = true, "authenticated", RefreshStateFailed
		},
	}
	for name, mutate := range needsLogin {
		t.Run(name, func(t *testing.T) {
			in := base
			mutate(&in)
			hs := CalculateHealth(in, nil)
			assert.Equal(t, LevelUnhealthy, hs.Level)
			assert.Equal(t, ActionConfigure, hs.Action)
			assert.NotContains(t, hs.Actions, ActionLogin)
			assert.NotEqual(t, StatusSignInRequired, hs.Status)
			assert.Equal(t, StatusNeedsConfig, hs.Status)
			assert.Equal(t, spec123ConnectedDetail, hs.Detail)
			assert.False(t, hs.Usable)
			assert.Equal(t, StateEnabled, hs.AdminState)
			assert.True(t, IsOAuthTokenUndecryptable(hs))
		})
	}

	t.Run("connected server that needs no login stays healthy", func(t *testing.T) {
		hs := CalculateHealth(base, nil)
		assert.Equal(t, LevelHealthy, hs.Level)
		assert.Equal(t, ActionNone, hs.Action)
		assert.True(t, hs.Usable)
		assert.False(t, IsOAuthTokenUndecryptable(hs))
	})

	t.Run("a stale undecryptable LastError does not affect a connected server", func(t *testing.T) {
		in := base
		in.OAuthTokenUndecryptable = ""
		in.LastError = spec123UndecryptableLastError
		hs := CalculateHealth(in, nil)
		assert.Equal(t, LevelHealthy, hs.Level)
	})

	t.Run("connected server with a login need but no undecryptable row still offers login", func(t *testing.T) {
		in := base
		in.OAuthTokenUndecryptable = ""
		in.CallTimeOAuthRequired = true
		hs := CalculateHealth(in, nil)
		assert.Equal(t, ActionLogin, hs.Action)
	})
}
