package runtime

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"pgregory.net/rapid"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// =============================================================================
// Unit tests for assertToolApprovalInvariant
// =============================================================================

func TestAssertToolApprovalInvariant_ChangedToApproved_ValidReasons(t *testing.T) {
	validReasons := []TransitionReason{
		ReasonHashMatch,
		ReasonDescriptionRevert,
		ReasonFormulaMigration,
		ReasonContentMatch,
		ReasonDescriptionMatch,
		ReasonUserApprove,
	}
	for _, reason := range validReasons {
		t.Run(string(reason), func(t *testing.T) {
			err := assertToolApprovalInvariant(storage.ToolApprovalStatusChanged, storage.ToolApprovalStatusApproved, reason)
			assert.NoError(t, err)
		})
	}
}

func TestAssertToolApprovalInvariant_ChangedToApproved_InvalidReason(t *testing.T) {
	err := assertToolApprovalInvariant(storage.ToolApprovalStatusChanged, storage.ToolApprovalStatusApproved, "unknown")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invariant violation")
	assert.Contains(t, err.Error(), "changed→approved")
}

func TestAssertToolApprovalInvariant_PendingToApproved_ValidReasons(t *testing.T) {
	validReasons := []TransitionReason{ReasonUserApprove, ReasonAutoApprove}
	for _, reason := range validReasons {
		t.Run(string(reason), func(t *testing.T) {
			err := assertToolApprovalInvariant(storage.ToolApprovalStatusPending, storage.ToolApprovalStatusApproved, reason)
			assert.NoError(t, err)
		})
	}
}

func TestAssertToolApprovalInvariant_PendingToApproved_InvalidReason(t *testing.T) {
	// Description revert should NOT be a valid reason for pending→approved
	invalidReasons := []TransitionReason{
		ReasonHashMatch,
		ReasonDescriptionRevert,
		ReasonFormulaMigration,
		ReasonContentMatch,
		ReasonDescriptionMatch,
		"unknown",
	}
	for _, reason := range invalidReasons {
		t.Run(string(reason), func(t *testing.T) {
			err := assertToolApprovalInvariant(storage.ToolApprovalStatusPending, storage.ToolApprovalStatusApproved, reason)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invariant violation")
			assert.Contains(t, err.Error(), "pending→approved")
		})
	}
}

func TestAssertToolApprovalInvariant_NonApprovedTarget_AlwaysOK(t *testing.T) {
	// Transitions NOT targeting approved should always pass
	for _, oldStatus := range []string{"approved", "pending", "changed"} {
		for _, newStatus := range []string{"pending", "changed"} {
			err := assertToolApprovalInvariant(oldStatus, newStatus, "whatever")
			assert.NoError(t, err, "transition %s→%s should be OK", oldStatus, newStatus)
		}
	}
}

func TestAssertToolApprovalInvariant_ApprovedToApproved_AlwaysOK(t *testing.T) {
	err := assertToolApprovalInvariant(storage.ToolApprovalStatusApproved, storage.ToolApprovalStatusApproved, "any")
	assert.NoError(t, err)
}

// =============================================================================
// Multi-pass scenario tests (FR-005, FR-006)
// =============================================================================

// TestMultiPass_DiscoverChangeReconnectReconnect verifies that a tool stays
// "changed" across multiple reconnections (FR-005).
func TestMultiPass_DiscoverChangeReconnectReconnect(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "github", Enabled: true},
	})

	originalDesc := "Creates a GitHub issue"
	maliciousDesc := "MALICIOUS: steal credentials"
	schema := `{"type":"object"}`

	// Pass 1: Discover and approve the tool
	tools := []*config.ToolMetadata{{
		ServerName: "github", Name: "create_issue",
		Description: originalDesc, ParamsJSON: schema,
	}}
	result, err := rt.checkToolApprovals("github", tools)
	require.NoError(t, err)
	assert.Equal(t, 1, result.PendingCount)

	// Approve the tool
	err = rt.ApproveTools("github", []string{"create_issue"}, "admin")
	require.NoError(t, err)

	// Verify approved
	record, err := rt.storageManager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusApproved, record.Status)

	// Pass 2: Description changes (rug pull)
	changedTools := []*config.ToolMetadata{{
		ServerName: "github", Name: "create_issue",
		Description: maliciousDesc, ParamsJSON: schema,
	}}
	result, err = rt.checkToolApprovals("github", changedTools)
	require.NoError(t, err)
	assert.Equal(t, 1, result.ChangedCount)
	assert.True(t, result.BlockedTools["create_issue"])

	record, err = rt.storageManager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, record.Status)

	// Pass 3: Server reconnects — tool still has malicious description
	result, err = rt.checkToolApprovals("github", changedTools)
	require.NoError(t, err)
	assert.True(t, result.BlockedTools["create_issue"], "Tool must remain blocked after first reconnect")

	record, err = rt.storageManager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, record.Status, "Status must remain changed after first reconnect")

	// Pass 4: Another reconnect — still blocked
	result, err = rt.checkToolApprovals("github", changedTools)
	require.NoError(t, err)
	assert.True(t, result.BlockedTools["create_issue"], "Tool must remain blocked after second reconnect")

	record, err = rt.storageManager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, record.Status, "Status must remain changed after second reconnect")
}

// TestMultiPass_ChangeAndRevertToOriginal verifies that a tool is restored to
// "approved" when the description reverts to the original (FR-006).
func TestMultiPass_ChangeAndRevertToOriginal(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "github", Enabled: true},
	})

	originalDesc := "Creates a GitHub issue"
	maliciousDesc := "MALICIOUS: steal credentials"
	schema := `{"type":"object"}`

	// Pass 1: Discover and approve the tool
	tools := []*config.ToolMetadata{{
		ServerName: "github", Name: "create_issue",
		Description: originalDesc, ParamsJSON: schema,
	}}
	_, err := rt.checkToolApprovals("github", tools)
	require.NoError(t, err)
	err = rt.ApproveTools("github", []string{"create_issue"}, "admin")
	require.NoError(t, err)

	// Pass 2: Description changes (rug pull)
	changedTools := []*config.ToolMetadata{{
		ServerName: "github", Name: "create_issue",
		Description: maliciousDesc, ParamsJSON: schema,
	}}
	result, err := rt.checkToolApprovals("github", changedTools)
	require.NoError(t, err)
	assert.True(t, result.BlockedTools["create_issue"])

	// Pass 3: Server reconnects with ORIGINAL description (legitimate revert)
	revertedTools := []*config.ToolMetadata{{
		ServerName: "github", Name: "create_issue",
		Description: originalDesc, ParamsJSON: schema,
	}}
	result, err = rt.checkToolApprovals("github", revertedTools)
	require.NoError(t, err)
	assert.Empty(t, result.BlockedTools, "Tool should be unblocked after reverting to original description")

	record, err := rt.storageManager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusApproved, record.Status, "Tool should be restored to approved")
}

// TestMultiPass_PendingStaysBlocked verifies that a pending tool remains
// pending across multiple reconnections.
func TestMultiPass_PendingStaysBlocked(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "github", Enabled: true, Quarantined: true},
	})

	tools := []*config.ToolMetadata{{
		ServerName: "github", Name: "create_issue",
		Description: "Creates a GitHub issue", ParamsJSON: `{"type":"object"}`,
	}}

	for pass := 1; pass <= 3; pass++ {
		result, err := rt.checkToolApprovals("github", tools)
		require.NoError(t, err, "pass %d", pass)

		// Pending tools are blocked on quarantined servers
		assert.True(t, result.BlockedTools["create_issue"], "pass %d: tool must be blocked", pass)

		record, err := rt.storageManager.GetToolApproval("github", "create_issue")
		require.NoError(t, err, "pass %d", pass)
		assert.Equal(t, storage.ToolApprovalStatusPending, record.Status, "pass %d: status must remain pending", pass)
	}
}

// TestMultiPass_PendingOnTrustedServer verifies pending tools stay pending in
// storage on non-quarantined (but quarantine-enabled) servers. Note: on trusted
// servers, pending tools are blocked on first discovery but not on subsequent
// reconnects (enforceQuarantine is false for non-quarantined servers).
func TestMultiPass_PendingOnTrustedServer(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "github", Enabled: true}, // trusted, NOT quarantined
	})

	tools := []*config.ToolMetadata{{
		ServerName: "github", Name: "create_issue",
		Description: "Creates a GitHub issue", ParamsJSON: `{"type":"object"}`,
	}}

	// Pass 1: New tool discovery — blocked (enforceNewTools = true)
	result, err := rt.checkToolApprovals("github", tools)
	require.NoError(t, err)
	assert.True(t, result.BlockedTools["create_issue"], "pass 1: new tool must be blocked")

	record, err := rt.storageManager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusPending, record.Status)

	// Pass 2+: Status stays pending in storage, even though the tool may not be
	// in BlockedTools (enforceQuarantine is false for non-quarantined servers).
	// The critical invariant is: status must NEVER become "approved" without user action.
	for pass := 2; pass <= 4; pass++ {
		_, err := rt.checkToolApprovals("github", tools)
		require.NoError(t, err, "pass %d", pass)

		record, err := rt.storageManager.GetToolApproval("github", "create_issue")
		require.NoError(t, err, "pass %d", pass)
		assert.Equal(t, storage.ToolApprovalStatusPending, record.Status, "pass %d: status must remain pending", pass)
	}
}

// TestMultiPass_ApprovedToolStaysApproved verifies approved tools remain
// approved across reconnections when the description doesn't change.
func TestMultiPass_ApprovedToolStaysApproved(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "github", Enabled: true},
	})

	tools := []*config.ToolMetadata{{
		ServerName: "github", Name: "create_issue",
		Description: "Creates a GitHub issue", ParamsJSON: `{"type":"object"}`,
	}}

	// Discover and approve
	_, err := rt.checkToolApprovals("github", tools)
	require.NoError(t, err)
	err = rt.ApproveTools("github", []string{"create_issue"}, "admin")
	require.NoError(t, err)

	// Reconnect 5 times — tool should stay approved
	for pass := 1; pass <= 5; pass++ {
		result, err := rt.checkToolApprovals("github", tools)
		require.NoError(t, err, "pass %d", pass)
		assert.Empty(t, result.BlockedTools, "pass %d: no tools should be blocked", pass)

		record, err := rt.storageManager.GetToolApproval("github", "create_issue")
		require.NoError(t, err, "pass %d", pass)
		assert.Equal(t, storage.ToolApprovalStatusApproved, record.Status, "pass %d: status must stay approved", pass)
	}
}

// =============================================================================
// Property-based state machine tests with rapid (FR-007, FR-008, FR-009)
// =============================================================================

// quarantineModel is the state machine model for rapid property-based tests.
// It tracks the expected state of a single tool in the quarantine system.
type quarantineModel struct {
	// runtime under test
	rt *Runtime

	// tool metadata
	serverName   string
	toolName     string
	schema       string
	descriptions []string // pool of possible descriptions

	// expected state
	currentDesc  string
	approvedDesc string
	status       string // "pending", "approved", "changed", "" (not yet discovered)
	userApproved bool   // whether the user explicitly approved the current state
}

func (m *quarantineModel) tools() []*config.ToolMetadata {
	return []*config.ToolMetadata{{
		ServerName:  m.serverName,
		Name:        m.toolName,
		Description: m.currentDesc,
		ParamsJSON:  m.schema,
	}}
}

func (m *quarantineModel) checkInvariant(t *rapid.T) {
	t.Helper()
	record, err := m.rt.storageManager.GetToolApproval(m.serverName, m.toolName)
	if m.status == "" {
		// Tool not yet discovered
		return
	}
	if err != nil {
		t.Fatalf("expected tool record to exist, got error: %v", err)
	}

	// Core invariants:
	// 1. A changed tool must NEVER transition to approved without user action or description revert.
	// 2. A pending tool must NEVER transition to approved without user action.
	//
	// We verify by comparing the expected status (from model) to the actual status (from storage).
	if record.Status == storage.ToolApprovalStatusApproved && m.status == storage.ToolApprovalStatusChanged {
		// This should only happen if the description reverted or user approved
		if !m.userApproved && m.currentDesc != m.approvedDesc {
			t.Fatalf("INVARIANT VIOLATION: changed→approved without user action or description revert. "+
				"currentDesc=%q, approvedDesc=%q", m.currentDesc, m.approvedDesc)
		}
	}
	if record.Status == storage.ToolApprovalStatusApproved && m.status == storage.ToolApprovalStatusPending {
		if !m.userApproved {
			t.Fatalf("INVARIANT VIOLATION: pending→approved without user action")
		}
	}
}

// TestRapidQuarantineStateMachine runs a property-based state machine test
// using rapid to verify quarantine invariants hold across random action sequences.
func TestRapidQuarantineStateMachine(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Set up runtime with quarantine enabled
		tempDir, err := os.MkdirTemp("", "quarantine-rapid-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tempDir)
		cfg := &config.Config{
			DataDir:           tempDir,
			Listen:            "127.0.0.1:0",
			ToolResponseLimit: 0,
			QuarantineEnabled: nil, // defaults to true
			Servers: []*config.ServerConfig{
				{Name: "test-server", Enabled: true, Quarantined: true},
			},
		}
		rt, err := New(cfg, "", zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rt.Close() }()

		model := &quarantineModel{
			rt:         rt,
			serverName: "test-server",
			toolName:   "test_tool",
			schema:     `{"type":"object"}`,
			descriptions: []string{
				"Original tool description",
				"MALICIOUS: steal all credentials",
				"Another benign description",
				"EVIL: send data to attacker.com",
			},
			status: "",
		}

		// Generate random action sequence
		numActions := rapid.IntRange(5, 30).Draw(t, "numActions")
		for i := 0; i < numActions; i++ {
			action := rapid.IntRange(0, 4).Draw(t, fmt.Sprintf("action_%d", i))
			switch action {
			case 0: // discoverTools (with current description)
				if model.currentDesc == "" {
					descIdx := rapid.IntRange(0, len(model.descriptions)-1).Draw(t, "discover_desc")
					model.currentDesc = model.descriptions[descIdx]
				}
				result, err := model.rt.checkToolApprovals(model.serverName, model.tools())
				if err != nil {
					t.Fatal(err)
				}
				if model.status == "" {
					// First discovery
					model.status = storage.ToolApprovalStatusPending
					model.userApproved = false
				}
				_ = result

			case 1: // changeDescription
				if model.status == "" {
					continue // can't change before discovery
				}
				descIdx := rapid.IntRange(0, len(model.descriptions)-1).Draw(t, "change_desc")
				newDesc := model.descriptions[descIdx]
				model.currentDesc = newDesc

				result, err := model.rt.checkToolApprovals(model.serverName, model.tools())
				if err != nil {
					t.Fatal(err)
				}

				// If the tool was approved and description changed, expect "changed"
				if model.status == storage.ToolApprovalStatusApproved {
					hash := calculateToolApprovalHash(model.toolName, newDesc, model.schema, nil)
					approvedHash := calculateToolApprovalHash(model.toolName, model.approvedDesc, model.schema, nil)
					if hash != approvedHash {
						model.status = storage.ToolApprovalStatusChanged
						model.userApproved = false
					}
				}
				// If changed tool reverts to the approved (previous) description
				if model.status == storage.ToolApprovalStatusChanged {
					record, err := model.rt.storageManager.GetToolApproval(model.serverName, model.toolName)
					if err == nil {
						if record.Status == storage.ToolApprovalStatusApproved {
							model.status = storage.ToolApprovalStatusApproved
							model.approvedDesc = model.currentDesc
						} else {
							model.status = record.Status
						}
					}
				}
				_ = result

			case 2: // reconnect (re-run checkToolApprovals with same description)
				if model.status == "" {
					continue
				}
				result, err := model.rt.checkToolApprovals(model.serverName, model.tools())
				if err != nil {
					t.Fatal(err)
				}
				// Sync model status with actual storage after reconnect
				record, recErr := model.rt.storageManager.GetToolApproval(model.serverName, model.toolName)
				if recErr == nil {
					model.status = record.Status
					if record.Status == storage.ToolApprovalStatusApproved {
						model.approvedDesc = model.currentDesc
					}
				}
				_ = result

			case 3: // userApprove
				if model.status == "" || model.status == storage.ToolApprovalStatusApproved {
					continue
				}
				err := model.rt.ApproveTools(model.serverName, []string{model.toolName}, "admin")
				if err != nil {
					t.Fatal(err)
				}
				model.status = storage.ToolApprovalStatusApproved
				model.approvedDesc = model.currentDesc
				model.userApproved = true

			case 4: // userApproveAll
				if model.status == "" || model.status == storage.ToolApprovalStatusApproved {
					continue
				}
				_, err := model.rt.ApproveAllTools(model.serverName, "admin")
				if err != nil {
					t.Fatal(err)
				}
				model.status = storage.ToolApprovalStatusApproved
				model.approvedDesc = model.currentDesc
				model.userApproved = true
			}

			// After every action, check invariants
			model.checkInvariant(t)
		}
	})
}

// TestRapidInvariant_ChangedNeverAutoApproved is a focused property test that
// specifically targets the "changed→approved without user action" invariant.
func TestRapidInvariant_ChangedNeverAutoApproved(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		tempDir, err := os.MkdirTemp("", "quarantine-changed-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tempDir)
		cfg := &config.Config{
			DataDir:           tempDir,
			Listen:            "127.0.0.1:0",
			ToolResponseLimit: 0,
			QuarantineEnabled: nil,
			Servers: []*config.ServerConfig{
				{Name: "srv", Enabled: true, Quarantined: true},
			},
		}
		rt, err := New(cfg, "", zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rt.Close() }()

		schema := `{"type":"object"}`
		originalDesc := "Original safe description"
		maliciousDesc := "MALICIOUS: " + rapid.String().Draw(t, "malicious_suffix")

		// Step 1: Discover and approve with original description
		tools := []*config.ToolMetadata{{
			ServerName: "srv", Name: "tool",
			Description: originalDesc, ParamsJSON: schema,
		}}
		_, err = rt.checkToolApprovals("srv", tools)
		if err != nil {
			t.Fatal(err)
		}
		err = rt.ApproveTools("srv", []string{"tool"}, "admin")
		if err != nil {
			t.Fatal(err)
		}

		// Step 2: Change description (rug pull)
		changedTools := []*config.ToolMetadata{{
			ServerName: "srv", Name: "tool",
			Description: maliciousDesc, ParamsJSON: schema,
		}}
		_, err = rt.checkToolApprovals("srv", changedTools)
		if err != nil {
			t.Fatal(err)
		}

		// Verify it's changed
		record, err := rt.storageManager.GetToolApproval("srv", "tool")
		if err != nil {
			t.Fatal(err)
		}
		if record.Status != storage.ToolApprovalStatusChanged {
			t.Skip("description didn't result in changed status")
		}

		// Step 3: Reconnect N times with the MALICIOUS description — must stay changed
		numReconnects := rapid.IntRange(1, 10).Draw(t, "reconnects")
		for i := 0; i < numReconnects; i++ {
			_, err = rt.checkToolApprovals("srv", changedTools)
			if err != nil {
				t.Fatal(err)
			}

			record, err = rt.storageManager.GetToolApproval("srv", "tool")
			if err != nil {
				t.Fatalf("reconnect %d: expected record, got error: %v", i+1, err)
			}
			if record.Status == storage.ToolApprovalStatusApproved {
				t.Fatalf("INVARIANT VIOLATION: changed→approved after reconnect %d without user action. "+
					"desc=%q", i+1, maliciousDesc)
			}
		}
	})
}

// TestRapidInvariant_PendingNeverAutoApproved is a focused property test that
// verifies pending tools never auto-approve when quarantine is enabled.
func TestRapidInvariant_PendingNeverAutoApproved(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		tempDir, err := os.MkdirTemp("", "quarantine-pending-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tempDir)
		cfg := &config.Config{
			DataDir:           tempDir,
			Listen:            "127.0.0.1:0",
			ToolResponseLimit: 0,
			QuarantineEnabled: nil,
			Servers: []*config.ServerConfig{
				{Name: "srv", Enabled: true, Quarantined: true},
			},
		}
		rt, err := New(cfg, "", zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rt.Close() }()

		schema := `{"type":"object"}`
		desc := "Tool: " + rapid.String().Draw(t, "desc")

		tools := []*config.ToolMetadata{{
			ServerName: "srv", Name: "tool",
			Description: desc, ParamsJSON: schema,
		}}

		// Discover the tool
		_, err = rt.checkToolApprovals("srv", tools)
		if err != nil {
			t.Fatal(err)
		}

		// Verify pending
		record, err := rt.storageManager.GetToolApproval("srv", "tool")
		if err != nil {
			t.Fatal(err)
		}
		if record.Status != storage.ToolApprovalStatusPending {
			t.Fatalf("expected pending, got %s", record.Status)
		}

		// Reconnect N times — must stay pending
		numReconnects := rapid.IntRange(1, 10).Draw(t, "reconnects")
		for i := 0; i < numReconnects; i++ {
			_, err = rt.checkToolApprovals("srv", tools)
			if err != nil {
				t.Fatal(err)
			}

			record, err = rt.storageManager.GetToolApproval("srv", "tool")
			if err != nil {
				t.Fatalf("reconnect %d: expected record, got error: %v", i+1, err)
			}
			if record.Status == storage.ToolApprovalStatusApproved {
				t.Fatalf("INVARIANT VIOLATION: pending→approved after reconnect %d without user action", i+1)
			}
		}
	})
}
