package preflight

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fr003Row transcribes one row of the NORMATIVE FR-003 table (spec.md).
// This table is locked: if the implementation disagrees, the implementation is
// wrong. Adding an enum code without adding its row here fails
// TestReasonTable_CoversExactlyTheClosedEnum.
type fr003Row struct {
	reason    Reason
	class     Class
	retryable bool
	action    string // "" == the action field is OMITTED
	verdict   Verdict
	exit      int
}

var fr003Table = []fr003Row{
	{ReasonServerInitializing, ClassRetryable, true, "", VerdictDegradedRetryable, 10},
	{ReasonServerUnhealthy, ClassRetryable, true, "view_logs", VerdictDegradedRetryable, 10},
	{ReasonServerDisabled, ClassFixState, false, "enable", VerdictBlocked, 11},
	{ReasonServerQuarantined, ClassFixState, false, "approve", VerdictBlocked, 11},
	{ReasonToolPendingApproval, ClassFixState, false, "approve", VerdictBlocked, 11},
	{ReasonToolChanged, ClassFixState, false, "approve", VerdictBlocked, 11},
	{ReasonToolBlockedByUser, ClassFixState, false, "enable", VerdictBlocked, 11},
	{ReasonOAuthRequired, ClassFixState, false, "login", VerdictBlocked, 11},
	{ReasonHashMismatch, ClassFixState, false, "configure", VerdictBlocked, 11},
	{ReasonServerNotInScope, ClassPermanentConfig, false, "configure", VerdictBlocked, 11},
	{ReasonToolDeniedByConfig, ClassPermanentConfig, false, "configure", VerdictBlocked, 11},
	{ReasonMissingAnnotation, ClassPermanentConfig, false, "configure", VerdictBlocked, 11},
	{ReasonPolicyFiltered, ClassPermanentConfig, false, "", VerdictBlocked, 11},
	{ReasonNotFound, ClassPermanentConfig, false, "configure", VerdictUnknownIDs, 12},
	{ReasonServerNotConfigured, ClassPermanentConfig, false, "configure", VerdictUnknownIDs, 12},
}

func TestFR003Table(t *testing.T) {
	require.Len(t, fr003Table, 15, "the v1 enum is exactly 15 codes")

	for _, row := range fr003Table {
		t.Run(row.reason, func(t *testing.T) {
			assert.True(t, ValidReason(row.reason), "reason must be a member of the closed enum")
			assert.Equal(t, row.class, ReasonClass(row.reason), "class")
			assert.Equal(t, row.retryable, Retryable(row.reason), "retryable")
			assert.Equal(t, row.action, DefaultAction(row.reason),
				"default action (empty string == field omitted, per the health constants)")
			assert.Equal(t, row.verdict, ReasonVerdict(row.reason), "set verdict")
			assert.Equal(t, row.exit, ExitCode(ReasonVerdict(row.reason)), "CLI exit code")
			assert.NotEmpty(t, DefaultRemediation(row.reason), "every reason carries one actionable instruction")
		})
	}
}

// The enum is closed: the implementation table and the spec table must contain
// exactly the same members, so a new code cannot ship without its spec row.
func TestReasonTable_CoversExactlyTheClosedEnum(t *testing.T) {
	spec := map[Reason]bool{}
	for _, row := range fr003Table {
		spec[row.reason] = true
	}
	impl := map[Reason]bool{}
	for code := range reasonTable {
		impl[code] = true
	}
	assert.Equal(t, spec, impl, "reasons.go and the FR-003 spec table must be identical sets")

	assert.Len(t, AllReasons(), 15)
	inPrecedence := map[Reason]int{}
	for _, r := range Precedence {
		inPrecedence[r]++
	}
	for code := range impl {
		assert.Equal(t, 1, inPrecedence[code], "%s must appear exactly once in the precedence chain", code)
	}
	assert.Len(t, Precedence, 15, "precedence covers the whole enum")
}

// TestPrecedence_ExactOrder pins the FR-004 chain verbatim.
func TestPrecedence_ExactOrder(t *testing.T) {
	want := []Reason{
		"server_not_configured",
		"server_not_in_scope",
		"server_quarantined",
		"server_disabled",
		"not_found",
		"tool_denied_by_config",
		"tool_blocked_by_user",
		"tool_changed",
		"tool_pending_approval",
		"hash_mismatch",
		"oauth_required",
		"server_unhealthy",
		"server_initializing",
		"missing_annotation",
		"policy_filtered",
	}
	assert.Equal(t, want, Precedence)
}

// The enum values are a wire contract: a rename is a breaking change.
func TestReasonWireValues(t *testing.T) {
	assert.Equal(t, "server_initializing", ReasonServerInitializing)
	assert.Equal(t, "server_unhealthy", ReasonServerUnhealthy)
	assert.Equal(t, "server_disabled", ReasonServerDisabled)
	assert.Equal(t, "server_quarantined", ReasonServerQuarantined)
	assert.Equal(t, "tool_pending_approval", ReasonToolPendingApproval)
	assert.Equal(t, "tool_changed", ReasonToolChanged)
	assert.Equal(t, "tool_blocked_by_user", ReasonToolBlockedByUser)
	assert.Equal(t, "oauth_required", ReasonOAuthRequired)
	assert.Equal(t, "hash_mismatch", ReasonHashMismatch)
	assert.Equal(t, "server_not_in_scope", ReasonServerNotInScope)
	assert.Equal(t, "tool_denied_by_config", ReasonToolDeniedByConfig)
	assert.Equal(t, "missing_annotation", ReasonMissingAnnotation)
	assert.Equal(t, "policy_filtered", ReasonPolicyFiltered)
	assert.Equal(t, "not_found", ReasonNotFound)
	assert.Equal(t, "server_not_configured", ReasonServerNotConfigured)

	assert.Equal(t, "ready", StatusReady)
	assert.Equal(t, "unavailable", StatusUnavailable)
	assert.Equal(t, "ready", VerdictReady)
	assert.Equal(t, "degraded_retryable", VerdictDegradedRetryable)
	assert.Equal(t, "blocked", VerdictBlocked)
	assert.Equal(t, "unknown_ids", VerdictUnknownIDs)
}

// `server_saturated` is reserved but unimplemented: it must not be a member of
// the enum, or the wire contract would promise a verdict nothing emits.
func TestReservedCodeNotImplemented(t *testing.T) {
	assert.False(t, ValidReason("server_saturated"))
}

func TestVerdictAggregation_WorstClassWins(t *testing.T) {
	tests := []struct {
		name    string
		reasons []Reason
		want    Verdict
		exit    int
	}{
		{"all ready", nil, VerdictReady, 0},
		{"only retryable", []Reason{ReasonServerInitializing, ReasonServerUnhealthy}, VerdictDegradedRetryable, 10},
		{"blocked beats retryable", []Reason{ReasonServerInitializing, ReasonToolChanged}, VerdictBlocked, 11},
		{"unknown id beats blocked", []Reason{ReasonToolChanged, ReasonNotFound}, VerdictUnknownIDs, 12},
		{"unknown id beats everything", []Reason{ReasonServerInitializing, ReasonToolChanged, ReasonServerNotConfigured}, VerdictUnknownIDs, 12},
		{"order independent", []Reason{ReasonNotFound, ReasonServerInitializing}, VerdictUnknownIDs, 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VerdictForReasons(tt.reasons)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.exit, ExitCode(got))
		})
	}
}

func TestVerdictForResults_IgnoresReadyEntries(t *testing.T) {
	results := []Result{
		{ID: "a:one", Status: StatusReady},
		{ID: "b:two", Status: StatusUnavailable, Reason: ReasonServerInitializing},
		{ID: "c:three", Status: StatusReady},
	}
	assert.Equal(t, VerdictDegradedRetryable, VerdictForResults(results))

	allReady := []Result{{ID: "a:one", Status: StatusReady}}
	assert.Equal(t, VerdictReady, VerdictForResults(allReady))
	assert.Equal(t, 0, ExitCode(VerdictForResults(allReady)))
}

// Unknown codes (a newer proxy talking to an older consumer) must degrade
// safely: non-retryable, blocked, never "ready".
func TestUnknownCodeDegradesSafely(t *testing.T) {
	const future = "some_future_reason"
	assert.False(t, ValidReason(future))
	assert.False(t, Retryable(future))
	assert.Equal(t, ClassPermanentConfig, ReasonClass(future))
	assert.Equal(t, VerdictBlocked, ReasonVerdict(future))
	assert.Equal(t, ExitBlocked, ExitCode("nonsense_verdict"))
}
