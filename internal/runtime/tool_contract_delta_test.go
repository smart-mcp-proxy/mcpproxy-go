package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// contractDeltaCase is one row of testdata/contract_delta_cases.json (shared
// with frontend/tests/unit/contract-delta-parity.spec.ts).
type contractDeltaCase struct {
	Name             string           `json:"name"`
	Before           *json.RawMessage `json:"before"`
	After            *json.RawMessage `json:"after"`
	BeforeText       *string          `json:"before_text"`
	AfterText        *string          `json:"after_text"`
	BeforeOutput     *json.RawMessage `json:"before_output"`
	AfterOutput      *json.RawMessage `json:"after_output"`
	BeforeOutputText *string          `json:"before_output_text"`
	AfterOutputText  *string          `json:"after_output_text"`
	Signals          []string         `json:"signals"`
}

func (c contractDeltaCase) schemas() (before, after string) {
	pick := func(raw *json.RawMessage, text *string) string {
		if text != nil {
			return *text
		}
		if raw != nil {
			return string(*raw)
		}
		return ""
	}
	return pick(c.Before, c.BeforeText), pick(c.After, c.AfterText)
}

func (c contractDeltaCase) outputs() (before, after string) {
	pick := func(raw *json.RawMessage, text *string) string {
		if text != nil {
			return *text
		}
		if raw != nil {
			return string(*raw)
		}
		return ""
	}
	return pick(c.BeforeOutput, c.BeforeOutputText), pick(c.AfterOutput, c.AfterOutputText)
}

func loadContractDeltaCases(t *testing.T) []contractDeltaCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/contract_delta_cases.json")
	require.NoError(t, err)
	var doc struct {
		Cases []contractDeltaCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NotEmpty(t, doc.Cases)
	return doc.Cases
}

// TestContractDelta_SharedFixture (spec 120 FR-004/FR-005/FR-007): every rule,
// the benign cases that must NOT hold, and nested schemas, from the fixture the
// Web parity test reads too.
func TestContractDelta_SharedFixture(t *testing.T) {
	for _, tc := range loadContractDeltaCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			before, after := tc.schemas()
			beforeOut, afterOut := tc.outputs()
			got := signalStrings(contractDelta(
				contractSnapshot{InputSchema: before, OutputSchema: beforeOut},
				contractSnapshot{InputSchema: after, OutputSchema: afterOut},
			))
			want := tc.Signals
			if len(want) == 0 {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, want, got)
		})
	}
}

// TestContractDelta_IsDeterministicAndPure: the same inputs give the same
// signals in the same order, and the inputs are not mutated.
func TestContractDelta_IsDeterministicAndPure(t *testing.T) {
	before := `{"type":"object","properties":{"b":{"type":"string"},"a":{"type":"string","enum":["x"]}},"required":["a"]}`
	after := `{"type":"object","properties":{"b":{"type":"integer"},"a":{"type":"string","enum":["x","y"]},"c":{"type":"string","default":"q"}},"required":["a","b"]}`
	first := signalStrings(contractDelta(contractSnapshot{InputSchema: before}, contractSnapshot{InputSchema: after}))
	for i := 0; i < 20; i++ {
		assert.Equal(t, first, signalStrings(contractDelta(contractSnapshot{InputSchema: before}, contractSnapshot{InputSchema: after})))
	}
	assert.Equal(t, []string{
		"GATE-SCHEMA-SHAPE:new-required:b",
		"GATE-SCHEMA-SHAPE:enum-added:a",
		"GATE-SCHEMA-SHAPE:type:b",
		"GATE-SCHEMA-SHAPE:new-property:c",
	}, first)
}

// TestSignal_String pins the recorded form.
func TestSignal_String(t *testing.T) {
	assert.Equal(t, "GATE-HINT-SAFER-FLIP", Signal{Rule: "GATE-HINT-SAFER-FLIP"}.String())
	assert.Equal(t, "GATE-OUTPUT-SCHEMA:added", Signal{Rule: "GATE-OUTPUT-SCHEMA", Reason: "added"}.String())
	assert.Equal(t, "GATE-SCHEMA-SHAPE:new-required:bcc", Signal{Rule: "GATE-SCHEMA-SHAPE", Reason: "new-required", Path: "bcc"}.String())
}

// TestCompactGateSignals (review r1 P2): more gate signals than the record
// keeps are summarised per rule, never silently dropped: every rule family
// stays visible and the count of what was folded is recorded.
func TestCompactGateSignals(t *testing.T) {
	var many []Signal
	for i := 0; i < storage.MaxToolHeldSignals+5; i++ {
		many = append(many, Signal{Rule: "GATE-SCHEMA-SHAPE", Reason: "new-required", Path: fmt.Sprintf("p%02d", i)})
	}
	many = append(many, Signal{Rule: "GATE-OUTPUT-SCHEMA", Reason: "added"})
	out := withGateSignals(nil, many)
	rec := &storage.ToolApprovalRecord{}
	rec.SetScanHold(out.Reason, out.Verdict, out.Signals)
	assert.LessOrEqual(t, len(rec.HeldSignals), storage.MaxToolHeldSignals)
	assert.Contains(t, rec.HeldSignals, "GATE-OUTPUT-SCHEMA:added", "the rule ordered last is still recorded")
	assert.Contains(t, rec.HeldSignals, "GATE-SCHEMA-SHAPE:new-required:p00")
	folded := 0
	for _, s := range rec.HeldSignals {
		if strings.HasPrefix(s, "GATE-SCHEMA-SHAPE:more:+") {
			folded++
			assert.Equal(t, fmt.Sprintf("GATE-SCHEMA-SHAPE:more:+%d", len(many)-1-(maxGateSignals-1)), s)
		}
	}
	assert.Equal(t, 1, folded, "one marker per rule that lost signals: %v", rec.HeldSignals)

	short := []Signal{{Rule: "GATE-HINT-SAFER-FLIP"}, {Rule: "GATE-OUTPUT-SCHEMA", Reason: "added"}}
	assert.Equal(t, []string{"GATE-HINT-SAFER-FLIP", "GATE-OUTPUT-SCHEMA:added"}, withGateSignals(nil, short).Signals, "short lists pass through")
}
