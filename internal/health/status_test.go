package health

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCalculateHealth_StatusVocabulary is T041: one row per CalculateHealth
// branch (Spec 109 FR-010-012's derivation table), asserting the
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
		// wantActions asserts the FULL Actions slice (order included), not just
		// Actions[0]/wantAction. Populated for every branch with more than one
		// element, so silently dropping a trailing entry (e.g. ActionViewLogs)
		// fails a test instead of passing on the unchanged Actions[0].
		wantActions []string // nil = only Actions[0] is asserted (via wantAction)
	}{
		{
			name:        "disabled",
			input:       HealthCalculatorInput{Enabled: false},
			wantStatus:  StatusDisabled,
			wantUsable:  false,
			wantAction:  ActionEnable,
			wantLevel:   LevelHealthy,
			wantActions: []string{ActionEnable},
		},
		{
			name: "quarantined and OAuth login required (pending auth)",
			input: HealthCalculatorInput{
				Enabled:       true,
				Quarantined:   true,
				OAuthRequired: true,
				State:         "pending auth",
			},
			wantStatus:  StatusSignInRequired,
			wantUsable:  false,
			wantAction:  ActionLogin,
			wantActions: []string{ActionLogin, ActionApprove},
		},
		{
			name: "quarantined and OAuth login required (call-time)",
			input: HealthCalculatorInput{
				Enabled:               true,
				Quarantined:           true,
				CallTimeOAuthRequired: true,
			},
			wantStatus:  StatusSignInRequired,
			wantUsable:  false,
			wantAction:  ActionLogin,
			wantActions: []string{ActionLogin, ActionApprove},
		},
		{
			name: "quarantined transport fault",
			input: HealthCalculatorInput{
				Enabled:     true,
				Quarantined: true,
				State:       "error",
				LastError:   "command not found: definitely-not-a-real-binary",
			},
			wantStatus:  StatusError,
			wantUsable:  false,
			wantAction:  ActionApprove,
			wantLevel:   LevelUnhealthy,
			wantActions: []string{ActionApprove, ActionViewLogs},
		},
		{
			name: "quarantined and error state with an OAuth-related error (FR-010)",
			input: HealthCalculatorInput{
				Enabled:       true,
				Quarantined:   true,
				State:         "error",
				OAuthRequired: true,
				LastError:     "oauth authentication required: login available",
			},
			wantStatus:  StatusSignInRequired,
			wantUsable:  false,
			wantAction:  ActionLogin,
			wantLevel:   LevelDegraded,
			wantActions: []string{ActionLogin, ActionApprove},
		},
		{
			name:        "quarantined otherwise",
			input:       HealthCalculatorInput{Enabled: true, Quarantined: true},
			wantStatus:  StatusNeedsReview,
			wantUsable:  false,
			wantAction:  ActionApprove,
			wantLevel:   LevelHealthy,
			wantActions: []string{ActionApprove},
		},
		{
			name:        "missing secret",
			input:       HealthCalculatorInput{Enabled: true, MissingSecret: "GITHUB_TOKEN"},
			wantStatus:  StatusNeedsSecret,
			wantUsable:  false,
			wantAction:  ActionSetSecret,
			wantActions: []string{ActionSetSecret},
		},
		{
			name:        "OAuth config error",
			input:       HealthCalculatorInput{Enabled: true, OAuthConfigErr: "requires 'resource' parameter"},
			wantStatus:  StatusNeedsConfig,
			wantUsable:  false,
			wantAction:  ActionConfigure,
			wantActions: []string{ActionConfigure},
		},
		{
			name: "RetryStopped (GH #1145)",
			input: HealthCalculatorInput{
				Enabled:      true,
				RetryStopped: true,
				RetryCount:   3,
				LastError:    "handshake timeout",
			},
			wantStatus:  StatusError,
			wantUsable:  false,
			wantAction:  ActionRestart,
			wantActions: []string{ActionRestart, ActionViewLogs},
		},
		{
			name: "endpoint address error",
			input: HealthCalculatorInput{
				Enabled:        true,
				State:          "error",
				HasEndpointURL: true,
				LastError:      "no such host",
			},
			wantStatus:  StatusNeedsConfig,
			wantUsable:  false,
			wantAction:  ActionEditURL,
			wantActions: []string{ActionEditURL},
		},
		{
			name: "OAuth login required / re-auth (error state)",
			input: HealthCalculatorInput{
				Enabled:       true,
				State:         "error",
				OAuthRequired: true,
				LastError:     "oauth authentication required: login available",
			},
			wantStatus:  StatusSignInRequired,
			wantUsable:  false,
			wantAction:  ActionLogin,
			wantActions: []string{ActionLogin},
		},
		{
			name:        "connecting",
			input:       HealthCalculatorInput{Enabled: true, State: "connecting"},
			wantStatus:  StatusConnecting,
			wantUsable:  false,
			wantAction:  ActionNone,
			wantLevel:   LevelHealthy,
			wantActions: []string{},
		},
		{
			name:        "idle",
			input:       HealthCalculatorInput{Enabled: true, State: "idle"},
			wantStatus:  StatusConnecting,
			wantUsable:  false,
			wantAction:  ActionNone,
			wantActions: []string{},
		},
		{
			name:        "pending auth (#1013)",
			input:       HealthCalculatorInput{Enabled: true, State: "pending auth"},
			wantStatus:  StatusSignInRequired,
			wantUsable:  false,
			wantAction:  ActionLogin,
			wantActions: []string{ActionLogin},
		},
		{
			name:        "call-time OAuth required (MCP-2084)",
			input:       HealthCalculatorInput{Enabled: true, CallTimeOAuthRequired: true},
			wantStatus:  StatusSignInRequired,
			wantUsable:  false,
			wantAction:  ActionLogin,
			wantLevel:   LevelDegraded,
			wantActions: []string{ActionLogin},
		},
		{
			name:        "connection error (generic)",
			input:       HealthCalculatorInput{Enabled: true, State: "error", LastError: "connection refused"},
			wantStatus:  StatusError,
			wantUsable:  false,
			wantAction:  ActionRestart,
			wantActions: []string{ActionRestart, ActionViewLogs},
		},
		{
			name:        "disconnected (generic)",
			input:       HealthCalculatorInput{Enabled: true, State: "disconnected"},
			wantStatus:  StatusError,
			wantUsable:  false,
			wantAction:  ActionRestart,
			wantLevel:   LevelUnhealthy,
			wantActions: []string{ActionRestart, ActionViewLogs},
		},
		{
			// Mirrors the "endpoint address error" row above (same
			// isEndpointAddressError gate), but reached through the
			// "disconnected" case rather than "error" — the two share the
			// gate but were only exercised together via ActionInvariant,
			// which never asserts Status/Usable/the full Actions slice.
			name: "disconnected with endpoint address error",
			input: HealthCalculatorInput{
				Enabled:        true,
				State:          "disconnected",
				HasEndpointURL: true,
				LastError:      "no such host",
			},
			wantStatus:  StatusNeedsConfig,
			wantUsable:  false,
			wantAction:  ActionEditURL,
			wantLevel:   LevelUnhealthy,
			wantActions: []string{ActionEditURL},
		},
		{
			// Mirrors "OAuth login required / re-auth (error state)" above,
			// but through the "disconnected" case's own isOAuthRelatedError
			// gate.
			name: "disconnected with an OAuth-related error",
			input: HealthCalculatorInput{
				Enabled:       true,
				State:         "disconnected",
				OAuthRequired: true,
				LastError:     "oauth authentication required: login available",
			},
			wantStatus:  StatusSignInRequired,
			wantUsable:  false,
			wantAction:  ActionLogin,
			wantLevel:   LevelDegraded,
			wantActions: []string{ActionLogin},
		},
		{
			name: "OAuth user explicitly logged out",
			input: HealthCalculatorInput{
				Enabled:       true,
				OAuthRequired: true,
				UserLoggedOut: true,
			},
			wantStatus:  StatusSignInRequired,
			wantUsable:  false,
			wantAction:  ActionLogin,
			wantLevel:   LevelUnhealthy,
			wantActions: []string{ActionLogin},
		},
		{
			name: "OAuth token expired (OAuthStatus)",
			input: HealthCalculatorInput{
				Enabled:       true,
				OAuthRequired: true,
				OAuthStatus:   "expired",
			},
			wantStatus:  StatusSignInRequired,
			wantUsable:  false,
			wantAction:  ActionLogin,
			wantLevel:   LevelUnhealthy,
			wantActions: []string{ActionLogin},
		},
		{
			name: "OAuth error, not expired (OAuthStatus)",
			input: HealthCalculatorInput{
				Enabled:       true,
				OAuthRequired: true,
				OAuthStatus:   "error",
			},
			wantStatus:  StatusSignInRequired,
			wantUsable:  false,
			wantAction:  ActionLogin,
			wantLevel:   LevelUnhealthy,
			wantActions: []string{ActionLogin},
		},
		{
			name: "OAuth not yet authenticated (OAuthStatus none)",
			input: HealthCalculatorInput{
				Enabled:       true,
				OAuthRequired: true,
				OAuthStatus:   "none",
			},
			wantStatus:  StatusSignInRequired,
			wantUsable:  false,
			wantAction:  ActionLogin,
			wantLevel:   LevelUnhealthy,
			wantActions: []string{ActionLogin},
		},
		{
			name: "OAuth refresh retrying",
			input: HealthCalculatorInput{
				Enabled:      true,
				RefreshState: RefreshStateRetrying,
			},
			wantStatus:  StatusReady,
			wantUsable:  true,
			wantAction:  ActionViewLogs,
			wantLevel:   LevelDegraded,
			wantActions: []string{ActionViewLogs},
		},
		{
			name: "OAuth refresh failed",
			input: HealthCalculatorInput{
				Enabled:      true,
				RefreshState: RefreshStateFailed,
			},
			wantStatus:  StatusSignInRequired,
			wantUsable:  false,
			wantAction:  ActionLogin,
			wantActions: []string{ActionLogin},
		},
		{
			name: "OAuth token expiring soon, has refresh token",
			input: HealthCalculatorInput{
				Enabled:         true,
				OAuthRequired:   true,
				HasRefreshToken: true,
				TokenExpiresAt:  &soon,
			},
			wantStatus:  StatusReady,
			wantUsable:  true,
			wantAction:  ActionNone,
			wantLevel:   LevelHealthy,
			wantActions: []string{},
		},
		{
			name: "OAuth token expiring soon, no refresh token — proactive nudge stays ready",
			input: HealthCalculatorInput{
				Enabled:        true,
				OAuthRequired:  true,
				TokenExpiresAt: &soon,
			},
			wantStatus:  StatusReady,
			wantUsable:  true,
			wantAction:  ActionLogin,
			wantLevel:   LevelDegraded,
			wantActions: []string{ActionLogin},
		},
		{
			name:        "connected, healthy",
			input:       HealthCalculatorInput{Enabled: true, ToolCount: 5},
			wantStatus:  StatusReady,
			wantUsable:  true,
			wantAction:  ActionNone,
			wantLevel:   LevelHealthy,
			wantActions: []string{},
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
			if tc.wantActions != nil {
				// Full-slice check: a spot-check of Actions[0] alone would not
				// catch a trailing entry (e.g. ActionViewLogs) silently dropped.
				assert.Equal(t, tc.wantActions, result.Actions, "actions (full slice)")
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
// no cross-surface label (Spec 109 FR-014's label table).
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

// TestConnectionErrorStatus pins connectionErrorStatus's three branches,
// including its `default` case — the only branch without its own switch
// label. Both real call sites in CalculateHealth only ever pass
// ActionRestart, ActionEditURL, or ActionLogin, so `default` today means
// exactly "ActionRestart, or anything else". This test exists so a change to
// what falls into `default` (e.g. a new override the "error"/"disconnected"
// branches start emitting) is a visible, deliberate diff here rather than a
// silent remap discovered later.
func TestConnectionErrorStatus(t *testing.T) {
	cases := []struct {
		action      string
		wantStatus  string
		wantUsable  bool
		wantActions []string
	}{
		{ActionEditURL, StatusNeedsConfig, false, []string{ActionEditURL}},
		{ActionLogin, StatusSignInRequired, false, []string{ActionLogin}},
		{ActionRestart, StatusError, false, []string{ActionRestart, ActionViewLogs}},
		// The default branch's fallback, for any action neither call site
		// today produces.
		{"unexpected-action", StatusError, false, []string{ActionRestart, ActionViewLogs}},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			status, usable, actions := connectionErrorStatus(tc.action)
			assert.Equal(t, tc.wantStatus, status)
			assert.Equal(t, tc.wantUsable, usable)
			assert.Equal(t, tc.wantActions, actions)
		})
	}
}
