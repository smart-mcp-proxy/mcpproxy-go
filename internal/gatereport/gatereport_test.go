package gatereport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// passAllBlocking returns one passing fragment per blocking manifest entry.
func passAllBlocking() []Fragment {
	var frags []Fragment
	for _, m := range Manifest() {
		if m.Reserved {
			continue
		}
		frags = append(frags, Fragment{Name: m.Name, Status: StatusPass})
	}
	return frags
}

func entryByName(t *testing.T, r *Report, name string) *Entry {
	t.Helper()
	for i := range r.Entries {
		if r.Entries[i].Name == name {
			return &r.Entries[i]
		}
	}
	t.Fatalf("entry %s not found in report", name)
	return nil
}

func TestMerge_AllBlockingPass_VerdictPass_ReservedNotRun(t *testing.T) {
	r := Merge(passAllBlocking())
	if !r.Passed() {
		t.Fatalf("expected pass verdict, got %s (failures: %v)", r.Verdict, r.BlockingFailures)
	}
	// Reserved slots must be recorded, never silently absent (FR-004).
	for _, name := range []string{EntryReservedMacOSSmoke, EntryReservedSurfaceConsistency} {
		e := entryByName(t, r, name)
		if e.Status != StatusNotRun || e.Reason != ReasonNotImplemented {
			t.Errorf("%s: got status=%s reason=%q, want not-run/%s", name, e.Status, e.Reason, ReasonNotImplemented)
		}
		if e.Blocking {
			t.Errorf("%s must not be blocking while reserved", name)
		}
	}
	if len(r.Entries) != len(Manifest()) {
		t.Errorf("entries=%d want %d", len(r.Entries), len(Manifest()))
	}
}

func TestMerge_MissingBlockingFragment_IsFail(t *testing.T) {
	frags := passAllBlocking()
	// Drop the docker cell fragment.
	var kept []Fragment
	for _, f := range frags {
		if f.Name != EntryMatrixDocker {
			kept = append(kept, f)
		}
	}
	r := Merge(kept)
	if r.Passed() {
		t.Fatal("verdict must fail when a blocking fragment is missing")
	}
	e := entryByName(t, r, EntryMatrixDocker)
	if e.Status != StatusFail || e.Reason != ReasonMissingFragment {
		t.Errorf("got status=%s reason=%q, want fail/%q", e.Status, e.Reason, ReasonMissingFragment)
	}
}

func TestMerge_FlakyBlockingEntry_StillPasses(t *testing.T) {
	frags := passAllBlocking()
	for i := range frags {
		if frags[i].Name == EntryMatrixSSE {
			frags[i].Status = StatusFlaky
			frags[i].Reason = "passed on retry 2"
			frags[i].Retries = 1
		}
	}
	r := Merge(frags)
	if !r.Passed() {
		t.Fatalf("flaky blocking entry must not fail the gate (FR-010): %v", r.BlockingFailures)
	}
	if r.Counts[StatusFlaky] != 1 {
		t.Errorf("flaky count=%d want 1", r.Counts[StatusFlaky])
	}
}

func TestMerge_SkippedAndNotRunBlockingEntries_Block(t *testing.T) {
	for _, status := range []Status{StatusSkipped, StatusNotRun, StatusFail, StatusAdvisoryFail} {
		frags := passAllBlocking()
		for i := range frags {
			if frags[i].Name == EntryMatrixOAuth {
				frags[i].Status = status
				frags[i].Reason = "test reason"
			}
		}
		r := Merge(frags)
		if r.Passed() {
			t.Errorf("status %s on a blocking entry must block the gate", status)
		}
	}
}

func TestMerge_AdvisoryFailOnNonBlockingReservedSlot_DoesNotBlock(t *testing.T) {
	frags := passAllBlocking()
	frags = append(frags, Fragment{
		Name:   EntryReservedMacOSSmoke,
		Status: StatusAdvisoryFail,
		Reason: "tray did not render",
	})
	r := Merge(frags)
	if !r.Passed() {
		t.Fatalf("advisory-fail on non-blocking entry must not block: %v", r.BlockingFailures)
	}
	if len(r.AdvisoryFailures) != 1 || !strings.Contains(r.AdvisoryFailures[0], EntryReservedMacOSSmoke) {
		t.Errorf("advisory failure must be prominently recorded, got %v", r.AdvisoryFailures)
	}
}

// Spec 081 T2: the Web UI sweep is wired as an ADVISORY entry — it is a real
// manifest entry (no longer a reserved "not-implemented-yet" slot), it never
// blocks the tag, and every non-green outcome (including a missing fragment,
// i.e. the job died before uploading) is still reported as an advisory failure.
func TestManifest_WebUISweep_IsAdvisoryNotReserved(t *testing.T) {
	var found bool
	for _, m := range Manifest() {
		if m.Name != EntryAdvisoryWebUISweep {
			continue
		}
		found = true
		if m.Blocking {
			t.Errorf("%s must not block the gate (advisory, T2)", m.Name)
		}
		if m.Reserved {
			t.Errorf("%s must no longer be a reserved slot — the sweep job exists", m.Name)
		}
	}
	if !found {
		t.Fatalf("manifest is missing the %s entry", EntryAdvisoryWebUISweep)
	}
}

func TestMerge_WebUISweepFailure_IsAdvisoryOnly(t *testing.T) {
	for _, status := range []Status{StatusFail, StatusAdvisoryFail} {
		frags := passAllBlocking()
		for i := range frags {
			if frags[i].Name == EntryAdvisoryWebUISweep {
				frags[i].Status = status
				frags[i].Reason = "sweep spec failed"
			}
		}
		r := Merge(frags)
		if !r.Passed() {
			t.Fatalf("web-ui sweep %s must not block the gate: %v", status, r.BlockingFailures)
		}
		if len(r.AdvisoryFailures) != 1 || !strings.Contains(r.AdvisoryFailures[0], EntryAdvisoryWebUISweep) {
			t.Errorf("web-ui sweep %s must be reported as an advisory failure, got %v", status, r.AdvisoryFailures)
		}
	}
}

func TestMerge_WebUISweepMissingFragment_IsAdvisoryFailureNotBlocking(t *testing.T) {
	var kept []Fragment
	for _, f := range passAllBlocking() {
		if f.Name != EntryAdvisoryWebUISweep {
			kept = append(kept, f)
		}
	}
	r := Merge(kept)
	if !r.Passed() {
		t.Fatalf("a missing web-ui sweep fragment must not block the gate: %v", r.BlockingFailures)
	}
	e := entryByName(t, r, EntryAdvisoryWebUISweep)
	if e.Status != StatusFail || e.Reason != ReasonMissingFragment {
		t.Errorf("got status=%s reason=%q, want fail/%q", e.Status, e.Reason, ReasonMissingFragment)
	}
	if len(r.AdvisoryFailures) != 1 || !strings.Contains(r.AdvisoryFailures[0], EntryAdvisoryWebUISweep) {
		t.Errorf("missing sweep fragment must surface as an advisory failure, got %v", r.AdvisoryFailures)
	}
}

func TestMerge_UnexpectedFailingFragment_Blocks(t *testing.T) {
	frags := append(passAllBlocking(), Fragment{Name: "rogue/check", Status: StatusFail, Reason: "boom"})
	r := Merge(frags)
	if r.Passed() {
		t.Fatal("unexpected failing fragment must block (fail-closed)")
	}
	e := entryByName(t, r, "rogue/check")
	if e.Expected {
		t.Error("rogue fragment must be marked unexpected")
	}
}

func TestMerge_DuplicateFragments_Fail(t *testing.T) {
	frags := append(passAllBlocking(), Fragment{Name: EntryMatrixStdio, Status: StatusPass})
	r := Merge(frags)
	if r.Passed() {
		t.Fatal("duplicate fragments for one entry must fail")
	}
	e := entryByName(t, r, EntryMatrixStdio)
	if !strings.Contains(e.Reason, "duplicate") {
		t.Errorf("reason=%q want duplicate mention", e.Reason)
	}
}

func TestWriteLoadFragments_RoundTripAndCorruption(t *testing.T) {
	dir := t.TempDir()
	frag := &Fragment{
		Name:   EntryMatrixHTTP,
		Status: StatusPass,
		Steps:  []Step{{Name: "ready", Status: StatusPass}},
	}
	if err := WriteFragment(dir, frag); err != nil {
		t.Fatal(err)
	}
	// Corrupted fragment must surface as a failed entry, not disappear.
	if err := os.WriteFile(filepath.Join(dir, "matrix-sse.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The merged report file itself must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "gate-report.json"), []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	frags, err := LoadFragments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(frags) != 2 {
		t.Fatalf("got %d fragments, want 2: %+v", len(frags), frags)
	}
	var sawHTTP, sawCorrupt bool
	for _, f := range frags {
		switch {
		case f.Name == EntryMatrixHTTP && f.Status == StatusPass:
			sawHTTP = true
		case f.Name == "matrix-sse" && f.Status == StatusFail:
			sawCorrupt = true
		}
	}
	if !sawHTTP || !sawCorrupt {
		t.Errorf("round-trip=%v corruption-surfaced=%v, want both true (%+v)", sawHTTP, sawCorrupt, frags)
	}
}

func TestLoadFragments_MissingDir_ReturnsEmpty(t *testing.T) {
	frags, err := LoadFragments(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil || frags != nil {
		t.Fatalf("missing dir: frags=%v err=%v, want nil/nil", frags, err)
	}
}

func TestMarkdown_ContainsVerdictAndRows(t *testing.T) {
	frags := passAllBlocking()
	for i := range frags {
		if frags[i].Name == EntryMatrixDocker {
			frags[i].Status = StatusFail
			frags[i].Reason = "docker info failed | daemon unreachable"
			frags[i].Classification = ClassificationInfrastructure
		}
	}
	r := Merge(frags)
	md := Markdown(r)
	for _, want := range []string{"# Release QA Gate", "FAIL", EntryMatrixDocker, "infrastructure", "Blocking failures", ReasonNotImplemented} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	// Pipes in reasons must not break the table.
	if strings.Contains(md, "failed | daemon") {
		t.Error("unescaped pipe in markdown table reason")
	}
}
