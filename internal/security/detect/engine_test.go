package detect

import (
	"reflect"
	"testing"
)

// fakeCheck is a test double whose behavior is supplied per case.
type fakeCheck struct {
	id string
	fn func(ToolView, RegistryView) []Signal
}

func (f fakeCheck) ID() string                                  { return f.id }
func (f fakeCheck) Inspect(t ToolView, r RegistryView) []Signal { return f.fn(t, r) }

// flagByName emits a soft signal for any tool whose name matches.
func flagByName(id, name string) fakeCheck {
	return fakeCheck{id: id, fn: func(t ToolView, _ RegistryView) []Signal {
		if t.Name == name {
			return []Signal{{CheckID: id, Tier: TierSoft, ThreatType: ThreatToolPoisoning, Confidence: 0.5, Detail: "matched"}}
		}
		return nil
	}}
}

func panicCheck(id string) fakeCheck {
	return fakeCheck{id: id, fn: func(ToolView, RegistryView) []Signal {
		panic("boom")
	}}
}

func sampleReg() RegistryView {
	return NewRegistryView([]ToolView{
		{Server: "s", Name: "good"},
		{Server: "s", Name: "bad"},
	})
}

func TestEngineDeterminism(t *testing.T) {
	e := NewEngine(Options{Checks: []Check{flagByName("c.one", "bad")}})
	reg := sampleReg()
	r1 := e.Scan(reg)
	r2 := e.Scan(reg)
	if !reflect.DeepEqual(r1, r2) {
		t.Errorf("Scan not deterministic:\n%+v\n%+v", r1, r2)
	}
	if len(r1.Findings) != 1 || r1.Findings[0].Location != "s:bad" {
		t.Errorf("unexpected findings: %+v", r1.Findings)
	}
}

func TestEngineTotalityIsolatesPanic(t *testing.T) {
	e := NewEngine(Options{Checks: []Check{
		flagByName("c.good", "bad"),
		panicCheck("c.panic"),
	}})
	r := e.Scan(sampleReg())

	if r.Coverage.ChecksFailed != 1 {
		t.Errorf("ChecksFailed = %d, want 1", r.Coverage.ChecksFailed)
	}
	if len(r.Coverage.FailedCheckIDs) != 1 || r.Coverage.FailedCheckIDs[0] != "c.panic" {
		t.Errorf("FailedCheckIDs = %v, want [c.panic]", r.Coverage.FailedCheckIDs)
	}
	if r.Coverage.ChecksRun != 1 {
		t.Errorf("ChecksRun = %d, want 1", r.Coverage.ChecksRun)
	}
	// The non-panicking check's finding survives the panic.
	if len(r.Findings) != 1 || r.Findings[0].Location != "s:bad" {
		t.Errorf("panic should not drop other findings: %+v", r.Findings)
	}
}

func TestEngineNoSignalsNoFindings(t *testing.T) {
	e := NewEngine(Options{Checks: []Check{flagByName("c", "nonexistent")}})
	r := e.Scan(sampleReg())
	if len(r.Findings) != 0 {
		t.Errorf("expected no findings, got %+v", r.Findings)
	}
	if r.Coverage.ChecksRun != 1 || r.Coverage.ChecksFailed != 0 {
		t.Errorf("coverage = %+v", r.Coverage)
	}
}

func TestEngineBuildsUnindexedRegistry(t *testing.T) {
	// Passing a RegistryView with only Tools set (no indexes) must still work —
	// the engine builds the view once per scan.
	e := NewEngine(Options{Checks: []Check{flagByName("c", "bad")}})
	r := e.Scan(RegistryView{Tools: []ToolView{{Server: "s", Name: "bad"}}})
	if len(r.Findings) != 1 {
		t.Errorf("expected 1 finding from unindexed reg, got %d", len(r.Findings))
	}
}

func TestEngineDefaultScannerID(t *testing.T) {
	e := NewEngine(Options{Checks: []Check{flagByName("c", "bad")}})
	r := e.Scan(sampleReg())
	if len(r.Findings) != 1 || r.Findings[0].Scanner != "tpa-descriptions" {
		t.Errorf("default scanner id not applied: %+v", r.Findings)
	}
}
