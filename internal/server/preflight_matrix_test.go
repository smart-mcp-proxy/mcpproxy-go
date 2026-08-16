package server

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
)

// Spec 098 T026 — the committed sabotage matrix and the checks over it that must
// run EVERYWHERE, including hosts where the binary-driven E2E
// (preflight_e2e_test.go) cannot run. Keeping the reflection check here is the
// point: FR-016 says adding an enum code without its cell must fail CI, and a
// gate that only fires when a binary and node happen to be present is not a
// gate.

const preflightMatrixPath = "testdata/preflight_sabotage_matrix.json"

type sabotageExpectation struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Retryable is a pointer so "absent" (a ready row) is distinguishable from
	// an explicit false.
	Retryable *bool  `json:"retryable,omitempty"`
	Action    string `json:"action,omitempty"`
	Verdict   string `json:"verdict"`
	ExitCode  int    `json:"exit_code"`
	// RequestError is set INSTEAD of a verdict for a row whose request is
	// rejected rather than evaluated (spec 099 FR-012/FR-016: the cap boundary,
	// filters without check, the reserved field). Its value is the fragment the
	// error message must contain.
	RequestError string `json:"request_error,omitempty"`
	// PlainError is set INSTEAD of a verdict for an mcp-plain row: plain
	// describe_tool answers per-id codes from its own vocabulary, not preflight
	// reasons (spec 099 FR-011).
	PlainError string `json:"plain_error,omitempty"`
}

type sabotageScenario struct {
	Scenario string `json:"scenario"`
	Surface  string `json:"surface"`
	// Tier is the disclosure tier the row is observed at (spec 099 FR-016).
	Tier     string              `json:"tier"`
	Sabotage string              `json:"sabotage"`
	Expect   sabotageExpectation `json:"expect"`
}

// Matrix surfaces. The first two induce state for the REST endpoint; the last
// two are the spec-099 in-band surfaces.
const (
	surfaceE2E           = "e2e"
	surfaceStateInjected = "state-injected"
	surfaceMCPCheck      = "mcp-check"
	surfaceMCPPlain      = "mcp-plain"
)

// mcpCheckExemptReasons are the two codes the in-band surface cannot produce BY
// DESIGN, and therefore the only two the mcp-check coverage gate excuses (spec
// 099 FR-008/FR-009). The exemption is encoded here rather than left implicit:
// if either ever becomes observable in band, this list is what has to change,
// in the same commit as the behavior.
var mcpCheckExemptReasons = map[string]string{
	preflight.ReasonHashMismatch:     "in-band hash pins were trimmed from v1 (FR-008): nothing in band can request a pin",
	preflight.ReasonServerNotInScope: "the in-band surface is pinned to the agent-token tier (FR-009), where this collapses to not_found",
	// FR-009 names BOTH collapsing codes; FR-016's parenthetical listed only
	// the first. The collapse is symmetric in the evaluator and has to be — if
	// an unconfigured server answered differently from an out-of-scope one, a
	// token could probe arbitrary names and learn which servers exist behind
	// its scope. The two mcp_check_unknown_server / mcp_check_out_of_scope rows
	// assert the collapse itself, which is the observable behavior.
	preflight.ReasonServerNotConfigured: "the in-band surface is pinned to the agent-token tier (FR-009), where this collapses to not_found",
}

type sabotageMatrix struct {
	Scenarios []sabotageScenario `json:"scenarios"`
}

func loadSabotageMatrix(t *testing.T) map[string]sabotageScenario {
	t.Helper()

	raw, err := os.ReadFile(preflightMatrixPath)
	require.NoError(t, err, "the committed sabotage matrix must be readable")

	var matrix sabotageMatrix
	require.NoError(t, json.Unmarshal(raw, &matrix), "sabotage matrix must be valid JSON")
	require.NotEmpty(t, matrix.Scenarios)

	byName := make(map[string]sabotageScenario, len(matrix.Scenarios))
	for _, scenario := range matrix.Scenarios {
		require.NotEmpty(t, scenario.Scenario, "every scenario needs a key")
		_, dup := byName[scenario.Scenario]
		require.Falsef(t, dup, "duplicate scenario key %q", scenario.Scenario)
		byName[scenario.Scenario] = scenario
	}
	return byName
}

// TestPreflightSabotageMatrixCoversEveryReason is the reflection check FR-016
// demands: every code of the closed enum owns at least one scenario, and every
// scenario's expectation agrees with the normative FR-003 taxonomy (so the
// matrix can never quietly encode a wrong retryable flag or action and then be
// "confirmed" by an E2E that reads its expectations from it).
func TestPreflightSabotageMatrixCoversEveryReason(t *testing.T) {
	scenarios := loadSabotageMatrix(t)

	covered := make(map[string]int)
	coveredInBand := make(map[string]int)
	for name, scenario := range scenarios {
		expect := scenario.Expect
		require.NotEmptyf(t, scenario.Surface, "scenario %q: surface must say how the state is induced", name)
		require.NotEmptyf(t, scenario.Sabotage, "scenario %q: sabotage must describe the induced state", name)
		require.Containsf(t, []string{preflight.TierOperator, preflight.TierAgentToken},
			scenario.Tier, "scenario %q: tier must be a valid disclosure tier", name)
		if scenario.Surface == surfaceMCPCheck || scenario.Surface == surfaceMCPPlain {
			assert.Equalf(t, preflight.TierAgentToken, scenario.Tier,
				"scenario %q: the whole in-band surface is the agent-token tier (spec 099 FR-009)", name)
		}

		// Rows that are REJECTED rather than evaluated, and rows on the plain
		// surface with its own vocabulary, carry no verdict to check against
		// the taxonomy.
		if expect.RequestError != "" || expect.PlainError != "" {
			assert.Emptyf(t, expect.Status, "scenario %q: a rejected/plain row carries no preflight status", name)
			assert.Emptyf(t, expect.Reason, "scenario %q: a rejected/plain row carries no preflight reason", name)
			assert.Emptyf(t, expect.Verdict, "scenario %q: a rejected/plain row carries no verdict", name)
			continue
		}

		require.Containsf(t, []string{preflight.StatusReady, preflight.StatusUnavailable},
			expect.Status, "scenario %q: status must be a valid preflight status", name)

		if expect.Status == preflight.StatusReady {
			assert.Emptyf(t, expect.Reason, "scenario %q: a ready row carries no reason", name)
			assert.Nilf(t, expect.Retryable, "scenario %q: a ready row carries no retryable flag", name)
			assert.Emptyf(t, expect.Action, "scenario %q: a ready row carries no action", name)
			assert.Equalf(t, preflight.VerdictReady, expect.Verdict, "scenario %q", name)
			assert.Equalf(t, preflight.ExitReady, expect.ExitCode, "scenario %q", name)
			continue
		}

		require.Truef(t, preflight.ValidReason(expect.Reason),
			"scenario %q: %q is not a member of the closed reason enum", name, expect.Reason)
		covered[expect.Reason]++
		if scenario.Surface == surfaceMCPCheck {
			coveredInBand[expect.Reason]++
		}

		require.NotNilf(t, expect.Retryable, "scenario %q: a failure row must state retryable", name)
		assert.Equalf(t, preflight.Retryable(expect.Reason), *expect.Retryable,
			"scenario %q: retryable disagrees with the taxonomy for %s", name, expect.Reason)
		assert.Equalf(t, preflight.DefaultAction(expect.Reason), expect.Action,
			"scenario %q: action disagrees with the taxonomy for %s", name, expect.Reason)
		assert.Equalf(t, preflight.ReasonVerdict(expect.Reason), expect.Verdict,
			"scenario %q: verdict disagrees with the taxonomy for %s", name, expect.Reason)
		assert.Equalf(t, preflight.ExitCode(expect.Verdict), expect.ExitCode,
			"scenario %q: exit code disagrees with the taxonomy for %s", name, expect.Verdict)
	}

	for _, reason := range preflight.AllReasons() {
		assert.Positivef(t, covered[reason],
			"reason %q has no scenario in %s: FR-016 requires a sabotage cell per enum code",
			reason, preflightMatrixPath)

		if why, exempt := mcpCheckExemptReasons[reason]; exempt {
			assert.Zerof(t, coveredInBand[reason],
				"reason %q has an mcp-check row but is documented as REST-only (%s): either the row or the exemption is wrong",
				reason, why)
			continue
		}
		assert.Positivef(t, coveredInBand[reason],
			"reason %q has no mcp-check scenario in %s: spec 099 FR-016 requires an in-band cell per observable enum code "+
				"(add one, or document the code in mcpCheckExemptReasons with the design decision that makes it unreachable)",
			reason, preflightMatrixPath)
	}
}

// ---------------------------------------------------------------------------
// State-injected cells
// ---------------------------------------------------------------------------

// TestPreflightSabotageMatrixPendingAuthCell covers the one matrix row whose
// state cannot be induced from outside a running proxy deterministically: the
// deferred-OAuth PendingAuth state depends on an upstream's 401 handshake AND on
// the proxy choosing to defer rather than open a browser. The matrix marks it
// `surface: state-injected` and it is asserted here against the same evaluator
// the served endpoint calls, with the connection snapshot reporting PendingAuth
// (FR-007).
func TestPreflightSabotageMatrixPendingAuthCell(t *testing.T) {
	scenarios := loadSabotageMatrix(t)
	scenario, ok := scenarios["pending_auth"]
	require.True(t, ok, "the pending_auth cell must exist in the matrix")
	require.Equal(t, "state-injected", scenario.Surface)

	const server, tool = "oauthy", "sync_issues"
	id := server + ":" + tool

	results, err := preflight.Evaluate(context.Background(), preflight.EvalContext{
		Index:     stubIndex{tools: map[string][]string{server: {tool}}},
		Approvals: stubApprovals{},
		State:     stubState{state: preflight.RuntimeStatePendingAuth},
		Policy:    stubPolicy{enabled: map[string]bool{server: true}},
		Tier:      preflight.TierOperator,
	}, []preflight.ToolRef{{ID: id}})
	require.NoError(t, err)
	require.Len(t, results, 1)

	result := results[0]
	verdict := preflight.VerdictForResults(results)
	assert.Equal(t, scenario.Expect.Status, result.Status)
	assert.Equal(t, scenario.Expect.Reason, result.Reason)
	require.NotNil(t, scenario.Expect.Retryable)
	assert.Equal(t, *scenario.Expect.Retryable, result.Retryable, "waiting cannot resolve a missing login")
	assert.Equal(t, scenario.Expect.Action, result.Action)
	assert.Equal(t, scenario.Expect.Verdict, verdict)
	assert.Equal(t, scenario.Expect.ExitCode, preflight.ExitCode(verdict))
}

// --- minimal read-interface stubs (no proxy wiring, no I/O) ---

type stubIndex struct{ tools map[string][]string }

func (s stubIndex) ToolsByServer(serverName string) ([]preflight.IndexedTool, error) {
	out := make([]preflight.IndexedTool, 0, len(s.tools[serverName]))
	for _, name := range s.tools[serverName] {
		out = append(out, preflight.IndexedTool{Name: serverName + ":" + name})
	}
	return out, nil
}

func (s stubIndex) IndexedServerNames() ([]string, error) {
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	return names, nil
}

type stubApprovals struct{}

func (stubApprovals) ToolApproval(_, _ string) (*preflight.ApprovalState, error) { return nil, nil }

type stubState struct{ state preflight.ServerRuntimeState }

func (s stubState) ServerRuntime(_ string) (preflight.ServerRuntime, bool) {
	return preflight.ServerRuntime{State: s.state}, true
}

type stubPolicy struct{ enabled map[string]bool }

func (p stubPolicy) ServerPolicy(serverName string) (preflight.ServerPolicy, error) {
	enabled, found := p.enabled[serverName]
	return preflight.ServerPolicy{Found: found, Enabled: enabled}, nil
}

func (stubPolicy) ToolConfigDenied(_, _ string) (bool, error) { return false, nil }
func (stubPolicy) QuarantineEnabled() bool                    { return true }
