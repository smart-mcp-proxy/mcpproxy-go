package health

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A quarantined server is deliberately disconnected — the supervisor issues
// ActionDisconnect for `Quarantined && !IsInspectionExempted` and refuses to
// dial it — so `connected: false` on its own is the designed state and says
// nothing about health. A *transport fault* is different: the scanner dials
// quarantined servers under an inspection exemption, so a missing binary or a
// dead host is genuinely observable, and the early return in CalculateHealth
// used to discard it and report `healthy` with a "Approve" call to action.
//
// Observed live before this fix: a quarantined stdio server pointed at a
// nonexistent command reported status="error" and a full spawn failure in
// last_error, while the API said level=healthy / action=approve. Approving it
// hands the user a second failure.
//
// The admin contract must survive: the server is still quarantined, and
// approving it is still the operator's next step. Only the health LEVEL and
// the human-readable summary change.
func TestCalculateHealth_QuarantinedWithTransportFault(t *testing.T) {
	input := HealthCalculatorInput{
		Name:        "broken-stdio",
		Enabled:     true,
		Quarantined: true,
		State:       "error",
		LastError:   `failed to connect: stdio transport: server process exited before completing the MCP initialize handshake; recent stderr: command not found: definitely-not-a-real-binary-xyz`,
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelUnhealthy, result.Level, "a quarantined server that cannot start is not healthy")
	assert.Equal(t, StateQuarantined, result.AdminState, "it is still quarantined")
	assert.Equal(t, ActionApprove, result.Action, "approval is still the operator's next step")
	assert.Contains(t, result.Detail, "definitely-not-a-real-binary-xyz", "the fault must be legible, not discarded")
	assert.NotEqual(t, "Quarantined for review", result.Summary, "the summary must name the fault, not hide it")
	assert.Contains(t, result.Summary, "uarantined", "and must still say it is quarantined")
}

// Control: an ordinary quarantined server — the overwhelmingly common case —
// must keep reporting healthy, or every freshly added server turns red and the
// tray badge storms. This is the behaviour the fix must NOT regress.
func TestCalculateHealth_QuarantinedWithoutFaultStaysHealthy(t *testing.T) {
	for _, state := range []string{"", "disconnected", "idle"} {
		input := HealthCalculatorInput{
			Name:        "ordinary",
			Enabled:     true,
			Quarantined: true,
			State:       state,
		}
		result := CalculateHealth(input, nil)
		assert.Equal(t, LevelHealthy, result.Level, "state %q must stay healthy", state)
		assert.Equal(t, "Quarantined for review", result.Summary, "state %q", state)
		assert.Equal(t, ActionApprove, result.Action, "state %q", state)
	}
}

// A disabled server is switched off on purpose; nobody is waiting on it, and it
// is not dialled at all. It must keep its healthy/intentional reading even if a
// stale error is still attached from before it was disabled.
func TestCalculateHealth_DisabledWithStaleErrorStaysHealthy(t *testing.T) {
	result := CalculateHealth(HealthCalculatorInput{
		Name:      "switched-off",
		Enabled:   false,
		State:     "error",
		LastError: "failed to connect: whatever",
	}, nil)

	assert.Equal(t, LevelHealthy, result.Level)
	assert.Equal(t, StateDisabled, result.AdminState)
	assert.Equal(t, ActionEnable, result.Action)
}
