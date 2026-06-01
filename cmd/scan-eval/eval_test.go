package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

const minCorpus = "testdata/security_corpus_min.json"

func findEntry(t *testing.T, r *verdictReport, id string) verdictEntry {
	t.Helper()
	for _, e := range r.Entries {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("entry %q not found in report", id)
	return verdictEntry{}
}

// sensitiveDataVerdict returns the single sensitive-data verdict for an entry.
func sensitiveDataVerdict(t *testing.T, e verdictEntry) detectorVerdict {
	t.Helper()
	for _, v := range e.Verdicts {
		if v.Detector == detectorSensitiveData {
			return v
		}
	}
	t.Fatalf("entry %q has no %q verdict", e.ID, detectorSensitiveData)
	return detectorVerdict{}
}

// TestEvaluate_SchemaShape — TDD #1: evaluate() over the fixture echoes
// id/label/category and emits one sensitive-data verdict per entry.
func TestEvaluate_SchemaShape(t *testing.T) {
	c, err := loadCorpus(minCorpus)
	if err != nil {
		t.Fatalf("loadCorpus: %v", err)
	}

	report := evaluate(c, security.NewDetector(nil))

	if report.CorpusVersion != "test-min-v1" {
		t.Errorf("corpus_version = %q, want %q", report.CorpusVersion, "test-min-v1")
	}
	if len(report.Detectors) != 1 || report.Detectors[0] != detectorSensitiveData {
		t.Errorf("detectors = %v, want [%q]", report.Detectors, detectorSensitiveData)
	}
	if len(report.Entries) != len(c.Entries) {
		t.Fatalf("entries = %d, want %d", len(report.Entries), len(c.Entries))
	}
	for i, e := range report.Entries {
		src := c.Entries[i]
		if e.ID != src.ID || e.Label != src.Label || e.Category != src.Category {
			t.Errorf("entry %d ground truth not echoed: got (%q,%q,%q) want (%q,%q,%q)",
				i, e.ID, e.Label, e.Category, src.ID, src.Label, src.Category)
		}
		v := sensitiveDataVerdict(t, e)
		if v.Detections == nil {
			t.Errorf("entry %q: detections must be non-nil (B3 contract requires the array)", e.ID)
		}
	}
}

// TestEvaluate_TruePositive — TDD #2 / INV-3 positive: a malicious entry whose
// description embeds an AWS key flags critical.
func TestEvaluate_TruePositive(t *testing.T) {
	c, err := loadCorpus(minCorpus)
	if err != nil {
		t.Fatalf("loadCorpus: %v", err)
	}
	report := evaluate(c, security.NewDetector(nil))

	v := sensitiveDataVerdict(t, findEntry(t, report, "tp-aws-key-001"))
	if !v.Flagged {
		t.Fatalf("tp-aws-key-001: flagged = false, want true (TP)")
	}
	if v.MaxSeverity != "critical" {
		t.Errorf("tp-aws-key-001: max_severity = %q, want %q", v.MaxSeverity, "critical")
	}
	found := false
	for _, d := range v.Detections {
		if d.Type == "aws_access_key" {
			found = true
		}
	}
	if !found {
		t.Errorf("tp-aws-key-001: expected an aws_access_key detection, got %+v", v.Detections)
	}
}

// TestEvaluate_TrueNegative — TDD #3 / INV-3 negative: a plain benign
// description is not flagged (no false positive).
func TestEvaluate_TrueNegative(t *testing.T) {
	c, err := loadCorpus(minCorpus)
	if err != nil {
		t.Fatalf("loadCorpus: %v", err)
	}
	report := evaluate(c, security.NewDetector(nil))

	v := sensitiveDataVerdict(t, findEntry(t, report, "benign-weather-001"))
	if v.Flagged {
		t.Errorf("benign-weather-001: flagged = true, want false (TN). detections=%+v", v.Detections)
	}
	if v.MaxSeverity != "" {
		t.Errorf("benign-weather-001: max_severity = %q, want empty", v.MaxSeverity)
	}
	if len(v.Detections) != 0 {
		t.Errorf("benign-weather-001: detections = %+v, want none", v.Detections)
	}
}

// TestRun_MissingCorpus — TDD #4: bad/missing corpus and missing flag both
// exit 4 (config error, matching repo convention).
func TestRun_MissingCorpus(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no --corpus flag", []string{}},
		{"nonexistent file", []string{"--corpus", filepath.Join(t.TempDir(), "nope.json")}},
		{"unparsable flag", []string{"--corpus", minCorpus, "--bogus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			if code := run(tc.args, &out, &errBuf); code != exitConfigError {
				t.Errorf("run(%v) = %d, want %d. stderr=%q", tc.args, code, exitConfigError, errBuf.String())
			}
		})
	}
}

// TestRun_EmptyCorpus — an entries-less corpus is a config error.
func TestRun_EmptyCorpus(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(p, []byte(`{"entries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	if code := run([]string{"--corpus", p}, &out, &errBuf); code != exitConfigError {
		t.Errorf("run(empty corpus) = %d, want %d", code, exitConfigError)
	}
}

// TestRun_Deterministic — TDD #5 / INV-5 spirit: two runs over an unchanged
// corpus produce byte-identical, schema-parseable verdict JSON.
func TestRun_Deterministic(t *testing.T) {
	var a, b bytes.Buffer
	if code := run([]string{"--corpus", minCorpus}, &a, &bytes.Buffer{}); code != exitOK {
		t.Fatalf("run #1 = %d, want %d", code, exitOK)
	}
	if code := run([]string{"--corpus", minCorpus}, &b, &bytes.Buffer{}); code != exitOK {
		t.Fatalf("run #2 = %d, want %d", code, exitOK)
	}
	if a.String() != b.String() {
		t.Errorf("non-deterministic output across runs")
	}
	var report verdictReport
	if err := json.Unmarshal(a.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not valid verdict JSON: %v", err)
	}
	if len(report.Entries) != 4 {
		t.Errorf("entries = %d, want 4", len(report.Entries))
	}
}

// TestRun_WritesToFile — --out writes the same bytes it would print to stdout.
func TestRun_WritesToFile(t *testing.T) {
	var stdoutBuf bytes.Buffer
	if code := run([]string{"--corpus", minCorpus}, &stdoutBuf, &bytes.Buffer{}); code != exitOK {
		t.Fatalf("stdout run = %d", code)
	}

	outPath := filepath.Join(t.TempDir(), "verdict.json")
	if code := run([]string{"--corpus", minCorpus, "--out", outPath}, &bytes.Buffer{}, &bytes.Buffer{}); code != exitOK {
		t.Fatalf("file run = %d", code)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading --out file: %v", err)
	}
	if string(got) != stdoutBuf.String() {
		t.Errorf("--out file differs from stdout output")
	}
}
