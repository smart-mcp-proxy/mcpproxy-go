package health

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Spec 123 T042 (FR-031, US2.1): a server whose stored OAuth row cannot be
// decrypted is unhealthy with the configure action and the reason in the
// detail. It is never reported as "login required": a login could not
// overwrite the row, and offering one is the silent re-login the spec rules out.

// spec123UndecryptableLastError is the connection error the upstream layer
// records for such a row (internal/oauth TokenUndecryptableError).
const spec123UndecryptableLastError = "all authentication strategies failed, last error: " +
	"stored OAuth token cannot be decrypted: no encryption key configured; " +
	"restore the encryption key and restart, or run `mcpproxy auth logout --server=gh` and then log in again"

func TestCalculateHealth_OAuthTokenUndecryptable(t *testing.T) {
	base := HealthCalculatorInput{
		Name: "gh", Enabled: true, HasEndpointURL: true,
		LastError: spec123UndecryptableLastError,
	}
	cases := map[string]func(in *HealthCalculatorInput){
		"error state, oauth configured": func(in *HealthCalculatorInput) {
			in.State, in.OAuthRequired, in.OAuthStatus = "error", true, "error"
		},
		"error state, autodiscovered": func(in *HealthCalculatorInput) {
			in.State = "error"
		},
		"disconnected": func(in *HealthCalculatorInput) {
			in.State, in.OAuthRequired = "disconnected", true
		},
		"pending auth park": func(in *HealthCalculatorInput) {
			in.State, in.OAuthRequired = "pending_auth", true
		},
		"retry stopped": func(in *HealthCalculatorInput) {
			in.State, in.RetryStopped, in.RetryStoppedCode = "error", true, "MCPX_OAUTH_TOKEN_UNDECRYPTABLE"
		},
		"connecting with an oauth none status": func(in *HealthCalculatorInput) {
			in.State, in.OAuthRequired, in.OAuthStatus = "connecting", true, "none"
		},
		"quarantined": func(in *HealthCalculatorInput) {
			in.State, in.Quarantined, in.OAuthRequired, in.OAuthStatus = "error", true, true, "none"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := base
			mutate(&in)
			hs := CalculateHealth(in, nil)
			assert.Equal(t, LevelUnhealthy, hs.Level)
			assert.Equal(t, ActionConfigure, hs.Action)
			assert.NotContains(t, hs.Actions, ActionLogin, "never offer a login over an undecryptable row")
			assert.NotEqual(t, StatusSignInRequired, hs.Status)
			assert.Contains(t, hs.Detail, "stored OAuth token cannot be decrypted: no encryption key configured")
			assert.False(t, hs.Usable)
			if in.Quarantined {
				assert.Equal(t, StateQuarantined, hs.AdminState)
				assert.Contains(t, hs.Actions, ActionApprove)
			} else {
				assert.Equal(t, StateEnabled, hs.AdminState)
			}
		})
	}
}

// A disabled server stays disabled: the admin state still wins.
func TestCalculateHealth_OAuthTokenUndecryptable_DisabledStaysDisabled(t *testing.T) {
	hs := CalculateHealth(HealthCalculatorInput{Name: "gh", Enabled: false, LastError: spec123UndecryptableLastError}, nil)
	assert.Equal(t, StateDisabled, hs.AdminState)
	assert.Equal(t, ActionEnable, hs.Action)
}
