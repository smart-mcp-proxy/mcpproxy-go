package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/tpacorpus"
)

const committedDetectCorpus = "../../specs/065-evaluation-foundation/datasets/detect_corpus_v1.json"

// runGateJSON runs the gate over the committed corpus with extra args and
// returns the exit code, the decoded metrics (when stdout held any) and stderr.
func runGateJSON(t *testing.T, extra ...string) (int, map[string]any, string) {
	t.Helper()
	args := append([]string{"--corpus", committedDetectCorpus, "--gate", "--min-recall", "0.90", "--max-fp", "0.05"}, extra...)
	var out, errBuf bytes.Buffer
	code := run(args, &out, &errBuf)
	var m map[string]any
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &m); err != nil {
			t.Fatalf("stdout is not metrics JSON: %v\n%s", err, out.String())
		}
	}
	return code, m, errBuf.String()
}

func TestBundleGate_DefaultScoresEmbeddedBundle(t *testing.T) {
	code, m, stderr := runGateJSON(t)
	if code != exitOK {
		t.Fatalf("default gate exit=%d stderr=%s", code, stderr)
	}
	found := false
	for _, id := range m["checks"].([]any) {
		if id == "tpa.bundle" {
			found = true
		}
	}
	if !found {
		t.Errorf("checks %v do not list tpa.bundle", m["checks"])
	}
	b, ok := m["bundle"].(map[string]any)
	if !ok {
		t.Fatalf("metrics have no bundle block: %v", m)
	}
	if fp, _ := b["fingerprint"].(string); fp == "" {
		t.Errorf("bundle.fingerprint empty: %v", b)
	}
	if v, _ := b["bundle_version"].(string); v == "" {
		t.Errorf("bundle.bundle_version empty: %v", b)
	}
	if n, _ := b["runnable_rules"].(float64); n < 1 {
		t.Errorf("bundle.runnable_rules = %v, want >= 1", b["runnable_rules"])
	}
	sigs, err := scanner.ParseBundleSignatures(scanner.EmbeddedBundle())
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := b["signature_count"].(float64); int(n) != len(sigs) {
		t.Errorf("bundle.signature_count = %v, want %d", b["signature_count"], len(sigs))
	}
}

func TestBundleGate_MissingBundleIsConfigError(t *testing.T) {
	code, m, stderr := runGateJSON(t, "--bundle", filepath.Join(t.TempDir(), "nope.json"))
	if code != exitConfigError {
		t.Fatalf("exit=%d want %d", code, exitConfigError)
	}
	if m != nil {
		t.Errorf("a failed bundle load must not emit metrics: %v", m)
	}
	if !strings.Contains(stderr, "nope.json") {
		t.Errorf("stderr should name the bundle path: %q", stderr)
	}
}

func TestBundleGate_InvalidBundleNeverFallsBack(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"unsupported_version": `{"bundle_version":"99.0.0","rules":[]}`,
		"no_runnable_rules":   `{"bundle_version":"0.1.0","rules":[]}`,
		"not_json":            `not json`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name+".json")
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			code, m, stderr := runGateJSON(t, "--bundle", p)
			if code != exitConfigError {
				t.Fatalf("exit=%d want %d (stderr=%s)", code, exitConfigError, stderr)
			}
			if m != nil {
				t.Errorf("an invalid bundle must not fall back to the embedded default and score: %v", m)
			}
			if !strings.Contains(stderr, p) {
				t.Errorf("stderr should name the bundle path: %q", stderr)
			}
		})
	}
}

func TestBundleGate_ExplicitEmbeddedCopyMatchesDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "copy.json")
	if err := os.WriteFile(p, scanner.EmbeddedBundle(), 0o644); err != nil {
		t.Fatal(err)
	}
	dcode, dm, _ := runGateJSON(t)
	ccode, cm, stderr := runGateJSON(t, "--bundle", p)
	if ccode != dcode {
		t.Fatalf("copy exit=%d default exit=%d stderr=%s", ccode, dcode, stderr)
	}
	for _, k := range []string{"overall_recall", "fp_rate", "checks", "categories", "bundle"} {
		a, _ := json.Marshal(dm[k])
		b, _ := json.Marshal(cm[k])
		if string(a) != string(b) {
			t.Errorf("%s differs: default=%s copy=%s", k, a, b)
		}
	}
}

// TestCategoryCheck_CoversCorpusClasses: every signature class (the vocabulary
// every dataset entry and signature draws from) and every gating signature
// category in the embedded bundle maps to the bundle check. A class missing here
// would be reported but never gated, so a regression in it could not fail CI.
func TestCategoryCheck_CoversCorpusClasses(t *testing.T) {
	for _, class := range tpacorpus.Classes {
		if got := categoryCheck[class]; got != "tpa.bundle" {
			t.Errorf("categoryCheck[%q] = %q, want %q", class, got, "tpa.bundle")
		}
	}
	sigs, err := scanner.ParseBundleSignatures(scanner.EmbeddedBundle())
	if err != nil {
		t.Fatalf("ParseBundleSignatures: %v", err)
	}
	for _, s := range sigs {
		if !s.Gating {
			continue
		}
		if got := categoryCheck[s.Category]; got != "tpa.bundle" {
			t.Errorf("gating signature %s: categoryCheck[%q] = %q, want %q", s.ID, s.Category, got, "tpa.bundle")
		}
	}
}
