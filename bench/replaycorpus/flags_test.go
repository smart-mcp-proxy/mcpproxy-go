package replaycorpus

import (
	"fmt"
	"strings"
	"testing"
)

// record builds one JSONL line from the fields a flag test cares about.
func record(t *testing.T, fields map[string]any) string {
	t.Helper()
	base := map[string]any{
		"id":              "rec-1",
		"type":            "tool_call",
		"server_name":     "github",
		"tool_name":       "list_issues",
		"status":          "success",
		"work_session_id": "ws-1",
		"request_id":      "req-1",
		"timestamp":       "2026-08-01T10:00:00Z",
	}
	for k, v := range fields {
		base[k] = v
	}
	return marshalLine(t, base)
}

func firstCall(t *testing.T, c *Corpus) *ReplayCall {
	t.Helper()
	if len(c.Sessions) != 1 {
		t.Fatalf("len(Sessions) = %d, want 1", len(c.Sessions))
	}
	all := c.Sessions[0].AllCalls()
	if len(all) == 0 {
		t.Fatal("session has no calls")
	}
	return all[0]
}

// A truncated record must never contribute silently (FR-002, SC-008). With a
// pre-truncation byte length it contributes an ANNOTATED estimate; without one
// it contributes nothing and is counted. It is never measured.
func TestTruncatedRecordNeverContributesSilently_WithBytes(t *testing.T) {
	c := loadString(t, record(t, map[string]any{
		"response_truncated": true,
		"response_bytes":     8192,
	}), testOptions(t.TempDir()))

	call := firstCall(t, c)
	if !call.Flags.Truncated {
		t.Error("call is not flagged truncated")
	}
	if !c.Sessions[0].Usability.Truncated {
		t.Error("session is not flagged truncated")
	}
	if call.ResponseCost.Basis != CostEstimated {
		t.Fatalf("ResponseCost.Basis = %s, want %s", call.ResponseCost.Basis, CostEstimated)
	}
	if !call.ResponseCost.Truncated {
		t.Error("the estimate is not annotated as coming from a truncated record")
	}
	if call.ResponseCost.Tokens != 0 {
		t.Errorf("an estimate must carry no token count, got %d", call.ResponseCost.Tokens)
	}
	if got := c.Exclusions.Flagged[ReasonTruncated]; got != 1 {
		t.Errorf("Flagged[%s] = %d, want 1", ReasonTruncated, got)
	}
}

func TestTruncatedRecordNeverContributesSilently_WithoutBytes(t *testing.T) {
	c := loadString(t, record(t, map[string]any{
		"response_truncated": true,
	}), testOptions(t.TempDir()))

	call := firstCall(t, c)
	if call.ResponseCost.Basis != CostUnavailable {
		t.Fatalf("ResponseCost.Basis = %s, want %s", call.ResponseCost.Basis, CostUnavailable)
	}
	if c.Exclusions.Withheld[ReasonNoByteCounts] != 1 {
		t.Errorf("Withheld[%s] = %d, want 1", ReasonNoByteCounts, c.Exclusions.Withheld[ReasonNoByteCounts])
	}
	if c.Exclusions.TotalWithheld() == 0 {
		t.Error("withholding a response cost was not counted anywhere")
	}
}

// Known byte-coverage gap 1: the internal retrieve_tools emission carries no
// byte counts at all, so bodies-off it falls to exclusion accounting — never to
// a zero that would read as a free call.
func TestInternalRetrieveToolsHasNoByteCounts(t *testing.T) {
	c := loadString(t, record(t, map[string]any{
		"id":          "rt-1",
		"type":        "internal_tool_call",
		"tool_name":   "retrieve_tools",
		"server_name": "",
	}), testOptions(t.TempDir()))

	call := firstCall(t, c)
	if call.ResponseCost.Basis != CostUnavailable {
		t.Fatalf("Basis = %s, want %s", call.ResponseCost.Basis, CostUnavailable)
	}
	if call.ResponseCost.Reason != ReasonInternalNoByteCounts {
		t.Errorf("Reason = %s, want %s", call.ResponseCost.Reason, ReasonInternalNoByteCounts)
	}
	if c.Exclusions.Withheld[ReasonInternalNoByteCounts] != 1 {
		t.Errorf("the gap was not counted: %+v", c.Exclusions.Withheld)
	}
}

// Known byte-coverage gap 2: code-execution sub-calls emit BOTH byte counts as
// zero, so a sub-call is unaccountable bodies-off for a different reason and
// must be reported under its own name.
func TestCodeExecutionSubCallZeroBytesIsExcluded(t *testing.T) {
	parent := record(t, map[string]any{
		"id": "p", "type": "internal_tool_call", "tool_name": "code_execution",
		"server_name": "", "request_id": "req-parent",
	})
	sub := record(t, map[string]any{
		"id": "s", "request_id": "req-sub", "parent_id": "req-parent",
		"request_bytes": 0, "response_bytes": 0,
	})
	c := loadString(t, parent+"\n"+sub, testOptions(t.TempDir()))

	subCall := c.Sessions[0].Calls[0].SubCalls[0]
	if subCall.ResponseCost.Basis != CostUnavailable {
		t.Fatalf("Basis = %s, want %s", subCall.ResponseCost.Basis, CostUnavailable)
	}
	if subCall.ResponseCost.Reason != ReasonSubCallZeroBytes {
		t.Errorf("Reason = %s, want %s", subCall.ResponseCost.Reason, ReasonSubCallZeroBytes)
	}
	if c.Exclusions.Withheld[ReasonSubCallZeroBytes] == 0 {
		t.Errorf("the sub-call gap was not counted: %+v", c.Exclusions.Withheld)
	}
}

func TestBodiesMissingIsFlaggedWhenBodiesOff(t *testing.T) {
	c := loadString(t, record(t, map[string]any{"response_bytes": 10}), testOptions(t.TempDir()))
	call := firstCall(t, c)
	if !call.Flags.BodiesMissing {
		t.Error("bodies-off load did not flag bodies_missing on the call")
	}
	if !c.Sessions[0].Usability.BodiesMissing {
		t.Error("bodies-off load did not flag bodies_missing on the session")
	}
	if c.Exclusions.Flagged[ReasonBodiesMissing] != 1 {
		t.Errorf("bodies_missing was not counted: %+v", c.Exclusions.Flagged)
	}
}

func TestSensitiveIsFlaggedAndCounted(t *testing.T) {
	c := loadString(t, record(t, map[string]any{
		"has_sensitive_data": true,
		"response_bytes":     100,
	}), testOptions(t.TempDir()))

	call := firstCall(t, c)
	if !call.Flags.Sensitive {
		t.Error("call is not flagged sensitive")
	}
	if !c.Sessions[0].Usability.Sensitive {
		t.Error("session is not flagged sensitive")
	}
	if c.Exclusions.Flagged[ReasonSensitive] != 1 {
		t.Errorf("sensitive was not counted: %+v", c.Exclusions.Flagged)
	}
}

func TestUnreplayableToolIsFlaggedAndCounted(t *testing.T) {
	opts := testOptions(t.TempDir())
	opts.FleetResolver = func(server, tool string) bool { return server == "github" && tool == "list_issues" }

	c := loadString(t, record(t, map[string]any{"tool_name": "gone_tool"}), opts)
	call := firstCall(t, c)
	if !call.Flags.Unreplayable {
		t.Error("call referencing a tool absent from the fleet is not flagged unreplayable")
	}
	if !c.Sessions[0].Usability.Unreplayable {
		t.Error("session is not flagged unreplayable")
	}
	if c.Exclusions.Flagged[ReasonUnreplayable] != 1 {
		t.Errorf("unreplayable was not counted: %+v", c.Exclusions.Flagged)
	}
	if !c.FleetChecked {
		t.Error("FleetChecked is false although a resolver was supplied")
	}
}

func TestUnreplayableIsNotClaimedWithoutAFleetInput(t *testing.T) {
	c := loadString(t, record(t, map[string]any{"tool_name": "gone_tool"}), testOptions(t.TempDir()))
	if c.FleetChecked {
		t.Error("FleetChecked is true although no resolver was supplied")
	}
	if firstCall(t, c).Flags.Unreplayable {
		t.Error("unreplayable was asserted without a fleet to resolve against")
	}
}

func TestExclusionRowsAreDeterministic(t *testing.T) {
	rep := ExclusionReport{
		Dropped: map[ExclusionReason]int{
			ReasonUnattributed: 2,
			ReasonNotACall:     1,
			ReasonMissingTool:  3,
		},
	}
	want := fmt.Sprint(rep.DroppedRows())
	for i := 0; i < 20; i++ {
		if got := fmt.Sprint(rep.DroppedRows()); got != want {
			t.Fatalf("DroppedRows() is not deterministic: %s != %s", got, want)
		}
	}
	rows := rep.DroppedRows()
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Reason >= rows[i].Reason {
			t.Fatalf("rows are not sorted by reason: %v", rows)
		}
	}
}

func TestUsabilityFlagsAreComputedOnceAtLoad(t *testing.T) {
	// Flags are data on the loaded value, not a re-derivation: reading them
	// twice cannot change them, and nothing downstream needs the record to
	// re-classify (data-model.md, "computed once at load").
	c := loadString(t, record(t, map[string]any{"response_truncated": true, "response_bytes": 5}), testOptions(t.TempDir()))
	before := c.Sessions[0].Usability

	// Assert the EXPECTED initial state before testing stability. Without this
	// the test is vacuous: delete load-time classification entirely and
	// before == after == Flags{}, so "nothing changed" still passes and the
	// test reports green on a loader that classifies nothing at all.
	if !before.Truncated {
		t.Fatalf("load-time classification must already have set Truncated, got %+v", before)
	}

	_ = c.Sessions[0].AllCalls()
	if c.Sessions[0].Usability != before {
		t.Error("usability flags changed after a downstream read")
	}
}

func TestFlagsUsableReportsTheReasons(t *testing.T) {
	f := Flags{Truncated: true, Sensitive: true}
	if f.Usable() {
		t.Error("Usable() is true for a truncated, sensitive session")
	}
	reasons := f.Reasons()
	if len(reasons) != 2 {
		t.Fatalf("Reasons() = %v, want 2 entries", reasons)
	}
	if !strings.Contains(fmt.Sprint(reasons), string(ReasonTruncated)) {
		t.Errorf("Reasons() = %v, missing %s", reasons, ReasonTruncated)
	}
	if (Flags{}).Usable() != true {
		t.Error("a clean session is not Usable()")
	}
}
