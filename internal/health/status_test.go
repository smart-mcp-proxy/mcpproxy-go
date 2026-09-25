package health

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCalculateHealth_StatusVocabulary is T041: one row per CalculateHealth
// branch (contracts/health-vocabulary.md's derivation table), asserting the
// new `status`/`usable`/`actions` triple and the `action == actions[0]`
// invariant (or "" when actions is empty). `level` values are asserted where
// the contract requires them unchanged for compatibility.
func TestCalculateHealth_StatusVocabulary(t *testing.T) {
	soon := time.Now().Add(30 * time.Minute)

	cases := []struct {
		name       string
		input      HealthCalculatorInput
		wantStatus string
		wantUsable bool
		wantAction string
		wantLevel  string // "" = not asserted
	}{
		{
			name:       "disabled",
			input:      HealthCalculatorInput{Enabled: false},
			wantStatus: StatusDisabled,
			wantUsable: false,
			wantAction: ActionEnable,
			wantLevel:  LevelHealthy,
		},
		{
			name: "quarantined and OAuth login required (pending auth)",
			input: HealthCalculatorInput{
				Enabled:       true,
				Quarantined:   true,
				OAuthRequired: true,
				State:         "pending auth",
			},
			wantStatus: StatusSignInRequired,
			wantUsable: false,
			wantAction: ActionLogin,
		},
		{
			name: "quarantined and OAuth login required (call-time)",
			input: HealthCalculatorInput{
				Enabled:               true,
				Quarantined:           true,
				CallTimeOAuthRequired: true,
			},
			wantStatus: StatusSignInRequired,
			wantUsable: false,
			wantAction: ActionLogin,
		},
		{
			name: "quarantined transport fault",
			input: HealthCalculatorInput{
				Enabled:     true,
				Quarantined: true,
				State:       "error",
				LastError:   "command not found: definitely-not-a-real-binary",
			},
			wantStatus: StatusError,
			wantUsable: false,
			wantAction: ActionApprove,
			wantLevel:  LevelUnhealthy,
		},
		{
			name:       "quarantined otherwise",
			input:      HealthCalculatorInput{Enabled: true, Quarantined: true},
			wantStatus: StatusNeedsReview,
			wantUsable: false,
			wantAction: ActionApprove,
			wantLevel:  LevelHealthy,
		},
		{
			name:       "missing secret",
			input:      HealthCalculatorInput{Enabled: true, MissingSecret: "GITHUB_TOKEN"},
			wantStatus: StatusNeedsSecret,
			wantUsable: false,
			wantAction: ActionSetSecret,
		},
		{
			name:       "OAuth config error",
			input:      HealthCalculatorInput{Enabled: true, OAuthConfigErr: "requires 'resource' parameter"},
			wantStatus: StatusNeedsConfig,
			wantUsable: false,
			wantAction: ActionConfigure,
		},
		{
			name: "RetryStopped (GH #1145)",
			input: HealthCalculatorInput{
				Enabled:      true,
				RetryStopped: true,
				RetryCount:   3,
				LastError:    "handshake timeout",
			},
			wantStatus: StatusError,
			wantUsable: false,
			wantAction: ActionRestart,
		},
		{
			name: "endpoint address error",
			input: HealthCalculatorInput{
				Enabled:        true,
				State:          "error",
				HasEndpointURL: true,
				LastError:      "no such host",
			},
			wantStatus: StatusNeedsConfig,
			wantUsable: false,
			wantAction: ActionEditURL,
		},
		{
			name: "OAuth login required / re-auth (error state)",
			input: HealthCalculatorInput{
				Enabled:       true,
				State:         "error",
				OAuthRequired: true,
				LastError:     "oauth authentication required: login available",
			},
			wantStatus: StatusSignInRequired,
			wantUsable: false,
			wantAction: ActionLogin,
		},
		{
			name:       "connecting",
			input:      HealthCalculatorInput{Enabled: true, State: "connecting"},
			wantStatus: StatusConnecting,
			wantUsable: false,
			wantAction: ActionNone,
			wantLevel:  LevelHealthy,
		},
		{
			name:       "idle",
			input:      HealthCalculatorInput{Enabled: true, State: "idle"},
			wantStatus: StatusConnecting,
			wantUsable: false,
			wantAction: ActionNone,
		},
		{
			name:       "pending auth (#1013)",
			input:      HealthCalculatorInput{Enabled: true, State: "pending auth"},
			wantStatus: StatusSignInRequired,
			wantUsable: false,
			wantAction: ActionLogin,
		},
		{
			name:       "call-time OAuth required (MCP-2084)",
			input:      HealthCalculatorInput{Enabled: true, CallTimeOAuthRequired: true},
			wantStatus: StatusSignInRequired,
			wantUsable: false,
			wantAction: ActionLogin,
			wantLevel:  LevelDegraded,
		},
		{
			name:       "connection error (generic)",
			input:      HealthCalculatorInput{Enabled: true, State: "error", LastError: "connection refused"},
			wantStatus: StatusError,
			wantUsable: false,
			wantAction: ActionRestart,
		},
		{
			name: "OAuth refresh retrying",
			input: HealthCalculatorInput{
				Enabled:      true,
				RefreshState: RefreshStateRetrying,
			},
			wantStatus: StatusReady,
			wantUsable: true,
			wantAction: ActionViewLogs,
			wantLevel:  LevelDegraded,
		},
		{
			name: "OAuth refresh failed",
			input: HealthCalculatorInput{
				Enabled:      true,
				RefreshState: RefreshStateFailed,
			},
			wantStatus: StatusSignInRequired,
			wantUsable: false,
			wantAction: ActionLogin,
		},
		{
			name: "OAuth token expiring soon, has refresh token",
			input: HealthCalculatorInput{
				Enabled:         true,
				OAuthRequired:   true,
				HasRefreshToken: true,
				TokenExpiresAt:  &soon,
			},
			wantStatus: StatusReady,
			wantUsable: true,
			wantAction: ActionNone,
			wantLevel:  LevelHealthy,
		},
		{
			name: "OAuth token expiring soon, no refresh token — proactive nudge stays ready",
			input: HealthCalculatorInput{
				Enabled:        true,
				OAuthRequired:  true,
				TokenExpiresAt: &soon,
			},
			wantStatus: StatusReady,
			wantUsable: true,
			wantAction: ActionLogin,
			wantLevel:  LevelDegraded,
		},
		{
			name:       "connected, healthy",
			input:      HealthCalculatorInput{Enabled: true, ToolCount: 5},
			wantStatus: StatusReady,
			wantUsable: true,
			wantAction: ActionNone,
			wantLevel:  LevelHealthy,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := CalculateHealth(tc.input, nil)
			assert.Equal(t, tc.wantStatus, result.Status, "status")
			assert.Equal(t, tc.wantUsable, result.Usable, "usable")
			assert.Equal(t, tc.wantAction, result.Action, "action")
			require.NotNil(t, result.Actions, "actions must never be nil")
			if tc.wantAction == "" {
				assert.Empty(t, result.Actions, "actions must be empty when action is none")
			} else {
				assert.Equal(t, tc.wantAction, result.Actions[0], "action must equal actions[0]")
			}
			if tc.wantLevel != "" {
				assert.Equal(t, tc.wantLevel, result.Level, "level must stay as documented")
			}
		})
	}
}

// TestCalculateHealth_QuarantinedOAuthActionOrder is FR-010/FR-012: a
// quarantined server that also needs OAuth sign-in reports actions
// ["login","approve"] in that priority order — login was "approve" before
// this PR (the one declared value change).
func TestCalculateHealth_QuarantinedOAuthActionOrder(t *testing.T) {
	result := CalculateHealth(HealthCalculatorInput{
		Enabled:               true,
		Quarantined:           true,
		CallTimeOAuthRequired: true,
	}, nil)

	assert.Equal(t, StatusSignInRequired, result.Status)
	assert.False(t, result.Usable)
	assert.Equal(t, []string{ActionLogin, ActionApprove}, result.Actions)
	assert.Equal(t, ActionLogin, result.Action, "FR-010: was approve before this PR")
	assert.Equal(t, StateQuarantined, result.AdminState, "admin_state stays quarantined")
}

// TestCalculateHealth_ActionInvariant is a property check over every branch
// exercised by the existing calculator_test.go / calculator_quarantine_fault_test.go
// fixtures: Action must always equal Actions[0] (or "" when Actions is empty),
// and Actions must never be nil.
func TestCalculateHealth_ActionInvariant(t *testing.T) {
	inputs := []HealthCalculatorInput{
		{Enabled: false},
		{Enabled: true, Quarantined: true},
		{Enabled: true, Quarantined: true, State: "error", LastError: "boom"},
		{Enabled: true, MissingSecret: "X"},
		{Enabled: true, OAuthConfigErr: "bad config"},
		{Enabled: true, RetryStopped: true},
		{Enabled: true, State: "error", LastError: "connection refused"},
		{Enabled: true, State: "disconnected"},
		{Enabled: true, State: "pending auth"},
		{Enabled: true, State: "connecting"},
		{Enabled: true, CallTimeOAuthRequired: true},
		{Enabled: true, OAuthRequired: true, UserLoggedOut: true},
		{Enabled: true, OAuthRequired: true, OAuthStatus: "expired"},
		{Enabled: true, OAuthRequired: true, OAuthStatus: "error"},
		{Enabled: true, OAuthRequired: true, OAuthStatus: "none"},
		{Enabled: true, RefreshState: RefreshStateRetrying},
		{Enabled: true, RefreshState: RefreshStateFailed},
		{Enabled: true, ToolCount: 3},
	}

	for i, in := range inputs {
		result := CalculateHealth(in, nil)
		require.NotNilf(t, result.Actions, "case %d: Actions must not be nil", i)
		if len(result.Actions) == 0 {
			assert.Emptyf(t, result.Action, "case %d: Action must be empty when Actions is empty", i)
		} else {
			assert.Equalf(t, result.Actions[0], result.Action, "case %d: Action must equal Actions[0]", i)
		}
	}
}

// TestStatusLabel_CoversEveryStatus and TestActionLabel_CoversEveryAction
// guard the label tables in constants.go against a status/action value with
// no cross-surface label (contracts/health-vocabulary.md's label table).
func TestStatusLabel_CoversEveryStatus(t *testing.T) {
	for _, s := range StatusOrder {
		label := StatusLabel(s)
		assert.NotEqual(t, s, label, "status %q has no label", s)
		assert.NotEmpty(t, label)
	}
}

func TestActionLabel_CoversEveryAction(t *testing.T) {
	for _, a := range ActionPriority {
		assert.NotEmpty(t, ActionLabel(a), "action %q has no label", a)
	}
	assert.Empty(t, ActionLabel(ActionNone))
}
