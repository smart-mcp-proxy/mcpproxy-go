package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/gatereport"
)

func readFragment(t *testing.T, dir, name string) gatereport.Fragment {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, gatereport.FragmentFileName(name)))
	if err != nil {
		t.Fatalf("read fragment %s: %v", name, err)
	}
	var frag gatereport.Fragment
	if err := json.Unmarshal(data, &frag); err != nil {
		t.Fatalf("parse fragment %s: %v", name, err)
	}
	return frag
}

// A failing advisory suite must be recorded as advisory-fail (never plain
// fail), so the merged report reads honestly: the check ran, it went red, and
// it did not block the tag (Spec 081 T2/FR-019).
func TestRunSuite_AdvisoryFailure_RecordsAdvisoryFail(t *testing.T) {
	dir := t.TempDir()
	ok, err := runSuite(context.Background(), gatereport.EntryAdvisoryWebUISweep, dir, true,
		[]string{"sh", "-c", "exit 3"})
	if err != nil {
		t.Fatalf("runSuite returned an error: %v", err)
	}
	if ok {
		t.Error("a failing suite must report ok=false even when advisory (the job goes red under continue-on-error)")
	}
	frag := readFragment(t, dir, gatereport.EntryAdvisoryWebUISweep)
	if frag.Status != gatereport.StatusAdvisoryFail {
		t.Errorf("status=%s want %s", frag.Status, gatereport.StatusAdvisoryFail)
	}
	if frag.Reason == "" {
		t.Error("a non-pass fragment must carry a reason (FR-004)")
	}
}

func TestRunSuite_AdvisorySuccess_RecordsPass(t *testing.T) {
	dir := t.TempDir()
	ok, err := runSuite(context.Background(), gatereport.EntryAdvisoryWebUISweep, dir, true,
		[]string{"sh", "-c", "exit 0"})
	if err != nil || !ok {
		t.Fatalf("runSuite ok=%v err=%v, want true/nil", ok, err)
	}
	if frag := readFragment(t, dir, gatereport.EntryAdvisoryWebUISweep); frag.Status != gatereport.StatusPass {
		t.Errorf("status=%s want %s", frag.Status, gatereport.StatusPass)
	}
}

// Blocking suites keep the original behaviour: a failure is a plain fail.
func TestRunSuite_BlockingFailure_RecordsFail(t *testing.T) {
	dir := t.TempDir()
	if _, err := runSuite(context.Background(), gatereport.EntrySuiteAPIE2E, dir, false,
		[]string{"sh", "-c", "exit 1"}); err != nil {
		t.Fatalf("runSuite returned an error: %v", err)
	}
	if frag := readFragment(t, dir, gatereport.EntrySuiteAPIE2E); frag.Status != gatereport.StatusFail {
		t.Errorf("status=%s want %s", frag.Status, gatereport.StatusFail)
	}
}
