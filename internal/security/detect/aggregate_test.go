package detect

import "testing"

func soft(id string, conf float64) Signal {
	return Signal{CheckID: id, Tier: TierSoft, ThreatType: ThreatToolPoisoning, Confidence: conf, Detail: id}
}
func hard(id string, conf float64) Signal {
	return Signal{CheckID: id, Tier: TierHard, ThreatType: ThreatPromptInjection, Confidence: conf, Detail: id}
}

func TestAggregateNoSignals(t *testing.T) {
	if _, ok := aggregate(ToolView{Name: "x"}, nil, "s"); ok {
		t.Fatal("no signals must yield ok=false")
	}
}

func TestAggregateHardIsDangerous(t *testing.T) {
	tool := ToolView{Server: "srv", Name: "calc"}
	f, ok := aggregate(tool, []Signal{hard("unicode.hidden", 0.95)}, "tpa-descriptions")
	if !ok {
		t.Fatal("expected a finding")
	}
	if f.ThreatLevel != ThreatLevelDangerous {
		t.Errorf("ThreatLevel = %q, want dangerous", f.ThreatLevel)
	}
	if f.Severity != SeverityCritical {
		t.Errorf("Severity = %q, want critical (escalated hard)", f.Severity)
	}
	if f.ThreatType != ThreatPromptInjection {
		t.Errorf("ThreatType = %q, want prompt_injection", f.ThreatType)
	}
	if f.Scanner != "tpa-descriptions" {
		t.Errorf("Scanner = %q", f.Scanner)
	}
	if f.Location != "srv:calc" {
		t.Errorf("Location = %q, want srv:calc", f.Location)
	}
	if len(f.Signals) != 1 || f.Signals[0] != "unicode.hidden" {
		t.Errorf("Signals = %v", f.Signals)
	}
}

func TestAggregateHardNonEscalatedIsHigh(t *testing.T) {
	f, _ := aggregate(ToolView{Name: "t"}, []Signal{hard("shadowing.cross_server", 0.6)}, "s")
	if f.Severity != SeverityHigh {
		t.Errorf("Severity = %q, want high (non-escalated hard)", f.Severity)
	}
	if f.ThreatLevel != ThreatLevelDangerous {
		t.Errorf("ThreatLevel = %q, want dangerous", f.ThreatLevel)
	}
}

func TestAggregateSoftSeverityLadder(t *testing.T) {
	cases := []struct {
		name string
		sigs []Signal
		want string
	}{
		{"one→low", []Signal{soft("a", 0.4)}, SeverityLow},
		{"two→medium", []Signal{soft("a", 0.4), soft("b", 0.3)}, SeverityMedium},
		{"three→high", []Signal{soft("a", 0.3), soft("b", 0.3), soft("c", 0.3)}, SeverityHigh},
		{"dupes count once", []Signal{soft("a", 0.2), soft("a", 0.2)}, SeverityLow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, ok := aggregate(ToolView{Name: "t"}, tc.sigs, "s")
			if !ok {
				t.Fatal("expected finding")
			}
			if f.Severity != tc.want {
				t.Errorf("Severity = %q, want %q", f.Severity, tc.want)
			}
			if f.ThreatLevel != ThreatLevelWarning {
				t.Errorf("soft-only ThreatLevel = %q, want warning", f.ThreatLevel)
			}
		})
	}
}

func TestAggregateConsensusRaisesConfidence(t *testing.T) {
	single, _ := aggregate(ToolView{Name: "t"}, []Signal{soft("a", 0.5)}, "s")
	double, _ := aggregate(ToolView{Name: "t"}, []Signal{soft("a", 0.5), soft("b", 0.4)}, "s")
	if !(double.Confidence > single.Confidence) {
		t.Errorf("consensus confidence %v not greater than single %v", double.Confidence, single.Confidence)
	}
	if single.Confidence != 0.5 {
		t.Errorf("single confidence = %v, want 0.5", single.Confidence)
	}
	// Independent signals add, capped at 1.0.
	capped, _ := aggregate(ToolView{Name: "t"}, []Signal{soft("a", 0.7), soft("b", 0.8)}, "s")
	if capped.Confidence != 1.0 {
		t.Errorf("capped confidence = %v, want 1.0", capped.Confidence)
	}
}

func TestAggregateDistinctSignalsList(t *testing.T) {
	f, _ := aggregate(ToolView{Name: "t"}, []Signal{soft("b", 0.2), soft("a", 0.2), soft("b", 0.2)}, "s")
	// First-seen order, deduped.
	if len(f.Signals) != 2 || f.Signals[0] != "b" || f.Signals[1] != "a" {
		t.Errorf("Signals = %v, want [b a]", f.Signals)
	}
}
