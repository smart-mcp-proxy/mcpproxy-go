package scanner_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/tpacorpus"
)

// This is an external test package because tpacorpus imports scanner. It still
// drives the PRODUCTION scan path: scanner.ScanToolMetadataVerdict is the exported
// entry the runtime tool-change gate uses, and it builds the same engine
// (baselineEngineChecks plus the active bundle check) as inProcessToolScan.

const sc004Dataset = "../../../specs/065-evaluation-foundation/datasets/detect_corpus_v1.json"

// TestSC004_CorpusBreadth: the embedded bundle has at least 25 gating
// signatures across at least 8 classes, every class in the vocabulary has a
// gating signature, and every signature carries provenance and an allowlisted
// licence. A non-gating signature cannot pad the count.
func TestSC004_CorpusBreadth(t *testing.T) {
	sigs, err := scanner.ParseBundleSignatures(scanner.EmbeddedBundle())
	if err != nil {
		t.Fatal(err)
	}
	gating := 0
	gatingByClass := map[string]int{}
	for _, s := range sigs {
		if s.Provenance.Source == "" || s.Provenance.Published == "" {
			t.Errorf("%s: missing provenance (source=%q published=%q)", s.ID, s.Provenance.Source, s.Provenance.Published)
		}
		allowed := false
		for _, l := range tpacorpus.LicenceAllowlist {
			if s.License == l {
				allowed = true
			}
		}
		if !allowed {
			t.Errorf("%s: licence %q is not on the allowlist %v", s.ID, s.License, tpacorpus.LicenceAllowlist)
		}
		if s.Gating {
			gating++
			gatingByClass[s.Category]++
		}
	}
	if gating < 25 {
		t.Errorf("gating signatures = %d, want >= 25", gating)
	}
	if len(gatingByClass) < 8 {
		t.Errorf("classes with a gating signature = %d, want >= 8", len(gatingByClass))
	}
	for _, class := range tpacorpus.Classes {
		if gatingByClass[class] < 1 {
			t.Errorf("class %s has no gating signature", class)
		}
	}
}

// TestSC004_CanonicalSampleHardFlaggedBySignature: for every class, its
// canonical dataset entry, scanned through the production in-process path,
// yields a dangerous finding whose signals include the prefix
// tpa.<that entry's tpa_id>. . A hard finding reached only through another
// check (for example phrase.injection) does not count.
func TestSC004_CanonicalSampleHardFlaggedBySignature(t *testing.T) {
	data, err := os.ReadFile(sc004Dataset)
	if err != nil {
		t.Fatal(err)
	}
	var ds struct {
		Entries []struct {
			ID        string `json:"id"`
			Label     string `json:"label"`
			Category  string `json:"category"`
			TPAID     string `json:"tpa_id"`
			Canonical bool   `json:"canonical"`
			Server    string `json:"server"`
			Tool      struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"input_schema"`
			} `json:"tool"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}

	canonical := map[string]int{}
	for _, e := range ds.Entries {
		if !e.Canonical {
			continue
		}
		canonical[e.Category]++
		if e.TPAID == "" {
			t.Errorf("canonical entry %s has no tpa_id", e.ID)
			continue
		}
		tool := &config.ToolMetadata{Name: e.Tool.Name, ServerName: e.Server, Description: e.Tool.Description}
		if len(e.Tool.InputSchema) > 0 {
			tool.ParamsJSON = string(e.Tool.InputSchema)
		}
		verdict, findings, coverageOK := scanner.ScanToolMetadataVerdict(e.Server, []*config.ToolMetadata{tool}, nil)
		if !coverageOK {
			t.Errorf("%s: scan coverage degraded (bundle unavailable?)", e.ID)
		}
		prefix := "tpa." + e.TPAID + "."
		hit := false
		for _, f := range findings {
			if f.ThreatLevel != scanner.ThreatLevelDangerous {
				continue
			}
			for _, sig := range f.Signals {
				if strings.HasPrefix(sig, prefix) {
					hit = true
				}
			}
		}
		if !hit {
			t.Errorf("%s (%s): no dangerous finding with a %s signal (verdict=%s findings=%+v)", e.ID, e.Category, prefix, verdict, findings)
		}
	}
	for _, class := range tpacorpus.Classes {
		if canonical[class] != 1 {
			t.Errorf("class %s has %d canonical dataset entries, want exactly 1", class, canonical[class])
		}
	}
}
