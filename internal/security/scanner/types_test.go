package scanner

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestScanFindingConfidenceSignalsRoundTrip verifies the additive Spec-076
// fields survive a JSON round-trip and serialize under their documented keys.
func TestScanFindingConfidenceSignalsRoundTrip(t *testing.T) {
	f := ScanFinding{
		RuleID:     "detect.unicode.hidden",
		Severity:   SeverityHigh,
		ThreatType: ThreatToolPoisoning,
		Confidence: 0.75,
		Signals:    []string{"unicode.hidden", "directive.imperative"},
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"confidence":0.75`) {
		t.Errorf("confidence not serialized under expected key: %s", data)
	}
	if !strings.Contains(string(data), `"signals":["unicode.hidden","directive.imperative"]`) {
		t.Errorf("signals not serialized under expected key: %s", data)
	}

	var back ScanFinding
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Confidence != 0.75 {
		t.Errorf("Confidence round-trip = %v, want 0.75", back.Confidence)
	}
	if !reflect.DeepEqual(back.Signals, f.Signals) {
		t.Errorf("Signals round-trip = %v, want %v", back.Signals, f.Signals)
	}
}

// TestScanFindingOmitEmpty ensures the new fields are omitempty so existing
// consumers and stored findings are byte-unaffected when they are unset.
func TestScanFindingOmitEmpty(t *testing.T) {
	data, err := json.Marshal(ScanFinding{RuleID: "legacy"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "confidence") {
		t.Errorf("zero Confidence should be omitted: %s", data)
	}
	if strings.Contains(string(data), "signals") {
		t.Errorf("empty Signals should be omitted: %s", data)
	}
}
