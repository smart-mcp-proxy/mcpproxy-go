package preflight

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// The approval-status values are mirrored, not imported, to keep this package a
// leaf. The mirror must be exact.
func TestApprovalStatusMirrorsStorage(t *testing.T) {
	assert.Equal(t, storage.ToolApprovalStatusApproved, ApprovalStatusApproved)
	assert.Equal(t, storage.ToolApprovalStatusPending, ApprovalStatusPending)
	assert.Equal(t, storage.ToolApprovalStatusChanged, ApprovalStatusChanged)
}

func enabledServer() ServerPolicy { return ServerPolicy{Found: true, Enabled: true} }

func TestClassifyTool_Precedence(t *testing.T) {
	tests := []struct {
		name string
		in   ClassifyInputs
		want ToolClass
	}{
		{
			name: "no server record",
			in:   ClassifyInputs{Server: ServerPolicy{}},
			want: ToolClassServerNotConfigured,
		},
		{
			name: "quarantined beats disabled",
			in:   ClassifyInputs{Server: ServerPolicy{Found: true, Quarantined: true}},
			want: ToolClassServerQuarantined,
		},
		{
			name: "disabled",
			in:   ClassifyInputs{Server: ServerPolicy{Found: true}},
			want: ToolClassServerDisabled,
		},
		{
			name: "config denial beats a user block",
			in: ClassifyInputs{
				Server: enabledServer(), ConfigDenied: true, QuarantineEnabled: true,
				Approval: &ApprovalState{Disabled: true},
			},
			want: ToolClassDeniedByConfig,
		},
		{
			name: "user block beats a changed status",
			in: ClassifyInputs{
				Server: enabledServer(), QuarantineEnabled: true,
				Approval: &ApprovalState{Disabled: true, Status: ApprovalStatusChanged},
			},
			want: ToolClassBlockedByUser,
		},
		{
			name: "changed",
			in: ClassifyInputs{
				Server: enabledServer(), QuarantineEnabled: true,
				Approval: &ApprovalState{Status: ApprovalStatusChanged},
			},
			want: ToolClassChanged,
		},
		{
			name: "pending",
			in: ClassifyInputs{
				Server: enabledServer(), QuarantineEnabled: true,
				Approval: &ApprovalState{Status: ApprovalStatusPending},
			},
			want: ToolClassPendingApproval,
		},
		{
			name: "approved",
			in: ClassifyInputs{
				Server: enabledServer(), QuarantineEnabled: true,
				Approval: &ApprovalState{Status: ApprovalStatusApproved},
			},
			want: ToolClassReady,
		},
		{
			name: "no approval record is the implicit-approved default",
			in:   ClassifyInputs{Server: enabledServer(), QuarantineEnabled: true},
			want: ToolClassReady,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ClassifyTool(tt.in))
		})
	}
}

// --- the three documented divergences (research D2) -------------------------
//
// Dispatch behavior is ground truth; these are the cases where the legacy
// classifyServerToolStatus disagreed with it.

func TestClassifyTool_QuarantineFlagsAreHonored(t *testing.T) {
	pending := &ApprovalState{Status: ApprovalStatusPending}

	// Global quarantine OFF: a pending record does not gate the tool.
	assert.Equal(t, ToolClassReady, ClassifyTool(ClassifyInputs{
		Server: enabledServer(), QuarantineEnabled: false, Approval: pending,
	}), "classifyServerToolStatus used to report pending_approval regardless of the global switch")

	// Per-server opt-out (trust_mode auto / auto_approve_tool_changes).
	assert.Equal(t, ToolClassReady, ClassifyTool(ClassifyInputs{
		Server:            ServerPolicy{Found: true, Enabled: true, AutoApproveToolChanges: true},
		QuarantineEnabled: true,
		Approval:          pending,
	}))

	// Both gates on: the record gates.
	assert.Equal(t, ToolClassPendingApproval, ClassifyTool(ClassifyInputs{
		Server: enabledServer(), QuarantineEnabled: true, Approval: pending,
	}))
}

func TestClassifyTool_AutoApproveMakesChangedToolsReady(t *testing.T) {
	changed := &ApprovalState{Status: ApprovalStatusChanged}
	assert.Equal(t, ToolClassReady, ClassifyTool(ClassifyInputs{
		Server:            ServerPolicy{Found: true, Enabled: true, AutoApproveToolChanges: true},
		QuarantineEnabled: true,
		Approval:          changed,
	}))
}

func TestClassifyTool_ChangedIsNotCollapsedIntoPending(t *testing.T) {
	changed := ClassifyTool(ClassifyInputs{
		Server: enabledServer(), QuarantineEnabled: true,
		Approval: &ApprovalState{Status: ApprovalStatusChanged},
	})
	pending := ClassifyTool(ClassifyInputs{
		Server: enabledServer(), QuarantineEnabled: true,
		Approval: &ApprovalState{Status: ApprovalStatusPending},
	})
	assert.NotEqual(t, pending, changed)
	assert.Equal(t, ReasonToolChanged, changed.Reason())
	assert.Equal(t, ReasonToolPendingApproval, pending.Reason())
}

// A user block is a user decision, not a quarantine gate: it applies even when
// the server opted out of tool-level quarantine.
func TestClassifyTool_UserBlockIgnoresQuarantineOptOut(t *testing.T) {
	assert.Equal(t, ToolClassBlockedByUser, ClassifyTool(ClassifyInputs{
		Server:            ServerPolicy{Found: true, Enabled: true, AutoApproveToolChanges: true},
		QuarantineEnabled: false,
		Approval:          &ApprovalState{Disabled: true},
	}))
}

// An unrecognized status (a record written by a newer proxy) must not silently
// lock the tool — the quarantine states are the closed set that gates.
func TestClassifyTool_UnknownStatusIsNotAGate(t *testing.T) {
	assert.Equal(t, ToolClassReady, ClassifyTool(ClassifyInputs{
		Server: enabledServer(), QuarantineEnabled: true,
		Approval: &ApprovalState{Status: "some_future_status"},
	}))
}

func TestToolClass_ReasonAndCallable(t *testing.T) {
	cases := map[ToolClass]Reason{
		ToolClassServerNotConfigured: ReasonServerNotConfigured,
		ToolClassServerQuarantined:   ReasonServerQuarantined,
		ToolClassServerDisabled:      ReasonServerDisabled,
		ToolClassDeniedByConfig:      ReasonToolDeniedByConfig,
		ToolClassBlockedByUser:       ReasonToolBlockedByUser,
		ToolClassChanged:             ReasonToolChanged,
		ToolClassPendingApproval:     ReasonToolPendingApproval,
		ToolClassReady:               "",
	}
	for class, reason := range cases {
		assert.Equal(t, reason, class.Reason(), "class %s", class)
		assert.Equal(t, class == ToolClassReady, class.Callable(), "class %s", class)
		if reason != "" {
			assert.True(t, ValidReason(reason), "every class reason is a member of the enum")
		}
	}
}
